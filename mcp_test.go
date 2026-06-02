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

// mcpRoundTrip connects a client with the given SDK MCP server and dispatches a
// single JSONRPC message via an inbound mcp_message, returning the unwrapped
// JSONRPC response.
func mcpRoundTrip(t *testing.T, serverName string, server *SdkMcpServer, jsonrpc map[string]any) json.RawMessage {
	t.Helper()
	tr := newInteractiveTransport()
	restore := installInteractive(tr)
	t.Cleanup(restore)

	client := NewClient(WithSDKMCPServer(serverName, server))
	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	resp, errStr := tr.sendInbound(t, "mcp_req_1", "mcp_message", map[string]any{
		"server_name": serverName,
		"message":     jsonrpc,
	})
	if errStr != "" {
		t.Fatalf("error response: %s", errStr)
	}
	var wrapper struct {
		McpResponse json.RawMessage `json:"mcp_response"`
	}
	if err := json.Unmarshal(resp, &wrapper); err != nil {
		t.Fatalf("unwrap mcp_response: %v (%s)", err, resp)
	}
	if len(wrapper.McpResponse) == 0 {
		t.Fatalf("missing mcp_response in %s", resp)
	}
	return wrapper.McpResponse
}

func TestMcpToolsList(t *testing.T) {
	server := NewSdkMcpServer("tools").AddTool(
		NewTool("greet", "Greet someone", func(ctx context.Context, in greetArgs) (ToolResult, error) {
			return TextResult("hi"), nil
		}))

	resp := mcpRoundTrip(t, "tools", server, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})

	var out struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if len(out.Result.Tools) != 1 {
		t.Fatalf("tools = %d", len(out.Result.Tools))
	}
	tl := out.Result.Tools[0]
	if tl.Name != "greet" || tl.Description != "Greet someone" {
		t.Errorf("tool = %+v", tl)
	}
	if len(tl.InputSchema) == 0 {
		t.Error("inputSchema is empty")
	}
}

func TestMcpToolsCallSuccess(t *testing.T) {
	server := NewSdkMcpServer("tools").AddTool(
		NewTool("greet", "Greet someone", func(ctx context.Context, in greetArgs) (ToolResult, error) {
			return TextResult("Hello, " + in.Name), nil
		}))

	resp := mcpRoundTrip(t, "tools", server, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "greet", "arguments": map[string]any{"name": "Ada"}},
	})

	var out struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp)
	}
	if out.Result.IsError {
		t.Error("isError = true, want false")
	}
	if len(out.Result.Content) != 1 || out.Result.Content[0].Text != "Hello, Ada" {
		t.Errorf("content = %+v", out.Result.Content)
	}
}

func TestMcpToolsCallHandlerError(t *testing.T) {
	server := NewSdkMcpServer("tools").AddTool(
		NewTool("boom", "Always fails", func(ctx context.Context, in greetArgs) (ToolResult, error) {
			return ToolResult{}, context.DeadlineExceeded
		}))

	resp := mcpRoundTrip(t, "tools", server, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "boom", "arguments": map[string]any{"name": "x"}},
	})

	var out struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(resp, &out)
	if !out.Result.IsError {
		t.Error("handler error should yield isError=true")
	}
	if len(out.Result.Content) == 0 || out.Result.Content[0].Text == "" {
		t.Errorf("error content = %+v", out.Result.Content)
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

func TestMcpInitialize(t *testing.T) {
	server := NewSdkMcpServer("tools", WithServerVersion("2.3.4"))
	resp := mcpRoundTrip(t, "tools", server, map[string]any{
		"jsonrpc": "2.0", "id": 0, "method": "initialize",
	})
	var out struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q", out.Result.ProtocolVersion)
	}
	if out.Result.ServerInfo.Name != "tools" || out.Result.ServerInfo.Version != "2.3.4" {
		t.Errorf("serverInfo = %+v", out.Result.ServerInfo)
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
