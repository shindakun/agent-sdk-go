package claude

import (
	"encoding/json"
	"testing"
	"time"
)

func TestForwardSubagentTextInitialize(t *testing.T) {
	for _, tc := range []struct {
		opts []Option
		want string
	}{
		{nil, ""},
		{[]Option{WithForwardSubagentText()}, "true"},
	} {
		req, err := newOptions(tc.opts...).buildInitializeRequest(newCallbackRegistry())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(req)
		var got map[string]json.RawMessage
		_ = json.Unmarshal(b, &got)
		if string(got["forwardSubagentText"]) != tc.want {
			t.Errorf("forwardSubagentText = %q, want %q (request %s)", got["forwardSubagentText"], tc.want, b)
		}
	}
}

func TestSystemPromptSnapshotInitialize(t *testing.T) {
	for name, tc := range map[string]struct {
		opts []Option
		want string
	}{
		"unset":                 {[]Option{WithSystemPrompt("x")}, ""},
		"custom, keep":          {[]Option{WithSystemPrompt("x"), WithSystemPromptSnapshot(true)}, "true"},
		"custom, rebuild":       {[]Option{WithSystemPrompt("x"), WithSystemPromptSnapshot(false)}, "false"},
		"append, rebuild":       {[]Option{WithAppendSystemPrompt("x"), WithSystemPromptSnapshot(false)}, "false"},
		"file ignores snapshot": {[]Option{WithSystemPromptFile("/p"), WithSystemPromptSnapshot(false)}, ""},
		"no prompt":             {[]Option{WithSystemPromptSnapshot(false)}, ""},
	} {
		req, err := newOptions(tc.opts...).buildInitializeRequest(newCallbackRegistry())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(req)
		var got map[string]json.RawMessage
		_ = json.Unmarshal(b, &got)
		if string(got["systemPromptSnapshot"]) != tc.want {
			t.Errorf("%s: systemPromptSnapshot = %q, want %q", name, got["systemPromptSnapshot"], tc.want)
		}
	}
}

// The CLI reads a hook matcher's timeout in seconds (upstream sends
// HookMatcher.timeout as given, in seconds); unset sends none.
func TestHookMatcherTimeoutInSeconds(t *testing.T) {
	for _, tc := range []struct {
		timeout time.Duration
		want    string // "" = key absent
	}{
		{0, ""},
		{30 * time.Second, "30"},
		{1500 * time.Millisecond, "1.5"},
		{2 * time.Minute, "120"},
	} {
		req, err := newOptions(WithHooks(map[HookEvent][]HookMatcher{
			HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{noopHook}, Timeout: tc.timeout}},
		})).buildInitializeRequest(newCallbackRegistry())
		if err != nil {
			t.Fatal(err)
		}
		var hooks map[string][]map[string]json.RawMessage
		if err := json.Unmarshal(req.Hooks, &hooks); err != nil {
			t.Fatal(err)
		}
		got, ok := hooks["PreToolUse"][0]["timeout"]
		switch {
		case tc.want == "" && ok:
			t.Errorf("Timeout %v: timeout sent as %s, want it omitted", tc.timeout, got)
		case tc.want != "" && string(got) != tc.want:
			t.Errorf("Timeout %v: timeout = %s, want %s", tc.timeout, got, tc.want)
		}
	}
}
