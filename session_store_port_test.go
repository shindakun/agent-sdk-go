package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Ports of upstream tests/test_session_helpers_store.py and
// tests/test_session_summary.py.

// --- helpers --------------------------------------------------------------------

func ssEntry(t *testing.T, v any) SessionStoreEntry {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return SessionStoreEntry{Data: b}
}

func ssEntries(t *testing.T, vs ...map[string]any) []SessionStoreEntry {
	t.Helper()
	out := make([]SessionStoreEntry, len(vs))
	for i, v := range vs {
		out[i] = ssEntry(t, v)
	}
	return out
}

func ssUser(text, uid string, parent any, sid string) map[string]any {
	return map[string]any{
		"type":       "user",
		"uuid":       uid,
		"parentUuid": parent,
		"sessionId":  sid,
		"timestamp":  "2024-01-01T00:00:00.000Z",
		"message":    map[string]any{"role": "user", "content": text},
	}
}

func ssAssistant(text, uid string, parent any, sid string) map[string]any {
	return map[string]any{
		"type":       "assistant",
		"uuid":       uid,
		"parentUuid": parent,
		"sessionId":  sid,
		"timestamp":  "2024-01-01T00:00:01.000Z",
		"message":    map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}},
	}
}

// ssSumUser mirrors test_session_summary.py's _user(text, ts, **extra).
func ssSumUser(text any, ts string, extra map[string]any) map[string]any {
	if ts == "" {
		ts = "2024-01-01T00:00:00.000Z"
	}
	m := map[string]any{
		"type":      "user",
		"timestamp": ts,
		"message":   map[string]any{"role": "user", "content": text},
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

type ssFixture struct {
	dir string
	pk  string
}

func ssNewFixture(t *testing.T) ssFixture {
	dir := t.TempDir()
	return ssFixture{dir: dir, pk: ProjectKeyForDirectory(dir)}
}

func (f ssFixture) key(sid string) SessionKey { return SessionKey{ProjectKey: f.pk, SessionID: sid} }

// ssSeedChain appends n user/assistant pairs and returns their UUIDs in order.
func ssSeedChain(t *testing.T, store SessionStore, f ssFixture, sid string, n int) []string {
	t.Helper()
	var uuids []string
	var parent any
	var entries []map[string]any
	for i := 0; i < n; i++ {
		u, a := newUUID(), newUUID()
		entries = append(entries, ssUser(fmt.Sprintf("prompt %d", i), u, parent, sid))
		entries = append(entries, ssAssistant(fmt.Sprintf("reply %d", i), a, u, sid))
		uuids = append(uuids, u, a)
		parent = a
	}
	if err := store.Append(context.Background(), f.key(sid), ssEntries(t, entries...)); err != nil {
		t.Fatal(err)
	}
	return uuids
}

func ssGetEntries(t *testing.T, store SessionStore, key SessionKey) []map[string]any {
	t.Helper()
	loaded, err := store.Load(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]any, len(loaded))
	for i, e := range loaded {
		if err := json.Unmarshal(e.Data, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func ssIDs(infos []SDKSessionInfo) map[string]bool {
	m := map[string]bool{}
	for _, s := range infos {
		m[s.SessionID] = true
	}
	return m
}

func ssSet(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// ssMinimalStore implements only Append and Load.
type ssMinimalStore struct {
	mu   sync.Mutex
	data map[string][]SessionStoreEntry
}

func ssNewMinimalStore() *ssMinimalStore {
	return &ssMinimalStore{data: map[string][]SessionStoreEntry{}}
}

func (s *ssMinimalStore) k(key SessionKey) string {
	return key.ProjectKey + "/" + key.SessionID + "/" + key.Subpath
}

func (s *ssMinimalStore) Append(_ context.Context, key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.k(key)] = append(s.data[s.k(key)], entries...)
	return nil
}

func (s *ssMinimalStore) Load(_ context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[s.k(key)], nil
}

func (s *ssMinimalStore) ListSessions(context.Context, string) ([]SessionStoreListEntry, error) {
	return nil, errors.ErrUnsupported
}

func (s *ssMinimalStore) ListSessionSummaries(context.Context, string) ([]SessionSummaryEntry, error) {
	return nil, errors.ErrUnsupported
}

func (s *ssMinimalStore) ListSubkeys(context.Context, SessionListSubkeysKey) ([]string, error) {
	return nil, errors.ErrUnsupported
}

func (s *ssMinimalStore) Delete(context.Context, SessionKey) error { return errors.ErrUnsupported }

// ssHookStore wraps an InMemorySessionStore with optional method overrides.
type ssHookStore struct {
	*InMemorySessionStore
	appendFn        func(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error
	loadFn          func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error)
	listSessionsFn  func(ctx context.Context, pk string) ([]SessionStoreListEntry, error)
	listSummariesFn func(ctx context.Context, pk string) ([]SessionSummaryEntry, error)
}

func ssNewHookStore() *ssHookStore {
	return &ssHookStore{InMemorySessionStore: NewInMemorySessionStore()}
}

func (s *ssHookStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	if s.appendFn != nil {
		return s.appendFn(ctx, key, entries)
	}
	return s.InMemorySessionStore.Append(ctx, key, entries)
}

func (s *ssHookStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	if s.loadFn != nil {
		return s.loadFn(ctx, key)
	}
	return s.InMemorySessionStore.Load(ctx, key)
}

func (s *ssHookStore) ListSessions(ctx context.Context, pk string) ([]SessionStoreListEntry, error) {
	if s.listSessionsFn != nil {
		return s.listSessionsFn(ctx, pk)
	}
	return s.InMemorySessionStore.ListSessions(ctx, pk)
}

func (s *ssHookStore) ListSessionSummaries(ctx context.Context, pk string) ([]SessionSummaryEntry, error) {
	if s.listSummariesFn != nil {
		return s.listSummariesFn(ctx, pk)
	}
	return s.InMemorySessionStore.ListSessionSummaries(ctx, pk)
}

func ssNoSummaries(context.Context, string) ([]SessionSummaryEntry, error) {
	return nil, errors.ErrUnsupported
}

// ssRefStore returns its internal listing slice verbatim.
type ssRefStore struct{ internal []SessionStoreListEntry }

func (s *ssRefStore) Append(context.Context, SessionKey, []SessionStoreEntry) error { return nil }
func (s *ssRefStore) Load(context.Context, SessionKey) ([]SessionStoreEntry, error) {
	return nil, nil
}
func (s *ssRefStore) ListSessions(context.Context, string) ([]SessionStoreListEntry, error) {
	return s.internal, nil
}
func (s *ssRefStore) ListSessionSummaries(context.Context, string) ([]SessionSummaryEntry, error) {
	return nil, errors.ErrUnsupported
}
func (s *ssRefStore) ListSubkeys(context.Context, SessionListSubkeysKey) ([]string, error) {
	return nil, errors.ErrUnsupported
}
func (s *ssRefStore) Delete(context.Context, SessionKey) error { return errors.ErrUnsupported }

// ssRecorder records Load session ids concurrently.
type ssRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *ssRecorder) add(sid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sid)
}

func (r *ssRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// ssRunBoundedLoadTest drives a store whose Load blocks until released and
// checks the peak in-flight Load count equals storeListLoadConcurrency.
func ssRunBoundedLoadTest(t *testing.T, store *ssHookStore, f ssFixture, n int, entryFor func(i int) map[string]any, summaries func(context.Context, string) ([]SessionSummaryEntry, error)) []SDKSessionInfo {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := store.InMemorySessionStore.Append(ctx, f.key(newUUID()), ssEntries(t, entryFor(i))); err != nil {
			t.Fatal(err)
		}
	}
	var inFlight, peak atomic.Int64
	gate := make(chan struct{})
	store.listSummariesFn = summaries
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		cur := inFlight.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		<-gate
		inFlight.Add(-1)
		return store.InMemorySessionStore.Load(ctx, key)
	}
	type res struct {
		infos []SDKSessionInfo
		err   error
	}
	done := make(chan res, 1)
	go func() {
		infos, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
		done <- res{infos, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < storeListLoadConcurrency && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	peakAtSaturation := peak.Load()
	close(gate)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if peakAtSaturation <= 0 || peakAtSaturation > storeListLoadConcurrency {
		t.Fatalf("peak at saturation = %d, want in (0, %d]", peakAtSaturation, storeListLoadConcurrency)
	}
	if peak.Load() != storeListLoadConcurrency {
		t.Fatalf("peak = %d, want %d", peak.Load(), storeListLoadConcurrency)
	}
	return r.infos
}

// --- test_session_helpers_store.py: TestListSessionsFromStore -------------------

func TestSSListSessionsFromStore_SortedByMtime(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sidA, sidB := newUUID(), newUUID()
	ssSeedChain(t, store, f, sidA, 2)
	ssSeedChain(t, store, f, sidB, 2)

	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ssIDs(sessions); !reflect.DeepEqual(got, ssSet(sidA, sidB)) {
		t.Fatalf("ids = %v", got)
	}
	for _, s := range sessions {
		if s.Summary != "prompt 0" || s.FirstPrompt != "prompt 0" {
			t.Fatalf("summary=%q first_prompt=%q", s.Summary, s.FirstPrompt)
		}
	}
	if !sort.SliceIsSorted(sessions, func(i, j int) bool { return sessions[i].LastModified > sessions[j].LastModified }) {
		t.Fatalf("not sorted by mtime desc: %+v", sessions)
	}
}

func TestSSListSessionsFromStore_LimitAndOffset(t *testing.T) {
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	for i := 0; i < 3; i++ {
		ssSeedChain(t, store, f, newUUID(), 2)
	}
	page, err := ListSessionsFromStore(context.Background(), store, f.dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("len = %d", len(page))
	}
}

func TestSSListSessionsFromStore_RaisesWhenStoreLacksListSessions(t *testing.T) {
	f := ssNewFixture(t)
	_, err := ListSessionsFromStore(context.Background(), ssNewMinimalStore(), f.dir, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "ListSessions") {
		t.Fatalf("err = %v", err)
	}
}

func TestSSListSessionsFromStore_DropsSidechainSessions(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	normal, side := newUUID(), newUUID()
	if err := store.Append(ctx, f.key(normal), ssEntries(t, ssUser("hello world", newUUID(), nil, normal))); err != nil {
		t.Fatal(err)
	}
	se := ssUser("internal", newUUID(), nil, side)
	se["isSidechain"] = true
	if err := store.Append(ctx, f.key(side), ssEntries(t, se)); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := ssIDs(sessions)
	if !ids[normal] || ids[side] {
		t.Fatalf("ids = %v", ids)
	}
	for _, s := range sessions {
		if s.Summary == "" {
			t.Fatalf("empty summary row: %+v", s)
		}
	}
}

func TestSSListSessionsFromStore_LimitOffsetAfterSidechainFilter(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewHookStore()
	store.listSummariesFn = ssNoSummaries
	var valid []string
	for i := 0; i < 5; i++ {
		sid := newUUID()
		ssSeedChain(t, store, f, sid, 1)
		valid = append(valid, sid)
	}
	for i := 0; i < 3; i++ {
		sc := newUUID()
		e := ssUser("sidechain", newUUID(), nil, sc)
		e["isSidechain"] = true
		if err := store.Append(ctx, f.key(sc), ssEntries(t, e)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListSessionsFromStore(ctx, store, f.dir, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 5 || !reflect.DeepEqual(ssIDs(page), ssSet(valid...)) {
		t.Fatalf("page = %+v", page)
	}
	page2, err := ListSessionsFromStore(ctx, store, f.dir, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 3 {
		t.Fatalf("len(page2) = %d", len(page2))
	}
	vs := ssSet(valid...)
	for id := range ssIDs(page2) {
		if !vs[id] {
			t.Fatalf("unexpected id %s", id)
		}
	}
}

func TestSSListSessionsFromStore_DoesNotMutateAdapterList(t *testing.T) {
	f := ssNewFixture(t)
	store := &ssRefStore{internal: []SessionStoreListEntry{{SessionID: "a", Mtime: 1}, {SessionID: "b", Mtime: 2}}}
	if _, err := ListSessionsFromStore(context.Background(), store, f.dir, 0, 0); err != nil {
		t.Fatal(err)
	}
	if store.internal[0].SessionID != "a" || store.internal[1].SessionID != "b" {
		t.Fatalf("internal mutated: %+v", store.internal)
	}
}

func TestSSListSessionsFromStore_AdapterLoadErrorDegradesRow(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewHookStore()
	good, bad := newUUID(), newUUID()
	store.listSummariesFn = ssNoSummaries
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		if key.SessionID == bad {
			return nil, errors.New("backend down")
		}
		return store.InMemorySessionStore.Load(ctx, key)
	}
	ssSeedChain(t, store, f, good, 2)
	ssSeedChain(t, store, f, bad, 2)
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SDKSessionInfo{}
	for _, s := range sessions {
		byID[s.SessionID] = s
	}
	if byID[good].Summary != "prompt 0" {
		t.Fatalf("good = %+v", byID[good])
	}
	if b, ok := byID[bad]; !ok || b.Summary != "" {
		t.Fatalf("bad = %+v ok=%v", b, ok)
	}
}

func TestSSListSessionsFromStore_LoadConcurrencyIsBounded(t *testing.T) {
	f := ssNewFixture(t)
	store := ssNewHookStore()
	n := storeListLoadConcurrency * 3
	result := ssRunBoundedLoadTest(t, store, f, n,
		func(i int) map[string]any { return map[string]any{"type": "user", "uuid": fmt.Sprintf("u%d", i)} },
		ssNoSummaries)
	if len(result) > n {
		t.Fatalf("len = %d", len(result))
	}
}

// --- TestGetSessionInfoFromStore -------------------------------------------------

func TestSSGetSessionInfoFromStore_Seeded(t *testing.T) {
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	info, err := GetSessionInfoFromStore(context.Background(), store, sid, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.SessionID != sid || info.Summary != "prompt 0" || info.CreatedAt == 0 {
		t.Fatalf("info = %+v", info)
	}
}

func TestSSGetSessionInfoFromStore_Unknown(t *testing.T) {
	f := ssNewFixture(t)
	_, err := GetSessionInfoFromStore(context.Background(), NewInMemorySessionStore(), newUUID(), f.dir)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSSGetSessionInfoFromStore_ReflectsCustomTitle(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if err := RenameSessionViaStore(ctx, store, sid, "My Title", f.dir); err != nil {
		t.Fatal(err)
	}
	info, err := GetSessionInfoFromStore(ctx, store, sid, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.CustomTitle != "My Title" || info.Summary != "My Title" {
		t.Fatalf("info = %+v", info)
	}
}

func TestSSGetSessionInfoFromStore_CwdFallsBackToDirectory(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	canonical := canonicalizePath(f.dir)
	info, err := GetSessionInfoFromStore(ctx, store, sid, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Cwd != canonical {
		t.Fatalf("cwd = %q, want %q", info.Cwd, canonical)
	}
	listed, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) == 0 || listed[0].Cwd != canonical {
		t.Fatalf("listed = %+v", listed)
	}
}

// --- TestGetSessionMessagesFromStore --------------------------------------------

func TestSSGetSessionMessagesFromStore_ChainInOrder(t *testing.T) {
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	uuids := ssSeedChain(t, store, f, sid, 2)
	msgs, err := GetSessionMessagesFromStore(context.Background(), store, sid, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("len = %d", len(msgs))
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.UUID)
	}
	if !reflect.DeepEqual(got, uuids) {
		t.Fatalf("uuids = %v, want %v", got, uuids)
	}
	if msgs[0].Type != "user" || msgs[1].Type != "assistant" {
		t.Fatalf("types = %q %q", msgs[0].Type, msgs[1].Type)
	}
}

func TestSSGetSessionMessagesFromStore_IgnoresMetadataEntries(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 1)
	if err := RenameSessionViaStore(ctx, store, sid, "Title", f.dir); err != nil {
		t.Fatal(err)
	}
	if err := TagSessionViaStore(ctx, store, sid, "exp", f.dir); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSessionMessagesFromStore(ctx, store, sid, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d", len(msgs))
	}
}

func TestSSGetSessionMessagesFromStore_LimitOffset(t *testing.T) {
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 3)
	msgs, err := GetSessionMessagesFromStore(context.Background(), store, sid, f.dir, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len = %d", len(msgs))
	}
}

func TestSSGetSessionMessagesFromStore_UnknownEmpty(t *testing.T) {
	f := ssNewFixture(t)
	msgs, err := GetSessionMessagesFromStore(context.Background(), NewInMemorySessionStore(), newUUID(), f.dir, 0, 0)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("msgs = %v err = %v", msgs, err)
	}
}

// --- TestSubagentsFromStore ------------------------------------------------------

func TestSSSubagents_ListAndGetMessages(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	subKey := SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: "subagents/agent-abc123"}
	u, a := newUUID(), newUUID()
	if err := store.Append(ctx, subKey, ssEntries(t, ssUser("sub prompt", u, nil, sid), ssAssistant("sub reply", a, u, sid))); err != nil {
		t.Fatal(err)
	}
	ids, err := ListSubagentsFromStore(ctx, store, sid, f.dir)
	if err != nil || !reflect.DeepEqual(ids, []string{"abc123"}) {
		t.Fatalf("ids = %v err = %v", ids, err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, sid, "abc123", f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Type != "user" || msgs[1].Type != "assistant" {
		t.Fatalf("msgs = %+v", msgs)
	}
}

func TestSSSubagents_NestedWorkflowSubpath(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	subKey := SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: "subagents/workflows/run-1/agent-nested"}
	if err := store.Append(ctx, subKey, ssEntries(t, ssUser("hi", newUUID(), nil, sid))); err != nil {
		t.Fatal(err)
	}
	ids, err := ListSubagentsFromStore(ctx, store, sid, f.dir)
	if err != nil || !reflect.DeepEqual(ids, []string{"nested"}) {
		t.Fatalf("ids = %v err = %v", ids, err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, sid, "nested", f.dir, 0, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs = %+v err = %v", msgs, err)
	}
}

func TestSSSubagents_FiltersAgentMetadataEntries(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	subKey := SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: "subagents/agent-x"}
	if err := store.Append(ctx, subKey, ssEntries(t,
		map[string]any{"type": "agent_metadata", "name": "x"},
		ssUser("hi", newUUID(), nil, sid))); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, sid, "x", f.dir, 0, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs = %+v err = %v", msgs, err)
	}
	if msgs[0].ParentToolUseID != "" || msgs[0].ParentAgentID != "" {
		t.Fatalf("parent ids = %q %q", msgs[0].ParentToolUseID, msgs[0].ParentAgentID)
	}
}

func TestSSSubagents_ParentIDsFromAgentMetadataEntry(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	subKey := SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: "subagents/agent-x"}
	u, a := newUUID(), newUUID()
	if err := store.Append(ctx, subKey, ssEntries(t,
		map[string]any{"type": "agent_metadata", "agentType": "gp", "toolUseId": "toolu_old"},
		ssUser("hi", u, nil, sid),
		ssAssistant("hello", a, u, sid),
		map[string]any{"type": "agent_metadata", "agentType": "gp", "toolUseId": "toolu_new", "parentAgentId": "a-parent"},
	)); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, sid, "x", f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range msgs {
		got = append(got, m.UUID)
		if m.ParentToolUseID != "toolu_new" || m.ParentAgentID != "a-parent" {
			t.Fatalf("parent ids = %q %q", m.ParentToolUseID, m.ParentAgentID)
		}
	}
	if !reflect.DeepEqual(got, []string{u, a}) {
		t.Fatalf("uuids = %v", got)
	}
}

func TestSSSubagents_ParentIDsIgnoreNonStringMetadata(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	subKey := SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: "subagents/agent-x"}
	if err := store.Append(ctx, subKey, ssEntries(t,
		map[string]any{"type": "agent_metadata", "toolUseId": 7, "parentAgentId": nil},
		ssUser("hi", newUUID(), nil, sid))); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, sid, "x", f.dir, 0, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs = %+v err = %v", msgs, err)
	}
	if msgs[0].ParentToolUseID != "" || msgs[0].ParentAgentID != "" {
		t.Fatalf("parent ids = %q %q", msgs[0].ParentToolUseID, msgs[0].ParentAgentID)
	}
}

func TestSSSubagents_SessionMessagesParentIDsEmpty(t *testing.T) {
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 1)
	msgs, err := GetSessionMessagesFromStore(context.Background(), store, sid, f.dir, 0, 0)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("msgs = %+v err = %v", msgs, err)
	}
	for _, m := range msgs {
		if m.ParentToolUseID != "" || m.ParentAgentID != "" {
			t.Fatalf("parent ids = %q %q", m.ParentToolUseID, m.ParentAgentID)
		}
	}
}

func TestSSSubagents_ListDedupesAgentIDAcrossSubpaths(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid, u := newUUID(), newUUID()
	for _, sp := range []string{"subagents/agent-abc", "subagents/workflows/run-1/agent-abc"} {
		if err := store.Append(ctx, SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: sp}, ssEntries(t, ssUser("x", u, nil, sid))); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := ListSubagentsFromStore(ctx, store, sid, f.dir)
	if err != nil || !reflect.DeepEqual(ids, []string{"abc"}) {
		t.Fatalf("ids = %v err = %v", ids, err)
	}
}

func TestSSSubagents_NonUUIDSessionID(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	ids, err := ListSubagentsFromStore(ctx, store, "not-a-uuid", f.dir)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ids = %v err = %v", ids, err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, "not-a-uuid", "x", f.dir, 0, 0)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("msgs = %v err = %v", msgs, err)
	}
}

func TestSSSubagents_ListRaisesWhenStoreLacksListSubkeys(t *testing.T) {
	f := ssNewFixture(t)
	_, err := ListSubagentsFromStore(context.Background(), ssNewMinimalStore(), newUUID(), f.dir)
	if err == nil || !strings.Contains(err.Error(), "does not support ListSubkeys") {
		t.Fatalf("err = %v", err)
	}
}

func TestSSSubagents_GetDirectPathWithoutListSubkeys(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewMinimalStore()
	sid := newUUID()
	if err := store.Append(ctx, SessionKey{ProjectKey: f.pk, SessionID: sid, Subpath: "subagents/agent-direct"}, ssEntries(t, ssUser("hi", newUUID(), nil, sid))); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSubagentMessagesFromStore(ctx, store, sid, "direct", f.dir, 0, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("msgs = %+v err = %v", msgs, err)
	}
}

// --- TestRenameSessionViaStore / TestTagSessionViaStore --------------------------

func TestSSRenameViaStore_AppendsCustomTitleEntry(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if err := RenameSessionViaStore(ctx, store, sid, "  New Title  ", f.dir); err != nil {
		t.Fatal(err)
	}
	entries := ssGetEntries(t, store, f.key(sid))
	last := entries[len(entries)-1]
	if last["type"] != "custom-title" || last["customTitle"] != "New Title" || last["sessionId"] != sid {
		t.Fatalf("last = %v", last)
	}
	if _, ok := last["uuid"].(string); !ok {
		t.Fatalf("uuid = %v", last["uuid"])
	}
	if _, ok := last["timestamp"].(string); !ok {
		t.Fatalf("timestamp = %v", last["timestamp"])
	}
}

func TestSSRenameViaStore_InvalidInputs(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	if err := RenameSessionViaStore(ctx, store, "not-a-uuid", "t", ""); err == nil {
		t.Fatal("expected error for non-uuid")
	}
	if err := RenameSessionViaStore(ctx, store, newUUID(), "  ", ""); err == nil {
		t.Fatal("expected error for blank title")
	}
}

func TestSSTagViaStore_AppendsTagEntry(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if err := TagSessionViaStore(ctx, store, sid, "experiment", f.dir); err != nil {
		t.Fatal(err)
	}
	entries := ssGetEntries(t, store, f.key(sid))
	last := entries[len(entries)-1]
	if last["type"] != "tag" || last["tag"] != "experiment" || last["sessionId"] != sid {
		t.Fatalf("last = %v", last)
	}
}

func TestSSTagViaStore_EmptyClearsTag(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if err := TagSessionViaStore(ctx, store, sid, "", f.dir); err != nil {
		t.Fatal(err)
	}
	entries := ssGetEntries(t, store, f.key(sid))
	last := entries[len(entries)-1]
	if last["type"] != "tag" || last["tag"] != "" {
		t.Fatalf("last = %v", last)
	}
}

func TestSSTagViaStore_ReflectedInSessionInfo(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if err := TagSessionViaStore(ctx, store, sid, "exp", f.dir); err != nil {
		t.Fatal(err)
	}
	info, err := GetSessionInfoFromStore(ctx, store, sid, f.dir)
	if err != nil || info.Tag != "exp" {
		t.Fatalf("info = %+v err = %v", info, err)
	}
}

func TestSSTagViaStore_SurvivesAdapterKeyReordering(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewHookStore()
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		entries, err := store.InMemorySessionStore.Load(ctx, key)
		if err != nil || entries == nil {
			return entries, err
		}
		out := make([]SessionStoreEntry, len(entries))
		for i, e := range entries {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(e.Data, &m); err != nil {
				return nil, err
			}
			b, _ := json.Marshal(m) // keys sorted alphabetically
			out[i] = SessionStoreEntry{Data: b}
		}
		return out, nil
	}
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if err := TagSessionViaStore(ctx, store, sid, "exp", f.dir); err != nil {
		t.Fatal(err)
	}
	info, err := GetSessionInfoFromStore(ctx, store, sid, f.dir)
	if err != nil || info.Tag != "exp" {
		t.Fatalf("info = %+v err = %v", info, err)
	}
}

// --- TestDeleteSessionViaStore ---------------------------------------------------

func TestSSDeleteViaStore_RemovesSession(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	size := func() int {
		store.mu.Lock()
		defer store.mu.Unlock()
		return len(store.entries)
	}
	if size() != 1 {
		t.Fatalf("size = %d", size())
	}
	if err := DeleteSessionViaStore(ctx, store, sid, f.dir); err != nil {
		t.Fatal(err)
	}
	if size() != 0 {
		t.Fatalf("size = %d", size())
	}
	if got, err := store.Load(ctx, f.key(sid)); err != nil || got != nil {
		t.Fatalf("load = %v err = %v", got, err)
	}
}

func TestSSDeleteViaStore_NoopWhenStoreLacksDelete(t *testing.T) {
	f := ssNewFixture(t)
	if err := DeleteSessionViaStore(context.Background(), ssNewMinimalStore(), newUUID(), f.dir); err != nil {
		t.Fatal(err)
	}
}

func TestSSDeleteViaStore_RejectsNonUUID(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewHookStore()
	var appended atomic.Bool
	store.appendFn = func(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
		appended.Store(true)
		return store.InMemorySessionStore.Append(ctx, key, entries)
	}
	if err := DeleteSessionViaStore(ctx, store, "not-a-uuid", f.dir); err == nil || !strings.Contains(err.Error(), "not-a-uuid") {
		t.Fatalf("delete err = %v", err)
	}
	if err := TagSessionViaStore(ctx, store, "not-a-uuid", "tag", f.dir); err == nil || !strings.Contains(err.Error(), "not-a-uuid") {
		t.Fatalf("tag err = %v", err)
	}
	if appended.Load() {
		t.Fatal("store was appended to")
	}
}

// --- TestForkSessionViaStore -----------------------------------------------------

func ssMsgEntries(entries []map[string]any) []map[string]any {
	var out []map[string]any
	for _, e := range entries {
		if e["type"] == "user" || e["type"] == "assistant" {
			out = append(out, e)
		}
	}
	return out
}

func ssNonEmptyString(v any) bool { s, ok := v.(string); return ok && s != "" }

func TestSSForkViaStore_RoundTripsWithNewUUIDs(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	src := ssSet(ssSeedChain(t, store, f, sid, 2)...)
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID == sid {
		t.Fatal("fork kept the source session id")
	}
	forked := ssGetEntries(t, store, f.key(result.SessionID))
	msgs := ssMsgEntries(forked)
	if len(msgs) != 4 {
		t.Fatalf("len = %d", len(msgs))
	}
	for _, e := range msgs {
		if e["sessionId"] != result.SessionID {
			t.Fatalf("sessionId = %v", e["sessionId"])
		}
		if src[e["uuid"].(string)] {
			t.Fatalf("uuid %v not remapped", e["uuid"])
		}
		if e["forkedFrom"].(map[string]any)["sessionId"] != sid {
			t.Fatalf("forkedFrom = %v", e["forkedFrom"])
		}
	}
	if msgs[0]["parentUuid"] != nil {
		t.Fatalf("first parentUuid = %v", msgs[0]["parentUuid"])
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i]["parentUuid"] != msgs[i-1]["uuid"] {
			t.Fatalf("chain broken at %d", i)
		}
	}
	trailer := forked[len(forked)-1]
	if trailer["type"] != "custom-title" || !ssNonEmptyString(trailer["uuid"]) || !ssNonEmptyString(trailer["timestamp"]) {
		t.Fatalf("trailer = %v", trailer)
	}
}

func TestSSForkViaStore_TitleFromOriginalCustomTitle(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 1)
	if err := store.Append(ctx, f.key(sid), ssEntries(t, map[string]any{"type": "custom-title", "customTitle": "My Title", "sessionId": sid})); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	forked := ssGetEntries(t, store, f.key(result.SessionID))
	last := forked[len(forked)-1]
	if last["type"] != "custom-title" || last["customTitle"] != "My Title (fork)" {
		t.Fatalf("last = %v", last)
	}
}

func TestSSForkViaStore_TitleFromAITitle(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 1)
	if err := store.Append(ctx, f.key(sid), ssEntries(t, map[string]any{"type": "ai-title", "aiTitle": "Generated", "sessionId": sid})); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	forked := ssGetEntries(t, store, f.key(result.SessionID))
	if got := forked[len(forked)-1]["customTitle"]; got != "Generated (fork)" {
		t.Fatalf("customTitle = %v", got)
	}
}

func TestSSForkViaStore_ContentReplacementHasUUIDAndTimestamp(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 1)
	if err := store.Append(ctx, f.key(sid), ssEntries(t, map[string]any{
		"type":         "content-replacement",
		"sessionId":    sid,
		"replacements": []any{map[string]any{"toolUseId": "t1", "value": "redacted"}},
	})); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var cr map[string]any
	for _, e := range ssGetEntries(t, store, f.key(result.SessionID)) {
		if e["type"] == "content-replacement" {
			cr = e
			break
		}
	}
	if cr == nil {
		t.Fatal("no content-replacement entry")
	}
	if cr["sessionId"] != result.SessionID || !ssNonEmptyString(cr["uuid"]) || !ssNonEmptyString(cr["timestamp"]) {
		t.Fatalf("cr = %v", cr)
	}
}

func TestSSForkViaStore_ReadableViaGetSessionMessages(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, "", "Forked")
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSessionMessagesFromStore(ctx, store, result.SessionID, f.dir, 0, 0)
	if err != nil || len(msgs) != 4 {
		t.Fatalf("len = %d err = %v", len(msgs), err)
	}
}

func TestSSForkViaStore_UpToMessageID(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	uuids := ssSeedChain(t, store, f, sid, 3)
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, uuids[1], "")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(ssMsgEntries(ssGetEntries(t, store, f.key(result.SessionID)))); got != 2 {
		t.Fatalf("len = %d", got)
	}
}

func TestSSForkViaStore_NotFound(t *testing.T) {
	f := ssNewFixture(t)
	_, err := ForkSessionViaStore(context.Background(), NewInMemorySessionStore(), newUUID(), f.dir, "", "")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSSForkViaStore_RejectsNonUUIDSessionAndUpTo(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	if _, err := ForkSessionViaStore(ctx, store, "not-a-uuid", f.dir, "", ""); err == nil || !strings.Contains(err.Error(), "invalid session id") {
		t.Fatalf("err = %v", err)
	}
	sid := newUUID()
	ssSeedChain(t, store, f, sid, 2)
	if _, err := ForkSessionViaStore(ctx, store, sid, f.dir, "not-a-uuid", ""); err == nil || !strings.Contains(err.Error(), "invalid up-to message id") {
		t.Fatalf("err = %v", err)
	}
}

func TestSSForkViaStore_PreservesChainAndStampsSyntheticEntries(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	u1 := ssUser("one", newUUID(), nil, sid)
	a1 := ssAssistant("two", newUUID(), u1["uuid"], sid)
	u2 := ssUser("three", newUUID(), a1["uuid"], sid)
	cr := map[string]any{
		"type":         "content-replacement",
		"sessionId":    sid,
		"replacements": []any{map[string]any{"toolUseId": "tu_1", "newContent": "x"}},
	}
	if err := store.Append(ctx, f.key(sid), ssEntries(t, u1, a1, u2, cr)); err != nil {
		t.Fatal(err)
	}
	result, err := ForkSessionViaStore(ctx, store, sid, f.dir, a1["uuid"].(string), "My Fork")
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionID == sid {
		t.Fatal("fork kept the source session id")
	}
	forked := ssGetEntries(t, store, f.key(result.SessionID))
	if len(forked) != 4 {
		t.Fatalf("len = %d: %v", len(forked), forked)
	}
	f0, f1, crOut, title := forked[0], forked[1], forked[2], forked[3]
	if f0["uuid"] == u1["uuid"] || f0["parentUuid"] != nil || f1["parentUuid"] != f0["uuid"] || f0["sessionId"] != result.SessionID {
		t.Fatalf("f0 = %v f1 = %v", f0, f1)
	}
	if f0["forkedFrom"].(map[string]any)["messageUuid"] != u1["uuid"] {
		t.Fatalf("forkedFrom = %v", f0["forkedFrom"])
	}
	if title["type"] != "custom-title" || title["customTitle"] != "My Fork" || !ssNonEmptyString(title["uuid"]) {
		t.Fatalf("title = %v", title)
	}
	if _, ok := title["timestamp"].(string); !ok {
		t.Fatalf("title timestamp = %v", title["timestamp"])
	}
	if crOut["type"] != "content-replacement" || crOut["sessionId"] != result.SessionID || !ssNonEmptyString(crOut["uuid"]) {
		t.Fatalf("cr = %v", crOut)
	}
	if _, ok := crOut["timestamp"].(string); !ok {
		t.Fatalf("cr timestamp = %v", crOut["timestamp"])
	}
}

// --- test_session_summary.py: TestFoldSessionSummary -----------------------------

var ssSumKey = SessionKey{ProjectKey: "p", SessionID: "11111111-1111-4111-8111-111111111111"}

func TestSSFold_InitFromNil(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, nil)
	want := SessionSummaryEntry{SessionID: ssSumKey.SessionID, Mtime: 0, Data: map[string]any{}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("s = %+v", s)
	}
}

func TestSSFold_SetOnceFieldsFreeze(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:00.000Z", "cwd": "/a", "isSidechain": false},
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:05.000Z", "cwd": "/b"},
	))
	check := func(s SessionSummaryEntry) {
		t.Helper()
		if s.Data["created_at"] != int64(1704067200000) || s.Data["cwd"] != "/a" || s.Data["is_sidechain"] != false {
			t.Fatalf("data = %v", s.Data)
		}
	}
	check(s)
	s2 := FoldSessionSummary(&s, ssSumKey, ssEntries(t,
		map[string]any{"type": "x", "timestamp": "2024-01-02T00:00:00.000Z", "cwd": "/c", "isSidechain": true}))
	check(s2)
}

func TestSSFold_LastWinsOverwrite(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:00Z", "customTitle": "t1", "gitBranch": "main"},
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:01Z", "customTitle": "t2"},
	))
	if s.Data["custom_title"] != "t2" || s.Data["git_branch"] != "main" {
		t.Fatalf("data = %v", s.Data)
	}
	s2 := FoldSessionSummary(&s, ssSumKey, ssEntries(t,
		map[string]any{"type": "x", "aiTitle": "ai", "lastPrompt": "lp", "summary": "sm", "gitBranch": "dev"}))
	want := map[string]any{"custom_title": "t2", "ai_title": "ai", "last_prompt": "lp", "summary_hint": "sm", "git_branch": "dev"}
	for k, v := range want {
		if s2.Data[k] != v {
			t.Fatalf("%s = %v, want %v", k, s2.Data[k], v)
		}
	}
}

func TestSSFold_MtimeNotDerivedFromEntries(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:05.000Z"},
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:01.000Z"},
	))
	if s.Mtime != 0 {
		t.Fatalf("mtime = %d", s.Mtime)
	}
	prev := SessionSummaryEntry{SessionID: ssSumKey.SessionID, Mtime: 42, Data: map[string]any{}}
	s2 := FoldSessionSummary(&prev, ssSumKey, ssEntries(t, map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:10.000Z"}))
	if s2.Mtime != 42 {
		t.Fatalf("mtime = %d", s2.Mtime)
	}
}

func TestSSFold_TagSetAndClear(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t, map[string]any{"type": "tag", "tag": "wip"}))
	if s.Data["tag"] != "wip" {
		t.Fatalf("tag = %v", s.Data["tag"])
	}
	s2 := FoldSessionSummary(&s, ssSumKey, ssEntries(t, map[string]any{"type": "tag", "tag": ""}))
	if _, ok := s2.Data["tag"]; ok {
		t.Fatalf("tag not cleared: %v", s2.Data)
	}
	s3 := FoldSessionSummary(&s, ssSumKey, ssEntries(t, map[string]any{"type": "user", "tag": "ignored"}))
	if s3.Data["tag"] != "wip" {
		t.Fatalf("tag = %v", s3.Data["tag"])
	}
}

func TestSSFold_SidechainFromFirstEntry(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:00Z", "isSidechain": true}))
	if s.Data["is_sidechain"] != true {
		t.Fatalf("data = %v", s.Data)
	}
}

func TestSSFold_SidechainLatchedWhenFirstEntryLacksTimestamp(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		map[string]any{"type": "user", "isSidechain": true},
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:00Z"},
	))
	if s.Data["is_sidechain"] != true || s.Data["created_at"] != int64(1704067200000) {
		t.Fatalf("data = %v", s.Data)
	}
}

func TestSSFold_FirstPromptSkipsMetaToolResultAndCompact(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		ssSumUser("ignored meta", "", map[string]any{"isMeta": true}),
		ssSumUser("ignored compact", "", map[string]any{"isCompactSummary": true}),
		ssSumUser([]any{map[string]any{"type": "tool_result", "tool_use_id": "x", "content": "res"}}, "", nil),
		ssSumUser("real first", "", nil),
		ssSumUser("not me", "", nil),
	))
	if s.Data["first_prompt"] != "real first" || s.Data["first_prompt_locked"] != true {
		t.Fatalf("data = %v", s.Data)
	}
}

func TestSSFold_FirstPromptCommandFallback(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		ssSumUser("<command-name>/init</command-name> stuff", "", nil),
		ssSumUser("<command-name>/second</command-name>", "", nil),
	))
	if s.Data["first_prompt_locked"] == true || s.Data["command_fallback"] != "/init" {
		t.Fatalf("data = %v", s.Data)
	}
	s2 := FoldSessionSummary(&s, ssSumKey, ssEntries(t, ssSumUser("now real", "", nil)))
	if s2.Data["first_prompt"] != "now real" || s2.Data["first_prompt_locked"] != true {
		t.Fatalf("data = %v", s2.Data)
	}
}

func TestSSFold_FirstPromptSkipPattern(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t,
		ssSumUser("<local-command-stdout> some output", "", nil), ssSumUser("hello", "", nil)))
	if s.Data["first_prompt"] != "hello" {
		t.Fatalf("data = %v", s.Data)
	}
}

func TestSSFold_FirstPromptTruncated(t *testing.T) {
	s := FoldSessionSummary(nil, ssSumKey, ssEntries(t, ssSumUser(strings.Repeat("x", 300), "", nil)))
	fp, _ := s.Data["first_prompt"].(string)
	if len([]rune(fp)) > 201 || !strings.HasSuffix(fp, "…") {
		t.Fatalf("first_prompt = %q", fp)
	}
}

func TestSSFold_PrevIsNotMutated(t *testing.T) {
	prev := SessionSummaryEntry{SessionID: "a", Mtime: 5, Data: map[string]any{}}
	FoldSessionSummary(&prev, ssSumKey, ssEntries(t, map[string]any{"type": "x", "customTitle": "t"}))
	if !reflect.DeepEqual(prev, SessionSummaryEntry{SessionID: "a", Mtime: 5, Data: map[string]any{}}) {
		t.Fatalf("prev = %+v", prev)
	}
}

// --- TestSummaryEntryToSdkInfo ---------------------------------------------------

func TestSSSummaryToInfo_SidechainReturnsFalse(t *testing.T) {
	if _, ok := summaryEntryToSDKInfo(SessionSummaryEntry{SessionID: "s", Mtime: 1, Data: map[string]any{"is_sidechain": true, "custom_title": "t"}}, ""); ok {
		t.Fatal("want ok=false")
	}
}

func TestSSSummaryToInfo_EmptySummaryReturnsFalse(t *testing.T) {
	if _, ok := summaryEntryToSDKInfo(SessionSummaryEntry{SessionID: "s", Mtime: 1, Data: map[string]any{}}, ""); ok {
		t.Fatal("want ok=false")
	}
}

func TestSSSummaryToInfo_PrecedenceChain(t *testing.T) {
	data := map[string]any{
		"first_prompt":        "fp",
		"first_prompt_locked": true,
		"command_fallback":    "/cmd",
		"summary_hint":        "sh",
		"last_prompt":         "lp",
		"ai_title":            "ai",
		"custom_title":        "ct",
	}
	base := SessionSummaryEntry{SessionID: "s", Mtime: 1, Data: data}
	conv := func() SDKSessionInfo {
		t.Helper()
		info, ok := summaryEntryToSDKInfo(base, "")
		if !ok {
			t.Fatal("ok = false")
		}
		return info
	}
	if i := conv(); i.Summary != "ct" || i.CustomTitle != "ct" {
		t.Fatalf("info = %+v", i)
	}
	delete(data, "custom_title")
	if i := conv(); i.Summary != "ai" || i.CustomTitle != "ai" {
		t.Fatalf("info = %+v", i)
	}
	delete(data, "ai_title")
	if i := conv(); i.Summary != "lp" || i.CustomTitle != "" {
		t.Fatalf("info = %+v", i)
	}
	delete(data, "last_prompt")
	if i := conv(); i.Summary != "sh" {
		t.Fatalf("info = %+v", i)
	}
	delete(data, "summary_hint")
	if i := conv(); i.Summary != "fp" || i.FirstPrompt != "fp" {
		t.Fatalf("info = %+v", i)
	}
	data["first_prompt_locked"] = false
	if i := conv(); i.Summary != "/cmd" || i.FirstPrompt != "/cmd" {
		t.Fatalf("info = %+v", i)
	}
}

func TestSSSummaryToInfo_CwdFallbackToProjectPath(t *testing.T) {
	info, ok := summaryEntryToSDKInfo(SessionSummaryEntry{SessionID: "s", Mtime: 1, Data: map[string]any{"custom_title": "t"}}, "/proj")
	if !ok || info.Cwd != "/proj" {
		t.Fatalf("info = %+v ok = %v", info, ok)
	}
	info2, ok := summaryEntryToSDKInfo(SessionSummaryEntry{SessionID: "s", Mtime: 1, Data: map[string]any{"custom_title": "t", "cwd": "/own"}}, "/proj")
	if !ok || info2.Cwd != "/own" {
		t.Fatalf("info = %+v ok = %v", info2, ok)
	}
}

func TestSSSummaryToInfo_FieldPassthrough(t *testing.T) {
	info, ok := summaryEntryToSDKInfo(SessionSummaryEntry{SessionID: "s", Mtime: 99, Data: map[string]any{
		"custom_title": "t", "git_branch": "main", "tag": "wip", "created_at": int64(50),
	}}, "")
	if !ok {
		t.Fatal("ok = false")
	}
	if info.SessionID != "s" || info.LastModified != 99 || info.GitBranch != "main" || info.Tag != "wip" || info.CreatedAt != 50 || info.FileSize != 0 {
		t.Fatalf("info = %+v", info)
	}
}

// --- TestInMemoryListSessionSummaries --------------------------------------------

func ssSummariesByID(t *testing.T, store SessionStore, pk string) map[string]SessionSummaryEntry {
	t.Helper()
	sums, err := store.ListSessionSummaries(context.Background(), pk)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]SessionSummaryEntry{}
	for _, s := range sums {
		m[s.SessionID] = s
	}
	return m
}

func TestSSInMemorySummaries_TracksAppends(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	a, b := f.key("a"), f.key("b")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Append(ctx, a, ssEntries(t, ssSumUser("hello a", "2024-01-01T00:00:00Z", nil))))
	must(store.Append(ctx, a, ssEntries(t, map[string]any{"type": "x", "customTitle": "Title A"})))
	must(store.Append(ctx, b, ssEntries(t, ssSumUser("hello b", "2024-01-02T00:00:00Z", nil))))
	sums := ssSummariesByID(t, store, f.pk)
	if len(sums) != 2 {
		t.Fatalf("sums = %v", sums)
	}
	if sums["a"].Data["custom_title"] != "Title A" || sums["a"].Data["first_prompt"] != "hello a" || sums["b"].Data["first_prompt"] != "hello b" {
		t.Fatalf("sums = %v", sums)
	}
}

func TestSSInMemorySummaries_SubpathAppendsIgnored(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	if err := store.Append(ctx, f.key("m"), ssEntries(t, ssSumUser("main prompt", "", nil))); err != nil {
		t.Fatal(err)
	}
	sub := SessionKey{ProjectKey: f.pk, SessionID: "m", Subpath: "subagents/agent-1"}
	if err := store.Append(ctx, sub, ssEntries(t, ssSumUser("sub prompt", "", nil), map[string]any{"type": "x", "customTitle": "sub"})); err != nil {
		t.Fatal(err)
	}
	sums, err := store.ListSessionSummaries(ctx, f.pk)
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Data["first_prompt"] != "main prompt" {
		t.Fatalf("sums = %v", sums)
	}
	if _, ok := sums[0].Data["custom_title"]; ok {
		t.Fatalf("custom_title leaked: %v", sums[0].Data)
	}
}

func TestSSInMemorySummaries_DeleteDropsSummary(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	k := f.key("x")
	if err := store.Append(ctx, k, ssEntries(t, ssSumUser("hi", "", nil))); err != nil {
		t.Fatal(err)
	}
	if n := len(ssSummariesByID(t, store, f.pk)); n != 1 {
		t.Fatalf("n = %d", n)
	}
	if err := store.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if n := len(ssSummariesByID(t, store, f.pk)); n != 0 {
		t.Fatalf("n = %d", n)
	}
}

func TestSSInMemorySummaries_ProjectIsolation(t *testing.T) {
	ctx := context.Background()
	store := NewInMemorySessionStore()
	if err := store.Append(ctx, SessionKey{ProjectKey: "A", SessionID: "s"}, ssEntries(t, ssSumUser("a", "", nil))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, SessionKey{ProjectKey: "B", SessionID: "s"}, ssEntries(t, ssSumUser("b", "", nil))); err != nil {
		t.Fatal(err)
	}
	for pk, want := range map[string]int{"A": 1, "B": 1, "C": 0} {
		if n := len(ssSummariesByID(t, store, pk)); n != want {
			t.Fatalf("%s: n = %d, want %d", pk, n, want)
		}
	}
}

// --- TestListSessionsFromStoreFastPath -------------------------------------------

func TestSSFastPath_SkipsLoad(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewHookStore()
	sidA, sidB := newUUID(), newUUID()
	if err := store.Append(ctx, f.key(sidA), ssEntries(t, ssSumUser("first a", "2024-01-01T00:00:00Z", map[string]any{"cwd": f.dir}))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, f.key(sidB), ssEntries(t, ssSumUser("first b", "2024-01-02T00:00:00Z", map[string]any{"cwd": f.dir}))); err != nil {
		t.Fatal(err)
	}
	store.loadFn = func(context.Context, SessionKey) ([]SessionStoreEntry, error) {
		t.Error("Load must not be called on the fast path")
		return nil, errors.New("boom")
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ssIDs(sessions), ssSet(sidA, sidB)) {
		t.Fatalf("sessions = %+v", sessions)
	}
	if sessions[0].SessionID != sidB || sessions[0].Summary != "first b" || sessions[1].FirstPrompt != "first a" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestSSFastPath_FiltersSidechainAndEmpty(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	main, side, empty := newUUID(), newUUID(), newUUID()
	for sid, e := range map[string]map[string]any{
		main:  ssSumUser("hello", "2024-01-01T00:00:00Z", nil),
		side:  {"type": "user", "timestamp": "2024-01-01T00:00:00Z", "isSidechain": true, "message": map[string]any{"content": "x"}},
		empty: {"type": "x", "timestamp": "2024-01-01T00:00:00Z"},
	} {
		if err := store.Append(ctx, f.key(sid), ssEntries(t, e)); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ssIDs(sessions), ssSet(main)) {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestSSFastPath_LimitOffset(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	var sids []string
	for i := 0; i < 5; i++ {
		sid := newUUID()
		sids = append(sids, sid)
		if err := store.Append(ctx, f.key(sid), ssEntries(t, ssSumUser(fmt.Sprintf("p%d", i), fmt.Sprintf("2024-01-0%dT00:00:00Z", i+1), nil))); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ListSessionsFromStore(ctx, store, f.dir, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].SessionID != sids[3] || page[1].SessionID != sids[2] {
		t.Fatalf("page = %+v", page)
	}
}

func TestSSFastPath_UnsupportedFallsBackToLoad(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := ssNewHookStore()
	store.listSummariesFn = ssNoSummaries
	sid := newUUID()
	if err := store.Append(ctx, f.key(sid), ssEntries(t, ssSumUser("hi", "2024-01-01T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Summary != "hi" {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestSSFastPath_MixedSessionsGapFilled(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	sidWith, sidWithout := newUUID(), newUUID()
	store := ssNewHookStore()
	rec := &ssRecorder{}
	store.listSummariesFn = func(ctx context.Context, pk string) ([]SessionSummaryEntry, error) {
		full, err := store.InMemorySessionStore.ListSessionSummaries(ctx, pk)
		var out []SessionSummaryEntry
		for _, s := range full {
			if s.SessionID == sidWith {
				out = append(out, s)
			}
		}
		return out, err
	}
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		rec.add(key.SessionID)
		return store.InMemorySessionStore.Load(ctx, key)
	}
	if err := store.Append(ctx, f.key(sidWith), ssEntries(t, ssSumUser("has sidecar", "2024-01-02T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, f.key(sidWithout), ssEntries(t, ssSumUser("no sidecar", "2024-01-01T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]SDKSessionInfo{}
	for _, s := range sessions {
		byID[s.SessionID] = s
	}
	if len(byID) != 2 || byID[sidWith].Summary != "has sidecar" || byID[sidWithout].Summary != "no sidecar" {
		t.Fatalf("sessions = %+v", sessions)
	}
	if got := rec.get(); !reflect.DeepEqual(got, []string{sidWithout}) {
		t.Fatalf("load calls = %v", got)
	}
	if sessions[0].SessionID != sidWithout {
		t.Fatalf("sessions[0] = %s, want %s", sessions[0].SessionID, sidWithout)
	}
}

func TestSSFastPath_GapFillLoadBoundedByLimit(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	sidWith := newUUID()
	store := ssNewHookStore()
	rec := &ssRecorder{}
	store.listSummariesFn = func(ctx context.Context, pk string) ([]SessionSummaryEntry, error) {
		full, err := store.InMemorySessionStore.ListSessionSummaries(ctx, pk)
		var out []SessionSummaryEntry
		for _, s := range full {
			if s.SessionID == sidWith {
				out = append(out, s)
			}
		}
		return out, err
	}
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		rec.add(key.SessionID)
		return store.InMemorySessionStore.Load(ctx, key)
	}
	for i := 0; i < 5; i++ {
		if err := store.Append(ctx, f.key(newUUID()), ssEntries(t, ssSumUser(fmt.Sprintf("without %d", i), fmt.Sprintf("2024-01-0%dT00:00:00Z", i+1), nil))); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Append(ctx, f.key(sidWith), ssEntries(t, ssSumUser("with", "2024-01-10T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	page, err := ListSessionsFromStore(ctx, store, f.dir, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].SessionID != sidWith {
		t.Fatalf("page = %+v", page)
	}
	if n := len(rec.get()); n > 2 || n != 1 {
		t.Fatalf("load calls = %d", n)
	}
}

func TestSSFastPath_SidechainSummaryDoesNotConsumePageSlot(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sids := []string{newUUID(), newUUID(), newUUID()}
	if err := store.Append(ctx, f.key(sids[2]), ssEntries(t, ssSumUser("real 2", "2024-01-01T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, f.key(sids[1]), ssEntries(t, ssSumUser("real 1", "2024-01-02T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, f.key(sids[0]), ssEntries(t, map[string]any{
		"type": "user", "timestamp": "2024-01-03T00:00:00Z", "isSidechain": true, "message": map[string]any{"content": "x"},
	})); err != nil {
		t.Fatal(err)
	}
	page, err := ListSessionsFromStore(ctx, store, f.dir, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].SessionID != sids[1] || page[1].SessionID != sids[2] {
		t.Fatalf("page = %+v", page)
	}
}

func TestSSFastPath_StaleSidecarTriggersGapFill(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	sid := newUUID()
	const staleMtime int64 = 1_704_067_260_000
	const freshMtime int64 = 1_704_153_660_000
	store := ssNewHookStore()
	rec := &ssRecorder{}
	store.listSummariesFn = func(_ context.Context, pk string) ([]SessionSummaryEntry, error) {
		if pk != f.pk {
			return nil, nil
		}
		return []SessionSummaryEntry{{SessionID: sid, Mtime: staleMtime, Data: map[string]any{
			"custom_title":        "old",
			"first_prompt":        "old prompt",
			"first_prompt_locked": true,
			"created_at":          staleMtime,
		}}}, nil
	}
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		rec.add(key.SessionID)
		return store.InMemorySessionStore.Load(ctx, key)
	}
	if err := store.Append(ctx, f.key(sid), ssEntries(t,
		ssSumUser("fresh prompt", "2024-01-02T00:00:00Z", nil),
		map[string]any{"type": "x", "timestamp": "2024-01-02T00:01:00Z", "customTitle": "fresh"},
	)); err != nil {
		t.Fatal(err)
	}
	listed, err := store.InMemorySessionStore.ListSessions(ctx, f.pk)
	if err != nil || listed[0].Mtime <= staleMtime {
		t.Fatalf("listed = %+v err = %v", listed, err)
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	info := sessions[0]
	if info.SessionID != sid || info.CustomTitle != "fresh" || info.Summary != "fresh" || info.LastModified < freshMtime {
		t.Fatalf("info = %+v", info)
	}
	if got := rec.get(); !reflect.DeepEqual(got, []string{sid}) {
		t.Fatalf("load calls = %v", got)
	}
}

func TestSSFastPath_FreshSidecarWithStorageNewerMtimeNotGapFilled(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	sid := newUUID()
	const t1 int64 = 1_704_067_200_000
	const t2 int64 = 1_704_067_200_250
	store := ssNewHookStore()
	rec := &ssRecorder{}
	store.listSessionsFn = func(ctx context.Context, pk string) ([]SessionStoreListEntry, error) {
		full, err := store.InMemorySessionStore.ListSessions(ctx, pk)
		out := make([]SessionStoreListEntry, len(full))
		for i, e := range full {
			out[i] = SessionStoreListEntry{SessionID: e.SessionID, Mtime: t2}
		}
		return out, err
	}
	store.listSummariesFn = func(ctx context.Context, pk string) ([]SessionSummaryEntry, error) {
		full, err := store.InMemorySessionStore.ListSessionSummaries(ctx, pk)
		out := make([]SessionSummaryEntry, len(full))
		for i, s := range full {
			out[i] = SessionSummaryEntry{SessionID: s.SessionID, Mtime: t2, Data: s.Data}
		}
		return out, err
	}
	store.loadFn = func(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
		rec.add(key.SessionID)
		return store.InMemorySessionStore.Load(ctx, key)
	}
	if err := store.Append(ctx, f.key(sid), ssEntries(t,
		ssSumUser("fresh prompt", "2024-01-01T00:00:00.000Z", nil),
		map[string]any{"type": "x", "timestamp": "2024-01-01T00:00:00.000Z", "customTitle": "fresh"},
	)); err != nil {
		t.Fatal(err)
	}
	listed, _ := store.ListSessions(ctx, f.pk)
	summ, _ := store.ListSessionSummaries(ctx, f.pk)
	if listed[0].Mtime != t2 || summ[0].Mtime != t2 || t2 <= t1 {
		t.Fatalf("setup: listed = %+v summ = %+v", listed, summ)
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != sid || sessions[0].Summary != "fresh" || sessions[0].LastModified != t2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if got := rec.get(); len(got) != 0 {
		t.Fatalf("load calls = %v", got)
	}
}

func TestSSFastPath_SummaryWithoutListingIsDropped(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	real, ghost := newUUID(), newUUID()
	store := ssNewHookStore()
	store.listSessionsFn = func(ctx context.Context, pk string) ([]SessionStoreListEntry, error) {
		full, err := store.InMemorySessionStore.ListSessions(ctx, pk)
		var out []SessionStoreListEntry
		for _, e := range full {
			if e.SessionID != ghost {
				out = append(out, e)
			}
		}
		return out, err
	}
	if err := store.Append(ctx, f.key(real), ssEntries(t, ssSumUser("real", "2024-01-02T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, f.key(ghost), ssEntries(t, ssSumUser("ghost", "2024-01-01T00:00:00Z", nil))); err != nil {
		t.Fatal(err)
	}
	sessions, err := ListSessionsFromStore(ctx, store, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ssIDs(sessions), ssSet(real)) {
		t.Fatalf("sessions = %+v", sessions)
	}
}

func TestSSFastPath_GapFillBoundedConcurrency(t *testing.T) {
	f := ssNewFixture(t)
	store := ssNewHookStore()
	ssRunBoundedLoadTest(t, store, f, storeListLoadConcurrency*2,
		func(i int) map[string]any { return ssSumUser(fmt.Sprintf("p%d", i), "", nil) },
		func(context.Context, string) ([]SessionSummaryEntry, error) { return []SessionSummaryEntry{}, nil })
}

// --- TestParityWithLiteParse -----------------------------------------------------

func ssParityCheck(t *testing.T, sid, cwd string, entries []map[string]any, split int) {
	t.Helper()
	k := SessionKey{ProjectKey: "p", SessionID: sid}
	all := ssEntries(t, entries...)
	folded := FoldSessionSummary(nil, k, all[:split])
	if split < len(all) {
		folded = FoldSessionSummary(&folded, k, all[split:])
	}
	incremental, ok1 := summaryEntryToSDKInfo(folded, cwd)
	batch, ok2 := parseSessionInfoFromLite(sid, jsonlToLite(entriesToJSONL(all), folded.Mtime), cwd)
	if !ok1 || !ok2 {
		t.Fatalf("ok incremental=%v batch=%v", ok1, ok2)
	}
	batch.FileSize = 0
	if !reflect.DeepEqual(incremental, batch) {
		t.Fatalf("incremental = %+v\nbatch       = %+v", incremental, batch)
	}
}

func TestSSParity_IncrementalEqualsBatch(t *testing.T) {
	ssParityCheck(t, "22222222-2222-4222-8222-222222222222", "/work", []map[string]any{
		ssSumUser("<command-name>/clear</command-name>", "2024-01-01T00:00:00.000Z", map[string]any{"cwd": "/work", "gitBranch": "main"}),
		ssSumUser("ignored", "2024-01-01T00:00:01.000Z", map[string]any{"isMeta": true}),
		ssSumUser("real prompt here", "2024-01-01T00:00:02.000Z", nil),
		{"type": "assistant", "timestamp": "2024-01-01T00:00:03.000Z", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}},
		{"type": "x", "timestamp": "2024-01-01T00:00:04.000Z", "aiTitle": "AI Named"},
		{"type": "tag", "timestamp": "2024-01-01T00:00:05.000Z", "tag": "wip"},
		{"type": "x", "timestamp": "2024-01-01T00:00:06.000Z", "customTitle": "User Named", "gitBranch": "feature"},
	}, 3)
}

func TestSSParity_FirstPromptOnly(t *testing.T) {
	ssParityCheck(t, "33333333-3333-4333-8333-333333333333", "/w", []map[string]any{
		ssSumUser("just a prompt", "2024-02-01T00:00:00.000Z", map[string]any{"cwd": "/w"}),
	}, 1)
}
