package claude

import "encoding/json"

// Typed hook inputs decode the raw event payload delivered to a [HookCallback]
// into structured Go values. The callback still receives the payload as
// json.RawMessage (for forward-compatibility); use these helpers to decode it
// when you want typed access.

// BaseHookInput holds fields common to every hook event.
type BaseHookInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
}

// PreToolUseHookInput is the payload for [HookPreToolUse].
type PreToolUseHookInput struct {
	BaseHookInput
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
	AgentID   string          `json:"agent_id,omitempty"`
	AgentType string          `json:"agent_type,omitempty"`
}

// PostToolUseHookInput is the payload for [HookPostToolUse].
type PostToolUseHookInput struct {
	BaseHookInput
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`
	AgentID      string          `json:"agent_id,omitempty"`
	AgentType    string          `json:"agent_type,omitempty"`
}

// UserPromptSubmitHookInput is the payload for [HookUserPromptSubmit].
type UserPromptSubmitHookInput struct {
	BaseHookInput
	Prompt string `json:"prompt"`
}

// StopHookInput is the payload for [HookStop] and [HookSubagentStop].
type StopHookInput struct {
	BaseHookInput
	StopHookActive bool `json:"stop_hook_active"`
}

// NotificationHookInput is the payload for [HookNotification].
type NotificationHookInput struct {
	BaseHookInput
	Message          string `json:"message"`
	Title            string `json:"title,omitempty"`
	NotificationType string `json:"notification_type,omitempty"`
}

// PreCompactHookInput is the payload for [HookPreCompact].
type PreCompactHookInput struct {
	BaseHookInput
	Trigger string `json:"trigger,omitempty"`
}

// SubagentStartHookInput is the payload for a subagent-start hook.
type SubagentStartHookInput struct {
	BaseHookInput
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

// PermissionRequestHookInput is the payload for a permission-request hook.
type PermissionRequestHookInput struct {
	BaseHookInput
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// DecodePreToolUse decodes a [HookPreToolUse] payload.
func DecodePreToolUse(raw json.RawMessage) (PreToolUseHookInput, error) {
	var in PreToolUseHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// DecodePostToolUse decodes a [HookPostToolUse] payload.
func DecodePostToolUse(raw json.RawMessage) (PostToolUseHookInput, error) {
	var in PostToolUseHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// DecodeUserPromptSubmit decodes a [HookUserPromptSubmit] payload.
func DecodeUserPromptSubmit(raw json.RawMessage) (UserPromptSubmitHookInput, error) {
	var in UserPromptSubmitHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// DecodeStop decodes a [HookStop] or [HookSubagentStop] payload.
func DecodeStop(raw json.RawMessage) (StopHookInput, error) {
	var in StopHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// DecodeNotification decodes a [HookNotification] payload.
func DecodeNotification(raw json.RawMessage) (NotificationHookInput, error) {
	var in NotificationHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}
