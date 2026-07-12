// Request-aware guardrails inspect the BifrostRequest itself (model, request
// type) rather than the message text. Ported from Portkey's MIT modelWhitelist
// and allowedRequestTypes guardrails.

package loopbackguard

import (
	"fmt"

	"github.com/maximhq/bifrost/core/schemas"
)

// RequestGuardrail inspects the whole request and returns a Verdict.
// Pass == false means the request should be blocked.
type RequestGuardrail interface {
	Name() string
	CheckRequest(req *schemas.BifrostRequest) Verdict
}

// ---- modelWhitelist ----

// ModelWhitelist passes when the requested model is in Models (or, with Not,
// when it is not).
type ModelWhitelist struct {
	Models []string
	Not    bool
}

func (m ModelWhitelist) Name() string { return "modelWhitelist" }

func (m ModelWhitelist) CheckRequest(req *schemas.BifrostRequest) Verdict {
	_, model, _ := req.GetRequestFields()
	inList := false
	for _, allowed := range m.Models {
		if allowed == model {
			inList = true
			break
		}
	}
	pass := inList
	if m.Not {
		pass = !inList
	}
	return Verdict{Pass: pass, Explanation: fmt.Sprintf("modelWhitelist (not=%t): model=%q allowed=%v", m.Not, model, m.Models)}
}

// ---- allowedRequestTypes ----

// AllowedRequestTypes passes when the request's type is allowed and not blocked.
// If Allowed is empty, all types are allowed except those in Blocked. Values are
// schemas.RequestType (e.g. "chat", "embedding").
type AllowedRequestTypes struct {
	Allowed []schemas.RequestType
	Blocked []schemas.RequestType
}

func (a AllowedRequestTypes) Name() string { return "allowedRequestTypes" }

func (a AllowedRequestTypes) CheckRequest(req *schemas.BifrostRequest) Verdict {
	rt := req.RequestType
	for _, b := range a.Blocked {
		if b == rt {
			return Verdict{Pass: false, Explanation: fmt.Sprintf("allowedRequestTypes: %q is blocked", rt)}
		}
	}
	if len(a.Allowed) == 0 {
		return Verdict{Pass: true, Explanation: fmt.Sprintf("allowedRequestTypes: %q allowed (no allowlist)", rt)}
	}
	for _, al := range a.Allowed {
		if al == rt {
			return Verdict{Pass: true, Explanation: fmt.Sprintf("allowedRequestTypes: %q allowed", rt)}
		}
	}
	return Verdict{Pass: false, Explanation: fmt.Sprintf("allowedRequestTypes: %q not in allowlist %v", rt, a.Allowed)}
}
