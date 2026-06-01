// Command permission demonstrates a CanUseTool callback that approves reads but
// denies writes, rewriting tool input where useful.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	decide := func(ctx context.Context, tool string, input json.RawMessage, pc claude.PermissionContext) (claude.PermissionResult, error) {
		if strings.HasPrefix(tool, "Write") || strings.HasPrefix(tool, "Edit") {
			return claude.PermissionDeny{Message: "writes are not permitted in this session"}, nil
		}
		return claude.PermissionAllow{}, nil
	}

	for msg, err := range claude.Query(ctx, "Read README.md and summarize it.",
		claude.WithCanUseTool(decide),
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
