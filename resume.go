package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Resuming a session that lives in a SessionStore: the CLI can only resume
// from a local transcript, so the session is loaded from the store and written
// to a temporary directory laid out like ~/.claude, and the CLI runs with
// CLAUDE_CONFIG_DIR pointing there. Mirrors the official SDK's
// _internal/session_resume.py.

// defaultLoadTimeout bounds each store call during resume materialization.
const defaultLoadTimeout = 60 * time.Second

// keychainServiceName is the macOS Keychain service holding the CLI's OAuth
// credentials when CLAUDE_CONFIG_DIR is unset.
const keychainServiceName = "Claude Code-credentials"

var uuidRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// materializedResume is a session written to a temporary config dir.
type materializedResume struct {
	configDir       string
	resumeSessionID string
}

// cleanup removes the temporary config dir. Call it after the subprocess exits.
func (m *materializedResume) cleanup() {
	removeAllWithRetry(m.configDir)
}

// applyTo returns a copy of o repointed at the materialized config dir:
// CLAUDE_CONFIG_DIR set in env, resume set to the materialized session, and
// continue cleared (already resolved to a concrete session).
func (m *materializedResume) applyTo(o *Options) *Options {
	c := *o
	c.env = make(map[string]string, len(o.env)+1)
	for k, v := range o.env {
		c.env[k] = v
	}
	c.env["CLAUDE_CONFIG_DIR"] = m.configDir
	c.resume = m.resumeSessionID
	c.continueConversation = false
	return &c
}

// validateSessionStoreOptions rejects session-store option combinations that
// cannot work, before the subprocess is spawned.
func (o *Options) validateSessionStoreOptions() error {
	if o.sessionStore == nil {
		return nil
	}
	if o.enableFileCheckpointing {
		return errors.New("claude: WithSessionStore cannot be combined with WithEnableFileCheckpointing " +
			"(checkpoints are local-disk only and would diverge from the mirrored transcript)")
	}
	return nil
}

// materializeResumeSession loads the session to resume from the configured
// store and writes it to a temporary config dir. It returns nil when there is
// nothing to materialize: no store, no resume or continue, a resume value that
// is not a UUID, or no entries in the store. The caller then falls through to
// a plain resume (or, for continue, a fresh session).
func materializeResumeSession(ctx context.Context, o *Options) (*materializedResume, error) {
	store := o.sessionStore
	if store == nil || (o.resume == "" && !o.continueConversation) {
		return nil, nil
	}
	timeout := o.loadTimeout
	if timeout <= 0 {
		timeout = defaultLoadTimeout
	}
	projectKey := ProjectKeyForDirectory(o.cwd)

	var (
		sessionID string
		entries   []SessionStoreEntry
		err       error
	)
	if o.resume != "" {
		// The id becomes a path component below.
		if !uuidRE.MatchString(o.resume) {
			return nil, nil
		}
		sessionID = o.resume
		entries, err = loadWithTimeout(ctx, store, SessionKey{ProjectKey: projectKey, SessionID: sessionID}, timeout)
	} else {
		sessionID, entries, err = resolveContinueCandidate(ctx, store, projectKey, timeout)
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	tmp, err := os.MkdirTemp("", "claude-resume-")
	if err != nil {
		return nil, err
	}
	m := &materializedResume{configDir: tmp, resumeSessionID: sessionID}
	projectDir := filepath.Join(tmp, "projects", projectKey)
	if err := writeJSONL(filepath.Join(projectDir, sessionID+".jsonl"), entries); err != nil {
		m.cleanup()
		return nil, err
	}
	copyAuthFiles(tmp, o.env, o.stderr)
	if err := materializeSubkeys(ctx, store, projectDir, projectKey, sessionID, timeout, o.stderr); err != nil {
		m.cleanup()
		return nil, err
	}
	return m, nil
}

// storeCall runs one store call under the load timeout and wraps failures
// with what was being done.
func storeCall[T any](ctx context.Context, timeout time.Duration, what string, call func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := call(ctx)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			var zero T
			return zero, fmt.Errorf("claude: %s failed during resume materialization: %w", what, r.err)
		}
		return r.v, nil
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("claude: %s timed out after %dms during resume materialization",
			what, timeout.Milliseconds())
	}
}

func loadWithTimeout(ctx context.Context, store SessionStore, key SessionKey, timeout time.Duration) ([]SessionStoreEntry, error) {
	what := "SessionStore.Load() for session " + key.SessionID
	if key.Subpath != "" {
		what += " subpath " + key.Subpath
	}
	return storeCall(ctx, timeout, what, func(ctx context.Context) ([]SessionStoreEntry, error) {
		return store.Load(ctx, key)
	})
}

// resolveContinueCandidate picks the most recently modified session that is
// not a subagent sidechain. Sidechains are mirrored as top-level keys and
// often have the newest mtime, so each candidate is loaded (the load is needed
// anyway) and skipped if its first entry is a sidechain.
func resolveContinueCandidate(ctx context.Context, store SessionStore, projectKey string, timeout time.Duration) (string, []SessionStoreEntry, error) {
	sessions, err := storeCall(ctx, timeout, "SessionStore.ListSessions()", func(ctx context.Context) ([]SessionStoreListEntry, error) {
		return store.ListSessions(ctx, projectKey)
	})
	if err != nil {
		return "", nil, err
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Mtime > sessions[j].Mtime })
	for _, cand := range sessions {
		if !uuidRE.MatchString(cand.SessionID) {
			continue
		}
		entries, err := loadWithTimeout(ctx, store, SessionKey{ProjectKey: projectKey, SessionID: cand.SessionID}, timeout)
		if err != nil {
			return "", nil, err
		}
		if len(entries) == 0 {
			continue
		}
		var first struct {
			IsSidechain bool `json:"isSidechain"`
		}
		if json.Unmarshal(entries[0].Data, &first) == nil && first.IsSidechain {
			continue
		}
		return cand.SessionID, entries, nil
	}
	return "", nil, nil
}

// writeJSONL writes one compact JSON object per line, mode 0600.
func writeJSONL(path string, entries []SessionStoreEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range entries {
		if err := json.Compact(&buf, e.Data); err != nil {
			return fmt.Errorf("claude: invalid session store entry: %w", err)
		}
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0o600)
}

// copyAuthFiles seeds tmp with the caller's auth and user config so the
// resumed CLI can authenticate: .credentials.json (refresh token removed),
// .claude.json, and settings.json / cowork_settings.json (with keys that
// misbehave under a redirected config dir removed). Missing files are fine.
//
// Sources follow the CLI: .credentials.json and the settings files live in the
// config dir (CLAUDE_CONFIG_DIR, else ~/.claude); .claude.json lives in
// CLAUDE_CONFIG_DIR when set, else at ~/.claude.json.
func copyAuthFiles(tmp string, optEnv map[string]string, logw io.Writer) {
	callerConfigDir := optEnv["CLAUDE_CONFIG_DIR"]
	if callerConfigDir == "" {
		callerConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	home, _ := os.UserHomeDir()
	sourceConfigDir := callerConfigDir
	if sourceConfigDir == "" {
		sourceConfigDir = filepath.Join(home, ".claude")
	}

	creds := readIfPresent(filepath.Join(sourceConfigDir, ".credentials.json"), logw)

	// The default macOS setup keeps OAuth tokens in the Keychain. Redirecting
	// CLAUDE_CONFIG_DIR changes the Keychain service-name suffix, so the
	// resumed CLI's lookup misses and falls back to tmp/.credentials.json.
	// Populate that file from the Keychain unless env auth or a custom config
	// dir is already in play.
	envSet := func(k string) bool { return optEnv[k] != "" || os.Getenv(k) != "" }
	if callerConfigDir == "" && !envSet("ANTHROPIC_API_KEY") && !envSet("CLAUDE_CODE_OAUTH_TOKEN") {
		if kc := keychainCredentials(); kc != nil {
			creds = kc
		}
	}
	writeRedactedCredentials(creds, filepath.Join(tmp, ".credentials.json"))

	claudeJSON := filepath.Join(home, ".claude.json")
	if callerConfigDir != "" {
		claudeJSON = filepath.Join(callerConfigDir, ".claude.json")
	}
	copyIfPresent(claudeJSON, filepath.Join(tmp, ".claude.json"), nil, logw)

	// User settings carry apiKeyHelper (an auth mechanism of its own) plus
	// env, hooks, and permissions; without them an apiKeyHelper-only host is
	// "Not logged in" after resuming. cowork_settings.json is the name the
	// CLI reads in cowork-plugins mode.
	for _, name := range []string{"settings.json", "cowork_settings.json"} {
		copyIfPresent(filepath.Join(sourceConfigDir, name), filepath.Join(tmp, name), stripSettingsForResume, logw)
	}
}

// resumeSettingsStrippedKeys are user-settings keys that misbehave under the
// redirected config dir: plugin declarations would reconcile against the empty
// temp plugin cache and network-install every marketplace on each resume.
var resumeSettingsStrippedKeys = []string{"enabledPlugins", "extraKnownMarketplaces"}

// stripSettingsForResume removes resumeSettingsStrippedKeys and
// env.CLAUDE_CONFIG_DIR (which would point the CLI away from the temp dir).
// Content that is not a JSON object, or needs no change, is returned as is.
func stripSettingsForResume(content []byte) []byte {
	// The CLI's settings reader accepts a UTF-8 BOM, which PowerShell writes.
	body := bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	var parsed map[string]json.RawMessage
	if json.Unmarshal(body, &parsed) != nil || parsed == nil {
		return content
	}
	stripped := false
	for _, k := range resumeSettingsStrippedKeys {
		if _, ok := parsed[k]; ok {
			delete(parsed, k)
			stripped = true
		}
	}
	if raw, ok := parsed["env"]; ok {
		var env map[string]json.RawMessage
		if json.Unmarshal(raw, &env) == nil && env != nil {
			if _, ok := env["CLAUDE_CONFIG_DIR"]; ok {
				delete(env, "CLAUDE_CONFIG_DIR")
				b, err := json.Marshal(env)
				if err != nil {
					return content
				}
				parsed["env"] = b
				stripped = true
			}
		}
	}
	if !stripped {
		return content
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return content
	}
	return out
}

// writeRedactedCredentials writes creds with claudeAiOauth.refreshToken
// removed. The resumed CLI runs under a redirected config dir; a refresh there
// would consume the single-use refresh token and store the new tokens where
// the caller never reads them, revoking the caller's credentials. Without a
// refresh token the CLI does not try.
func writeRedactedCredentials(creds []byte, dst string) {
	if creds == nil {
		return
	}
	out := creds
	var data map[string]json.RawMessage
	if json.Unmarshal(creds, &data) == nil && data != nil {
		var oauth map[string]json.RawMessage
		if json.Unmarshal(data["claudeAiOauth"], &oauth) == nil && oauth != nil {
			if _, ok := oauth["refreshToken"]; ok {
				delete(oauth, "refreshToken")
				if b, err := json.Marshal(oauth); err == nil {
					data["claudeAiOauth"] = b
					if b, err := json.Marshal(data); err == nil {
						out = b
					}
				}
			}
		}
	}
	_ = os.WriteFile(dst, out, 0o600)
}

// readIfPresent reads a regular file. A missing file returns nil silently; any
// other failure (permissions, a directory or FIFO in its place) is logged and
// returns nil, since these files only enrich the temp config dir and must not
// abort or hang the resume.
func readIfPresent(src string, logw io.Writer) []byte {
	fi, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", src)
	}
	var b []byte
	if err == nil {
		b, err = os.ReadFile(src)
	}
	if err != nil {
		logResumeSkip(logw, src, err)
		return nil
	}
	return b
}

// copyIfPresent copies src to dst (mode 0600) through an optional transform.
// See readIfPresent for the skip policy.
func copyIfPresent(src, dst string, transform func([]byte) []byte, logw io.Writer) {
	b := readIfPresent(src, logw)
	if b == nil {
		return
	}
	if transform != nil {
		b = transform(b)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		// Do not leave a truncated file for the CLI to misparse.
		_ = os.Remove(dst)
		logResumeSkip(logw, src, err)
	}
}

func logResumeSkip(w io.Writer, src string, err error) {
	if w != nil {
		_, _ = fmt.Fprintf(w, "claude: warning: [SessionStore] resume: skipping %s (%v)\n", src, err)
	}
}

// keychainCredentials reads the CLI's OAuth credentials from the macOS
// Keychain. It is a package var so tests can substitute it.
var keychainCredentials = readKeychainCredentials

// readKeychainCredentials reads the CLI's OAuth credentials JSON from the
// macOS Keychain. It returns nil on any error and on other platforms.
func readKeychainCredentials() []byte {
	if runtime.GOOS != "darwin" {
		return nil
	}
	name := os.Getenv("USER")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		} else {
			name = "claude-code-user"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "security", "find-generic-password",
		"-a", name, "-w", "-s", keychainServiceName).Output()
	if err != nil {
		return nil
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// materializeSubkeys writes every subagent transcript and metadata sidecar
// stored under sessionID. A store without subkey support (ListSubkeys returns
// an error wrapping errors.ErrUnsupported) has none to write.
func materializeSubkeys(ctx context.Context, store SessionStore, projectDir, projectKey, sessionID string, timeout time.Duration, logw io.Writer) error {
	sessionDir := filepath.Join(projectDir, sessionID)
	subkeys, err := storeCall(ctx, timeout, "SessionStore.ListSubkeys() for session "+sessionID,
		func(ctx context.Context) ([]string, error) {
			return store.ListSubkeys(ctx, SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID})
		})
	if errors.Is(err, errors.ErrUnsupported) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, subpath := range subkeys {
		// Subpaths come from an external store and become path components.
		if !isSafeSubpath(subpath, sessionDir) {
			if logw != nil {
				_, _ = fmt.Fprintf(logw, "claude: warning: [SessionStore] skipping unsafe subpath from ListSubkeys: %q\n", subpath)
			}
			continue
		}
		entries, err := loadWithTimeout(ctx, store,
			SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath}, timeout)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			continue
		}
		metadata, transcript := splitAgentMetadata(entries)
		subFile := filepath.Join(sessionDir, filepath.FromSlash(subpath)) + ".jsonl"
		if len(transcript) > 0 {
			if err := writeJSONL(subFile, transcript); err != nil {
				return err
			}
		}
		if metadata != nil {
			delete(metadata, "type")
			b, err := json.Marshal(metadata)
			if err != nil {
				return err
			}
			meta := agentMetadataSidecarPath(subFile)
			if err := os.MkdirAll(filepath.Dir(meta), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(meta, b, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

// isSafeSubpath rejects subpaths that are empty, absolute, drive- or
// UNC-prefixed, contain "." or ".." segments or NUL, or resolve outside
// sessionDir. Both separators are checked whatever the host OS, since store
// keys may come from either.
func isSafeSubpath(subpath, sessionDir string) bool {
	if subpath == "" || strings.ContainsRune(subpath, 0) {
		return false
	}
	if strings.HasPrefix(subpath, "/") || strings.HasPrefix(subpath, `\`) || filepath.IsAbs(subpath) {
		return false
	}
	if len(subpath) >= 2 && subpath[1] == ':' {
		return false
	}
	for _, seg := range strings.FieldsFunc(subpath, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			return false
		}
	}
	target := filepath.Join(sessionDir, filepath.FromSlash(subpath)) + ".jsonl"
	rel, err := filepath.Rel(sessionDir, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}
	return true
}

// removeAllWithRetry removes dir, retrying briefly: on Windows an antivirus or
// indexer can hold a freshly written file (notably .credentials.json) open for
// a moment. Never fails loudly.
func removeAllWithRetry(dir string) {
	for i := 0; i < 4; i++ {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.RemoveAll(dir)
}
