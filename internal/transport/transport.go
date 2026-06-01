// Package transport manages the `claude` CLI subprocess and the newline-
// delimited stream-json framing on its stdin/stdout. It is internal to the SDK;
// the root package wraps its errors into the public error types.
package transport

import (
	"context"
	"io"
)

// RawLine is one framed stream-json line read from the subprocess. Exactly one
// of Data or Err is set; Err set to [io.EOF] marks the end of the stream.
type RawLine struct {
	Data []byte
	Err  error
}

// Transport drives a CLI subprocess over stream-json.
type Transport interface {
	// Connect spawns the subprocess and starts the read/write pumps. The
	// provided context governs the lifetime of the subprocess.
	Connect(ctx context.Context) error
	// Write sends one JSON object (a newline is appended) to the subprocess
	// stdin. Writes are serialized internally.
	Write(ctx context.Context, obj []byte) error
	// EndInput closes the subprocess stdin, signaling end-of-input so the CLI
	// finishes the current turn and exits. Further writes fail afterward.
	EndInput() error
	// Read returns the channel of framed lines. It is closed when the stream
	// ends; the final line carries Err == io.EOF.
	Read() <-chan RawLine
	// Close shuts the subprocess down gracefully and returns any process error.
	Close() error
}

// Config configures a subprocess transport.
type Config struct {
	// CLIPath, if set, is used directly; otherwise the binary is discovered.
	CLIPath string
	// Args are the configuration flags appended after the fixed base flags.
	Args []string
	// Cwd is the subprocess working directory.
	Cwd string
	// Env holds extra environment variables merged over the process environment.
	Env map[string]string
	// Stderr, if set, receives the subprocess's stderr in addition to the
	// internal capture buffer.
	Stderr io.Writer
	// SDKVersion is reported to the CLI via CLAUDE_AGENT_SDK_VERSION.
	SDKVersion string
	// MaxBufferSize caps a single stream-json line (bytes); 0 uses the default.
	MaxBufferSize int
	// EnableFileCheckpointing sets CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING.
	EnableFileCheckpointing bool
	// UID/GID, when UID is non-nil, run the subprocess as that OS user (Unix).
	UID *int
	GID int
}

// DefaultMaxBufferSize bounds a single stream-json line by default (10 MiB).
const DefaultMaxBufferSize = 10 << 20

// CLINotFoundError reports that the CLI binary could not be located.
type CLINotFoundError struct {
	Path string
	Hint string
}

func (e *CLINotFoundError) Error() string {
	if e.Path != "" {
		return "claude CLI not found at " + e.Path + ": " + e.Hint
	}
	return "claude CLI not found on PATH: " + e.Hint
}

// ProcessError reports a non-zero subprocess exit.
type ProcessError struct {
	ExitCode int
	Stderr   string
}

func (e *ProcessError) Error() string {
	return "claude CLI exited non-zero"
}
