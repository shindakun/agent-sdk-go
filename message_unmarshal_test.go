package claude

import (
	"testing"
)

func TestUnmarshalAssistant(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"model":"claude-sonnet-4-6","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"tu_1","name":"Read","input":{"file_path":"a.go"}}]},"parent_tool_use_id":"pt_9"}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	am, ok := msg.(*AssistantMessage)
	if !ok {
		t.Fatalf("got %T, want *AssistantMessage", msg)
	}
	if am.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q", am.Model)
	}
	if am.ParentToolUseID != "pt_9" {
		t.Errorf("parent = %q", am.ParentToolUseID)
	}
	if len(am.Content) != 2 {
		t.Fatalf("content len = %d", len(am.Content))
	}
	if tb, ok := am.Content[0].(*TextBlock); !ok || tb.Text != "hi" {
		t.Errorf("block 0 = %#v", am.Content[0])
	}
	tu, ok := am.Content[1].(*ToolUseBlock)
	if !ok || tu.Name != "Read" || tu.ID != "tu_1" {
		t.Errorf("block 1 = %#v", am.Content[1])
	}
}

func TestUnmarshalResult(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","is_error":false,"duration_ms":1200,"num_turns":3,"total_cost_usd":0.0123,"result":"done","session_id":"sess_1","usage":{"input_tokens":10,"output_tokens":20}}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rm, ok := msg.(*ResultMessage)
	if !ok {
		t.Fatalf("got %T, want *ResultMessage", msg)
	}
	if rm.Result != "done" || rm.SessionID != "sess_1" || rm.NumTurns != 3 {
		t.Errorf("result = %#v", rm)
	}
	if rm.Usage.InputTokens != 10 || rm.Usage.OutputTokens != 20 {
		t.Errorf("usage = %#v", rm.Usage)
	}
}

func TestUnmarshalSystemInit(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"init","session_id":"sess_42","tools":["Read","Bash"]}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sm, ok := msg.(*SystemMessage)
	if !ok {
		t.Fatalf("got %T, want *SystemMessage", msg)
	}
	if sm.Subtype != "init" || sm.SessionID != "sess_42" {
		t.Errorf("system = %#v", sm)
	}
	if len(sm.Tools) != 2 {
		t.Errorf("tools = %v", sm.Tools)
	}
}

func TestUnmarshalControlFramesAreNotMessages(t *testing.T) {
	for _, line := range []string{
		`{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"req_1"}}`,
		`{"type":"transcript_mirror"}`,
	} {
		_, err := UnmarshalMessage([]byte(line))
		if !IsNotAMessage(err) {
			t.Errorf("line %q: IsNotAMessage = false (err=%v)", line, err)
		}
	}
}

func TestToolResultBlockText(t *testing.T) {
	line := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":"file contents"}]}}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	um := msg.(*UserMessage)
	tr := um.Content[0].(*ToolResultBlock)
	got, ok := tr.Text()
	if !ok || got != "file contents" {
		t.Errorf("text = %q ok=%v", got, ok)
	}
}
