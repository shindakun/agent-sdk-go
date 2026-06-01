package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestFirstPromptFilteringSkipsSyntheticLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"plain", "hello world", "hello world"},
		{"local-command-stdout", "<local-command-stdout>output</local-command-stdout>", ""},
		{"session-start-hook", "<session-start-hook>x", ""},
		{"tick", "<tick>", ""},
		{"goal", "<goal>do it</goal>", ""},
		{"interrupt", "[Request interrupted by user for tool use]", ""},
		{"ide_opened", "<ide_opened_file>foo.go</ide_opened_file>", ""},
		{"ide_selection", "<ide_selection>sel</ide_selection>", ""},
		{"newlines collapsed", "line1\nline2", "line1 line2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"content": tc.content})
			got := firstUserText(raw)
			if got != tc.want {
				t.Errorf("firstUserText(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestFirstPromptCommandNameFallback(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"content": "<command-name>review</command-name>"})
	if got := firstUserText(raw); got != "review" {
		t.Errorf("command fallback = %q, want review", got)
	}
}

func TestFirstPromptTruncation(t *testing.T) {
	long := strings.Repeat("a", 250)
	raw, _ := json.Marshal(map[string]any{"content": long})
	got := firstUserText(raw)
	if len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") { // 200 + ellipsis
		t.Errorf("truncation wrong: len=%d suffix=%q", len([]rune(got)), got[len(got)-3:])
	}
}

func TestRateLimitEventCamelCaseDecode(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event","uuid":"u1","session_id":"s1","rate_limit_info":{"status":"allowed_warning","resetsAt":123,"rateLimitType":"five_hour","utilization":0.8,"overageStatus":"allowed"}}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ev, ok := msg.(*RateLimitEvent)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if ev.UUID != "u1" || ev.RateLimitInfo.Status != RateLimitAllowedWarning {
		t.Errorf("event = %+v", ev)
	}
	if ev.RateLimitInfo.ResetsAt == nil || *ev.RateLimitInfo.ResetsAt != 123 {
		t.Errorf("resetsAt not decoded: %+v", ev.RateLimitInfo.ResetsAt)
	}
	if ev.RateLimitInfo.RateLimitType == nil || *ev.RateLimitInfo.RateLimitType != RateLimitFiveHour {
		t.Errorf("rateLimitType not decoded: %+v", ev.RateLimitInfo.RateLimitType)
	}
}

func TestMirrorErrorMessageDecode(t *testing.T) {
	msg, err := UnmarshalMessage([]byte(`{"type":"system","subtype":"mirror_error","error":"append failed"}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	me, ok := msg.(*MirrorErrorMessage)
	if !ok || me.Error != "append failed" {
		t.Errorf("got %#v", msg)
	}
}

func TestNewParityFlags(t *testing.T) {
	o := newOptions(
		WithToolList("Read", "Bash"),
		WithSessionID("sess-9"),
		WithStrictMcpConfig(),
		WithIncludeHookEvents(),
		WithEffort(EffortHigh),
		WithTaskBudget(TaskBudget{Total: 5000}),
	)
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	checks := map[string]string{
		"--tools":       "Read,Bash",
		"--session-id":  "sess-9",
		"--effort":      "high",
		"--task-budget": "5000",
	}
	for flag, val := range checks {
		if !argsContainPair(args, flag, val) {
			t.Errorf("missing %s %s; args=%v", flag, val, args)
		}
	}
	for _, flag := range []string{"--strict-mcp-config", "--include-hook-events"} {
		if !argsContainsFlag(args, flag) {
			t.Errorf("missing %s", flag)
		}
	}
}

func TestToolsPresetFlag(t *testing.T) {
	o := newOptions(WithToolsPreset())
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--tools", "default") {
		t.Errorf("preset should emit --tools default; args=%v", args)
	}
}

func TestFilePathToSessionKey(t *testing.T) {
	projects := "/home/u/.claude/projects"
	main, ok := filePathToSessionKey(projects+"/-w-proj/sess-1.jsonl", projects)
	if !ok || main.ProjectKey != "-w-proj" || main.SessionID != "sess-1" || main.Subpath != "" {
		t.Errorf("main key = %+v ok=%v", main, ok)
	}
	sub, ok := filePathToSessionKey(projects+"/-w-proj/sess-1/subagents/agent-7.jsonl", projects)
	if !ok || sub.SessionID != "sess-1" || sub.Subpath != "subagents/agent-7" {
		t.Errorf("subagent key = %+v ok=%v", sub, ok)
	}
	if _, ok := filePathToSessionKey("/elsewhere/x.jsonl", projects); ok {
		t.Error("path outside projects should not yield a key")
	}
}

func TestFoldSessionSummary(t *testing.T) {
	entries := []SessionStoreEntry{
		{Data: json.RawMessage(`{"type":"user","timestamp":"2026-01-02T03:04:05Z","cwd":"/w","message":{"content":"real prompt"}}`)},
	}
	s := FoldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s1"}, entries)
	if s.SessionID != "s1" {
		t.Errorf("session id = %q", s.SessionID)
	}
	if s.Data["first_prompt"] != "real prompt" {
		t.Errorf("first_prompt = %v", s.Data["first_prompt"])
	}
	if s.Data["created_at"] == nil {
		t.Error("created_at not latched")
	}
	if s.Data["cwd"] != "/w" {
		t.Errorf("cwd = %v", s.Data["cwd"])
	}
	if s.Summary != "real prompt" {
		t.Errorf("summary = %q", s.Summary)
	}
}

func TestImportSessionToStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/work/imp"
	writeSession(t, home, cwd, "imp-1",
		`{"type":"user","uuid":"u1","sessionId":"imp-1","message":{"content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"imp-1","message":{"content":[{"type":"text","text":"yo"}]}}`,
	)

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), "imp-1", store, ImportDirectory(cwd), ImportBatchSize(1)); err != nil {
		t.Fatalf("import: %v", err)
	}
	pk := ProjectKeyForDirectory(cwd)
	entries, err := store.Load(context.Background(), SessionKey{ProjectKey: pk, SessionID: "imp-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("imported %d entries, want 2", len(entries))
	}
}
