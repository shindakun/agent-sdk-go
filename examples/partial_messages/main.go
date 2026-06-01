// Command partial_messages demonstrates streaming partial message events as they
// arrive. Mirrors the upstream include_partial_messages.py example.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	partials := 0
	for msg, err := range claude.Query(ctx,
		"Write a short haiku about Go's goroutines.",
		claude.WithIncludePartialMessages(),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		switch m := msg.(type) {
		case *claude.StreamEvent:
			// Raw streaming deltas arrive here; count them for the demo.
			partials++
		case *claude.ResultMessage:
			fmt.Printf("\n%s\n(received %d partial stream events)\n", m.Result, partials)
		}
	}
}
