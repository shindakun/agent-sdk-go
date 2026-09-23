package claude

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Port of tests/test_sessions.py from claude-agent-sdk-python.

// spKV and spObj build JSON objects with a fixed key order, so fixtures can be
// written byte for byte the way Python's json.dumps writes them.
type spKV struct {
	k string
	v any
}

type spObj []spKV

func spEncode(b *strings.Builder, v any, compact bool) {
	itemSep, keySep := ", ", ": "
	if compact {
		itemSep, keySep = ",", ":"
	}
	switch x := v.(type) {
	case spObj:
		b.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				b.WriteString(itemSep)
			}
			spEncode(b, kv.k, compact)
			b.WriteString(keySep)
			spEncode(b, kv.v, compact)
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(itemSep)
			}
			spEncode(b, e, compact)
		}
		b.WriteByte(']')
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			panic(err)
		}
		b.WriteString(strings.TrimSuffix(buf.String(), "\n"))
	}
}

// spDumps mirrors json.dumps with default separators.
func spDumps(v any) string {
	var b strings.Builder
	spEncode(&b, v, false)
	return b.String()
}

// spDumpsCompact mirrors json.dumps(..., separators=(",", ":")).
func spDumpsCompact(v any) string {
	var b strings.Builder
	spEncode(&b, v, true)
	return b.String()
}

func spUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// spConfigDir mirrors the claude_config_dir fixture: an isolated HOME and a
// CLAUDE_CONFIG_DIR with an empty projects dir. It returns the config dir and
// the test's tmp_path.
func spConfigDir(t *testing.T) (string, string) {
	t.Helper()
	tmp := t.TempDir()
	setHomeDir(t, filepath.Join(tmp, "home"))
	cfg := filepath.Join(tmp, ".claude")
	if err := os.MkdirAll(filepath.Join(cfg, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	return cfg, tmp
}

// spMissingConfigDir points CLAUDE_CONFIG_DIR at a directory that does not exist.
func spMissingConfigDir(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	setHomeDir(t, filepath.Join(tmp, "home"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, "nonexistent"))
}

func spRealpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func spMkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func spWriteFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func spWriteLines(t *testing.T, p string, lines ...string) {
	t.Helper()
	spWriteFile(t, p, strings.Join(lines, "\n")+"\n")
}

func spSetMtime(t *testing.T, p string, sec int64) {
	t.Helper()
	ts := time.Unix(sec, 0)
	if err := os.Chtimes(p, ts, ts); err != nil {
		t.Fatal(err)
	}
}

// spMakeProjectDir mirrors _make_project_dir.
func spMakeProjectDir(t *testing.T, cfg, projectPath string) string {
	t.Helper()
	return spMkdir(t, filepath.Join(cfg, "projects", sanitizePath(projectPath)))
}

// spProject creates tmp/name and its project dir, returning both.
func spProject(t *testing.T, cfg, tmp, name string) (projectPath, projectDir string) {
	t.Helper()
	projectPath = spMkdir(t, filepath.Join(tmp, name))
	return projectPath, spMakeProjectDir(t, cfg, spRealpath(t, projectPath))
}

type spSessionOpts struct {
	sessionID   string
	firstPrompt string
	summary     string
	customTitle string
	gitBranch   string
	cwd         string
	isSidechain bool
	isMetaOnly  bool
	mtime       int64
}

// spMakeSessionFile mirrors _make_session_file; empty strings stand for None.
func spMakeSessionFile(t *testing.T, projectDir string, o spSessionOpts) (string, string) {
	t.Helper()
	sid := o.sessionID
	if sid == "" {
		sid = spUUID(t)
	}
	prompt := o.firstPrompt
	if prompt == "" {
		prompt = "Hello Claude"
	}
	path := filepath.Join(projectDir, sid+".jsonl")

	first := spObj{{"type", "user"}, {"message", spObj{{"role", "user"}, {"content", prompt}}}}
	if o.cwd != "" {
		first = append(first, spKV{"cwd", o.cwd})
	}
	if o.gitBranch != "" {
		first = append(first, spKV{"gitBranch", o.gitBranch})
	}
	if o.isSidechain {
		first = append(first, spKV{"isSidechain", true})
	}
	if o.isMetaOnly {
		first = append(first, spKV{"isMeta", true})
	}
	assistant := spObj{{"type", "assistant"}, {"message", spObj{{"role", "assistant"}, {"content", "Hi there!"}}}}
	tail := spObj{{"type", "summary"}}
	if o.summary != "" {
		tail = append(tail, spKV{"summary", o.summary})
	}
	if o.customTitle != "" {
		tail = append(tail, spKV{"customTitle", o.customTitle})
	}
	if o.gitBranch != "" {
		tail = append(tail, spKV{"gitBranch", o.gitBranch})
	}
	spWriteLines(t, path, spDumps(first), spDumps(assistant), spDumps(tail))
	if o.mtime != 0 {
		spSetMtime(t, path, o.mtime)
	}
	return sid, path
}

// spEntry mirrors _make_transcript_entry; parent "" is null, content nil is omitted.
func spEntry(typ, id, parent, sid string, content any, extras ...spKV) spObj {
	var p any
	if parent != "" {
		p = parent
	}
	e := spObj{{"type", typ}, {"uuid", id}, {"parentUuid", p}, {"sessionId", sid}}
	if content != nil {
		role := "user"
		if typ == "user" || typ == "assistant" {
			role = typ
		}
		e = append(e, spKV{"message", spObj{{"role", role}, {"content", content}}})
	}
	return append(e, extras...)
}

func spWriteTranscript(t *testing.T, path string, entries ...spObj) {
	t.Helper()
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = spDumps(e)
	}
	spWriteLines(t, path, lines...)
}

func spList(t *testing.T, dir string, limit, offset int, opts ...ListSessionsOption) []SDKSessionInfo {
	t.Helper()
	s, err := ListSessions(dir, limit, offset, opts...)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return s
}

func spListNoWT(t *testing.T, dir string) []SDKSessionInfo {
	t.Helper()
	return spList(t, dir, 0, 0, ListIncludeWorktrees(false))
}

func spMsgs(t *testing.T, sid, dir string, limit, offset int) []SessionMessage {
	t.Helper()
	m, err := GetSessionMessages(sid, dir, limit, offset)
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	return m
}

func spSubMsgs(t *testing.T, sid, agentID, dir string, limit, offset int) []SessionMessage {
	t.Helper()
	m, err := GetSubagentMessages(sid, agentID, dir, limit, offset)
	if err != nil {
		t.Fatalf("GetSubagentMessages: %v", err)
	}
	return m
}

func spSubagents(t *testing.T, sid, dir string) []string {
	t.Helper()
	ids, err := ListSubagents(sid, dir)
	if err != nil {
		t.Fatalf("ListSubagents: %v", err)
	}
	return ids
}

func spUUIDs(msgs []SessionMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.UUID
	}
	return out
}

func spAssertMessage(t *testing.T, raw json.RawMessage, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("message %s: %v", raw, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("message = %v, want %v", got, want)
	}
}

func spAssertNotFound(t *testing.T, info SDKSessionInfo, err error) {
	t.Helper()
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("GetSessionInfo = %+v, %v; want ErrSessionNotFound", info, err)
	}
}

func spEqualStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func TestSPHelpers(t *testing.T) {
	t.Run("validate_uuid_valid", func(t *testing.T) {
		for _, s := range []string{"550e8400-e29b-41d4-a716-446655440000", "550E8400-E29B-41D4-A716-446655440000"} {
			if !uuidRE.MatchString(s) {
				t.Errorf("%q should be valid", s)
			}
		}
	})
	t.Run("validate_uuid_invalid", func(t *testing.T) {
		for _, s := range []string{"not-a-uuid", "", "550e8400-e29b-41d4-a716"} {
			if uuidRE.MatchString(s) {
				t.Errorf("%q should be invalid", s)
			}
		}
	})
	t.Run("sanitize_path_basic", func(t *testing.T) {
		if got := sanitizePath("/Users/foo/my-project"); got != "-Users-foo-my-project" {
			t.Errorf("got %q", got)
		}
		if got := sanitizePath("plugin:name:server"); got != "plugin-name-server" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("sanitize_path_long", func(t *testing.T) {
		result := sanitizePath(strings.Repeat("/x", 150))
		if len(result) <= 200 {
			t.Errorf("len = %d, want > 200", len(result))
		}
		if !strings.HasPrefix(result, "-x-x") {
			t.Errorf("prefix: %q", result[:10])
		}
		if !strings.Contains(result[200:], "-") {
			t.Errorf("no hash separator after 200 chars: %q", result[200:])
		}
	})
	t.Run("simple_hash_deterministic", func(t *testing.T) {
		if a, b := simpleHash("hello"), simpleHash("hello"); a != b {
			t.Error("not deterministic")
		}
		if simpleHash("hello") == simpleHash("world") {
			t.Error("hello and world collide")
		}
	})
	t.Run("simple_hash_zero", func(t *testing.T) {
		if got := simpleHash(""); got != "0" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("extract_json_string_field_simple", func(t *testing.T) {
		text := `{"foo":"bar","baz":"qux"}`
		if v, ok := extractJSONStringField(text, "foo"); !ok || v != "bar" {
			t.Errorf("foo = %q, %v", v, ok)
		}
		if v, ok := extractJSONStringField(text, "baz"); !ok || v != "qux" {
			t.Errorf("baz = %q, %v", v, ok)
		}
		if v, ok := extractJSONStringField(text, "missing"); ok {
			t.Errorf("missing = %q, want not found", v)
		}
	})
	t.Run("extract_json_string_field_with_space", func(t *testing.T) {
		if v, ok := extractJSONStringField(`{"foo": "bar"}`, "foo"); !ok || v != "bar" {
			t.Errorf("got %q, %v", v, ok)
		}
	})
	t.Run("extract_json_string_field_escaped", func(t *testing.T) {
		if v, ok := extractJSONStringField(`{"foo":"bar\"baz"}`, "foo"); !ok || v != `bar"baz` {
			t.Errorf("got %q, %v", v, ok)
		}
	})
	t.Run("extract_last_json_string_field", func(t *testing.T) {
		text := "{\"summary\":\"first\"}\n{\"summary\":\"second\"}\n{\"summary\":\"third\"}"
		if v, ok := extractLastJSONStringField(text, "summary"); !ok || v != "third" {
			t.Errorf("got %q, %v", v, ok)
		}
	})
	t.Run("extract_first_prompt_simple", func(t *testing.T) {
		head := spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "Hello!"}}}}) + "\n"
		if got := extractFirstPromptFromHead(head); got != "Hello!" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("extract_first_prompt_skips_meta", func(t *testing.T) {
		head := spDumps(spObj{{"type", "user"}, {"isMeta", true}, {"message", spObj{{"content", "meta"}}}}) + "\n" +
			spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "real prompt"}}}}) + "\n"
		if got := extractFirstPromptFromHead(head); got != "real prompt" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("extract_first_prompt_skips_tool_result", func(t *testing.T) {
		head := spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", []any{spObj{{"type", "tool_result"}, {"content", "x"}}}}}}}) + "\n" +
			spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "actual prompt"}}}}) + "\n"
		if got := extractFirstPromptFromHead(head); got != "actual prompt" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("extract_first_prompt_content_blocks", func(t *testing.T) {
		head := spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", []any{spObj{{"type", "text"}, {"text", "block prompt"}}}}}}}) + "\n"
		if got := extractFirstPromptFromHead(head); got != "block prompt" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("extract_first_prompt_truncates", func(t *testing.T) {
		head := spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", strings.Repeat("x", 300)}}}}) + "\n"
		got := extractFirstPromptFromHead(head)
		if n := len([]rune(got)); n > 201 {
			t.Errorf("len = %d, want <= 201", n)
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("no ellipsis: %q", got)
		}
	})
	t.Run("extract_first_prompt_command_fallback", func(t *testing.T) {
		head := spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "<command-name>/help</command-name>stuff"}}}}) + "\n"
		if got := extractFirstPromptFromHead(head); got != "/help" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("extract_first_prompt_empty", func(t *testing.T) {
		if got := extractFirstPromptFromHead(""); got != "" {
			t.Errorf("got %q", got)
		}
		if got := extractFirstPromptFromHead("{\"type\":\"assistant\"}\n"); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ListSessions
// ---------------------------------------------------------------------------

func TestSPListSessions(t *testing.T) {
	t.Run("empty_projects_dir", func(t *testing.T) {
		spConfigDir(t)
		if s := spList(t, "", 0, 0); len(s) != 0 {
			t.Errorf("got %d sessions", len(s))
		}
	})
	t.Run("no_config_dir", func(t *testing.T) {
		spMissingConfigDir(t)
		if s := spList(t, "", 0, 0); len(s) != 0 {
			t.Errorf("got %d sessions", len(s))
		}
	})
	t.Run("single_session", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "my-project")
		sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "What is 2+2?", gitBranch: "main", cwd: projectPath})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		s := sessions[0]
		if s.SessionID != sid || s.FirstPrompt != "What is 2+2?" || s.Summary != "What is 2+2?" ||
			s.GitBranch != "main" || s.Cwd != projectPath || s.CustomTitle != "" {
			t.Errorf("unexpected info %+v", s)
		}
		if s.FileSize <= 0 || s.LastModified <= 0 {
			t.Errorf("size/mtime not set: %+v", s)
		}
	})
	t.Run("custom_title_wins_summary", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "original question", summary: "auto summary", customTitle: "My Custom Title"})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		s := sessions[0]
		if s.Summary != "My Custom Title" || s.CustomTitle != "My Custom Title" || s.FirstPrompt != "original question" {
			t.Errorf("unexpected info %+v", s)
		}
	})
	t.Run("summary_wins_first_prompt", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "question", summary: "better summary"})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		if sessions[0].Summary != "better summary" || sessions[0].CustomTitle != "" {
			t.Errorf("unexpected info %+v", sessions[0])
		}
	})
	t.Run("multiple_sessions_sorted_by_mtime", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sidOld, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "old", mtime: 1000})
		sidNew, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "new", mtime: 3000})
		sidMid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "mid", mtime: 2000})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 3 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		var ids []string
		var mtimes []int64
		for _, s := range sessions {
			ids = append(ids, s.SessionID)
			mtimes = append(mtimes, s.LastModified)
		}
		spEqualStrings(t, ids, []string{sidNew, sidMid, sidOld})
		if !reflect.DeepEqual(mtimes, []int64{3_000_000, 2_000_000, 1_000_000}) {
			t.Errorf("mtimes = %v", mtimes)
		}
	})
	t.Run("limit", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		for i := 0; i < 5; i++ {
			spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: fmt.Sprintf("prompt %d", i), mtime: 1000 + int64(i)})
		}
		sessions := spList(t, projectPath, 2, 0, ListIncludeWorktrees(false))
		if len(sessions) != 2 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		if sessions[0].LastModified < sessions[1].LastModified {
			t.Errorf("not newest first: %d < %d", sessions[0].LastModified, sessions[1].LastModified)
		}
	})
	t.Run("offset_pagination", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		for i := 0; i < 5; i++ {
			spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: fmt.Sprintf("prompt %d", i), mtime: 1000 + int64(i)})
		}
		page1 := spList(t, projectPath, 2, 0, ListIncludeWorktrees(false))
		if len(page1) != 2 {
			t.Fatalf("page1 has %d", len(page1))
		}
		page2 := spList(t, projectPath, 2, 2, ListIncludeWorktrees(false))
		if len(page2) != 2 {
			t.Fatalf("page2 has %d", len(page2))
		}
		ids := map[string]bool{}
		for _, s := range page1 {
			ids[s.SessionID] = true
		}
		for _, s := range page2 {
			if ids[s.SessionID] {
				t.Errorf("session %s on both pages", s.SessionID)
			}
		}
		if page1[0].LastModified <= page2[0].LastModified {
			t.Errorf("page1 not newer than page2")
		}
		if empty := spList(t, projectPath, 0, 100, ListIncludeWorktrees(false)); len(empty) != 0 {
			t.Errorf("offset beyond end returned %d", len(empty))
		}
	})
	t.Run("filters_sidechain_sessions", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "normal"})
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "sidechain", isSidechain: true})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].FirstPrompt != "normal" {
			t.Errorf("got %+v", sessions)
		}
	})
	t.Run("filters_empty_sessions", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "ignored meta", isMetaOnly: true})
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "real content"})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].FirstPrompt != "real content" {
			t.Errorf("got %+v", sessions)
		}
	})
	t.Run("filters_non_uuid_filenames", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spWriteFile(t, filepath.Join(projectDir, "not-a-uuid.jsonl"),
			spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "x"}}}})+"\n")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "valid session"})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].FirstPrompt != "valid session" {
			t.Errorf("got %+v", sessions)
		}
	})
	t.Run("ignores_non_jsonl_files", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spWriteFile(t, filepath.Join(projectDir, "README.md"), "not a session")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "session"})

		if sessions := spListNoWT(t, projectPath); len(sessions) != 1 {
			t.Errorf("got %d sessions", len(sessions))
		}
	})
	t.Run("list_all_sessions", func(t *testing.T) {
		cfg, _ := spConfigDir(t)
		proj1 := spMakeProjectDir(t, cfg, "/some/path/one")
		proj2 := spMakeProjectDir(t, cfg, "/some/path/two")
		spMakeSessionFile(t, proj1, spSessionOpts{firstPrompt: "from proj1", mtime: 1000})
		spMakeSessionFile(t, proj2, spSessionOpts{firstPrompt: "from proj2", mtime: 2000})

		sessions := spList(t, "", 0, 0)
		if len(sessions) != 2 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		if sessions[0].FirstPrompt != "from proj2" || sessions[1].FirstPrompt != "from proj1" {
			t.Errorf("order: %q, %q", sessions[0].FirstPrompt, sessions[1].FirstPrompt)
		}
	})
	t.Run("list_all_sessions_dedupes", func(t *testing.T) {
		cfg, _ := spConfigDir(t)
		proj1 := spMakeProjectDir(t, cfg, "/path/one")
		proj2 := spMakeProjectDir(t, cfg, "/path/two")
		shared := spUUID(t)
		spMakeSessionFile(t, proj1, spSessionOpts{sessionID: shared, firstPrompt: "older", mtime: 1000})
		spMakeSessionFile(t, proj2, spSessionOpts{sessionID: shared, firstPrompt: "newer", mtime: 2000})

		sessions := spList(t, "", 0, 0)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		if sessions[0].FirstPrompt != "newer" || sessions[0].LastModified != 2_000_000 {
			t.Errorf("got %+v", sessions[0])
		}
	})
	t.Run("nonexistent_project_dir", func(t *testing.T) {
		_, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "never-used"))
		if sessions := spListNoWT(t, projectPath); len(sessions) != 0 {
			t.Errorf("got %d sessions", len(sessions))
		}
	})
	t.Run("empty_file_filtered", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spWriteFile(t, filepath.Join(projectDir, spUUID(t)+".jsonl"), "")
		if sessions := spListNoWT(t, projectPath); len(sessions) != 0 {
			t.Errorf("got %d sessions", len(sessions))
		}
	})
	t.Run("include_worktrees_disabled", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "main-proj"))
		canonical := spRealpath(t, projectPath)
		mainDir := spMakeProjectDir(t, cfg, canonical)
		spMakeSessionFile(t, mainDir, spSessionOpts{firstPrompt: "main session"})
		otherDir := spMakeProjectDir(t, cfg, canonical+"-worktree")
		spMakeSessionFile(t, otherDir, spSessionOpts{firstPrompt: "worktree session"})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].FirstPrompt != "main session" {
			t.Errorf("got %+v", sessions)
		}
	})
	t.Run("limit_zero_returns_all", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		for i := 0; i < 3; i++ {
			spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: fmt.Sprintf("p%d", i)})
		}
		if sessions := spList(t, projectPath, 0, 0, ListIncludeWorktrees(false)); len(sessions) != 3 {
			t.Errorf("got %d sessions", len(sessions))
		}
	})
	t.Run("cwd_from_head_fallback_to_project_path", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		canonical := spRealpath(t, projectPath)
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "no cwd field"})

		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].Cwd != canonical {
			t.Errorf("got %+v, want cwd %q", sessions, canonical)
		}
	})
	t.Run("git_branch_from_tail_preferred", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"),
			spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "hello"}}}, {"gitBranch", "old-branch"}}),
			spDumps(spObj{{"type", "summary"}, {"gitBranch", "new-branch"}}),
		)
		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].GitBranch != "new-branch" {
			t.Errorf("got %+v", sessions)
		}
	})
}

func TestSPSDKSessionInfoType(t *testing.T) {
	t.Run("creation_required_fields", func(t *testing.T) {
		info := SDKSessionInfo{SessionID: "abc", Summary: "test", LastModified: 1000, FileSize: 42}
		if info.SessionID != "abc" || info.Summary != "test" || info.LastModified != 1000 || info.FileSize != 42 {
			t.Errorf("got %+v", info)
		}
		if info.CustomTitle != "" || info.FirstPrompt != "" || info.GitBranch != "" || info.Cwd != "" {
			t.Errorf("optional fields not zero: %+v", info)
		}
	})
	t.Run("creation_all_fields", func(t *testing.T) {
		info := SDKSessionInfo{SessionID: "abc", Summary: "test", LastModified: 1000, FileSize: 42,
			CustomTitle: "title", FirstPrompt: "prompt", GitBranch: "main", Cwd: "/foo"}
		if info.CustomTitle != "title" || info.FirstPrompt != "prompt" || info.GitBranch != "main" || info.Cwd != "/foo" {
			t.Errorf("got %+v", info)
		}
	})
}

// ---------------------------------------------------------------------------
// GetSessionMessages
// ---------------------------------------------------------------------------

func TestSPGetSessionMessages(t *testing.T) {
	t.Run("invalid_session_id", func(t *testing.T) {
		spConfigDir(t)
		for _, id := range []string{"not-a-uuid", ""} {
			if m := spMsgs(t, id, "", 0, 0); len(m) != 0 {
				t.Errorf("%q: got %d messages", id, len(m))
			}
		}
	})
	t.Run("nonexistent_session", func(t *testing.T) {
		spConfigDir(t)
		if m := spMsgs(t, spUUID(t), "", 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("no_config_dir", func(t *testing.T) {
		spMissingConfigDir(t)
		if m := spMsgs(t, spUUID(t), "", 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("simple_chain", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, a1, u2, a2 := spUUID(t), spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", u1, "", sid, "hello"),
			spEntry("assistant", a1, u1, sid, "hi!"),
			spEntry("user", u2, a1, sid, "thanks"),
			spEntry("assistant", a2, u2, sid, "welcome"),
		)
		msgs := spMsgs(t, sid, projectPath, 0, 0)
		if len(msgs) != 4 {
			t.Fatalf("got %d messages", len(msgs))
		}
		if msgs[0].Type != "user" || msgs[0].UUID != u1 || msgs[0].SessionID != sid || msgs[0].ParentToolUseID != "" {
			t.Errorf("msgs[0] = %+v", msgs[0])
		}
		spAssertMessage(t, msgs[0].Message, map[string]any{"role": "user", "content": "hello"})
		if msgs[1].Type != "assistant" || msgs[1].UUID != a1 {
			t.Errorf("msgs[1] = %+v", msgs[1])
		}
		spAssertMessage(t, msgs[1].Message, map[string]any{"role": "assistant", "content": "hi!"})
		if msgs[2].Type != "user" || msgs[2].UUID != u2 {
			t.Errorf("msgs[2] = %+v", msgs[2])
		}
		if msgs[3].Type != "assistant" || msgs[3].UUID != a2 {
			t.Errorf("msgs[3] = %+v", msgs[3])
		}
	})
	t.Run("filters_meta_messages", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, meta, a1 := spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", u1, "", sid, "hello"),
			spEntry("user", meta, u1, sid, "meta", spKV{"isMeta", true}),
			spEntry("assistant", a1, meta, sid, "hi"),
		)
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 0)), []string{u1, a1})
	})
	t.Run("filters_non_user_assistant_from_chain", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, prog, a1 := spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", u1, "", sid, "hello"),
			spEntry("progress", prog, u1, sid, nil),
			spEntry("assistant", a1, prog, sid, "hi"),
		)
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 0)), []string{u1, a1})
	})
	t.Run("keeps_compact_summary", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, a1 := spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", u1, "", sid, "compact summary", spKV{"isCompactSummary", true}),
			spEntry("assistant", a1, u1, sid, "hi"),
		)
		msgs := spMsgs(t, sid, projectPath, 0, 0)
		if len(msgs) != 2 || msgs[0].UUID != u1 {
			t.Errorf("got %v", spUUIDs(msgs))
		}
	})
	t.Run("limit_and_offset", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		uuids := make([]string, 6)
		entries := make([]spObj, 6)
		for i := range uuids {
			uuids[i] = spUUID(t)
			parent := ""
			if i > 0 {
				parent = uuids[i-1]
			}
			typ := "user"
			if i%2 == 1 {
				typ = "assistant"
			}
			entries[i] = spEntry(typ, uuids[i], parent, sid, fmt.Sprintf("m%d", i))
		}
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"), entries...)

		if all := spMsgs(t, sid, projectPath, 0, 0); len(all) != 6 {
			t.Errorf("all: %d", len(all))
		}
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 2, 0)), uuids[0:2])
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 2, 2)), uuids[2:4])
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 4)), uuids[4:6])
		if p := spMsgs(t, sid, projectPath, 0, 0); len(p) != 6 {
			t.Errorf("limit 0: %d", len(p))
		}
		if p := spMsgs(t, sid, projectPath, 0, 100); len(p) != 0 {
			t.Errorf("offset beyond end: %d", len(p))
		}
	})
	t.Run("picks_main_chain_over_sidechain", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		root, mainLeaf, sideLeaf := spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", root, "", sid, "root"),
			spEntry("assistant", mainLeaf, root, sid, "main"),
			spEntry("assistant", sideLeaf, root, sid, "side", spKV{"isSidechain", true}),
		)
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 0)), []string{root, mainLeaf})
	})
	t.Run("picks_latest_leaf_by_file_position", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		root, oldLeaf, newLeaf := spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", root, "", sid, "root"),
			spEntry("assistant", oldLeaf, root, sid, "old"),
			spEntry("assistant", newLeaf, root, sid, "new"),
		)
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 0)), []string{root, newLeaf})
	})
	t.Run("terminal_non_message_walked_back", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, a1, prog := spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", u1, "", sid, "hi"),
			spEntry("assistant", a1, u1, sid, "hello"),
			spEntry("progress", prog, a1, sid, nil),
		)
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 0)), []string{u1, a1})
	})
	t.Run("corrupt_lines_skipped", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, a1 := spUUID(t), spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"),
			spDumps(spEntry("user", u1, "", sid, "hi")),
			"not valid json {{{",
			"",
			spDumps(spEntry("assistant", a1, u1, sid, "hello")),
		)
		if m := spMsgs(t, sid, projectPath, 0, 0); len(m) != 2 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("search_all_projects_when_no_dir", func(t *testing.T) {
		cfg, _ := spConfigDir(t)
		spMakeProjectDir(t, cfg, "/path/one")
		proj2 := spMakeProjectDir(t, cfg, "/path/two")
		sid := spUUID(t)
		u1, a1 := spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(proj2, sid+".jsonl"),
			spEntry("user", u1, "", sid, "hi"),
			spEntry("assistant", a1, u1, sid, "hello"),
		)
		msgs := spMsgs(t, sid, "", 0, 0)
		if len(msgs) != 2 || msgs[0].UUID != u1 {
			t.Errorf("got %v", spUUIDs(msgs))
		}
	})
	t.Run("cycle_detection", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, a1 := spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			spEntry("user", u1, a1, sid, "hi"),
			spEntry("assistant", a1, u1, sid, "hello"),
		)
		if m := spMsgs(t, sid, projectPath, 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("empty_transcript_file", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteFile(t, filepath.Join(projectDir, sid+".jsonl"), "")
		if m := spMsgs(t, sid, projectPath, 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("ignores_non_transcript_types", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		u1, a1 := spUUID(t), spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"),
			spDumps(spEntry("user", u1, "", sid, "hi")),
			spDumps(spObj{{"type", "summary"}, {"summary", "A nice chat"}}),
			spDumps(spEntry("assistant", a1, u1, sid, "hello")),
		)
		if m := spMsgs(t, sid, projectPath, 0, 0); len(m) != 2 {
			t.Errorf("got %d messages", len(m))
		}
	})
}

func spTE(t *testing.T, s string) transcriptEntry {
	t.Helper()
	var e transcriptEntry
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSPBuildConversationChain(t *testing.T) {
	t.Run("empty_input", func(t *testing.T) {
		if got := buildConversationChain(nil); len(got) != 0 {
			t.Errorf("got %d entries", len(got))
		}
	})
	t.Run("single_entry", func(t *testing.T) {
		entry := spTE(t, `{"type": "user", "uuid": "a", "parentUuid": null}`)
		got := buildConversationChain([]transcriptEntry{entry})
		if !reflect.DeepEqual(got, []transcriptEntry{entry}) {
			t.Errorf("got %v", got)
		}
	})
	t.Run("linear_chain", func(t *testing.T) {
		entries := []transcriptEntry{
			spTE(t, `{"type": "user", "uuid": "a", "parentUuid": null}`),
			spTE(t, `{"type": "assistant", "uuid": "b", "parentUuid": "a"}`),
			spTE(t, `{"type": "user", "uuid": "c", "parentUuid": "b"}`),
		}
		var ids []string
		for _, e := range buildConversationChain(entries) {
			ids = append(ids, e.str("uuid"))
		}
		spEqualStrings(t, ids, []string{"a", "b", "c"})
	})
	t.Run("only_progress_entries_returns_empty", func(t *testing.T) {
		entries := []transcriptEntry{
			spTE(t, `{"type": "progress", "uuid": "a", "parentUuid": null}`),
			spTE(t, `{"type": "progress", "uuid": "b", "parentUuid": "a"}`),
		}
		if got := buildConversationChain(entries); len(got) != 0 {
			t.Errorf("got %d entries", len(got))
		}
	})
}

func TestSPSessionMessageType(t *testing.T) {
	t.Run("creation", func(t *testing.T) {
		msg := SessionMessage{Type: "user", UUID: "abc", SessionID: "sess", Message: json.RawMessage(`{"role": "user", "content": "hi"}`)}
		if msg.Type != "user" || msg.UUID != "abc" || msg.SessionID != "sess" {
			t.Errorf("got %+v", msg)
		}
		spAssertMessage(t, msg.Message, map[string]any{"role": "user", "content": "hi"})
		if msg.ParentToolUseID != "" || msg.ParentAgentID != "" {
			t.Errorf("parent ids not zero: %+v", msg)
		}
	})
	t.Run("parent_ids", func(t *testing.T) {
		msg := SessionMessage{Type: "assistant", UUID: "abc", SessionID: "sess", ParentToolUseID: "toolu_1", ParentAgentID: "agent-1"}
		if msg.ParentToolUseID != "toolu_1" || msg.ParentAgentID != "agent-1" {
			t.Errorf("got %+v", msg)
		}
	})
}

// ---------------------------------------------------------------------------
// Tag extraction
// ---------------------------------------------------------------------------

func spTagLine(tag, sid string) string {
	return spDumpsCompact(spObj{{"type", "tag"}, {"tag", tag}, {"sessionId", sid}})
}

func spUserLine(content string) string {
	return spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", content}}}})
}

func TestSPTagExtraction(t *testing.T) {
	listOne := func(t *testing.T, projectPath string) SDKSessionInfo {
		t.Helper()
		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		return sessions[0]
	}
	t.Run("tag_extracted_from_tail", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"), spUserLine("hello"), spTagLine("my-tag", sid))
		if got := listOne(t, projectPath).Tag; got != "my-tag" {
			t.Errorf("tag = %q", got)
		}
	})
	t.Run("tag_last_wins", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"), spUserLine("hello"),
			spTagLine("first-tag", sid), spTagLine("second-tag", sid))
		if got := listOne(t, projectPath).Tag; got != "second-tag" {
			t.Errorf("tag = %q", got)
		}
	})
	t.Run("tag_empty_string_is_none", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"), spUserLine("hello"),
			spTagLine("old-tag", sid), spTagLine("", sid))
		if got := listOne(t, projectPath).Tag; got != "" {
			t.Errorf("tag = %q", got)
		}
	})
	t.Run("tag_absent", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "hello"})
		if got := listOne(t, projectPath).Tag; got != "" {
			t.Errorf("tag = %q", got)
		}
	})
	t.Run("tag_ignores_tool_use_inputs", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		toolUse := spDumps(spObj{{"type", "assistant"}, {"message", spObj{{"content", []any{spObj{
			{"type", "tool_use"}, {"name", "mcp__docker__build"},
			{"input", spObj{{"tag", "myapp:v2"}, {"context", "."}}},
		}}}}}})
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"), spUserLine("tag this v1.0"), spTagLine("real-tag", sid), toolUse)
		if got := listOne(t, projectPath).Tag; got != "real-tag" {
			t.Errorf("tag = %q", got)
		}
	})
	t.Run("tag_none_when_only_tool_use_tag", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		toolUse := spDumps(spObj{{"type", "assistant"}, {"message", spObj{{"content", []any{spObj{
			{"type", "tool_use"}, {"input", spObj{{"tag", "prod"}}},
		}}}}}})
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"), spUserLine("build docker"), toolUse)
		if got := listOne(t, projectPath).Tag; got != "" {
			t.Errorf("tag = %q", got)
		}
	})
	t.Run("parse_session_info_from_lite_helper", func(t *testing.T) {
		sid := spUUID(t)
		path := filepath.Join(t.TempDir(), sid+".jsonl")
		spWriteLines(t, path,
			spDumps(spObj{{"type", "user"}, {"message", spObj{{"content", "test prompt"}}}, {"cwd", "/workspace"}}),
			spTagLine("experiment", sid),
		)
		lite, ok := readSessionLite(path)
		if !ok {
			t.Fatal("readSessionLite failed")
		}
		info, ok := parseSessionInfoFromLite(sid, lite, "/fallback")
		if !ok {
			t.Fatal("parseSessionInfoFromLite returned no info")
		}
		if info.SessionID != sid || info.Summary != "test prompt" || info.Tag != "experiment" || info.Cwd != "/workspace" {
			t.Errorf("got %+v", info)
		}
	})
}

// ---------------------------------------------------------------------------
// CreatedAt extraction
// ---------------------------------------------------------------------------

func TestSPCreatedAtExtraction(t *testing.T) {
	tsLine := func(typ, content, ts string) string {
		return spDumps(spObj{{"type", typ}, {"message", spObj{{"content", content}}}, {"timestamp", ts}})
	}
	t.Run("created_at_from_iso_timestamp", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"),
			tsLine("user", "hello", "2026-01-15T10:30:00.000Z"),
			tsLine("assistant", "hi", "2026-01-15T10:35:00.000Z"),
		)
		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].CreatedAt != 1768473000000 {
			t.Errorf("got %+v", sessions)
		}
	})
	t.Run("created_at_leq_last_modified", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		path := filepath.Join(projectDir, sid+".jsonl")
		spWriteLines(t, path, tsLine("user", "hello", "2026-01-01T00:00:00.000Z"))
		spSetMtime(t, path, 1769904000)
		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 {
			t.Fatalf("got %d sessions", len(sessions))
		}
		if sessions[0].CreatedAt == 0 || sessions[0].CreatedAt > sessions[0].LastModified {
			t.Errorf("created_at %d, last_modified %d", sessions[0].CreatedAt, sessions[0].LastModified)
		}
	})
	t.Run("created_at_when_first_line_lacks_timestamp", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"),
			spDumps(spObj{{"type", "permission-mode"}, {"permissionMode", "acceptEdits"}}),
			tsLine("user", "hello", "2026-01-15T10:30:00.000Z"),
		)
		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].CreatedAt != 1768473000000 {
			t.Errorf("got %+v", sessions)
		}
	})
	t.Run("created_at_none_when_missing", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "no timestamp"})
		sessions := spListNoWT(t, projectPath)
		if len(sessions) != 1 || sessions[0].CreatedAt != 0 {
			t.Errorf("got %+v", sessions)
		}
	})
	parseFile := func(t *testing.T, ts string) SDKSessionInfo {
		t.Helper()
		sid := spUUID(t)
		path := filepath.Join(t.TempDir(), sid+".jsonl")
		spWriteLines(t, path, tsLine("user", "hello", ts))
		lite, ok := readSessionLite(path)
		if !ok {
			t.Fatal("readSessionLite failed")
		}
		info, ok := parseSessionInfoFromLite(sid, lite, "")
		if !ok {
			t.Fatal("parseSessionInfoFromLite returned no info")
		}
		return info
	}
	t.Run("created_at_none_on_invalid_format", func(t *testing.T) {
		if got := parseFile(t, "not-a-valid-iso-date").CreatedAt; got != 0 {
			t.Errorf("created_at = %d", got)
		}
	})
	t.Run("created_at_without_z_suffix", func(t *testing.T) {
		if got := parseFile(t, "2026-01-15T10:30:00+00:00").CreatedAt; got != 1768473000000 {
			t.Errorf("created_at = %d", got)
		}
	})
	t.Run("sdksessioninfo_created_at_default", func(t *testing.T) {
		info := SDKSessionInfo{SessionID: "abc", Summary: "test", LastModified: 1000, FileSize: 42}
		if info.CreatedAt != 0 {
			t.Errorf("created_at = %d", info.CreatedAt)
		}
	})
}

// ---------------------------------------------------------------------------
// GetSessionInfo
// ---------------------------------------------------------------------------

func TestSPGetSessionInfo(t *testing.T) {
	t.Run("invalid_session_id", func(t *testing.T) {
		spConfigDir(t)
		for _, id := range []string{"not-a-uuid", ""} {
			info, err := GetSessionInfo(id, "")
			spAssertNotFound(t, info, err)
		}
	})
	t.Run("nonexistent_session", func(t *testing.T) {
		spConfigDir(t)
		info, err := GetSessionInfo(spUUID(t), "")
		spAssertNotFound(t, info, err)
	})
	t.Run("no_config_dir", func(t *testing.T) {
		spMissingConfigDir(t)
		info, err := GetSessionInfo(spUUID(t), "")
		spAssertNotFound(t, info, err)
	})
	t.Run("found_with_directory", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "hello", gitBranch: "main"})
		info, err := GetSessionInfo(sid, projectPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.SessionID != sid || info.Summary != "hello" || info.GitBranch != "main" {
			t.Errorf("got %+v", info)
		}
	})
	t.Run("found_without_directory", func(t *testing.T) {
		cfg, _ := spConfigDir(t)
		projectDir := spMakeProjectDir(t, cfg, "/some/project")
		sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "search all"})
		info, err := GetSessionInfo(sid, "")
		if err != nil {
			t.Fatal(err)
		}
		if info.SessionID != sid || info.Summary != "search all" {
			t.Errorf("got %+v", info)
		}
	})
	t.Run("returns_none_for_sidechain", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{firstPrompt: "sidechain", isSidechain: true})
		info, err := GetSessionInfo(sid, projectPath)
		spAssertNotFound(t, info, err)
	})
	t.Run("directory_not_containing_session", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		_, dirA := spProject(t, cfg, tmp, "proj-a")
		projectB, _ := spProject(t, cfg, tmp, "proj-b")
		sid, _ := spMakeSessionFile(t, dirA, spSessionOpts{firstPrompt: "in A only"})
		info, err := GetSessionInfo(sid, projectB)
		spAssertNotFound(t, info, err)
		if _, err := GetSessionInfo(sid, ""); err != nil {
			t.Errorf("search all: %v", err)
		}
	})
	t.Run("includes_tag", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		spWriteLines(t, filepath.Join(projectDir, sid+".jsonl"), spUserLine("hello"), spTagLine("urgent", sid))
		info, err := GetSessionInfo(sid, projectPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Tag != "urgent" {
			t.Errorf("tag = %q", info.Tag)
		}
	})
	t.Run("sdksessioninfo_new_fields_defaults", func(t *testing.T) {
		info := SDKSessionInfo{SessionID: "abc", Summary: "test", LastModified: 1000, FileSize: 42}
		if info.Tag != "" {
			t.Errorf("tag = %q", info.Tag)
		}
	})
}

// ---------------------------------------------------------------------------
// ListSubagents / GetSubagentMessages
// ---------------------------------------------------------------------------

// spMakeSessionWithSubagents mirrors _make_session_with_subagents.
func spMakeSessionWithSubagents(t *testing.T, cfg, projectPath string, agentIDs ...string) (string, string) {
	t.Helper()
	projectDir := spMakeProjectDir(t, cfg, spRealpath(t, projectPath))
	sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{})
	subDir := spMkdir(t, filepath.Join(projectDir, sid, "subagents"))
	for _, id := range agentIDs {
		spWriteFile(t, filepath.Join(subDir, "agent-"+id+".jsonl"),
			spDumps(spObj{{"type", "user"}, {"uuid", "u"}, {"parentUuid", nil}})+"\n")
	}
	return sid, subDir
}

func TestSPListSubagents(t *testing.T) {
	t.Run("invalid_session_id", func(t *testing.T) {
		spConfigDir(t)
		for _, id := range []string{"not-a-uuid", ""} {
			if ids := spSubagents(t, id, ""); len(ids) != 0 {
				t.Errorf("%q: got %q", id, ids)
			}
		}
	})
	t.Run("nonexistent_session", func(t *testing.T) {
		spConfigDir(t)
		if ids := spSubagents(t, spUUID(t), ""); len(ids) != 0 {
			t.Errorf("got %q", ids)
		}
	})
	t.Run("session_exists_no_subagents_dir", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{})
		if ids := spSubagents(t, sid, projectPath); len(ids) != 0 {
			t.Errorf("got %q", ids)
		}
	})
	t.Run("empty_subagents_dir", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, _ := spMakeSessionWithSubagents(t, cfg, projectPath)
		if ids := spSubagents(t, sid, projectPath); len(ids) != 0 {
			t.Errorf("got %q", ids)
		}
	})
	t.Run("happy_path", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, _ := spMakeSessionWithSubagents(t, cfg, projectPath, "abc123", "def456")
		ids := spSubagents(t, sid, projectPath)
		sort.Strings(ids)
		spEqualStrings(t, ids, []string{"abc123", "def456"})
	})
	t.Run("ignores_non_agent_files", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath, "keep")
		spWriteFile(t, filepath.Join(subDir, "agent-keep.meta.json"), "{}")
		spWriteFile(t, filepath.Join(subDir, "other.jsonl"), "{}\n")
		spWriteFile(t, filepath.Join(subDir, "agent-noext"), "{}")
		spEqualStrings(t, spSubagents(t, sid, projectPath), []string{"keep"})
	})
	t.Run("recurses_into_subdirectories", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath, "top")
		nested := spMkdir(t, filepath.Join(subDir, "workflows", "run-1"))
		spWriteFile(t, filepath.Join(nested, "agent-nested.jsonl"), "{}\n")
		ids := spSubagents(t, sid, projectPath)
		sort.Strings(ids)
		spEqualStrings(t, ids, []string{"nested", "top"})
	})
	t.Run("searches_all_projects_without_directory", func(t *testing.T) {
		cfg, _ := spConfigDir(t)
		projectDir := spMakeProjectDir(t, cfg, "/some/project")
		sid, _ := spMakeSessionFile(t, projectDir, spSessionOpts{})
		subDir := spMkdir(t, filepath.Join(projectDir, sid, "subagents"))
		spWriteFile(t, filepath.Join(subDir, "agent-x.jsonl"), "{}\n")
		spEqualStrings(t, spSubagents(t, sid, ""), []string{"x"})
	})
}

// spWriteAgent mirrors TestGetSubagentMessages._write_agent. meta is nil for
// no sidecar, a string for raw content, or an object to encode.
func spWriteAgent(t *testing.T, subDir, agentID, sid string, meta any) {
	t.Helper()
	u1, a1 := spUUID(t), spUUID(t)
	spWriteTranscript(t, filepath.Join(subDir, "agent-"+agentID+".jsonl"),
		spEntry("user", u1, "", sid, "hi"),
		spEntry("assistant", a1, u1, sid, "hello"),
	)
	if meta == nil {
		return
	}
	content, ok := meta.(string)
	if !ok {
		content = spDumps(meta)
	}
	spWriteFile(t, filepath.Join(subDir, "agent-"+agentID+".meta.json"), content)
}

func spAssertParentIDs(t *testing.T, msgs []SessionMessage, toolUseID, parentAgentID string) {
	t.Helper()
	for i, m := range msgs {
		if m.ParentToolUseID != toolUseID || m.ParentAgentID != parentAgentID {
			t.Errorf("msgs[%d] parents = (%q, %q), want (%q, %q)", i, m.ParentToolUseID, m.ParentAgentID, toolUseID, parentAgentID)
		}
	}
}

func TestSPGetSubagentMessages(t *testing.T) {
	t.Run("invalid_session_id", func(t *testing.T) {
		spConfigDir(t)
		for _, id := range []string{"not-a-uuid", ""} {
			if m := spSubMsgs(t, id, "abc", "", 0, 0); len(m) != 0 {
				t.Errorf("%q: got %d messages", id, len(m))
			}
		}
	})
	t.Run("empty_agent_id", func(t *testing.T) {
		spConfigDir(t)
		if m := spSubMsgs(t, spUUID(t), "", "", 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("nonexistent_session", func(t *testing.T) {
		spConfigDir(t)
		if m := spSubMsgs(t, spUUID(t), "abc", "", 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("nonexistent_agent", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, _ := spMakeSessionWithSubagents(t, cfg, projectPath, "other")
		if m := spSubMsgs(t, sid, "missing", projectPath, 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
	t.Run("simple_chain", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		u1, a1, u2, a2 := spUUID(t), spUUID(t), spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(subDir, "agent-abc.jsonl"),
			spEntry("user", u1, "", sid, "task"),
			spEntry("assistant", a1, u1, sid, "working"),
			spEntry("user", u2, a1, sid, "continue"),
			spEntry("assistant", a2, u2, sid, "done"),
		)
		msgs := spSubMsgs(t, sid, "abc", projectPath, 0, 0)
		if len(msgs) != 4 {
			t.Fatalf("got %d messages", len(msgs))
		}
		spEqualStrings(t, spUUIDs(msgs), []string{u1, a1, u2, a2})
		if msgs[0].Type != "user" || msgs[0].SessionID != sid || msgs[0].ParentToolUseID != "" {
			t.Errorf("msgs[0] = %+v", msgs[0])
		}
		spAssertMessage(t, msgs[0].Message, map[string]any{"role": "user", "content": "task"})
		if msgs[3].Type != "assistant" {
			t.Errorf("msgs[3].Type = %q", msgs[3].Type)
		}
	})
	t.Run("finds_agent_in_nested_subdirectory", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		nested := spMkdir(t, filepath.Join(subDir, "workflows", "run-1"))
		u1, a1 := spUUID(t), spUUID(t)
		spWriteTranscript(t, filepath.Join(nested, "agent-deep.jsonl"),
			spEntry("user", u1, "", sid, "hi"),
			spEntry("assistant", a1, u1, sid, "hello"),
		)
		spEqualStrings(t, spUUIDs(spSubMsgs(t, sid, "deep", projectPath, 0, 0)), []string{u1, a1})
	})
	t.Run("parent_ids_recovered_from_meta_sidecar", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		spWriteAgent(t, subDir, "abc", sid, spObj{
			{"agentType", "general-purpose"}, {"toolUseId", "toolu_01ABC"},
			{"parentAgentId", "a-parent"}, {"spawnDepth", 2},
		})
		msgs := spSubMsgs(t, sid, "abc", projectPath, 0, 0)
		if len(msgs) != 2 {
			t.Fatalf("got %d messages", len(msgs))
		}
		spAssertParentIDs(t, msgs, "toolu_01ABC", "a-parent")
	})
	t.Run("parent_ids_from_nested_meta_sidecar", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		nested := spMkdir(t, filepath.Join(subDir, "workflows", "run-1"))
		spWriteAgent(t, nested, "deep", sid, spObj{{"toolUseId", "toolu_nested"}})
		msgs := spSubMsgs(t, sid, "deep", projectPath, 0, 0)
		if len(msgs) != 2 {
			t.Fatalf("got %d messages", len(msgs))
		}
		spAssertParentIDs(t, msgs, "toolu_nested", "")
	})
	for _, tc := range []struct {
		name string
		meta any
	}{
		{"no_sidecar", nil},
		{"unreadable_sidecar", "not json {"},
		{"sidecar_without_ids", spObj{{"agentType", "general-purpose"}}},
		{"wrong_types", spObj{{"toolUseId", 42}, {"parentAgentId", []any{"x"}}}},
	} {
		t.Run("parent_ids_none_when_sidecar_missing_or_unusable/"+tc.name, func(t *testing.T) {
			cfg, tmp := spConfigDir(t)
			projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
			sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
			spWriteAgent(t, subDir, "x", sid, tc.meta)
			msgs := spSubMsgs(t, sid, "x", projectPath, 0, 0)
			if len(msgs) != 2 {
				t.Fatalf("got %d messages", len(msgs))
			}
			spAssertParentIDs(t, msgs, "", "")
		})
	}
	t.Run("unreadable_sidecar_degrades_to_none", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		spWriteAgent(t, subDir, "x", sid, nil)
		spMkdir(t, filepath.Join(subDir, "agent-x.meta.json"))
		msgs := spSubMsgs(t, sid, "x", projectPath, 0, 0)
		if len(msgs) != 2 {
			t.Fatalf("got %d messages", len(msgs))
		}
		spAssertParentIDs(t, msgs, "", "")
	})
	t.Run("skips_corrupt_lines", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		u1, a1 := spUUID(t), spUUID(t)
		spWriteLines(t, filepath.Join(subDir, "agent-x.jsonl"),
			spDumps(spEntry("user", u1, "", sid, "hi")),
			"not valid json {",
			"",
			spDumps(spEntry("assistant", a1, u1, sid, "ok")),
		)
		spEqualStrings(t, spUUIDs(spSubMsgs(t, sid, "x", projectPath, 0, 0)), []string{u1, a1})
	})
	t.Run("limit_and_offset", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		uuids := make([]string, 6)
		entries := make([]spObj, 6)
		for i := range uuids {
			uuids[i] = spUUID(t)
			parent := ""
			if i > 0 {
				parent = uuids[i-1]
			}
			typ := "user"
			if i%2 == 1 {
				typ = "assistant"
			}
			entries[i] = spEntry(typ, uuids[i], parent, sid, fmt.Sprintf("m%d", i))
		}
		spWriteTranscript(t, filepath.Join(subDir, "agent-p.jsonl"), entries...)

		if all := spSubMsgs(t, sid, "p", projectPath, 0, 0); len(all) != 6 {
			t.Errorf("all: %d", len(all))
		}
		spEqualStrings(t, spUUIDs(spSubMsgs(t, sid, "p", projectPath, 2, 0)), uuids[:2])
		spEqualStrings(t, spUUIDs(spSubMsgs(t, sid, "p", projectPath, 2, 2)), uuids[2:4])
		spEqualStrings(t, spUUIDs(spSubMsgs(t, sid, "p", projectPath, 0, 4)), uuids[4:])
		if p := spSubMsgs(t, sid, "p", projectPath, 0, 0); len(p) != 6 {
			t.Errorf("limit 0: %d", len(p))
		}
	})
	t.Run("empty_agent_file", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath := spMkdir(t, filepath.Join(tmp, "proj"))
		sid, subDir := spMakeSessionWithSubagents(t, cfg, projectPath)
		spWriteFile(t, filepath.Join(subDir, "agent-empty.jsonl"), "")
		if m := spSubMsgs(t, sid, "empty", projectPath, 0, 0); len(m) != 0 {
			t.Errorf("got %d messages", len(m))
		}
	})
}
