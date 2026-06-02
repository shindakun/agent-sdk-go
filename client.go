package claude

import (
	"context"
	"errors"
	"io"
	"iter"
	"sync"
)

// Result pairs a streamed [Message] with a terminal error. Exactly one is set:
// a non-nil Err (other than [io.EOF], which is not surfaced) ends the stream.
type Result struct {
	Message Message
	Err     error
}

// Client is a long-lived, full-duplex session with the CLI. Unlike [Query],
// which runs a single prompt to completion, a Client supports multiple prompts,
// runtime control (model/permission changes, interrupts), and interleaved
// sending and receiving.
//
// A Client must be connected with [Client.Connect] before use and released with
// [Client.Close]. It is safe for one sender and one receiver to operate
// concurrently; concurrent prompts are serialized by the CLI, which processes
// one turn at a time.
type Client struct {
	opts *Options

	mu   sync.Mutex
	sess *session
	out  chan Result
	done chan struct{}

	closeOnce sync.Once
	closeErr  error
	closed    bool
}

// NewClient creates an unconnected Client.
func NewClient(opts ...Option) *Client {
	return &Client{opts: newOptions(opts...)}
}

// Connect spawns the CLI subprocess and completes the initialize handshake. The
// provided context governs the subprocess lifetime; canceling it terminates the
// session.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil {
		return nil
	}

	sess := newSession(c.opts)
	if err := sess.connect(ctx); err != nil {
		return err
	}
	c.sess = sess
	c.out = make(chan Result, 64)
	c.done = make(chan struct{})

	go c.readLoop()
	return nil
}

// readLoop drains the engine's message lines, captures the session id from the
// init message, decodes each line, and forwards it to consumers. When a live
// SessionStore mirror is configured it also forwards synthesized
// MirrorErrorMessages and flushes the mirror on each result message.
func (c *Client) readLoop() {
	defer close(c.out)
	defer close(c.done)

	lines := c.sess.messages()
	inject := c.sess.injectCh // nil unless mirroring is active

	for {
		select {
		case m, ok := <-inject:
			if ok {
				c.out <- Result{Message: m}
			} else {
				inject = nil
			}
		case line, ok := <-lines:
			if !ok {
				return
			}
			msg, err := decodeMessageLine(line)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				c.out <- Result{Err: err}
				continue
			}
			if msg == nil {
				continue
			}
			c.captureSessionID(msg)
			if _, isResult := msg.(*ResultMessage); isResult && c.sess.mirror != nil {
				c.sess.mirror.Flush(context.Background())
			}
			c.out <- Result{Message: msg}
		}
	}
}

func (c *Client) captureSessionID(msg Message) {
	if sm, ok := msg.(*SystemMessage); ok && sm.Subtype == "init" && sm.SessionID != "" {
		c.mu.Lock()
		c.sess.sessionID = sm.SessionID
		c.mu.Unlock()
	}
}

// Query sends a user prompt. It does not wait for the response; consume it via
// [Client.Receive] or [Client.Messages].
func (c *Client) Query(ctx context.Context, prompt string) error {
	sess, err := c.session()
	if err != nil {
		return err
	}
	return sess.sendPrompt(ctx, prompt)
}

// Receive returns the channel of streamed results. The channel is closed when
// the session ends.
func (c *Client) Receive() <-chan Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out
}

// Messages is an iterator over streamed messages, sugar over [Client.Receive].
// Iteration ends when the session ends or ctx is canceled.
func (c *Client) Messages(ctx context.Context) iter.Seq2[Message, error] {
	out := c.Receive()
	return func(yield func(Message, error) bool) {
		for {
			select {
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			case res, ok := <-out:
				if !ok {
					return
				}
				if !yield(res.Message, res.Err) {
					return
				}
			}
		}
	}
}

// Close shuts the session down and waits for the read loop to finish. It
// returns any process error from the CLI.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		// Snapshot and mark closed in a single critical section so a concurrent
		// session() cannot observe sess set while closed is still false.
		c.mu.Lock()
		sess := c.sess
		done := c.done
		c.closed = true
		c.mu.Unlock()
		if sess == nil {
			return
		}
		c.closeErr = sess.close()
		if done != nil {
			<-done
		}
	})
	return c.closeErr
}

// session returns the connected session or an error if not connected.
func (c *Client) session() (*session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess == nil || c.closed {
		return nil, ErrClosed
	}
	return c.sess, nil
}
