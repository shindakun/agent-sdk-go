package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

// Skills are three-state upstream: unset (no configuration), "all", or a
// list, where an empty list suppresses every skill.
func TestSkillsStates(t *testing.T) {
	for name, tc := range map[string]struct {
		opts       []Option
		allowed    string
		sources    bool
		initSkills string
	}{
		"unset":         {nil, "", false, ""},
		"all":           {[]Option{WithAllSkills()}, "Skill", true, ""},
		"none":          {[]Option{WithSkills()}, "", true, "[]"},
		"listed":        {[]Option{WithSkills("pdf", "plugin:review")}, "Skill(pdf),Skill(plugin:review)", true, `["pdf","plugin:review"]`},
		"all wins last": {[]Option{WithSkills("pdf"), WithAllSkills()}, "Skill", true, ""},
	} {
		o := newOptions(tc.opts...)
		args, err := o.buildArgs()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tc.allowed == "" && argsContainsFlag(args, "--allowedTools") {
			t.Errorf("%s: unexpected --allowedTools in %v", name, args)
		}
		if tc.allowed != "" && !argsContainPair(args, "--allowedTools", tc.allowed) {
			t.Errorf("%s: want --allowedTools %s; args=%v", name, tc.allowed, args)
		}
		if got := argsContainEquals(args, "--setting-sources", "user,project"); got != tc.sources {
			t.Errorf("%s: default setting sources = %v; args=%v", name, got, args)
		}
		req, err := o.buildInitializeRequest(newCallbackRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if string(req.Skills) != tc.initSkills {
			t.Errorf("%s: initialize skills = %q, want %q", name, req.Skills, tc.initSkills)
		}
	}
}

func TestAgentDefinitionOptionalFields(t *testing.T) {
	b, err := json.Marshal(AgentDefinition{
		Description: "d", Prompt: "p",
		Background: Bool(false),
		Effort:     8000,
		MCPServers: []any{"github", map[string]any{"local": map[string]any{"type": "stdio", "command": "srv"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"description":"d","prompt":"p","mcpServers":["github",{"local":{"command":"srv","type":"stdio"}}],"background":false,"effort":8000}`
	if string(b) != want {
		t.Errorf("agent = %s\nwant    %s", b, want)
	}
	b, _ = json.Marshal(AgentDefinition{Description: "d", Prompt: "p", Effort: EffortHigh})
	if !strings.Contains(string(b), `"effort":"high"`) || strings.Contains(string(b), "background") {
		t.Errorf("agent = %s", b)
	}
}

func TestHookOutputFields(t *testing.T) {
	b, err := marshalHookOutput(HookOutput{
		Decision: "block", Reason: "why", SystemMessage: "warn",
		Continue: Bool(false), StopReason: "stopped", SuppressOutput: true,
		Async: true, AsyncTimeout: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	for k, v := range map[string]any{"decision": "block", "reason": "why", "systemMessage": "warn",
		"continue": false, "stopReason": "stopped", "suppressOutput": true, "async": true, "asyncTimeout": float64(5000)} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (output %s)", k, got[k], v, b)
		}
	}
	b, _ = marshalHookOutput(HookOutput{})
	if string(b) != "{}" {
		t.Errorf("zero output = %s", b)
	}
}

func TestAssistantMessageError(t *testing.T) {
	for raw, want := range map[string]AssistantMessageError{
		`"rate_limit"`: ErrorRateLimit,
		`null`:         "",
		`{"x":1}`:      ErrorUnknown,
	} {
		msg, err := UnmarshalMessage([]byte(`{"type":"assistant","message":{"model":"m","content":[]},"error":` + raw + `}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := msg.(*AssistantMessage).Error; got != want {
			t.Errorf("error %s = %q, want %q", raw, got, want)
		}
	}
}
