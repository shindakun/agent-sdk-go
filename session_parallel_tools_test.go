package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Ports the parallel-tool-call tests from upstream beffc50 (tests/test_sessions.py
// and tests/test_session_helpers_store.py).

// ptEntry builds a transcript entry from key/value pairs; values are
// JSON-encoded.
func ptEntry(t *testing.T, kv ...any) transcriptEntry {
	t.Helper()
	e := transcriptEntry{}
	for i := 0; i+1 < len(kv); i += 2 {
		b, err := json.Marshal(kv[i+1])
		if err != nil {
			t.Fatal(err)
		}
		e[kv[i].(string)] = b
	}
	return e
}

// ptClone copies an entry so a test can change its fields.
func ptClone(e transcriptEntry) transcriptEntry {
	c := make(transcriptEntry, len(e))
	for k, v := range e {
		c[k] = v
	}
	return c
}

// ptParallelTurn mirrors _parallel_tool_turn: one assistant response making
// `tools` parallel calls, as the CLI writes it. Each call is its own assistant
// entry sharing message.id "msg_parallel", chained one after another; each
// result's parentUuid is its own call. resultOrder is the order the results
// are written in. Both slices are in file order.
func ptParallelTurn(t *testing.T, sid, parent string, tools int, resultOrder []int) (calls, results []transcriptEntry) {
	t.Helper()
	for i := 0; i < tools; i++ {
		p := parent
		if len(calls) > 0 {
			p = calls[len(calls)-1].str("uuid")
		}
		calls = append(calls, ptEntry(t,
			"type", "assistant", "uuid", fmt.Sprintf("call-%d", i), "parentUuid", p, "sessionId", sid,
			"message", map[string]any{
				"role": "assistant", "id": "msg_parallel",
				"content": []any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i), "name": "Read", "input": map[string]any{}}},
			},
			"timestamp", fmt.Sprintf("2026-01-01T00:00:01.%03dZ", i),
		))
	}
	for pos, i := range resultOrder {
		results = append(results, ptEntry(t,
			"type", "user", "uuid", fmt.Sprintf("result-%d", i), "parentUuid", fmt.Sprintf("call-%d", i), "sessionId", sid,
			"message", map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "tool_result", "tool_use_id": fmt.Sprintf("toolu_%d", i), "content": "ok"}},
			},
			"sourceToolAssistantUUID", fmt.Sprintf("call-%d", i),
			"timestamp", fmt.Sprintf("2026-01-01T00:00:02.%03dZ", pos),
		))
	}
	return calls, results
}

// ptMsg builds a plain user/assistant entry; parent "" is null.
func ptMsg(t *testing.T, typ, id, parent, sid string, content any) transcriptEntry {
	t.Helper()
	var p any
	if parent != "" {
		p = parent
	}
	e := ptEntry(t, "type", typ, "uuid", id, "parentUuid", p, "sessionId", sid)
	if content != nil {
		e["message"], _ = json.Marshal(map[string]any{"role": typ, "content": content})
	}
	return e
}

func ptJoin(groups ...[]transcriptEntry) []transcriptEntry {
	var out []transcriptEntry
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func ptChainUUIDs(entries []transcriptEntry) []string {
	var ids []string
	for _, e := range buildConversationChain(entries) {
		ids = append(ids, e.str("uuid"))
	}
	return ids
}

func ptWriteTranscript(t *testing.T, path string, entries []transcriptEntry) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ptBlockIDs mirrors _block_ids: field of every blockType content block, in order.
func ptBlockIDs(t *testing.T, msgs []SessionMessage, blockType, field string) []string {
	t.Helper()
	var ids []string
	for _, m := range msgs {
		var msg struct {
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(m.Message, &msg); err != nil {
			t.Fatal(err)
		}
		var blocks []map[string]any
		if json.Unmarshal(msg.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b["type"] == blockType {
				ids = append(ids, b[field].(string))
			}
		}
	}
	return ids
}

func TestGetSessionMessagesParallelToolCalls(t *testing.T) {
	t.Run("keep_every_call_and_result", func(t *testing.T) {
		for _, order := range [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}, {0, 2, 1}} {
			t.Run(fmt.Sprint(order), func(t *testing.T) {
				cfg, tmp := spConfigDir(t)
				projectPath, projectDir := spProject(t, cfg, tmp, "proj")
				sid := spUUID(t)
				calls, results := ptParallelTurn(t, sid, "prompt", 3, order)
				entries := ptJoin(
					[]transcriptEntry{ptMsg(t, "user", "prompt", "", sid, "read them")},
					calls, results,
					// The next response continues from the last result written.
					[]transcriptEntry{ptMsg(t, "assistant", "answer", results[len(results)-1].str("uuid"), sid, "done")},
				)
				ptWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"), entries)

				msgs := spMsgs(t, sid, projectPath, 0, 0)

				spEqualStrings(t, ptBlockIDs(t, msgs, "tool_use", "id"), []string{"toolu_0", "toolu_1", "toolu_2"})
				resultIDs := ptBlockIDs(t, msgs, "tool_result", "tool_use_id")
				slices.Sort(resultIDs)
				spEqualStrings(t, resultIDs, []string{"toolu_0", "toolu_1", "toolu_2"})
				uuids := spUUIDs(msgs)
				if len(msgs) != 8 || len(slices.Compact(slices.Sorted(slices.Values(uuids)))) != 8 {
					t.Fatalf("got %d messages (%v), want 8 distinct", len(msgs), uuids)
				}
				// The turn stays in one piece: prompt, calls, results, answer.
				var types []string
				for _, m := range msgs[1:7] {
					types = append(types, m.Type)
				}
				spEqualStrings(t, types, []string{"assistant", "assistant", "assistant", "user", "user", "user"})
				if msgs[0].UUID != "prompt" || msgs[7].UUID != "answer" {
					t.Errorf("uuids = %v", uuids)
				}
			})
		}
	})
	t.Run("as_last_turn", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		calls, results := ptParallelTurn(t, sid, "prompt", 2, []int{1, 0})
		ptWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"),
			ptJoin([]transcriptEntry{ptMsg(t, "user", "prompt", "", sid, "read them")}, calls, results))
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 0, 0)),
			[]string{"prompt", "call-0", "call-1", "result-1", "result-0"})
	})
	t.Run("limit_offset", func(t *testing.T) {
		cfg, tmp := spConfigDir(t)
		projectPath, projectDir := spProject(t, cfg, tmp, "proj")
		sid := spUUID(t)
		calls, results := ptParallelTurn(t, sid, "prompt", 3, []int{0, 1, 2})
		ptWriteTranscript(t, filepath.Join(projectDir, sid+".jsonl"), ptJoin(
			[]transcriptEntry{ptMsg(t, "user", "prompt", "", sid, "read them")},
			calls, results,
			[]transcriptEntry{ptMsg(t, "assistant", "answer", "result-2", sid, "k")},
		))
		// Paging counts the recovered calls and results.
		spEqualStrings(t, spUUIDs(spMsgs(t, sid, projectPath, 3, 4)),
			[]string{"result-0", "result-1", "result-2"})
	})
}

func TestBuildConversationChainParallelToolCalls(t *testing.T) {
	prompt := func() transcriptEntry { return ptEntry(t, "type", "user", "uuid", "prompt", "parentUuid", nil) }
	answer := func(parent string) transcriptEntry {
		return ptEntry(t, "type", "assistant", "uuid", "answer", "parentUuid", parent)
	}
	one := func(e transcriptEntry) []transcriptEntry { return []transcriptEntry{e} }

	t.Run("recovers_sibling_calls_dropped_by_the_walk", func(t *testing.T) {
		calls, results := ptParallelTurn(t, "s", "prompt", 3, []int{1, 2, 0})
		// The off-branch calls and their results go right after the last call
		// on the branch, ahead of that branch's own result.
		spEqualStrings(t, ptChainUUIDs(ptJoin(one(prompt()), calls, results, one(answer("result-0")))),
			[]string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer"})
	})
	t.Run("recovered_results_are_in_completion_order", func(t *testing.T) {
		// The second call finished first.
		calls, results := ptParallelTurn(t, "s", "prompt", 3, []int{1, 0, 2})
		spEqualStrings(t, ptChainUUIDs(ptJoin(one(prompt()), calls, results, one(answer("result-2")))),
			[]string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-0", "result-2", "answer"})
	})
	t.Run("recovered_calls_are_in_timestamp_order", func(t *testing.T) {
		calls, results := ptParallelTurn(t, "s", "prompt", 3, []int{1, 2, 0})
		// call-2 reached the file before call-1.
		got := ptChainUUIDs(ptJoin(one(prompt()), []transcriptEntry{calls[0], calls[2], calls[1]}, results, one(answer("result-0"))))
		spEqualStrings(t, got[1:4], []string{"call-0", "call-1", "call-2"})
	})
	t.Run("sequential_tool_calls_are_unchanged", func(t *testing.T) {
		entries := []transcriptEntry{
			prompt(),
			ptEntry(t, "type", "assistant", "uuid", "call-0", "parentUuid", "prompt",
				"message", map[string]any{"id": "msg_1", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_0"}}}),
			ptEntry(t, "type", "user", "uuid", "result-0", "parentUuid", "call-0",
				"message", map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_0"}}}),
			ptEntry(t, "type", "assistant", "uuid", "call-1", "parentUuid", "result-0",
				"message", map[string]any{"id": "msg_1", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_1"}}}),
			ptEntry(t, "type", "user", "uuid", "result-1", "parentUuid", "call-1",
				"message", map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1"}}}),
		}
		spEqualStrings(t, ptChainUUIDs(entries), []string{"prompt", "call-0", "result-0", "call-1", "result-1"})
	})
	t.Run("abandoned_branch_is_not_recovered", func(t *testing.T) {
		// Only entries that share a response id with the chain are recovered;
		// an earlier attempt at the same turn is a different response.
		calls, results := ptParallelTurn(t, "s", "prompt", 2, []int{0, 1})
		old := ptEntry(t, "type", "assistant", "uuid", "old-call", "parentUuid", "prompt",
			"message", map[string]any{"id": "msg_abandoned", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_old"}}})
		oldResult := ptEntry(t, "type", "user", "uuid", "old-result", "parentUuid", "old-call",
			"message", map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_old"}}})
		spEqualStrings(t, ptChainUUIDs(ptJoin(one(prompt()), []transcriptEntry{old, oldResult}, calls, results)),
			[]string{"prompt", "call-0", "call-1", "result-0", "result-1"})
	})
	t.Run("recovers_result_whose_parent_is_outside_the_turn", func(t *testing.T) {
		// Matched to its call by tool_use_id.
		calls, results := ptParallelTurn(t, "s", "prompt", 2, []int{0, 1})
		results[0]["parentUuid"] = json.RawMessage(`"missing"`)
		delete(results[0], "sourceToolAssistantUUID")
		spEqualStrings(t, ptChainUUIDs(ptJoin(one(prompt()), calls, results, one(answer("result-1")))),
			[]string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"})
	})
	t.Run("does_not_duplicate_an_answered_call", func(t *testing.T) {
		calls, results := ptParallelTurn(t, "s", "prompt", 2, []int{0, 1})
		dup := ptClone(results[1])
		dup["uuid"] = json.RawMessage(`"result-1-copy"`)
		dup["parentUuid"] = json.RawMessage(`"elsewhere"`)
		delete(dup, "sourceToolAssistantUUID")
		spEqualStrings(t, ptChainUUIDs(ptJoin(one(prompt()), calls, results, one(dup), one(answer("result-1")))),
			[]string{"prompt", "call-0", "call-1", "result-0", "result-1", "answer"})
	})
	t.Run("does_not_pull_in_another_agents_result", func(t *testing.T) {
		// A sidechain result that reuses a main-chain tool_use_id.
		calls, results := ptParallelTurn(t, "s", "prompt", 2, []int{0, 1})
		results[0]["parentUuid"] = json.RawMessage(`"sidechain-call"`)
		results[0]["isSidechain"] = json.RawMessage(`true`)
		results[0]["agentId"] = json.RawMessage(`"agent-1"`)
		delete(results[0], "sourceToolAssistantUUID")
		spEqualStrings(t, ptChainUUIDs(ptJoin(one(prompt()), calls, results, one(answer("result-1")))),
			[]string{"prompt", "call-0", "call-1", "result-1", "answer"})
	})
	t.Run("tolerates_malformed_entries", func(t *testing.T) {
		calls, results := ptParallelTurn(t, "s", "prompt", 2, []int{0, 1})
		results[0]["timestamp"] = json.RawMessage(`12345`)
		entries := ptJoin(one(prompt()), calls, results, []transcriptEntry{
			ptEntry(t, "type", "assistant", "uuid", "no-message", "parentUuid", "result-1"),
			ptEntry(t, "type", "assistant", "uuid", "odd", "parentUuid", "no-message",
				"message", map[string]any{"id": 7, "content": []any{"text", nil, map[string]any{"type": "tool_use"}}}),
			ptEntry(t, "type", "user", "uuid", "odd-result", "parentUuid", "odd",
				"message", map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": 3}}}),
		})
		spEqualStrings(t, ptChainUUIDs(entries),
			[]string{"prompt", "call-0", "call-1", "result-0", "result-1", "no-message", "odd", "odd-result"})
	})
}

// Mirrors test_session_helpers_store.py's
// test_parallel_tool_calls_keep_every_call_and_result: the store-backed reader
// shares the chain builder.
func TestGetSessionMessagesFromStoreParallelToolCalls(t *testing.T) {
	ctx := context.Background()
	f := ssNewFixture(t)
	store := NewInMemorySessionStore()
	sid := newUUID()
	calls, results := ptParallelTurn(t, sid, "prompt", 3, []int{0, 1, 2})
	// The next response continues from the first result, so the walk back from
	// it reaches only call-0.
	entries := ptJoin(
		[]transcriptEntry{ptMsg(t, "user", "prompt", "", sid, "read them")},
		calls, results,
		[]transcriptEntry{ptMsg(t, "assistant", "answer", "result-0", sid, "done")},
	)
	var se []SessionStoreEntry
	for _, e := range entries {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		se = append(se, SessionStoreEntry{Data: b})
	}
	if err := store.Append(ctx, f.key(sid), se); err != nil {
		t.Fatal(err)
	}
	msgs, err := GetSessionMessagesFromStore(ctx, store, sid, f.dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	spEqualStrings(t, spUUIDs(msgs),
		[]string{"prompt", "call-0", "call-1", "call-2", "result-1", "result-2", "result-0", "answer"})
}
