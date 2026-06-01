# Go Port of the Anthropic Claude Agent SDK

## Context

Anthropic ships an official **Claude Agent SDK** in Python and TypeScript, but there is no Go port. This project creates one at `github.com/shindakun/agent-sdk-go` (greenfield — empty dir, Go 1.26).

The crucial architectural fact: the official SDKs are **not** reimplementations of the agent loop. They are thin drivers that spawn the user-installed `claude` Code CLI binary as a subprocess and communicate over newline-delimited `stream-json` on stdin/stdout, plus a bidirectional JSON **control protocol** layered on the same stream. The subprocess owns the agent loop, built-in tools, and context management. The SDK owns: process lifecycle, stream-json framing, control-protocol correlation, and dispatch of in-process callbacks (permissions, hooks, SDK MCP tools).

Decisions (confirmed with user):
- **Architecture:** faithful CLI-subprocess wrapper (do NOT reimplement the loop). Requires `claude` installed.
- **Scope:** full parity with the Python/TS SDKs.
- **API style:** idiomatic Go — `context.Context`, functional options, errors-as-values, `iter.Seq2` for streaming, channels for the interactive client.

Outcome: a Go package where `claude.Query(ctx, "fix the bug", claude.WithAllowedTools("Read","Edit","Bash"))` ranges over typed messages, and a `claude.Client` supports interactive multi-turn sessions with hooks, permissions, and in-process Go tools.

## Reference protocol (verified from the Python SDK source)

- Spawn: `claude --output-format stream-json --verbose --input-format stream-json` + config flags (`--system-prompt`/`--append-system-prompt`, `--allowedTools`/`--disallowedTools`, `--max-turns`, `--max-budget-usd`, `--model`, `--fallback-model`, `--betas`, `--thinking`/`--max-thinking-tokens`/`--effort`, `--settings`, `--add-dir`, `--mcp-config`, `--plugin-dir`, `--json-schema`, `--resume`, …).
- Prompt is sent via **stdin** as a stream-json `user` message (never as a CLI arg), after an `initialize` handshake, so large configs/agents go through initialize.
- Inbound CLI→SDK top-level `type`s: `user`, `assistant`, `system` (subtype `init`/`session_state_changed`), `result` (`is_error`+`errors[]`), `task_notification`, `error`, `end`, plus `control_request`, `control_response`, `control_cancel_request`, `transcript_mirror`.
- **CLI→SDK** `control_request` subtypes the SDK services: `can_use_tool` → `{behavior: allow|deny, updatedInput, message}`; `hook_callback` (`callback_id`, `input`, `tool_use_id`); `mcp_message` (`server_name` + JSONRPC → routed to in-process SDK MCP server, reply `{mcp_response}`).
- **SDK→CLI** `control_request` subtypes the SDK initiates: `initialize` (hooks config `{event:[{matcher, hookCallbackIds, timeout}]}`, agents, skills, `excludeDynamicSections`), `interrupt`, `set_permission_mode`, `set_model`, `mcp_status`, `get_context_usage`, `mcp_reconnect`, `mcp_toggle`, `rewind_files`, `stop_task`.
- Correlation: `request_id` = `req_{counter}_{hex}`; responses are `{type:control_response, response:{subtype:success|error, request_id, response|error}}`. SDK keeps a `request_id → response` pending map and blocks until the match arrives (initialize default 60s).

## Package layout

Root package name **`claude`** (`claude.Query`, `claude.NewClient`, `claude.AssistantMessage`). Mechanism pushed into `internal/`.

```
agent-sdk-go/
  go.mod                      // module github.com/shindakun/agent-sdk-go; go 1.26
  claude.go                   // package doc, version const
  query.go                    // Query(ctx, prompt, ...Option) iter.Seq2[Message,error] + Collect
  client.go                   // Client: Connect/Close/Query/Receive/Messages
  client_control.go           // Interrupt/SetModel/SetPermissionMode/GetContextUsage/Mcp*…
  options.go                  // Options struct + With* functional options
  options_apply.go            // Options -> CLI args / initialize payload mapping
  message.go                  // Message interface + concrete message structs
  message_unmarshal.go        // tagged-JSON decode into concrete types
  content.go                  // ContentBlock interface + block structs
  tool.go / mcp.go            // Tool, ToolHandler, ToolResult, SdkMcpServer, McpServerConfig
  hooks.go                    // HookEvent, HookMatcher, HookCallback, HookOutput
  permission.go               // CanUseTool, PermissionResult (Allow/Deny)
  errors.go                   // typed errors
  internal/transport/         // transport.go, subprocess.go, discover.go
  internal/protocol/          // protocol.go (envelopes), control.go, framing.go
  examples/                   // query, interactive, mcp_tool
```

## Public API (idiomatic Go)

**One-shot** — `iter.Seq2[Message, error]` (Go 1.26 range-over-func is stable; gives natural early-break + cleanup that cancels the subprocess, and inline per-item errors):
```go
func Query(ctx context.Context, prompt string, opts ...Option) iter.Seq2[Message, error]
func Collect(ctx context.Context, prompt string, opts ...Option) ([]Message, error)
```

**Interactive** — long-lived, full-duplex `Client` (iterator is awkward for interleaved send/receive/control, so method + channel based):
```go
func NewClient(opts ...Option) *Client
func (c *Client) Connect(ctx context.Context) error   // spawn + initialize handshake
func (c *Client) Close() error
func (c *Client) Query(ctx context.Context, prompt string) error
func (c *Client) Receive(ctx context.Context) <-chan Result   // Result{Message; Err error}
func (c *Client) Messages(ctx context.Context) iter.Seq2[Message, error]  // sugar over Receive
func (c *Client) Interrupt(ctx) error
func (c *Client) SetModel(ctx, model string) error
func (c *Client) SetPermissionMode(ctx, mode PermissionMode) error
func (c *Client) GetContextUsage(ctx) (ContextUsage, error)
func (c *Client) McpStatus / McpReconnect / McpToggle / RewindFiles / StopTask …
```
`Query` is internally `NewClient` + send prompt + `Messages`, closing when the iterator ends.

## Message & content union

Sealed interface + concrete pointer structs + custom decoder. `UnmarshalMessage(b []byte) (Message, error)` probes `{type}` (and `subtype`) then decodes into the concrete struct. Same `type`-probe in a `ContentBlock` slice's `UnmarshalJSON`.
- Messages: `AssistantMessage`, `UserMessage`, `SystemMessage`, `ResultMessage`, `StreamEvent`, `TaskNotification`.
- Blocks: `TextBlock`, `ThinkingBlock`, `ToolUseBlock`, `ToolResultBlock`.
- Keep a `Raw json.RawMessage` on big structs and **lenient** decoding (no `DisallowUnknownFields`) for forward-compat with CLI changes.

## Options → three destinations (options_apply.go)

`Options` struct + `WithModel/WithSystemPrompt/WithAppendSystemPrompt/WithAllowedTools/WithDisallowedTools/WithMaxTurns/WithMaxBudgetUSD/WithBetas/WithThinking/WithSettings/WithAddDir/WithPermissionMode/WithResume/WithForkSession/WithCwd/WithEnv/WithPluginDir/WithJSONSchema/WithHooks/WithAgents/WithSkills/WithMCPServers/WithSDKMCPServer/WithCanUseTool/WithCLIPath/WithStderr/WithExtraArgs`.

1. **CLI flags** (built at spawn): model, tools, turns, budget, thinking, settings, add-dir, resume, plugins, json-schema; external MCP servers serialize to `--mcp-config`.
2. **initialize request** (first SDK→CLI control_request, awaited): hooks config, agents, skills, `excludeDynamicSections`; SDK (in-process) MCP servers registered by name.
3. **runtime control requests / callbacks**: `CanUseTool` serviced on demand; `SetModel`/`SetPermissionMode`/etc. are post-connect methods.

## Transport (internal/transport)

`Transport` interface (`Connect/Write/Read()<-chan RawLine/Close`) with `subprocessTransport`:
- Spawn via `exec.CommandContext`; set `Dir`, merged `Env` (+ `CLAUDE_CODE_ENTRYPOINT=sdk-go`).
- **Discovery** (discover.go): `Options.CLIPath` → `exec.LookPath("claude")` → common install paths (`~/.claude/local/claude`, npm-global, homebrew) → typed `CLINotFoundError` w/ install hint.
- **Read pump** goroutine using `bufio.Reader.ReadBytes('\n')` (not `Scanner` — avoids the 64 KB token ceiling; stream-json frames can be megabytes). Emit `RawLine{Data}`; on EOF emit terminal `RawLine{Err:io.EOF}` and close.
- **Write** guarded by `sync.Mutex`, append `\n`, flush — serializes prompt + control responses.
- **Stderr** drained to `Options.Stderr` + bounded ring buffer for `ProcessError`.
- **Shutdown**: close stdin → grace wait → ctx-cancel kill; join all goroutines via `WaitGroup`; wrap non-zero exit in `ProcessError{ExitCode, Stderr}`.

## Control protocol (internal/protocol)

`ControlProtocol{ t, mu, counter, pending map[string]chan controlResult, onCanUseTool, onHook, onMcp }`.
- `request_id` = `req_{atomic-counter}_{randHex}`.
- **SDK→CLI blocking** `sendRequest(ctx, subtype, payload)`: buffered(1) result chan in `pending`, write envelope, `select` on result vs `ctx.Done()`; per-request timeout (initialize 60s).
- **Inbound dispatch** (read pump classifies by `type`): `control_response` → deliver to pending; `control_request` → route by subtype to handler **in its own goroutine** (slow callback must not block reading), reply with `control_response`; `control_cancel_request` → cancel that handler's ctx; everything else → forward to the message channel.
- **Initialize handshake** on `Connect` before prompts/iteration.
- Safety: `pending` under mutex; counter atomic; handler goroutines **recover panics** → error response so user callbacks can't kill the pump.

## In-process MCP tools (tool.go, mcp.go)

```go
type ToolHandler func(ctx, args json.RawMessage) (ToolResult, error)
type Tool struct { Name, Description string; InputSchema json.RawMessage; Handler ToolHandler }
func NewTool[T any](name, desc string, fn func(ctx, in T)(ToolResult,error)) Tool  // reflection-derived schema
type SdkMcpServer struct { … }  // NewSdkMcpServer + AddTool
```
Registered via `WithSDKMCPServer(name, server)`. On `mcp_message`, do **manual JSONRPC dispatch** (no external MCP lib): `initialize` → caps; `tools/list` → tool metadata; `tools/call` → decode args, run handler, marshal `ToolResult`; wrap in `{mcp_response}`.

## Hooks & permissions

- `HookEvent` consts (PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStop, PreCompact, Notification, SessionStart, SessionEnd). `HookMatcher{Matcher, Callbacks, Timeout}`, `HookCallback func(ctx, input, toolUseID)(HookOutput,error)`. At initialize, assign stable `hook_callback_id`s, store `map[id]HookCallback`, serialize config; dispatch on `hook_callback`.
- `CanUseTool func(ctx, toolName, input, PermissionContext)(PermissionResult,error)`; `PermissionAllow{UpdatedInput,…}` / `PermissionDeny{Message,Interrupt}` → `{behavior:allow|deny,…}`. Serviced on `can_use_tool`.

## Errors (errors.go)

`CLINotFoundError`, `ConnectionError`, `ProcessError{ExitCode,Stderr}`, `ControlProtocolError`, `JSONDecodeError`, `MessageParseError`; all implement `error`, support `errors.Is/As` via `Unwrap`; sentinel `ErrClosed`.

## Milestones

1. **Walking skeleton:** go.mod, core Options/With*, transport (spawn/discover/read-pump/write/stderr/shutdown), framing + minimal envelope + initialize handshake + prompt-over-stdin, core message/content types + `UnmarshalMessage`, `Query`+`Collect`, base errors. Exit: `Collect(ctx,"say hi")` works against real `claude`.
2. **Control protocol + Client:** pending map/correlation/cancel/timeouts; `Client` Connect/Close/Query/Receive/Messages; `Interrupt/SetModel/SetPermissionMode/GetContextUsage`. Exit: interactive multi-turn with mid-stream SetModel/Interrupt.
3. **Permissions + hooks:** `CanUseTool` + `can_use_tool` dispatch; hook events/matchers/callback-ID registration + `hook_callback` dispatch. Exit: PreToolUse hook blocks a tool; CanUseTool allows/denies with updatedInput.
4. **In-process SDK MCP:** Tool/NewTool[T]/SdkMcpServer/WithSDKMCPServer; `mcp_message` JSONRPC dispatch. Exit: a Go-defined tool is listed and called end-to-end.
5. **Full parity + polish:** remaining flags (fallback-model, betas, thinking/effort, budget, settings, add-dir, plugins, json-schema, agents, skills, external MCP, resume/fork, extra-args); remaining control requests (mcp_status/reconnect/toggle, rewind_files, stop_task, task_notification, partial/stream events, session_state_changed, transcript_mirror); examples, docs, README, CHANGELOG, integration suite. Exit: parity checklist met; `go test -race ./...` green.
6. **Wider parity:** on-disk session reading (`ListSessions`/`GetSessionInfo`/`GetSessionMessages`/`ListSubagents`/`GetSubagentMessages`, reading `~/.claude/projects/<sanitized-cwd>/<id>.jsonl` with the official path-sanitization); typed hook inputs (`Decode*` over the raw payload); typed `TaskNotification` + `TaskUsage`; full `AgentDefinition` field set; `SettingSource`, `SdkPluginConfig`; four more examples. Deferred: session mutations/store, sandbox/rate-limit types, task started/progress messages — tracked in PARITY.md.

## Verification

- **Unit (no subprocess):** inject a `scriptedTransport` implementing the `Transport` interface that replays canned JSON lines and records writes — test framing (large/split lines), control correlation, initialize handshake, timeouts, can_use_tool/hook/mcp round-trips.
- **Subprocess-level (fake binary):** re-exec the test binary in "fake CLI" mode (env flag) that reads stdin stream-json and replays scripted stdout; run real `subprocessTransport` against it for spawn/pipes/shutdown/exit-code/stderr — no `claude` needed.
- **Decode fixtures:** golden JSON captured from real `claude --output-format stream-json` runs in `testdata/` → assert concrete types/fields.
- **Integration (gated):** `//go:build integration` + `skipIfNoCLI` (LookPath + `CLAUDE_SDK_INTEGRATION=1`) running real end-to-end `Query`/`Client` flows.
- Always run `go test -race ./...`.
