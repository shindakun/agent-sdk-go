package claude

import "testing"

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
