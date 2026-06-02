package claude

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCanUseToolAllowWithUpdatedInput(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()

	ctx := context.Background()
	client := NewClient(WithCanUseTool(
		func(ctx context.Context, tool string, input json.RawMessage, pc PermissionContext) (PermissionResult, error) {
			if tool != "Bash" {
				t.Errorf("tool = %q", tool)
			}
			if pc.ToolUseID != "tu_77" {
				t.Errorf("tool_use_id = %q", pc.ToolUseID)
			}
			return PermissionAllow{UpdatedInput: json.RawMessage(`{"command":"ls -la"}`)}, nil
		}))
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	resp, errStr := tr.sendInbound(t, "req_in_1", "can_use_tool", map[string]any{
		"tool_name":   "Bash",
		"input":       json.RawMessage(`{"command":"ls"}`),
		"tool_use_id": "tu_77",
	})
	if errStr != "" {
		t.Fatalf("error response: %s", errStr)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal resp: %v (%s)", err, resp)
	}
	if string(got["behavior"]) != `"allow"` {
		t.Errorf("behavior = %s", got["behavior"])
	}
	if string(got["updatedInput"]) != `{"command":"ls -la"}` {
		t.Errorf("updatedInput = %s", got["updatedInput"])
	}
}

func TestCanUseToolDeny(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()

	ctx := context.Background()
	client := NewClient(WithCanUseTool(
		func(ctx context.Context, tool string, input json.RawMessage, pc PermissionContext) (PermissionResult, error) {
			return PermissionDeny{Message: "not allowed", Interrupt: true}, nil
		}))
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	resp, errStr := tr.sendInbound(t, "req_in_2", "can_use_tool", map[string]any{
		"tool_name": "Write",
		"input":     json.RawMessage(`{}`),
	})
	if errStr != "" {
		t.Fatalf("error response: %s", errStr)
	}
	var got map[string]any
	_ = json.Unmarshal(resp, &got)
	if got["behavior"] != "deny" || got["message"] != "not allowed" || got["interrupt"] != true {
		t.Errorf("deny response = %v", got)
	}
}

func TestHookCallbackDispatchAndConfig(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()

	var called bool
	block := false
	client := NewClient(WithHooks(map[HookEvent][]HookMatcher{
		HookPreToolUse: {{
			Matcher: "Bash",
			Callbacks: []HookCallback{
				func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error) {
					called = true
					return HookOutput{
						Decision:      "block",
						SystemMessage: "blocked by policy",
						Continue:      &block,
					}, nil
				},
			},
		}},
	}))

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// The initialize request must advertise the hook under hookCallbackIds.
	assertHooksConfigSent(t, tr.writtenLines())

	// Dispatch the hook callback (id hook_0 is the first assigned).
	resp, errStr := tr.sendInbound(t, "req_in_3", "hook_callback", map[string]any{
		"callback_id": "hook_0",
		"input":       json.RawMessage(`{"tool_name":"Bash"}`),
		"tool_use_id": "tu_1",
	})
	if errStr != "" {
		t.Fatalf("error response: %s", errStr)
	}
	if !called {
		t.Error("hook callback was not invoked")
	}
	var got map[string]any
	_ = json.Unmarshal(resp, &got)
	if got["decision"] != "block" {
		t.Errorf("decision = %v", got["decision"])
	}
	if got["systemMessage"] != "blocked by policy" {
		t.Errorf("systemMessage = %v", got["systemMessage"])
	}
	if got["continue"] != false {
		t.Errorf("continue = %v, want false", got["continue"])
	}
}

func TestUnsupportedControlRequestErrors(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()

	// A permission callback makes the handler non-nil, so an unknown subtype
	// reaches the default branch and returns an error response.
	client := NewClient(WithCanUseTool(
		func(ctx context.Context, tool string, in json.RawMessage, pc PermissionContext) (PermissionResult, error) {
			return PermissionAllow{}, nil
		}))
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, errStr := tr.sendInbound(t, "req_in_4", "totally_unknown", nil)
	if errStr == "" {
		t.Error("expected error response for unknown subtype")
	}
}

func assertHooksConfigSent(t *testing.T, lines [][]byte) {
	t.Helper()
	for _, l := range lines {
		var env struct {
			Type    string `json:"type"`
			Request struct {
				Subtype string                       `json:"subtype"`
				Hooks   map[string][]json.RawMessage `json:"hooks"`
			} `json:"request"`
		}
		if json.Unmarshal(l, &env) != nil || env.Type != "control_request" || env.Request.Subtype != "initialize" {
			continue
		}
		entries, ok := env.Request.Hooks["PreToolUse"]
		if !ok || len(entries) == 0 {
			t.Fatalf("initialize hooks missing PreToolUse: %s", l)
		}
		var entry struct {
			Matcher         string   `json:"matcher"`
			HookCallbackIDs []string `json:"hookCallbackIds"`
		}
		_ = json.Unmarshal(entries[0], &entry)
		if entry.Matcher != "Bash" {
			t.Errorf("matcher = %q", entry.Matcher)
		}
		if len(entry.HookCallbackIDs) != 1 || entry.HookCallbackIDs[0] != "hook_0" {
			t.Errorf("hookCallbackIds = %v", entry.HookCallbackIDs)
		}
		return
	}
	t.Fatal("no initialize control_request with hooks config was sent")
}
