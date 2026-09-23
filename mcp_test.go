package claude

import (
	"context"
	"encoding/json"
	"testing"
)

type greetArgs struct {
	Name  string `json:"name"`
	Count int    `json:"count,omitempty"`
}

func TestNewToolSchema(t *testing.T) {
	tool := NewTool("greet", "Greet someone",
		func(ctx context.Context, in greetArgs) (ToolResult, error) {
			return TextResult("hi " + in.Name), nil
		})

	var schema struct {
		Type       string                       `json:"type"`
		Properties map[string]map[string]string `json:"properties"`
		Required   []string                     `json:"required"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("schema: %v (%s)", err, tool.InputSchema)
	}
	if schema.Type != "object" {
		t.Errorf("type = %q", schema.Type)
	}
	if schema.Properties["name"]["type"] != "string" {
		t.Errorf("name type = %v", schema.Properties["name"])
	}
	if schema.Properties["count"]["type"] != "integer" {
		t.Errorf("count type = %v", schema.Properties["count"])
	}
	// name is required (no omitempty); count is optional (omitempty).
	if len(schema.Required) != 1 || schema.Required[0] != "name" {
		t.Errorf("required = %v, want [name]", schema.Required)
	}
}

func TestSdkMcpServerAdvertisedInConfig(t *testing.T) {
	o := newOptions(WithSDKMCPServer("calc", NewSdkMcpServer("calc")))
	blob, err := o.buildMcpConfig()
	if err != nil {
		t.Fatalf("buildMcpConfig: %v", err)
	}
	var cfg struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(blob), &cfg); err != nil {
		t.Fatalf("decode: %v (%s)", err, blob)
	}
	srv := cfg.McpServers["calc"]
	if srv["type"] != "sdk" || srv["name"] != "calc" {
		t.Errorf("sdk server config = %v, want type=sdk name=calc", srv)
	}
}

func TestNewToolSchemaNested(t *testing.T) {
	type inner struct {
		X int      `json:"x"`
		Y []string `json:"y"`
	}
	type outer struct {
		Name  string `json:"name"`
		Inner inner  `json:"inner"`
	}
	tool := NewTool("t", "d", func(ctx context.Context, in outer) (ToolResult, error) {
		return TextResult(""), nil
	})
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("schema: %v (%s)", err, tool.InputSchema)
	}
	var innerSchema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema.Properties["inner"], &innerSchema); err != nil {
		t.Fatalf("inner schema: %v", err)
	}
	if innerSchema.Type != "object" {
		t.Errorf("inner type = %q, want object", innerSchema.Type)
	}
	if _, ok := innerSchema.Properties["x"]; !ok {
		t.Errorf("nested struct not recursed: %s", schema.Properties["inner"])
	}
	if _, ok := innerSchema.Properties["y"]; !ok {
		t.Errorf("nested slice field missing: %s", schema.Properties["inner"])
	}
}
