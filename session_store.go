package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionStore is an abstract transcript store, mirroring the official SDK's
// SessionStore. Implementations persist session entries keyed by project and
// session id; [InMemorySessionStore] is the built-in implementation.
//
// Append and Load are required. The other methods are optional in the official
// SDK; a store that does not support one returns an error wrapping
// errors.ErrUnsupported, and callers fall back or skip as upstream does:
// ListSessionSummaries falls back to ListSessions plus Load, ListSubkeys means
// no subagents, and Delete is a no-op (for append-only backends).
type SessionStore interface {
	Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error
	Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error)
	ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error)
	ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error)
	ListSubkeys(ctx context.Context, key SessionListSubkeysKey) ([]string, error)
	Delete(ctx context.Context, key SessionKey) error
}

// SessionKey identifies a session (optionally a subagent subpath) within a
// project. Subagent subpaths mirror the on-disk layout, such as
// "subagents/agent-<id>".
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

// SessionStoreEntry is one stored transcript line (a raw JSON object). Stores
// treat it as an opaque blob.
type SessionStoreEntry struct {
	Data json.RawMessage
}

// SessionStoreListEntry is a session id with its last-modified time.
type SessionStoreListEntry struct {
	SessionID string
	// Mtime is the last-modified time in epoch milliseconds.
	Mtime int64
}

// SessionSummaryEntry is an incrementally maintained session summary. Stores
// obtain it from [FoldSessionSummary] inside Append, persist it verbatim, and
// return the set from ListSessionSummaries. Data is SDK-owned state that
// stores must not interpret.
type SessionSummaryEntry struct {
	SessionID string
	// Mtime is the storage write time of the summary in epoch milliseconds,
	// on the same clock as ListSessions' mtime for the session.
	Mtime int64
	Data  map[string]any
}

// SessionStoreFlushMode controls when a store flushes pending writes.
type SessionStoreFlushMode string

const (
	// FlushBatched flushes on each result message (explicit flush points).
	FlushBatched SessionStoreFlushMode = "batched"
	// FlushEager flushes eagerly as entries arrive.
	FlushEager SessionStoreFlushMode = "eager"
)

// storeKey is the internal map key.
func storeKey(k SessionKey) string {
	if k.Subpath != "" {
		return k.ProjectKey + "/" + k.SessionID + "/" + k.Subpath
	}
	return k.ProjectKey + "/" + k.SessionID
}

// InMemorySessionStore is an in-memory [SessionStore], suitable for tests and
// ephemeral mirrors. It maintains folded session summaries on Append and
// strictly increasing wall-clock mtimes, as the official implementation does.
type InMemorySessionStore struct {
	mu        sync.Mutex
	entries   map[string][]SessionStoreEntry
	mtimes    map[string]int64
	summaries map[[2]string]SessionSummaryEntry
	lastMtime int64
}

// NewInMemorySessionStore creates an empty in-memory store.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		entries:   map[string][]SessionStoreEntry{},
		mtimes:    map[string]int64{},
		summaries: map[[2]string]SessionSummaryEntry{},
	}
}

// nextMtime returns the wall-clock time in epoch ms, bumped so back-to-back
// appends always get distinct, increasing mtimes.
func (s *InMemorySessionStore) nextMtime() int64 {
	now := time.Now().UnixMilli()
	if now <= s.lastMtime {
		now = s.lastMtime + 1
	}
	s.lastMtime = now
	return now
}

func (s *InMemorySessionStore) Append(ctx context.Context, key SessionKey, entries []SessionStoreEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := storeKey(key)
	s.entries[k] = append(s.entries[k], entries...)
	now := s.nextMtime()
	if key.Subpath == "" {
		sk := [2]string{key.ProjectKey, key.SessionID}
		var prev *SessionSummaryEntry
		if p, ok := s.summaries[sk]; ok {
			prev = &p
		}
		folded := FoldSessionSummary(prev, key, entries)
		folded.Mtime = now
		s.summaries[sk] = folded
	}
	s.mtimes[k] = now
	return nil
}

func (s *InMemorySessionStore) Load(ctx context.Context, key SessionKey) ([]SessionStoreEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.entries[storeKey(key)]
	if src == nil {
		return nil, nil
	}
	return append([]SessionStoreEntry(nil), src...), nil
}

func (s *InMemorySessionStore) ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SessionStoreListEntry
	prefix := projectKey + "/"
	for k := range s.entries {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok || strings.Contains(rest, "/") {
			continue // other projects and subagent subkeys
		}
		out = append(out, SessionStoreListEntry{SessionID: rest, Mtime: s.mtimes[k]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out, nil
}

func (s *InMemorySessionStore) ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionSummaryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []SessionSummaryEntry
	for sk, sum := range s.summaries {
		if sk[0] == projectKey {
			out = append(out, sum)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
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

// Delete removes a key; deleting a main transcript also removes its summary and
// subagent subkeys.
func (s *InMemorySessionStore) Delete(ctx context.Context, key SessionKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := storeKey(key)
	delete(s.entries, k)
	delete(s.mtimes, k)
	if key.Subpath == "" {
		delete(s.summaries, [2]string{key.ProjectKey, key.SessionID})
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

// ProjectKeyForDirectory returns the project key (the sanitized directory name)
// the CLI uses for sessions under the given working directory, which defaults
// to the current directory. The path is made absolute and symlinks are
// resolved first, as the CLI does, so keys match between local transcripts and
// store-mirrored ones.
func ProjectKeyForDirectory(directory string) string {
	return sanitizePath(canonicalizePath(directory))
}

// --- Summary folding ------------------------------------------------------------

// lastWinsSummaryFields maps transcript keys to summary data keys for string
// fields where each appended value replaces the previous one.
var lastWinsSummaryFields = [][2]string{
	{"customTitle", "custom_title"},
	{"aiTitle", "ai_title"},
	{"lastPrompt", "last_prompt"},
	{"summary", "summary_hint"},
	{"gitBranch", "git_branch"},
}

// FoldSessionSummary folds a batch of appended entries into the running summary
// for key, without re-reading the transcript. prev is the previous summary for
// the same key (nil for the first append). Every derived field is set-once or
// last-wins, so stores never re-read earlier entries.
//
// Mtime is left as prev's (0 for a new summary): it is the store's write time,
// stamped by the store after persisting, on the same clock as ListSessions.
// Do not call this for keys with a Subpath; subagent transcripts must not feed
// the main session's summary.
func FoldSessionSummary(prev *SessionSummaryEntry, key SessionKey, entries []SessionStoreEntry) SessionSummaryEntry {
	out := SessionSummaryEntry{SessionID: key.SessionID, Data: map[string]any{}}
	if prev != nil {
		out.SessionID, out.Mtime = prev.SessionID, prev.Mtime
		for k, v := range prev.Data {
			out.Data[k] = v
		}
	}
	data := out.Data
	for _, raw := range entries {
		var e transcriptEntry
		if json.Unmarshal(raw.Data, &e) != nil || e == nil {
			continue
		}
		if _, ok := data["is_sidechain"]; !ok {
			data["is_sidechain"] = string(bytes.TrimSpace(e["isSidechain"])) == "true"
		}
		if _, ok := data["created_at"]; !ok {
			if ms, ok := isoToMillis(e.str("timestamp")); ok && e.str("timestamp") != "" {
				data["created_at"] = ms
			}
		}
		if _, ok := data["cwd"]; !ok {
			if cwd := e.str("cwd"); cwd != "" {
				data["cwd"] = cwd
			}
		}
		foldFirstPrompt(data, e)
		for _, f := range lastWinsSummaryFields {
			if raw := bytes.TrimSpace(e[f[0]]); len(raw) > 0 && raw[0] == '"' {
				data[f[1]] = e.str(f[0])
			}
		}
		if e.str("type") == "tag" {
			if tag := e.str("tag"); tag != "" {
				data["tag"] = tag
			} else {
				delete(data, "tag")
			}
		}
	}
	return out
}

// foldFirstPrompt applies extractFirstPromptFromHead's rules to one entry: it
// locks the first meaningful prompt, or stashes a slash-command fallback.
func foldFirstPrompt(data map[string]any, e transcriptEntry) {
	if locked, _ := data["first_prompt_locked"].(bool); locked {
		return
	}
	if e.str("type") != "user" {
		return
	}
	if string(bytes.TrimSpace(e["isMeta"])) == "true" || string(bytes.TrimSpace(e["isCompactSummary"])) == "true" {
		return
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(e["message"], &message) != nil || message == nil {
		return
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(message["content"], &blocks) == nil {
		for _, b := range blocks {
			if jsonString(b["type"]) == "tool_result" {
				return
			}
		}
	}
	for _, raw := range contentTexts(message["content"]) {
		result := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
		if result == "" {
			continue
		}
		if m := commandNameRe.FindStringSubmatch(result); m != nil {
			if fb, _ := data["command_fallback"].(string); fb == "" {
				data["command_fallback"] = m[1]
			}
			continue
		}
		if skipFirstPromptRe.MatchString(result) {
			continue
		}
		data["first_prompt"] = truncateRunes(result, 200)
		data["first_prompt_locked"] = true
		return
	}
}

// summaryEntryToSDKInfo converts a folded summary to session metadata. ok is
// false for sidechain sessions and sessions with no summary.
func summaryEntryToSDKInfo(entry SessionSummaryEntry, projectPath string) (SDKSessionInfo, bool) {
	data := entry.Data
	str := func(k string) string { s, _ := data[k].(string); return s }
	if b, _ := data["is_sidechain"].(bool); b {
		return SDKSessionInfo{}, false
	}
	firstPrompt := str("command_fallback")
	if locked, _ := data["first_prompt_locked"].(bool); locked {
		firstPrompt = str("first_prompt")
	}
	customTitle := firstNonEmpty(str("custom_title"), str("ai_title"))
	summary := firstNonEmpty(customTitle, str("last_prompt"), str("summary_hint"), firstPrompt)
	if summary == "" {
		return SDKSessionInfo{}, false
	}
	info := SDKSessionInfo{
		SessionID:    entry.SessionID,
		Summary:      summary,
		LastModified: entry.Mtime,
		CustomTitle:  customTitle,
		FirstPrompt:  firstPrompt,
		GitBranch:    str("git_branch"),
		Cwd:          firstNonEmpty(str("cwd"), projectPath),
		Tag:          str("tag"),
	}
	switch v := data["created_at"].(type) {
	case int64:
		info.CreatedAt = v
	case float64:
		info.CreatedAt = int64(v)
	case json.Number:
		info.CreatedAt, _ = v.Int64()
	}
	return info, true
}

// --- Store-backed reading -------------------------------------------------------

// storeListLoadConcurrency bounds concurrent Load calls when listing.
const storeListLoadConcurrency = 16

// entriesToJSONL serializes store entries as JSONL with each object's "type"
// first, the byte shape the disk path has (the tag scan relies on it).
func entriesToJSONL(entries []SessionStoreEntry) []byte {
	var buf bytes.Buffer
	for _, e := range entries {
		buf.Write(typeFirstJSON(e.Data))
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// typeFirstJSON re-encodes a JSON object compactly with its "type" key first;
// anything else is compacted as is.
func typeFirstJSON(raw json.RawMessage) []byte {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		var buf bytes.Buffer
		if json.Compact(&buf, raw) != nil {
			return raw
		}
		return buf.Bytes()
	}
	return marshalTypeFirst(obj)
}

// marshalTypeFirst encodes obj compactly, "type" first and the rest sorted.
func marshalTypeFirst(obj map[string]json.RawMessage) []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	write := func(k string, v json.RawMessage) {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		if json.Compact(&buf, v) != nil {
			buf.WriteString("null")
		}
	}
	if t, ok := obj["type"]; ok {
		write("type", t)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if k != "type" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		write(k, obj[k])
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// mtimeFromJSONLTail reads the last entry's timestamp, or returns now.
func mtimeFromJSONLTail(jsonl []byte) int64 {
	trimmed := bytes.TrimRight(jsonl, " \t\r\n")
	last := trimmed[bytes.LastIndexByte(trimmed, '\n')+1:]
	var obj map[string]json.RawMessage
	if json.Unmarshal(last, &obj) == nil {
		if ms, ok := isoToMillis(jsonString(obj["timestamp"])); ok && jsonString(obj["timestamp"]) != "" {
			return ms
		}
	}
	return time.Now().UnixMilli()
}

func loadStoreJSONL(ctx context.Context, store SessionStore, sessionID, directory string) ([]byte, error) {
	entries, err := store.Load(ctx, SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID})
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return entriesToJSONL(entries), nil
}

// deriveInfosViaLoad derives metadata for each listed session by loading it,
// with bounded concurrency. A failing Load leaves that session with an empty
// summary rather than failing the list; sidechain and no-summary sessions are
// dropped.
func deriveInfosViaLoad(ctx context.Context, store SessionStore, listing []SessionStoreListEntry, directory, projectPath string) []SDKSessionInfo {
	type outcome struct {
		jsonl []byte
		err   error
	}
	settled := make([]outcome, len(listing))
	sem := make(chan struct{}, storeListLoadConcurrency)
	var wg sync.WaitGroup
	for i, e := range listing {
		wg.Add(1)
		go func(i int, sid string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			jsonl, err := loadStoreJSONL(ctx, store, sid, directory)
			settled[i] = outcome{jsonl, err}
		}(i, e.SessionID)
	}
	wg.Wait()

	var out []SDKSessionInfo
	for i, e := range listing {
		o := settled[i]
		if o.err != nil {
			out = append(out, SDKSessionInfo{SessionID: e.SessionID, LastModified: e.Mtime})
			continue
		}
		if o.jsonl == nil {
			continue
		}
		info, ok := parseSessionInfoFromLite(e.SessionID, jsonlToLite(o.jsonl, e.Mtime), projectPath)
		if !ok {
			continue
		}
		info.LastModified = e.Mtime
		out = append(out, info)
	}
	return out
}

// ListSessionsFromStore lists a project's sessions from a store, newest first,
// with the same metadata [ListSessions] derives from disk. directory selects
// the project key (the current directory when empty). A limit of 0 means no
// limit.
//
// When the store supports ListSessionSummaries, it uses the folded summaries
// plus one ListSessions call to find sessions whose summary is missing or
// stale, and loads only those (after paging). Otherwise it loads every
// session (16 at a time). It fails if the store supports neither listing
// method.
func ListSessionsFromStore(ctx context.Context, store SessionStore, directory string, limit, offset int) ([]SDKSessionInfo, error) {
	projectPath := canonicalizePath(directory)
	projectKey := sanitizePath(projectPath)

	listing, listErr := store.ListSessions(ctx, projectKey)
	hasListSessions := !errors.Is(listErr, errors.ErrUnsupported)
	if hasListSessions && listErr != nil {
		return nil, listErr
	}

	summaries, sumErr := store.ListSessionSummaries(ctx, projectKey)
	if sumErr == nil {
		known := map[string]int64{}
		for _, e := range listing {
			known[e.SessionID] = e.Mtime
		}
		type slot struct {
			mtime int64
			sid   string
			info  *SDKSessionInfo
		}
		var slots []slot
		fresh := map[string]bool{}
		for _, s := range summaries {
			if hasListSessions {
				m, ok := known[s.SessionID]
				if !ok || s.Mtime < m {
					// Gone from the listing, or stale: re-derive from source.
					continue
				}
			}
			fresh[s.SessionID] = true
			if info, ok := summaryEntryToSDKInfo(s, projectPath); ok {
				slots = append(slots, slot{mtime: s.Mtime, info: &info})
			}
		}
		for _, e := range listing {
			if !fresh[e.SessionID] {
				slots = append(slots, slot{mtime: e.Mtime, sid: e.SessionID})
			}
		}
		// Page before loading, so the loads are bounded by the page size.
		sort.SliceStable(slots, func(i, j int) bool { return slots[i].mtime > slots[j].mtime })
		slots = page(slots, limit, offset)
		var toFill []SessionStoreListEntry
		for _, sl := range slots {
			if sl.info == nil {
				toFill = append(toFill, SessionStoreListEntry{SessionID: sl.sid, Mtime: sl.mtime})
			}
		}
		filled := map[string]SDKSessionInfo{}
		for _, f := range deriveInfosViaLoad(ctx, store, toFill, directory, projectPath) {
			filled[f.SessionID] = f
		}
		var out []SDKSessionInfo
		for _, sl := range slots {
			if sl.info != nil {
				out = append(out, *sl.info)
			} else if f, ok := filled[sl.sid]; ok {
				out = append(out, f)
			}
		}
		return out, nil
	}
	if !errors.Is(sumErr, errors.ErrUnsupported) {
		return nil, sumErr
	}
	if !hasListSessions {
		return nil, errors.New("claude: session store supports neither ListSessionSummaries nor ListSessions; cannot list sessions")
	}
	return sortLimitOffset(deriveInfosViaLoad(ctx, store, listing, directory, projectPath), limit, offset), nil
}

// GetSessionInfoFromStore returns one session's metadata from a store, or
// [ErrSessionNotFound].
func GetSessionInfoFromStore(ctx context.Context, store SessionStore, sessionID, directory string) (SDKSessionInfo, error) {
	if !uuidRE.MatchString(sessionID) {
		return SDKSessionInfo{}, ErrSessionNotFound
	}
	jsonl, err := loadStoreJSONL(ctx, store, sessionID, directory)
	if err != nil {
		return SDKSessionInfo{}, err
	}
	if jsonl == nil {
		return SDKSessionInfo{}, ErrSessionNotFound
	}
	info, ok := parseSessionInfoFromLite(sessionID, jsonlToLite(jsonl, mtimeFromJSONLTail(jsonl)), canonicalizePath(directory))
	if !ok {
		return SDKSessionInfo{}, ErrSessionNotFound
	}
	return info, nil
}

// GetSessionMessagesFromStore returns a session's conversation from a store,
// as [GetSessionMessages] does from disk.
func GetSessionMessagesFromStore(ctx context.Context, store SessionStore, sessionID, directory string, limit, offset int) ([]SessionMessage, error) {
	if !uuidRE.MatchString(sessionID) {
		return nil, nil
	}
	entries, err := store.Load(ctx, SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID})
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	return entriesToSessionMessages(filterTranscriptEntries(entries), limit, offset), nil
}

// ListSubagentsFromStore returns a session's subagent ids from a store's
// subagents/.../agent-<id> subkeys. It fails if the store does not support
// ListSubkeys.
func ListSubagentsFromStore(ctx context.Context, store SessionStore, sessionID, directory string) ([]string, error) {
	if !uuidRE.MatchString(sessionID) {
		return nil, nil
	}
	subkeys, err := store.ListSubkeys(ctx, SessionListSubkeysKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID})
	if errors.Is(err, errors.ErrUnsupported) {
		return nil, errors.New("claude: session store does not support ListSubkeys; cannot list subagents")
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, sp := range subkeys {
		if !strings.HasPrefix(sp, "subagents/") {
			continue
		}
		last := sp[strings.LastIndex(sp, "/")+1:]
		if id, ok := strings.CutPrefix(last, "agent-"); ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// GetSubagentMessagesFromStore returns a subagent's conversation from a store,
// as [GetSubagentMessages] does from disk; the parent ids come from the
// subagent's agent_metadata entry. Without ListSubkeys support it tries
// subagents/agent-<id> directly.
func GetSubagentMessagesFromStore(ctx context.Context, store SessionStore, sessionID, agentID, directory string, limit, offset int) ([]SessionMessage, error) {
	if !uuidRE.MatchString(sessionID) || agentID == "" {
		return nil, nil
	}
	projectKey := ProjectKeyForDirectory(directory)
	subpath := "subagents/agent-" + agentID
	subkeys, err := store.ListSubkeys(ctx, SessionListSubkeysKey{ProjectKey: projectKey, SessionID: sessionID})
	switch {
	case errors.Is(err, errors.ErrUnsupported):
	case err != nil:
		return nil, err
	default:
		subpath = ""
		for _, sk := range subkeys {
			if strings.HasPrefix(sk, "subagents/") && sk[strings.LastIndex(sk, "/")+1:] == "agent-"+agentID {
				subpath = sk
				break
			}
		}
		if subpath == "" {
			return nil, nil
		}
	}
	entries, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: sessionID, Subpath: subpath})
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	meta, transcript := splitAgentMetadata(entries)
	if len(transcript) == 0 {
		return nil, nil
	}
	toolUseID, parentAgentID := parentIDsFromAgentMetadata(meta)
	return entriesToSubagentMessages(filterTranscriptEntries(transcript), limit, offset, toolUseID, parentAgentID), nil
}
