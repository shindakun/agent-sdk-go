package protocol

import (
	"context"
	"encoding/json"
	"time"
)

// InitializeRequest is the SDK->CLI initialize handshake payload. Hooks, Agents,
// and Skills are pre-serialized by the root package into the shapes the CLI
// expects.
type InitializeRequest struct {
	Subtype                string          `json:"subtype"` // always "initialize"
	Hooks                  json.RawMessage `json:"hooks,omitempty"`
	Agents                 json.RawMessage `json:"agents,omitempty"`
	Skills                 json.RawMessage `json:"skills,omitempty"`
	ExcludeDynamicSections *bool           `json:"excludeDynamicSections,omitempty"`
}

// Initialize performs the handshake and returns the CLI's initialize response.
func (e *Engine) Initialize(ctx context.Context, req InitializeRequest, timeout time.Duration) (json.RawMessage, error) {
	req.Subtype = "initialize"
	if timeout <= 0 {
		timeout = DefaultInitializeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return e.SendRequest(ctx, req)
}

// SendControl issues a control request of the given subtype with optional extra
// fields (already a JSON object, or nil). It is the generic path used by the
// root package's client_control methods.
func (e *Engine) SendControl(ctx context.Context, subtype string, extra map[string]any) (json.RawMessage, error) {
	req := map[string]any{"subtype": subtype}
	for k, v := range extra {
		req[k] = v
	}
	return e.SendRequest(ctx, req)
}
