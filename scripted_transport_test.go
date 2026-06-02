package claude

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

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

	payload := json.RawMessage(`{}`)
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

func (s *scriptedTransport) EndInput() error { return nil }

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

// interactiveTransport is a scriptedTransport variant for multi-turn Client
// tests. It answers every control_request, emits a scripted reply per user
// message, and stays open (no EOF) until Close.
type interactiveTransport struct {
	ch     chan transport.RawLine
	mu     sync.Mutex
	writes [][]byte
	closed bool

	// onUser returns the message lines to emit in response to a user prompt.
	onUser func(turn int, prompt string) [][]byte
	// controlResponder produces a response payload per control subtype.
	controlResponder func(subtype string, payload json.RawMessage) json.RawMessage
	turn             int
}

func newInteractiveTransport() *interactiveTransport {
	return &interactiveTransport{ch: make(chan transport.RawLine, 64)}
}

func (s *interactiveTransport) Connect(ctx context.Context) error { return nil }
func (s *interactiveTransport) Read() <-chan transport.RawLine    { return s.ch }

func (s *interactiveTransport) Write(ctx context.Context, obj []byte) error {
	s.mu.Lock()
	s.writes = append(s.writes, append([]byte(nil), obj...))
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return io.ErrClosedPipe
	}

	var probe struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Request   json.RawMessage `json:"request"`
		Message   struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(obj, &probe); err != nil {
		return nil
	}

	switch probe.Type {
	case "control_request":
		var sub struct {
			Subtype string `json:"subtype"`
		}
		_ = json.Unmarshal(probe.Request, &sub)
		payload := json.RawMessage(`{}`)
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
		s.send(transport.RawLine{Data: b})
	case "user":
		s.mu.Lock()
		s.turn++
		turn := s.turn
		s.mu.Unlock()
		if s.onUser != nil {
			for _, line := range s.onUser(turn, probe.Message.Content) {
				s.send(transport.RawLine{Data: append([]byte(nil), line...)})
			}
		}
	}
	return nil
}

func (s *interactiveTransport) send(l transport.RawLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.ch <- l
	}
}

func (s *interactiveTransport) EndInput() error { return nil }

func (s *interactiveTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.ch <- transport.RawLine{Err: io.EOF}
		close(s.ch)
	}
	return nil
}

func (s *interactiveTransport) writtenLines() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.writes))
	copy(out, s.writes)
	return out
}

// sendInbound injects a CLI->SDK control_request and blocks until the SDK's
// control_response for it is written, returning the response's "response"
// payload (or an error string if the SDK answered with subtype "error").
func (s *interactiveTransport) sendInbound(t interface{ Fatalf(string, ...any) }, requestID, subtype string, extra map[string]any) (json.RawMessage, string) {
	req := map[string]any{"subtype": subtype}
	for k, v := range extra {
		req[k] = v
	}
	env := map[string]any{"type": "control_request", "request_id": requestID, "request": req}
	b, _ := json.Marshal(env)
	s.send(transport.RawLine{Data: b})

	// Poll the written lines for the matching control_response.
	for i := 0; i < 200; i++ {
		for _, l := range s.writtenLines() {
			var resp struct {
				Type     string `json:"type"`
				Response struct {
					Subtype   string          `json:"subtype"`
					RequestID string          `json:"request_id"`
					Response  json.RawMessage `json:"response"`
					Error     string          `json:"error"`
				} `json:"response"`
			}
			if json.Unmarshal(l, &resp) != nil || resp.Type != "control_response" {
				continue
			}
			if resp.Response.RequestID == requestID {
				return resp.Response.Response, resp.Response.Error
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no control_response for request %q (subtype %q)", requestID, subtype)
	return nil, ""
}

func installInteractive(st *interactiveTransport) func() {
	prev := transportFactory
	transportFactory = func(transport.Config) transport.Transport { return st }
	return func() { transportFactory = prev }
}
