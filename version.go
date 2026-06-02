package claude

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/shindakun/agent-sdk-go/internal/transport"
)

// CLIVersion returns the version reported by the installed `claude` binary
// (from `claude --version`). cliPath may be empty to use discovery; see
// [WithCLIPath] for the same resolution rules. The returned string is the bare
// version (for example "2.1.159"), parsed from the CLI's output.
func CLIVersion(ctx context.Context, cliPath string) (string, error) {
	path, err := transport.Discover(cliPath)
	if err != nil {
		return "", mapTransportError(err)
	}
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", &ConnectionError{Err: err}
	}
	return parseCLIVersion(string(out)), nil
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
