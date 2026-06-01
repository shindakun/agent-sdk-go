package claude

import (
	"context"
	"encoding/json"
	"testing"
)

func TestQueryEndToEndScripted(t *testing.T) {
	st := newScriptedTransport(
		[]byte(`{"type":"system","subtype":"init","session_id":"s1","tools":["Read"]}`),
		[]byte(`{"type":"assistant","message":{"model":"m","content":[{"type":"text","text":"hello"}]}}`),
		[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"hello","session_id":"s1"}`),
	)
	restore := installScriptedTransport(st)
	defer restore()

	var (
		gotInit   bool
		gotResult *ResultMessage
		count     int
	)
	for msg, err := range Query(context.Background(), "hi") {
		if err != nil {
			t.Fatalf("query err: %v", err)
		}
		count++
		switch m := msg.(type) {
		case *SystemMessage:
			if m.Subtype == "init" {
				gotInit = true
			}
		case *ResultMessage:
			gotResult = m
		}
	}

	if !gotInit {
		t.Error("missing init message")
	}
	if gotResult == nil || gotResult.Result != "hello" {
		t.Errorf("result = %#v", gotResult)
	}
	if count != 3 {
		t.Errorf("message count = %d, want 3", count)
	}

	// The prompt must be sent over stdin as a user stream-json message after
	// the initialize handshake.
	lines := st.writtenLines()
	var sawInit, sawPrompt bool
	for _, l := range lines {
		var probe struct {
			Type    string `json:"type"`
			Request struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		_ = json.Unmarshal(l, &probe)
		if probe.Type == "control_request" && probe.Request.Subtype == "initialize" {
			sawInit = true
		}
		if probe.Type == "user" && probe.Message.Content == "hi" {
			sawPrompt = true
		}
	}
	if !sawInit {
		t.Error("no initialize control_request was written")
	}
	if !sawPrompt {
		t.Error("prompt was not written as a user message over stdin")
	}
}

func TestCollectScripted(t *testing.T) {
	st := newScriptedTransport(
		[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`),
	)
	defer installScriptedTransport(st)()

	msgs, err := Collect(context.Background(), "hi")
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages", len(msgs))
	}
	if _, ok := msgs[0].(*ResultMessage); !ok {
		t.Errorf("got %T", msgs[0])
	}
}

func TestBuildArgsMapping(t *testing.T) {
	o := newOptions(
		WithModel("opus"),
		WithAllowedTools("Read", "Edit"),
		WithMaxTurns(5),
		WithSystemPrompt("be terse"),
		WithPermissionMode(PermissionAcceptEdits),
	)
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	joined := joinComma(args) // reuse comma joiner just to scan
	_ = joined
	want := map[string]string{
		"--model":           "opus",
		"--allowedTools":    "Read,Edit",
		"--max-turns":       "5",
		"--system-prompt":   "be terse",
		"--permission-mode": "acceptEdits",
	}
	for flag, val := range want {
		if !argsContainPair(args, flag, val) {
			t.Errorf("args missing %s %s; got %v", flag, val, args)
		}
	}
}

func argsContainPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}
