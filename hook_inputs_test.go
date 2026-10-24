package claude

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestDecodePreToolUse(t *testing.T) {
	raw := []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","cwd":"/w","tool_name":"Bash","tool_input":{"command":"ls"},"tool_use_id":"tu_1","agent_id":"ag_1"}`)
	in, err := DecodePreToolUse(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if in.HookEventName != "PreToolUse" || in.ToolName != "Bash" || in.ToolUseID != "tu_1" {
		t.Errorf("input = %+v", in)
	}
	if in.SessionID != "s1" || in.Cwd != "/w" || in.AgentID != "ag_1" {
		t.Errorf("base/agent fields = %+v", in)
	}
	if string(in.ToolInput) != `{"command":"ls"}` {
		t.Errorf("tool_input = %s", in.ToolInput)
	}
}

func TestDecodePostToolUseAndNotification(t *testing.T) {
	post, err := DecodePostToolUse([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Read","tool_response":{"ok":true},"tool_use_id":"tu_2"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if post.ToolName != "Read" || string(post.ToolResponse) != `{"ok":true}` {
		t.Errorf("post = %+v", post)
	}

	note, err := DecodeNotification([]byte(`{"hook_event_name":"Notification","message":"done","notification_type":"info"}`))
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	if note.Message != "done" || note.NotificationType != "info" {
		t.Errorf("note = %+v", note)
	}
}

func TestTaskNotificationDecode(t *testing.T) {
	msg, err := UnmarshalMessage([]byte(`{"type":"task_notification","task_id":"t1","status":"completed","summary":"all good","session_id":"s9","usage":{"total_tokens":42,"tool_uses":3,"duration_ms":1000}}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tn, ok := msg.(*TaskNotificationMessage)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if tn.TaskID != "t1" || tn.Status != "completed" || tn.Summary != "all good" {
		t.Errorf("task notification = %+v", tn)
	}
	if tn.Usage == nil || tn.Usage.TotalTokens != 42 || tn.Usage.ToolUses != 3 {
		t.Errorf("usage = %+v", tn.Usage)
	}
}

// fillAll returns a copy of v with every field set to a non-zero value, so a
// marshal shows each key, including omitempty ones.
func fillAll(t *testing.T, v any) any {
	t.Helper()
	rv := reflect.New(reflect.TypeOf(v)).Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		switch {
		case f.Type() == reflect.TypeOf(json.RawMessage(nil)):
			f.Set(reflect.ValueOf(json.RawMessage(`{"k":1}`)))
		case f.Kind() == reflect.String:
			f.SetString("x")
		case f.Kind() == reflect.Bool:
			f.SetBool(true)
		case f.Kind() == reflect.Slice && f.Type().Elem().Kind() == reflect.String:
			f.Set(reflect.ValueOf([]string{"x"}))
		default:
			t.Fatalf("%T.%s: unhandled kind %s", v, rv.Type().Field(i).Name, f.Kind())
		}
	}
	return rv.Interface()
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func sortedStrings(ks ...string) []string {
	out := slices.Clone(ks)
	slices.Sort(out)
	return out
}

// TestHookSpecificOutputKeySets mirrors upstream's
// test_output_key_sets_match_typescript (a8e7ce3): a zero value marshals to the
// required keys only, and a filled one to required plus optional.
func TestHookSpecificOutputKeySets(t *testing.T) {
	for _, tc := range []struct {
		v                  any
		required, optional []string
	}{
		{UserPromptSubmitHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext", "sessionTitle", "suppressOriginalPrompt"}},
		{UserPromptExpansionHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext", "suppressOriginalPrompt"}},
		{SessionStartHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext", "initialUserMessage", "sessionTitle", "watchPaths", "reloadSkills"}},
		{PostToolBatchHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext"}},
		{StopHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext"}},
		{SubagentStopHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext"}},
		{PermissionDeniedHookSpecificOutput{}, []string{"hookEventName"}, []string{"retry"}},
		{PreModelSwitchHookSpecificOutput{}, []string{"hookEventName"}, []string{"permissionDecision", "permissionDecisionReason"}},
		{PostModelSwitchHookSpecificOutput{}, []string{"hookEventName"}, []string{"additionalContext"}},
		{ElicitationHookSpecificOutput{}, []string{"hookEventName"}, []string{"action", "content"}},
		{ElicitationResultHookSpecificOutput{}, []string{"hookEventName"}, []string{"action", "content"}},
		{CwdChangedHookSpecificOutput{}, []string{"hookEventName"}, []string{"watchPaths"}},
		{FileChangedHookSpecificOutput{}, []string{"hookEventName"}, []string{"watchPaths"}},
		{WorktreeCreateHookSpecificOutput{}, []string{"hookEventName", "worktreePath"}, nil},
		{MessageDisplayHookSpecificOutput{}, []string{"hookEventName"}, []string{"displayContent"}},
	} {
		name := reflect.TypeOf(tc.v).Name()
		if got, want := jsonKeys(t, tc.v), sortedStrings(tc.required...); !slices.Equal(got, want) {
			t.Errorf("%s required keys = %v, want %v", name, got, want)
		}
		if got, want := jsonKeys(t, fillAll(t, tc.v)), sortedStrings(append(tc.required, tc.optional...)...); !slices.Equal(got, want) {
			t.Errorf("%s all keys = %v, want %v", name, got, want)
		}
	}
}

func TestBackgroundWorkSummaryKeySets(t *testing.T) {
	for _, tc := range []struct {
		v                  any
		required, optional []string
	}{
		{BackgroundTaskSummary{}, []string{"id", "type", "status", "description"}, []string{"command", "agent_type", "server", "tool", "name"}},
		{SessionCronSummary{}, []string{"id", "schedule", "recurring", "prompt"}, nil},
		{HookEffort{}, []string{"level"}, nil},
	} {
		name := reflect.TypeOf(tc.v).Name()
		if got, want := jsonKeys(t, tc.v), sortedStrings(tc.required...); !slices.Equal(got, want) {
			t.Errorf("%s required keys = %v, want %v", name, got, want)
		}
		if got, want := jsonKeys(t, fillAll(t, tc.v)), sortedStrings(append(tc.required, tc.optional...)...); !slices.Equal(got, want) {
			t.Errorf("%s all keys = %v, want %v", name, got, want)
		}
	}
}

func TestHookInputNewFields(t *testing.T) {
	pre, err := DecodePreToolUse([]byte(`{"hook_event_name":"PreToolUse","session_id":"s","transcript_path":"/t","cwd":"/w",` +
		`"prompt_id":"550e8400-e29b-41d4-a716-446655440000","effort":{"level":"high"},"tool_name":"Bash","tool_input":{},"tool_use_id":"tu"}`))
	if err != nil {
		t.Fatalf("pre: %v", err)
	}
	if pre.PromptID != "550e8400-e29b-41d4-a716-446655440000" || pre.Effort == nil || pre.Effort.Level != "high" {
		t.Errorf("base fields = %+v", pre.BaseHookInput)
	}

	post, err := DecodePostToolUse([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{},"tool_use_id":"tu","duration_ms":12}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if post.DurationMs == nil || *post.DurationMs != 12 {
		t.Errorf("post duration_ms = %v", post.DurationMs)
	}
	fail, err := DecodePostToolUseFailure([]byte(`{"hook_event_name":"PostToolUseFailure","tool_name":"Bash","error":"e","duration_ms":0}`))
	if err != nil {
		t.Fatalf("failure: %v", err)
	}
	if fail.DurationMs == nil || *fail.DurationMs != 0 {
		t.Errorf("failure duration_ms = %v, want a present 0", fail.DurationMs)
	}

	// The new fields are optional: an input without them still decodes.
	bare, err := DecodePostToolUse([]byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`))
	if err != nil {
		t.Fatalf("bare: %v", err)
	}
	if bare.DurationMs != nil || bare.Effort != nil || bare.PromptID != "" {
		t.Errorf("absent fields not zero: %+v", bare)
	}
}

func TestStopHookInputBackgroundWork(t *testing.T) {
	const work = `"last_assistant_message":"Done.",` +
		`"background_tasks":[{"id":"task-1","type":"shell","status":"running","description":"npm test","command":"npm test"}],` +
		`"session_crons":[{"id":"cron-1","schedule":"0 9 * * 1-5","recurring":true,"prompt":"check CI"}]`
	wantTasks := []BackgroundTaskSummary{{ID: "task-1", Type: "shell", Status: "running", Description: "npm test", Command: "npm test"}}
	wantCrons := []SessionCronSummary{{ID: "cron-1", Schedule: "0 9 * * 1-5", Recurring: true, Prompt: "check CI"}}

	stop, err := DecodeStop([]byte(`{"hook_event_name":"Stop","stop_hook_active":false,` + work + `}`))
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stop.LastAssistantMessage != "Done." || !reflect.DeepEqual(stop.BackgroundTasks, wantTasks) || !reflect.DeepEqual(stop.SessionCrons, wantCrons) {
		t.Errorf("stop = %+v", stop)
	}

	sub, err := DecodeSubagentStop([]byte(`{"hook_event_name":"SubagentStop","stop_hook_active":false,"agent_id":"a",` + work + `}`))
	if err != nil {
		t.Fatalf("subagent stop: %v", err)
	}
	if sub.LastAssistantMessage != "Done." || !reflect.DeepEqual(sub.BackgroundTasks, wantTasks) || !reflect.DeepEqual(sub.SessionCrons, wantCrons) {
		t.Errorf("subagent stop = %+v", sub)
	}
}
