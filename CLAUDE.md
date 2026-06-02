# Workflow

```bash
# Format and lint
gofmt -w .
go vet ./...

# Build everything (library + examples)
go build ./...

# Unit tests (always with the race detector)
go test -race ./...

# Run a single test
go test -race -run TestUnmarshalAssistant .

# Integration tests against a real `claude` binary (excluded by default).
# Requires `claude` on PATH and a working Claude Code auth session.
go test -tags integration -timeout 600s ./...

# Full e2e tier (paid API calls) and the example runner.
go test -tags e2e -timeout 1200s ./...
bash scripts/run-examples.sh
```

CI lives in `.github/workflows/`: `lint` and `test` (race, OS matrix) run on
every PR/push; `e2e` is manual (`workflow_dispatch`) and needs an
`ANTHROPIC_API_KEY` secret. The lint/test jobs mirror the local `gofmt`/`vet`/
`go test -race` commands above, so a green local run predicts green CI.

# Codebase Structure

Root package `claude` (import as `claude "github.com/shindakun/agent-sdk-go"`).
The SDK is a thin driver over the `claude` CLI subprocess; the CLI owns the agent
loop, built-in tools, and context management.

- `query.go` — `Query` (one-shot, `iter.Seq2[Message,error]`) and `Collect`.
- `client.go` / `client_control.go` — `Client` for interactive sessions, and the
  SDK→CLI control methods (Interrupt, SetModel, GetServerInfo, …).
- `session.go` — shared core: spawns the transport, runs the initialize
  handshake, sends prompts, ends input, hosts the live store mirror.
- `options.go` / `options_apply.go` — `Options` + `With*` functional options and
  their mapping to CLI flags / the initialize request.
- `message.go` / `message_unmarshal.go` / `content.go` — the `Message` and
  `ContentBlock` discriminated unions and their JSON decoders.
- `types.go` — remaining public types (thinking config, sandbox, rate limits,
  context usage, MCP status, server-tool blocks, …).
- `tool.go` / `mcp.go` / `mcp_dispatch.go` — in-process tools (`NewTool[T]`,
  `SdkMcpServer`) and the `mcp_message` JSONRPC dispatch.
- `hooks.go` / `hook_inputs.go` / `permission.go` / `dispatch.go` / `registry.go`
  — hooks, typed hook inputs, permissions (`CanUseTool`), and inbound
  control-request dispatch.
- `sessions.go` / `session_store.go` / `mirror.go` — on-disk session reading,
  the `SessionStore` family, and the live transcript mirror.
- `internal/transport/` — subprocess management, binary discovery, stream-json
  framing, stdin/stdout pumps, end-input, run-as-user (Unix).
- `internal/protocol/` — the bidirectional control protocol: request/response
  correlation, the initialize handshake, inbound dispatch.

# Parity

This is a faithful port of `anthropics/claude-agent-sdk-python`. For parity
audits, clone the upstream source **outside the module tree** (e.g.
`/tmp/agent-sdk-go-parity-ref/`) so the Go toolchain never descends into it, and
pin it to the CLI version the installed `claude` reports (`_cli_version.py`):

```bash
git clone --depth 1 https://github.com/anthropics/claude-agent-sdk-python \
  /tmp/agent-sdk-go-parity-ref/claude-agent-sdk-python
```

Before changing protocol or option behavior, diff against it — names, struct
fields, enum values, and CLI flags. See [PARITY.md](PARITY.md). Verify behavior
with the integration/e2e tests, not just unit tests — static parity is not
behavioral parity.

The known-good CLI version is pinned as `SupportedCLIVersion` in
[claude.go](claude.go) (currently `2.1.159`, matching upstream's
`_cli_version.py`). When upstream bumps its bundled CLI, update the clone, this
const, and re-run the audits. `CLIVersion()` / `CheckCLIVersion()` report the
installed binary's version at runtime.
