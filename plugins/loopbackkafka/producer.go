package loopbackkafka

import (
	"context"
	"crypto/tls"
	"sync"
	"sync/atomic"
	"time"

	kafka "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
)

// messageWriter is the minimal Kafka writer surface the producer depends on.
// *kafka.Writer satisfies it; tests substitute a fake to exercise the batching,
// backpressure, and retry logic without a live broker.
type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// producerConfig is the resolved (defaults applied) transport configuration.
type producerConfig struct {
	brokers        []string
	topic          string
	batchSize      int
	batchTimeoutMs int
	queueSize      int
	maxRetries     int
	retryBackoffMs int
	requiredAcks   kafka.RequiredAcks
	saslUsername   string
	saslPassword   string
}

// newKafkaWriter builds the real *kafka.Writer transport. Note that kafka-go's
// own batching is disabled (BatchSize: 1) because batching, backpressure, and
// retries are owned by the producer below — this keeps the connector's delivery
// semantics identical regardless of the underlying client.
func newKafkaWriter(cfg producerConfig) (*kafka.Writer, error) {
	transport := &kafka.Transport{}
	if cfg.saslUsername != "" && cfg.saslPassword != "" {
		var mech sasl.Mechanism = plain.Mechanism{Username: cfg.saslUsername, Password: cfg.saslPassword}
		transport.SASL = mech
		transport.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	w := &kafka.Writer{
		Addr:         kafka.TCP(cfg.brokers...),
		Topic:        cfg.topic,
		Balancer:     &kafka.Hash{}, // route by key so a session/request lands on one partition
		RequiredAcks: cfg.requiredAcks,
		BatchSize:    1, // producer below owns batching
		Async:        false,
		Transport:    transport,
	}
	return w, nil
}

// producer is the transport layer: an async, batched, retrying writer fronted by
// a bounded queue. Inject hands messages to enqueue (non-blocking); a single
// background goroutine accumulates them into size/time-bounded batches and writes
// each batch with bounded exponential backoff.
type producer struct {
	writer       messageWriter
	queue        chan kafka.Message
	batchSize    int
	batchTimeout time.Duration
	maxRetries   int
	retryBackoff time.Duration

	wg        sync.WaitGroup
	done      chan struct{}
	closeOnce sync.Once
	dropped   atomic.Uint64
}

// newProducer starts the background flush goroutine and returns the producer.
func newProducer(w messageWriter, cfg producerConfig) *producer {
	p := &producer{
		writer:       w,
		queue:        make(chan kafka.Message, cfg.queueSize),
		batchSize:    cfg.batchSize,
		batchTimeout: time.Duration(cfg.batchTimeoutMs) * time.Millisecond,
		maxRetries:   cfg.maxRetries,
		retryBackoff: time.Duration(cfg.retryBackoffMs) * time.Millisecond,
		done:         make(chan struct{}),
	}
	p.wg.Add(1)
	go p.run()
	return p
}

// enqueue offers a message to the bounded queue without blocking. When the queue
// is full (the downstream broker can't keep up) the message is dropped and the
// dropped counter incremented — observability must never apply backpressure to
// the request-completion path.
func (p *producer) enqueue(msg kafka.Message) {
	select {
	case p.queue <- msg:
	default:
		n := p.dropped.Add(1)
		// Log on the first drop and then every 1000th to avoid log floods.
		if logger != nil && (n == 1 || n%1000 == 0) {
			logger.Warn("[loopback-kafka] queue full, dropped %d telemetry event(s)", n)
		}
	}
}

// Dropped reports the number of events dropped due to a full queue.
func (p *producer) Dropped() uint64 { return p.dropped.Load() }

// run is the single batching goroutine. It flushes a batch when it reaches
// batchSize or when batchTimeout elapses with a non-empty batch.
func (p *producer) run() {
	defer p.wg.Done()
	batch := make([]kafka.Message, 0, p.batchSize)
	timer := time.NewTimer(p.batchTimeout)
	defer timer.Stop()

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(p.batchTimeout)
	}

	for {
		select {
		case msg := <-p.queue:
			batch = append(batch, msg)
			if len(batch) >= p.batchSize {
				p.flush(batch)
				batch = batch[:0]
				resetTimer()
			}
		case <-timer.C:
			if len(batch) > 0 {
				p.flush(batch)
				batch = batch[:0]
			}
			timer.Reset(p.batchTimeout)
		case <-p.done:
			// Drain whatever is queued, then flush the final partial batch.
			for {
				select {
				case msg := <-p.queue:
					batch = append(batch, msg)
					if len(batch) >= p.batchSize {
						p.flush(batch)
						batch = batch[:0]
					}
				default:
					if len(batch) > 0 {
						p.flush(batch)
					}
					return
				}
			}
		}
	}
}

// flush writes a batch with bounded exponential backoff. A write that still fails
// after maxRetries is dropped (counted) rather than retried forever — the buffer
// is bounded and the request path has long since completed.
func (p *producer) flush(batch []kafka.Message) {
	if len(batch) == 0 {
		return
	}
	// Copy: the caller reuses the backing array after flush returns.
	msgs := make([]kafka.Message, len(batch))
	copy(msgs, batch)

	backoff := p.retryBackoff
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := p.writer.WriteMessages(ctx, msgs...)
		cancel()
		if err == nil {
			if logger != nil {
				logger.Debug("[loopback-kafka] published %d telemetry event(s)", len(msgs))
			}
			return
		}
		if attempt == p.maxRetries {
			n := p.dropped.Add(uint64(len(msgs)))
			if logger != nil {
				logger.Error("[loopback-kafka] dropping %d event(s) after %d attempts: %v (total dropped %d)", len(msgs), attempt+1, err, n)
			}
			return
		}
		if logger != nil {
			logger.Warn("[loopback-kafka] write attempt %d failed: %v; retrying in %s", attempt+1, err, backoff)
		}
		p.sleep(backoff)
		backoff *= 2
	}
}

// sleep waits for d, returning early if the producer is closing.
func (p *producer) sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-p.done:
	}
}

// close stops the goroutine (after a final drain/flush) and closes the writer.
// Safe to call more than once.
func (p *producer) close() error {
	p.closeOnce.Do(func() {
		close(p.done)
		p.wg.Wait()
	})
	if p.writer != nil {
		return p.writer.Close()
	}
	return nil
}

// requiredAcksOrDefault maps the config string to a kafka.RequiredAcks value.
func requiredAcksOrDefault(s string) kafka.RequiredAcks {
	switch s {
	case "none":
		return kafka.RequireNone
	case "all":
		return kafka.RequireAll
	case "one", "":
		return kafka.RequireOne
	default:
		return kafka.RequireOne
	}
}
