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
// A working `claude` binary must be available (see [WithCLIPath]). Errors —
// binary discovery, connection, decode, or a non-zero CLI exit — are delivered
// as the second value of the iterator and terminate iteration.
//
//	for msg, err := range claude.Query(ctx, "hello") {
//	    if err != nil { return err }
//	    // type-switch on msg
//	}
func Query(ctx context.Context, prompt string, opts ...Option) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		client := NewClient(opts...)
		if err := client.Connect(ctx); err != nil {
			yield(nil, err)
			return
		}
		// Close on iterator exit; its error (a non-zero CLI exit) is best-effort
		// here — message-level errors already surfaced through yield above.
		defer func() { _ = client.Close() }()

		if err := client.Query(ctx, prompt); err != nil {
			yield(nil, err)
			return
		}

		// One-shot: signal end-of-input so the CLI exits after the turn. If the
		// session needs bidirectional traffic (SDK MCP servers, hooks, or a
		// permission callback), keep stdin open until the first result; close
		// it immediately otherwise. This mirrors the official SDK.
		bidi := client.sess.needsBidirectional()
		if !bidi {
			_ = client.sess.endInput()
		}

		for msg, err := range client.Messages(ctx) {
			if !yield(msg, err) {
				return // consumer broke out; defer cancels + closes
			}
			if err != nil {
				return
			}
			if _, isResult := msg.(*ResultMessage); isResult && bidi {
				_ = client.sess.endInput()
			}
		}
	}
}

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
