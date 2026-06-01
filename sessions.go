package claude

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Session management reads Claude Code's on-disk session transcripts. Sessions
// are stored as newline-delimited JSON under
// ~/.claude/projects/<sanitized-cwd>/<session-id>.jsonl, matching the layout
// the official SDK reads. These functions are disk-based and do not require a
// running CLI.

const maxSanitizedLength = 200

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// SDKSessionInfo is metadata about a stored session.
type SDKSessionInfo struct {
	SessionID    string `json:"session_id"`
	Summary      string `json:"summary"`
	LastModified int64  `json:"last_modified"` // epoch milliseconds
	FileSize     int64  `json:"file_size"`
	CustomTitle  string `json:"custom_title,omitempty"`
	FirstPrompt  string `json:"first_prompt,omitempty"`
	GitBranch    string `json:"git_branch,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Tag          string `json:"tag,omitempty"`
	CreatedAt    int64  `json:"created_at,omitempty"`
}

// SessionMessage is one transcript entry from a session file.
type SessionMessage struct {
	Type            string          `json:"type"` // "user" | "assistant"
	UUID            string          `json:"uuid"`
	SessionID       string          `json:"session_id"`
	ParentToolUseID string          `json:"parent_tool_use_id,omitempty"`
	Message         json.RawMessage `json:"message"`
}

// SessionsDir returns the directory holding session files for the given working
// directory, applying the same sanitization the CLI uses. When directory is
// empty, the current working directory is used.
func SessionsDir(directory string) (string, error) {
	if directory == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		directory = wd
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects", sanitizePath(directory)), nil
}

// sanitizePath replaces non-alphanumeric runs with hyphens, appending a djb2
// base-36 hash suffix when the result exceeds the length limit (matching the
// official SDK).
func sanitizePath(name string) string {
	sanitized := sanitizeRe.ReplaceAllString(name, "-")
	if len(sanitized) <= maxSanitizedLength {
		return sanitized
	}
	return sanitized[:maxSanitizedLength] + "-" + simpleHash(name)
}

// simpleHash is the djb2 variant used by the official SDK, in base 36.
func simpleHash(s string) string {
	var h int32
	for _, c := range s {
		h = (h << 5) - h + c
	}
	return strconv.FormatInt(int64(uint32(h)), 36)
}

// ListSessions returns metadata for sessions in directory, newest first. A
// limit of 0 means no limit; offset skips that many entries.
func ListSessions(directory string, limit, offset int) ([]SDKSessionInfo, error) {
	dir, err := SessionsDir(directory)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var infos []SDKSessionInfo
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := readSessionInfo(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip unreadable/partial files
		}
		infos = append(infos, info)
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].LastModified > infos[j].LastModified
	})

	if offset > 0 {
		if offset >= len(infos) {
			return nil, nil
		}
		infos = infos[offset:]
	}
	if limit > 0 && limit < len(infos) {
		infos = infos[:limit]
	}
	return infos, nil
}

// GetSessionInfo returns metadata for a single session by id.
func GetSessionInfo(sessionID, directory string) (SDKSessionInfo, error) {
	dir, err := SessionsDir(directory)
	if err != nil {
		return SDKSessionInfo{}, err
	}
	return readSessionInfo(filepath.Join(dir, sessionID+".jsonl"))
}

// GetSessionMessages reads the conversation transcript for a session. A limit of
// 0 means all messages; offset skips that many entries.
func GetSessionMessages(sessionID, directory string, limit, offset int) ([]SessionMessage, error) {
	dir, err := SessionsDir(directory)
	if err != nil {
		return nil, err
	}
	return readSessionMessages(filepath.Join(dir, sessionID+".jsonl"), limit, offset)
}

// ListSubagents returns the distinct subagent ids referenced in a session.
func ListSubagents(sessionID, directory string) ([]string, error) {
	dir, err := SessionsDir(directory)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	seen := map[string]bool{}
	var ids []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var line struct {
			AgentID string `json:"agentId"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.AgentID == "" {
			continue
		}
		if !seen[line.AgentID] {
			seen[line.AgentID] = true
			ids = append(ids, line.AgentID)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

// GetSubagentMessages reads the transcript for one subagent within a session.
func GetSubagentMessages(sessionID, agentID, directory string, limit, offset int) ([]SessionMessage, error) {
	dir, err := SessionsDir(directory)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []SessionMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var line struct {
			Type            string          `json:"type"`
			AgentID         string          `json:"agentId"`
			UUID            string          `json:"uuid"`
			SessionID       string          `json:"sessionId"`
			ParentToolUseID string          `json:"parentToolUseId"`
			Message         json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.AgentID != agentID {
			continue
		}
		if line.Type != "user" && line.Type != "assistant" {
			continue
		}
		msgs = append(msgs, SessionMessage{
			Type:            line.Type,
			UUID:            line.UUID,
			SessionID:       line.SessionID,
			ParentToolUseID: line.ParentToolUseID,
			Message:         line.Message,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	if offset > 0 {
		if offset >= len(msgs) {
			return nil, nil
		}
		msgs = msgs[offset:]
	}
	if limit > 0 && limit < len(msgs) {
		msgs = msgs[:limit]
	}
	return msgs, nil
}

// readSessionInfo derives metadata from a session file's stat and contents.
func readSessionInfo(path string) (SDKSessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SDKSessionInfo{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return SDKSessionInfo{}, err
	}

	info := SDKSessionInfo{
		SessionID:    sessionIDFromPath(path),
		LastModified: st.ModTime().UnixMilli(),
		FileSize:     st.Size(),
	}

	// Scan lines for metadata. JSONL keys are camelCase.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var line struct {
			Type        string          `json:"type"`
			CustomTitle string          `json:"customTitle"`
			AITitle     string          `json:"aiTitle"`
			Summary     string          `json:"summary"`
			LastPrompt  string          `json:"lastPrompt"`
			GitBranch   string          `json:"gitBranch"`
			Cwd         string          `json:"cwd"`
			Tag         string          `json:"tag"`
			Timestamp   string          `json:"timestamp"`
			Message     json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.CustomTitle != "" {
			info.CustomTitle = line.CustomTitle
		}
		if line.GitBranch != "" {
			info.GitBranch = line.GitBranch
		}
		if line.Cwd != "" {
			info.Cwd = line.Cwd
		}
		if line.Tag != "" {
			info.Tag = line.Tag
		}
		if info.FirstPrompt == "" && line.Type == "user" {
			info.FirstPrompt = firstUserText(line.Message)
		}
		if info.CreatedAt == 0 && line.Timestamp != "" {
			info.CreatedAt = isoToMillis(line.Timestamp)
		}
	}

	info.Summary = pickSummary(info)
	return info, nil
}

func readSessionMessages(path string, limit, offset int) ([]SessionMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var msgs []SessionMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var line struct {
			Type            string          `json:"type"`
			UUID            string          `json:"uuid"`
			SessionID       string          `json:"sessionId"`
			ParentToolUseID string          `json:"parentToolUseId"`
			Message         json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Type != "user" && line.Type != "assistant" {
			continue
		}
		msgs = append(msgs, SessionMessage{
			Type:            line.Type,
			UUID:            line.UUID,
			SessionID:       line.SessionID,
			ParentToolUseID: line.ParentToolUseID,
			Message:         line.Message,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	if offset > 0 {
		if offset >= len(msgs) {
			return nil, nil
		}
		msgs = msgs[offset:]
	}
	if limit > 0 && limit < len(msgs) {
		msgs = msgs[:limit]
	}
	return msgs, nil
}

// isoToMillis parses an ISO-8601 timestamp into epoch milliseconds, returning 0
// on failure.
func isoToMillis(s string) int64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}

func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))]
}

func pickSummary(info SDKSessionInfo) string {
	switch {
	case info.CustomTitle != "":
		return info.CustomTitle
	case info.FirstPrompt != "":
		return info.FirstPrompt
	default:
		return ""
	}
}

// firstUserText extracts the leading text of a user message whose content may be
// a string or an array of text blocks.
func firstUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var env struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(env.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(env.Content, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" {
				return b.Text
			}
		}
	}
	return ""
}
