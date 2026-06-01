// Command filesystem demonstrates an agent reading, writing, and editing files
// in a working directory. Mirrors the upstream filesystem_agents.py example.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "agent-sdk-fs-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	for msg, err := range claude.Query(ctx,
		"Create a file named notes.txt containing three TODO items, then read it back to confirm.",
		claude.WithCwd(dir),
		claude.WithAllowedTools("Read", "Write", "Edit", "Glob"),
		claude.WithPermissionMode(claude.PermissionAcceptEdits),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if r, ok := msg.(*claude.ResultMessage); ok {
			fmt.Println(r.Result)
		}
	}
	fmt.Println("(worked in", dir+")")
}
