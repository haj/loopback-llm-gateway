package governance

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// violationResult builds a deny-shaped EvaluationResult with an attached VK.
func violationResult(decision Decision, reason string) *EvaluationResult {
	return &EvaluationResult{
		Decision: decision,
		Reason:   reason,
		VirtualKey: &configstoreTables.TableVirtualKey{
			ID:   "vk-123",
			Name: "team-a-key",
		},
	}
}

func violationRequest() *EvaluationRequest {
	return &EvaluationRequest{
		VirtualKey: "vk-value",
		Provider:   schemas.ModelProvider("openai"),
		Model:      "gpt-4o",
	}
}

// waitViolation receives one ViolationInfo or fails the test.
func waitViolation(t *testing.T, ch chan ViolationInfo) ViolationInfo {
	t.Helper()
	select {
	case info := <-ch:
		return info
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for violation callback")
		return ViolationInfo{}
	}
}

func TestViolationListener_FiresForBudgetExceeded(t *testing.T) {
	p := &GovernancePlugin{}
	ch := make(chan ViolationInfo, 1)
	p.SetViolationListener(func(info ViolationInfo) { ch <- info })

	p.notifyViolation(violationResult(DecisionBudgetExceeded, "monthly budget exhausted"), violationRequest())

	info := waitViolation(t, ch)
	assert.Equal(t, DecisionBudgetExceeded, info.Decision)
	assert.Equal(t, "monthly budget exhausted", info.Reason)
	assert.Equal(t, "vk-123", info.VirtualKeyID)
	assert.Equal(t, "team-a-key", info.VirtualKeyName)
	assert.Equal(t, schemas.ModelProvider("openai"), info.Provider)
	assert.Equal(t, "gpt-4o", info.Model)
}

func TestViolationListener_FiresForRateLimited(t *testing.T) {
	p := &GovernancePlugin{}
	ch := make(chan ViolationInfo, 1)
	p.SetViolationListener(func(info ViolationInfo) { ch <- info })

	p.notifyViolation(violationResult(DecisionRateLimited, "requests per minute exceeded"), violationRequest())

	info := waitViolation(t, ch)
	assert.Equal(t, DecisionRateLimited, info.Decision)
	assert.Equal(t, "requests per minute exceeded", info.Reason)
}

func TestViolationListener_NilSafety(t *testing.T) {
	// No listener installed: notify is a silent no-op.
	p := &GovernancePlugin{}
	p.notifyViolation(violationResult(DecisionBudgetExceeded, "x"), violationRequest())

	// Removing the listener restores the no-op state.
	ch := make(chan ViolationInfo, 1)
	p.SetViolationListener(func(info ViolationInfo) { ch <- info })
	p.SetViolationListener(nil)
	p.notifyViolation(violationResult(DecisionBudgetExceeded, "x"), violationRequest())
	select {
	case <-ch:
		t.Fatal("removed listener must not receive violations")
	case <-time.After(50 * time.Millisecond):
	}

	// Nil result / request / receiver never panic.
	p.notifyViolation(nil, nil)
	var nilPlugin *GovernancePlugin
	nilPlugin.SetViolationListener(func(ViolationInfo) {})
	nilPlugin.notifyViolation(violationResult(DecisionRateLimited, "x"), nil)

	// Missing VK and request leave those fields empty without panicking.
	p.SetViolationListener(func(info ViolationInfo) { ch <- info })
	p.notifyViolation(&EvaluationResult{Decision: DecisionRequestLimited, Reason: "r"}, nil)
	info := waitViolation(t, ch)
	require.Empty(t, info.VirtualKeyID)
	require.Empty(t, info.Model)
	assert.Equal(t, DecisionRequestLimited, info.Decision)
}
