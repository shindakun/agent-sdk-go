package claude

import (
	"context"
	"iter"
)

// Query runs a single prompt to completion and returns an iterator over the
// messages the CLI emits. Iteration ends after the terminal result message (or
// on error); breaking out of the range loop early cancels the CLI subprocess
// and releases all resources.
//
// A working `claude` binary must be available (see [WithCLIPath]). Errors
// (binary discovery, connection, decode, or a non-zero CLI exit) are delivered
// as the second value of the iterator and terminate iteration.
//
// With hooks, [WithCanUseTool] or SDK MCP servers, stdin stays open past the
// result so the CLI can still ask the SDK questions: until the CLI reports the
// session idle (a background agent's completion can wake a follow-up turn), or
// until CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS (10 minutes by default, 0 for no
// limit) passes between turns with the CLI still busy. A CLI that does not
// report session state ends input at the first result with no background
// agent in flight.
//
//	for msg, err := range claude.Query(ctx, "hello") {
//	    if err != nil { return err }
//	    // type-switch on msg
//	}
func Query(ctx context.Context, prompt string, opts ...Option) iter.Seq2[Message, error] {
	return runQuery(ctx, opts, func(ctx context.Context, c *Client) (int, error) {
		return 1, c.Query(ctx, prompt)
	})
}

// QueryMessages is [Query] with streamed user messages instead of a string
// prompt, the counterpart of passing an async iterable to the official
// query(). See [Client.QueryMessages] for the message shape. Input ends once
// every message is written (when callbacks need the control channel, once the
// last message's run has ended, as [Query] describes); if msgs yields nothing,
// it ends at once.
func QueryMessages(ctx context.Context, msgs iter.Seq[map[string]any], opts ...Option) iter.Seq2[Message, error] {
	return runQuery(ctx, opts, func(ctx context.Context, c *Client) (int, error) {
		sess, err := c.session()
		if err != nil {
			return 0, err
		}
		return sess.sendMessages(ctx, msgs, "")
	})
}

// runQuery connects a client, sends the prompt with send (which reports how
// many messages it wrote), ends input, and yields the messages until the run
// ends.
func runQuery(ctx context.Context, opts []Option, send func(context.Context, *Client) (int, error)) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		client := NewClient(opts...)
		client.oneShot = true
		if err := client.Connect(ctx); err != nil {
			yield(nil, err)
			return
		}
		// Close on iterator exit; its error (a non-zero CLI exit) is reported
		// through the stream when it matters, so it is not repeated here.
		defer func() { _ = client.Close() }()

		written, err := send(ctx, client)
		if err != nil {
			_ = client.sess.endInput()
			yield(nil, err)
			return
		}

		// One-shot: end input so the CLI exits after the run. If the session
		// needs bidirectional traffic (SDK MCP servers, hooks, or a permission
		// callback), keep stdin open until the run ends (see runTracker), on
		// a goroutine woken by the read loop; close it immediately otherwise,
		// or when nothing was sent (no result would come to release it). This
		// mirrors the official SDK.
		run := client.sess.run
		waited := make(chan struct{})
		stop := make(chan struct{})
		if run == nil || written == 0 {
			run.finish()
			_ = client.sess.endInput()
			close(waited)
		} else {
			go func() {
				defer close(waited)
				select {
				case <-run.done():
				case <-stop:
					return
				case <-ctx.Done():
					return
				}
				run.finish()
				_ = client.sess.endInput()
			}()
		}
		defer func() {
			close(stop)
			<-waited
		}()

		for msg, err := range client.Messages(ctx) {
			if !yield(msg, err) || err != nil {
				return // defer stops the waiter, cancels + closes
			}
		}
		// The stream ended, so the read loop finished the run; let the
		// waiter close stdin before the deferred stop can race it.
		<-waited
	}
}

// deferringTaskTypes are the task types whose completion runs a follow-up turn,
// and which therefore may still need the control channel after the turn's
// result frame.
//
// This mirrors the set the CLI itself holds a result back for, which is
// narrower than its notion of "delegated agent work". The omissions are
// deliberate: background shells and monitors run indefinitely by design, so
// deferring the close on one withholds it forever rather than briefly;
// teammates are long-lived too, their status staying "running" for their whole
// lifetime; and remote agents can be long-running monitors the CLI likewise
// refuses to wait on. Anything added here must reliably reach a terminal
// status, or it will hang the query.
var deferringTaskTypes = map[string]struct{}{
	"local_agent": {}, "local_workflow": {},
}

// inflightTasks tracks started-but-not-finished tasks from the system task
// lifecycle frames, so [Query] can tell a turn-ending result from a run-ending
// one. The zero value is ready to use.
//
// An empty set means "nothing we know of is running", which is not the same as
// "the run is over": a task that settles before the turn's result frame leaves
// the set empty at that result, even though its completion may still wake the
// parent for a follow-up turn. No ledger can close that gap; the CLI's session
// state does (see runTracker), and this ledger remains the guard for CLIs that
// do not report it.
type inflightTasks struct {
	ids map[string]struct{}
}

// track records a task lifecycle frame. TaskStartedMessage marks a task in
// flight; a TaskNotificationMessage or a TaskUpdatedMessage carrying a terminal
// status clears it. Terminal completion can arrive as either frame (not every
// terminal task emits a notification), so both are handled, and clearing is
// idempotent.
func (i *inflightTasks) track(msg Message) {
	switch m := msg.(type) {
	case *TaskStartedMessage:
		if m.TaskID == "" {
			return
		}
		if _, ok := deferringTaskTypes[m.TaskType]; !ok {
			return
		}
		if i.ids == nil {
			i.ids = make(map[string]struct{})
		}
		i.ids[m.TaskID] = struct{}{}
	case *TaskNotificationMessage:
		delete(i.ids, m.TaskID)
	case *TaskUpdatedMessage:
		if IsTerminalTaskStatus(string(m.Status)) {
			delete(i.ids, m.TaskID)
		}
	}
}

func (i *inflightTasks) empty() bool { return len(i.ids) == 0 }

// Collect runs a prompt to completion and returns all emitted messages. It is a
// convenience wrapper over [Query] for callers that do not need streaming.
func Collect(ctx context.Context, prompt string, opts ...Option) ([]Message, error) {
	var msgs []Message
	for msg, err := range Query(ctx, prompt, opts...) {
		if err != nil {
			return msgs, err
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}
