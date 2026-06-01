// Command interrupt demonstrates interrupting an in-flight turn with the
// bidirectional Client. A Go-idiomatic extra (uses context + goroutines).
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	client := claude.NewClient(
		claude.WithAllowedTools("Bash"),
		claude.WithPermissionMode(claude.PermissionBypass),
	)
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := client.Query(ctx,
		"Count from 1 to 20, running `sleep 1` between each number using Bash."); err != nil {
		fmt.Fprintln(os.Stderr, "query:", err)
		os.Exit(1)
	}

	// Let it run briefly, then interrupt.
	go func() {
		time.Sleep(4 * time.Second)
		fmt.Println("--- interrupting ---")
		_ = client.Interrupt(ctx)
	}()

	for res := range client.Receive() {
		if res.Err != nil {
			fmt.Fprintln(os.Stderr, "stream:", res.Err)
			return
		}
		if r, ok := res.Message.(*claude.ResultMessage); ok {
			fmt.Printf("ended: subtype=%s\n", r.Subtype)
			return
		}
	}
}
