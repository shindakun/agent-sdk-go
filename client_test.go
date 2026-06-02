package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestClientMultiTurn(t *testing.T) {
	tr := newInteractiveTransport()
	tr.onUser = func(turn int, prompt string) [][]byte {
		lines := [][]byte{}
		if turn == 1 {
			lines = append(lines, []byte(`{"type":"system","subtype":"init","session_id":"sess_X","tools":["Read"]}`))
		}
		lines = append(lines,
			[]byte(fmt.Sprintf(`{"type":"assistant","message":{"model":"m","content":[{"type":"text","text":"reply-%d to %s"}]}}`, turn, prompt)),
			[]byte(fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"result":"reply-%d"}`, turn)),
		)
		return lines
	}
	defer installInteractive(tr)()

	ctx := context.Background()
	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	recv := client.Receive()

	// Turn 1.
	if err := client.Query(ctx, "first"); err != nil {
		t.Fatalf("query 1: %v", err)
	}
	r1 := drainToResult(t, recv)
	if r1.Result != "reply-1" {
		t.Errorf("turn 1 result = %q", r1.Result)
	}

	// The session id captured from init should now be used on subsequent
	// prompts.
	if got := client.sessionIDForTest(); got != "sess_X" {
		t.Errorf("session id = %q, want sess_X", got)
	}

	// Turn 2.
	if err := client.Query(ctx, "second"); err != nil {
		t.Fatalf("query 2: %v", err)
	}
	r2 := drainToResult(t, recv)
	if r2.Result != "reply-2" {
		t.Errorf("turn 2 result = %q", r2.Result)
	}

	// The second prompt must carry the captured session id, not "default".
	lines := tr.writtenLines()
	var secondPromptSID string
	for _, l := range lines {
		var p struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Message   struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		_ = json.Unmarshal(l, &p)
		if p.Type == "user" && p.Message.Content == "second" {
			secondPromptSID = p.SessionID
		}
	}
	if secondPromptSID != "sess_X" {
		t.Errorf("second prompt session_id = %q, want sess_X", secondPromptSID)
	}
}

func TestClientControlRequests(t *testing.T) {
	tr := newInteractiveTransport()
	tr.controlResponder = func(subtype string, payload json.RawMessage) json.RawMessage {
		if subtype == "get_context_usage" {
			return json.RawMessage(`{"used_tokens":1234}`)
		}
		return json.RawMessage(`{}`)
	}
	defer installInteractive(tr)()

	ctx := context.Background()
	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.SetModel(ctx, "opus"); err != nil {
		t.Errorf("set model: %v", err)
	}
	if err := client.SetPermissionMode(ctx, PermissionAcceptEdits); err != nil {
		t.Errorf("set permission mode: %v", err)
	}
	if err := client.Interrupt(ctx); err != nil {
		t.Errorf("interrupt: %v", err)
	}
	usage, err := client.GetContextUsage(ctx)
	if err != nil {
		t.Errorf("get context usage: %v", err)
	}
	if string(usage.Raw) != `{"used_tokens":1234}` {
		t.Errorf("usage payload = %s", usage.Raw)
	}

	// Verify the exact wire field names match the official SDK.
	assertControlSent(t, tr.writtenLines(), "set_model", map[string]any{"model": "opus"})
	assertControlSent(t, tr.writtenLines(), "set_permission_mode", map[string]any{"mode": "acceptEdits"})
	assertControlSent(t, tr.writtenLines(), "interrupt", nil)
}

func TestClientOperationsAfterCloseFail(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()

	ctx := context.Background()
	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := client.Query(ctx, "after close"); err != ErrClosed {
		t.Errorf("query after close err = %v, want ErrClosed", err)
	}
}

// helpers --------------------------------------------------------------------

func (c *Client) sessionIDForTest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sess.sessionID
}

func drainToResult(t *testing.T, recv <-chan Result) *ResultMessage {
	t.Helper()
	for res := range recv {
		if res.Err != nil {
			t.Fatalf("stream err: %v", res.Err)
		}
		if rm, ok := res.Message.(*ResultMessage); ok {
			return rm
		}
	}
	t.Fatal("stream ended before a result message")
	return nil
}

func assertControlSent(t *testing.T, lines [][]byte, subtype string, fields map[string]any) {
	t.Helper()
	for _, l := range lines {
		var env struct {
			Type    string         `json:"type"`
			Request map[string]any `json:"request"`
		}
		if json.Unmarshal(l, &env) != nil || env.Type != "control_request" {
			continue
		}
		if env.Request["subtype"] != subtype {
			continue
		}
		for k, v := range fields {
			if fmt.Sprint(env.Request[k]) != fmt.Sprint(v) {
				t.Errorf("control %q field %q = %v, want %v", subtype, k, env.Request[k], v)
			}
		}
		return
	}
	t.Errorf("no control_request with subtype %q was sent", subtype)
}
