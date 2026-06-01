// Command hooks demonstrates lifecycle hooks: a PreToolUse hook logs and can
// block tool calls, using the typed hook-input decoder.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	logBash := func(ctx context.Context, input json.RawMessage, toolUseID string) (claude.HookOutput, error) {
		in, err := claude.DecodePreToolUse(input)
		if err != nil {
			return claude.HookOutput{}, err
		}
		fmt.Fprintf(os.Stderr, "[hook] PreToolUse tool=%s\n", in.ToolName)
		return claude.HookOutput{}, nil
	}

	for msg, err := range claude.Query(ctx, "List the files here using Bash.",
		claude.WithAllowedTools("Bash"),
		claude.WithHooks(map[claude.HookEvent][]claude.HookMatcher{
			claude.HookPreToolUse: {{Matcher: "Bash", Callbacks: []claude.HookCallback{logBash}}},
		}),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if r, ok := msg.(*claude.ResultMessage); ok {
			fmt.Println(r.Result)
		}
	}
}
