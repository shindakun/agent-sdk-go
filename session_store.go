package claude

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// SessionStore is an abstract transcript store, mirroring the official SDK's
// SessionStore. Implementations persist session entries keyed by project and
// session id; [InMemorySessionStore] is the built-in implementation.
type SessionStore interface {
	Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error
	Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error)
	ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error)
	ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error)
	ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error)
	Delete(ctx context.Context, key SessionKey) error
}

// SessionKey identifies a session (optionally a subagent subpath) within a
// project.
type SessionKey struct {
	ProjectKey string
	SessionID  string
	Subpath    string
}

// SessionListSubkeysKey identifies a session whose subkeys are being listed.
type SessionListSubkeysKey struct {
	ProjectKey string
	SessionID  string
}

// SessionStoreEntry is one stored transcript line (a raw JSON object).
type SessionStoreEntry struct {
	Data json.RawMessage
}

// SessionStoreListEntry is a session id with its last-modified marker.
type SessionStoreListEntry struct {
	SessionID string
	Mtime     int64
}

// SessionSummaryEntry is a session summary with its last-modified marker.
type SessionSummaryEntry struct {
	SessionID string
	Summary   string
	Mtime     int64
}

// SessionStoreFlushMode controls when a store flushes pending writes.
type SessionStoreFlushMode int

const (
	// FlushImmediate flushes on every append.
	FlushImmediate SessionStoreFlushMode = iota
	// FlushOnClose defers flushing until the store is closed.
	FlushOnClose
)

// storeKey is the internal map key.
func storeKey(k SessionKey) string {
	if k.Subpath != "" {
		return k.ProjectKey + "/" + k.SessionID + "/" + k.Subpath
	}
	return k.ProjectKey + "/" + k.SessionID
}

// InMemorySessionStore is an in-memory [SessionStore], suitable for tests and
// ephemeral mirrors. It maintains monotonically increasing mtimes for
// staleness detection, mirroring the official implementation.
type InMemorySessionStore struct {
	mu      sync.Mutex
	entries map[string][]SessionStoreEntry
	mtimes  map[string]int64
	clock   int64
}

// NewInMemorySessionStore creates an empty in-memory store.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		entries: map[string][]SessionStoreEntry{},
		mtimes:  map[string]int64{},
	}
}

func (s *InMemorySessionStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := storeKey(key)
	s.entries[k] = append(s.entries[k], entries...)
	s.clock++
	s.mtimes[k] = s.clock
	return nil
}

func (s *InMemorySessionStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.entries[storeKey(key)]
	out := make([]SessionStoreEntry, len(src))
	copy(out, src)
	return out, nil
}

func (s *InMemorySessionStore) ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SessionStoreListEntry
	prefix := projectKey + "/"
	for k, mtime := range s.mtimes {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok || strings.Contains(rest, "/") {
			continue // skip other projects and subagent subkeys
		}
		out = append(out, SessionStoreListEntry{SessionID: rest, Mtime: mtime})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out, nil
}

func (s *InMemorySessionStore) ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error) {
	list, err := s.ListSessions(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionSummaryEntry, 0, len(list))
	for _, e := range list {
		summary := ""
		if entries := s.entries[projectKey+"/"+e.SessionID]; len(entries) > 0 {
			summary = firstUserText(extractMessage(entries[0].Data))
		}
		out = append(out, SessionSummaryEntry{SessionID: e.SessionID, Summary: summary, Mtime: e.Mtime})
	}
	return out, nil
}

func (s *InMemorySessionStore) ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := key.ProjectKey + "/" + key.SessionID + "/"
	var out []string
	for k := range s.entries {
		if rest, ok := strings.CutPrefix(k, prefix); ok && rest != "" {
			out = append(out, rest)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *InMemorySessionStore) Delete(ctx context.Context, key SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := storeKey(key)
	delete(s.entries, k)
	delete(s.mtimes, k)
	// When deleting a session, also drop its subagent subkeys.
	if key.Subpath == "" {
		prefix := k + "/"
		for ek := range s.entries {
			if strings.HasPrefix(ek, prefix) {
				delete(s.entries, ek)
				delete(s.mtimes, ek)
			}
		}
	}
	return nil
}

// extractMessage pulls the "message" field out of a stored entry, if present.
func extractMessage(data json.RawMessage) json.RawMessage {
	var env struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(data, &env) == nil && len(env.Message) > 0 {
		return env.Message
	}
	return data
}

// ProjectKeyForDirectory returns the project key (the sanitized directory name)
// the CLI uses for sessions under the given working directory.
func ProjectKeyForDirectory(directory string) string {
	return sanitizePath(directory)
}

// ForkSessionResult reports the new session id produced by a fork.
type ForkSessionResult struct {
	SessionID string
}

// RenameSessionViaStore records a custom title for a session in the store.
func RenameSessionViaStore(ctx context.Context, store SessionStore, key SessionKey, title string) error {
	b, _ := json.Marshal(map[string]string{"type": "rename", "customTitle": title})
	return store.Append(ctx, key, []SessionStoreEntry{{Data: b}})
}

// TagSessionViaStore records a tag for a session in the store.
func TagSessionViaStore(ctx context.Context, store SessionStore, key SessionKey, tag string) error {
	b, _ := json.Marshal(map[string]string{"type": "tag", "tag": tag})
	return store.Append(ctx, key, []SessionStoreEntry{{Data: b}})
}

// DeleteSessionViaStore removes a session from the store.
func DeleteSessionViaStore(ctx context.Context, store SessionStore, key SessionKey) error {
	return store.Delete(ctx, key)
}

// ForkSessionViaStore copies a session's entries under a new session id within
// the same project, returning the new id.
func ForkSessionViaStore(ctx context.Context, store SessionStore, key SessionKey, newSessionID string) (ForkSessionResult, error) {
	entries, err := store.Load(ctx, key)
	if err != nil {
		return ForkSessionResult{}, err
	}
	dst := SessionKey{ProjectKey: key.ProjectKey, SessionID: newSessionID}
	if err := store.Append(ctx, dst, entries); err != nil {
		return ForkSessionResult{}, err
	}
	return ForkSessionResult{SessionID: newSessionID}, nil
}
