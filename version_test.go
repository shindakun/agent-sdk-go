package claude

import "testing"

func TestParseCLIVersion(t *testing.T) {
	cases := map[string]string{
		"2.1.159 (Claude Code)\n": "2.1.159",
		"2.1.159":                 "2.1.159",
		"  2.0.0 (x)  ":           "2.0.0",
	}
	for in, want := range cases {
		if got := parseCLIVersion(in); got != want {
			t.Errorf("parseCLIVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCLIVersionMismatchError(t *testing.T) {
	e := &CLIVersionMismatch{Installed: "2.1.0", Supported: "2.1.159"}
	if e.Error() == "" {
		t.Error("empty error string")
	}
}
