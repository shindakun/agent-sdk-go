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

	// A small, deterministic delegation so the example finishes quickly. (A
	// real code-review agent would be given Read/Glob/Grep and a codebase.)
	for msg, err := range claude.Query(ctx,
		"Use the haiku-writer agent to write a two-line haiku about Go. Reply with only the haiku.",
		claude.WithAllowedTools("Agent"),
		claude.WithAgents(map[string]claude.AgentDefinition{
			"haiku-writer": {
				Description: "Writes short haiku on a given topic.",
				Prompt:      "You write a concise haiku about the topic the user names. Output only the haiku.",
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
