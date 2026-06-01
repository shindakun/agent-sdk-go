// Command sessions lists stored Claude Code sessions for the current directory
// and prints the transcript of the most recent one. This reads on-disk session
// files and does not require a running CLI.
package main

import (
	"fmt"
	"os"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	infos, err := claude.ListSessions("", 10, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list sessions:", err)
		os.Exit(1)
	}
	if len(infos) == 0 {
		fmt.Println("no sessions found for this directory")
		return
	}

	fmt.Printf("%d session(s):\n", len(infos))
	for _, s := range infos {
		fmt.Printf("  %s  %s\n", s.SessionID, s.Summary)
	}

	latest := infos[0]
	fmt.Printf("\nmessages in %s:\n", latest.SessionID)
	msgs, err := claude.GetSessionMessages(latest.SessionID, "", 0, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "get messages:", err)
		os.Exit(1)
	}
	for _, m := range msgs {
		fmt.Printf("  [%s] %s\n", m.Type, m.UUID)
	}
}
