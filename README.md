# agent-sdk-go

[![Lint](https://github.com/shindakun/agent-sdk-go/actions/workflows/lint.yml/badge.svg)](https://github.com/shindakun/agent-sdk-go/actions/workflows/lint.yml)
[![Test](https://github.com/shindakun/agent-sdk-go/actions/workflows/test.yml/badge.svg)](https://github.com/shindakun/agent-sdk-go/actions/workflows/test.yml)

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

Verified against **Claude Code CLI 2.1.178** (the version the upstream SDK
bundles, pinned as `claude.SupportedCLIVersion`) — statically (126/126 public
names, 45/45 options) and behaviorally (an integration suite that runs against
the real binary). `claude.CheckCLIVersion` reports the installed binary's
version and whether it matches the pin.

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
`TaskNotificationMessage`, `TaskUpdatedMessage`, `RateLimitEvent`,
`MirrorErrorMessage`. Content blocks:
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
golangci-lint run       # see .golangci.yml (lints integration/e2e files too)
go test -race ./...                       # unit tests
go test -tags integration -timeout 600s   # smoke tier against a real claude binary
go test -tags e2e -timeout 1200s          # full e2e tier (paid API calls)
bash scripts/run-examples.sh              # run every example against the binary
```

### CI

GitHub Actions:

- **Lint** and **Test** run on every pull request and push to `main` —
  `gofmt`/`go vet`/build, and `go test -race` across Linux/macOS/Windows. Free,
  no secrets.
- **E2E** is manual (`workflow_dispatch`): it installs `claude`, runs the
  `e2e`-tagged tests and every example against the real binary. It needs an
  `ANTHROPIC_API_KEY` repo secret and makes paid API calls, so it is not run
  automatically.
- **Upstream watch** runs daily (and on demand): it watches
  `anthropics/claude-agent-sdk-python` for new commits and files triage issues
  here. Pure CLI-version bumps roll into one `Upstream CLI version bumps` issue;
  commits touching SDK source are classified by Claude and get an individual
  labeled issue with the diff link and a port recommendation; docs/test/example
  commits are ignored. It needs the `ANTHROPIC_API_KEY` secret. The triage prompt
  treats commit messages and diffs as untrusted data (prompt-injection hardened),
  and the model can only *suggest* a label — it never decides whether to file or
  takes any action. Logic lives in [`scripts/upstream-watch.sh`](scripts/upstream-watch.sh)
  and is runnable locally with `DRY_RUN=1`.

See [CLAUDE.md](CLAUDE.md) for the codebase map and the parity workflow.

## Parity

Verified name-for-name and field-for-field against
`claude-agent-sdk-python` (CLI 2.1.178): all 126 public `__all__` names and 45
`ClaudeAgentOptions` fields covered (a handful of Python-runtime-specific names
documented N/A), with behavioral checks against the real binary. See
[PARITY.md](PARITY.md).

## Design

See [DESIGN.md](DESIGN.md) for the architecture and protocol details.

## License

[MIT](LICENSE).
