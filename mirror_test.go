package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Ports of the batcher cases in upstream's tests/test_transcript_mirror.py.

type mirrorRecStore struct {
	*InMemorySessionStore
	mu       sync.Mutex
	appends  []string // "<session>/<subpath>:<n>"
	failures int32    // Append fails this many times first
	block    time.Duration
	attempts atomic.Int32
}

func (r *mirrorRecStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	n := r.attempts.Add(1)
	if r.block > 0 {
		select {
		case <-time.After(r.block):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if n <= atomic.LoadInt32(&r.failures) {
		return errors.New("backend down")
	}
	r.mu.Lock()
	r.appends = append(r.appends, fmt.Sprintf("%s/%s:%d", key.SessionID, key.Subpath, len(entries)))
	r.mu.Unlock()
	return r.InMemorySessionStore.Append(ctx, key, entries)
}

func mirrorFrame(path string, n int) []byte {
	var entries []json.RawMessage
	for i := 0; i < n; i++ {
		entries = append(entries, json.RawMessage(fmt.Sprintf(`{"type":"user","uuid":"u%d"}`, i)))
	}
	b, _ := json.Marshal(transcriptMirrorFrame{FilePath: path, Entries: entries})
	return b
}

type mirrorHarness struct {
	b      *mirrorBatcher
	store  *mirrorRecStore
	errsMu sync.Mutex
	errs   []*MirrorErrorMessage
	warns  []string
}

func newMirrorHarness(mode SessionStoreFlushMode) *mirrorHarness {
	h := &mirrorHarness{store: &mirrorRecStore{InMemorySessionStore: NewInMemorySessionStore()}}
	h.b = newMirrorBatcher(h.store, mode, func() (string, error) { return "/cfg/projects", nil },
		func(m Message) {
			h.errsMu.Lock()
			h.errs = append(h.errs, m.(*MirrorErrorMessage))
			h.errsMu.Unlock()
		},
		func(s string) { h.warns = append(h.warns, s) })
	return h
}

func (h *mirrorHarness) appendLog() []string {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	return append([]string(nil), h.store.appends...)
}

func fastMirrorRetries(t *testing.T) {
	prev := mirrorAppendBackoff
	mirrorAppendBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { mirrorAppendBackoff = prev })
}

func TestMirrorEnqueueThenFlushAppends(t *testing.T) {
	h := newMirrorHarness(FlushBatched)
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s1.jsonl", 2))
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s1.jsonl", 0))
	if log := h.appendLog(); len(log) != 0 {
		t.Fatalf("appended before flush: %v", log)
	}
	h.b.Flush(context.Background())
	if log := h.appendLog(); strings.Join(log, ",") != "s1/:2" {
		t.Errorf("appends = %v", log)
	}
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s2.jsonl", 0))
	h.b.Flush(context.Background())
	if log := h.appendLog(); len(log) != 1 {
		t.Errorf("an empty batch was appended: %v", log)
	}
}

func TestMirrorCoalescesPerFilePreservingOrder(t *testing.T) {
	h := newMirrorHarness(FlushBatched)
	h.b.enqueue(mirrorFrame("/cfg/projects/p/b.jsonl", 1))
	h.b.enqueue(mirrorFrame("/cfg/projects/p/a.jsonl", 2))
	h.b.enqueue(mirrorFrame("/cfg/projects/p/b.jsonl", 3))
	h.b.enqueue(mirrorFrame("/cfg/projects/p/b/subagents/agent-x.jsonl", 1))
	h.b.Flush(context.Background())
	if log := strings.Join(h.appendLog(), ","); log != "b/:4,a/:2,b/subagents/agent-x:1" {
		t.Errorf("appends = %s", log)
	}
}

func TestMirrorEagerFlushThresholds(t *testing.T) {
	waitAppends := func(h *mirrorHarness, n int) []string {
		for i := 0; i < 200 && len(h.appendLog()) < n; i++ {
			time.Sleep(5 * time.Millisecond)
		}
		return h.appendLog()
	}

	h := newMirrorHarness(FlushBatched)
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", mirrorMaxPendingEntries))
	time.Sleep(20 * time.Millisecond)
	if log := h.appendLog(); len(log) != 0 {
		t.Errorf("flushed at exactly the entry threshold: %v", log)
	}
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 1))
	if log := waitAppends(h, 1); len(log) != 1 {
		t.Errorf("no eager flush past the entry threshold: %v", log)
	}

	h = newMirrorHarness(FlushBatched)
	big := json.RawMessage(`{"type":"user","text":"` + strings.Repeat("x", mirrorMaxPendingBytes) + `"}`)
	b, _ := json.Marshal(transcriptMirrorFrame{FilePath: "/cfg/projects/p/s.jsonl", Entries: []json.RawMessage{big}})
	h.b.enqueue(b)
	if log := waitAppends(h, 1); len(log) != 1 {
		t.Errorf("no eager flush past the byte threshold: %v", log)
	}

	h = newMirrorHarness(FlushEager)
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 1))
	if log := waitAppends(h, 1); len(log) != 1 {
		t.Errorf("eager mode did not flush the frame: %v", log)
	}
}

func TestMirrorRetries(t *testing.T) {
	fastMirrorRetries(t)
	h := newMirrorHarness(FlushBatched)
	h.store.failures = 2
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 1))
	h.b.Flush(context.Background())
	if len(h.appendLog()) != 1 || len(h.errs) != 0 || h.store.attempts.Load() != 3 {
		t.Errorf("retry then success: appends %v, errors %v, attempts %d", h.appendLog(), h.errs, h.store.attempts.Load())
	}

	h = newMirrorHarness(FlushBatched)
	h.store.failures = 10
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 1))
	h.b.Flush(context.Background())
	if h.store.attempts.Load() != mirrorAppendMaxAttempts || len(h.errs) != 1 {
		t.Fatalf("exhausted: attempts %d, errors %v", h.store.attempts.Load(), h.errs)
	}
	if e := h.errs[0]; e.Key == nil || e.Key.SessionID != "s" || e.Error != "backend down" {
		t.Errorf("error message = %+v", e)
	}
}

func TestMirrorTimeoutIsNotRetried(t *testing.T) {
	prev := mirrorSendTimeout
	mirrorSendTimeout = 20 * time.Millisecond
	t.Cleanup(func() { mirrorSendTimeout = prev })
	h := newMirrorHarness(FlushBatched)
	h.store.block = time.Second
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 1))
	h.b.Flush(context.Background())
	if h.store.attempts.Load() != 1 || len(h.errs) != 1 {
		t.Errorf("attempts %d, errors %v", h.store.attempts.Load(), h.errs)
	}
}

func TestMirrorUnmappedPathDropped(t *testing.T) {
	h := newMirrorHarness(FlushBatched)
	h.b.enqueue(mirrorFrame("/elsewhere/p/s.jsonl", 1))
	h.b.Flush(context.Background())
	if len(h.appendLog()) != 0 || len(h.errs) != 0 {
		t.Errorf("appends %v, errors %v", h.appendLog(), h.errs)
	}
	if len(h.warns) != 1 || !strings.Contains(h.warns[0], "dropping mirror frame") {
		t.Errorf("warnings = %v", h.warns)
	}
}

func TestMirrorFlushWaitsForInFlightEagerFlush(t *testing.T) {
	h := newMirrorHarness(FlushEager)
	h.store.block = 50 * time.Millisecond
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 1))
	time.Sleep(5 * time.Millisecond)
	h.b.enqueue(mirrorFrame("/cfg/projects/p/s.jsonl", 2))
	h.b.Flush(context.Background())
	for i := 0; i < 100 && len(h.appendLog()) < 2; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	total := 0
	for _, a := range h.appendLog() {
		var n int
		_, _ = fmt.Sscanf(a[strings.LastIndex(a, ":")+1:], "%d", &n)
		total += n
	}
	if total != 3 {
		t.Errorf("appends %v, want 3 entries total, none lost or duplicated", h.appendLog())
	}
}
