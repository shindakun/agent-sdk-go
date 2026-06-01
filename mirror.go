package claude

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
)

// mirrorMaxPendingEntries bounds the eager-flush threshold for the live mirror.
const mirrorMaxPendingEntries = 500

// transcriptMirrorFrame is the CLI's stdout frame carrying entries to mirror.
type transcriptMirrorFrame struct {
	FilePath string            `json:"filePath"`
	Entries  []json.RawMessage `json:"entries"`
}

// mirrorBatcher accumulates transcript_mirror frames and flushes them to a
// SessionStore, mirroring the official TranscriptMirrorBatcher. On append
// failure it emits a MirrorErrorMessage via emit (non-fatal).
type mirrorBatcher struct {
	store      SessionStore
	flush      SessionStoreFlushMode
	projectsCb func() (string, error) // resolves the projects dir lazily
	emit       func(Message)

	mu      sync.Mutex
	pending map[string][]SessionStoreEntry // keyed by storeKey
	keys    map[string]SessionKey
	count   int
}

func newMirrorBatcher(store SessionStore, flush SessionStoreFlushMode, projectsCb func() (string, error), emit func(Message)) *mirrorBatcher {
	return &mirrorBatcher{
		store:      store,
		flush:      flush,
		projectsCb: projectsCb,
		emit:       emit,
		pending:    map[string][]SessionStoreEntry{},
		keys:       map[string]SessionKey{},
	}
}

// enqueue buffers a raw transcript_mirror frame; with eager flush mode it
// flushes once thresholds are exceeded.
func (b *mirrorBatcher) enqueue(raw []byte) {
	var frame transcriptMirrorFrame
	if json.Unmarshal(raw, &frame) != nil {
		return
	}
	projectsDir, err := b.projectsCb()
	if err != nil {
		return
	}
	key, ok := filePathToSessionKey(frame.FilePath, projectsDir)
	if !ok {
		return
	}

	entries := make([]SessionStoreEntry, 0, len(frame.Entries))
	for _, e := range frame.Entries {
		entries = append(entries, SessionStoreEntry{Data: append([]byte(nil), e...)})
	}

	b.mu.Lock()
	sk := storeKey(key)
	b.pending[sk] = append(b.pending[sk], entries...)
	b.keys[sk] = key
	b.count += len(entries)
	overflow := b.flush == FlushImmediate || b.count >= mirrorMaxPendingEntries
	b.mu.Unlock()

	if overflow {
		b.Flush(context.Background())
	}
}

// Flush appends all pending entries to the store. A per-key append failure
// emits a MirrorErrorMessage and is otherwise non-fatal.
func (b *mirrorBatcher) Flush(ctx context.Context) {
	b.mu.Lock()
	pending := b.pending
	keys := b.keys
	b.pending = map[string][]SessionStoreEntry{}
	b.keys = map[string]SessionKey{}
	b.count = 0
	b.mu.Unlock()

	for sk, entries := range pending {
		key := keys[sk]
		if err := b.store.Append(ctx, key, entries); err != nil {
			k := key
			if b.emit != nil {
				b.emit(&MirrorErrorMessage{Key: &k, Error: err.Error()})
			}
		}
	}
}

// filePathToSessionKey derives a SessionKey from a transcript file path under
// projectsDir, mirroring the official file_path_to_session_key:
//   - main:     <projectsDir>/<projectKey>/<sessionID>.jsonl
//   - subagent: <projectsDir>/<projectKey>/<sessionID>/subagents/.../agent-<id>.jsonl
func filePathToSessionKey(filePath, projectsDir string) (SessionKey, bool) {
	rel, err := filepath.Rel(projectsDir, filePath)
	if err != nil {
		return SessionKey{}, false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return SessionKey{}, false
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return SessionKey{}, false
	}
	projectKey := parts[0]
	second := parts[1]

	if len(parts) == 2 && strings.HasSuffix(second, ".jsonl") {
		return SessionKey{
			ProjectKey: projectKey,
			SessionID:  strings.TrimSuffix(second, ".jsonl"),
		}, true
	}
	if len(parts) >= 4 {
		sub := append([]string(nil), parts[2:]...)
		last := sub[len(sub)-1]
		sub[len(sub)-1] = strings.TrimSuffix(last, ".jsonl")
		return SessionKey{
			ProjectKey: projectKey,
			SessionID:  second,
			Subpath:    strings.Join(sub, "/"),
		}, true
	}
	return SessionKey{}, false
}
