package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// Session mutations port the official SDK's _internal/session_mutations.py.
// Rename and tag append metadata entries to the transcript, as the CLI does;
// delete removes the transcript and its subagent directory; fork writes a new
// session with every UUID remapped.
//
// If the session is open in a CLI process, the CLI re-reads the tail before
// re-appending its cached metadata, so an SDK write in that window is kept.

// ForkSessionResult reports the new session id produced by a fork.
type ForkSessionResult struct {
	SessionID string
}

func invalidSessionID(id string) error { return fmt.Errorf("claude: invalid session id: %q", id) }

// validateTitle trims a title and rejects an empty one, as the CLI does.
func validateTitle(title string) (string, error) {
	t := strings.TrimSpace(title)
	if t == "" {
		return "", errors.New("claude: title must be non-empty")
	}
	return t, nil
}

// validateTag sanitizes a tag; "" clears it, but a tag that sanitizes to
// nothing is rejected.
func validateTag(tag string) (string, error) {
	if tag == "" {
		return "", nil
	}
	t := strings.TrimSpace(sanitizeUnicode(tag))
	if t == "" {
		return "", errors.New("claude: tag must be non-empty (use \"\" to clear)")
	}
	return t, nil
}

// orderedJSON encodes key/value pairs as a compact JSON object in the given
// order (the transcript's tag scan expects "type" first).
func orderedJSON(pairs ...any) []byte {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(pairs[i])
		v, _ := json.Marshal(pairs[i+1])
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// RenameSession sets a session's title by appending a custom-title entry; the
// last one wins. The title is trimmed and must be non-empty. With a directory
// it looks in that project and its git worktrees; with "" it searches every
// project. A missing session returns an error wrapping [ErrSessionNotFound].
func RenameSession(sessionID, title, directory string) error {
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	t, err := validateTitle(title)
	if err != nil {
		return err
	}
	line := orderedJSON("type", "custom-title", "customTitle", t, "sessionId", sessionID)
	return appendToSession(sessionID, append(line, '\n'), directory)
}

// TagSession sets a session's tag by appending a tag entry; the last one wins,
// and "" clears it. Tags are stripped of invisible and formatting characters.
// Directory resolution is as for [RenameSession].
func TagSession(sessionID, tag, directory string) error {
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	t, err := validateTag(tag)
	if err != nil {
		return err
	}
	line := orderedJSON("type", "tag", "tag", t, "sessionId", sessionID)
	return appendToSession(sessionID, append(line, '\n'), directory)
}

// DeleteSession permanently removes a session's transcript and its subagent
// directory. For a soft delete, tag the session and filter on listing.
// Directory resolution is as for [RenameSession].
func DeleteSession(sessionID, directory string) error {
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	path, _ := resolveSessionFile(sessionID, directory)
	if path == "" {
		return notFound(sessionID, directory)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return notFound(sessionID, directory)
		}
		return err
	}
	_ = os.RemoveAll(filepath.Join(filepath.Dir(path), sessionID))
	return nil
}

func notFound(sessionID, directory string) error {
	if directory != "" {
		return fmt.Errorf("%w: %s in the project directory for %s", ErrSessionNotFound, sessionID, directory)
	}
	return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
}

// ForkSession copies a session into a new one, remapping every message UUID
// and keeping the parentUuid chain, and returns the new id. upToMessageID, if
// set, keeps the transcript up to and including that message. title names the
// fork; when empty, the source's title with " (fork)" is used. File-history
// snapshots are not copied. Directory resolution is as for [RenameSession].
func ForkSession(sessionID, directory, upToMessageID, title string) (ForkSessionResult, error) {
	if !uuidRE.MatchString(sessionID) {
		return ForkSessionResult{}, invalidSessionID(sessionID)
	}
	if upToMessageID != "" && !uuidRE.MatchString(upToMessageID) {
		return ForkSessionResult{}, fmt.Errorf("claude: invalid up-to message id: %q", upToMessageID)
	}
	path, projectDir := resolveSessionFile(sessionID, directory)
	if path == "" {
		return ForkSessionResult{}, notFound(sessionID, directory)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return ForkSessionResult{}, err
	}
	if len(content) == 0 {
		return ForkSessionResult{}, fmt.Errorf("claude: session %s has no messages to fork", sessionID)
	}
	transcript, replacements := parseForkTranscript(splitJSONLObjects(content), sessionID)
	deriveTitle := func() string {
		lite := jsonlToLite(content, 0)
		return firstNonEmpty(lastField(lite.tail, "customTitle"), lastField(lite.head, "customTitle"),
			lastField(lite.tail, "aiTitle"), lastField(lite.head, "aiTitle"), extractFirstPromptFromHead(lite.head))
	}
	forkedID, lines, err := buildForkLines(transcript, replacements, sessionID, upToMessageID, title, deriveTitle)
	if err != nil {
		return ForkSessionResult{}, err
	}
	f, err := os.OpenFile(filepath.Join(projectDir, forkedID+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ForkSessionResult{}, err
	}
	_, err = f.Write(append(bytesJoin(lines, '\n'), '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return ForkSessionResult{}, err
	}
	return ForkSessionResult{SessionID: forkedID}, nil
}

func bytesJoin(lines [][]byte, sep byte) []byte {
	var out []byte
	for i, l := range lines {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, l...)
	}
	return out
}

// forkTranscriptTypes are the entry types copied into a fork.
var forkTranscriptTypes = map[string]bool{
	"user": true, "assistant": true, "attachment": true, "system": true, "progress": true,
}

// splitJSONLObjects parses each JSONL line that is a JSON object.
func splitJSONLObjects(content []byte) []transcriptEntry {
	var out []transcriptEntry
	for _, line := range strings.Split(decodeLossy(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e transcriptEntry
		if json.Unmarshal([]byte(line), &e) == nil && e != nil {
			out = append(out, e)
		}
	}
	return out
}

// parseForkTranscript separates transcript entries (with a uuid) from the
// source session's content-replacement records.
func parseForkTranscript(entries []transcriptEntry, sessionID string) (transcript []transcriptEntry, replacements []json.RawMessage) {
	for _, e := range entries {
		typ := e.str("type")
		switch {
		case forkTranscriptTypes[typ] && e.hasStringUUID():
			transcript = append(transcript, e)
		case typ == "content-replacement" && e.str("sessionId") == sessionID:
			var reps []json.RawMessage
			if json.Unmarshal(e["replacements"], &reps) == nil && e["replacements"] != nil && string(e["replacements"]) != "null" {
				replacements = append(replacements, reps...)
			}
		}
	}
	return transcript, replacements
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isoNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000Z") }

// buildForkLines is the fork transform shared by the disk and store paths: it
// drops sidechains, cuts at upToMessageID, remaps UUIDs (skipping progress
// entries in the parent chain and in the output), points each entry at the
// fork, and appends content replacements and a custom-title entry.
// deriveTitle runs only when no title is given.
func buildForkLines(transcript []transcriptEntry, replacements []json.RawMessage, sessionID, upToMessageID, title string, deriveTitle func() string) (string, [][]byte, error) {
	var kept []transcriptEntry
	for _, e := range transcript {
		if !e.truthy("isSidechain") {
			kept = append(kept, e)
		}
	}
	transcript = kept
	noMessages := fmt.Errorf("claude: session %s has no messages to fork", sessionID)
	if len(transcript) == 0 {
		return "", nil, noMessages
	}
	if upToMessageID != "" {
		cut := -1
		for i, e := range transcript {
			if e.str("uuid") == upToMessageID {
				cut = i
				break
			}
		}
		if cut < 0 {
			return "", nil, fmt.Errorf("claude: message %s not found in session %s", upToMessageID, sessionID)
		}
		transcript = transcript[:cut+1]
	}

	mapping := map[string]string{}
	byUUID := map[string]transcriptEntry{}
	for _, e := range transcript {
		mapping[e.str("uuid")] = newUUID()
		byUUID[e.str("uuid")] = e
	}
	var writable []transcriptEntry
	for _, e := range transcript {
		if e.str("type") != "progress" {
			writable = append(writable, e)
		}
	}
	if len(writable) == 0 {
		return "", nil, noMessages
	}

	forkedID := newUUID()
	now := isoNow()
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	var lines [][]byte
	for i, orig := range writable {
		var newParent any
		for pid := orig.str("parentUuid"); pid != ""; {
			parent, ok := byUUID[pid]
			if !ok {
				break
			}
			if parent.str("type") != "progress" {
				if m, ok := mapping[pid]; ok {
					newParent = m
				}
				break
			}
			pid = parent.str("parentUuid")
		}
		// Only the last message gets a new timestamp, for leaf detection on
		// resume.
		ts := raw(now)
		if i != len(writable)-1 {
			if t, ok := orig["timestamp"]; ok {
				ts = t
			}
		}
		var logical any
		if lp := orig.str("logicalParentUuid"); lp != "" {
			if m, ok := mapping[lp]; ok {
				logical = m
			}
		} else if v, ok := orig["logicalParentUuid"]; ok {
			logical = v
		}

		forked := transcriptEntry{}
		for k, v := range orig {
			forked[k] = v
		}
		forked["uuid"] = raw(mapping[orig.str("uuid")])
		forked["parentUuid"] = raw(newParent)
		forked["logicalParentUuid"] = raw(logical)
		forked["sessionId"] = raw(forkedID)
		forked["timestamp"] = ts
		forked["isSidechain"] = raw(false)
		forked["forkedFrom"] = orderedJSON("sessionId", sessionID, "messageUuid", orig.str("uuid"))
		for _, k := range []string{"teamName", "agentName", "slug", "sourceToolAssistantUUID"} {
			delete(forked, k)
		}
		lines = append(lines, marshalTypeFirst(forked))
	}

	if len(replacements) > 0 {
		lines = append(lines, marshalTypeFirst(map[string]json.RawMessage{
			"type":         raw("content-replacement"),
			"sessionId":    raw(forkedID),
			"replacements": raw(replacements),
			"uuid":         raw(newUUID()),
			"timestamp":    raw(now),
		}))
	}

	forkTitle := strings.TrimSpace(title)
	if forkTitle == "" {
		forkTitle = firstNonEmpty(deriveTitle(), "Forked session") + " (fork)"
	}
	lines = append(lines, marshalTypeFirst(map[string]json.RawMessage{
		"type":        raw("custom-title"),
		"sessionId":   raw(forkedID),
		"customTitle": raw(forkTitle),
		"uuid":        raw(newUUID()),
		"timestamp":   raw(now),
	}))
	return forkedID, lines, nil
}

// appendToSession appends data to an existing, non-empty session transcript in
// the first candidate project dir that has one.
func appendToSession(sessionID string, data []byte, directory string) error {
	if directory == "" {
		projects, err := projectsDirFor(nil)
		if err == nil {
			_, err = os.ReadDir(projects)
		}
		if err != nil {
			return fmt.Errorf("%w: %s (no projects directory)", ErrSessionNotFound, sessionID)
		}
	}
	dirs, _ := candidateProjectDirs(directory)
	for _, d := range dirs {
		ok, err := tryAppend(filepath.Join(d, sessionID+".jsonl"), data)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	if directory == "" {
		return fmt.Errorf("%w: %s in any project directory", ErrSessionNotFound, sessionID)
	}
	return notFound(sessionID, directory)
}

// tryAppend appends to path if it exists and is non-empty. It opens without
// O_CREATE, so a missing file is a clean miss rather than a race; an empty file
// is a miss too, as readers treat it. Other errors are returned.
func tryAppend(path string, data []byte) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if st.Size() == 0 {
		return false, nil
	}
	if _, err := f.Write(data); err != nil {
		return false, err
	}
	return true, nil
}

// unicodeStripRe matches characters removed from tags: zero-width and
// directional marks, directional isolates, the BOM, and BMP private use.
var unicodeStripRe = regexp.MustCompile(`[\x{200b}-\x{200f}\x{202a}-\x{202e}\x{2066}-\x{2069}\x{feff}\x{e000}-\x{f8ff}]`)

// sanitizeUnicode removes format (Cf), private-use (Co), and unassigned (Cn)
// characters, repeating until stable. The official SDK also applies NFKC
// normalization, which needs tables outside the standard library.
func sanitizeUnicode(value string) string {
	cur := value
	for i := 0; i < 10; i++ {
		prev := cur
		cur = strings.Map(func(r rune) rune {
			if unicode.In(r, unicode.Cf, unicode.Co) || !assigned(r) {
				return -1
			}
			return r
		}, cur)
		cur = unicodeStripRe.ReplaceAllString(cur, "")
		if cur == prev {
			break
		}
	}
	return cur
}

// assigned reports whether r has a Unicode general category (is not Cn).
func assigned(r rune) bool {
	for _, t := range unicode.Categories {
		if unicode.Is(t, r) {
			return true
		}
	}
	return false
}

// --- Store-backed mutations -------------------------------------------------------

// RenameSessionViaStore sets a session's title by appending a custom-title
// entry to the store, under the project key for directory (the current
// directory when empty).
func RenameSessionViaStore(ctx context.Context, store SessionStore, sessionID, title, directory string) error {
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	t, err := validateTitle(title)
	if err != nil {
		return err
	}
	entry := orderedJSON("type", "custom-title", "customTitle", t, "sessionId", sessionID, "uuid", newUUID(), "timestamp", isoNow())
	return store.Append(ctx, SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}, []SessionStoreEntry{{Data: entry}})
}

// TagSessionViaStore sets a session's tag by appending a tag entry to the
// store; "" clears it. Tags are sanitized as by [TagSession].
func TagSessionViaStore(ctx context.Context, store SessionStore, sessionID, tag, directory string) error {
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	t, err := validateTag(tag)
	if err != nil {
		return err
	}
	entry := orderedJSON("type", "tag", "tag", t, "sessionId", sessionID, "uuid", newUUID(), "timestamp", isoNow())
	return store.Append(ctx, SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID}, []SessionStoreEntry{{Data: entry}})
}

// DeleteSessionViaStore deletes a session from the store. A store without
// Delete support (errors.ErrUnsupported) makes this a no-op. Whether subagent
// transcripts go too depends on the store; [InMemorySessionStore] removes them.
func DeleteSessionViaStore(ctx context.Context, store SessionStore, sessionID, directory string) error {
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	err := store.Delete(ctx, SessionKey{ProjectKey: ProjectKeyForDirectory(directory), SessionID: sessionID})
	if errors.Is(err, errors.ErrUnsupported) {
		return nil
	}
	return err
}

// ForkSessionViaStore forks a session within the store, as [ForkSession] does
// on disk. The entries pass through this process so every UUID is remapped; a
// storage-level copy would not be a fork.
func ForkSessionViaStore(ctx context.Context, store SessionStore, sessionID, directory, upToMessageID, title string) (ForkSessionResult, error) {
	if !uuidRE.MatchString(sessionID) {
		return ForkSessionResult{}, invalidSessionID(sessionID)
	}
	if upToMessageID != "" && !uuidRE.MatchString(upToMessageID) {
		return ForkSessionResult{}, fmt.Errorf("claude: invalid up-to message id: %q", upToMessageID)
	}
	projectKey := ProjectKeyForDirectory(directory)
	loaded, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: sessionID})
	if err != nil {
		return ForkSessionResult{}, err
	}
	if len(loaded) == 0 {
		return ForkSessionResult{}, notFound(sessionID, "")
	}
	var raw []transcriptEntry
	for _, se := range loaded {
		var e transcriptEntry
		if json.Unmarshal(se.Data, &e) == nil && e != nil {
			raw = append(raw, e)
		}
	}
	transcript, replacements := parseForkTranscript(raw, sessionID)
	forkedID, lines, err := buildForkLines(transcript, replacements, sessionID, upToMessageID, title,
		func() string { return deriveTitleFromEntries(raw) })
	if err != nil {
		return ForkSessionResult{}, err
	}
	entries := make([]SessionStoreEntry, len(lines))
	for i, l := range lines {
		entries[i] = SessionStoreEntry{Data: l}
	}
	if err := store.Append(ctx, SessionKey{ProjectKey: projectKey, SessionID: forkedID}, entries); err != nil {
		return ForkSessionResult{}, err
	}
	return ForkSessionResult{SessionID: forkedID}, nil
}

// deriveTitleFromEntries applies the disk path's title precedence to parsed
// entries: the last customTitle, else the last aiTitle, else the first prompt.
func deriveTitleFromEntries(entries []transcriptEntry) string {
	var custom, ai string
	for _, e := range entries {
		if v := e.str("customTitle"); v != "" {
			custom = v
		}
		if v := e.str("aiTitle"); v != "" {
			ai = v
		}
	}
	if t := firstNonEmpty(custom, ai); t != "" {
		return t
	}
	var jsonl []byte
	for _, e := range entries {
		b, _ := json.Marshal(e)
		jsonl = append(append(jsonl, b...), '\n')
	}
	return extractFirstPromptFromHead(string(jsonl))
}
