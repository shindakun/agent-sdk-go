package claude

import (
	"context"
	"encoding/json"
	"testing"
)

func entry(s string) SessionStoreEntry { return SessionStoreEntry{Data: json.RawMessage(s)} }

func TestInMemorySessionStore(t *testing.T) {
	ctx := context.Background()
	st := NewInMemorySessionStore()
	pk := "proj"

	k1 := SessionKey{ProjectKey: pk, SessionID: "s1"}
	if err := st.Append(ctx, k1, []SessionStoreEntry{
		entry(`{"type":"user","message":{"content":"hello"}}`),
		entry(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`),
	}); err != nil {
		t.Fatal(err)
	}
	k2 := SessionKey{ProjectKey: pk, SessionID: "s2"}
	if err := st.Append(ctx, k2, []SessionStoreEntry{entry(`{"type":"user","message":{"content":"yo"}}`)}); err != nil {
		t.Fatal(err)
	}

	// Load
	got, err := st.Load(ctx, k1)
	if err != nil || len(got) != 2 {
		t.Fatalf("load: %v len=%d", err, len(got))
	}

	// List (newest first by mtime; s2 appended last)
	list, err := st.ListSessions(ctx, pk)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].SessionID != "s2" {
		t.Errorf("list = %+v", list)
	}

	// Summaries
	sums, err := st.ListSessionSummaries(ctx, pk)
	if err != nil {
		t.Fatal(err)
	}
	var s1Summary string
	for _, s := range sums {
		if s.SessionID == "s1" {
			s1Summary = s.Summary
		}
	}
	if s1Summary != "hello" {
		t.Errorf("s1 summary = %q", s1Summary)
	}

	// Delete
	if err := DeleteSessionViaStore(ctx, st, k1); err != nil {
		t.Fatal(err)
	}
	list, _ = st.ListSessions(ctx, pk)
	if len(list) != 1 || list[0].SessionID != "s2" {
		t.Errorf("after delete list = %+v", list)
	}
}

func TestSessionMutationsViaStore(t *testing.T) {
	ctx := context.Background()
	st := NewInMemorySessionStore()
	k := SessionKey{ProjectKey: "proj", SessionID: "s1"}
	_ = st.Append(ctx, k, []SessionStoreEntry{entry(`{"type":"user","message":{"content":"go"}}`)})

	if err := RenameSessionViaStore(ctx, st, k, "My Title"); err != nil {
		t.Fatal(err)
	}
	if err := TagSessionViaStore(ctx, st, k, "important"); err != nil {
		t.Fatal(err)
	}

	res, err := ForkSessionViaStore(ctx, st, k, "s1-fork")
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != "s1-fork" {
		t.Errorf("fork id = %q", res.SessionID)
	}
	forked, _ := st.Load(ctx, SessionKey{ProjectKey: "proj", SessionID: "s1-fork"})
	// original had 1 message + rename + tag entries = 3 entries copied
	if len(forked) != 3 {
		t.Errorf("forked entries = %d, want 3", len(forked))
	}
}

func TestProjectKeyForDirectory(t *testing.T) {
	if ProjectKeyForDirectory("/a/b") != "-a-b" {
		t.Errorf("project key = %q", ProjectKeyForDirectory("/a/b"))
	}
}
