# Parity with the official SDK

This maps the Go port name-for-name against the reference
[`anthropics/claude-agent-sdk-python`](https://github.com/anthropics/claude-agent-sdk-python).
Parity is **mechanically verified** against a clone of the source at three
levels — names, fields, and enum values — using AST extraction, not eyeballing:

- Public names (`__all__`): **123/123** accounted for — 119 with a Go
  equivalent, 4 documented N/A below.
- `ClaudeAgentOptions` fields: **45/45** covered (2 documented N/A below).
- **Per-type field sets**: every public dataclass/TypedDict field diffed against
  the Go struct (incl. nested-vs-top-level decode sources).
- **Literal value sets**: `PermissionMode` (6), `HookEvent` (10),
  `SessionStoreFlushMode` (batched/eager), `RateLimitType`/`Status`,
  `TaskNotificationStatus`, `ServerToolName`, `ThinkingDisplay`, etc.
- **Inbound control-request fields**: `can_use_tool` delivers the full
  `ToolPermissionContext` (agent_id, blocked_path, decision_reason, title,
  display_name, description), `hook_callback`, `mcp_message`.

Addresses [claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498).

**Verified against Claude Code CLI 2.1.160** — the version the upstream SDK
bundles (`_cli_version.py`), matching the installed binary. In addition to the
static checks above, an integration suite (`go test -tags integration`) runs the
**real binary** for: one-shot query, multi-turn client, custom Go tool, CanUseTool
deny, PreToolUse hook, session resume, interrupt, and adaptive thinking. Static
parity is necessary but not sufficient — two behavioral bugs (a one-shot stdin
hang and a dead `CanUseTool`) were caught only by running the binary, plus a
`--thinking` value bug caught by re-checking against 2.1.159.

Notable wire details verified against the source:

- A `CanUseTool` callback **must** add `--permission-prompt-tool stdio` (set by
  the upstream `_internal/client.py`, emitted by `subprocess_cli.py`); it is
  mutually exclusive with an explicit `WithPermissionPromptToolName`.
- Thinking is driven by the typed `ThinkingConfig` union: `--thinking adaptive`,
  `--max-thinking-tokens N` (enabled — no bare `--thinking`), `--thinking
  disabled`, plus `--thinking-display`.
- `rate_limit_event` uses camelCase keys (`resetsAt`, `rateLimitType`…) though
  the public type is snake_case.
- The session first-prompt summary skips synthetic lines
  (`<local-command-stdout>`, `<tick>`, IDE markers, interrupt notices) and
  extracts `<command-name>`.
- One-shot `Query` closes stdin after the prompt (immediately, or after the first
  result when SDK MCP/hooks/CanUseTool are configured) so the CLI exits.

## Core

| Python | Go |
| --- | --- |
| `query` | `Query`, `Collect` |
| `ClaudeSDKClient` | `Client` |
| `ClaudeAgentOptions` | `Options` + `With*` |
| `Transport` | `internal/transport.Transport` (internal) |
| `create_sdk_mcp_server`, `tool`, `SdkMcpTool` | `NewSdkMcpServer`, `NewTool[T]`, `Tool` |

## Messages & content blocks

| Python | Go |
| --- | --- |
| `Message`, `UserMessage`, `AssistantMessage`, `SystemMessage`, `ResultMessage`, `StreamEvent` | same |
| `TaskNotificationMessage`, `TaskStartedMessage`, `TaskProgressMessage`, `TaskUsage`, `TaskNotificationStatus` | `TaskNotification`, `TaskStartedMessage`, `TaskProgressMessage`, `TaskUsage`, status as field |
| `TextBlock`, `ThinkingBlock`, `ToolUseBlock`, `ToolResultBlock`, `ContentBlock` | same |
| `ServerToolUseBlock`, `ServerToolResultBlock`, `ServerToolName` | same |
| `DeferredToolUse` | `DeferredToolUse` |

## Options, tools, MCP, agents

| Python | Go |
| --- | --- |
| `PermissionMode`, `EffortLevel`, `TaskBudget`, `SettingSource`, `SdkBeta` | same (consts) |
| `McpServerConfig`, `McpSdkServerConfig` | `McpServerConfig`, `*SdkMcpServer`, `StdioMcpServer`, `HTTPMcpServer`, `SSEMcpServer` |
| `McpServerStatus`, `McpServerInfo`, `McpStatusResponse`, `McpToolInfo`, `McpToolAnnotations`, `ToolAnnotations`, `McpServerConnectionStatus` | `McpServerStatusInfo`, `McpServerInfo`, `McpStatusResponse`, `McpToolInfo`, `McpToolAnnotations`, `ToolAnnotations`, `McpServerConnectionStatus` |
| `AgentDefinition` | `AgentDefinition` (full field set) |
| `SdkPluginConfig` | `SdkPluginConfig`, `WithPlugins` |
| `SandboxSettings`, `SandboxNetworkConfig`, `SandboxIgnoreViolations` | same (`WithSandbox`) |

## Permissions & hooks

| Python | Go |
| --- | --- |
| `CanUseTool`, `ToolPermissionContext`, `PermissionResult`, `PermissionResultAllow`, `PermissionResultDeny`, `PermissionUpdate` | `CanUseTool`, `PermissionContext`, `PermissionResult`, `PermissionAllow`, `PermissionDeny`, `PermissionUpdate` |
| `HookCallback`, `HookMatcher`, `HookContext`, hook event consts | `HookCallback`, `HookMatcher`, `HookOutput`, `HookEvent` consts |
| `PreToolUseHookInput`, `PostToolUseHookInput`, `UserPromptSubmitHookInput`, `StopHookInput`, `SubagentStopHookInput`, `PreCompactHookInput`, `NotificationHookInput`, `SubagentStartHookInput`, `PermissionRequestHookInput`, `BaseHookInput` | same + `Decode*` helpers |

## Rate limits & context usage

| Python | Go |
| --- | --- |
| `RateLimitEvent`, `RateLimitInfo`, `RateLimitStatus`, `RateLimitType` | same |
| `ContextUsageCategory`, `ContextUsageResponse` | same (`ContextUsage.Typed()`) |

## Sessions

| Python | Go |
| --- | --- |
| `list_sessions`, `get_session_info`, `get_session_messages` | `ListSessions`, `GetSessionInfo`, `GetSessionMessages` |
| `list_subagents`, `get_subagent_messages` | `ListSubagents`, `GetSubagentMessages` |
| `SDKSessionInfo`, `SessionMessage` | same |
| `SessionStore`, `InMemorySessionStore`, `SessionKey`, `SessionStoreEntry`, `SessionStoreListEntry`, `SessionSummaryEntry`, `SessionListSubkeysKey`, `SessionStoreFlushMode` | same |
| `rename_session`, `tag_session`, `delete_session`, `fork_session` (+ `_via_store`), `ForkSessionResult` | `RenameSessionViaStore`, `TagSessionViaStore`, `DeleteSessionViaStore`, `ForkSessionViaStore`, `ForkSessionResult` |
| `fold_session_summary`, `import_session_to_store` | `FoldSessionSummary`, `ImportSessionToStore` |
| `project_key_for_directory` | `ProjectKeyForDirectory` |
| live `session_store` mirror (transcript_mirror → store) | `WithSessionStore` + `MirrorErrorMessage` |

Session reading is disk-based: it reads the CLI's
`~/.claude/projects/<sanitized-cwd>/<id>.jsonl` transcripts directly, using the
same path-sanitization (non-alphanumeric → `-`, djb2/base-36 hash suffix past
200 bytes) as the official SDK. No running CLI is required.

## Errors

| Python | Go |
| --- | --- |
| `CLINotFoundError`, `ProcessError`, `CLIConnectionError`, `CLIJSONDecodeError`, `ClaudeSDKError` | `CLINotFoundError`, `ProcessError`, `ConnectionError`, `JSONDecodeError`, `MessageParseError`, `ControlProtocolError` |

## Examples

16 example programs mapped 1:1 to the upstream Python examples, plus a few
Go-idiomatic extras. See [examples/README.md](examples/README.md) for the full
table. The only upstream examples not ported are the async-runtime variants
(`streaming_mode_trio`, `streaming_mode_ipython`) — no Go analogue.

## End-to-end coverage

Two test tiers run against the real `claude` binary:

- **`integration`** (smoke, cheap) — `go test -tags integration`: one-shot query,
  multi-turn client, custom tool, CanUseTool, hook, resume, interrupt, thinking.
- **`e2e`** (full, faithful) — `go test -tags e2e`, mirroring upstream's
  `e2e-tests/`:

| Upstream e2e file | Go `e2e` test(s) |
| --- | --- |
| `test_structured_output.py` | `TestE2EStructuredOutputSimple`, `…Enum` |
| `test_dynamic_control.py` | `TestE2ESetModel`, `TestE2ESetPermissionMode` (+ interrupt in integration) |
| `test_hooks.py` / `test_hook_events.py` | `TestE2EHookFires`, `…PermissionDecisionDeny`, `…MultipleHooks` |
| `test_agents_and_settings.py` | `TestE2EAgentDefinition`, `TestE2ESettingSources` |
| `test_sdk_mcp_tools.py` | `TestE2ESdkMcpMultipleTools`, `…PermissionEnforcement` |
| `test_include_partial_messages.py` | `TestE2EPartialMessagesPresentAndAbsent` |
| `test_stderr_callback.py` | `TestE2EStderrCallback` |
| (plugins) | `TestE2EPluginLoaded` |

Plugin note: a plugin's commands are **auto-discovered** from its `commands/`
directory — `plugin.json` does not list them. The loaded plugin appears in the
init `SystemMessage.Plugins` list.

## Not applicable

Public names exported by Python but intentionally absent in Go, with the reason:

- `__version__` → exported as `Version`.
- `Transport` → the transport is an internal abstraction
  (`internal/transport.Transport`); the public API is `Query`/`Client`.
- `HookContext` → a TypedDict whose only field, `signal`, is reserved
  (upstream always passes `None` — abort-signal support is a TODO). The Go
  `HookCallback` already receives a `context.Context`, the idiomatic cancellation
  carrier, so a separate type carries no information today.
- `McpServerStatusConfig` → an output-only union (the type of
  `McpServerStatus.config`); folded into `McpServerStatusInfo.Config` (raw JSON).
- `ClaudeSDKError` → Python's base exception; Go uses concrete typed errors and
  `errors.Is`/`errors.As` (every concrete error in `_errors.py` has a Go
  equivalent).

`HookEventMessage` is now a real Go type (it was previously, wrongly, listed
here) — emitted as a `*HookEventMessage` for `system`/`hook_started`/
`hook_response` frames when `WithIncludeHookEvents` is set.

Options exported by Python but N/A in Go:

- `debug_stderr` → Go uses `WithStderr(io.Writer)`.
- `output_format` → always `stream-json` (the SDK transport requires it).

Trio / IPython streaming **examples** are Python-async-runtime specific; Go uses
goroutines and `context.Context`, covered by the `interactive` example.
