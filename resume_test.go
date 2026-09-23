package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shindakun/agent-sdk-go/internal/transport"
)

const (
	resumeSID  = "11111111-1111-4111-8111-111111111111"
	resumeSID2 = "22222222-2222-4222-8222-222222222222"
)

// isolateResume points home and the config dir at a temp dir and stubs the
// Keychain, so tests never read the developer's real credentials. It returns
// the fake home and a cwd whose project key the tests use.
func isolateResume(t *testing.T) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	setHomeDir(t, home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	prev := keychainCredentials
	keychainCredentials = func() []byte { return nil }
	t.Cleanup(func() { keychainCredentials = prev })
	cwd = t.TempDir()
	return home, cwd
}

func seed(t *testing.T, store SessionStore, cwd, sid string, lines ...string) {
	t.Helper()
	var es []SessionStoreEntry
	for _, l := range lines {
		es = append(es, entry(l))
	}
	if err := store.Append(context.Background(), SessionKey{ProjectKey: ProjectKeyForDirectory(cwd), SessionID: sid}, es); err != nil {
		t.Fatal(err)
	}
}

func materialize(t *testing.T, opts ...Option) *materializedResume {
	t.Helper()
	m, err := materializeResumeSession(context.Background(), newOptions(opts...))
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if m != nil {
		t.Cleanup(m.cleanup)
	}
	return m
}

func TestResumeNoMaterialization(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	for name, opts := range map[string][]Option{
		"no store":              {WithCwd(cwd), WithResume(resumeSID)},
		"no resume or continue": {WithCwd(cwd), WithSessionStore(store, FlushBatched)},
		"non-UUID resume":       {WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume("../../etc")},
		"no entries":            {WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID2)},
		"continue, empty store": {WithCwd(cwd), WithSessionStore(NewInMemorySessionStore(), FlushBatched), WithContinueConversation()},
	} {
		if m := materialize(t, opts...); m != nil {
			t.Errorf("%s: materialized %+v", name, m)
		}
	}
}

func TestResumeWritesTranscriptAndCleanupRemovesDir(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user", "uuid":"u1"}`, `{"type":"assistant","uuid":"a1"}`)

	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID))
	if m == nil || m.resumeSessionID != resumeSID {
		t.Fatalf("materialized = %+v", m)
	}
	path := filepath.Join(m.configDir, "projects", ProjectKeyForDirectory(cwd), resumeSID+".jsonl")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"type\":\"user\",\"uuid\":\"u1\"}\n{\"type\":\"assistant\",\"uuid\":\"a1\"}\n"; string(got) != want {
		t.Errorf("transcript = %q, want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("transcript mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	m.cleanup()
	if _, err := os.Stat(m.configDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config dir not removed: %v", err)
	}
}

func TestResumeCredentialsRedacted(t *testing.T) {
	home, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	cfg := filepath.Join(home, ".claude")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	creds := `{"claudeAiOauth":{"accessToken":"at","refreshToken":"rt","expiresAt":1},"other":true}`
	if err := os.WriteFile(filepath.Join(cfg, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"userID":"u"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID))
	var got struct {
		OAuth map[string]any `json:"claudeAiOauth"`
		Other bool           `json:"other"`
	}
	b, err := os.ReadFile(filepath.Join(m.configDir, ".credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.OAuth["refreshToken"]; ok {
		t.Error("refreshToken was copied")
	}
	if got.OAuth["accessToken"] != "at" || !got.Other {
		t.Errorf("other fields lost: %s", b)
	}
	if cj, _ := os.ReadFile(filepath.Join(m.configDir, ".claude.json")); string(cj) != `{"userID":"u"}` {
		t.Errorf(".claude.json = %q", cj)
	}
}

func TestResumeKeychainFallback(t *testing.T) {
	_, cwd := isolateResume(t)
	keychainCredentials = func() []byte { return []byte(`{"claudeAiOauth":{"accessToken":"kc","refreshToken":"x"}}`) }
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)

	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID))
	b, _ := os.ReadFile(filepath.Join(m.configDir, ".credentials.json"))
	if !bytes.Contains(b, []byte(`"kc"`)) || bytes.Contains(b, []byte("refreshToken")) {
		t.Errorf("credentials = %s", b)
	}

	// Env auth or a caller config dir means the Keychain is not consulted.
	for _, opt := range []Option{
		WithEnv(map[string]string{"ANTHROPIC_API_KEY": "sk"}),
		WithEnv(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"}),
		WithEnv(map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()}),
	} {
		m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID), opt)
		if _, err := os.Stat(filepath.Join(m.configDir, ".credentials.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Keychain credentials written despite env auth or config dir")
		}
	}
}

func TestResumeSettingsSeededFromCallerConfigDir(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	caller := t.TempDir()
	settings := []byte(`{"apiKeyHelper":"/bin/print-key","outputStyle":"Explanatory"}`)
	for _, name := range []string{"settings.json", "cowork_settings.json", ".credentials.json", ".claude.json"} {
		if err := os.WriteFile(filepath.Join(caller, name), settings, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, opt := range map[string]func(){
		"options env": func() {},
		"process env": func() { t.Setenv("CLAUDE_CONFIG_DIR", caller) },
	} {
		opt()
		opts := []Option{WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID)}
		if name == "options env" {
			opts = append(opts, WithEnv(map[string]string{"CLAUDE_CONFIG_DIR": caller}))
		}
		m := materialize(t, opts...)
		for _, f := range []string{"settings.json", "cowork_settings.json", ".claude.json"} {
			got, err := os.ReadFile(filepath.Join(m.configDir, f))
			if err != nil || !bytes.Equal(got, settings) {
				t.Errorf("%s: %s = %q, %v", name, f, got, err)
			}
		}
	}
}

func TestResumeSettingsStripped(t *testing.T) {
	home, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	cfg := filepath.Join(home, ".claude")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	original := `{"apiKeyHelper":"/bin/print-key","enabledPlugins":{"p@m":true},` +
		`"extraKnownMarketplaces":{"m":{"source":"github","repo":"o/r"}},` +
		`"env":{"CLAUDE_CONFIG_DIR":"/elsewhere","KEEP":"1"},"permissions":{"allow":["Bash(ls)"]}}`
	for _, name := range []string{"settings.json", "cowork_settings.json"} {
		// PowerShell writes a UTF-8 BOM.
		if err := os.WriteFile(filepath.Join(cfg, name), append([]byte("\xef\xbb\xbf"), original...), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID))
	want := map[string]any{
		"apiKeyHelper": "/bin/print-key",
		"env":          map[string]any{"KEEP": "1"},
		"permissions":  map[string]any{"allow": []any{"Bash(ls)"}},
	}
	for _, name := range []string{"settings.json", "cowork_settings.json"} {
		b, _ := os.ReadFile(filepath.Join(m.configDir, name))
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("%s: %v (%q)", name, err, b)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestResumeSettingsPassThroughUnchanged(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"malformed":                           {`{not json`, `{not json`},
		"not an object":                       {`[1, 2]`, `[1, 2]`},
		"env not an object":                   {`{"env": "nope", "a": 1}`, `{"env": "nope", "a": 1}`},
		"nothing to strip":                    {`{"a": 1, "env": {"K": "v"}}`, `{"a": 1, "env": {"K": "v"}}`},
		"null":                                {`null`, `null`},
		"overflowing number stays valid JSON": {`{"enabledPlugins": {}, "threshold": 1e999}`, `{"threshold":1e999}`},
	} {
		if got := stripSettingsForResume([]byte(tc.in)); string(got) != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

func TestResumeUnreadableSeedFilesDoNotAbort(t *testing.T) {
	home, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	cfg := filepath.Join(home, ".claude")
	for _, d := range []string{filepath.Join(cfg, "settings.json"), filepath.Join(cfg, ".credentials.json"), filepath.Join(home, ".claude.json")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var logs bytes.Buffer
	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID), WithStderr(&logs))
	for _, f := range []string{"settings.json", ".credentials.json", ".claude.json"} {
		if _, err := os.Stat(filepath.Join(m.configDir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was written", f)
		}
	}
	if _, err := os.Stat(filepath.Join(m.configDir, "projects", ProjectKeyForDirectory(cwd), resumeSID+".jsonl")); err != nil {
		t.Errorf("transcript missing: %v", err)
	}
	if !strings.Contains(logs.String(), "not a regular file") {
		t.Errorf("skip not logged: %q", logs.String())
	}
}

func TestResumeContinuePicksNewestNonSidechain(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user","uuid":"old"}`)
	seed(t, store, cwd, resumeSID2, `{"type":"user","uuid":"new"}`)
	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithContinueConversation())
	if m == nil || m.resumeSessionID != resumeSID2 {
		t.Fatalf("continue picked %+v, want the newest session", m)
	}

	// A newer sidechain is skipped.
	side := "33333333-3333-4333-8333-333333333333"
	seed(t, store, cwd, side, `{"type":"user","isSidechain":true}`)
	if m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithContinueConversation()); m == nil || m.resumeSessionID != resumeSID2 {
		t.Errorf("continue picked %+v, want the newest non-sidechain", m)
	}

	only := NewInMemorySessionStore()
	seed(t, only, cwd, side, `{"type":"user","isSidechain":true}`)
	if m := materialize(t, WithCwd(cwd), WithSessionStore(only, FlushBatched), WithContinueConversation()); m != nil {
		t.Errorf("only sidechains: materialized %+v", m)
	}
}

func TestResumeSubagentsAndMetadata(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	pk := ProjectKeyForDirectory(cwd)
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	sub := func(subpath string, lines ...string) {
		var es []SessionStoreEntry
		for _, l := range lines {
			es = append(es, entry(l))
		}
		if err := store.Append(context.Background(), SessionKey{ProjectKey: pk, SessionID: resumeSID, Subpath: subpath}, es); err != nil {
			t.Fatal(err)
		}
	}
	sub("subagents/agent-a", `{"type":"user","uuid":"s1"}`, `{"type":"agent_metadata","agentType":"old"}`,
		`{"type":"agent_metadata","agentType":"general","worktreePath":"/w"}`)
	sub("subagents/workflows/run-1/agent-b", `{"type":"assistant","uuid":"s2"}`)
	sub("../escape", `{"type":"user"}`)
	sub("/abs", `{"type":"user"}`)
	sub(`C:evil`, `{"type":"user"}`)
	sub(`subagents\..\..\x`, `{"type":"user"}`)

	var logs bytes.Buffer
	m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID), WithStderr(&logs))
	sessionDir := filepath.Join(m.configDir, "projects", pk, resumeSID)

	a, err := os.ReadFile(filepath.Join(sessionDir, "subagents", "agent-a.jsonl"))
	if err != nil || string(a) != "{\"type\":\"user\",\"uuid\":\"s1\"}\n" {
		t.Errorf("agent-a transcript = %q, %v", a, err)
	}
	meta, err := os.ReadFile(filepath.Join(sessionDir, "subagents", "agent-a.meta.json"))
	var got map[string]any
	if err != nil || json.Unmarshal(meta, &got) != nil || got["agentType"] != "general" || got["type"] != nil {
		t.Errorf("agent-a metadata = %q, %v (last entry wins, type removed)", meta, err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "subagents", "workflows", "run-1", "agent-b.jsonl")); err != nil {
		t.Errorf("nested subagent missing: %v", err)
	}
	for _, bad := range []string{"../escape", "/abs", "C:evil", `subagents\..\..\x`} {
		if !strings.Contains(logs.String(), fmt.Sprintf("%q", bad)) {
			t.Errorf("unsafe subpath %q not skipped; logs=%q", bad, logs.String())
		}
	}
	if _, err := os.Stat(filepath.Join(m.configDir, "projects", pk, "escape.jsonl")); err == nil {
		t.Error("traversal wrote outside the session dir")
	}
}

// unsupportedSubkeysStore is a store without subagent support.
type unsupportedSubkeysStore struct{ *InMemorySessionStore }

func (unsupportedSubkeysStore) ListSubkeys(context.Context, SessionListSubkeysKey) ([]string, error) {
	return nil, fmt.Errorf("subkeys: %w", errors.ErrUnsupported)
}

func TestResumeStoreWithoutSubkeys(t *testing.T) {
	_, cwd := isolateResume(t)
	store := unsupportedSubkeysStore{NewInMemorySessionStore()}
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	if m := materialize(t, WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID)); m == nil {
		t.Fatal("not materialized")
	}
}

// blockingStore never answers Load; failingStore fails it.
type blockingStore struct{ *InMemorySessionStore }

func (blockingStore) Load(ctx context.Context, _ SessionKey) ([]SessionStoreEntry, error) {
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)
	return nil, nil
}

type failingStore struct{ *InMemorySessionStore }

func (failingStore) Load(context.Context, SessionKey) ([]SessionStoreEntry, error) {
	return nil, errors.New("backend down")
}

func TestResumeLoadTimeoutAndErrors(t *testing.T) {
	_, cwd := isolateResume(t)
	_, err := materializeResumeSession(context.Background(), newOptions(
		WithCwd(cwd), WithSessionStore(blockingStore{NewInMemorySessionStore()}, FlushBatched),
		WithResume(resumeSID), WithLoadTimeout(50*time.Millisecond)))
	if err == nil || !strings.Contains(err.Error(), "timed out after 50ms") {
		t.Errorf("timeout err = %v", err)
	}
	_, err = materializeResumeSession(context.Background(), newOptions(
		WithCwd(cwd), WithSessionStore(failingStore{NewInMemorySessionStore()}, FlushBatched), WithResume(resumeSID)))
	if err == nil || !strings.Contains(err.Error(), "backend down") || !strings.Contains(err.Error(), "resume materialization") {
		t.Errorf("load err = %v", err)
	}
}

func TestResumeRejectsFileCheckpointing(t *testing.T) {
	o := newOptions(WithSessionStore(NewInMemorySessionStore(), FlushBatched), WithEnableFileCheckpointing())
	if err := o.validateSessionStoreOptions(); err == nil {
		t.Error("session store with file checkpointing was accepted")
	}
}

// captureTransport records the config the session spawns the CLI with.
func captureTransport(t *testing.T, st transport.Transport) *transport.Config {
	t.Helper()
	var cfg transport.Config
	prev := transportFactory
	transportFactory = func(c transport.Config) transport.Transport { cfg = c; return st }
	t.Cleanup(func() { transportFactory = prev })
	return &cfg
}

func TestResumeClientSpawnsAgainstMaterializedDir(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	cfg := captureTransport(t, newScriptedTransport(
		[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"`+resumeSID+`"}`)))

	for _, err := range Query(context.Background(), "hi", WithCwd(cwd),
		WithSessionStore(store, FlushBatched), WithContinueConversation()) {
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := cfg.Env["CLAUDE_CONFIG_DIR"]
	if dir == "" || !strings.Contains(filepath.Base(dir), "claude-resume-") {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q", dir)
	}
	if !argsContainEquals(cfg.Args, "--resume", resumeSID) {
		t.Errorf("missing --resume=%s; args=%v", resumeSID, cfg.Args)
	}
	if argsContainsFlag(cfg.Args, "--continue") {
		t.Errorf("--continue passed alongside the resolved resume; args=%v", cfg.Args)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("materialized dir not removed after the query: %v", err)
	}
}

// failingConnectTransport fails to start.
type failingConnectTransport struct{ *scriptedTransport }

func (failingConnectTransport) Connect(context.Context) error { return errors.New("spawn failed") }

func TestResumeConnectFailureRemovesDir(t *testing.T) {
	_, cwd := isolateResume(t)
	store := NewInMemorySessionStore()
	seed(t, store, cwd, resumeSID, `{"type":"user"}`)
	cfg := captureTransport(t, failingConnectTransport{newScriptedTransport()})

	client := NewClient(WithCwd(cwd), WithSessionStore(store, FlushBatched), WithResume(resumeSID))
	if err := client.Connect(context.Background()); err == nil {
		t.Fatal("connect succeeded")
	}
	dir := cfg.Env["CLAUDE_CONFIG_DIR"]
	if dir == "" {
		t.Fatal("no materialized dir")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("materialized dir left behind after a failed connect: %v", err)
	}
}

func TestInitializeTimeout(t *testing.T) {
	for env, want := range map[string]time.Duration{
		"":       60 * time.Second,
		"1000":   60 * time.Second,
		"120000": 120 * time.Second,
		"junk":   60 * time.Second,
	} {
		t.Setenv("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT", env)
		if got := initializeTimeout(); got != want {
			t.Errorf("CLAUDE_CODE_STREAM_CLOSE_TIMEOUT=%q: %v, want %v", env, got, want)
		}
	}
}
