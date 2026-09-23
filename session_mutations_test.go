package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ports tests/test_session_mutations.py from the Python SDK.

// smKV is an ordered JSON object (alternating keys and values).
type smKV []any

// smPyJSON encodes v like Python's json.dumps defaults (", " and ": ").
func smPyJSON(v any) string {
	switch x := v.(type) {
	case smKV:
		var b strings.Builder
		b.WriteByte('{')
		for i := 0; i+1 < len(x); i += 2 {
			if i > 0 {
				b.WriteString(", ")
			}
			k, _ := json.Marshal(x[i])
			b.Write(k)
			b.WriteString(": ")
			b.WriteString(smPyJSON(x[i+1]))
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = smPyJSON(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// smConfigDir mirrors the claude_config_dir fixture.
func smConfigDir(t *testing.T) (configDir, tmp string) {
	t.Helper()
	tmp = t.TempDir()
	setHomeDir(t, t.TempDir())
	configDir = filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(filepath.Join(configDir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	return configDir, tmp
}

// smMakeProjectDir mirrors _make_project_dir (projectPath is already real).
func smMakeProjectDir(t *testing.T, configDir, projectPath string) string {
	t.Helper()
	d := filepath.Join(configDir, "projects", sanitizePath(projectPath))
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// smProject creates <tmp>/proj and its project dir; returns (projectPath, projectDir).
func smProject(t *testing.T, configDir, tmp string) (string, string) {
	t.Helper()
	projectPath := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	realPath, err := filepath.EvalSymlinks(projectPath)
	if err != nil {
		t.Fatal(err)
	}
	return projectPath, smMakeProjectDir(t, configDir, realPath)
}

func smWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func smReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// smMakeSessionFile mirrors _make_session_file.
func smMakeSessionFile(t *testing.T, projectDir, sid, firstPrompt string) (string, string) {
	t.Helper()
	if sid == "" {
		sid = newUUID()
	}
	if firstPrompt == "" {
		firstPrompt = "Hello Claude"
	}
	path := filepath.Join(projectDir, sid+".jsonl")
	lines := []string{
		smPyJSON(smKV{"type", "user", "message", smKV{"role", "user", "content", firstPrompt}}),
		smPyJSON(smKV{"type", "assistant", "message", smKV{"role", "assistant", "content", "Hi!"}}),
	}
	smWriteFile(t, path, strings.Join(lines, "\n")+"\n")
	return sid, path
}

// smMakeTranscriptSession mirrors _make_transcript_session.
func smMakeTranscriptSession(t *testing.T, projectDir string, numTurns int) (string, string, []string) {
	t.Helper()
	sid := newUUID()
	path := filepath.Join(projectDir, sid+".jsonl")
	var uuids, lines []string
	var parent any
	for i := 0; i < numTurns; i++ {
		u := newUUID()
		uuids = append(uuids, u)
		lines = append(lines, smPyJSON(smKV{
			"type", "user", "uuid", u, "parentUuid", parent, "sessionId", sid,
			"timestamp", "2026-03-01T00:00:00Z",
			"message", smKV{"role", "user", "content", "Turn " + smItoa(i+1) + " question"},
		}))
		parent = u
		a := newUUID()
		uuids = append(uuids, a)
		lines = append(lines, smPyJSON(smKV{
			"type", "assistant", "uuid", a, "parentUuid", parent, "sessionId", sid,
			"timestamp", "2026-03-01T00:00:00Z",
			"message", smKV{"role", "assistant", "content", []any{smKV{"type", "text", "text", "Turn " + smItoa(i+1) + " answer"}}},
		}))
		parent = a
	}
	smWriteFile(t, path, strings.Join(lines, "\n")+"\n")
	return sid, path, uuids
}

func smItoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func smLines(t *testing.T, path string) []string {
	t.Helper()
	return strings.Split(strings.TrimSpace(smReadFile(t, path)), "\n")
}

func smLastEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	lines := smLines(t, path)
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("last line %q: %v", lines[len(lines)-1], err)
	}
	return m
}

func smEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range smLines(t, path) {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

// smWantErr asserts err is non-nil and its message contains sub (case-insensitive).
func smWantErr(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", sub)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(sub)) {
		t.Fatalf("error %q does not contain %q", err, sub)
	}
}

func smWantNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func smNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionMutationsTryAppend(t *testing.T) {
	t.Run("append_to_existing_file", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "test.jsonl")
		smWriteFile(t, f, "line1\n")
		ok, err := tryAppend(f, []byte("line2\n"))
		smNoErr(t, err)
		if !ok {
			t.Fatal("expected true")
		}
		if got := smReadFile(t, f); got != "line1\nline2\n" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("missing_file_returns_false", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "nonexistent.jsonl")
		ok, err := tryAppend(f, []byte("data\n"))
		smNoErr(t, err)
		if ok {
			t.Fatal("expected false")
		}
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("file was created")
		}
	})
	t.Run("missing_parent_dir_returns_false", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "nonexistent", "file.jsonl")
		ok, err := tryAppend(f, []byte("data\n"))
		smNoErr(t, err)
		if ok {
			t.Fatal("expected false")
		}
	})
	t.Run("zero_byte_file_returns_false", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "stub.jsonl")
		smWriteFile(t, f, "")
		ok, err := tryAppend(f, []byte("data\n"))
		smNoErr(t, err)
		if ok {
			t.Fatal("expected false")
		}
		if got := smReadFile(t, f); got != "" {
			t.Fatalf("stub modified: %q", got)
		}
	})
	t.Run("multiple_appends", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "test.jsonl")
		smWriteFile(t, f, "line1\n")
		_, _ = tryAppend(f, []byte("line2\n"))
		_, _ = tryAppend(f, []byte("line3\n"))
		if got := smReadFile(t, f); got != "line1\nline2\nline3\n" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestSessionMutationsRenameSession(t *testing.T) {
	t.Run("invalid_session_id_raises", func(t *testing.T) {
		smConfigDir(t)
		smWantErr(t, RenameSession("not-a-uuid", "title", ""), "invalid session id")
		smWantErr(t, RenameSession("", "title", ""), "invalid session id")
	})
	t.Run("empty_title_raises", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _ := smMakeSessionFile(t, projectDir, "", "")
		for _, title := range []string{"", "   ", "\n\t"} {
			smWantErr(t, RenameSession(sid, title, projectPath), "title must be non-empty")
		}
	})
	t.Run("session_not_found_raises", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, _ := smProject(t, cfg, tmp)
		smWantNotFound(t, RenameSession(newUUID(), "title", projectPath))
	})
	t.Run("no_projects_dir_raises", func(t *testing.T) {
		tmp := t.TempDir()
		setHomeDir(t, t.TempDir())
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, "nonexistent"))
		err := RenameSession(newUUID(), "title", "")
		smWantNotFound(t, err)
		if !strings.Contains(err.Error(), "no projects directory") {
			t.Errorf("err = %v, want it to say no projects directory", err)
		}
	})
	t.Run("appends_custom_title_entry", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, RenameSession(sid, "My New Title", projectPath))
		e := smLastEntry(t, path)
		if e["type"] != "custom-title" || e["customTitle"] != "My New Title" || e["sessionId"] != sid {
			t.Fatalf("entry = %v", e)
		}
	})
	t.Run("title_trimmed_before_storing", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, RenameSession(sid, "  Trimmed Title  ", projectPath))
		if got := smLastEntry(t, path)["customTitle"]; got != "Trimmed Title" {
			t.Fatalf("customTitle = %v", got)
		}
	})
	t.Run("last_wins_via_list_sessions", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _ := smMakeSessionFile(t, projectDir, "", "original")
		for _, title := range []string{"First Title", "Second Title", "Final Title"} {
			smNoErr(t, RenameSession(sid, title, projectPath))
		}
		sessions, err := ListSessions(projectPath, 0, 0, ListIncludeWorktrees(false))
		smNoErr(t, err)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		if sessions[0].CustomTitle != "Final Title" || sessions[0].Summary != "Final Title" {
			t.Fatalf("session = %+v", sessions[0])
		}
	})
	t.Run("search_all_projects", func(t *testing.T) {
		cfg, _ := smConfigDir(t)
		projectDir := smMakeProjectDir(t, cfg, "/some/project")
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, RenameSession(sid, "Found Without Dir", ""))
		if got := smLastEntry(t, path)["customTitle"]; got != "Found Without Dir" {
			t.Fatalf("customTitle = %v", got)
		}
	})
	t.Run("skips_zero_byte_stub", func(t *testing.T) {
		cfg, _ := smConfigDir(t)
		projA := smMakeProjectDir(t, cfg, "/aaa/project")
		projZ := smMakeProjectDir(t, cfg, "/zzz/project")
		sid := newUUID()
		stub := filepath.Join(projA, sid+".jsonl")
		smWriteFile(t, stub, "")
		smMakeSessionFile(t, projZ, sid, "real")
		smNoErr(t, RenameSession(sid, "New Title", ""))
		if got := smReadFile(t, stub); got != "" {
			t.Fatalf("stub modified: %q", got)
		}
		if real := smReadFile(t, filepath.Join(projZ, sid+".jsonl")); !strings.Contains(real, `"customTitle":"New Title"`) {
			t.Fatalf("real file missing entry: %q", real)
		}
	})
	t.Run("compact_json_format", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, RenameSession(sid, "Title", projectPath))
		lines := smLines(t, path)
		want := `{"type":"custom-title","customTitle":"Title","sessionId":"` + sid + `"}`
		if lines[len(lines)-1] != want {
			t.Fatalf("got %s\nwant %s", lines[len(lines)-1], want)
		}
	})
}

func TestSessionMutationsTagSession(t *testing.T) {
	t.Run("invalid_session_id_raises", func(t *testing.T) {
		smConfigDir(t)
		smWantErr(t, TagSession("not-a-uuid", "tag", ""), "invalid session id")
		smWantErr(t, TagSession("", "tag", ""), "invalid session id")
	})
	t.Run("empty_tag_raises", func(t *testing.T) {
		// Python's tag_session(sid, "") raise has no Go form ("" clears).
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _ := smMakeSessionFile(t, projectDir, "", "")
		smWantErr(t, TagSession(sid, "   ", projectPath), "tag must be non-empty")
	})
	t.Run("session_not_found_raises", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, _ := smProject(t, cfg, tmp)
		smWantNotFound(t, TagSession(newUUID(), "tag", projectPath))
	})
	t.Run("appends_tag_entry", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, TagSession(sid, "experiment", projectPath))
		e := smLastEntry(t, path)
		if e["type"] != "tag" || e["tag"] != "experiment" || e["sessionId"] != sid {
			t.Fatalf("entry = %v", e)
		}
	})
	t.Run("tag_trimmed", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, TagSession(sid, "  my-tag  ", projectPath))
		if got := smLastEntry(t, path)["tag"]; got != "my-tag" {
			t.Fatalf("tag = %v", got)
		}
	})
	t.Run("none_clears_tag", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, TagSession(sid, "original-tag", projectPath))
		smNoErr(t, TagSession(sid, "", projectPath))
		e := smLastEntry(t, path)
		if e["type"] != "tag" || e["tag"] != "" || e["sessionId"] != sid {
			t.Fatalf("entry = %v", e)
		}
	})
	t.Run("last_wins", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		for _, tag := range []string{"first", "second", "third"} {
			smNoErr(t, TagSession(sid, tag, projectPath))
		}
		if got := smLastEntry(t, path)["tag"]; got != "third" {
			t.Fatalf("tag = %v", got)
		}
		n := 0
		for _, e := range smEntries(t, path) {
			if e["type"] == "tag" {
				n++
			}
		}
		if n != 3 {
			t.Fatalf("got %d tag entries", n)
		}
	})
	t.Run("compact_json_format", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, TagSession(sid, "mytag", projectPath))
		lines := smLines(t, path)
		want := `{"type":"tag","tag":"mytag","sessionId":"` + sid + `"}`
		if lines[len(lines)-1] != want {
			t.Fatalf("got %s\nwant %s", lines[len(lines)-1], want)
		}
	})
	t.Run("unicode_sanitization", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, TagSession(sid, "clean\u200btag\ufeff", projectPath))
		if got := smLastEntry(t, path)["tag"]; got != "cleantag" {
			t.Fatalf("tag = %q", got)
		}
	})
	t.Run("sanitization_rejects_pure_invisible", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _ := smMakeSessionFile(t, projectDir, "", "")
		smWantErr(t, TagSession(sid, "\u200b\u200c\ufeff", projectPath), "tag must be non-empty")
	})
}

func TestSessionMutationsSanitizeUnicode(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"passthrough_clean_string", "hello", "hello"},
		{"passthrough_clean_string", "tag-with-dashes_123", "tag-with-dashes_123"},
		{"strips_zero_width", "a\u200bb", "ab"},
		{"strips_zero_width", "a\u200cb", "ab"},
		{"strips_zero_width", "a\u200db", "ab"},
		{"strips_bom", "\ufeffhello", "hello"},
		{"strips_directional_marks", "a\u202ab\u202cc", "abc"},
		{"strips_directional_marks", "a\u2066b\u2069c", "abc"},
		{"strips_private_use", "a\ue000b", "ab"},
		{"strips_private_use", "a\uf8ffb", "ab"},
		{"iterative_converges", "a" + strings.Repeat("\u200b", 20) + "b", "ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeUnicode(c.in); got != c.want {
				t.Fatalf("sanitizeUnicode(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
	t.Run("nfkc_normalization", func(t *testing.T) {
		if got := sanitizeUnicode("\uff21"); got != "A" {
			t.Skip("NFKC normalization not implemented")
		}
	})
}

func TestSessionMutationsDeleteSession(t *testing.T) {
	t.Run("invalid_session_id_raises", func(t *testing.T) {
		smConfigDir(t)
		smWantErr(t, DeleteSession("not-a-uuid", ""), "invalid session id")
	})
	t.Run("session_not_found_raises", func(t *testing.T) {
		smConfigDir(t)
		err := DeleteSession(newUUID(), "")
		smWantNotFound(t, err)
		smWantErr(t, err, "not found")
	})
	t.Run("deletes_session_file", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		smNoErr(t, DeleteSession(sid, projectPath))
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("session file still exists")
		}
	})
	t.Run("removes_subagent_transcript_dir", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		sub := filepath.Join(projectDir, sid)
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		smWriteFile(t, filepath.Join(sub, newUUID()+".jsonl"), "{}\n")
		smNoErr(t, DeleteSession(sid, projectPath))
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("session file still exists")
		}
		if _, err := os.Stat(sub); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("subagent dir still exists")
		}
	})
	t.Run("deletes_without_directory", func(t *testing.T) {
		cfg, _ := smConfigDir(t)
		projectDir := smMakeProjectDir(t, cfg, "/any/project")
		sid, path := smMakeSessionFile(t, projectDir, "", "")
		smNoErr(t, DeleteSession(sid, ""))
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("session file still exists")
		}
	})
	t.Run("no_longer_in_list_sessions", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _ := smMakeSessionFile(t, projectDir, "", "")
		has := func() bool {
			sessions, err := ListSessions(projectPath, 0, 0)
			smNoErr(t, err)
			for _, s := range sessions {
				if s.SessionID == sid {
					return true
				}
			}
			return false
		}
		if !has() {
			t.Fatal("session not listed before delete")
		}
		smNoErr(t, DeleteSession(sid, projectPath))
		if has() {
			t.Fatal("session still listed after delete")
		}
	})
}

func TestSessionMutationsForkSession(t *testing.T) {
	t.Run("invalid_session_id_raises", func(t *testing.T) {
		smConfigDir(t)
		_, err := ForkSession("not-a-uuid", "", "", "")
		smWantErr(t, err, "invalid session id")
	})
	t.Run("session_not_found_raises", func(t *testing.T) {
		smConfigDir(t)
		_, err := ForkSession(newUUID(), "", "", "")
		smWantNotFound(t, err)
		smWantErr(t, err, "not found")
	})
	t.Run("invalid_up_to_message_id_raises", func(t *testing.T) {
		smConfigDir(t)
		_, err := ForkSession(newUUID(), "", "not-valid", "")
		smWantErr(t, err, "invalid up-to message id")
	})
	t.Run("fork_creates_new_session", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		if res.SessionID == sid {
			t.Fatal("fork reused the source session id")
		}
		if _, err := os.Stat(filepath.Join(projectDir, res.SessionID+".jsonl")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("fork_remaps_uuids", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, orig := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		isOrig := map[any]bool{}
		for _, u := range orig {
			isOrig[u] = true
		}
		for _, e := range smEntries(t, filepath.Join(projectDir, res.SessionID+".jsonl")) {
			if e["type"] != "user" && e["type"] != "assistant" {
				continue
			}
			if isOrig[e["uuid"]] {
				t.Fatalf("uuid not remapped: %v", e["uuid"])
			}
			if p := e["parentUuid"]; p != nil && isOrig[p] {
				t.Fatalf("parentUuid not remapped: %v", p)
			}
		}
	})
	t.Run("fork_preserves_message_count", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 3)
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		origMsgs, err := GetSessionMessages(sid, projectPath, 0, 0)
		smNoErr(t, err)
		forkMsgs, err := GetSessionMessages(res.SessionID, projectPath, 0, 0)
		smNoErr(t, err)
		if len(forkMsgs) != len(origMsgs) {
			t.Fatalf("fork has %d messages, original %d", len(forkMsgs), len(origMsgs))
		}
	})
	t.Run("fork_up_to_message_id", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, uuids := smMakeTranscriptSession(t, projectDir, 3)
		res, err := ForkSession(sid, projectPath, uuids[1], "")
		smNoErr(t, err)
		msgs, err := GetSessionMessages(res.SessionID, projectPath, 0, 0)
		smNoErr(t, err)
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
	})
	t.Run("fork_up_to_message_id_not_found_raises", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		_, err := ForkSession(sid, projectPath, newUUID(), "")
		smWantErr(t, err, "not found in session")
	})
	smForkInfo := func(t *testing.T, projectPath, id string) SDKSessionInfo {
		t.Helper()
		sessions, err := ListSessions(projectPath, 0, 0)
		smNoErr(t, err)
		for _, s := range sessions {
			if s.SessionID == id {
				return s
			}
		}
		t.Fatalf("fork %s not listed", id)
		return SDKSessionInfo{}
	}
	t.Run("fork_custom_title", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, projectPath, "", "My Fork")
		smNoErr(t, err)
		if got := smForkInfo(t, projectPath, res.SessionID).CustomTitle; got != "My Fork" {
			t.Fatalf("CustomTitle = %q", got)
		}
	})
	t.Run("fork_default_title_has_suffix", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		got := smForkInfo(t, projectPath, res.SessionID).CustomTitle
		if got == "" || !strings.HasSuffix(got, "(fork)") {
			t.Fatalf("CustomTitle = %q", got)
		}
	})
	t.Run("fork_session_id_in_entries", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		for _, e := range smEntries(t, filepath.Join(projectDir, res.SessionID+".jsonl")) {
			if e["sessionId"] != res.SessionID {
				t.Fatalf("entry sessionId = %v, want %s", e["sessionId"], res.SessionID)
			}
		}
	})
	t.Run("fork_forked_from_field", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		for _, e := range smEntries(t, filepath.Join(projectDir, res.SessionID+".jsonl")) {
			if e["type"] != "user" && e["type"] != "assistant" {
				continue
			}
			ff, _ := e["forkedFrom"].(map[string]any)
			if ff == nil || ff["sessionId"] != sid {
				t.Fatalf("forkedFrom = %v", e["forkedFrom"])
			}
		}
	})
	t.Run("fork_without_directory", func(t *testing.T) {
		cfg, _ := smConfigDir(t)
		projectDir := smMakeProjectDir(t, cfg, "/any/project")
		sid, _, _ := smMakeTranscriptSession(t, projectDir, 2)
		res, err := ForkSession(sid, "", "", "")
		smNoErr(t, err)
		if _, err := os.Stat(filepath.Join(projectDir, res.SessionID+".jsonl")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("fork_clears_stale_fields", func(t *testing.T) {
		cfg, tmp := smConfigDir(t)
		projectPath, projectDir := smProject(t, cfg, tmp)
		sid := newUUID()
		entry := smKV{
			"type", "user", "uuid", newUUID(), "parentUuid", nil, "sessionId", sid,
			"timestamp", "2026-03-01T00:00:00Z",
			"teamName", "test-team", "agentName", "test-agent", "slug", "test-slug",
			"message", smKV{"role", "user", "content", "Hello"},
		}
		smWriteFile(t, filepath.Join(projectDir, sid+".jsonl"), smPyJSON(entry)+"\n")
		res, err := ForkSession(sid, projectPath, "", "")
		smNoErr(t, err)
		for _, e := range smEntries(t, filepath.Join(projectDir, res.SessionID+".jsonl")) {
			if e["type"] != "user" {
				continue
			}
			for _, k := range []string{"teamName", "agentName", "slug"} {
				if _, ok := e[k]; ok {
					t.Fatalf("%s not cleared: %v", k, e)
				}
			}
		}
	})
}
