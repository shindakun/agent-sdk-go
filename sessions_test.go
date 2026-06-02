package claude

import (
	"os"
	"path/filepath"
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

// writeSession creates a session JSONL file under a fake HOME and returns the
// session id.
func writeSession(t *testing.T, home, cwd, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", sanitizePath(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setHomeDir points os.UserHomeDir() at home on all platforms (HOME on Unix,
// USERPROFILE on Windows).
func setHomeDir(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestListAndReadSessions(t *testing.T) {
	home := t.TempDir()
	setHomeDir(t, home)
	cwd := "/work/proj"

	writeSession(t, home, cwd, "sess-a",
		`{"type":"user","uuid":"u1","sessionId":"sess-a","message":{"role":"user","content":"first prompt"},"cwd":"/work/proj","gitBranch":"main","timestamp":"2026-01-02T03:04:05Z"}`,
		`{"type":"assistant","uuid":"a1","sessionId":"sess-a","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	)
	writeSession(t, home, cwd, "sess-b",
		`{"type":"user","uuid":"u2","sessionId":"sess-b","message":{"role":"user","content":"second"},"customTitle":"My Session"}`,
	)

	infos, err := ListSessions(cwd, 0, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("got %d sessions, want 2", len(infos))
	}

	// Look up specifics by id (order is by mtime, which the test does not pin).
	byID := map[string]SDKSessionInfo{}
	for _, s := range infos {
		byID[s.SessionID] = s
	}
	a := byID["sess-a"]
	if a.FirstPrompt != "first prompt" || a.GitBranch != "main" || a.Cwd != "/work/proj" {
		t.Errorf("sess-a info = %+v", a)
	}
	if a.CreatedAt == 0 {
		t.Error("sess-a created_at not parsed from timestamp")
	}
	b := byID["sess-b"]
	if b.CustomTitle != "My Session" || b.Summary != "My Session" {
		t.Errorf("sess-b summary/title = %+v", b)
	}

	// Messages.
	msgs, err := GetSessionMessages("sess-a", cwd, 0, 0)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Type != "user" || msgs[1].Type != "assistant" {
		t.Errorf("messages = %+v", msgs)
	}

	// Single info.
	si, err := GetSessionInfo("sess-a", cwd)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if si.SessionID != "sess-a" || si.FileSize == 0 {
		t.Errorf("info = %+v", si)
	}
}

func TestListSessionsMissingDirIsEmpty(t *testing.T) {
	home := t.TempDir()
	setHomeDir(t, home)
	infos, err := ListSessions("/nonexistent/dir", 0, 0)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected no sessions, got %d", len(infos))
	}
}

func TestSubagents(t *testing.T) {
	home := t.TempDir()
	setHomeDir(t, home)
	cwd := "/work/proj"

	writeSession(t, home, cwd, "sess-c",
		`{"type":"user","uuid":"u1","sessionId":"sess-c","message":{"content":"go"}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"sess-c","agentId":"agent-1","message":{"content":[{"type":"text","text":"sub work"}]}}`,
		`{"type":"user","uuid":"u2","sessionId":"sess-c","agentId":"agent-1","message":{"content":"more"}}`,
	)

	ids, err := ListSubagents("sess-c", cwd)
	if err != nil {
		t.Fatalf("list subagents: %v", err)
	}
	if len(ids) != 1 || ids[0] != "agent-1" {
		t.Errorf("subagent ids = %v", ids)
	}

	msgs, err := GetSubagentMessages("sess-c", "agent-1", cwd, 0, 0)
	if err != nil {
		t.Fatalf("subagent messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("subagent messages = %d, want 2", len(msgs))
	}
}
