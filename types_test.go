package claude

import (
	"encoding/json"
	"testing"
)

func TestSandboxSettingsSerializeIntoSettings(t *testing.T) {
	o := newOptions(WithSandbox(SandboxSettings{
		Enabled: true,
		Network: &SandboxNetworkConfig{AllowedDomains: []string{"example.com"}},
	}))
	args, err := o.buildArgs()
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	// Find --settings value and confirm the sandbox is embedded with camelCase.
	var settings string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--settings" {
			settings = args[i+1]
		}
	}
	if settings == "" {
		t.Fatal("no --settings flag emitted for sandbox")
	}
	var parsed struct {
		Sandbox struct {
			Enabled bool `json:"enabled"`
			Network struct {
				AllowedDomains []string `json:"allowedDomains"`
			} `json:"network"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("settings json: %v (%s)", err, settings)
	}
	if !parsed.Sandbox.Enabled || len(parsed.Sandbox.Network.AllowedDomains) != 1 {
		t.Errorf("sandbox not embedded correctly: %s", settings)
	}
}

func TestContextUsageTyped(t *testing.T) {
	cu := ContextUsage{Raw: json.RawMessage(`{"totalTokens":100,"maxTokens":200,"percentage":50,"model":"m","categories":[{"name":"sys","tokens":40,"color":"red"}]}`)}
	r, err := cu.Typed()
	if err != nil {
		t.Fatalf("typed: %v", err)
	}
	if r.TotalTokens != 100 || r.MaxTokens != 200 || r.Model != "m" {
		t.Errorf("usage = %+v", r)
	}
	if len(r.Categories) != 1 || r.Categories[0].Name != "sys" {
		t.Errorf("categories = %+v", r.Categories)
	}
}

func TestServerToolBlocksDecode(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"model":"m","content":[{"type":"server_tool_use","id":"st_1","name":"web_search","input":{"q":"go"}},{"type":"server_tool_result","tool_use_id":"st_1","content":{"results":[]}}]}}`)
	msg, err := UnmarshalMessage(line)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	am := msg.(*AssistantMessage)
	if len(am.Content) != 2 {
		t.Fatalf("content len = %d", len(am.Content))
	}
	su, ok := am.Content[0].(*ServerToolUseBlock)
	if !ok || su.Name != ServerToolWebSearch || su.ID != "st_1" {
		t.Errorf("server_tool_use = %#v", am.Content[0])
	}
	if _, ok := am.Content[1].(*ServerToolResultBlock); !ok {
		t.Errorf("server_tool_result = %#v", am.Content[1])
	}
}

func TestTaskStartedAndProgressDecode(t *testing.T) {
	start, err := UnmarshalMessage([]byte(`{"type":"system","subtype":"task_started","task_id":"t1","description":"do work","uuid":"u","session_id":"s"}`))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ts, ok := start.(*TaskStartedMessage)
	if !ok || ts.TaskID != "t1" || ts.Description != "do work" {
		t.Errorf("task started = %#v", start)
	}

	prog, err := UnmarshalMessage([]byte(`{"type":"system","subtype":"task_progress","task_id":"t1","description":"working","usage":{"total_tokens":5,"tool_uses":1,"duration_ms":10},"uuid":"u","session_id":"s","last_tool_name":"Bash"}`))
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	tp, ok := prog.(*TaskProgressMessage)
	if !ok || tp.Usage.TotalTokens != 5 || tp.LastToolName != "Bash" {
		t.Errorf("task progress = %#v", prog)
	}
}
