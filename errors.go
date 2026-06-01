package claude

import (
	"errors"
	"fmt"
)

// ErrClosed is returned when an operation is attempted on a Client or transport
// that has already been closed.
var ErrClosed = errors.New("claude: closed")

// CLINotFoundError indicates the `claude` Code CLI binary could not be located.
type CLINotFoundError struct {
	// Path is the path that was tried, if an explicit one was configured.
	Path string
	// Hint is a human-readable suggestion for resolving the problem.
	Hint string
}

func (e *CLINotFoundError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("claude: CLI not found at %q: %s", e.Path, e.Hint)
	}
	return fmt.Sprintf("claude: CLI not found on PATH: %s", e.Hint)
}

// ConnectionError wraps a lower-level failure establishing or maintaining the
// connection to the CLI subprocess.
type ConnectionError struct {
	Err error
}

func (e *ConnectionError) Error() string { return "claude: connection error: " + e.Err.Error() }
func (e *ConnectionError) Unwrap() error { return e.Err }

// ProcessError reports that the CLI subprocess exited with a non-zero status.
type ProcessError struct {
	ExitCode int
	Stderr   string
}

func (e *ProcessError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("claude: CLI exited with code %d: %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("claude: CLI exited with code %d", e.ExitCode)
}

// ControlProtocolError reports a failure in the bidirectional control protocol,
// such as an error response to a control request.
type ControlProtocolError struct {
	Subtype   string
	RequestID string
	Message   string
}

func (e *ControlProtocolError) Error() string {
	return fmt.Sprintf("claude: control protocol error (subtype=%q request_id=%q): %s",
		e.Subtype, e.RequestID, e.Message)
}

// JSONDecodeError reports a stream-json line that could not be decoded.
type JSONDecodeError struct {
	Line []byte
	Err  error
}

func (e *JSONDecodeError) Error() string {
	return fmt.Sprintf("claude: failed to decode JSON line: %v", e.Err)
}
func (e *JSONDecodeError) Unwrap() error { return e.Err }

// MessageParseError reports a decoded JSON object that could not be mapped onto
// a concrete [Message] type.
type MessageParseError struct {
	Type string
	Raw  []byte
	Err  error
}

func (e *MessageParseError) Error() string {
	return fmt.Sprintf("claude: failed to parse %q message: %v", e.Type, e.Err)
}
func (e *MessageParseError) Unwrap() error { return e.Err }
