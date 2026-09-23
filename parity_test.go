package claude

import (
	"context"
	"encoding/json"
	"testing"
)

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
		"--effort":      "high",
		"--task-budget": "5000",
	}
	for flag, val := range checks {
		if !argsContainPair(args, flag, val) {
			t.Errorf("missing %s %s; args=%v", flag, val, args)
		}
	}
	if !argsContainEquals(args, "--session-id", "sess-9") {
		t.Errorf("missing --session-id=sess-9; args=%v", args)
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

func TestAssistantMessageFullFields(t *testing.T) {
	line := []byte(`{"type":"assistant","parent_tool_use_id":"pt","session_id":"s1","uuid":"u1","message":{"model":"m","id":"msg_1","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":4},"content":[{"type":"text","text":"hi"}]}}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	am := msg.(*AssistantMessage)
	if am.MessageID != "msg_1" || am.StopReason != "end_turn" || am.SessionID != "s1" || am.UUID != "u1" {
		t.Errorf("assistant fields = %+v", am)
	}
	if am.Usage == nil || am.Usage.InputTokens != 3 || am.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v", am.Usage)
	}
}

func TestResultErrorsAreStrings(t *testing.T) {
	// Python's ResultMessage.errors is list[str]; ensure we decode that shape.
	line := []byte(`{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["boom","again"],"duration_ms":1,"duration_api_ms":2,"num_turns":1,"session_id":"s","stop_reason":"max_turns"}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rm := msg.(*ResultMessage)
	if len(rm.Errors) != 2 || rm.Errors[0] != "boom" {
		t.Errorf("errors = %v", rm.Errors)
	}
	if rm.DurationAPIMs != 2 || rm.StopReason != "max_turns" {
		t.Errorf("result extra fields = %+v", rm)
	}
}

func TestHookEventValuesMatchPython(t *testing.T) {
	// The full set of 10 hook events from the Python HookEvent union.
	want := []HookEvent{
		HookPreToolUse, HookPostToolUse, HookPostToolUseFailure, HookUserPromptSubmit,
		HookStop, HookSubagentStop, HookSubagentStart, HookPreCompact,
		HookNotification, HookPermissionRequest,
	}
	wantStr := map[string]bool{
		"PreToolUse": true, "PostToolUse": true, "PostToolUseFailure": true,
		"UserPromptSubmit": true, "Stop": true, "SubagentStop": true,
		"SubagentStart": true, "PreCompact": true, "Notification": true,
		"PermissionRequest": true,
	}
	if len(want) != len(wantStr) {
		t.Fatalf("want set size mismatch")
	}
	for _, e := range want {
		if !wantStr[string(e)] {
			t.Errorf("unexpected hook event value %q", e)
		}
	}
}

func TestPermissionModeValues(t *testing.T) {
	// The 6 Python PermissionMode values.
	want := map[PermissionMode]bool{
		PermissionDefault: true, PermissionAcceptEdits: true, PermissionPlan: true,
		PermissionBypass: true, PermissionDontAsk: true, PermissionAuto: true,
	}
	wantStr := []string{"default", "acceptEdits", "plan", "bypassPermissions", "dontAsk", "auto"}
	if len(want) != len(wantStr) {
		t.Fatalf("count mismatch")
	}
	have := map[string]bool{}
	for m := range want {
		have[string(m)] = true
	}
	for _, s := range wantStr {
		if !have[s] {
			t.Errorf("missing permission mode %q", s)
		}
	}
}

func TestSessionStoreFlushModeValues(t *testing.T) {
	if FlushBatched != "batched" || FlushEager != "eager" {
		t.Errorf("flush modes = %q/%q, want batched/eager", FlushBatched, FlushEager)
	}
}

func TestCanUseToolContextFullFields(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()
	var got PermissionContext
	client := NewClient(WithCanUseTool(
		func(ctx context.Context, tool string, in json.RawMessage, pc PermissionContext) (PermissionResult, error) {
			got = pc
			return PermissionAllow{}, nil
		}))
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	tr.sendInbound(t, "rin_1", "can_use_tool", map[string]any{
		"tool_name": "Bash", "input": json.RawMessage(`{}`), "tool_use_id": "tu",
		"agent_id": "ag", "blocked_path": "/etc", "decision_reason": "policy",
		"title": "T", "display_name": "DN", "description": "D",
	})
	if got.AgentID != "ag" || got.BlockedPath != "/etc" || got.DecisionReason != "policy" ||
		got.Title != "T" || got.DisplayName != "DN" || got.Description != "D" {
		t.Errorf("permission context missing fields: %+v", got)
	}
}

func TestSystemInitPluginsDecode(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","session_id":"s","tools":["Read"],"plugins":[{"name":"demo-plugin","path":"/p/demo-plugin","source":"demo-plugin@inline"}]}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sm := msg.(*SystemMessage)
	if len(sm.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(sm.Plugins))
	}
	p := sm.Plugins[0]
	if p.Name != "demo-plugin" || p.Path != "/p/demo-plugin" || p.Source != "demo-plugin@inline" {
		t.Errorf("plugin = %+v", p)
	}
}

func TestHookEventMessageDecode(t *testing.T) {
	for _, sub := range []string{"hook_started", "hook_response"} {
		line := []byte(`{"type":"system","subtype":"` + sub + `","hook_event":"PreToolUse","session_id":"s1","uuid":"u1"}`)
		msg, err := UnmarshalMessage(line)
		if err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		hm, ok := msg.(*HookEventMessage)
		if !ok {
			t.Fatalf("%s: got %T", sub, msg)
		}
		if hm.Subtype != sub || hm.HookEventName != "PreToolUse" || hm.SessionID != "s1" || hm.UUID != "u1" {
			t.Errorf("%s: %+v", sub, hm)
		}
	}
	// Falls back through hook_name then hook_event_name.
	msg, _ := UnmarshalMessage([]byte(`{"type":"system","subtype":"hook_started","hook_name":"Stop"}`))
	if hm := msg.(*HookEventMessage); hm.HookEventName != "Stop" {
		t.Errorf("hook_name fallback = %q", hm.HookEventName)
	}
}

func TestSystemPromptModes(t *testing.T) {
	// Unset -> empty system prompt (matches upstream).
	args, _ := newOptions().buildArgs()
	if !argsContainPair(args, "--system-prompt", "") {
		t.Errorf("unset should emit --system-prompt \"\"; args=%v", args)
	}
	// Replace.
	args, _ = newOptions(WithSystemPrompt("be terse")).buildArgs()
	if !argsContainPair(args, "--system-prompt", "be terse") {
		t.Errorf("replace; args=%v", args)
	}
	// Append.
	args, _ = newOptions(WithAppendSystemPrompt("also this")).buildArgs()
	if !argsContainPair(args, "--append-system-prompt", "also this") {
		t.Errorf("append; args=%v", args)
	}
	// File.
	args, _ = newOptions(WithSystemPromptFile("/tmp/sp.txt")).buildArgs()
	if !argsContainPair(args, "--system-prompt-file", "/tmp/sp.txt") {
		t.Errorf("file; args=%v", args)
	}
}

func TestSkillsDefaults(t *testing.T) {
	// Skills inject Skill(name) into allowedTools and default setting-sources.
	args, _ := newOptions(WithSkills("my-skill", "other")).buildArgs()
	if !argsContainPair(args, "--allowedTools", "Skill(my-skill),Skill(other)") {
		t.Errorf("skills not injected into allowedTools; args=%v", args)
	}
	if !argsContainEquals(args, "--setting-sources", "user,project") {
		t.Errorf("setting-sources default missing; args=%v", args)
	}
	// Explicit setting-sources is preserved.
	args, _ = newOptions(WithSkills("s"), WithSettingSources("local")).buildArgs()
	if !argsContainEquals(args, "--setting-sources", "local") {
		t.Errorf("explicit setting-sources overridden; args=%v", args)
	}
	// No skills -> no injection.
	args, _ = newOptions(WithAllowedTools("Read")).buildArgs()
	if !argsContainPair(args, "--allowedTools", "Read") {
		t.Errorf("plain allowedTools; args=%v", args)
	}
}

func TestSessionMirrorFlag(t *testing.T) {
	store := NewInMemorySessionStore()
	args, _ := newOptions(WithSessionStore(store, FlushBatched)).buildArgs()
	if !argsContainsFlag(args, "--session-mirror") {
		t.Errorf("WithSessionStore should emit --session-mirror; args=%v", args)
	}
	// No store -> no flag.
	args, _ = newOptions().buildArgs()
	if argsContainsFlag(args, "--session-mirror") {
		t.Errorf("--session-mirror should not be emitted without a store")
	}
}

func TestTaskUpdatedDecode(t *testing.T) {
	// status is extracted from patch.status; terminal "killed" must be detected.
	line := []byte(`{"type":"system","subtype":"task_updated","task_id":"t1","session_id":"s","uuid":"u","patch":{"status":"killed","end_time":123}}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tu, ok := msg.(*TaskUpdatedMessage)
	if !ok {
		t.Fatalf("got %T, want *TaskUpdatedMessage", msg)
	}
	if tu.TaskID != "t1" || tu.SessionID != "s" || tu.UUID != "u" {
		t.Errorf("fields = %+v", tu)
	}
	if tu.Status != TaskUpdatedKilled {
		t.Errorf("status = %q, want killed", tu.Status)
	}
	if !IsTerminalTaskStatus(string(tu.Status)) {
		t.Error("killed should be a terminal status")
	}
	// patch preserved
	if len(tu.Patch) == 0 {
		t.Error("patch not preserved")
	}
}

func TestTaskUpdatedDefensive(t *testing.T) {
	// A task_updated with no/odd patch must not raise and yields empty status.
	for _, line := range []string{
		`{"type":"system","subtype":"task_updated","task_id":"t2"}`,
		`{"type":"system","subtype":"task_updated","task_id":"t3","patch":"not-an-object"}`,
		`{"type":"system","subtype":"task_updated","task_id":"t4","patch":{"end_time":9}}`,
	} {
		msg, err := UnmarshalMessage([]byte(line))
		if err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		tu := msg.(*TaskUpdatedMessage)
		if tu.Status != "" {
			t.Errorf("%q: status = %q, want empty", line, tu.Status)
		}
	}
}

func TestTerminalTaskStatusBothVocabularies(t *testing.T) {
	for _, s := range []string{"completed", "failed", "stopped", "killed"} {
		if !IsTerminalTaskStatus(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"pending", "running", "paused", ""} {
		if IsTerminalTaskStatus(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}
