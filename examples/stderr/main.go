// Command stderr demonstrates capturing the CLI subprocess's stderr for logging
// and debugging. Mirrors the upstream stderr_callback_example.py example.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

// prefixWriter tags each write so subprocess stderr is distinguishable.
type prefixWriter struct{ w *os.File }

func (p prefixWriter) Write(b []byte) (int, error) {
	fmt.Fprintf(p.w, "[claude stderr] %s", b)
	return len(b), nil
}

func main() {
	ctx := context.Background()

	for msg, err := range claude.Query(ctx, "Say hello.",
		claude.WithStderr(prefixWriter{w: os.Stderr}),
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
