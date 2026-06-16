# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Added

- **`TaskUpdatedMessage`** (and `TaskUpdatedStatus`, `TerminalTaskStatuses` /
  `IsTerminalTaskStatus`) — ports upstream's new `system`/`task_updated` lifecycle
  event (claude-agent-sdk-python `141c37f`). A background task's terminal state
  can arrive *only* here (e.g. a task stopped via `StopTask` reports
  `Status: "killed"` with no accompanying `TaskNotificationMessage`); use
  `IsTerminalTaskStatus` to detect completion across both message types. Decoded
  defensively — the patch may omit fields and parsing never fails on a lifecycle
  event.

### Re-synced to Claude Code CLI 2.1.178

- Bumped `SupportedCLIVersion` to `2.1.178`. The 2.1.175→2.1.178 range was CLI
  bumps plus the one `task_updated` feature above (verified via the aggregate
  `de4562d...HEAD` diff: no other SDK source changed). Name diff now **126/126**
  (122 covered, 4 N/A) — the feature added `TaskUpdatedMessage`,
  `TaskUpdatedStatus`, and `TERMINAL_TASK_STATUSES`. Static, unit `-race`,
  integration, and an e2e subset green against the live binary; `CheckCLIVersion`
  confirms installed == pinned.

## [v0.1.1] - 2026-06-12

### Re-synced to Claude Code CLI 2.1.175

- Bumped `SupportedCLIVersion` to `2.1.175`. The upstream range from 2.1.161 to
  2.1.175 (commits `6772ffc`…`de4562d`) was a run of pure CLI-version bumps — the
  only `src/claude_agent_sdk/` changes besides `_cli_version.py` were `_version.py`
  (the Python package version) and a test-conformance helper, neither of which is
  SDK source to port (verified via the aggregate `8e11815...de4562d` diff). No
  port work needed.
- Re-ran the name diff against the 2.1.175 reference: 0 missing (119/123, 4 N/A).
  Static (build/vet/gofmt/golangci-lint) clean; unit `-race`, integration, and an
  e2e subset (structured output, hook deny, SDK MCP permission enforcement,
  plugin) green against the live binary. `CheckCLIVersion` confirms installed ==
  pinned (2.1.175). The bumps were surfaced and correctly classified by the
  upstream-watch workflow.

## [v0.1.0] - 2026-06-03

### Summary

First tagged release: a faithful, idiomatic-Go port of
`anthropics/claude-agent-sdk-python`, verified name-for-name (119/123 public
names, 4 documented N/A; 45/45 options) and behaviorally against Claude Code CLI
**2.1.161** via integration and e2e suites. Includes `Query`/`Collect`, the
interactive `Client` (with `ReceiveResponse`), in-process SDK MCP tools, hooks,
permissions, sessions + store, the full typed message/option surface, 16
examples, CI (lint/test/e2e), the upstream-watch automation, and a pinned
`SupportedCLIVersion`. Details below.

### Re-synced to Claude Code CLI 2.1.161

- Bumped `SupportedCLIVersion` to `2.1.161`. Upstream commit `8e11815` was a pure
  `_cli_version.py` bump (no SDK source change — verified via the per-commit
  diff), so no port work was needed. Re-ran the name diff (0 missing, 119/123
  covered) and the integration + e2e tiers against 2.1.161; all green.
  `CheckCLIVersion` confirms installed == pinned.

### Re-synced to Claude Code CLI 2.1.160

- Bumped `SupportedCLIVersion` to `2.1.160`. Upstream commit `6d93523` was a
  pure `_cli_version.py` bump (no SDK source change — verified via the
  per-commit diff), so no port work was needed. Re-ran the name diff (0 missing,
  119/123 covered) and the integration + e2e tiers against 2.1.160; all green.
  `CheckCLIVersion` confirms installed == pinned. This was the first bump
  surfaced by the upstream-watch workflow, which correctly classified it as a
  CLI bump.

### Verified against Claude Code CLI 2.1.159

- Re-synced to the upstream `claude-agent-sdk-python` at the bundled CLI version
  2.1.159; re-ran the mechanical name/field/value diffs and the live integration
  suite.

### Fixed

- **Skills were non-functional.** `WithSkills` only sent skills via the
  initialize request; the CLI also needs the `Skill(name)` tool in
  `--allowedTools` and a `--setting-sources` default to discover them. Now
  replicates the official `_apply_skills_defaults`.
- **Live `SessionStore` mirror produced nothing.** `WithSessionStore` didn't emit
  `--session-mirror`, so the CLI never sent `transcript_mirror` frames. Now it
  does (verified live: the store receives entries).
- **Default system prompt.** With no system prompt configured, the SDK now emits
  `--system-prompt ""` (matching upstream, which suppresses Claude Code's default)
  rather than omitting the flag.
- Added `HookEventMessage` (system/`hook_started`/`hook_response` frames under
  `WithIncludeHookEvents`), `WithSystemPromptFile` (`--system-prompt-file`), and
  `WithMCPConfig` (the string/path form of `mcp_servers`). These were gaps found
  in a deeper functional sweep against the source.
- `Client.ReceiveResponse` (mirrors `receive_response`: yields until and
  including the next `ResultMessage`, then stops) and `Client.QuerySession`
  (mirrors `query`'s `session_id` argument for multi-session routing).
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

- **Upstream watch** workflow + `scripts/upstream-watch.sh`: a daily (and
  on-demand) job that watches `anthropics/claude-agent-sdk-python` and files
  triage issues here — a rollup for CLI-version bumps, Claude-triaged labeled
  issues for SDK-source commits (with diff link + port recommendation), and
  ignores docs/test/example commits. Idempotent, `DRY_RUN` support, and
  prompt-injection hardened (commit messages/diffs treated as untrusted data; the
  model can only suggest a label, never act). Needs an `ANTHROPIC_API_KEY` secret.
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
- `SupportedCLIVersion` constant (mirrors upstream's `_cli_version.py`, pinned to
  `2.1.159`) plus `CLIVersion()` / `CheckCLIVersion()` to report the installed
  binary's version and flag a mismatch.
- GitHub Actions CI: `Lint` (gofmt/vet/build + golangci-lint) and `Test`
  (`go test -race` across Linux/macOS/Windows) on every PR/push; a manual-only
  `E2E` workflow that installs `claude` and runs the e2e tests + examples (the Go
  analogues of upstream's `lint.yml` / `test.yml`; Python packaging workflows
  have no Go equivalent).
- `.golangci.yml` (golangci-lint v2) — standard linters plus bodyclose, misspell,
  unconvert, and revive (with the noisiest revive rules disabled); lints the
  integration/e2e-tagged files too. The tree is golangci-lint-clean.

## Earlier (M1–M8)

The initial implementation, built milestone-by-milestone against the verified
official wire protocol:

- M1 — walking skeleton: `Query`/`Collect` over the subprocess; transport;
  control engine + initialize handshake; typed message/content union; errors.
- M2 — control protocol + interactive `Client`; SDK→CLI control methods.
- M3 — permissions (`CanUseTool`) and lifecycle hooks.
- M4 — in-process SDK MCP tools (`NewTool[T]`, `mcp_message` dispatch).
- M5–M8 — full name-for-name parity: all options/flags, session reading, store,
  mutations, sandbox/rate-limit/context-usage types, server-tool blocks, task
  lifecycle messages, typed hook inputs; mechanically verified 123/123 names.
