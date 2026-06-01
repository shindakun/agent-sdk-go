package claude

import "testing"

func TestThinkingAdaptive(t *testing.T) {
	o := newOptions(WithThinkingConfig(ThinkingConfigAdaptive{Type: "adaptive"}))
	args, err := o.buildArgs()
	if err != nil {
		t.Fatal(err)
	}
	if !argsContainPair(args, "--thinking", "adaptive") {
		t.Errorf("adaptive should emit --thinking adaptive; args=%v", args)
	}
}

func TestThinkingAdaptiveWithDisplay(t *testing.T) {
	o := newOptions(WithThinkingConfig(ThinkingConfigAdaptive{Type: "adaptive", Display: ThinkingDisplaySummarized}))
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--thinking", "adaptive") {
		t.Errorf("missing --thinking adaptive; args=%v", args)
	}
	if !argsContainPair(args, "--thinking-display", "summarized") {
		t.Errorf("missing --thinking-display summarized; args=%v", args)
	}
}

func TestThinkingEnabledEmitsBudgetNotBareThinking(t *testing.T) {
	o := newOptions(WithThinkingConfig(ThinkingConfigEnabled{Type: "enabled", BudgetTokens: 8000}))
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--max-thinking-tokens", "8000") {
		t.Errorf("enabled should emit --max-thinking-tokens 8000; args=%v", args)
	}
	// It must NOT emit a bare --thinking flag (the old bug).
	if argsContainsFlag(args, "--thinking") {
		t.Errorf("enabled must not emit a bare --thinking; args=%v", args)
	}
}

func TestThinkingDisabled(t *testing.T) {
	o := newOptions(WithThinkingConfig(ThinkingConfigDisabled{Type: "disabled"}))
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--thinking", "disabled") {
		t.Errorf("disabled should emit --thinking disabled; args=%v", args)
	}
}

func TestThinkingDeprecatedScalar(t *testing.T) {
	o := newOptions(WithMaxThinkingTokens(4096))
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--max-thinking-tokens", "4096") {
		t.Errorf("scalar path should emit --max-thinking-tokens 4096; args=%v", args)
	}
	if argsContainsFlag(args, "--thinking") {
		t.Errorf("scalar path must not emit --thinking; args=%v", args)
	}
}

func TestThinkingConfigTakesPrecedenceOverScalar(t *testing.T) {
	o := newOptions(
		WithMaxThinkingTokens(4096),
		WithThinkingConfig(ThinkingConfigAdaptive{Type: "adaptive"}),
	)
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--thinking", "adaptive") {
		t.Errorf("config should win; args=%v", args)
	}
	// The deprecated scalar must be suppressed when a config is present.
	if argsContainPair(args, "--max-thinking-tokens", "4096") {
		t.Errorf("scalar should be suppressed when config present; args=%v", args)
	}
}

func TestThinkingDisplayOptionLevel(t *testing.T) {
	o := newOptions(
		WithThinkingConfig(ThinkingConfigEnabled{Type: "enabled", BudgetTokens: 2000}),
		WithThinkingDisplay(ThinkingDisplayOmitted),
	)
	args, _ := o.buildArgs()
	if !argsContainPair(args, "--thinking-display", "omitted") {
		t.Errorf("option-level display should apply; args=%v", args)
	}
}
