package claude

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shindakun/agent-sdk-go/internal/protocol"
)

// inboundHandler builds the control-request dispatcher that services CLI->SDK
// requests: can_use_tool, hook_callback, and mcp_message (M4). It returns nil
// when the session has nothing to service, so the engine answers any unexpected
// request with an error.
func (s *session) inboundHandler() protocol.InboundHandler {
	hasPerm := s.opts.canUseTool != nil
	hasHooks := len(s.registry.hooks) > 0
	hasMCP := len(s.opts.sdkMcpServers()) > 0
	if !hasPerm && !hasHooks && !hasMCP {
		return nil
	}

	return func(ctx context.Context, subtype string, payload json.RawMessage) (json.RawMessage, error) {
		switch subtype {
		case "can_use_tool":
			return s.handleCanUseTool(ctx, payload)
		case "hook_callback":
			return s.handleHookCallback(ctx, payload)
		case "mcp_message":
			return s.handleMcpMessage(ctx, payload)
		default:
			return nil, fmt.Errorf("unsupported control request subtype %q", subtype)
		}
	}
}

// canUseToolRequest mirrors the inbound can_use_tool payload.
type canUseToolRequest struct {
	ToolName              string          `json:"tool_name"`
	Input                 json.RawMessage `json:"input"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions"`
	ToolUseID             string          `json:"tool_use_id"`
	AgentID               string          `json:"agent_id"`
	BlockedPath           string          `json:"blocked_path"`
	DecisionReason        string          `json:"decision_reason"`
	Title                 string          `json:"title"`
	DisplayName           string          `json:"display_name"`
	Description           string          `json:"description"`
}

func (s *session) handleCanUseTool(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	if s.opts.canUseTool == nil {
		return nil, fmt.Errorf("no permission callback registered")
	}
	var req canUseToolRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}

	result, err := s.opts.canUseTool(ctx, req.ToolName, req.Input, PermissionContext{
		ToolUseID:      req.ToolUseID,
		Suggestions:    req.PermissionSuggestions,
		AgentID:        req.AgentID,
		BlockedPath:    req.BlockedPath,
		DecisionReason: req.DecisionReason,
		Title:          req.Title,
		DisplayName:    req.DisplayName,
		Description:    req.Description,
	})
	if err != nil {
		return nil, err
	}

	switch r := result.(type) {
	case PermissionAllow:
		out := map[string]any{"behavior": "allow"}
		// updatedInput defaults to the original input when not rewritten,
		// matching the official SDK.
		if r.UpdatedInput != nil {
			out["updatedInput"] = r.UpdatedInput
		} else {
			out["updatedInput"] = req.Input
		}
		if r.UpdatedPermissions != nil {
			out["updatedPermissions"] = r.UpdatedPermissions
		}
		return json.Marshal(out)
	case PermissionDeny:
		out := map[string]any{"behavior": "deny", "message": r.Message}
		if r.Interrupt {
			out["interrupt"] = true
		}
		return json.Marshal(out)
	default:
		return nil, fmt.Errorf("unexpected permission result type %T", result)
	}
}

// hookCallbackRequest mirrors the inbound hook_callback payload.
type hookCallbackRequest struct {
	CallbackID string          `json:"callback_id"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
}

func (s *session) handleHookCallback(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	var req hookCallbackRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	cb := s.registry.lookupHook(req.CallbackID)
	if cb == nil {
		return nil, fmt.Errorf("no hook callback registered for id %q", req.CallbackID)
	}

	out, err := cb(ctx, req.Input, req.ToolUseID)
	if err != nil {
		return nil, err
	}
	return marshalHookOutput(out)
}

// marshalHookOutput serializes a HookOutput into the CLI's expected shape,
// renaming Go-friendly fields to the wire names ("continue", etc.).
func marshalHookOutput(out HookOutput) (json.RawMessage, error) {
	m := map[string]any{}
	if out.Decision != "" {
		m["decision"] = out.Decision
	}
	if out.SystemMessage != "" {
		m["systemMessage"] = out.SystemMessage
	}
	if out.Continue != nil {
		m["continue"] = *out.Continue
	}
	if out.SuppressOutput {
		m["suppressOutput"] = true
	}
	if len(out.HookSpecificOutput) > 0 {
		m["hookSpecificOutput"] = out.HookSpecificOutput
	}
	return json.Marshal(m)
}
