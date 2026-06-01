# Parity with the official SDK

This tracks how the Go port compares to the reference
[`anthropics/claude-agent-sdk-python`](https://github.com/anthropics/claude-agent-sdk-python).
The Python package exports ~144 public names; the Go port currently covers the
core agent-driving surface (M1–M5) and defers a session-management subsystem and
several typed helpers to follow-up milestones.

Addresses [claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498).

## Covered (M1–M5)

| Capability | Python | Go |
| --- | --- | --- |
| One-shot query | `query` | `Query`, `Collect` |
| Interactive client | `ClaudeSDKClient` | `Client` |
| Options | `ClaudeAgentOptions` | `Options` + `With*` |
| Messages | `AssistantMessage`, `UserMessage`, `SystemMessage`, `ResultMessage`, `StreamEvent` | same |
| Content blocks | `TextBlock`, `ThinkingBlock`, `ToolUseBlock`, `ToolResultBlock` | same |
| In-process tools | `create_sdk_mcp_server`, `tool` | `NewSdkMcpServer`, `NewTool[T]` |
| External MCP | `McpServerConfig` (stdio/http/sse) | `StdioMcpServer`, `HTTPMcpServer`, `SSEMcpServer` |
| Permissions | `CanUseTool`, `PermissionResultAllow/Deny` | `CanUseTool`, `PermissionAllow/Deny` |
| Hooks | events, `HookMatcher`, callbacks | same (event consts, `HookMatcher`, `HookCallback`) |
| Subagents | `AgentDefinition` | `AgentDefinition` |
| Control requests | interrupt, set_model, set_permission_mode, get_context_usage, mcp_status/reconnect/toggle, stop_task, rewind_files | same |
| Errors | `CLINotFoundError`, `ProcessError`, `CLIConnectionError`, `CLIJSONDecodeError` | `CLINotFoundError`, `ProcessError`, `ConnectionError`, `JSONDecodeError`, `MessageParseError` |

Wire format for each was verified against the Python source. Notably, a
`CanUseTool` callback does **not** add `--permission-prompt-tool`; in stream-json
mode the CLI routes permission requests to the SDK over the control protocol.

## Deferred (M6+)

- **Session management** — `list_sessions`, `get_session_info`,
  `get_session_messages`, `list_subagents`, `get_subagent_messages`, and the
  `SessionStore` family (`InMemorySessionStore`, fork/rename/tag/delete). The Go
  port currently exposes only the `--resume` / `--fork-session` flags via
  `WithResume` / `WithForkSession`, not the session query/store API.
- **Typed hook inputs** — Python has `PreToolUseHookInput`,
  `PostToolUseHookInput`, etc. The Go port delivers the raw event payload as
  `json.RawMessage`; typed structs are a future ergonomic layer.
- **Sandbox settings** — `SandboxSettings`, `SandboxNetworkConfig`.
- **Rate-limit types** — `RateLimitInfo`, `RateLimitStatus`, `RateLimitEvent`.
- **Task progress messages** — `TaskStartedMessage`, `TaskProgressMessage`
  (currently surfaced generically as `TaskNotification`).
- **Plugins** — `SdkPluginConfig` (the `--plugin-dir` flag is supported via
  `WithPluginDir`, but not the structured plugin config).
- **Examples** — Python ships ~16; the Go port ships 3 (query, interactive,
  customtool). Planned additions that map to already-implemented features:
  hooks, tool-permission callback, system prompt, setting sources, max budget,
  include-partial-messages, stderr callback.
