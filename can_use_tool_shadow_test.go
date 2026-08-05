package claude

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func noopCanUseTool(context.Context, string, json.RawMessage, PermissionContext) (PermissionResult, error) {
	return PermissionAllow{}, nil
}

// TestWholeToolAllowed mirrors the CLI's rule parser: an entry allows a whole
// tool when it has no specifier, or an empty/lone-wildcard one. A real
// specifier only allows matching invocations.
func TestWholeToolAllowed(t *testing.T) {
	whole := map[string]string{
		"Read":      "Read",
		"Read()":    "Read",
		"Read(*)":   "Read",
		"mcp__x__y": "mcp__x__y",
	}
	for entry, want := range whole {
		if got := wholeToolAllowed(entry); got != want {
			t.Errorf("wholeToolAllowed(%q) = %q, want %q", entry, got, want)
		}
	}
	narrow := []string{
		"Bash(ls:*)",
		"Read(/etc/*)",
		"Skill(deploy)",
		"",
		"   ",
		"(orphan)",        // no tool name before the specifier
		"Read(unbalanced", // malformed: the CLI treats it as a literal tool name
	}
	for _, entry := range narrow {
		if got := wholeToolAllowed(entry); got != "" {
			t.Errorf("wholeToolAllowed(%q) = %q, want empty", entry, got)
		}
	}
}

func TestCanUseToolShadowed(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
		want []string
	}{
		{
			name: "no callback means nothing to shadow",
			opts: []Option{WithAllowedTools("Read")},
			want: nil,
		},
		{
			name: "whole-tool entries shadow, narrow ones do not",
			opts: []Option{WithCanUseTool(noopCanUseTool), WithAllowedTools("Read", "Bash(ls:*)", "Write()")},
			want: []string{"Read", "Write"},
		},
		{
			name: "redundant entries are deduped",
			opts: []Option{WithCanUseTool(noopCanUseTool), WithAllowedTools("Read", "Read()", "Read(*)")},
			want: []string{"Read"},
		},
		{
			name: "narrow entries do not shadow",
			opts: []Option{WithCanUseTool(noopCanUseTool), WithAllowedTools("Bash(ls:*)")},
			want: nil,
		},
		{
			name: "bypassPermissions shadows everything",
			opts: []Option{WithCanUseTool(noopCanUseTool), WithPermissionMode(PermissionBypass)},
			want: []string{"*"},
		},
		{
			// WithSkills injects Skill(name) specifiers, which are narrow.
			name: "skills do not shadow",
			opts: []Option{WithCanUseTool(noopCanUseTool), WithSkills("deploy")},
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CanUseToolShadowed(c.opts...)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("CanUseToolShadowed() = %v, want %v", got, c.want)
			}
		})
	}
}

// The advisory message names the shadowed tools, and stays empty when nothing
// is shadowed. Delivery to the WithStderr writer is covered live in
// TestIntegrationCanUseToolShadowWarning.
func TestCanUseToolShadowedWarning(t *testing.T) {
	o := newOptions(WithCanUseTool(noopCanUseTool), WithAllowedTools("Read"))
	msg := o.canUseToolShadowedWarning()
	if msg == "" {
		t.Fatal("expected a warning message")
	}
	if !strings.Contains(msg, "Read") {
		t.Errorf("warning should name the shadowed tool, got %q", msg)
	}

	bypass := newOptions(WithCanUseTool(noopCanUseTool), WithPermissionMode(PermissionBypass))
	if m := bypass.canUseToolShadowedWarning(); !strings.Contains(m, "bypassPermissions") {
		t.Errorf("bypass warning should explain the mode, got %q", m)
	}

	quiet := newOptions(WithCanUseTool(noopCanUseTool), WithAllowedTools("Bash(ls:*)"))
	if m := quiet.canUseToolShadowedWarning(); m != "" {
		t.Errorf("expected silence, got %q", m)
	}
}
