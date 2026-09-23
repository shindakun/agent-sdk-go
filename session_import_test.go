package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const siSessionID = "550e8400-e29b-41d4-a716-446655440000"

type siEnv struct {
	cwd        string
	projectKey string
	claudeDir  string
	tmp        string
}

func siSetup(t *testing.T) siEnv {
	t.Helper()
	tmp := t.TempDir()
	setHomeDir(t, filepath.Join(tmp, "home"))
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	pk := ProjectKeyForDirectory(cwd)
	config := filepath.Join(tmp, "claude_config")
	projectDir := filepath.Join(config, "projects", pk)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	return siEnv{cwd: cwd, projectKey: pk, claudeDir: projectDir, tmp: tmp}
}

func siEntry(i int) map[string]any {
	return map[string]any{"type": "user", "uuid": fmt.Sprintf("u%d", i), "timestamp": fmt.Sprintf("2026-01-01T00:00:%02dZ", i)}
}

func siEntries(idx ...int) []map[string]any {
	out := make([]map[string]any, 0, len(idx))
	for _, i := range idx {
		out = append(out, siEntry(i))
	}
	return out
}

func siRange(n int) []map[string]any {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return siEntries(idx...)
}

func siWriteJSONL(t *testing.T, path string, entries []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func siParse(t *testing.T, entries []SessionStoreEntry) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		var m map[string]any
		if err := json.Unmarshal(e.Data, &m); err != nil {
			t.Fatalf("unmarshal %s: %v", e.Data, err)
		}
		out = append(out, m)
	}
	return out
}

func siGetEntries(t *testing.T, store SessionStore, key SessionKey) []map[string]any {
	t.Helper()
	got, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return siParse(t, got)
}

func siAssertEqual(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

type siAppendCall struct {
	key     SessionKey
	entries []SessionStoreEntry
}

type siRecordingStore struct {
	*InMemorySessionStore
	mu    sync.Mutex
	calls []siAppendCall
}

func (s *siRecordingStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	s.calls = append(s.calls, siAppendCall{key: key, entries: append([]SessionStoreEntry(nil), entries...)})
	s.mu.Unlock()
	return s.InMemorySessionStore.Append(ctx, key, entries)
}

func TestSessionImportImportsMainTranscript(t *testing.T) {
	env := siSetup(t)
	entries := siRange(7)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), entries)

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID}
	siAssertEqual(t, siGetEntries(t, store, key), entries)
}

func TestSessionImportBatchingCallsAppendPerChunk(t *testing.T) {
	env := siSetup(t)
	entries := siRange(5)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), entries)

	store := &siRecordingStore{InMemorySessionStore: NewInMemorySessionStore()}
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd), ImportBatchSize(2)); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 3 {
		t.Fatalf("append calls = %d, want 3", len(store.calls))
	}
	key := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID}
	want := [][]map[string]any{entries[0:2], entries[2:4], entries[4:5]}
	for i, c := range store.calls {
		siAssertEqual(t, c.key, key)
		siAssertEqual(t, siParse(t, c.entries), want[i])
	}
	siAssertEqual(t, siGetEntries(t, store, key), entries)
}

func TestSessionImportSkipsBlankLines(t *testing.T) {
	env := siSetup(t)
	b0, _ := json.Marshal(siEntry(0))
	b1, _ := json.Marshal(siEntry(1))
	path := filepath.Join(env.claudeDir, siSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(string(b0)+"\n\n"+string(b1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID}
	siAssertEqual(t, siGetEntries(t, store, key), siEntries(0, 1))
}

func TestSessionImportNonpositiveBatchSizeUsesDefault(t *testing.T) {
	env := siSetup(t)
	entries := siRange(3)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), entries)

	store := &siRecordingStore{InMemorySessionStore: NewInMemorySessionStore()}
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd), ImportBatchSize(0)); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("append calls = %d, want 1", len(store.calls))
	}
	key := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID}
	siAssertEqual(t, siGetEntries(t, store, key), entries)
}

func TestSessionImportSubagentTranscriptsWithSubpath(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))
	sub := siEntries(10, 11)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID, "subagents", "agent-abc.jsonl"), sub)

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	subKey := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID, Subpath: "subagents/agent-abc"}
	siAssertEqual(t, siGetEntries(t, store, subKey), sub)
	subkeys, err := store.ListSubkeys(context.Background(), SessionListSubkeysKey{ProjectKey: env.projectKey, SessionID: siSessionID})
	if err != nil {
		t.Fatal(err)
	}
	siAssertEqual(t, subkeys, []string{"subagents/agent-abc"})
}

func TestSessionImportNestedSubagentTranscripts(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))
	nested := filepath.Join(env.claudeDir, siSessionID, "subagents", "workflows", "run-1")
	siWriteJSONL(t, filepath.Join(nested, "agent-def.jsonl"), siEntries(20))

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	subKey := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID, Subpath: "subagents/workflows/run-1/agent-def"}
	siAssertEqual(t, siGetEntries(t, store, subKey), siEntries(20))
}

func TestSessionImportMetaJSONSidecarAsAgentMetadata(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))
	subDir := filepath.Join(env.claudeDir, siSessionID, "subagents")
	siWriteJSONL(t, filepath.Join(subDir, "agent-abc.jsonl"), siEntries(10))
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.meta.json"), []byte(`{"agentType": "coder", "worktreePath": "/tmp/wt"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	subKey := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID, Subpath: "subagents/agent-abc"}
	stored := siGetEntries(t, store, subKey)
	if len(stored) < 2 {
		t.Fatalf("stored = %#v, want at least 2 entries", stored)
	}
	siAssertEqual(t, stored[0], siEntry(10))
	siAssertEqual(t, stored[1], map[string]any{"type": "agent_metadata", "agentType": "coder", "worktreePath": "/tmp/wt"})
}

func TestSessionImportMetaJSONTypeKeyCannotShadowMarker(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))
	subDir := filepath.Join(env.claudeDir, siSessionID, "subagents")
	siWriteJSONL(t, filepath.Join(subDir, "agent-abc.jsonl"), siEntries(10))
	if err := os.WriteFile(filepath.Join(subDir, "agent-abc.meta.json"), []byte(`{"type": "something-else", "toolUseId": "toolu_1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	subKey := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID, Subpath: "subagents/agent-abc"}
	stored := siGetEntries(t, store, subKey)
	if len(stored) < 2 {
		t.Fatalf("stored = %#v, want at least 2 entries", stored)
	}
	siAssertEqual(t, stored[1], map[string]any{"type": "agent_metadata", "toolUseId": "toolu_1"})
}

func TestSessionImportUnusableMetaJSONSidecarTreatedAsAbsent(t *testing.T) {
	for _, sidecar := range []string{"not json {", "[1, 2]", "42"} {
		t.Run(sidecar, func(t *testing.T) {
			env := siSetup(t)
			siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))
			subDir := filepath.Join(env.claudeDir, siSessionID, "subagents")
			siWriteJSONL(t, filepath.Join(subDir, "agent-abc.jsonl"), siEntries(10))
			if err := os.WriteFile(filepath.Join(subDir, "agent-abc.meta.json"), []byte(sidecar), 0o644); err != nil {
				t.Fatal(err)
			}

			store := NewInMemorySessionStore()
			if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
				t.Fatal(err)
			}
			subKey := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID, Subpath: "subagents/agent-abc"}
			siAssertEqual(t, siGetEntries(t, store, subKey), siEntries(10))
		})
	}
}

func TestSessionImportIncludeSubagentsFalseSkipsSubagents(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID, "subagents", "agent-abc.jsonl"), siEntries(10))

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd), ImportIncludeSubagents(false)); err != nil {
		t.Fatal(err)
	}
	subkeys, err := store.ListSubkeys(context.Background(), SessionListSubkeysKey{ProjectKey: env.projectKey, SessionID: siSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(subkeys) != 0 {
		t.Fatalf("subkeys = %v, want none", subkeys)
	}
}

func TestSessionImportNoSubagentsDirIsNoop(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID}
	siAssertEqual(t, siGetEntries(t, store, key), siEntries(0))
}

func TestSessionImportInvalidUUIDErrors(t *testing.T) {
	setHomeDir(t, t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	err := ImportSessionToStore(context.Background(), "../../etc/passwd", NewInMemorySessionStore())
	if err == nil {
		t.Fatal("expected error for invalid session id")
	}
	if !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("err = %v, want invalid session id", err)
	}
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, should not be ErrSessionNotFound", err)
	}
}

func TestSessionImportSessionNotFound(t *testing.T) {
	env := siSetup(t)
	err := ImportSessionToStore(context.Background(), siSessionID, NewInMemorySessionStore(), ImportDirectory(env.cwd))
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v, want ErrSessionNotFound", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want message containing \"not found\"", err)
	}
}

func TestSessionImportSubpathMatchesFilePathToSessionKey(t *testing.T) {
	env := siSetup(t)
	mainFile := filepath.Join(env.claudeDir, siSessionID+".jsonl")
	siWriteJSONL(t, mainFile, siEntries(0))
	subFile := filepath.Join(env.claudeDir, siSessionID, "subagents", "agent-xyz.jsonl")
	siWriteJSONL(t, subFile, siEntries(1))

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store, ImportDirectory(env.cwd)); err != nil {
		t.Fatal(err)
	}
	projectsDir := filepath.Dir(env.claudeDir)
	expectedMain, ok1 := filePathToSessionKey(mainFile, projectsDir)
	expectedSub, ok2 := filePathToSessionKey(subFile, projectsDir)
	if !ok1 || !ok2 {
		t.Fatalf("filePathToSessionKey ok = %v, %v", ok1, ok2)
	}
	siAssertEqual(t, siGetEntries(t, store, expectedMain), siEntries(0))
	siAssertEqual(t, siGetEntries(t, store, expectedSub), siEntries(1))
}

func TestSessionImportDirectoryUnsetKeysFromResolvedPathNotCwd(t *testing.T) {
	env := siSetup(t)
	siWriteJSONL(t, filepath.Join(env.claudeDir, siSessionID+".jsonl"), siEntries(0))

	elsewhere := filepath.Join(env.tmp, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(elsewhere)
	if ProjectKeyForDirectory("") == env.projectKey {
		t.Fatal("precondition: cwd project key must differ")
	}

	store := NewInMemorySessionStore()
	if err := ImportSessionToStore(context.Background(), siSessionID, store); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{ProjectKey: env.projectKey, SessionID: siSessionID}
	siAssertEqual(t, siGetEntries(t, store, key), siEntries(0))
}
