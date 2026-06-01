package claude

import (
	"context"
	"io"
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
	o := newOptions(opts...)

	return func(yield func(Message, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		sess := newSession(o)
		// connect uses a nil inbound handler in M1; permissions/hooks/MCP are
		// wired in later milestones.
		if err := sess.connect(ctx, nil); err != nil {
			yield(nil, err)
			return
		}
		defer sess.close()

		if err := sess.sendPrompt(ctx, prompt); err != nil {
			yield(nil, err)
			return
		}

		for line := range sess.messages() {
			msg, err := decodeMessageLine(line)
			if err == io.EOF {
				return
			}
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}
			if msg == nil {
				continue // non-message frame; skip
			}
			if !yield(msg, nil) {
				return // consumer broke out; defer cancels + closes
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
