package protocol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shindakun/agent-sdk-go/internal/transport"
)

// DefaultInitializeTimeout bounds the initialize handshake.
const DefaultInitializeTimeout = 60 * time.Second

// InboundHandler services an inbound CLI->SDK control request. It receives the
// request payload (raw JSON, including its subtype) and returns the JSON
// response payload, or an error to be reported back to the CLI. The provided
// context is canceled if the CLI sends a control_cancel_request for this call
// or if the engine shuts down.
type InboundHandler func(ctx context.Context, subtype string, payload json.RawMessage) (json.RawMessage, error)

// Engine runs the control protocol over a transport. It demultiplexes inbound
// lines into user-facing messages (delivered on Messages) and control frames
// (handled internally).
type Engine struct {
	t transport.Transport

	// handler services inbound control requests; may be nil (then inbound
	// control requests are answered with an error).
	handler InboundHandler

	counter uint64

	mu      sync.Mutex
	pending map[string]chan controlResult

	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc

	msgCh chan MessageLine

	// onMirror, when set, receives raw transcript_mirror frames instead of the
	// engine dropping them.
	onMirror func([]byte)

	startOnce sync.Once
	closed    atomic.Bool
	// handlers tracks in-flight inbound-request goroutines so Close can wait
	// for them rather than leaking or racing the transport.
	handlers sync.WaitGroup
}

// SetMirrorSink registers a callback to receive transcript_mirror frames. It
// must be called before [Engine.Start].
func (e *Engine) SetMirrorSink(fn func([]byte)) { e.onMirror = fn }

// MessageLine is a non-control stream-json line forwarded to the consumer, or a
// terminal error/EOF.
type MessageLine struct {
	Data []byte
	Err  error
}

type controlResult struct {
	payload json.RawMessage
	err     error
}

// NewEngine creates an engine over t. handler may be nil.
func NewEngine(t transport.Transport, handler InboundHandler) *Engine {
	return &Engine{
		t:       t,
		handler: handler,
		pending: map[string]chan controlResult{},
		cancels: map[string]context.CancelFunc{},
		msgCh:   make(chan MessageLine, 64),
	}
}

// Messages returns the channel of forwarded message lines. It is closed when
// the stream ends.
func (e *Engine) Messages() <-chan MessageLine { return e.msgCh }

// Start begins reading from the transport. It must be called once after the
// transport is connected.
func (e *Engine) Start() {
	e.startOnce.Do(func() { go e.readLoop() })
}

func (e *Engine) readLoop() {
	defer close(e.msgCh)
	for line := range e.t.Read() {
		if line.Err != nil {
			e.msgCh <- MessageLine{Err: line.Err}
			// Continue draining; io.EOF is terminal and the channel will close.
			continue
		}
		e.dispatch(line.Data)
	}
	e.failPending(fmt.Errorf("protocol: connection closed"))
}

func (e *Engine) dispatch(line []byte) {
	typ, ok := classify(line)
	if !ok {
		// Not valid JSON; forward as a message line so the consumer surfaces
		// the decode error.
		e.msgCh <- MessageLine{Data: append([]byte(nil), line...)}
		return
	}

	switch typ {
	case "control_response":
		e.handleControlResponse(line)
	case "control_request":
		e.handleInboundRequest(line)
	case "control_cancel_request":
		e.handleCancel(line)
	case "transcript_mirror":
		// SessionStore write frames are peeled off the stream. When a sink is
		// registered they are delivered to it; otherwise dropped.
		if e.onMirror != nil {
			e.onMirror(append([]byte(nil), line...))
		}
	default:
		e.msgCh <- MessageLine{Data: append([]byte(nil), line...)}
	}
}

func (e *Engine) handleControlResponse(line []byte) {
	var env controlResponseEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return
	}
	id := env.Response.RequestID

	e.mu.Lock()
	ch := e.pending[id]
	delete(e.pending, id)
	e.mu.Unlock()
	if ch == nil {
		return
	}

	if env.Response.Subtype == "error" {
		ch <- controlResult{err: fmt.Errorf("%s", env.Response.Error)}
		return
	}
	ch <- controlResult{payload: env.Response.Response}
}

func (e *Engine) handleInboundRequest(line []byte) {
	var env controlRequestInbound
	if err := json.Unmarshal(line, &env); err != nil {
		return
	}
	var sub controlSubtype
	_ = json.Unmarshal(env.Request, &sub)

	// Drop new inbound requests once closed so we don't spawn goroutines that
	// race a closing transport.
	if e.closed.Load() {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.cancelMu.Lock()
	e.cancels[env.RequestID] = cancel
	e.cancelMu.Unlock()

	e.handlers.Add(1)
	go func() {
		defer e.handlers.Done()
		defer func() {
			e.cancelMu.Lock()
			delete(e.cancels, env.RequestID)
			e.cancelMu.Unlock()
			cancel()
		}()

		payload, err := e.serviceInbound(ctx, sub.Subtype, env.Request)
		e.respondInbound(env.RequestID, payload, err)
	}()
}

// serviceInbound runs the registered handler with panic recovery so a faulty
// callback cannot kill the read loop.
func (e *Engine) serviceInbound(ctx context.Context, subtype string, payload json.RawMessage) (out json.RawMessage, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	if e.handler == nil {
		return nil, fmt.Errorf("no handler registered for control request %q", subtype)
	}
	return e.handler(ctx, subtype, payload)
}

func (e *Engine) respondInbound(requestID string, payload json.RawMessage, err error) {
	out := controlResponseOut{Type: "control_response"}
	out.Response.RequestID = requestID
	if err != nil {
		out.Response.Subtype = "error"
		out.Response.Error = err.Error()
	} else {
		out.Response.Subtype = "success"
		out.Response.Response = payload
	}
	b, mErr := json.Marshal(out)
	if mErr != nil {
		return
	}
	// Skip the write if we're shutting down; the transport may be closing.
	if e.closed.Load() {
		return
	}
	_ = e.t.Write(context.Background(), b)
}

func (e *Engine) handleCancel(line []byte) {
	var c controlCancel
	if err := json.Unmarshal(line, &c); err != nil {
		return
	}
	e.cancelMu.Lock()
	if cancel := e.cancels[c.RequestID]; cancel != nil {
		cancel()
	}
	e.cancelMu.Unlock()
}

// SendRequest issues an SDK->CLI control request and blocks until the matching
// response arrives, the context is canceled, or the connection closes.
func (e *Engine) SendRequest(ctx context.Context, request any) (json.RawMessage, error) {
	if e.closed.Load() {
		return nil, fmt.Errorf("protocol: closed")
	}
	id := e.nextID()
	ch := make(chan controlResult, 1)

	e.mu.Lock()
	e.pending[id] = ch
	e.mu.Unlock()

	env := controlRequestEnvelope{Type: "control_request", RequestID: id, Request: request}
	b, err := json.Marshal(env)
	if err != nil {
		e.dropPending(id)
		return nil, err
	}
	if err := e.t.Write(ctx, b); err != nil {
		e.dropPending(id)
		return nil, err
	}

	select {
	case res := <-ch:
		return res.payload, res.err
	case <-ctx.Done():
		e.dropPending(id)
		return nil, ctx.Err()
	}
}

func (e *Engine) dropPending(id string) {
	e.mu.Lock()
	delete(e.pending, id)
	e.mu.Unlock()
}

func (e *Engine) failPending(err error) {
	e.mu.Lock()
	for id, ch := range e.pending {
		ch <- controlResult{err: err}
		delete(e.pending, id)
	}
	e.mu.Unlock()
}

func (e *Engine) nextID() string {
	n := atomic.AddUint64(&e.counter, 1)
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("req_%d_%s", n, hex.EncodeToString(b[:]))
}

// Close marks the engine closed. It does not close the transport; the caller
// owns the transport lifecycle.
func (e *Engine) Close() {
	e.closed.Store(true)
	e.cancelMu.Lock()
	for _, cancel := range e.cancels {
		cancel()
	}
	e.cancelMu.Unlock()
	// Wait for in-flight inbound-request handlers to finish so they don't race
	// the transport shutdown.
	e.handlers.Wait()
}
