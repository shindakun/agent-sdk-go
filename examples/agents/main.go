// Command agents demonstrates defining a subagent the main agent can delegate to
// via the Agent tool. Mirrors the upstream agents.py example.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	for msg, err := range claude.Query(ctx,
		"Use the code-reviewer agent to review the code in this directory.",
		claude.WithAllowedTools("Read", "Glob", "Grep", "Agent"),
		claude.WithAgents(map[string]claude.AgentDefinition{
			"code-reviewer": {
				Description: "Expert code reviewer for quality and security reviews.",
				Prompt:      "Analyze code quality and suggest concrete improvements.",
				Tools:       []string{"Read", "Glob", "Grep"},
			},
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
