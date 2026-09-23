package claude

import (
	"context"
	"encoding/json"
	"iter"
	"slices"
	"testing"
)

func userFrame(content any, extra map[string]any) map[string]any {
	m := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": content}, "parent_tool_use_id": nil}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func TestQueryMessagesWritesFrames(t *testing.T) {
	st := newScriptedTransport(
		[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`),
	)
	defer installScriptedTransport(st)()

	first := userFrame([]any{map[string]any{"type": "text", "text": "hi"}}, map[string]any{"origin": map[string]any{"kind": "human"}})
	second := userFrame("more", map[string]any{"session_id": "mine"})
	for _, err := range QueryMessages(context.Background(), slices.Values([]map[string]any{first, second}), WithVerbatimPrompts()) {
		if err != nil {
			t.Fatal(err)
		}
	}
	frames := userFrames(t, st.writtenLines())
	if len(frames) != 2 {
		t.Fatalf("frames = %d", len(frames))
	}
	if string(frames[0]["session_id"]) != `"default"` || string(frames[1]["session_id"]) != `"mine"` {
		t.Errorf("session ids = %s, %s", frames[0]["session_id"], frames[1]["session_id"])
	}
	if string(frames[0]["origin"]) != `{"kind":"human"}` || string(frames[0]["client_composed"]) != "true" {
		t.Errorf("frame 0 = %v", frames[0])
	}
	var msg struct {
		Content []map[string]any `json:"content"`
	}
	_ = json.Unmarshal(frames[0]["message"], &msg)
	if len(msg.Content) != 1 || msg.Content[0]["text"] != "hi" {
		t.Errorf("content = %v", msg.Content)
	}
	if _, ok := first["session_id"]; ok {
		t.Error("the caller's map was modified")
	}
	if st.endInputCount() == 0 {
		t.Error("stdin not closed")
	}
}

// With nothing sent no result would release the hold, so stdin closes at once
// even when callbacks need the control channel.
func TestQueryMessagesEmptyClosesInput(t *testing.T) {
	st := newScriptedTransport(
		[]byte(`{"type":"system","subtype":"init","session_id":"s1"}`),
	)
	defer installScriptedTransport(st)()
	var empty iter.Seq[map[string]any] = func(func(map[string]any) bool) {}
	beforeEOF := -1
	for msg, err := range QueryMessages(context.Background(), empty, WithHooks(map[HookEvent][]HookMatcher{
		HookPreToolUse: {{Callbacks: []HookCallback{func(context.Context, json.RawMessage, string) (HookOutput, error) { return HookOutput{}, nil }}}},
	})) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := msg.(*SystemMessage); ok {
			beforeEOF = st.endInputCount()
		}
	}
	if beforeEOF != 1 {
		t.Errorf("stdin closes before any output: EndInput count %d at init", beforeEOF)
	}
}

func TestClientQueryMessagesSession(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()
	c := NewClient()
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.QueryMessagesSession(context.Background(), slices.Values([]map[string]any{userFrame("x", nil)}), "s9"); err != nil {
		t.Fatal(err)
	}
	frames := userFrames(t, tr.writtenLines())
	if len(frames) != 1 || string(frames[0]["session_id"]) != `"s9"` {
		t.Errorf("frames = %v", frames)
	}
}
