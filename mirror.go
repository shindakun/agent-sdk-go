package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Live mirror thresholds and retry policy, as in the official
// TranscriptMirrorBatcher.
const (
	mirrorMaxPendingEntries = 500
	mirrorMaxPendingBytes   = 1 << 20

	mirrorAppendMaxAttempts = 3
)

var (
	mirrorAppendBackoff = []time.Duration{200 * time.Millisecond, 800 * time.Millisecond}
	mirrorSendTimeout   = 60 * time.Second
)

// transcriptMirrorFrame is the CLI's stdout frame carrying entries to mirror.
type transcriptMirrorFrame struct {
	FilePath string            `json:"filePath"`
	Entries  []json.RawMessage `json:"entries"`
}

type mirrorItem struct {
	filePath string
	entries  []SessionStoreEntry
}

// mirrorBatcher accumulates transcript_mirror frames and appends them to a
// SessionStore on each result, on close, and in the background once the
// pending buffer passes 500 entries or 1 MiB (every frame in eager mode).
// Each file's entries are appended in order, files in first-seen order. A
// failed append is retried twice with backoff (a timeout is not retried, since
// the call may still land); a batch that still fails is dropped and reported
// as a MirrorErrorMessage. The local transcript is already durable, so
// failures never stop the session.
type mirrorBatcher struct {
	store      SessionStore
	projectsCb func() (string, error) // resolves the projects dir lazily
	emit       func(Message)
	warn       func(string)
	maxEntries int
	maxBytes   int

	mu      sync.Mutex
	pending []mirrorItem
	entries int
	bytes   int

	// flushMu serializes drains so appends stay in order.
	flushMu sync.Mutex
}

func newMirrorBatcher(store SessionStore, flush SessionStoreFlushMode, projectsCb func() (string, error), emit func(Message), warn func(string)) *mirrorBatcher {
	b := &mirrorBatcher{store: store, projectsCb: projectsCb, emit: emit, warn: warn,
		maxEntries: mirrorMaxPendingEntries, maxBytes: mirrorMaxPendingBytes}
	if flush == FlushEager {
		b.maxEntries, b.maxBytes = 0, 0
	}
	return b
}

// enqueue buffers a raw transcript_mirror frame, starting a background drain
// when the thresholds are passed.
func (b *mirrorBatcher) enqueue(raw []byte) {
	var frame transcriptMirrorFrame
	if json.Unmarshal(raw, &frame) != nil {
		return
	}
	entries := make([]SessionStoreEntry, 0, len(frame.Entries))
	size := 2
	for _, e := range frame.Entries {
		entries = append(entries, SessionStoreEntry{Data: append([]byte(nil), e...)})
		size += len(e) + 1
	}
	b.mu.Lock()
	b.pending = append(b.pending, mirrorItem{frame.FilePath, entries})
	b.entries += len(entries)
	b.bytes += size
	overflow := b.entries > b.maxEntries || b.bytes > b.maxBytes
	b.mu.Unlock()
	if overflow {
		go b.Flush(context.Background())
	}
}

// Flush appends everything pending, after any drain already in progress.
func (b *mirrorBatcher) Flush(ctx context.Context) {
	b.mu.Lock()
	items := b.pending
	b.pending, b.entries, b.bytes = nil, 0, 0
	b.mu.Unlock()

	b.flushMu.Lock()
	if len(items) == 0 {
		b.flushMu.Unlock()
		return
	}
	errs := b.doFlush(ctx, items)
	b.flushMu.Unlock()
	// Reported after releasing the lock, so a slow consumer never holds up
	// later drains.
	for _, e := range errs {
		if b.emit != nil {
			b.emit(e)
		}
	}
}

func (b *mirrorBatcher) doFlush(ctx context.Context, items []mirrorItem) []*MirrorErrorMessage {
	var order []string
	byPath := map[string][]SessionStoreEntry{}
	for _, it := range items {
		if _, ok := byPath[it.filePath]; !ok {
			order = append(order, it.filePath)
		}
		byPath[it.filePath] = append(byPath[it.filePath], it.entries...)
	}
	projectsDir, err := b.projectsCb()
	if err != nil {
		return nil
	}
	var errs []*MirrorErrorMessage
	for _, path := range order {
		entries := byPath[path]
		if len(entries) == 0 {
			continue
		}
		key, ok := filePathToSessionKey(path, projectsDir)
		if !ok {
			if b.warn != nil {
				b.warn(fmt.Sprintf("[SessionStore] dropping mirror frame: filePath %s is not under %s -- "+
					"subprocess CLAUDE_CONFIG_DIR likely differs from parent (custom env / container?)", path, projectsDir))
			}
			continue
		}
		var lastErr error
		for attempt := 0; attempt < mirrorAppendMaxAttempts; attempt++ {
			if attempt > 0 {
				time.Sleep(mirrorAppendBackoff[attempt-1])
			}
			actx, cancel := context.WithTimeout(ctx, mirrorSendTimeout)
			lastErr = b.store.Append(actx, key, entries)
			timedOut := errors.Is(actx.Err(), context.DeadlineExceeded)
			cancel()
			if lastErr == nil || timedOut {
				break
			}
		}
		if lastErr != nil {
			k := key
			errs = append(errs, &MirrorErrorMessage{Key: &k, Error: lastErr.Error()})
		}
	}
	return errs
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
