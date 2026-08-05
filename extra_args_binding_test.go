package claude

import (
	"runtime"
	"strings"
	"testing"
)

// TestExtraArgsValueBinding pins the argv shape of extraArgs values. A
// dash-leading value must bind to its flag, or the CLI parses it as a separate
// flag (the class the --resume equals form closes). Valueless flags must stay
// bare tokens, and ordinary values keep the two-token form that string-driven
// boolean flags rely on.
func TestExtraArgsValueBinding(t *testing.T) {
	dash := "--version"
	plain := "true"
	o := newOptions(WithExtraArgs(map[string]*string{
		"evil":    &dash,
		"verbose": &plain,
		"bare":    nil,
	}))
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}

	if !argsContainEquals(args, "--evil", dash) {
		t.Errorf("dash-leading value not bound to its flag; args=%v", args)
	}
	for _, a := range args {
		if a == dash {
			t.Errorf("dash-leading value leaked as a standalone token; args=%v", args)
		}
	}
	if !argsContainPair(args, "--verbose", plain) {
		t.Errorf("ordinary value lost the two-token form; args=%v", args)
	}
	if !argsContainsFlag(args, "--bare") {
		t.Errorf("valueless flag missing; args=%v", args)
	}
	for _, a := range args {
		if a == "--bare=" {
			t.Errorf("valueless flag emitted in equals form; args=%v", args)
		}
	}
}

// TestRejectWindowsCmdMetacharacters pins the platform gate in both
// directions: the guard rejects cmd.exe metacharacters on Windows and is inert
// everywhere else. A resume value may be an arbitrary session title, so
// rejecting "R&D notes" off Windows would be a real regression.
func TestRejectWindowsCmdMetacharacters(t *testing.T) {
	const bad = "R&D notes"
	err := rejectWindowsCmdMetacharacters("resume", bad+"\n")
	_, buildErr := newOptions(WithResume(bad)).buildArgs()

	if runtime.GOOS == "windows" {
		if err == nil {
			t.Error("guard must reject cmd.exe metacharacters on Windows")
		} else {
			for _, want := range []string{"&", `\n`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should name the offending character %q, got %v", want, err)
				}
			}
		}
		if buildErr == nil {
			t.Error("buildArgs must reject a metacharacter-bearing resume on Windows")
		}
		return
	}

	if err != nil {
		t.Errorf("guard must be inert off Windows, got %v", err)
	}
	if buildErr != nil {
		t.Errorf("buildArgs must not reject metacharacters off Windows: %v", buildErr)
	}
}
