# Examples

Each example is a standalone `package main`. Run one with:

```bash
go run ./examples/query
```

All require a working `claude` binary on `PATH` and Claude Code auth (see the
top-level [README](../README.md)).

## Mapping to the upstream Python SDK

These mirror the examples in
[`claude-agent-sdk-python/examples`](https://github.com/anthropics/claude-agent-sdk-python/tree/main/examples).

| This example | Upstream | Demonstrates |
| --- | --- | --- |
| [query](query) | `quick_start.py` | One-shot `Query` over an iterator |
| [interactive](interactive) | `streaming_mode.py` | Multi-turn `Client` from stdin |
| [customtool](customtool) | `mcp_calculator.py` | In-process Go tool via `NewTool`/`SdkMcpServer` |
| [hooks](hooks) | `hooks.py` | `PreToolUse` hook with typed input |
| [permission](permission) | `tool_permission_callback.py` | `CanUseTool` allow/deny |
| [agents](agents) | `agents.py` | Subagents via `WithAgents`/`AgentDefinition` |
| [filesystem](filesystem) | `filesystem_agents.py` | Read/Write/Edit in a working dir |
| [tools_option](tools_option) | `tools_option.py` | Restrict tools with `WithToolList` |
| [partial_messages](partial_messages) | `include_partial_messages.py` | `StreamEvent` partial deltas |
| [stderr](stderr) | `stderr_callback_example.py` | Capture subprocess stderr |
| [options](options) | `system_prompt.py`, `setting_sources.py`, `max_budget_usd.py` | System prompt + setting sources + budget + partial + stderr |
| [plugins](plugins) | `plugin_example.py` | Load a local plugin (`demo-plugin/`) |
| [sessions](sessions) | `session_stores/` | On-disk session reading |
| [thinking](thinking) | — (Go addition) | Typed `WithThinkingConfig` |
| [collect](collect) | — (Go addition) | `Collect` + typed result fields |
| [interrupt](interrupt) | — (Go addition) | `Client.Interrupt` mid-turn |

## Not ported

`streaming_mode_ipython.py` and `streaming_mode_trio.py` are Python async-runtime
variants with no Go analogue — goroutines and `context.Context` cover that ground
(see [interactive](interactive)).
