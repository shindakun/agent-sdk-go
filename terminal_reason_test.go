package claude

import "testing"

// TestResultTerminalReason pins the terminal_reason wire key and the values
// that distinguish a cancelled turn from a completed one. Values captured from
// live CLI 2.1.222 frames: a normal turn reports "completed", and a turn
// interrupted mid-stream reports "aborted_streaming" with an empty stop_reason.
func TestResultTerminalReason(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "completed",
			raw:  `{"type":"result","subtype":"success","session_id":"s","stop_reason":"end_turn","terminal_reason":"completed"}`,
			want: "completed",
		},
		{
			name: "interrupted",
			raw:  `{"type":"result","subtype":"error_during_execution","session_id":"s","terminal_reason":"aborted_streaming"}`,
			want: "aborted_streaming",
		},
		{
			name: "absent",
			raw:  `{"type":"result","subtype":"success","session_id":"s"}`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := UnmarshalMessage([]byte(c.raw))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := m.(*ResultMessage).TerminalReason; got != c.want {
				t.Errorf("TerminalReason = %q, want %q", got, c.want)
			}
		})
	}
}
