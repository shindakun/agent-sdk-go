package claude

import "testing"

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

// TestRejectWindowsCmdMetacharacters is a POSIX host, so the guard is inert
// here by design; assert that explicitly so the platform gate stays honest.
func TestRejectWindowsCmdMetacharactersIsPOSIXInert(t *testing.T) {
	if err := rejectWindowsCmdMetacharacters("resume", "R&D notes\n"); err != nil {
		t.Errorf("guard must be inert off Windows, got %v", err)
	}
	o := newOptions(WithResume("R&D notes"))
	if _, err := o.buildArgs(); err != nil {
		t.Errorf("buildArgs must not reject metacharacters off Windows: %v", err)
	}
}
