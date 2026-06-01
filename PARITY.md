# Parity with the official SDK

This maps the Go port name-for-name against the reference
[`anthropics/claude-agent-sdk-python`](https://github.com/anthropics/claude-agent-sdk-python).
Parity is **mechanically verified** against a clone of the source: the public
`__all__` (**123 names**) and every `ClaudeAgentOptions` field (**45**) are diffed
against the Go exported surface.

- Public names: **123/123** covered (5 documented N/A below).
- Options: **45/45** covered (2 documented N/A below).

Addresses [claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498).

Every wire format was verified against the Python source. Notable details caught
this way: a `CanUseTool` callback does **not** add `--permission-prompt-tool` (the
CLI routes permission requests over the control protocol in stream-json mode);
the `rate_limit_event` frame uses camelCase keys (`resetsAt`, `rateLimitType`…)
though the public type is snake_case; and the session first-prompt summary skips
synthetic lines (`<local-command-stdout>`, `<tick>`, IDE markers, interrupt
notices) and extracts `<command-name>`.

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

8 example programs (Python ships ~16, several being async-runtime variants —
trio, IPython — that have no Go analogue): `query`, `interactive`, `customtool`,
`hooks`, `permission`, `options` (system prompt + setting sources + budget +
partial messages + stderr), `sessions`.

## Not applicable

Public names exported by Python but intentionally absent in Go, with the reason:

- `__version__` → exported as `Version`.
- `Transport` → the transport is an internal abstraction
  (`internal/transport.Transport`); the public API is `Query`/`Client`.
- `HookContext`, `HookEventMessage` → Python typing/runtime shims with no Go
  analogue; hook callbacks receive the raw payload plus typed `Decode*` helpers.
- `McpServerStatusConfig` → an output-only sub-shape of MCP status; folded into
  `McpServerStatusInfo.Config` (raw JSON).
- `ClaudeSDKError` → Python's base exception; Go uses concrete typed errors and
  `errors.Is`/`errors.As`.

Options exported by Python but N/A in Go:

- `debug_stderr` → Go uses `WithStderr(io.Writer)`.
- `output_format` → always `stream-json` (the SDK transport requires it).

Trio / IPython streaming **examples** are Python-async-runtime specific; Go uses
goroutines and `context.Context`, covered by the `interactive` example.
