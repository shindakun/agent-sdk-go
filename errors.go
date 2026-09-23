package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// ResultError reports that the CLI exited after a terminal error result. The
// CLI ends a failed run with a result message (is_error true, delivered as a
// [ResultMessage]) and then exits non-zero; this error takes the place of the
// bare [ProcessError] for that case and carries the result's payload, so
// callers can branch on why the run failed:
//
//	var re *claude.ResultError
//	if errors.As(err, &re) && re.TerminalReason == "api_error" {
//	    // retry
//	}
//
// It unwraps to a [*ProcessError], so errors.As with a ProcessError target
// still matches. The same error fails a connect when the CLI rejects the
// session before answering initialize (a refused resume, for example).
type ResultError struct {
	// Message is the text Error returns: the most informative of the result's
	// errors, result text, subtype, or HTTP status.
	Message string
	// Subtype is the result subtype ("error_max_turns",
	// "error_during_execution", ..., or "success" when the loop completed but
	// the last turn was an API error).
	Subtype string
	// Errors are the error strings the CLI reported (may be empty).
	Errors []string
	// Result is the result text; for API failures, the "API Error: ..." prose.
	Result string
	// APIErrorStatus is the HTTP status of the failing API call, if any.
	APIErrorStatus *int
	// TerminalReason is why the run ended ("api_error", "max_turns", ...).
	TerminalReason string
	SessionID      string
	// Data is the result message as the CLI sent it.
	Data     json.RawMessage
	ExitCode int
}

func (e *ResultError) Error() string { return "claude: " + e.Message }

// Unwrap returns the process exit as a [*ProcessError]. Stderr is left empty:
// the result text is the real cause.
func (e *ResultError) Unwrap() error { return &ProcessError{ExitCode: e.ExitCode} }

// newResultError builds a ResultError from an is_error result frame.
func newResultError(data []byte, exitCode int) *ResultError {
	var wire map[string]json.RawMessage
	_ = json.Unmarshal(data, &wire)
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(wire[k], &s)
		return s
	}
	e := &ResultError{
		Subtype:        str("subtype"),
		Errors:         normalizeResultErrors(wire["errors"]),
		Result:         str("result"),
		TerminalReason: str("terminal_reason"),
		SessionID:      str("session_id"),
		Data:           append(json.RawMessage(nil), data...),
		ExitCode:       exitCode,
	}
	var status int
	if json.Unmarshal(wire["api_error_status"], &status) == nil && wire["api_error_status"] != nil {
		e.APIErrorStatus = &status
	}
	e.Message = "Claude Code returned an error result: " + errorResultText(e)
	return e
}

// normalizeResultErrors reads a result's errors field: a list of strings, or a
// bare string from older emitters, dropping blank and non-string entries.
func normalizeResultErrors(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if one = strings.TrimSpace(one); one != "" {
			return []string{one}
		}
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// errorResultText picks the most informative text from an error result:
// errors, then the result text (where an API failure puts its prose, under
// subtype "success"), then a non-success subtype, then the HTTP status.
func errorResultText(e *ResultError) string {
	switch {
	case len(e.Errors) > 0:
		return strings.Join(e.Errors, "; ")
	case strings.TrimSpace(e.Result) != "":
		return strings.TrimSpace(e.Result)
	case e.Subtype != "" && e.Subtype != "success":
		return e.Subtype
	case e.APIErrorStatus != nil:
		return fmt.Sprintf("API error (HTTP %d)", *e.APIErrorStatus)
	}
	return "unknown error"
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
