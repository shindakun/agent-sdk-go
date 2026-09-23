package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Ports of the wire-level cases in upstream's tests/test_sdk_mcp_integration.py:
// raw JSON-RPC sent the way the CLI sends it, asserting on what comes back.

// mcpTestClient plays the CLI's side for in-process MCP servers.
type mcpTestClient struct {
	t   *testing.T
	tr  *interactiveTransport
	seq atomic.Int64
	ids atomic.Int64
}

func newMcpTestClient(t *testing.T, opts ...Option) *mcpTestClient {
	t.Helper()
	tr := newInteractiveTransport()
	t.Cleanup(installInteractive(tr))
	c := NewClient(opts...)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	m := &mcpTestClient{t: t, tr: tr}
	m.ids.Store(-1)
	return m
}

// send forwards one JSON-RPC message and returns the mcp_response, or nil for
// the bare acknowledgement a notification gets.
func (m *mcpTestClient) send(server string, msg map[string]any) map[string]any {
	m.t.Helper()
	resp, errStr := m.tr.sendInbound(m.t, fmt.Sprintf("req-%d", m.seq.Add(1)), "mcp_message",
		map[string]any{"server_name": server, "message": msg})
	if errStr != "" {
		m.t.Fatalf("control error: %s", errStr)
	}
	var wrapper struct {
		McpResponse map[string]any `json:"mcp_response"`
	}
	if err := json.Unmarshal(resp, &wrapper); err != nil {
		m.t.Fatalf("decode %s: %v", resp, err)
	}
	if _, hasID := wrapper.McpResponse["id"]; !hasID {
		if r, ok := wrapper.McpResponse["result"].(map[string]any); ok && len(r) == 0 {
			return nil
		}
	}
	return wrapper.McpResponse
}

func (m *mcpTestClient) request(server, method string, params any) map[string]any {
	m.t.Helper()
	id := m.ids.Add(1)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	resp := m.send(server, msg)
	if resp == nil || resp["jsonrpc"] != "2.0" || resp["id"] != float64(id) {
		m.t.Fatalf("response %v does not answer id %d", resp, id)
	}
	return resp
}

func (m *mcpTestClient) notify(server, method string, params any) map[string]any {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return m.send(server, msg)
}

var mcpInitParams = map[string]any{
	"protocolVersion": "2025-06-18",
	"capabilities":    map[string]any{},
	"clientInfo":      map[string]any{"name": "test-client", "version": "0.0.0"},
}

func (m *mcpTestClient) initialize(server string) map[string]any {
	m.t.Helper()
	resp := m.request(server, "initialize", mcpInitParams)
	m.notify(server, "notifications/initialized", nil)
	return resp["result"].(map[string]any)
}

func (m *mcpTestClient) listTools(server string) []any {
	m.t.Helper()
	return m.request(server, "tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
}

func (m *mcpTestClient) callTool(server, name string, args any) map[string]any {
	m.t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	resp := m.request(server, "tools/call", map[string]any{"name": name, "arguments": args})
	if _, isErr := resp["error"]; isErr {
		m.t.Fatalf("tools/call error: %v", resp)
	}
	return resp["result"].(map[string]any)
}

func mcpTexts(result map[string]any) []string {
	var out []string
	for _, b := range result["content"].([]any) {
		if blk := b.(map[string]any); blk["type"] == "text" {
			out = append(out, blk["text"].(string))
		}
	}
	return out
}

func rpcErrorOf(t *testing.T, resp map[string]any) (float64, string) {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error in %v", resp)
	}
	return e["code"].(float64), e["message"].(string)
}

// connectedMcp is a client for one server registered as "srv", initialized.
func connectedMcp(t *testing.T, srv *SdkMcpServer) *mcpTestClient {
	t.Helper()
	m := newMcpTestClient(t, WithSDKMCPServer("srv", srv))
	m.initialize("srv")
	return m
}

type addArgs struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

func TestMcpInitializeReportsServerInfoAndCapabilities(t *testing.T) {
	m := newMcpTestClient(t, WithSDKMCPServer("hello", NewSdkMcpServer("hello", WithServerVersion("3.2.1"))))
	result := m.request("hello", "initialize", mcpInitParams)["result"].(map[string]any)
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "hello" || info["version"] != "3.2.1" {
		t.Errorf("serverInfo = %v", info)
	}
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want the client's", result["protocolVersion"])
	}
	caps := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities = %v", caps)
	}

	// An unknown version gets the latest the server speaks.
	other := m.request("hello", "initialize", map[string]any{"protocolVersion": "1999-01-01"})["result"].(map[string]any)
	if other["protocolVersion"] != latestHandshakeVersion {
		t.Errorf("negotiated %v for an unknown version", other["protocolVersion"])
	}
}

func TestMcpNotificationsGetNoReplyButAreAcknowledged(t *testing.T) {
	m := newMcpTestClient(t, WithSDKMCPServer("srv", NewSdkMcpServer("srv")))
	m.request("srv", "initialize", mcpInitParams)
	resp, errStr := m.tr.sendInbound(t, "ack-1", "mcp_message", map[string]any{
		"server_name": "srv",
		"message":     map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
	})
	if errStr != "" || string(resp) != `{"mcp_response":{"jsonrpc":"2.0","result":{}}}` {
		t.Errorf("ack = %s, %q", resp, errStr)
	}
	if tools := m.listTools("srv"); len(tools) != 0 {
		t.Errorf("tools = %v, want []", tools)
	}
}

func TestMcpUnknownServerIsAJSONRPCError(t *testing.T) {
	m := newMcpTestClient(t, WithSDKMCPServer("srv", NewSdkMcpServer("srv")))
	code, msg := rpcErrorOf(t, m.request("missing", "tools/list", map[string]any{}))
	if code != -32601 || !strings.Contains(msg, "missing") {
		t.Errorf("error = %v %q", code, msg)
	}
}

func TestMcpUnimplementedMethodIsMethodNotFound(t *testing.T) {
	m := connectedMcp(t, NewSdkMcpServer("srv"))
	if code, _ := rpcErrorOf(t, m.request("srv", "resources/list", map[string]any{})); code != -32601 {
		t.Errorf("code = %v", code)
	}
}

func TestMcpMalformedMessageIsAnErrorAndTheSessionSurvives(t *testing.T) {
	m := connectedMcp(t, NewSdkMcpServer("srv"))
	resp := m.send("srv", map[string]any{"jsonrpc": "2.0", "id": 5})
	if code, _ := rpcErrorOf(t, resp); code != -32603 || resp["id"] != float64(5) {
		t.Errorf("response = %v", resp)
	}
	if tools := m.listTools("srv"); len(tools) != 0 {
		t.Errorf("tools = %v", tools)
	}
}

func TestMcpNonIntegerIDIsANotification(t *testing.T) {
	m := connectedMcp(t, NewSdkMcpServer("srv"))
	if reply := m.send("srv", map[string]any{"jsonrpc": "2.0", "id": 2.5, "method": "tools/list"}); reply != nil {
		t.Errorf("reply = %v, want none", reply)
	}
}

func TestMcpPingBeforeInitializeAndGate(t *testing.T) {
	m := newMcpTestClient(t, WithSDKMCPServer("srv", NewSdkMcpServer("srv")))
	if r := m.request("srv", "ping", nil)["result"].(map[string]any); len(r) != 0 {
		t.Errorf("ping result = %v", r)
	}
	if code, _ := rpcErrorOf(t, m.request("srv", "tools/list", map[string]any{})); code != -32602 {
		t.Errorf("tools/list before initialize: code %v", code)
	}
}

func TestMcpToolsListWireFormat(t *testing.T) {
	noop := func(ctx context.Context, args json.RawMessage) (ToolResult, error) { return ToolResult{}, nil }
	srv := NewSdkMcpServer("srv", WithTools(
		Tool{Name: "read_data", Description: "Read data from source",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"source":{"type":"string"}},"required":["source"]}`),
			Annotations: &ToolAnnotations{ReadOnlyHint: Bool(true), OpenWorldHint: Bool(false)}, Handler: noop},
		Tool{Name: "delete_item", Description: "Delete an item",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
			Annotations: &ToolAnnotations{DestructiveHint: Bool(true), IdempotentHint: Bool(true)}, Handler: noop},
		Tool{Name: "plain", Description: "Tool without annotations",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"},"n":{"type":"integer"}},"required":["x","n"]}`),
			Handler:     noop},
		Tool{Name: "big", Description: "Large results",
			Annotations: &ToolAnnotations{ReadOnlyHint: Bool(true), MaxResultSizeChars: func() *int { n := 500000; return &n }()}, Handler: noop},
	))
	m := connectedMcp(t, srv)
	tools := map[string]map[string]any{}
	for _, raw := range m.listTools("srv") {
		tl := raw.(map[string]any)
		tools[tl["name"].(string)] = tl
	}
	plain, _ := json.Marshal(tools["plain"])
	if string(plain) != `{"description":"Tool without annotations","inputSchema":{"properties":{"n":{"type":"integer"},"x":{"type":"string"}},"required":["x","n"],"type":"object"},"name":"plain"}` {
		t.Errorf("plain = %s", plain)
	}
	if a, _ := json.Marshal(tools["read_data"]["annotations"]); string(a) != `{"openWorldHint":false,"readOnlyHint":true}` {
		t.Errorf("read_data annotations = %s", a)
	}
	if a, _ := json.Marshal(tools["delete_item"]["annotations"]); string(a) != `{"destructiveHint":true,"idempotentHint":true}` {
		t.Errorf("delete_item annotations = %s", a)
	}
	if _, ok := tools["read_data"]["_meta"]; ok {
		t.Error("_meta on a tool without maxResultSizeChars")
	}
	if meta, _ := json.Marshal(tools["big"]["_meta"]); string(meta) != `{"anthropic/maxResultSizeChars":500000}` {
		t.Errorf("big _meta = %s", meta)
	}
	if a, _ := json.Marshal(tools["big"]["annotations"]); string(a) != `{"readOnlyHint":true}` {
		t.Errorf("maxResultSizeChars leaked into annotations: %s", a)
	}
}

func TestMcpToolCallResults(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	srv := NewSdkMcpServer("srv").
		AddTool(NewTool("greet_user", "Greets", func(ctx context.Context, in struct {
			Name string `json:"name"`
		}) (ToolResult, error) {
			mu.Lock()
			calls = append(calls, in.Name)
			mu.Unlock()
			return TextResult("Hello, " + in.Name + "!"), nil
		})).
		AddTool(NewTool("divide", "Divide", func(ctx context.Context, in addArgs) (ToolResult, error) {
			if in.B == 0 {
				return ErrorResult("Division by zero"), nil
			}
			return TextResult(fmt.Sprint(in.A / in.B)), nil
		})).
		AddTool(NewTool("chart", "Chart", func(ctx context.Context, in struct{}) (ToolResult, error) {
			return ToolResult{Content: []ContentBlock{&TextBlock{Text: "Generated chart"}, &ImageBlock{Data: "iVBORw0KGgo=", MimeType: "image/png"}, &ThinkingBlock{Thinking: "dropped"}}}, nil
		})).
		AddTool(NewTool("fail", "Fails", func(ctx context.Context, in struct{}) (ToolResult, error) {
			return ToolResult{}, errors.New("Expected error")
		})).
		AddTool(NewTool("empty", "No content", func(ctx context.Context, in struct{}) (ToolResult, error) {
			return ToolResult{}, nil
		}))
	m := connectedMcp(t, srv)

	greeting := m.callTool("srv", "greet_user", map[string]any{"name": "Alice"})
	if g, _ := json.Marshal(greeting); string(g) != `{"content":[{"text":"Hello, Alice!","type":"text"}],"isError":false}` {
		t.Errorf("greeting = %s", g)
	}
	failure := m.callTool("srv", "divide", map[string]any{"a": 1, "b": 0})
	if failure["isError"] != true || strings.Join(mcpTexts(failure), "") != "Division by zero" {
		t.Errorf("divide by zero = %v", failure)
	}
	if ok := m.callTool("srv", "divide", map[string]any{"a": 6, "b": 3}); ok["isError"] != false || mcpTexts(ok)[0] != "2" {
		t.Errorf("divide = %v", ok)
	}
	chart, _ := json.Marshal(m.callTool("srv", "chart", nil)["content"])
	if string(chart) != `[{"text":"Generated chart","type":"text"},{"data":"iVBORw0KGgo=","mimeType":"image/png","type":"image"}]` {
		t.Errorf("chart content = %s", chart)
	}
	if r := m.callTool("srv", "fail", nil); r["isError"] != true || mcpTexts(r)[0] != "Expected error" {
		t.Errorf("handler error = %v", r)
	}
	if r := m.callTool("srv", "mystery", nil); r["isError"] != true || mcpTexts(r)[0] != "Tool 'mystery' not found" {
		t.Errorf("unknown tool = %v", r)
	}
	if c, _ := json.Marshal(m.callTool("srv", "empty", nil)["content"]); string(c) != `[]` {
		t.Errorf("empty content = %s", c)
	}
	if len(calls) != 1 || calls[0] != "Alice" {
		t.Errorf("calls = %v", calls)
	}
}

func TestMcpInvalidArgumentsRejectedBeforeTheHandler(t *testing.T) {
	var calls atomic.Int32
	srv := NewSdkMcpServer("srv").
		AddTool(Tool{Name: "add", Description: "Add",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
			Handler: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
				calls.Add(1)
				var in addArgs
				_ = json.Unmarshal(args, &in)
				return TextResult(fmt.Sprint(in.A + in.B)), nil
			}}).
		AddTool(Tool{Name: "validate", Description: "Validate",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":2}},"required":["name"]}`),
			Handler:     func(ctx context.Context, args json.RawMessage) (ToolResult, error) { return TextResult("OK"), nil }}).
		AddTool(Tool{Name: "broken", Description: "Invalid schema",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"bogus"}}}`),
			Handler: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
				return TextResult("unreachable"), nil
			}})
	m := connectedMcp(t, srv)

	missing := m.callTool("srv", "add", map[string]any{"a": 1})
	if missing["isError"] != true || mcpTexts(missing)[0] != "Input validation error: 'b' is a required property" {
		t.Errorf("missing = %v", missing)
	}
	wrong := m.callTool("srv", "add", map[string]any{"a": 1, "b": "two"})
	if msg := mcpTexts(wrong)[0]; wrong["isError"] != true || !strings.HasPrefix(msg, "Input validation error: ") || !strings.Contains(msg, "'two'") {
		t.Errorf("wrong type = %v", wrong)
	}
	if fine := m.callTool("srv", "add", map[string]any{"a": 1, "b": 2}); mcpTexts(fine)[0] != "3" {
		t.Errorf("fine = %v", fine)
	}
	if calls.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", calls.Load())
	}
	if short := m.callTool("srv", "validate", map[string]any{"name": "x"}); short["isError"] != true || !strings.HasPrefix(mcpTexts(short)[0], "Input validation error: ") {
		t.Errorf("too short = %v", short)
	}
	if ok := m.callTool("srv", "validate", map[string]any{"name": "xy"}); mcpTexts(ok)[0] != "OK" {
		t.Errorf("valid = %v", ok)
	}
	if b := m.callTool("srv", "broken", map[string]any{"x": 1}); b["isError"] != true || !strings.Contains(mcpTexts(b)[0], "bogus") {
		t.Errorf("broken schema = %v", b)
	}
}

func TestMcpCancelledToolCallIsEnded(t *testing.T) {
	started := make(chan struct{})
	var outcome atomic.Value
	srv := NewSdkMcpServer("srv").AddTool(Tool{Name: "slow", Description: "Sleeps",
		Handler: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			close(started)
			select {
			case <-ctx.Done():
				outcome.Store("cancelled")
				return ToolResult{}, ctx.Err()
			case <-time.After(30 * time.Second):
				outcome.Store("finished")
				return ToolResult{}, nil
			}
		}})
	m := connectedMcp(t, srv)
	done := make(chan map[string]any, 1)
	go func() {
		done <- m.send("srv", map[string]any{"jsonrpc": "2.0", "id": 77, "method": "tools/call",
			"params": map[string]any{"name": "slow", "arguments": map[string]any{}}})
	}()
	<-started
	m.notify("srv", "notifications/cancelled", map[string]any{"requestId": 77, "reason": "user interrupted"})
	resp := <-done
	if outcome.Load() != "cancelled" {
		t.Errorf("outcome = %v", outcome.Load())
	}
	if code, msg := rpcErrorOf(t, resp); code != -32800 || !strings.Contains(strings.ToLower(msg), "cancelled") || resp["id"] != float64(77) {
		t.Errorf("response = %v", resp)
	}
	if tools := m.listTools("srv"); len(tools) != 1 {
		t.Errorf("session did not carry on: %v", tools)
	}
}

func TestMcpReusingAnInFlightIDIsRefused(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	srv := NewSdkMcpServer("srv").AddTool(Tool{Name: "wait", Description: "Waits",
		Handler: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			calls.Add(1)
			<-release
			return TextResult("done"), nil
		}})
	m := connectedMcp(t, srv)
	call := map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "wait", "arguments": map[string]any{}}}
	first := make(chan map[string]any, 1)
	go func() { first <- m.send("srv", call) }()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	second := m.send("srv", call)
	close(release)
	if _, msg := rpcErrorOf(t, second); !strings.Contains(msg, "already in flight") || second["id"] != float64(7) {
		t.Errorf("second = %v", second)
	}
	if f := <-first; mcpTexts(f["result"].(map[string]any))[0] != "done" {
		t.Errorf("first = %v", f)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestMcpConcurrentToolCallsBothResolve(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(2)
	srv := NewSdkMcpServer("srv").AddTool(NewTool("rendezvous", "Waits for its sibling", func(ctx context.Context, in struct {
		Tag string `json:"tag"`
	}) (ToolResult, error) {
		wg.Done()
		wg.Wait()
		return TextResult(in.Tag), nil
	}))
	m := connectedMcp(t, srv)
	results := make(chan string, 2)
	for _, tag := range []string{"a", "b"} {
		go func(tag string) { results <- mcpTexts(m.callTool("srv", "rendezvous", map[string]any{"tag": tag}))[0] }(tag)
	}
	got := map[string]bool{<-results: true, <-results: true}
	if !got["a"] || !got["b"] {
		t.Errorf("results = %v", got)
	}
}
