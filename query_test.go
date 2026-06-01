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

func TestBuildArgsParityFlags(t *testing.T) {
	o := newOptions(
		WithIncludePartialMessages(),
		WithSettingSources("user", "project"),
		WithContinueConversation(),
		WithResume("sess_7"),
		WithForkSession(),
		WithPermissionPromptToolName("mcp__perm__prompt"),
		WithFallbackModel("haiku"),
		WithMaxBudgetUSD(2.5),
		WithEffort(EffortHigh),
		WithAddDir("/a", "/b"),
		WithPluginDir("/p"),
	)
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if !argsContainsFlag(args, "--include-partial-messages") {
		t.Error("missing --include-partial-messages")
	}
	if !argsContainsFlag(args, "--continue") {
		t.Error("missing --continue")
	}
	if !argsContainsFlag(args, "--fork-session") {
		t.Error("missing --fork-session")
	}
	for flag, val := range map[string]string{
		"--setting-sources":        "user,project",
		"--resume":                 "sess_7",
		"--permission-prompt-tool": "mcp__perm__prompt",
		"--fallback-model":         "haiku",
		"--max-budget-usd":         "2.5",
		"--effort":                 "high",
	} {
		if !argsContainPair(args, flag, val) {
			t.Errorf("missing %s %s; args=%v", flag, val, args)
		}
	}
	// --add-dir appears once per directory.
	if !argsContainPair(args, "--add-dir", "/a") || !argsContainPair(args, "--add-dir", "/b") {
		t.Errorf("missing per-dir --add-dir; args=%v", args)
	}
}

func TestCanUseToolSetsPermissionPromptToolStdio(t *testing.T) {
	// A CanUseTool callback must add --permission-prompt-tool stdio so the CLI
	// routes permission requests to the SDK over the control protocol. (Verified
	// against the real binary: without it, can_use_tool is never sent.)
	o := newOptions(WithCanUseTool(
		func(ctx context.Context, tool string, in json.RawMessage, pc PermissionContext) (PermissionResult, error) {
			return PermissionAllow{}, nil
		}))
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if !argsContainPair(args, "--permission-prompt-tool", "stdio") {
		t.Errorf("CanUseTool should add --permission-prompt-tool stdio; args=%v", args)
	}
}

func TestCanUseToolAndPromptToolNameMutuallyExclusive(t *testing.T) {
	o := newOptions(
		WithCanUseTool(func(ctx context.Context, tool string, in json.RawMessage, pc PermissionContext) (PermissionResult, error) {
			return PermissionAllow{}, nil
		}),
		WithPermissionPromptToolName("mcp__x__y"),
	)
	if _, err := o.buildArgs(); err == nil {
		t.Error("expected an error when both CanUseTool and PermissionPromptToolName are set")
	}
}

func argsContainsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func argsContainPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}
