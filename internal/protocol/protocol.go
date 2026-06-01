// Package protocol implements the bidirectional control protocol layered on the
// CLI's stream-json connection: request/response correlation by request_id, the
// initialize handshake, and dispatch of inbound control requests (permissions,
// hooks, MCP) to handlers supplied by the root package.
//
// It is internal to the SDK and deliberately free of any dependency on the root
// package; handlers are plumbed in as function-typed fields operating on raw
// JSON so there is no import cycle.
package protocol

import "encoding/json"

// Frame classification ---------------------------------------------------------

// envelope probes the top-level type of an inbound stream-json line.
type envelope struct {
	Type string `json:"type"`
}

// classify reports the top-level type of a raw line.
func classify(line []byte) (string, bool) {
	var e envelope
	if err := json.Unmarshal(line, &e); err != nil {
		return "", false
	}
	return e.Type, true
}

// Outbound: SDK -> CLI control request ----------------------------------------

// controlRequestEnvelope is written to the CLI to invoke a control request.
type controlRequestEnvelope struct {
	Type      string `json:"type"` // "control_request"
	RequestID string `json:"request_id"`
	Request   any    `json:"request"`
}

// Inbound: CLI -> SDK control response ----------------------------------------

// controlResponseEnvelope is the CLI's reply to an SDK control request.
type controlResponseEnvelope struct {
	Type     string `json:"type"` // "control_response"
	Response struct {
		Subtype   string          `json:"subtype"` // "success" | "error"
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response,omitempty"`
		Error     string          `json:"error,omitempty"`
	} `json:"response"`
}

// Inbound: CLI -> SDK control request -----------------------------------------

// controlRequestInbound is a control request initiated by the CLI that the SDK
// must service (can_use_tool, hook_callback, mcp_message).
type controlRequestInbound struct {
	Type      string          `json:"type"` // "control_request"
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
}

// controlSubtype probes the subtype of an inbound control request payload.
type controlSubtype struct {
	Subtype string `json:"subtype"`
}

// controlCancel is a CLI -> SDK cancellation of an in-flight inbound request.
type controlCancel struct {
	Type      string `json:"type"` // "control_cancel_request"
	RequestID string `json:"request_id"`
}

// Outbound: SDK -> CLI control response ----------------------------------------

// controlResponseOut is written back to the CLI after servicing an inbound
// control request.
type controlResponseOut struct {
	Type     string              `json:"type"` // "control_response"
	Response controlResponseBody `json:"response"`
}

type controlResponseBody struct {
	Subtype   string          `json:"subtype"` // "success" | "error"
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}
