# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Verified against Claude Code CLI 2.1.159

- Re-synced to the upstream `claude-agent-sdk-python` at the bundled CLI version
  2.1.159; re-ran the mechanical name/field/value diffs (still 123/123 names,
  45/45 options) and the live integration suite.

### Fixed

- **`--thinking` flag emission.** The SDK emitted a bare `--thinking` (rejected
  by the CLI). Replaced `WithThinking(maxTokens, effort)` with the typed
  `WithThinkingConfig` (adaptive → `--thinking adaptive`, enabled →
  `--max-thinking-tokens N`, disabled → `--thinking disabled`),
  `WithMaxThinkingTokens`, and `WithThinkingDisplay`; effort stays separate via
  `WithEffort`.
- **`CanUseTool` was non-functional.** It now adds `--permission-prompt-tool
  stdio` so the CLI routes permission requests to the SDK (mutually exclusive
  with `WithPermissionPromptToolName`). Verified end-to-end.
- **One-shot `Query` hang.** `Query` now closes stdin after the prompt so the CLI
  exits; the bidirectional path waits for the first result first.

### Added

- `CLAUDE.md`, this changelog, and 9 new examples (16 total) mapped 1:1 to the
  upstream Python examples plus Go-idiomatic extras (collect, interrupt,
  thinking). See `examples/README.md`.
- A two-tier test suite against the real `claude` binary: `integration` (smoke)
  and `e2e` (full, mirroring upstream's `e2e-tests/` — structured output, dynamic
  control, hook variants, setting sources, SDK MCP permission enforcement,
  partial messages, agents, plugins). Run with `go test -tags e2e`.
- `scripts/run-examples.sh` runs every example against the live binary.
- `SystemMessage.Plugins` (`[]PluginInfo`) populated from the init message,
  matching upstream's `data["plugins"]`. The `plugins` example now inspects this
  typed field instead of asking the model.

## Earlier (M1–M8)

The initial implementation, built milestone-by-milestone against the verified
official wire protocol:

- M1 — walking skeleton: `Query`/`Collect` over the subprocess; transport;
  control engine + initialize handshake; typed message/content union; errors.
- M2 — control protocol + interactive `Client`; SDK→CLI control methods.
- M3 — permissions (`CanUseTool`) and lifecycle hooks.
- M4 — in-process SDK MCP tools (`NewTool[T]`, `mcp_message` dispatch).
- M5–M8 — full name-for-name parity: all options/flags, session reading + store
  + mutations, sandbox/rate-limit/context-usage types, server-tool blocks, task
  lifecycle messages, typed hook inputs; mechanically verified 123/123 names.
