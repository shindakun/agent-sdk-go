# agent-sdk-go

A Go SDK for building agents with Claude Code — a faithful, idiomatic-Go port of
Anthropic's official [Claude Agent SDK](https://platform.claude.com/docs/en/agent-sdk/overview)
(Python and TypeScript).

This addresses [anthropics/claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498),
the upstream request for Go/Golang SDK support — native Go client, tool use and
MCP integration, streaming over channels, and `context.Context` cancellation.

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
- [x] **M2 — Control protocol + interactive `Client`:** multi-turn sessions,
      `Interrupt` / `SetModel` / `SetPermissionMode` / `GetContextUsage` /
      `Mcp*` / `StopTask`; session-id capture and reuse; `Query` now runs on top
      of `Client`.
- [x] **M3 — Permissions + hooks:** `CanUseTool` (allow with `updatedInput` /
      deny with `interrupt`) and lifecycle hooks (callback-id registration in
      the initialize handshake, `hook_callback` dispatch).
- [x] **M4 — In-process SDK MCP tools:** define Go tools the agent can call
      (`NewTool[T]` with reflection-derived schema, `SdkMcpServer`); `mcp_message`
      JSONRPC dispatch (initialize / tools.list / tools.call) wrapped in
      `mcp_response`.
- [x] **M5 — Full parity + examples:** remaining flags (`--include-partial-messages`,
      `--setting-sources`, `--continue`, fallback model, thinking/effort, budget,
      plugins) and env vars (`CLAUDE_AGENT_SDK_VERSION`, `PWD`); `RewindFiles`;
      runnable [examples](examples/) for one-shot query, interactive sessions,
      and a custom Go tool.
- [x] **M6 — Wider parity:** on-disk session reading (`ListSessions`,
      `GetSessionInfo`, `GetSessionMessages`, `ListSubagents`,
      `GetSubagentMessages`), typed hook inputs (`Decode*` helpers), typed task
      notifications, the full `AgentDefinition` field set, `SettingSource`,
      `SdkPluginConfig`; four more examples (hooks, permission, options,
      sessions).
- [x] **M7 — Full name-for-name parity:** session store + mutations
      (`SessionStore`, `InMemorySessionStore`, `*ViaStore`), sandbox settings
      (`WithSandbox`), rate-limit and context-usage types, server-tool content
      blocks, task started/progress messages, and the remaining hook-input and
      MCP-status types. See [PARITY.md](PARITY.md).

> Fidelity note: each milestone's wire format was checked against the official
> `claude-agent-sdk-python` source. One correction surfaced in M5 — a
> `CanUseTool` callback must **not** add `--permission-prompt-tool`; in
> stream-json mode the CLI routes permission requests to the SDK over the
> control protocol automatically.

## Parity

The Go port covers the official Python SDK's public surface name-for-name —
query and interactive client, options, messages and content blocks (including
server-tool and task lifecycle), tools and MCP, permissions and typed hook
inputs, subagents, control requests, on-disk session reading, the session store
and mutations, sandbox, rate-limit and context-usage types, and errors. Every
wire format was verified against the Python source. See [PARITY.md](PARITY.md)
for the full name-by-name mapping.

## Design

See [DESIGN.md](DESIGN.md) for the architecture and the verified protocol
details.

## License

[MIT](LICENSE).
