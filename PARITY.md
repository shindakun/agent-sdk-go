# Parity with the official SDK

This maps the Go port name-for-name against the reference
[`anthropics/claude-agent-sdk-python`](https://github.com/anthropics/claude-agent-sdk-python)
(`__all__`, ~144 names). The Go port covers the full public surface; remaining
items are noted in [Not applicable](#not-applicable) with the reason.

Addresses [claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498).

Every wire format was verified against the Python source. Notably, a `CanUseTool`
callback does **not** add `--permission-prompt-tool`; in stream-json mode the CLI
routes permission requests to the SDK over the control protocol.

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
| `project_key_for_directory` | `ProjectKeyForDirectory` |

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

- `__version__` → exported as `Version`.
- Trio / IPython streaming examples — Python-async-runtime specific; Go uses
  goroutines and `context.Context`, covered by the `interactive` example.
- Disk-backed session mutations: the official `sessions.py` implements only
  reading; mutations operate through a `SessionStore`. The Go port matches that
  (mutations are the `*ViaStore` functions); the `--resume` / `--fork-session`
  CLI flags are also available via `WithResume` / `WithForkSession`.
