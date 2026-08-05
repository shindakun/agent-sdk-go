package claude

import (
	"strings"
	"testing"
)

// TestValidateSkillNameRejects covers names that cannot ride safely in a
// Skill(name) rule. Names go into --allowedTools, which the CLI splits on
// commas and spaces outside parentheses without honoring escapes.
func TestValidateSkillNameRejects(t *testing.T) {
	bad := map[string]string{
		"empty":            "",
		"whitespace only":  "   ",
		"leading space":    " deploy",
		"trailing space":   "deploy ",
		"comma":            "a,b",
		"open paren":       "a(b",
		"close paren":      "a)b",
		"control char":     "a\x01b",
		"DEL":              "a\x7fb",
		"C1 raw byte":      "a\x85b",
		"C1 code point":    "a\u0085b",
		"BOM":              "a\ufeffb",
		"newline":          "a\nb",
		"bare wildcard":    "*",
		"colon wildcard":   "plugin:*",
		"space wildcard":   "plugin *",
		"slash command":    "/deploy",
		"double backslash": `a\\b`,
		"trailing slash":   `a\`,
	}
	for label, name := range bad {
		if err := validateSkillName(name); err == nil {
			t.Errorf("%s: validateSkillName(%q) = nil, want an error", label, name)
		}
	}
}

func TestValidateSkillNameAccepts(t *testing.T) {
	good := []string{
		"deploy",
		"code-review",
		"my_skill",
		"plugin:skill",
		"Skill123",
		"a.b",
	}
	for _, name := range good {
		if err := validateSkillName(name); err != nil {
			t.Errorf("validateSkillName(%q) = %v, want nil", name, err)
		}
	}
}

// A rejected name must fail the build rather than silently corrupt the
// --allowedTools rule list.
func TestBuildArgsRejectsBadSkillName(t *testing.T) {
	o := newOptions(WithSkills("ok-skill", "evil,Bash"))
	args, err := o.buildArgs()
	if err == nil {
		t.Fatalf("buildArgs = nil error, want rejection; args=%v", args)
	}
	if !strings.Contains(err.Error(), "evil,Bash") {
		t.Errorf("error should name the offending skill, got %v", err)
	}
}

// A valid name still produces the Skill(name) rule.
func TestBuildArgsAcceptsGoodSkillName(t *testing.T) {
	o := newOptions(WithSkills("deploy"))
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if !argsContainPair(args, "--allowedTools", "Skill(deploy)") {
		t.Errorf("missing Skill(deploy) rule; args=%v", args)
	}
}
