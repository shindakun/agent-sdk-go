package claude

import (
	"strings"
	"testing"
)

func TestSanitizePath(t *testing.T) {
	got := sanitizePath("/Users/steve/go/src/proj")
	if strings.ContainsAny(got, "/.") {
		t.Errorf("sanitized path still has separators: %q", got)
	}
	if got != "-Users-steve-go-src-proj" {
		t.Errorf("sanitize = %q", got)
	}

	// Vectors from the official SDK's _simple_hash: the last three hash
	// negative, where the CLI takes the absolute value.
	for in, want := range map[string]string{
		"":                                   "0",
		"hello":                              "1n1e4y",
		"/" + strings.Repeat("a", 300):       "vtkmfl",
		"/Users/" + strings.Repeat("x", 250): "v4ezpm",
		"/" + strings.Repeat("a", 300) + "/ünï": "zbxwf7",
	} {
		if got := simpleHash(in); got != want {
			t.Errorf("simpleHash(%.12q...) = %s, want %s", in, got, want)
		}
	}

	// Long paths get truncated with a base-36 hash suffix.
	long := "/" + strings.Repeat("a", 300)
	s := sanitizePath(long)
	if len(s) <= maxSanitizedLength {
		t.Errorf("long path not extended with hash: len=%d", len(s))
	}
	if !strings.Contains(s, "-") {
		t.Error("expected hash suffix delimiter")
	}
}

// setHomeDir points os.UserHomeDir() at home on all platforms (HOME on Unix,
// USERPROFILE on Windows).
func setHomeDir(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
}
