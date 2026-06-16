// Package claude is a Go SDK for building agents with Claude Code.
//
// It is a faithful port of Anthropic's official Claude Agent SDK (Python and
// TypeScript). Like those SDKs, it does not reimplement the agent loop: it
// drives the user-installed `claude` Code CLI binary as a subprocess, speaking
// newline-delimited stream-json over the subprocess's stdin/stdout plus a
// bidirectional JSON control protocol layered on the same stream. The CLI owns
// the agent loop, built-in tools, and context management; this SDK owns process
// lifecycle, framing, control-protocol correlation, and dispatch of in-process
// callbacks (permissions, hooks, and SDK MCP tools).
//
// The package offers two entry points:
//
//   - [Query] for one-shot prompts, returning an iterator over typed messages.
//   - [Client] for interactive, multi-turn, full-duplex sessions.
//
// A working `claude` binary must be available on PATH (or supplied via
// [WithCLIPath]) and the appropriate credentials configured in the environment
// (for example ANTHROPIC_API_KEY).
package claude

// Version is the SDK version.
const Version = "0.1.1"

// SupportedCLIVersion is the Claude Code CLI version this SDK was verified
// against — the version the upstream `claude-agent-sdk-python` bundles
// (`_cli_version.py`). Unlike the Python/TS SDKs, this SDK does not bundle the
// CLI; it requires `claude` on PATH. The pin records the known-good version for
// parity audits and lets callers warn on a mismatch via [CheckCLIVersion].
const SupportedCLIVersion = "2.1.178"
