//go:build integration || e2e

package claude

import (
	"os"
	"testing"
)

// inheritedSessionEnv names variables a parent Claude Code session exports to
// its child processes. When these tests run inside such a session (for
// example a Claude Code cloud session), the CLI they spawn would inherit them:
// CLAUDE_CODE_REMOTE_SESSION_ID makes it reuse the parent's session id, so it
// writes to the parent's transcript and its own subagent transcripts are not
// found where the tests look. The rest identify the parent session or mark the
// process as running inside Claude Code (upstream's transport drops CLAUDECODE
// for that reason).
var inheritedSessionEnv = []string{
	"CLAUDE_CODE_REMOTE_SESSION_ID",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDECODE",
}

// clearInheritedSessionEnv unsets inheritedSessionEnv for the rest of the
// test, restoring them afterwards. Like t.Setenv, it must not be used in
// parallel tests.
func clearInheritedSessionEnv(t *testing.T) {
	t.Helper()
	for _, k := range inheritedSessionEnv {
		if _, ok := os.LookupEnv(k); ok {
			t.Setenv(k, "") // registers the restore
			if err := os.Unsetenv(k); err != nil {
				t.Fatal(err)
			}
		}
	}
}
