package claude

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shindakun/agent-sdk-go/internal/transport"
)

// CLIVersion returns the version reported by the installed `claude` binary
// (from `claude --version`). cliPath may be empty to use discovery; see
// [WithCLIPath] for the same resolution rules. The returned string is the bare
// version (for example "2.1.159"), parsed from the CLI's output.
func CLIVersion(ctx context.Context, cliPath string) (string, error) {
	version, _, err := cliVersionProbe(ctx, cliPath)
	return version, err
}

// cliVersionProbe resolves the CLI and returns its version and path. It is a
// package var so tests can substitute it.
var cliVersionProbe = func(ctx context.Context, cliPath string) (version, path string, err error) {
	path, err = transport.Discover(cliPath)
	if err != nil {
		return "", "", mapTransportError(err)
	}
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", path, &ConnectionError{Err: err}
	}
	return parseCLIVersion(string(out)), path, nil
}

const (
	minimumCLIVersion                = "2.0.0"
	verbatimPromptsMinimumCLIVersion = "2.1.248"
)

// cliVersionWarnings runs the connect-time version check the official SDK
// runs: a warning when the CLI is older than the minimum it supports, and one
// when [WithVerbatimPrompts] is on but the CLI predates client_composed. Any
// failure to determine the version yields no warnings.
func (o *Options) cliVersionWarnings(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	version, path, err := cliVersionProbe(ctx, o.cliPath)
	if err != nil {
		return nil
	}
	v, ok := parseVersionTriple(version)
	if !ok {
		return nil
	}
	var warnings []string
	if lessVersion(v, minimumCLIVersion) {
		warnings = append(warnings, fmt.Sprintf(
			"claude: warning: Claude Code version %s at %s is unsupported in the Agent SDK. "+
				"Minimum required version is %s. Some features may not work correctly.",
			version, path, minimumCLIVersion))
	}
	if o.verbatimPrompts && lessVersion(v, verbatimPromptsMinimumCLIVersion) {
		warnings = append(warnings, fmt.Sprintf(
			"claude: warning: verbatim prompts are enabled, but Claude Code version %s at %s ignores them: "+
				"prompts will still have @path mentions expanded and slash commands dispatched. "+
				"Claude Code %s or later is required.",
			version, path, verbatimPromptsMinimumCLIVersion))
	}
	return warnings
}

var versionTripleRE = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)`)

// parseVersionTriple parses the leading major.minor.patch of a version string.
func parseVersionTriple(s string) ([3]int, bool) {
	var v [3]int
	m := versionTripleRE.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// lessVersion reports whether v is older than min (a well-formed x.y.z).
func lessVersion(v [3]int, min string) bool {
	m, _ := parseVersionTriple(min)
	for i := range v {
		if v[i] != m[i] {
			return v[i] < m[i]
		}
	}
	return false
}

// parseCLIVersion extracts the leading version token from `claude --version`
// output (e.g. "2.1.159 (Claude Code)" -> "2.1.159").
func parseCLIVersion(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		s = s[:i]
	}
	return s
}

// CheckCLIVersion compares the installed CLI version against
// [SupportedCLIVersion]. It returns the installed version and a non-nil error
// (a [CLIVersionMismatch]) when they differ, so callers can warn without
// failing. A nil error means the installed CLI matches the verified version.
func CheckCLIVersion(ctx context.Context, cliPath string) (string, error) {
	got, err := CLIVersion(ctx, cliPath)
	if err != nil {
		return "", err
	}
	if got != SupportedCLIVersion {
		return got, &CLIVersionMismatch{Installed: got, Supported: SupportedCLIVersion}
	}
	return got, nil
}

// CLIVersionMismatch reports that the installed CLI differs from the version
// this SDK was verified against. It is advisory — the SDK still operates.
type CLIVersionMismatch struct {
	Installed string
	Supported string
}

func (e *CLIVersionMismatch) Error() string {
	return fmt.Sprintf("claude: installed CLI version %q differs from the verified version %q",
		e.Installed, e.Supported)
}
