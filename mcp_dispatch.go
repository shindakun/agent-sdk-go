package claude

import (
	"context"
	"encoding/json"
	"fmt"
)

// mcpMessageRequest is the inbound mcp_message control payload: a server name
// plus a raw JSONRPC message to dispatch against that in-process server.
type mcpMessageRequest struct {
	ServerName string          `json:"server_name"`
	Message    json.RawMessage `json:"message"`
}

// jsonrpcMessage is the subset of a JSONRPC request the SDK dispatches on.
type jsonrpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// handleMcpMessage services an inbound mcp_message by dispatching the embedded
// JSONRPC request against the named in-process SDK MCP server and wrapping the
// JSONRPC response as {"mcp_response": <response>} for the control_response.
func (s *session) handleMcpMessage(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	var req mcpMessageRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}

	servers := s.opts.sdkMcpServers()
	server := servers[req.ServerName]
	if server == nil {
		return nil, fmt.Errorf("no in-process MCP server named %q", req.ServerName)
	}

	var msg jsonrpcMessage
	if err := json.Unmarshal(req.Message, &msg); err != nil {
		return nil, err
	}

	mcpResp, err := server.dispatch(ctx, msg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]json.RawMessage{"mcp_response": mcpResp})
}

// dispatch routes one JSONRPC message and returns the JSONRPC response bytes.
func (srv *SdkMcpServer) dispatch(ctx context.Context, msg jsonrpcMessage) (json.RawMessage, error) {
	switch msg.Method {
	case "initialize":
		return srv.jsonrpcResult(msg.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    srv.Name,
				"version": srv.versionOrDefault(),
			},
		})
	case "notifications/initialized", "notifications/cancelled":
		// Notifications have no id and expect no result body.
		return json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
	case "tools/list":
		return srv.jsonrpcResult(msg.ID, map[string]any{"tools": srv.toolList()})
	case "tools/call":
		return srv.callTool(ctx, msg)
	default:
		return srv.jsonrpcError(msg.ID, -32601, "method not found: "+msg.Method)
	}
}

func (srv *SdkMcpServer) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(srv.order))
	for _, name := range srv.order {
		t := srv.tools[name]
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return out
}

// callToolParams is the params object for a tools/call request.
type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (srv *SdkMcpServer) callTool(ctx context.Context, msg jsonrpcMessage) (json.RawMessage, error) {
	var params callToolParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return srv.jsonrpcError(msg.ID, -32602, "invalid params: "+err.Error())
	}
	tool, ok := srv.tools[params.Name]
	if !ok {
		return srv.jsonrpcError(msg.ID, -32601, "unknown tool: "+params.Name)
	}

	result, err := tool.Handler(ctx, params.Arguments)
	if err != nil {
		// Surface handler errors as an error tool-result rather than a JSONRPC
		// transport error, matching the official SDK.
		return srv.jsonrpcResult(msg.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}

	return srv.jsonrpcResult(msg.ID, map[string]any{
		"content": mcpContent(result.Content),
		"isError": result.IsError,
	})
}

// mcpContent converts ToolResult content blocks into MCP content items.
func mcpContent(blocks []ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case *TextBlock:
			out = append(out, map[string]any{"type": "text", "text": v.Text})
		default:
			// Fallback: stringify unknown blocks as text.
			raw, _ := json.Marshal(v)
			out = append(out, map[string]any{"type": "text", "text": string(raw)})
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"type": "text", "text": ""})
	}
	return out
}

func (srv *SdkMcpServer) jsonrpcResult(id json.RawMessage, result any) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"result":  result,
	})
}

func (srv *SdkMcpServer) jsonrpcError(id json.RawMessage, code int, message string) (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"error":   map[string]any{"code": code, "message": message},
	})
}

func (srv *SdkMcpServer) versionOrDefault() string {
	if srv.Version == "" {
		return "1.0.0"
	}
	return srv.Version
}

// rawID returns the id as-is, or JSON null when absent, so the response echoes
// the request id faithfully.
func rawID(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}
