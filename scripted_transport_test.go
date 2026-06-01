package claude

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/shindakun/agent-sdk-go/internal/transport"
)

// scriptedTransport is an in-memory transport.Transport for tests. It records
// every line written to it, auto-answers SDK->CLI control requests (so the
// initialize handshake completes), and replays a fixed script of message lines
// after the handshake.
type scriptedTransport struct {
	// script holds raw stream-json message lines emitted after initialize.
	script [][]byte
	// controlResponder, if set, produces the response payload for an inbound
	// control request given its decoded subtype; nil yields an empty success.
	controlResponder func(subtype string, payload json.RawMessage) json.RawMessage

	ch        chan transport.RawLine
	mu        sync.Mutex
	writes    [][]byte
	connected bool
	scriptOut bool
}

func newScriptedTransport(script ...[]byte) *scriptedTransport {
	return &scriptedTransport{
		script: script,
		ch:     make(chan transport.RawLine, 64),
	}
}

func (s *scriptedTransport) Connect(ctx context.Context) error {
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()
	return nil
}

func (s *scriptedTransport) Read() <-chan transport.RawLine { return s.ch }

// Write records the line. If it is a control_request (the initialize handshake
// or any SDK->CLI request), it synthesizes a matching control_response and, the
// first time, flushes the scripted messages followed by EOF.
func (s *scriptedTransport) Write(ctx context.Context, obj []byte) error {
	s.mu.Lock()
	s.writes = append(s.writes, append([]byte(nil), obj...))
	s.mu.Unlock()

	var probe struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(obj, &probe); err != nil {
		return nil
	}
	if probe.Type != "control_request" {
		return nil
	}

	var sub struct {
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(probe.Request, &sub)

	var payload json.RawMessage = json.RawMessage(`{}`)
	if s.controlResponder != nil {
		if p := s.controlResponder(sub.Subtype, probe.Request); p != nil {
			payload = p
		}
	}

	resp := map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": probe.RequestID,
			"response":   payload,
		},
	}
	b, _ := json.Marshal(resp)
	s.ch <- transport.RawLine{Data: b}

	// After the initialize handshake, replay the scripted output once.
	if sub.Subtype == "initialize" {
		s.mu.Lock()
		already := s.scriptOut
		s.scriptOut = true
		s.mu.Unlock()
		if !already {
			go s.flushScript()
		}
	}
	return nil
}

func (s *scriptedTransport) flushScript() {
	for _, line := range s.script {
		s.ch <- transport.RawLine{Data: append([]byte(nil), line...)}
	}
	s.ch <- transport.RawLine{Err: io.EOF}
}

func (s *scriptedTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connected {
		s.connected = false
	}
	return nil
}

func (s *scriptedTransport) writtenLines() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.writes))
	copy(out, s.writes)
	return out
}

// installScriptedTransport swaps the package transport factory for one that
// returns st, returning a restore function.
func installScriptedTransport(st *scriptedTransport) func() {
	prev := transportFactory
	transportFactory = func(transport.Config) transport.Transport { return st }
	return func() { transportFactory = prev }
}
