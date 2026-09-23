package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// In-process MCP servers are driven by the CLI through mcp_message control
// requests, each carrying one JSON-RPC message. The official SDK serves them
// with the Python mcp library's server; this answers them the same way:
//
//   - initialize negotiates the protocol version and reports the server's
//     capabilities; ping answers {}; other methods a tools server does not
//     serve get -32601, and requests before initialize (except ping) -32602.
//   - Notifications and responses get no JSON-RPC reply; the control request
//     is still acknowledged.
//   - notifications/cancelled cancels the named in-flight call, which then
//     answers -32800. A request reusing an in-flight id is refused.
//   - tools/call validates arguments against the tool's schema, and reports
//     unknown tools, invalid arguments, and handler errors as isError results.

// handshakeProtocolVersions are the MCP revisions negotiable through
// initialize; a client asking for another gets the latest.
var handshakeProtocolVersions = []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"}

const latestHandshakeVersion = "2025-11-25"

// JSON-RPC error codes.
const (
	rpcInvalidParams    = -32602
	rpcMethodNotFound   = -32601
	rpcInternalError    = -32603
	rpcRequestCancelled = -32800
)

// mcpMessageRequest is the inbound mcp_message control payload.
type mcpMessageRequest struct {
	ServerName string          `json:"server_name"`
	Message    json.RawMessage `json:"message"`
}

// mcpServerState is one SDK server's connection state within a session.
type mcpServerState struct {
	mu          sync.Mutex
	initialized bool
	inflight    map[string]*inflightCall
}

type inflightCall struct {
	cancel    context.CancelFunc
	cancelled bool
}

func (s *session) mcpState(name string) *mcpServerState {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpStates == nil {
		s.mcpStates = map[string]*mcpServerState{}
	}
	st := s.mcpStates[name]
	if st == nil {
		st = &mcpServerState{inflight: map[string]*inflightCall{}}
		s.mcpStates[name] = st
	}
	return st
}

// handleMcpMessage services an inbound mcp_message, answering with
// {"mcp_response": <JSON-RPC response>}.
func (s *session) handleMcpMessage(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	var req mcpMessageRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	if req.ServerName == "" || len(bytes.TrimSpace(req.Message)) == 0 {
		return nil, errors.New("missing server_name or message for MCP request")
	}
	resp := s.dispatchMcp(ctx, req.ServerName, req.Message)
	if resp == nil {
		// A notification or response gets no JSON-RPC reply, but the control
		// request that carried it still expects an acknowledgement.
		resp = json.RawMessage(`{"jsonrpc":"2.0","result":{}}`)
	}
	return json.Marshal(map[string]json.RawMessage{"mcp_response": resp})
}

// jsonrpcEnvelope holds the fields that classify a JSON-RPC message.
type jsonrpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// validRequestID reports whether raw is a JSON-RPC id (a string or integer).
func validRequestID(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '"' {
		var s string
		return json.Unmarshal(raw, &s) == nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return false
	}
	_, err := n.Int64()
	return err == nil
}

// dispatchMcp answers one JSON-RPC message for the named server, returning nil
// when no reply is due.
func (s *session) dispatchMcp(ctx context.Context, serverName string, raw json.RawMessage) json.RawMessage {
	var env jsonrpcEnvelope
	decodeErr := json.Unmarshal(raw, &env)
	id := env.ID

	srv := s.opts.sdkMcpServers()[serverName]
	if srv == nil {
		return rpcError(id, rpcMethodNotFound, fmt.Sprintf("Server '%s' not found", serverName))
	}
	isRequest := env.Method != nil && validRequestID(id)
	isNotification := env.Method != nil && !isRequest
	isResponse := env.Method == nil && len(id) > 0 && (len(env.Result) > 0 || len(env.Error) > 0)
	if decodeErr != nil || env.JSONRPC != "2.0" || (!isRequest && !isNotification && !isResponse) {
		return rpcError(id, rpcInternalError, "Invalid JSON-RPC message")
	}

	st := s.mcpState(serverName)
	if isResponse {
		return nil
	}
	if isNotification {
		if *env.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(env.Params, &p) == nil {
				st.cancel(p.RequestID)
			}
		}
		return nil
	}

	key := string(bytes.TrimSpace(id))
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	call := &inflightCall{cancel: cancel}
	st.mu.Lock()
	if _, busy := st.inflight[key]; busy {
		st.mu.Unlock()
		return rpcError(id, rpcInternalError, fmt.Sprintf("Request id %s is already in flight", pyRepr(decodeAny(id))))
	}
	st.inflight[key] = call
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		delete(st.inflight, key)
		st.mu.Unlock()
	}()

	resp := s.serveMcpRequest(callCtx, srv, st, *env.Method, id, env.Params)
	st.mu.Lock()
	cancelled := call.cancelled
	st.mu.Unlock()
	if cancelled {
		return rpcError(id, rpcRequestCancelled, "Request cancelled")
	}
	return resp
}

// cancel cancels the in-flight request with the given id, if any.
func (st *mcpServerState) cancel(id json.RawMessage) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if call, ok := st.inflight[string(bytes.TrimSpace(id))]; ok {
		call.cancelled = true
		call.cancel()
	}
}

func decodeAny(raw json.RawMessage) any {
	var v any
	_ = decodeNumbers(raw, &v)
	return v
}

func (s *session) serveMcpRequest(ctx context.Context, srv *SdkMcpServer, st *mcpServerState, method string, id, params json.RawMessage) json.RawMessage {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		version := latestHandshakeVersion
		for _, v := range handshakeProtocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		st.mu.Lock()
		st.initialized = true
		st.mu.Unlock()
		return rpcResult(id, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"experimental": map[string]any{}, "tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": srv.Name, "version": srv.versionOrDefault()},
		})
	case "ping":
		return rpcResult(id, map[string]any{})
	case "tools/list", "tools/call":
	default:
		return rpcError(id, rpcMethodNotFound, "Method not found")
	}
	st.mu.Lock()
	ready := st.initialized
	st.mu.Unlock()
	if !ready {
		return rpcError(id, rpcInvalidParams, "Invalid request parameters")
	}
	if method == "tools/list" {
		return rpcResult(id, map[string]any{"tools": srv.toolList()})
	}
	return rpcResult(id, s.callTool(ctx, srv, params))
}

func (srv *SdkMcpServer) toolList() []map[string]any {
	out := make([]map[string]any, 0, len(srv.order))
	for _, name := range srv.order {
		t := srv.tools[name]
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		entry := map[string]any{"name": t.Name, "inputSchema": schema}
		if t.Description != "" {
			entry["description"] = t.Description
		}
		if a := t.Annotations; a != nil {
			if b, _ := json.Marshal(a); string(b) != "{}" {
				entry["annotations"] = json.RawMessage(b)
			}
			if a.MaxResultSizeChars != nil {
				entry["_meta"] = map[string]any{"anthropic/maxResultSizeChars": *a.MaxResultSizeChars}
			}
		}
		out = append(out, entry)
	}
	return out
}

// callTool runs a tools/call and returns its result. Every failure is an
// isError result the model can read, never a protocol error.
func (s *session) callTool(ctx context.Context, srv *SdkMcpServer, params json.RawMessage) map[string]any {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	tool, ok := srv.tools[p.Name]
	if !ok {
		return toolErrorResult(fmt.Sprintf("Tool '%s' not found", p.Name))
	}
	args := p.Arguments
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "null" {
		args = json.RawMessage(`{}`)
	}
	schema := tool.InputSchema
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if err := validateToolArguments(schema, args); err != nil {
		var se *schemaError
		if errors.As(err, &se) {
			return toolErrorResult(se.msg)
		}
		return toolErrorResult("Input validation error: " + err.Error())
	}
	result, err := tool.Handler(ctx, args)
	if err != nil {
		return toolErrorResult(err.Error())
	}
	return map[string]any{"content": s.mcpContent(result.Content), "isError": result.IsError}
}

func toolErrorResult(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

// mcpContent converts tool result blocks to MCP content: text and images pass
// through; other block kinds are dropped with a warning on the stderr writer.
func (s *session) mcpContent(blocks []ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case *TextBlock:
			out = append(out, map[string]any{"type": "text", "text": v.Text})
		case *ImageBlock:
			out = append(out, map[string]any{"type": "image", "data": v.Data, "mimeType": v.MimeType})
		default:
			if w := s.opts.stderr; w != nil {
				_, _ = io.WriteString(w, fmt.Sprintf("claude: warning: unsupported content type %T in tool result, skipping\n", b))
			}
		}
	}
	return out
}

func rpcResult(id json.RawMessage, result any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result})
	return b
}

func rpcError(id json.RawMessage, code int, message string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{"code": code, "message": message}})
	return b
}

func (srv *SdkMcpServer) versionOrDefault() string {
	if srv.Version == "" {
		return "1.0.0"
	}
	return srv.Version
}

// rawID returns the id as-is, or JSON null when absent, so the response echoes
// the request id.
func rawID(id json.RawMessage) any {
	if len(bytes.TrimSpace(id)) == 0 {
		return nil
	}
	return id
}
