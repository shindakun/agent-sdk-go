// Command collect demonstrates the Collect convenience (gather all messages at
// once) and reading typed fields off the result. A Go-idiomatic extra with no
// direct upstream counterpart.
package main

import (
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	msgs, err := claude.Collect(ctx, "In one sentence, what is the Go programming language?")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	for _, msg := range msgs {
		if r, ok := msg.(*claude.ResultMessage); ok {
			fmt.Println("result:    ", r.Result)
			fmt.Printf("cost:       $%.4f USD\n", r.TotalCostUSD)
			fmt.Printf("turns:      %d\n", r.NumTurns)
			fmt.Printf("duration:   %dms (api %dms)\n", r.DurationMs, r.DurationAPIMs)
			fmt.Printf("session:    %s\n", r.SessionID)
		}
	}
}
