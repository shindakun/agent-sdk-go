// Command tools_option demonstrates restricting the agent's available tools with
// the --tools option. Mirrors the upstream tools_option.py example.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	// WithToolList restricts availability to exactly these tools. (Use
	// WithToolsPreset() for the default preset, or WithToolList() with no args
	// to disable all tools.)
	for msg, err := range claude.Query(ctx,
		"List the Go files here and summarize what the package does.",
		claude.WithToolList("Read", "Glob", "Grep"),
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
