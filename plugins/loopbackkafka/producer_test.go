package loopbackkafka

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

// fakeWriter is an in-memory messageWriter used to drive the producer's batching,
// backpressure, and retry logic without a live broker.
type fakeWriter struct {
	mu       sync.Mutex
	batches  [][]kafka.Message
	total    int
	failN    int // fail the first failN write attempts, then succeed
	attempts int
	blockCh  chan struct{} // when non-nil, WriteMessages blocks until closed
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	if w.blockCh != nil {
		<-w.blockCh
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.attempts++
	if w.attempts <= w.failN {
		return errors.New("simulated write failure")
	}
	cp := make([]kafka.Message, len(msgs))
	copy(cp, msgs)
	w.batches = append(w.batches, cp)
	w.total += len(msgs)
	return nil
}

func (w *fakeWriter) Close() error { return nil }

func (w *fakeWriter) totals() (batches, msgs, attempts int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.batches), w.total, w.attempts
}

func baseCfg() producerConfig {
	return producerConfig{
		batchSize:      3,
		batchTimeoutMs: 50,
		queueSize:      100,
		maxRetries:     3,
		retryBackoffMs: 1,
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestProducerFlushesFullBatch(t *testing.T) {
	w := &fakeWriter{}
	p := newProducer(w, baseCfg())
	defer p.close()

	for i := 0; i < 3; i++ {
		p.enqueue(kafka.Message{Value: []byte("x")})
	}
	waitFor(t, func() bool { _, msgs, _ := w.totals(); return msgs == 3 })
	batches, msgs, _ := w.totals()
	if batches != 1 || msgs != 3 {
		t.Fatalf("expected 1 batch of 3, got %d batches / %d msgs", batches, msgs)
	}
}

func TestProducerFlushesPartialBatchOnTimeout(t *testing.T) {
	w := &fakeWriter{}
	p := newProducer(w, baseCfg())
	defer p.close()

	p.enqueue(kafka.Message{Value: []byte("x")})
	// fewer than batchSize, so only the timeout should trigger a flush
	waitFor(t, func() bool { _, msgs, _ := w.totals(); return msgs == 1 })
}

func TestProducerFlushesRemainderOnClose(t *testing.T) {
	w := &fakeWriter{}
	cfg := baseCfg()
	cfg.batchTimeoutMs = 10000 // long timeout so close(), not the timer, flushes
	p := newProducer(w, cfg)

	p.enqueue(kafka.Message{Value: []byte("a")})
	p.enqueue(kafka.Message{Value: []byte("b")})
	if err := p.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, msgs, _ := w.totals()
	if msgs != 2 {
		t.Fatalf("expected 2 msgs flushed on close, got %d", msgs)
	}
}

func TestProducerRetriesThenSucceeds(t *testing.T) {
	w := &fakeWriter{failN: 2} // first 2 attempts fail, 3rd succeeds
	p := newProducer(w, baseCfg())
	defer p.close()

	for i := 0; i < 3; i++ {
		p.enqueue(kafka.Message{Value: []byte("x")})
	}
	waitFor(t, func() bool { _, msgs, _ := w.totals(); return msgs == 3 })
	_, _, attempts := w.totals()
	if attempts != 3 {
		t.Fatalf("expected 3 write attempts (2 fail + 1 ok), got %d", attempts)
	}
	if d := p.Dropped(); d != 0 {
		t.Fatalf("expected 0 dropped, got %d", d)
	}
}

func TestProducerDropsAfterMaxRetries(t *testing.T) {
	w := &fakeWriter{failN: 100} // always fail
	cfg := baseCfg()
	cfg.maxRetries = 2
	p := newProducer(w, cfg)
	defer p.close()

	for i := 0; i < 3; i++ {
		p.enqueue(kafka.Message{Value: []byte("x")})
	}
	// maxRetries=2 => 3 attempts total, then the batch of 3 is dropped.
	waitFor(t, func() bool { return p.Dropped() == 3 })
	_, _, attempts := w.totals()
	if attempts != 3 {
		t.Fatalf("expected 3 attempts before drop, got %d", attempts)
	}
}

func TestProducerBackpressureDropsWhenQueueFull(t *testing.T) {
	block := make(chan struct{})
	w := &fakeWriter{blockCh: block} // first write blocks, stalling the drain goroutine
	cfg := baseCfg()
	cfg.queueSize = 4
	cfg.batchSize = 1 // first enqueue triggers an immediate (blocking) flush
	p := newProducer(w, cfg)

	// First message is picked up and the writer blocks on it. Subsequent messages
	// fill the bounded queue; once full, further enqueues are dropped.
	for i := 0; i < 100; i++ {
		p.enqueue(kafka.Message{Value: []byte("x")})
	}
	waitFor(t, func() bool { return p.Dropped() > 0 })

	close(block) // unblock the writer so close() can complete
	_ = p.close()
}
