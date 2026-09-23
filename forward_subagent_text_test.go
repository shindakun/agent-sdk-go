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
