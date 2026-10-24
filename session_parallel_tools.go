package claude

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Recovery of parallel tool calls dropped by the parentUuid walk. Ports
// upstream beffc50 (_recover_orphaned_parallel_tool_results), which mirrors
// the TypeScript SDK's getSessionMessages.

// rawString decodes raw as a JSON string, reporting false for any other JSON
// value (or none).
func rawString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// entryContent returns the blocks of an entry's message.content list, or nil
// when message is not an object or content is not a list.
func entryContent(e transcriptEntry) []json.RawMessage {
	var msg map[string]json.RawMessage
	if json.Unmarshal(e["message"], &msg) != nil || msg == nil {
		return nil
	}
	var content []json.RawMessage
	if json.Unmarshal(msg["content"], &content) != nil {
		return nil
	}
	return content
}

// assistantMessageID returns the API message.id of an assistant entry, or "".
func assistantMessageID(e transcriptEntry) string {
	if e.str("type") != "assistant" {
		return ""
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(e["message"], &msg) != nil || msg == nil {
		return ""
	}
	id, _ := rawString(msg["id"])
	return id
}

// contentBlockIDs returns idField of each blockType content block in an
// entry, skipping blocks whose idField is not a string.
func contentBlockIDs(e transcriptEntry, blockType, idField string) []string {
	var ids []string
	for _, raw := range entryContent(e) {
		var block map[string]json.RawMessage
		if json.Unmarshal(raw, &block) != nil || block == nil {
			continue
		}
		if t, _ := rawString(block["type"]); t != blockType {
			continue
		}
		if id, ok := rawString(block[idField]); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// isToolResultEntry reports whether e is a user entry with a parent that
// carries a tool_result block.
func isToolResultEntry(e transcriptEntry) bool {
	if e.str("type") != "user" {
		return false
	}
	if p, _ := rawString(e["parentUuid"]); p == "" {
		return false
	}
	for _, raw := range entryContent(e) {
		var block map[string]json.RawMessage
		if json.Unmarshal(raw, &block) == nil && block != nil {
			if t, _ := rawString(block["type"]); t == "tool_result" {
				return true
			}
		}
	}
	return false
}

// rawValue normalizes a raw JSON value for equality, treating absent and null
// alike.
func rawValue(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" {
		return ""
	}
	return string(raw)
}

// sameWriter reports whether two entries were written by the same (sub)agent.
func sameWriter(a, b transcriptEntry) bool {
	return a.truthy("isSidechain") == b.truthy("isSidechain") && rawValue(a["agentId"]) == rawValue(b["agentId"])
}

// timestampKey is an entry's timestamp string, or "" when it is not a string.
func timestampKey(e transcriptEntry) string {
	ts, _ := rawString(e["timestamp"])
	return ts
}

func sortByTimestamp(es []transcriptEntry) {
	sort.SliceStable(es, func(i, j int) bool { return timestampKey(es[i]) < timestampKey(es[j]) })
}

// recoverParallelToolResults puts back the tool calls and results a parallel
// turn left off the chain.
//
// The CLI writes one assistant entry per content block, so a response with N
// parallel tool calls is N assistant entries that share message.id and are
// chained one after another. Each tool_result entry's parentUuid is its own
// call's assistant entry, so the results fan out from different parents and
// the transcript forks. The parentUuid walk follows only the branch the next
// turn continued from: it keeps one result, and drops every call written
// after the one that result answers.
//
// For each response on the chain, this collects its assistant entries and
// their tool results that the walk did not reach, and inserts them right after
// the response's last entry on the chain, so the response stays in one piece
// and every result follows its call.
//
// order lists each uuid in byUUID once, by first appearance in the file.
func recoverParallelToolResults(byUUID map[string]transcriptEntry, order []string, chain []transcriptEntry) []transcriptEntry {
	var chainAssistants []transcriptEntry
	seen := make(map[string]bool, len(chain))
	for _, e := range chain {
		seen[e.str("uuid")] = true
		if e.str("type") == "assistant" {
			chainAssistants = append(chainAssistants, e)
		}
	}
	if len(chainAssistants) == 0 {
		return chain
	}

	// Anchor = the last on-chain entry of each response. chainAssistants is in
	// chain order, so later entries overwrite earlier ones.
	anchorByMessageID := map[string]transcriptEntry{}
	for _, e := range chainAssistants {
		if id := assistantMessageID(e); id != "" {
			anchorByMessageID[id] = e
		}
	}
	if len(anchorByMessageID) == 0 {
		return chain
	}

	// Group every assistant entry by response, and record which entry made
	// each tool call. A tool_use id claimed by two different responses is
	// ambiguous (a nil owner) and is never used to place a result.
	siblingsByMessageID := map[string][]transcriptEntry{}
	callOwner := map[string]transcriptEntry{}
	var toolResultEntries []transcriptEntry
	for _, uid := range order {
		e := byUUID[uid]
		if id := assistantMessageID(e); id != "" {
			siblingsByMessageID[id] = append(siblingsByMessageID[id], e)
			for _, callID := range contentBlockIDs(e, "tool_use", "id") {
				owner, claimed := callOwner[callID]
				if !claimed {
					callOwner[callID] = e
					continue
				}
				if owner != nil && assistantMessageID(owner) == id {
					callOwner[callID] = e
				} else {
					callOwner[callID] = nil
				}
			}
		} else if isToolResultEntry(e) {
			toolResultEntries = append(toolResultEntries, e)
		}
	}

	// Index each tool result under the assistant entries it may belong to: its
	// parent, its sourceToolAssistantUUID, and the owner of each call it
	// answers. The last two only count when written by the same agent.
	resultsByAssistant := map[string][]transcriptEntry{}
	for _, r := range toolResultEntries {
		parent := r.str("parentUuid")
		resultsByAssistant[parent] = append(resultsByAssistant[parent], r)
		if source, ok := rawString(r["sourceToolAssistantUUID"]); ok {
			if se, ok := byUUID[source]; ok && sameWriter(r, se) {
				resultsByAssistant[source] = append(resultsByAssistant[source], r)
			}
		}
		for _, callID := range contentBlockIDs(r, "tool_result", "tool_use_id") {
			if owner := callOwner[callID]; owner != nil && sameWriter(r, owner) {
				ou := owner.str("uuid")
				resultsByAssistant[ou] = append(resultsByAssistant[ou], r)
			}
		}
	}

	// Only needed to place strays, which are rare: built on first use.
	var answeredOnChain map[string]bool
	var filePosition map[string]int

	processed := map[string]bool{}
	inserts := map[string][]transcriptEntry{}
	for _, assistant := range chainAssistants {
		id := assistantMessageID(assistant)
		if id == "" || processed[id] {
			continue
		}
		processed[id] = true

		group, ok := siblingsByMessageID[id]
		if !ok {
			group = []transcriptEntry{assistant}
		}
		memberUUIDs := make(map[string]bool, len(group))
		for _, m := range group {
			memberUUIDs[m.str("uuid")] = true
		}
		var orphanedSiblings []transcriptEntry
		for _, m := range group {
			if !seen[m.str("uuid")] {
				orphanedSiblings = append(orphanedSiblings, m)
			}
		}
		// Results indexed under this response whose parent is not one of its
		// entries: recovered only if they answer a still-unanswered call.
		var orphanedResults, strays []transcriptEntry
		collected := map[string]bool{}
		for _, m := range group {
			for _, r := range resultsByAssistant[m.str("uuid")] {
				ru := r.str("uuid")
				if seen[ru] || collected[ru] {
					continue
				}
				collected[ru] = true
				if memberUUIDs[r.str("parentUuid")] {
					orphanedResults = append(orphanedResults, r)
				} else {
					strays = append(strays, r)
				}
			}
		}

		if len(strays) > 0 {
			if answeredOnChain == nil {
				answeredOnChain = map[string]bool{}
				for _, e := range chain {
					for _, callID := range contentBlockIDs(e, "tool_result", "tool_use_id") {
						answeredOnChain[callID] = true
					}
				}
				filePosition = make(map[string]int, len(order))
				for i, uid := range order {
					filePosition[uid] = i
				}
			}
			answered := make(map[string]bool, len(answeredOnChain))
			for k := range answeredOnChain {
				answered[k] = true
			}
			for _, r := range orphanedResults {
				for _, callID := range contentBlockIDs(r, "tool_result", "tool_use_id") {
					answered[callID] = true
				}
			}
			sort.SliceStable(strays, func(i, j int) bool {
				return filePosition[strays[i].str("uuid")] < filePosition[strays[j].str("uuid")]
			})
			for _, r := range strays {
				callIDs := contentBlockIDs(r, "tool_result", "tool_use_id")
				answersOpenCall := false
				for _, callID := range callIDs {
					if owner := callOwner[callID]; !answered[callID] && owner != nil && memberUUIDs[owner.str("uuid")] {
						answersOpenCall = true
						break
					}
				}
				if !answersOpenCall {
					continue
				}
				for _, callID := range callIDs {
					answered[callID] = true
				}
				orphanedResults = append(orphanedResults, r)
			}
		}

		if len(orphanedSiblings) == 0 && len(orphanedResults) == 0 {
			continue
		}

		// Timestamp order keeps content-block and completion order. The sort
		// is stable, so on a tie siblings keep file order and results keep the
		// order of the calls they answer.
		sortByTimestamp(orphanedSiblings)
		sortByTimestamp(orphanedResults)
		recovered := append(orphanedSiblings, orphanedResults...)
		for _, e := range recovered {
			seen[e.str("uuid")] = true
		}
		inserts[anchorByMessageID[id].str("uuid")] = recovered
	}

	if len(inserts) == 0 {
		return chain
	}
	out := make([]transcriptEntry, 0, len(chain))
	for _, e := range chain {
		out = append(out, e)
		out = append(out, inserts[e.str("uuid")]...)
	}
	return out
}
