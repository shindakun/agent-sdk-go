package claude

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Ports the HookMatcher on_failure tests from upstream d38e51e
// (tests/test_query.py and tests/test_tool_callbacks.py).

func noopHook(ctx context.Context, in json.RawMessage, id string) (HookOutput, error) {
	return HookOutput{}, nil
}

func TestHookOnFailureInInitialize(t *testing.T) {
	for _, tc := range []struct {
		name       string
		onFailure  HookOnFailure
		wantField  string // "" = key absent
		wantBlocks bool
	}{
		{"unset", "", "", false},
		{"continue", HookOnFailureContinue, `"continue"`, false},
		{"block", HookOnFailureBlock, `"block"`, true},
		// The CLI reads an unknown value as "block".
		{"unknown", "refuse", `"refuse"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := newCallbackRegistry()
			req, err := newOptions(WithHooks(map[HookEvent][]HookMatcher{
				HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{noopHook}, OnFailure: tc.onFailure}},
			})).buildInitializeRequest(reg)
			if err != nil {
				t.Fatal(err)
			}
			var hooks map[string][]map[string]json.RawMessage
			if err := json.Unmarshal(req.Hooks, &hooks); err != nil {
				t.Fatal(err)
			}
			got, ok := hooks["PreToolUse"][0]["onFailure"]
			switch {
			case tc.wantField == "" && ok:
				t.Errorf("onFailure sent as %s, want it omitted", got)
			case tc.wantField != "" && string(got) != tc.wantField:
				t.Errorf("onFailure = %s, want %s", got, tc.wantField)
			}
			if reg.blocksOnFailure != tc.wantBlocks {
				t.Errorf("blocksOnFailure = %v, want %v", reg.blocksOnFailure, tc.wantBlocks)
			}
		})
	}
}

// connectWithInitResponse connects a Client with hooks set to onFailure against
// a transport answering initialize with resp.
func connectWithInitResponse(t *testing.T, onFailure HookOnFailure, resp string) error {
	t.Helper()
	tr := newInteractiveTransport()
	tr.controlResponder = func(subtype string, _ json.RawMessage) json.RawMessage {
		if subtype == "initialize" {
			return json.RawMessage(resp)
		}
		return nil
	}
	defer installInteractive(tr)()
	client := NewClient(WithHooks(map[HookEvent][]HookMatcher{
		HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{noopHook}, OnFailure: onFailure}},
	}))
	err := client.Connect(context.Background())
	_ = client.Close()
	return err
}

func TestHookOnFailureConnectGuards(t *testing.T) {
	for _, tc := range []struct {
		name      string
		onFailure HookOnFailure
		resp      string
		wantErr   string // "" = connect succeeds
	}{
		{"block, supported", HookOnFailureBlock, `{"hook_on_failure_supported":true}`, ""},
		{"block, supported and applied", HookOnFailureBlock, `{"hook_on_failure_supported":true,"hooks_applied":true}`, ""},
		// A CLI that predates onFailure drops the field and answers normally.
		{"block, CLI too old", HookOnFailureBlock, `{}`, "does not honor OnFailure"},
		{"block, support false", HookOnFailureBlock, `{"hook_on_failure_supported":false}`, "does not honor OnFailure"},
		{"block, hooks not applied", HookOnFailureBlock, `{"hook_on_failure_supported":true,"hooks_applied":false}`, "already running"},
		{"unknown value counts as block", "refuse", `{}`, "does not honor OnFailure"},
		// "continue" (or unset) needs no support.
		{"continue, CLI too old", HookOnFailureContinue, `{}`, ""},
		{"unset, CLI too old", "", `{}`, ""},
		{"continue, hooks not applied", HookOnFailureContinue, `{"hooks_applied":false}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := connectWithInitResponse(t, tc.onFailure, tc.resp)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("connect: %v", err)
				}
				return
			}
			var ce *ConnectionError
			if !errors.As(err, &ce) {
				t.Fatalf("connect error = %v (%T), want a *ConnectionError", err, err)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("connect error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// OnFailure "block" relies on a failing callback answering with an error frame:
// the CLI only blocks on an error reply, so a success would let the action run.
// A Go callback fails by returning an error or by panicking.
func TestFailedHookSendsErrorResponse(t *testing.T) {
	for _, tc := range []struct {
		name string
		cb   HookCallback
	}{
		{"returns error", func(ctx context.Context, in json.RawMessage, id string) (HookOutput, error) {
			return HookOutput{}, errors.New("policy service down")
		}},
		{"panics", func(ctx context.Context, in json.RawMessage, id string) (HookOutput, error) {
			panic("policy service down")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newInteractiveTransport()
			defer installInteractive(tr)()
			client := NewClient(WithHooks(map[HookEvent][]HookMatcher{
				HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{tc.cb}, OnFailure: HookOnFailureBlock}},
			}))
			tr.controlResponder = func(subtype string, _ json.RawMessage) json.RawMessage {
				if subtype == "initialize" {
					return json.RawMessage(`{"hook_on_failure_supported":true}`)
				}
				return nil
			}
			if err := client.Connect(context.Background()); err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer func() { _ = client.Close() }()
			_, errStr := tr.sendInbound(t, "req_failed_hook", "hook_callback", map[string]any{
				"callback_id": "hook_0",
				"input":       json.RawMessage(`{"tool_name":"Bash"}`),
				"tool_use_id": "tu_1",
			})
			if errStr == "" {
				t.Error("failing hook answered with success, want an error response")
			}
		})
	}
}
