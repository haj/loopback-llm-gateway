//go:build !windows

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSyslogConn records Info messages in order.
type fakeSyslogConn struct {
	messages []string
	failNext bool
	closed   bool
}

func (c *fakeSyslogConn) Info(m string) error {
	if c.failNext {
		c.failNext = false
		return fmt.Errorf("injected syslog failure")
	}
	c.messages = append(c.messages, m)
	return nil
}

func (c *fakeSyslogConn) Close() error {
	c.closed = true
	return nil
}

// withFakeSyslog swaps dialSyslog for the test and restores it afterwards.
func withFakeSyslog(t *testing.T, conn *fakeSyslogConn, dialErr error) (gotNetwork, gotAddress, gotTag *string) {
	t.Helper()
	var network, address, tag string
	original := dialSyslog
	dialSyslog = func(n, a, tg string) (syslogConn, error) {
		network, address, tag = n, a, tg
		if dialErr != nil {
			return nil, dialErr
		}
		return conn, nil
	}
	t.Cleanup(func() { dialSyslog = original })
	return &network, &address, &tag
}

func TestSyslogDestination_WritesOneMessagePerEvent(t *testing.T) {
	conn := &fakeSyslogConn{}
	network, address, tag := withFakeSyslog(t, conn, nil)

	dest, err := newSyslogDestination("udp", "logs.example.com:514", "custom-tag")
	require.NoError(t, err)
	assert.Equal(t, "syslog", dest.Name())
	assert.Equal(t, "udp", *network)
	assert.Equal(t, "logs.example.com:514", *address)
	assert.Equal(t, "custom-tag", *tag)

	events := []configstoreTables.TableAuditLog{testAuditEvent(0), testAuditEvent(1)}
	require.NoError(t, dest.Write(context.Background(), events))
	require.Len(t, conn.messages, 2)

	// Each message is one JSON document carrying the signature, so downstream
	// collectors archive independently verifiable records.
	for i, msg := range conn.messages {
		var row configstoreTables.TableAuditLog
		require.NoError(t, json.Unmarshal([]byte(msg), &row))
		assert.Equal(t, events[i].ID, row.ID)
		assert.True(t, row.VerifySignature(auditExportTestKey))
	}

	require.NoError(t, dest.Close())
	assert.True(t, conn.closed)
}

func TestSyslogDestination_DefaultsTag(t *testing.T) {
	conn := &fakeSyslogConn{}
	_, _, tag := withFakeSyslog(t, conn, nil)

	_, err := newSyslogDestination("", "", "")
	require.NoError(t, err)
	assert.Equal(t, defaultAuditSyslogTag, *tag)
}

func TestSyslogDestination_RequiresAddressForRemoteNetworks(t *testing.T) {
	_, err := newSyslogDestination("udp", "", "tag")
	assert.Error(t, err)
	_, err = newSyslogDestination("tcp", "", "tag")
	assert.Error(t, err)
}

func TestSyslogDestination_PropagatesDialError(t *testing.T) {
	withFakeSyslog(t, nil, fmt.Errorf("no syslog daemon"))
	_, err := newSyslogDestination("tcp", "localhost:514", "tag")
	assert.ErrorContains(t, err, "failed to connect to syslog")
}

func TestSyslogDestination_PropagatesWriteError(t *testing.T) {
	conn := &fakeSyslogConn{failNext: true}
	withFakeSyslog(t, conn, nil)

	dest, err := newSyslogDestination("", "", "")
	require.NoError(t, err)
	assert.Error(t, dest.Write(context.Background(), []configstoreTables.TableAuditLog{testAuditEvent(0)}))
}
