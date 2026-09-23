package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func entry(s string) SessionStoreEntry { return SessionStoreEntry{Data: json.RawMessage(s)} }

func TestProjectKeyForDirectory(t *testing.T) {
	// On Windows "/a/b" resolves onto the current drive, as the CLI's realpath
	// does (D:\a\b keys as "D--a-b").
	want := "-a-b"
	if runtime.GOOS == "windows" {
		abs, _ := filepath.Abs("/a/b")
		want = sanitizePath(abs)
	}
	if got := ProjectKeyForDirectory("/a/b"); got != want {
		t.Errorf("project key = %q, want %q", got, want)
	}

	// A symlinked directory keys by its target, as the CLI's own transcript
	// directory does.
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ProjectKeyForDirectory(link), sanitizePath(resolved); got != want {
		t.Errorf("symlink key = %q, want %q", got, want)
	}
}
