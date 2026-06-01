# agent-sdk-go

A Go SDK for building agents with Claude Code — a faithful, idiomatic-Go port of
Anthropic's official [Claude Agent SDK](https://platform.claude.com/docs/en/agent-sdk/overview)
(Python and TypeScript). Addresses
[claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498),
the upstream request for a Go SDK.

Like the official SDKs, this is **not** a reimplementation of the agent loop. It
drives the user-installed `claude` Code CLI as a subprocess, speaking
newline-delimited `stream-json` over stdin/stdout plus a bidirectional JSON
control protocol on the same stream. The CLI owns the agent loop, built-in tools,
and context management; this SDK owns process lifecycle, framing,
control-protocol correlation, and dispatch of in-process callbacks (permissions,
hooks, and SDK MCP tools).

Verified against **Claude Code CLI 2.1.159** (the version the upstream SDK
bundles) — statically (123/123 public names, 45/45 options) and behaviorally (an
integration suite that runs against the real binary).

## Installation

```bash
go get github.com/shindakun/agent-sdk-go
```

Requirements:

- Go 1.26+
- A working `claude` binary on `PATH` (or via `WithCLIPath`):
  `npm install -g @anthropic-ai/claude-code`
- Claude Code auth — either `ANTHROPIC_API_KEY` in the environment, or an active
  Claude Code session.

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

## Basic usage: `Query`

`Query` runs a single prompt to completion and returns an
`iter.Seq2[Message, error]`. Type-switch on the concrete message types:

```go
for msg, err := range claude.Query(ctx, "Find and fix the bug in auth.go",
	claude.WithAllowedTools("Read", "Edit", "Bash"),
	claude.WithModel("claude-sonnet-4-6"),
) {
	if err != nil { return err }
	switch m := msg.(type) {
	case *claude.AssistantMessage:
		// m.Content holds TextBlock / ToolUseBlock / ThinkingBlock / ...
	case *claude.ResultMessage:
		fmt.Println(m.Result)
	}
}
```

`claude.Collect(ctx, prompt, opts...)` gathers all messages into a slice for
callers that don't need streaming.

## Interactive sessions: `Client`

For multi-turn, full-duplex use (interleaved sends/receives, runtime control):

```go
client := claude.NewClient(claude.WithAllowedTools("Read", "Grep"))
if err := client.Connect(ctx); err != nil { return err }
defer client.Close()

client.Query(ctx, "Summarize the package.")
for res := range client.Receive() {
	if res.Err != nil { return res.Err }
	// handle res.Message
}

client.SetModel(ctx, "opus")      // runtime control
client.Interrupt(ctx)             // stop the current turn
```

## Custom tools (in-process SDK MCP servers)

Define a tool in Go; it runs in your process and is called by the agent — no
external MCP subprocess:

```go
type addArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

calc := claude.NewSdkMcpServer("calc").AddTool(
	claude.NewTool("add", "Add two integers", func(ctx context.Context, in addArgs) (claude.ToolResult, error) {
		return claude.TextResult(fmt.Sprint(in.A + in.B)), nil
	}))

claude.Query(ctx, "Use the add tool to compute 2+3.",
	claude.WithSDKMCPServer("calc", calc),
	claude.WithAllowedTools("mcp__calc__add"))
```

## Hooks

Run Go code at lifecycle points (validate, log, block):

```go
claude.WithHooks(map[claude.HookEvent][]claude.HookMatcher{
	claude.HookPreToolUse: {{
		Matcher: "Bash",
		Callbacks: []claude.HookCallback{func(ctx context.Context, input json.RawMessage, toolUseID string) (claude.HookOutput, error) {
			in, _ := claude.DecodePreToolUse(input)
			log.Printf("about to run %s", in.ToolName)
			return claude.HookOutput{}, nil
		}},
	}},
})
```

## Permissions

```go
claude.WithCanUseTool(func(ctx context.Context, tool string, input json.RawMessage, pc claude.PermissionContext) (claude.PermissionResult, error) {
	if strings.HasPrefix(tool, "Write") {
		return claude.PermissionDeny{Message: "writes disabled"}, nil
	}
	return claude.PermissionAllow{}, nil
})
```

## Types

The streamed `Message` union: `AssistantMessage`, `UserMessage`, `SystemMessage`,
`ResultMessage`, `StreamEvent`, `TaskStartedMessage`, `TaskProgressMessage`,
`TaskNotificationMessage`, `RateLimitEvent`, `MirrorErrorMessage`. Content blocks:
`TextBlock`, `ThinkingBlock`, `ToolUseBlock`, `ToolResultBlock`,
`ServerToolUseBlock`, `ServerToolResultBlock`.

## Error handling

Errors are typed values — use `errors.As`: `CLINotFoundError`, `ProcessError`,
`ConnectionError`, `JSONDecodeError`, `MessageParseError`, `ControlProtocolError`,
and the `ErrClosed` sentinel.

## Sessions

Read on-disk transcripts (no running CLI needed): `ListSessions`,
`GetSessionInfo`, `GetSessionMessages`, `ListSubagents`, `GetSubagentMessages`.
A `SessionStore` interface with `InMemorySessionStore`, `*ViaStore` mutations,
`FoldSessionSummary`, and `ImportSessionToStore` mirrors the upstream session API.
Live mirroring is available via `WithSessionStore`.

## Examples

See [examples/](examples/) — 16 runnable programs mapped 1:1 to the upstream
Python examples (plus a few Go-idiomatic extras). Each maps to its counterpart in
[examples/README.md](examples/README.md).

## Development

```bash
go build ./...          # library + examples
go vet ./...
gofmt -l .
go test -race ./...                       # unit tests
go test -tags integration -timeout 600s   # against a real claude binary
```

See [CLAUDE.md](CLAUDE.md) for the codebase map and the parity workflow.

## Parity

Verified name-for-name and field-for-field against
`claude-agent-sdk-python` (CLI 2.1.159): all 123 public `__all__` names and 45
`ClaudeAgentOptions` fields covered (a handful of Python-runtime-specific names
documented N/A), with behavioral checks against the real binary. See
[PARITY.md](PARITY.md).

## Design

See [DESIGN.md](DESIGN.md) for the architecture and protocol details.

## License

[MIT](LICENSE).
