package claude

import (
	"encoding/json"
	"testing"
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
