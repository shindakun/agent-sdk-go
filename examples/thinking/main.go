// Command thinking demonstrates configuring extended thinking via the typed
// ThinkingConfig union.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	// Adaptive thinking lets the model decide how much to think.
	// Alternatives: ThinkingConfigEnabled{BudgetTokens: 8000} for a fixed budget,
	// or ThinkingConfigDisabled{} to turn it off.
	for msg, err := range claude.Query(ctx,
		"A bat and a ball cost $1.10 total. The bat costs $1 more than the ball. "+
			"How much is the ball? Show your reasoning briefly.",
		claude.WithThinkingConfig(claude.ThinkingConfigAdaptive{Type: "adaptive"}),
		claude.WithThinkingDisplay(claude.ThinkingDisplaySummarized),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		switch m := msg.(type) {
		case *claude.AssistantMessage:
			for _, b := range m.Content {
				if tb, ok := b.(*claude.ThinkingBlock); ok {
					fmt.Println("[thinking]", tb.Thinking)
				}
			}
		case *claude.ResultMessage:
			fmt.Println("[answer]", m.Result)
		}
	}
}
