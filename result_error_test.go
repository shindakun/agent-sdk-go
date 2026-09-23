package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/shindakun/agent-sdk-go/internal/transport"
)

func TestResultErrorPayload(t *testing.T) {
	data := []byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["a"," ","b",3],` +
		`"result":"r","api_error_status":529,"terminal_reason":"max_turns","session_id":"s1","uuid":"u1"}`)
	e := newResultError(data, 1)
	if e.Subtype != "error_max_turns" || e.Result != "r" || e.TerminalReason != "max_turns" ||
		e.SessionID != "s1" || e.APIErrorStatus == nil || *e.APIErrorStatus != 529 || e.ExitCode != 1 {
		t.Errorf("payload = %+v", e)
	}
	if strings.Join(e.Errors, "|") != "a|b" {
		t.Errorf("errors = %q, want blank and non-string entries dropped", e.Errors)
	}
	if e.Error() != "claude: Claude Code returned an error result: a; b" {
		t.Errorf("Error() = %q", e.Error())
	}
	var pe *ProcessError
	if !errors.As(error(e), &pe) || pe.ExitCode != 1 {
		t.Errorf("does not unwrap to ProcessError: %v", pe)
	}
	var raw map[string]any
	if json.Unmarshal(e.Data, &raw) != nil || raw["uuid"] != "u1" {
		t.Errorf("Data = %s", e.Data)
	}
}

func TestResultErrorText(t *testing.T) {
	for want, data := range map[string]string{
		"x; y":                      `{"errors":["x","y"],"result":"r","subtype":"error_during_execution"}`,
		"lone":                      `{"errors":" lone "}`,
		"API Error: 529 overloaded": `{"subtype":"success","errors":[],"result":" API Error: 529 overloaded "}`,
		"error_during_execution":    `{"subtype":"error_during_execution","errors":[]}`,
		"API error (HTTP 500)":      `{"subtype":"success","api_error_status":500}`,
		"unknown error":             `{"subtype":"success"}`,
	} {
		e := newResultError([]byte(data), 1)
		if got := strings.TrimPrefix(e.Message, "Claude Code returned an error result: "); got != want {
			t.Errorf("%s: text = %q, want %q", data, got, want)
		}
	}
	// Malformed fields leave zero values rather than failing.
	e := newResultError([]byte(`{"subtype":5,"errors":{"x":1},"api_error_status":"nope","result":[1]}`), 2)
	if e.Subtype != "" || e.Errors != nil || e.APIErrorStatus != nil || e.Result != "" {
		t.Errorf("malformed payload = %+v", e)
	}
}

// exitingTransport replays lines after connect and then reports a process
// exit status through Wait, like the subprocess transport. It answers control
// requests only when answer is set.
type exitingTransport struct {
	ch     chan transport.RawLine
	lines  [][]byte
	answer bool
	exit   error
	once   sync.Once
}

func (e *exitingTransport) Connect(context.Context) error  { return nil }
func (e *exitingTransport) Read() <-chan transport.RawLine { return e.ch }
func (e *exitingTransport) EndInput() error                { return nil }
func (e *exitingTransport) Close() error                   { return e.exit }
func (e *exitingTransport) Wait() error                    { return e.exit }

func (e *exitingTransport) Write(_ context.Context, obj []byte) error {
	var probe struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(obj, &probe)
	if probe.Type == "control_request" && e.answer {
		b, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": probe.RequestID, "response": map[string]any{}}})
		e.ch <- transport.RawLine{Data: b}
	}
	e.once.Do(func() {
		go func() {
			for _, l := range e.lines {
				e.ch <- transport.RawLine{Data: l}
			}
			e.ch <- transport.RawLine{Err: io.EOF}
			close(e.ch)
		}()
	})
	return nil
}

func installExiting(t *testing.T, tr *exitingTransport) {
	t.Helper()
	tr.ch = make(chan transport.RawLine, 16)
	prev := transportFactory
	transportFactory = func(transport.Config) transport.Transport { return tr }
	t.Cleanup(func() { transportFactory = prev })
}

const errorResultLine = `{"type":"result","subtype":"success","is_error":true,"errors":[],` +
	`"result":"API Error: model not found","terminal_reason":"api_error","session_id":"s1"}`

func TestQueryYieldsResultErrorAfterErrorResult(t *testing.T) {
	installExiting(t, &exitingTransport{
		answer: true,
		lines:  [][]byte{[]byte(errorResultLine)},
		exit:   &transport.ProcessError{ExitCode: 1, Stderr: "noise"},
	})
	var sawResult bool
	var got error
	for msg, err := range Query(context.Background(), "hi") {
		if err != nil {
			got = err
			break
		}
		if r, ok := msg.(*ResultMessage); ok && r.IsError {
			sawResult = true
		}
	}
	if !sawResult {
		t.Error("the error result was not delivered before the error")
	}
	var re *ResultError
	if !errors.As(got, &re) || re.TerminalReason != "api_error" || !strings.Contains(re.Error(), "model not found") {
		t.Fatalf("err = %v, want a ResultError carrying the result", got)
	}
}

func TestQueryYieldsProcessErrorOnCrash(t *testing.T) {
	installExiting(t, &exitingTransport{
		answer: true,
		lines:  [][]byte{[]byte(`{"type":"system","subtype":"init","session_id":"s1"}`)},
		exit:   &transport.ProcessError{ExitCode: 3, Stderr: "boom"},
	})
	var got error
	for _, err := range Query(context.Background(), "hi") {
		if err != nil {
			got = err
		}
	}
	var pe *ProcessError
	var re *ResultError
	if !errors.As(got, &pe) || pe.ExitCode != 3 || errors.As(got, &re) {
		t.Errorf("err = %v, want a plain ProcessError", got)
	}
}

func TestCleanExitYieldsNoError(t *testing.T) {
	installExiting(t, &exitingTransport{
		answer: true,
		lines:  [][]byte{[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`)},
	})
	for _, err := range Query(context.Background(), "hi") {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

// A CLI that rejects the session before answering initialize (a refused
// resume) fails the connect with the result's text, not "connection closed".
func TestInitializeFailsWithResultError(t *testing.T) {
	installExiting(t, &exitingTransport{
		lines: [][]byte{[]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,` +
			`"errors":["Resume rejected by --resume-drops-turn: entry x is not part of turn y"],"session_id":"s1"}`)},
		exit: &transport.ProcessError{ExitCode: 1},
	})
	err := NewClient().Connect(context.Background())
	var re *ResultError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "Resume rejected by --resume-drops-turn:") || re.ExitCode != 1 {
		t.Fatalf("connect err = %v, want a ResultError with the CLI's text", err)
	}
}

func TestResumeTruncationFlags(t *testing.T) {
	args, err := newOptions(WithResume("s"), WithResumeSessionAt("--evil"), WithResumeDropsTurn("p1")).buildArgs()
	if err != nil {
		t.Fatal(err)
	}
	if !argsContainEquals(args, "--resume-session-at", "--evil") || !argsContainEquals(args, "--resume-drops-turn", "p1") {
		t.Errorf("args = %v", args)
	}
	args, _ = newOptions(WithResume("s")).buildArgs()
	for _, a := range args {
		if strings.HasPrefix(a, "--resume-session-at") || strings.HasPrefix(a, "--resume-drops-turn") {
			t.Errorf("unset flag emitted: %s", a)
		}
	}
	// An empty drops-turn is forwarded so the CLI rejects it instead of the
	// check being silently off.
	args, _ = newOptions(WithResume("s"), WithResumeDropsTurn("")).buildArgs()
	if !argsContainEquals(args, "--resume-drops-turn", "") {
		t.Errorf("empty --resume-drops-turn dropped; args=%v", args)
	}
}
