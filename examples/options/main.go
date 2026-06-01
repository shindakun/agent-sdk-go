// Command options demonstrates several configuration options at once: a custom
// system prompt, setting sources, a budget cap, partial-message streaming, and
// an stderr sink.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	for msg, err := range claude.Query(ctx, "In one sentence, what is Go?",
		claude.WithSystemPrompt("You are a concise technical writer."),
		claude.WithSettingSources(
			string(claude.SettingSourceProject),
			string(claude.SettingSourceUser),
		),
		claude.WithMaxBudgetUSD(0.50),
		claude.WithIncludePartialMessages(),
		claude.WithStderr(os.Stderr),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		switch m := msg.(type) {
		case *claude.StreamEvent:
			// Partial events arrive here when include-partial-messages is set.
		case *claude.ResultMessage:
			fmt.Println(m.Result)
		}
	}
}
