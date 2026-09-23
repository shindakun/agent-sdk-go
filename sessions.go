package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Session reading ports the official SDK's _internal/sessions.py. Sessions are
// newline-delimited JSON under <config dir>/projects/<project key>/<id>.jsonl,
// with subagent transcripts under <id>/subagents/. Listing reads only the head
// and tail of each file; message reading rebuilds the conversation chain from
// parentUuid links. None of this needs a running CLI.

// ErrSessionNotFound reports that a session (or its summary) could not be
// found. Match it with errors.Is.
var ErrSessionNotFound = errors.New("claude: session not found")

const (
	maxSanitizedLength = 200
	// liteReadBufSize is the head/tail window read for listing metadata.
	liteReadBufSize = 65536
)

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// skipFirstPromptRe matches auto-generated or system user messages that should
// be skipped when extracting a session's first meaningful prompt, mirroring the
// official SDK's _SKIP_FIRST_PROMPT_PATTERN.
var skipFirstPromptRe = regexp.MustCompile(
	`^(?:<local-command-stdout>|<session-start-hook>|<tick>|<goal>|` +
		`\[Request interrupted by user[^\]]*\]|` +
		`\s*<ide_opened_file>[\s\S]*</ide_opened_file>\s*$|` +
		`\s*<ide_selection>[\s\S]*</ide_selection>\s*$)`)

// commandNameRe extracts a slash-command name from a transcript line.
var commandNameRe = regexp.MustCompile(`<command-name>(.*?)</command-name>`)

// SDKSessionInfo is metadata about a stored session. Zero values mean the
// field is unknown.
type SDKSessionInfo struct {
	SessionID string `json:"session_id"`
	// Summary is the display title: the custom or AI title, else the last
	// prompt, the CLI's summary, or the first prompt.
	Summary      string `json:"summary"`
	LastModified int64  `json:"last_modified"` // epoch milliseconds
	// FileSize is the transcript's byte size; zero for store-backed sessions.
	FileSize    int64  `json:"file_size,omitempty"`
	CustomTitle string `json:"custom_title,omitempty"`
	FirstPrompt string `json:"first_prompt,omitempty"`
	GitBranch   string `json:"git_branch,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Tag         string `json:"tag,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"` // epoch milliseconds
}

// SessionMessage is one user or assistant message of a session's conversation.
type SessionMessage struct {
	Type      string          `json:"type"` // "user" | "assistant"
	UUID      string          `json:"uuid"`
	SessionID string          `json:"session_id"`
	Message   json.RawMessage `json:"message"`
	// ParentToolUseID, for subagent messages, is the id of the Agent tool_use
	// in the parent session that spawned the subagent (from its metadata).
	// Empty for top-level session messages.
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`
	// ParentAgentID, for a nested subagent, is the agent id of the subagent
	// that spawned it. Empty otherwise.
	ParentAgentID string `json:"parent_agent_id,omitempty"`
}

// --- Paths --------------------------------------------------------------------

// SessionsDir returns the directory holding session files for the given
// working directory (the current directory when empty), under the config dir
// (CLAUDE_CONFIG_DIR, else ~/.claude).
func SessionsDir(directory string) (string, error) {
	projects, err := projectsDirFor(nil)
	if err != nil {
		return "", err
	}
	return filepath.Join(projects, ProjectKeyForDirectory(directory)), nil
}

// claudeConfigDir returns the Claude config directory: CLAUDE_CONFIG_DIR from
// env (the options env passed to the subprocess), then from the process
// environment, then ~/.claude.
func claudeConfigDir(env map[string]string) (string, error) {
	if dir := env["CLAUDE_CONFIG_DIR"]; dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// projectsDirFor returns the projects directory (the parent of all
// per-project session dirs) under the config dir env resolves to.
func projectsDirFor(env map[string]string) (string, error) {
	dir, err := claudeConfigDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects"), nil
}

// canonicalizePath resolves a directory to the absolute, symlink-free form the
// CLI keys projects by; "" means the current directory. On failure the input
// is returned unchanged. The official SDK also NFC-normalizes, which only
// matters on filesystems that store decomposed Unicode names.
func canonicalizePath(d string) string {
	if d == "" {
		d = "."
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return d
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// sanitizePath replaces non-alphanumeric characters with hyphens, appending a
// djb2 base-36 hash suffix when the result exceeds the length limit (matching
// the official SDK).
func sanitizePath(name string) string {
	sanitized := sanitizeRe.ReplaceAllString(name, "-")
	if len(sanitized) <= maxSanitizedLength {
		return sanitized
	}
	return sanitized[:maxSanitizedLength] + "-" + simpleHash(name)
}

// simpleHash is the djb2 variant used by the official SDK and the CLI: a
// 32-bit signed hash, absolute value, in base 36.
func simpleHash(s string) string {
	var h int32
	for _, c := range s {
		h = (h << 5) - h + c
	}
	v := int64(h)
	if v < 0 {
		v = -v
	}
	return strconv.FormatInt(v, 36)
}

// findProjectDir returns the project directory for a canonical project path.
// For paths past the length limit it falls back to a prefix match, since the
// CLI's hash suffix can differ from the SDK's.
func findProjectDir(projectPath string) string {
	projects, err := projectsDirFor(nil)
	if err != nil {
		return ""
	}
	sanitized := sanitizePath(projectPath)
	exact := filepath.Join(projects, sanitized)
	if isDir(exact) {
		return exact
	}
	if len(sanitized) <= maxSanitizedLength {
		return ""
	}
	prefix := sanitized[:maxSanitizedLength] + "-"
	entries, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return filepath.Join(projects, e.Name())
		}
	}
	return ""
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// projectDirs lists every directory under the projects dir.
func projectDirs() []string {
	projects, err := projectsDirFor(nil)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(projects, e.Name()))
		}
	}
	return out
}

// worktreePaths returns the absolute worktree paths of the git repository
// containing cwd, or nil if git is unavailable or cwd is not in a repository.
func worktreePaths(cwd string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// candidateProjectDirs returns the project directories to search for a
// session: for a directory, its project dir and then those of its git
// worktrees; with no directory, every project dir. Each candidate comes with
// the project path it stands for ("" when unknown).
func candidateProjectDirs(directory string) (dirs, projectPaths []string) {
	if directory == "" {
		for _, d := range projectDirs() {
			dirs = append(dirs, d)
			projectPaths = append(projectPaths, "")
		}
		return dirs, projectPaths
	}
	canonical := canonicalizePath(directory)
	if d := findProjectDir(canonical); d != "" {
		dirs = append(dirs, d)
		projectPaths = append(projectPaths, canonical)
	}
	for _, wt := range worktreePaths(canonical) {
		if wt == canonical {
			continue
		}
		if d := findProjectDir(wt); d != "" {
			dirs = append(dirs, d)
			projectPaths = append(projectPaths, wt)
		}
	}
	return dirs, projectPaths
}

// resolveSessionFile returns the path of the first non-empty <id>.jsonl among
// the candidate project dirs, with its project dir, or "" if none.
func resolveSessionFile(sessionID, directory string) (path, projectDir string) {
	dirs, _ := candidateProjectDirs(directory)
	for _, d := range dirs {
		p := filepath.Join(d, sessionID+".jsonl")
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return p, d
		}
	}
	return "", ""
}

// --- Lite metadata --------------------------------------------------------------

// liteSessionFile is a session file's head, tail, mtime, and size.
type liteSessionFile struct {
	mtime int64
	size  int64
	head  string
	tail  string
}

// readSessionLite stats a session file and reads its head and tail. It returns
// false on any error or for an empty file.
func readSessionLite(path string) (liteSessionFile, bool) {
	f, err := os.Open(path)
	if err != nil {
		return liteSessionFile{}, false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return liteSessionFile{}, false
	}
	head := make([]byte, liteReadBufSize)
	n, err := io.ReadFull(f, head)
	if n == 0 && err != nil {
		return liteSessionFile{}, false
	}
	head = head[:n]
	lite := liteSessionFile{mtime: st.ModTime().UnixMilli(), size: st.Size(), head: decodeLossy(head)}
	if off := st.Size() - liteReadBufSize; off > 0 {
		tail := make([]byte, liteReadBufSize)
		m, _ := f.ReadAt(tail, off)
		lite.tail = decodeLossy(tail[:m])
	} else {
		lite.tail = lite.head
	}
	return lite, true
}

func decodeLossy(b []byte) string { return strings.ToValidUTF8(string(b), "\uFFFD") }

// jsonlToLite builds the lite shape from an in-memory JSONL string, with the
// same byte windows the disk path reads.
func jsonlToLite(jsonl []byte, mtime int64) liteSessionFile {
	size := int64(len(jsonl))
	head := jsonl
	if len(head) > liteReadBufSize {
		head = head[:liteReadBufSize]
	}
	lite := liteSessionFile{mtime: mtime, size: size, head: decodeLossy(head)}
	if size > liteReadBufSize {
		lite.tail = decodeLossy(jsonl[size-liteReadBufSize:])
	} else {
		lite.tail = lite.head
	}
	return lite
}

// unescapeJSONString unescapes a JSON string value extracted as raw text.
func unescapeJSONString(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	var s string
	if json.Unmarshal([]byte(`"`+raw+`"`), &s) != nil {
		return raw
	}
	return s
}

// scanJSONStringValue returns the string value starting at start (just past
// the opening quote), honoring escapes, and the index after its closing
// quote; ok is false if the value is unterminated.
func scanJSONStringValue(text string, start int) (value string, next int, ok bool) {
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			return unescapeJSONString(text[start:i]), i + 1, true
		}
	}
	return "", len(text), false
}

// extractJSONStringField finds the first "key":"value" (or "key": "value") in
// text without a full parse; it works on truncated lines.
func extractJSONStringField(text, key string) (string, bool) {
	for _, pattern := range []string{`"` + key + `":"`, `"` + key + `": "`} {
		idx := strings.Index(text, pattern)
		if idx < 0 {
			continue
		}
		if v, _, ok := scanJSONStringValue(text, idx+len(pattern)); ok {
			return v, true
		}
	}
	return "", false
}

// extractLastJSONStringField is extractJSONStringField for the last occurrence.
func extractLastJSONStringField(text, key string) (string, bool) {
	var last string
	found := false
	for _, pattern := range []string{`"` + key + `":"`, `"` + key + `": "`} {
		from := 0
		for {
			idx := strings.Index(text[from:], pattern)
			if idx < 0 {
				break
			}
			v, next, ok := scanJSONStringValue(text, from+idx+len(pattern))
			if ok {
				last, found = v, true
			}
			from = next
			if from >= len(text) {
				break
			}
		}
	}
	return last, found
}

// firstNonEmpty returns the first non-empty value, mirroring Python's `a or b`.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func lastField(text, key string) string  { v, _ := extractLastJSONStringField(text, key); return v }
func firstField(text, key string) string { v, _ := extractJSONStringField(text, key); return v }

// extractFirstPromptFromHead returns the first meaningful user prompt in a
// JSONL head chunk, skipping tool results, meta and compact-summary messages,
// slash commands (whose name is the fallback), and auto-generated text.
// Truncated to 200 characters.
func extractFirstPromptFromHead(head string) string {
	commandFallback := ""
	for _, line := range strings.Split(head, "\n") {
		if !strings.Contains(line, `"type":"user"`) && !strings.Contains(line, `"type": "user"`) {
			continue
		}
		if strings.Contains(line, `"tool_result"`) ||
			strings.Contains(line, `"isMeta":true`) || strings.Contains(line, `"isMeta": true`) ||
			strings.Contains(line, `"isCompactSummary":true`) || strings.Contains(line, `"isCompactSummary": true`) {
			continue
		}
		var entry map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &entry) != nil || jsonString(entry["type"]) != "user" {
			continue
		}
		var message map[string]json.RawMessage
		if json.Unmarshal(entry["message"], &message) != nil || message == nil {
			continue
		}
		for _, raw := range contentTexts(message["content"]) {
			result := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
			if result == "" {
				continue
			}
			if m := commandNameRe.FindStringSubmatch(result); m != nil {
				if commandFallback == "" {
					commandFallback = m[1]
				}
				continue
			}
			if skipFirstPromptRe.MatchString(result) {
				continue
			}
			return truncateRunes(result, 200)
		}
	}
	return commandFallback
}

// contentTexts returns the text of a message content: the string itself, or
// the text of each text block.
func contentTexts(content json.RawMessage) []string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return []string{s}
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if jsonString(b["type"]) != "text" {
			continue
		}
		var t string
		if json.Unmarshal(b["text"], &t) == nil {
			out = append(out, t)
		}
	}
	return out
}

// jsonString decodes raw as a string, or returns "".
func jsonString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// truncateRunes truncates s to at most n runes, trimming trailing whitespace
// and appending an ellipsis when cut.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRightFunc(string(r[:n]), unicode.IsSpace) + "\u2026"
}

// isoToMillis parses an ISO-8601 timestamp into epoch milliseconds; ok is false
// when it does not parse. A timestamp without a zone is local time.
func isoToMillis(s string) (int64, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli(), true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

// parseSessionInfoFromLite derives session metadata from a lite read. ok is
// false for sidechain sessions and for metadata-only sessions with no summary.
func parseSessionInfoFromLite(sessionID string, lite liteSessionFile, projectPath string) (SDKSessionInfo, bool) {
	head, tail := lite.head, lite.tail
	firstLine := head
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		firstLine = head[:i]
	}
	if strings.Contains(firstLine, `"isSidechain":true`) || strings.Contains(firstLine, `"isSidechain": true`) {
		return SDKSessionInfo{}, false
	}

	// A user-set title wins over an AI-generated one; the head covers short
	// sessions whose title entry is not in the tail.
	customTitle := firstNonEmpty(lastField(tail, "customTitle"), lastField(head, "customTitle"),
		lastField(tail, "aiTitle"), lastField(head, "aiTitle"))
	firstPrompt := extractFirstPromptFromHead(head)
	summary := firstNonEmpty(customTitle, lastField(tail, "lastPrompt"), lastField(tail, "summary"), firstPrompt)
	if summary == "" {
		return SDKSessionInfo{}, false
	}

	info := SDKSessionInfo{
		SessionID:    sessionID,
		Summary:      summary,
		LastModified: lite.mtime,
		FileSize:     lite.size,
		CustomTitle:  customTitle,
		FirstPrompt:  firstPrompt,
		GitBranch:    firstNonEmpty(lastField(tail, "gitBranch"), firstField(head, "gitBranch")),
		Cwd:          firstNonEmpty(firstField(head, "cwd"), projectPath),
	}
	// Only {"type":"tag"} lines carry the session tag; a bare scan would match
	// tool inputs (git tags, Docker tags).
	lines := strings.Split(tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], `{"type":"tag"`) {
			info.Tag = lastField(lines[i], "tag")
			break
		}
	}
	if ts, ok := extractJSONStringField(head, "timestamp"); ok {
		if ms, ok := isoToMillis(ts); ok {
			info.CreatedAt = ms
		}
	}
	return info, true
}

// --- Listing --------------------------------------------------------------------

// ListSessionsOption configures [ListSessions].
type ListSessionsOption func(*listSessionsConfig)

type listSessionsConfig struct{ includeWorktrees bool }

// ListIncludeWorktrees sets whether listing a directory inside a git repository
// also includes sessions from the repository's other worktrees. Default true.
func ListIncludeWorktrees(v bool) ListSessionsOption {
	return func(c *listSessionsConfig) { c.includeWorktrees = v }
}

// ListSessions returns session metadata, newest first. With a directory it
// lists that project (and, by default, its git worktrees); with "" it lists
// every project. A limit of 0 means no limit; offset skips that many.
// Sidechain sessions and sessions with no title or prompt are omitted.
func ListSessions(directory string, limit, offset int, opts ...ListSessionsOption) ([]SDKSessionInfo, error) {
	cfg := listSessionsConfig{includeWorktrees: true}
	for _, o := range opts {
		o(&cfg)
	}
	if directory == "" {
		var all []SDKSessionInfo
		for _, d := range projectDirs() {
			all = append(all, readSessionsFromDir(d, "")...)
		}
		return sortLimitOffset(dedupeBySessionID(all), limit, offset), nil
	}
	return listSessionsForProject(directory, limit, offset, cfg.includeWorktrees), nil
}

func listSessionsForProject(directory string, limit, offset int, includeWorktrees bool) []SDKSessionInfo {
	canonical := canonicalizePath(directory)
	var worktrees []string
	if includeWorktrees {
		worktrees = worktreePaths(canonical)
	}
	if len(worktrees) <= 1 {
		d := findProjectDir(canonical)
		if d == "" {
			return nil
		}
		return sortLimitOffset(readSessionsFromDir(d, canonical), limit, offset)
	}

	caseInsensitive := runtime.GOOS == "windows"
	fold := func(s string) string {
		if caseInsensitive {
			return strings.ToLower(s)
		}
		return s
	}
	type indexed struct{ path, prefix string }
	var wts []indexed
	for _, wt := range worktrees {
		wts = append(wts, indexed{wt, fold(sanitizePath(wt))})
	}
	// Longest prefix first, so the most specific worktree wins.
	sort.SliceStable(wts, func(i, j int) bool { return len(wts[i].prefix) > len(wts[j].prefix) })

	var all []SDKSessionInfo
	seen := map[string]bool{}
	// The directory itself always counts, even a subdirectory of a worktree
	// root that no worktree prefix matches.
	if d := findProjectDir(canonical); d != "" {
		seen[fold(filepath.Base(d))] = true
		all = append(all, readSessionsFromDir(d, canonical)...)
	}
	for _, d := range projectDirs() {
		name := fold(filepath.Base(d))
		if seen[name] {
			continue
		}
		for _, wt := range wts {
			// Prefix matching only for truncated names with a hash suffix; a
			// short path must match exactly, so /p does not match /p-foo.
			if name == wt.prefix || (len(wt.prefix) >= maxSanitizedLength && strings.HasPrefix(name, wt.prefix+"-")) {
				seen[name] = true
				all = append(all, readSessionsFromDir(d, wt.path)...)
				break
			}
		}
	}
	return sortLimitOffset(dedupeBySessionID(all), limit, offset)
}

// readSessionsFromDir reads the metadata of every session file in a project
// dir, skipping sidechain and metadata-only sessions.
func readSessionsFromDir(projectDir, projectPath string) []SDKSessionInfo {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}
	var out []SDKSessionInfo
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || !uuidRE.MatchString(id) {
			continue
		}
		lite, ok := readSessionLite(filepath.Join(projectDir, e.Name()))
		if !ok {
			continue
		}
		if info, ok := parseSessionInfoFromLite(id, lite, projectPath); ok {
			out = append(out, info)
		}
	}
	return out
}

// dedupeBySessionID keeps the newest entry per session id.
func dedupeBySessionID(infos []SDKSessionInfo) []SDKSessionInfo {
	byID := map[string]int{}
	var out []SDKSessionInfo
	for _, s := range infos {
		if i, ok := byID[s.SessionID]; ok {
			if s.LastModified > out[i].LastModified {
				out[i] = s
			}
			continue
		}
		byID[s.SessionID] = len(out)
		out = append(out, s)
	}
	return out
}

func sortLimitOffset(infos []SDKSessionInfo, limit, offset int) []SDKSessionInfo {
	sort.SliceStable(infos, func(i, j int) bool { return infos[i].LastModified > infos[j].LastModified })
	return page(infos, limit, offset)
}

// page applies offset then limit (0 = none), as the official SDK does.
func page[T any](items []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(items) {
			return nil
		}
		items = items[offset:]
	}
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}

// GetSessionInfo returns metadata for one session. With a directory it looks
// in that project and its git worktrees; with "" it searches every project.
// It returns [ErrSessionNotFound] when the session is missing, is a sidechain,
// or has no summary.
func GetSessionInfo(sessionID, directory string) (SDKSessionInfo, error) {
	if !uuidRE.MatchString(sessionID) {
		return SDKSessionInfo{}, ErrSessionNotFound
	}
	dirs, projectPaths := candidateProjectDirs(directory)
	for i, d := range dirs {
		lite, ok := readSessionLite(filepath.Join(d, sessionID+".jsonl"))
		if !ok {
			continue
		}
		if info, ok := parseSessionInfoFromLite(sessionID, lite, projectPaths[i]); ok {
			return info, nil
		}
		return SDKSessionInfo{}, ErrSessionNotFound
	}
	return SDKSessionInfo{}, ErrSessionNotFound
}

// --- Transcripts ----------------------------------------------------------------

// transcriptEntryTypes are the entry types that carry uuid/parentUuid links.
var transcriptEntryTypes = map[string]bool{
	"user": true, "assistant": true, "progress": true, "system": true, "attachment": true,
}

// transcriptEntry is a parsed transcript line, kept as raw fields so every key
// survives and truthiness follows the JSON values.
type transcriptEntry map[string]json.RawMessage

func (e transcriptEntry) str(key string) string { return jsonString(e[key]) }

// truthy reports whether key holds a truthy JSON value (not absent, null,
// false, 0, "", [], or {}).
func (e transcriptEntry) truthy(key string) bool {
	raw := bytes.TrimSpace(e[key])
	switch string(raw) {
	case "", "null", "false", "0", `""`, "[]", "{}":
		return false
	}
	if f, err := strconv.ParseFloat(string(raw), 64); err == nil {
		return f != 0
	}
	return true
}

// hasStringUUID reports whether the entry has a string uuid.
func (e transcriptEntry) hasStringUUID() bool {
	raw := bytes.TrimSpace(e["uuid"])
	return len(raw) > 0 && raw[0] == '"'
}

// parseTranscriptEntries parses JSONL into transcript entries, keeping only
// transcript types with a string uuid and skipping corrupt lines.
func parseTranscriptEntries(content []byte) []transcriptEntry {
	var out []transcriptEntry
	for _, line := range bytes.Split(content, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e transcriptEntry
		if json.Unmarshal(line, &e) != nil || e == nil {
			continue
		}
		if transcriptEntryTypes[e.str("type")] && e.hasStringUUID() {
			out = append(out, e)
		}
	}
	return out
}

// filterTranscriptEntries is parseTranscriptEntries for store entries.
func filterTranscriptEntries(entries []SessionStoreEntry) []transcriptEntry {
	var out []transcriptEntry
	for _, se := range entries {
		var e transcriptEntry
		if json.Unmarshal(se.Data, &e) != nil || e == nil {
			continue
		}
		if transcriptEntryTypes[e.str("type")] && e.hasStringUUID() {
			out = append(out, e)
		}
	}
	return out
}

// buildConversationChain finds the main-chain leaf and walks parentUuid back
// to the root, returning entries root first. logicalParentUuid (on compact
// boundaries) is not followed: the compact summary replaces what came before.
func buildConversationChain(entries []transcriptEntry) []transcriptEntry {
	if len(entries) == 0 {
		return nil
	}
	byUUID := make(map[string]transcriptEntry, len(entries))
	index := make(map[string]int, len(entries))
	parents := map[string]bool{}
	for i, e := range entries {
		byUUID[e.str("uuid")] = e
		index[e.str("uuid")] = i
		if p := e.str("parentUuid"); p != "" {
			parents[p] = true
		}
	}

	// From each terminal (no child points to it), walk back to the nearest
	// user or assistant entry.
	var leaves []transcriptEntry
	for _, t := range entries {
		if parents[t.str("uuid")] {
			continue
		}
		seen := map[string]bool{}
		for cur := t; cur != nil; {
			uid := cur.str("uuid")
			if seen[uid] {
				break
			}
			seen[uid] = true
			if typ := cur.str("type"); typ == "user" || typ == "assistant" {
				leaves = append(leaves, cur)
				break
			}
			cur = byUUID[cur.str("parentUuid")]
		}
	}
	if len(leaves) == 0 {
		return nil
	}

	// Prefer the newest leaf on the main chain (not sidechain, team, or meta).
	var main []transcriptEntry
	for _, l := range leaves {
		if !l.truthy("isSidechain") && !l.truthy("teamName") && !l.truthy("isMeta") {
			main = append(main, l)
		}
	}
	pick := func(c []transcriptEntry) transcriptEntry {
		best := c[0]
		for _, cur := range c[1:] {
			if index[cur.str("uuid")] > index[best.str("uuid")] {
				best = cur
			}
		}
		return best
	}
	leaf := pick(leaves)
	if len(main) > 0 {
		leaf = pick(main)
	}
	return walkToRoot(leaf, byUUID)
}

// walkToRoot follows parentUuid from leaf and returns the chain root first.
func walkToRoot(leaf transcriptEntry, byUUID map[string]transcriptEntry) []transcriptEntry {
	var chain []transcriptEntry
	seen := map[string]bool{}
	for cur := leaf; cur != nil; {
		uid := cur.str("uuid")
		if seen[uid] {
			break
		}
		seen[uid] = true
		chain = append(chain, cur)
		cur = byUUID[cur.str("parentUuid")]
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// isVisibleMessage reports whether a chain entry is returned as a message.
// Compact summaries are included: after compaction they are the only record
// of what came before.
func isVisibleMessage(e transcriptEntry) bool {
	if typ := e.str("type"); typ != "user" && typ != "assistant" {
		return false
	}
	return !e.truthy("isMeta") && !e.truthy("isSidechain") && !e.truthy("teamName")
}

func toSessionMessage(e transcriptEntry, parentToolUseID, parentAgentID string) SessionMessage {
	typ := "assistant"
	if e.str("type") == "user" {
		typ = "user"
	}
	msg := e["message"]
	if msg == nil {
		msg = json.RawMessage("null")
	}
	return SessionMessage{
		Type:            typ,
		UUID:            e.str("uuid"),
		SessionID:       e.str("sessionId"),
		Message:         append(json.RawMessage(nil), msg...),
		ParentToolUseID: parentToolUseID,
		ParentAgentID:   parentAgentID,
	}
}

func entriesToSessionMessages(entries []transcriptEntry, limit, offset int) []SessionMessage {
	var msgs []SessionMessage
	for _, e := range buildConversationChain(entries) {
		if isVisibleMessage(e) {
			msgs = append(msgs, toSessionMessage(e, "", ""))
		}
	}
	return page(msgs, limit, offset)
}

// GetSessionMessages returns a session's conversation, oldest first: the
// transcript's main chain rebuilt from parentUuid links, as user and assistant
// messages. With a directory it looks in that project and its git worktrees;
// with "" it searches every project. A missing session yields no messages.
func GetSessionMessages(sessionID, directory string, limit, offset int) ([]SessionMessage, error) {
	if !uuidRE.MatchString(sessionID) {
		return nil, nil
	}
	dirs, _ := candidateProjectDirs(directory)
	for _, d := range dirs {
		content, err := os.ReadFile(filepath.Join(d, sessionID+".jsonl"))
		if err != nil || len(content) == 0 {
			continue
		}
		return entriesToSessionMessages(parseTranscriptEntries(content), limit, offset), nil
	}
	return nil, nil
}

// --- Subagents ------------------------------------------------------------------

// subagentsDir returns <projectDir>/<sessionId>/subagents for a session, or ""
// if the session is not found.
func subagentsDir(sessionID, directory string) string {
	path, _ := resolveSessionFile(sessionID, directory)
	if path == "" {
		return ""
	}
	return filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
}

type agentFile struct{ id, path string }

// collectAgentFiles finds agent-<id>.jsonl files under dir, recursively (nested
// runs live under subagents/workflows/<runId>/), in name order.
func collectAgentFiles(dir string) []agentFile {
	var out []agentFile
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			out = append(out, collectAgentFiles(p)...)
			continue
		}
		if name := e.Name(); e.Type().IsRegular() && strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl") {
			out = append(out, agentFile{strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl"), p})
		}
	}
	return out
}

// buildSubagentChain returns a subagent transcript's chain, root first.
// Subagent transcripts are linear: the last user or assistant entry is the leaf.
func buildSubagentChain(entries []transcriptEntry) []transcriptEntry {
	byUUID := make(map[string]transcriptEntry, len(entries))
	for _, e := range entries {
		byUUID[e.str("uuid")] = e
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if typ := entries[i].str("type"); typ == "user" || typ == "assistant" {
			return walkToRoot(entries[i], byUUID)
		}
	}
	return nil
}

func entriesToSubagentMessages(entries []transcriptEntry, limit, offset int, parentToolUseID, parentAgentID string) []SessionMessage {
	var msgs []SessionMessage
	for _, e := range buildSubagentChain(entries) {
		if typ := e.str("type"); typ == "user" || typ == "assistant" {
			msgs = append(msgs, toSessionMessage(e, parentToolUseID, parentAgentID))
		}
	}
	return page(msgs, limit, offset)
}

// ListSubagents returns the ids of a session's subagents, from the
// agent-<id>.jsonl transcripts under <session>/subagents/.
func ListSubagents(sessionID, directory string) ([]string, error) {
	if !uuidRE.MatchString(sessionID) {
		return nil, nil
	}
	dir := subagentsDir(sessionID, directory)
	if dir == "" {
		return nil, nil
	}
	var ids []string
	for _, f := range collectAgentFiles(dir) {
		ids = append(ids, f.id)
	}
	return ids, nil
}

// GetSubagentMessages returns a subagent's conversation, oldest first. Each
// message's ParentToolUseID is the Agent tool call that spawned the subagent,
// and ParentAgentID the spawning subagent when nested, both read from the
// agent-<id>.meta.json sidecar (empty if it is missing or unusable).
func GetSubagentMessages(sessionID, agentID, directory string, limit, offset int) ([]SessionMessage, error) {
	if !uuidRE.MatchString(sessionID) || agentID == "" {
		return nil, nil
	}
	dir := subagentsDir(sessionID, directory)
	if dir == "" {
		return nil, nil
	}
	var match string
	for _, f := range collectAgentFiles(dir) {
		if f.id == agentID {
			match = f.path
			break
		}
	}
	if match == "" {
		return nil, nil
	}
	content, err := os.ReadFile(match)
	if err != nil || len(content) == 0 {
		return nil, nil
	}
	meta, _ := readAgentMetadataSidecar(match)
	toolUseID, parentAgentID := parentIDsFromAgentMetadata(meta)
	return entriesToSubagentMessages(parseTranscriptEntries(content), limit, offset, toolUseID, parentAgentID), nil
}

// agentMetadataSidecarPath maps agent-<id>.jsonl to agent-<id>.meta.json.
func agentMetadataSidecarPath(transcriptPath string) string {
	return strings.TrimSuffix(transcriptPath, ".jsonl") + ".meta.json"
}

// readAgentMetadataSidecar reads the .meta.json beside a subagent transcript.
// A missing, invalid, or non-object sidecar reads as nil with no error; other
// read errors are returned.
func readAgentMetadataSidecar(transcriptPath string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(agentMetadataSidecarPath(transcriptPath))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(b, &meta) != nil {
		return nil, nil
	}
	return meta, nil
}

// splitAgentMetadata separates the synthetic agent_metadata entries (a
// subagent's .meta.json in store form; the last one wins) from transcript
// lines.
func splitAgentMetadata(entries []SessionStoreEntry) (map[string]json.RawMessage, []SessionStoreEntry) {
	var metadata map[string]json.RawMessage
	var transcript []SessionStoreEntry
	for _, e := range entries {
		var obj map[string]json.RawMessage
		if json.Unmarshal(e.Data, &obj) == nil && jsonString(obj["type"]) == "agent_metadata" {
			metadata = obj
			continue
		}
		transcript = append(transcript, e)
	}
	return metadata, transcript
}

// parentIDsFromAgentMetadata reads toolUseId and parentAgentId from agent
// metadata (a sidecar or its store entry).
func parentIDsFromAgentMetadata(meta map[string]json.RawMessage) (toolUseID, parentAgentID string) {
	return jsonString(meta["toolUseId"]), jsonString(meta["parentAgentId"])
}
