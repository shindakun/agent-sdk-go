package claude

import (
	"context"
	"encoding/json"
)

// PermissionMode controls how the CLI handles tool-permission decisions when no
// explicit allow/deny rule applies.
type PermissionMode string

const (
	// PermissionDefault prompts (or defers to CanUseTool) for each decision.
	PermissionDefault PermissionMode = "default"
	// PermissionAcceptEdits auto-approves file edits.
	PermissionAcceptEdits PermissionMode = "acceptEdits"
	// PermissionBypass approves all tool use without prompting.
	PermissionBypass PermissionMode = "bypassPermissions"
	// PermissionPlan runs in read-only planning mode.
	PermissionPlan PermissionMode = "plan"
	// PermissionDontAsk proceeds without prompting but does not bypass rules.
	PermissionDontAsk PermissionMode = "dontAsk"
	// PermissionAuto lets the CLI choose the mode automatically.
	PermissionAuto PermissionMode = "auto"
)

// CanUseTool is a callback invoked when the CLI asks the SDK to decide whether a
// tool may run. It returns either a [PermissionAllow] or a [PermissionDeny].
type CanUseTool func(ctx context.Context, toolName string, input json.RawMessage, pc PermissionContext) (PermissionResult, error)

// PermissionContext carries additional information about a permission request.
type PermissionContext struct {
	ToolUseID      string
	Suggestions    json.RawMessage
	AgentID        string
	BlockedPath    string
	DecisionReason string
	Title          string
	DisplayName    string
	Description    string
}

// PermissionResult is the outcome of a [CanUseTool] callback. The concrete types
// are [PermissionAllow] and [PermissionDeny].
type PermissionResult interface {
	isPermissionResult()
}

// PermissionAllow approves a tool call, optionally rewriting its input.
type PermissionAllow struct {
	// UpdatedInput, if non-nil, replaces the tool's input arguments.
	UpdatedInput json.RawMessage
	// UpdatedPermissions, if non-nil, updates the permission ruleset.
	UpdatedPermissions json.RawMessage
}

func (PermissionAllow) isPermissionResult() {}

// PermissionDeny rejects a tool call.
type PermissionDeny struct {
	Message string
	// Interrupt, when true, stops the current turn rather than letting the
	// model continue without the tool.
	Interrupt bool
}

func (PermissionDeny) isPermissionResult() {}
