package transport

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const installHint = "install it with `npm install -g @anthropic-ai/claude-code` " +
	"or set the CLI path explicitly with WithCLIPath"

// Discover locates the `claude` binary the same way Connect does: an explicit
// path is validated and used as-is; otherwise PATH is searched, then common
// install locations. It returns a *CLINotFoundError when nothing is found.
func Discover(explicit string) (string, error) {
	return discoverCLI(explicit)
}

// discoverCLI locates the `claude` binary. An explicit path is validated and
// used as-is; otherwise PATH is searched, then a set of common install
// locations. It returns a *CLINotFoundError when nothing is found, or a
// *BatchCLIRefusedError when the resolved path is a Windows batch script.
//
// The batch check runs on every return path (explicit, PATH, candidates) so it
// covers every route to the executable, including the version probe.
func discoverCLI(explicit string) (string, error) {
	path, err := discoverCLIPath(explicit)
	if err != nil {
		return "", err
	}
	if isWindowsBatchCLI(path) {
		return "", &BatchCLIRefusedError{Path: path}
	}
	return path, nil
}

func discoverCLIPath(explicit string) (string, error) {
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

// isWindowsBatchCLI reports whether path names a .bat/.cmd batch script on
// Windows. Always false off Windows.
//
// Windows has no shebang mechanism: CreateProcess runs batch scripts by
// rewriting the spawn into `cmd.exe /c`, and cmd.exe re-parses the whole
// command line. Go's os/exec quotes arguments for CommandLineToArgvW, and its
// own documentation names cmd.exe "(and thus, all batch files)" as an
// exception to that algorithm, so cmd.exe metacharacters inside an argument
// value reach cmd.exe unescaped and can execute injected commands. Reliable
// escaping for cmd.exe does not exist (%VAR% expands even inside double
// quotes), so refusing is the remediation, the same one Node.js shipped for
// CVE-2024-27980 ("BatBadBut").
//
// In practice this refuses npm's claude.cmd shim, which exec.LookPath returns
// via PATHEXT when no native claude.exe is discoverable.
//
// Deliberately plain string logic rather than path/filepath: filepath parses
// Windows and POSIX paths differently, and this must behave identically on
// POSIX CI hosts and on Windows.
//
// Every path component is classified, not only the last. Win32 opens a path
// after lexical normalization ("." / ".." collapsing, repeated separators,
// position-dependent trailing dot/space trimming), and re-deriving the
// effective final component here would be a race against that ruleset: get one
// rule slightly wrong and a spelling such as `claude.cmd\...\..` resolves to
// claude.cmd on Windows while the simulation lands elsewhere. Refusing whenever
// any component carries a batch extension closes that class outright and costs
// nothing legitimate, since no real claude.exe lives beneath a directory named
// like a batch file.
//
// Within a component, Win32 finds the extension with a last-dot scan over the
// whole component including any NTFS stream spec ("claude:evil.cmd" has
// extension ".cmd"), while a stream spec also opens its base file
// ("claude.cmd:stream" opens claude.cmd), and a drive prefix ("C:claude.cmd")
// rides in the same component. Splitting each component on ":" covers all of
// these; colons cannot appear in real file names, so nothing legitimate is
// over-refused. Trailing dots and spaces, which Windows strips at path
// resolution, are trimmed per segment (the normalization Rust applied for
// CVE-2024-24576), and a bare ".cmd" counts as a batch extension, as Win32
// PathFindExtension treats it.
func isWindowsBatchCLI(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return hasBatchExtension(path)
}

// hasBatchExtension implements the component/segment scan described on
// isWindowsBatchCLI. It is split out so tests can exercise the path logic on
// any host OS.
func hasBatchExtension(path string) bool {
	for _, component := range strings.Split(strings.ReplaceAll(path, `\`, "/"), "/") {
		for _, segment := range strings.Split(component, ":") {
			s := strings.ToLower(strings.TrimRight(segment, ". "))
			if strings.HasSuffix(s, ".bat") || strings.HasSuffix(s, ".cmd") {
				return true
			}
		}
	}
	return false
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
	// Require a regular file — reject directories, FIFOs, devices, sockets, and
	// other irregular modes that could be an execution vector.
	if !info.Mode().IsRegular() {
		return false
	}
	return hasExecPermission(info.Mode())
}
