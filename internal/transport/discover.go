package transport

import (
	"os"
	"os/exec"
	"path/filepath"
)

const installHint = "install it with `npm install -g @anthropic-ai/claude-code` " +
	"or set the CLI path explicitly with WithCLIPath"

// discoverCLI locates the `claude` binary. An explicit path is validated and
// used as-is; otherwise PATH is searched, then a set of common install
// locations. It returns a *CLINotFoundError when nothing is found.
func discoverCLI(explicit string) (string, error) {
	if explicit != "" {
		if isExecutable(explicit) {
			return explicit, nil
		}
		return "", &CLINotFoundError{Path: explicit, Hint: installHint}
	}

	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}

	for _, p := range candidatePaths() {
		if isExecutable(p) {
			return p, nil
		}
	}

	return "", &CLINotFoundError{Hint: installHint}
}

func candidatePaths() []string {
	var paths []string
	home, err := os.UserHomeDir()
	if err == nil {
		paths = append(paths,
			filepath.Join(home, ".claude", "local", "claude"),
			filepath.Join(home, ".npm-global", "bin", "claude"),
			filepath.Join(home, "node_modules", ".bin", "claude"),
			filepath.Join(home, ".local", "bin", "claude"),
		)
	}
	paths = append(paths,
		"/usr/local/bin/claude",
		"/opt/homebrew/bin/claude",
	)
	return paths
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	// Require a regular file with an executable bit — reject directories,
	// FIFOs, devices, sockets, and other irregular modes that could be an
	// execution vector.
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode()&0o111 != 0
}
