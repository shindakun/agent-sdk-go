// Command query is a one-shot example: it sends a single prompt and prints the
// final result. Requires a working `claude` binary and credentials in the
// environment (for example ANTHROPIC_API_KEY).
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	prompt := "What files are in this directory?"
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}

	for msg, err := range claude.Query(ctx, prompt,
		claude.WithAllowedTools("Bash", "Glob", "Read"),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		switch m := msg.(type) {
		case *claude.AssistantMessage:
			for _, b := range m.Content {
				if tb, ok := b.(*claude.TextBlock); ok {
					fmt.Print(tb.Text)
				}
			}
		case *claude.ResultMessage:
			fmt.Printf("\n\n--- done (%.4f USD, %d turns) ---\n", m.TotalCostUSD, m.NumTurns)
		}
	}
}
