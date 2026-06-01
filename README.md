# agent-sdk-go

A Go SDK for building agents with Claude Code — a faithful, idiomatic-Go port of
Anthropic's official [Claude Agent SDK](https://platform.claude.com/docs/en/agent-sdk/overview)
(Python and TypeScript).

Like the official SDKs, this is **not** a reimplementation of the agent loop. It
drives the user-installed `claude` Code CLI binary as a subprocess, speaking
newline-delimited `stream-json` over the subprocess's stdin/stdout plus a
bidirectional JSON control protocol on the same stream. The CLI owns the agent
loop, built-in tools, and context management; this SDK owns process lifecycle,
framing, control-protocol correlation, and dispatch of in-process callbacks
(permissions, hooks, and SDK MCP tools).

## Requirements

- Go 1.26+
- A working `claude` binary on `PATH` (or supplied via `WithCLIPath`).
  Install with `npm install -g @anthropic-ai/claude-code`.
- Credentials in the environment (for example `ANTHROPIC_API_KEY`).

## Quick start

```go
package main

import (
	"context"
	"fmt"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()
	for msg, err := range claude.Query(ctx, "What files are in this directory?",
		claude.WithAllowedTools("Bash", "Glob")) {
		if err != nil {
			panic(err)
		}
		if r, ok := msg.(*claude.ResultMessage); ok {
			fmt.Println(r.Result)
		}
	}
}
```

## Status

Built milestone-by-milestone against the verified official wire protocol.

- [x] **M1 — Walking skeleton:** `Query` / `Collect` over the real subprocess
      via stream-json; transport (spawn, discovery, framing, shutdown); control
      engine with the initialize handshake; typed message/content union; typed
      errors.
- [ ] **M2 — Control protocol + interactive `Client`:** multi-turn sessions,
      `Interrupt` / `SetModel` / `SetPermissionMode` / `GetContextUsage`.
- [ ] **M3 — Permissions + hooks:** `CanUseTool` and lifecycle hooks.
- [ ] **M4 — In-process SDK MCP tools:** define Go tools the agent can call.
- [ ] **M5 — Full parity + examples.**

## Design

See [DESIGN.md](DESIGN.md) for the architecture and the verified protocol
details.

## License

To be determined.
