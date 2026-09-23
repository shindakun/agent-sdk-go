package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ImportOption configures [ImportSessionToStore].
type ImportOption func(*importConfig)

type importConfig struct {
	directory        string
	batchSize        int
	includeSubagents bool
}

// ImportDirectory sets the project directory to find the session in. When
// unset, every project directory is searched.
func ImportDirectory(dir string) ImportOption {
	return func(c *importConfig) { c.directory = dir }
}

// ImportBatchSize sets the maximum entries per Append call (default 500).
func ImportBatchSize(n int) ImportOption {
	return func(c *importConfig) { c.batchSize = n }
}

// ImportIncludeSubagents sets whether subagent transcripts under
// <session>/subagents/ and their .meta.json sidecars are imported too
// (default true).
func ImportIncludeSubagents(v bool) ImportOption {
	return func(c *importConfig) { c.includeSubagents = v }
}

// importMaxPendingBytes flushes a batch once its lines reach this size.
const importMaxPendingBytes = 1 << 20

// ImportSessionToStore replays a local session transcript into a store,
// appending in batches of up to the batch size or 1 MiB of lines. Use it to
// move local sessions to a remote store, or to fill a gap a
// [MirrorErrorMessage] reported. Stores should treat each entry's uuid as an
// idempotency key so a re-import is safe.
//
// The project key is the name of the on-disk project directory the file was
// found in, the key the live mirror would have used, so an imported session
// resumes like a mirrored one. Subagent transcripts are imported under their
// subagents/... subpaths, each followed by its .meta.json sidecar as an
// agent_metadata entry.
func ImportSessionToStore(ctx context.Context, sessionID string, store SessionStore, opts ...ImportOption) error {
	cfg := importConfig{includeSubagents: true, batchSize: mirrorMaxPendingEntries}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.batchSize <= 0 {
		cfg.batchSize = mirrorMaxPendingEntries
	}
	if !uuidRE.MatchString(sessionID) {
		return invalidSessionID(sessionID)
	}
	path, projectDir := resolveSessionFile(sessionID, cfg.directory)
	if path == "" {
		return notFound(sessionID, "")
	}
	projectKey := filepath.Base(projectDir)

	if err := appendJSONLFileInBatches(ctx, path, SessionKey{ProjectKey: projectKey, SessionID: sessionID}, store, cfg.batchSize); err != nil {
		return err
	}
	if !cfg.includeSubagents {
		return nil
	}

	sessionDir := strings.TrimSuffix(path, ".jsonl")
	for _, file := range collectJSONLFiles(filepath.Join(sessionDir, "subagents")) {
		rel, err := filepath.Rel(sessionDir, file)
		if err != nil {
			return err
		}
		key := SessionKey{ProjectKey: projectKey, SessionID: sessionID,
			Subpath: strings.TrimSuffix(filepath.ToSlash(rel), ".jsonl")}
		if err := appendJSONLFileInBatches(ctx, file, key, store, cfg.batchSize); err != nil {
			return err
		}
		// Live mirrors receive the sidecar as agent_metadata entries; the
		// on-disk .jsonl does not contain them. Importing it lets a resume
		// restore the subagent's metadata.
		meta, err := readAgentMetadataSidecar(file)
		if err != nil {
			return err
		}
		if meta != nil {
			meta["type"] = json.RawMessage(`"agent_metadata"`)
			if err := store.Append(ctx, key, []SessionStoreEntry{{Data: marshalTypeFirst(meta)}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// appendJSONLFileInBatches streams a JSONL file into the store, skipping blank
// lines. A line that is not JSON fails the import.
func appendJSONLFileInBatches(ctx context.Context, path string, key SessionKey, store SessionStore, batchSize int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var batch []SessionStoreEntry
	n := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := store.Append(ctx, key, batch)
		batch, n = nil, 0
		return err
	}
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		line = []byte(strings.TrimRight(string(line), "\n"))
		if len(line) > 0 {
			if !json.Valid(line) {
				return fmt.Errorf("claude: %s: invalid JSON line", path)
			}
			batch = append(batch, SessionStoreEntry{Data: line})
			n += len(line)
			if len(batch) >= batchSize || n >= importMaxPendingBytes {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err != nil {
			break
		}
	}
	return flush()
}

// collectJSONLFiles returns every .jsonl file under dir, recursively, in name
// order per directory.
func collectJSONLFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			out = append(out, collectJSONLFiles(p)...)
		} else if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, p)
		}
	}
	return out
}
