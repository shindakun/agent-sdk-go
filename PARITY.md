# Parity with the official SDK

This maps the Go port name-for-name against the reference
[`anthropics/claude-agent-sdk-python`](https://github.com/anthropics/claude-agent-sdk-python).
Parity is **mechanically verified** against a clone of the source at three
levels — names, fields, and enum values — using AST extraction, not eyeballing:

- Public names (`__all__`): **133/133** accounted for: 127 with a Go
  equivalent, 6 documented N/A below.
- `ClaudeAgentOptions` fields: **49/49** covered (`debug_stderr` N/A below).
  `output_format`'s JSON schema is `WithJSONSchema`; `thinking` is
  `WithThinkingConfig`.
- **Per-type field sets**: every public dataclass/TypedDict field diffed against
  the Go struct (incl. nested-vs-top-level decode sources). Control-protocol
  TypedDicts (`SDKControl*`) map to internal request types; system-prompt and
  tools-preset dicts map to options.
- **Literal value sets**: all 18 `Literal` aliases in `types.py` have their
  values as Go constants: `PermissionMode` (6), `HookEvent` (10),
  `EffortLevel`, `SettingSource`, `SdkBeta`, `PermissionUpdateDestination`,
  `PermissionBehavior`, `McpServerConnectionStatus`, `ServerToolName`,
  `AssistantMessageError`, `MessageOriginKind`, `TaskNotificationOriginSubkind`,
  `TaskNotificationStatus`, `TaskUpdatedStatus`, `RateLimitStatus`,
  `RateLimitType`, `SessionStoreFlushMode`, `ThinkingDisplay`.
- **Inbound control-request fields**: `can_use_tool` delivers the full
  `ToolPermissionContext` (agent_id, blocked_path, decision_reason, title,
  display_name, description), `hook_callback`, `mcp_message`.

Addresses [claude-agent-sdk-python#498](https://github.com/anthropics/claude-agent-sdk-python/issues/498).

**Verified against Claude Code CLI 2.1.280**, the version the upstream SDK
bundles (`_cli_version.py`), matching the installed binary. In addition to the
static checks above, the integration and e2e suites run the **real binary**; the
e2e suite ports every file in upstream's `e2e-tests/` (table below). Static
parity is necessary but not sufficient: several bugs (a one-shot stdin hang, a
dead `CanUseTool`, unread tool annotations, subagent transcripts not found)
showed up only against the binary.

Notable wire details verified against the source:

- A `CanUseTool` callback **must** add `--permission-prompt-tool stdio` (set by
  the upstream `_internal/client.py`, emitted by `subprocess_cli.py`); it is
  mutually exclusive with an explicit `WithPermissionPromptToolName`.
- Thinking is driven by the typed `ThinkingConfig` union: `--thinking adaptive`,
  `--max-thinking-tokens N` (enabled — no bare `--thinking`), `--thinking
  disabled`, plus `--thinking-display`.
- Unknown top-level frame types are skipped, as upstream's parser does.
- `rate_limit_event` uses camelCase keys (`resetsAt`, `rateLimitType`…) though
  the public type is snake_case.
- The session first-prompt summary skips synthetic lines
  (`<local-command-stdout>`, `<tick>`, IDE markers, interrupt notices) and
  extracts `<command-name>`.
- One-shot `Query` closes stdin after the prompt (immediately, or after the first
  result when SDK MCP/hooks/CanUseTool are configured) so the CLI exits.
- `system_prompt` forms: a string or `{"type": "custom"}` is `WithSystemPrompt`,
  `{"type": "preset", "append": ...}` is `WithAppendSystemPrompt`, `{"type":
  "file"}` is `WithSystemPromptFile`. Their `snapshot` key is
  `WithSystemPromptSnapshot`, sent as `systemPromptSnapshot` in initialize for
  the custom and preset forms only.
- `verbatim_prompts` is SDK-side, not a CLI flag: `WithVerbatimPrompts` adds
  `"client_composed": true` to each user frame.
- `setting_sources=None` sends no flag; `setting_sources=[]` sends
  `--setting-sources=` (load no settings). `WithSettingSources()` with no
  arguments is the empty form.

## Core

| Python | Go |
| --- | --- |
| `query` (string prompt / async-iterable prompt) | `Query`, `Collect` / `QueryMessages` |
| `ClaudeSDKClient` (`query` with a string / async iterable) | `Client` (`Query` / `QueryMessages`) |
| `__version__` | `Version` |
| `ClaudeAgentOptions` | `Options` + `With*` |
| `Transport` | `internal/transport.Transport` (internal) |
| `create_sdk_mcp_server`, `tool`, `SdkMcpTool` | `NewSdkMcpServer`, `NewTool[T]`, `Tool` |

## Messages & content blocks

| Python | Go |
| --- | --- |
| `Message`, `UserMessage`, `AssistantMessage`, `SystemMessage`, `ResultMessage`, `StreamEvent` | same |
| `TaskNotificationMessage`, `TaskStartedMessage`, `TaskProgressMessage`, `TaskUpdatedMessage`, `TaskUsage`, `TaskNotificationStatus`, `TaskUpdatedStatus`, `TERMINAL_TASK_STATUSES` | `TaskNotificationMessage`, `TaskStartedMessage`, `TaskProgressMessage`, `TaskUpdatedMessage`, `TaskUsage`, status consts, `TerminalTaskStatuses`/`IsTerminalTaskStatus` |
| `TextBlock`, `ThinkingBlock`, `ToolUseBlock`, `ToolResultBlock`, `ContentBlock` | same |
| `ServerToolUseBlock`, `ServerToolResultBlock`, `ServerToolName` | same |
| `DeferredToolUse` | `DeferredToolUse` |
| `CanUseToolShadowedWarning` | N/A: Go has no `warnings` module. The condition is reported by `CanUseToolShadowed(opts...)` and written to the `WithStderr` writer on connect. |
| `ModelUsage` | `ModelUsage` (`ResultMessage.ModelUsage`, camelCase wire keys) |
| `ConversationResetMessage` | `ConversationResetMessage` |
| `MessageOrigin`, `MessageOriginKind`, `TaskNotificationOriginSubkind` | `MessageOrigin` (+ `Raw` for unmodeled keys), `MessageOriginKind` consts, `TaskNotificationOriginSubkind` consts |

## Options, tools, MCP, agents

| Python | Go |
| --- | --- |
| `PermissionMode`, `EffortLevel`, `TaskBudget`, `SettingSource`, `SdkBeta` | same (consts) |
| `McpServerConfig`, `McpSdkServerConfig` | `McpServerConfig`, `*SdkMcpServer`, `StdioMcpServer`, `HTTPMcpServer`, `SSEMcpServer` |
| `McpServerStatus`, `McpServerInfo`, `McpStatusResponse`, `McpToolInfo`, `McpToolAnnotations`, `McpServerConnectionStatus` | `McpServerStatusInfo`, `McpServerInfo`, `McpStatusResponse`, `McpToolInfo`, `McpToolAnnotations`, `McpServerConnectionStatus` |
| `ToolAnnotations` (MCP hints + `maxResultSizeChars`) | `ToolAnnotations` (`maxResultSizeChars` sent in `_meta`) |
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
| `list_sessions`, `get_session_info`, `get_session_messages` | `ListSessions` (+ `ListIncludeWorktrees`), `GetSessionInfo`, `GetSessionMessages` |
| `list_subagents`, `get_subagent_messages` | `ListSubagents`, `GetSubagentMessages` |
| `list_sessions_from_store`, `get_session_info_from_store`, `get_session_messages_from_store`, `list_subagents_from_store`, `get_subagent_messages_from_store` | `ListSessionsFromStore`, `GetSessionInfoFromStore`, `GetSessionMessagesFromStore`, `ListSubagentsFromStore`, `GetSubagentMessagesFromStore` |
| `SDKSessionInfo`, `SessionMessage` | same |
| `SessionStore`, `InMemorySessionStore`, `SessionKey`, `SessionStoreEntry`, `SessionStoreListEntry`, `SessionSummaryEntry`, `SessionListSubkeysKey`, `SessionStoreFlushMode` | same |
| `rename_session`, `tag_session`, `delete_session`, `fork_session`, `ForkSessionResult` | `RenameSession`, `TagSession`, `DeleteSession`, `ForkSession`, `ForkSessionResult` |
| `rename_session_via_store`, `tag_session_via_store`, `delete_session_via_store`, `fork_session_via_store` | `RenameSessionViaStore`, `TagSessionViaStore`, `DeleteSessionViaStore`, `ForkSessionViaStore` |
| `fold_session_summary`, `import_session_to_store` | `FoldSessionSummary`, `ImportSessionToStore` |
| `project_key_for_directory` | `ProjectKeyForDirectory` |
| live `session_store` mirror (transcript_mirror → store) | `WithSessionStore` + `MirrorErrorMessage` |
| store-backed resume (`session_resume.py`: materialize into a temp `CLAUDE_CONFIG_DIR`) | `WithSessionStore` + `WithResume`/`WithContinueConversation`, bounded by `WithLoadTimeout` |

Session reading is disk-based: it reads the CLI's
`<config dir>/projects/<project key>/<id>.jsonl` transcripts and
`<id>/subagents/**/agent-<id>.jsonl` subagent transcripts directly, with the
official SDK's path resolution (realpath, `CLAUDE_CONFIG_DIR`, non-alphanumeric
→ `-`, djb2/base-36 hash suffix past 200 bytes, worktree lookup) and chain
building. No running CLI is required. Upstream also NFC-normalizes paths and
NFKC-normalizes tags; that needs Unicode tables outside Go's standard library
and is not done.

In-process MCP servers: upstream serves them with the Python mcp library; the
Go dispatch reproduces that wire behavior (version negotiation, capabilities,
JSON-RPC errors, cancellation, argument validation with jsonschema's
messages), as its tests port the relevant cases of
`test_sdk_mcp_integration.py`.

## Errors

| Python | Go |
| --- | --- |
| `CLINotFoundError`, `ProcessError`, `CLIConnectionError`, `CLIJSONDecodeError`, `ClaudeSDKError` | `CLINotFoundError`, `ProcessError`, `ConnectionError`, `JSONDecodeError`, `MessageParseError`, `ControlProtocolError` |
| `ResultError` (subclass of `ProcessError`) | `ResultError` (unwraps to `*ProcessError`) |

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
| `test_subagent_session_reads.py` | `TestE2ESubagentMessagesCarryParentToolUseID` |
| (MCP annotations, via `mcp_status`) | `TestE2ESdkMcpAnnotationsReachCLI` |
| `test_forward_subagent_text.py` | `TestE2EForwardSubagentTextDeliversAttributedText`, `TestE2ESubagentTextNotForwardedByDefault` |
| `test_truncating_resume.py` | `TestE2ETruncatingResumeMatchingDropsTurn`, `TestE2ETruncatingResumeWrongDropsTurnRefused` |
| `test_error_results.py` | `TestE2EAPIErrorYieldsResultError` |
| `test_conversation_reset.py` | `TestE2EClearEmitsConversationReset` |
| `test_message_origin.py` | `TestE2EResultOriginRoundTrip` (the stamped frame is written directly; the Go API sends string prompts) |
| `test_session_store_resume_settings.py` | `TestE2ESessionStoreResumeAppliesUserSettings` |
| `test_verbatim_prompts.py` | `TestE2EAtPathExpandedByDefault`, `TestE2EVerbatimQueryNotExpanded`, `TestE2EVerbatimClientNotExpanded` |
| `test_tool_permissions.py` | `TestIntegrationCanUseToolOnlyStringPrompt`, `TestIntegrationCanUseToolDeny` (integration tier) |
| (plugins) | `TestE2EPluginLoaded` |

Plugin note: a plugin's commands are **auto-discovered** from its `commands/`
directory — `plugin.json` does not list them. The loaded plugin appears in the
init `SystemMessage.Plugins` list.

## Not applicable

Public names exported by Python but intentionally absent in Go, with the reason:

- `Transport` → the transport is an internal abstraction
  (`internal/transport.Transport`); the public API is `Query`/`Client`.
- `HookContext` → a TypedDict whose only field, `signal`, is reserved
  (upstream always passes `None` — abort-signal support is a TODO). The Go
  `HookCallback` already receives a `context.Context`, the idiomatic cancellation
  carrier, so a separate type carries no information today.
- `McpServerStatusConfig` → an output-only union (the type of
  `McpServerStatus.config`); folded into `McpServerStatusInfo.Config` (raw JSON).
- `HookInput` → the union of hook input types; a `HookCallback` receives the
  raw input and decodes it with the `Decode*` helpers.
- `CanUseToolShadowedWarning` → Python's warnings category; see
  `CanUseToolShadowed` above.
- `ClaudeSDKError` → Python's base exception; Go uses concrete typed errors and
  `errors.Is`/`errors.As` (every concrete error in `_errors.py` has a Go
  equivalent).

`HookEventMessage` is now a real Go type (it was previously, wrongly, listed
here) — emitted as a `*HookEventMessage` for `system`/`hook_started`/
`hook_response` frames when `WithIncludeHookEvents` is set.

Options exported by Python but N/A in Go:

- `debug_stderr` → Go uses `WithStderr(io.Writer)`.

Trio / IPython streaming **examples** are Python-async-runtime specific; Go uses
goroutines and `context.Context`, covered by the `interactive` example.
