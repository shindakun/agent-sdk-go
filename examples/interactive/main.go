// Command interactive is a multi-turn example using the bidirectional Client:
// it reads prompts from stdin, streams each response, and supports runtime
// control (here, switching the model after the first turn).
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	client := claude.NewClient(claude.WithAllowedTools("Read", "Glob", "Grep"))
	if err := client.Connect(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	// Consume the stream in the background, printing assistant text and noting
	// when each turn completes.
	go func() {
		for res := range client.Receive() {
			if res.Err != nil {
				fmt.Fprintln(os.Stderr, "stream:", res.Err)
				return
			}
			switch m := res.Message.(type) {
			case *claude.AssistantMessage:
				for _, b := range m.Content {
					if tb, ok := b.(*claude.TextBlock); ok {
						fmt.Print(tb.Text)
					}
				}
			case *claude.ResultMessage:
				fmt.Print("\n> ")
			}
		}
	}()

	fmt.Print("> ")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if line == "/quit" {
			return
		}
		if err := client.Query(ctx, line); err != nil {
			fmt.Fprintln(os.Stderr, "query:", err)
			return
		}
	}
}
