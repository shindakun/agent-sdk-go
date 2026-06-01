// Command customtool demonstrates exposing an in-process Go tool to the agent
// via an SDK MCP server. The agent can call the "add" tool, which runs inside
// this program.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

// addArgs is the input schema for the add tool; field tags drive the generated
// JSON Schema.
type addArgs struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

func main() {
	ctx := context.Background()

	calc := claude.NewSdkMcpServer("calc").AddTool(
		claude.NewTool("add", "Add two numbers",
			func(ctx context.Context, in addArgs) (claude.ToolResult, error) {
				return claude.TextResult(fmt.Sprintf("%g", in.A+in.B)), nil
			}),
	)

	for msg, err := range claude.Query(ctx, "Use the add tool to compute 2 + 3.",
		claude.WithSDKMCPServer("calc", calc),
		// Pre-approve the in-process tool so it runs without a prompt.
		claude.WithAllowedTools("mcp__calc__add"),
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
