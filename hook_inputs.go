package claude

import "encoding/json"

// Typed hook inputs decode the raw event payload delivered to a [HookCallback]
// into structured Go values. The callback still receives the payload as
// json.RawMessage (for forward-compatibility); use these helpers to decode it
// when you want typed access.

// HookEffort is the reasoning effort in effect when a hook fires.
type HookEffort struct {
	// Level is the effort level for the current turn (for example "low",
	// "high", "max"), after any downgrade for the selected model.
	Level string `json:"level"`
}

// BaseHookInput holds fields common to every hook event.
type BaseHookInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
	// PromptID is the UUID of the user prompt being processed. It matches the
	// prompt.id attribute on OpenTelemetry events. Empty until the first user
	// input.
	PromptID string `json:"prompt_id,omitempty"`
	// Effort is the effort in effect for the current turn. Present for hooks
	// that fire within a tool-use context (PreToolUse, PostToolUse, Stop,
	// SubagentStop, …) on models that support effort; nil otherwise.
	Effort *HookEffort `json:"effort,omitempty"`
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
	// DurationMs is the tool execution time in milliseconds, excluding
	// permission-prompt and hook time. Nil when the CLI omits it.
	DurationMs *int `json:"duration_ms,omitempty"`
}

// UserPromptSubmitHookInput is the payload for [HookUserPromptSubmit].
type UserPromptSubmitHookInput struct {
	BaseHookInput
	Prompt string `json:"prompt"`
}

// BackgroundTaskSummary is an in-flight background task, as listed on
// [StopHookInput] and [SubagentStopHookInput].
//
// Description and Command are capped at 1000 characters; a clipped value ends
// with an in-string "… [+N chars]" marker, so it is not always the original
// text.
type BackgroundTaskSummary struct {
	ID string `json:"id"`
	// Type is the task-type label (for example "shell", "subagent",
	// "monitor", "workflow"). It falls back to the raw discriminant for
	// unknown types.
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
	// Command is the shell command line. Only set for shell tasks.
	Command string `json:"command,omitempty"`
	// AgentType is the subagent type name. Only set for subagent tasks.
	AgentType string `json:"agent_type,omitempty"`
	// Server is the MCP server name. Only set for monitor / MCP tasks.
	Server string `json:"server,omitempty"`
	// Tool is the MCP tool name. Only set for monitor / MCP tasks.
	Tool string `json:"tool,omitempty"`
	// Name is the workflow name. Only set for workflow tasks.
	Name string `json:"name,omitempty"`
}

// SessionCronSummary is a session-scoped scheduled wakeup, as listed on
// [StopHookInput] and [SubagentStopHookInput].
type SessionCronSummary struct {
	ID string `json:"id"`
	// Schedule is a cron expression, for example "0 9 * * 1-5".
	Schedule string `json:"schedule"`
	// Recurring is false for one-shot wakeups and true for tasks that re-fire
	// on every match.
	Recurring bool `json:"recurring"`
	// Prompt is submitted when the cron fires. Capped at 1000 characters; a
	// clipped value ends with an in-string "… [+N chars]" marker.
	Prompt string `json:"prompt"`
}

// StopHookInput is the payload for [HookStop] and [HookSubagentStop].
type StopHookInput struct {
	BaseHookInput
	StopHookActive bool `json:"stop_hook_active"`
	// LastAssistantMessage is the text content of the last assistant message
	// before stopping.
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	// BackgroundTasks lists in-flight background work in this session. Empty
	// when nothing is in flight.
	BackgroundTasks []BackgroundTaskSummary `json:"background_tasks,omitempty"`
	// SessionCrons lists session-scoped scheduled wakeups that will resume
	// this session later. Empty when none are scheduled.
	SessionCrons []SessionCronSummary `json:"session_crons,omitempty"`
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
	Trigger            string `json:"trigger,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`
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
	ToolName              string          `json:"tool_name"`
	ToolInput             json.RawMessage `json:"tool_input"`
	ToolUseID             string          `json:"tool_use_id,omitempty"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
}

// SubagentStopHookInput is the payload for [HookSubagentStop].
type SubagentStopHookInput struct {
	BaseHookInput
	StopHookActive      bool   `json:"stop_hook_active"`
	AgentID             string `json:"agent_id,omitempty"`
	AgentTranscriptPath string `json:"agent_transcript_path,omitempty"`
	AgentType           string `json:"agent_type,omitempty"`
	// LastAssistantMessage is the text content of the subagent's last
	// assistant message before stopping.
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
	// BackgroundTasks lists in-flight background work, scoped to the parent
	// session.
	BackgroundTasks []BackgroundTaskSummary `json:"background_tasks,omitempty"`
	// SessionCrons lists scheduled wakeups, scoped to the parent session.
	SessionCrons []SessionCronSummary `json:"session_crons,omitempty"`
}

// PostToolUseFailureHookInput is the payload for a post-tool-use-failure hook.
type PostToolUseFailureHookInput struct {
	BaseHookInput
	ToolName    string          `json:"tool_name"`
	ToolInput   json.RawMessage `json:"tool_input"`
	ToolUseID   string          `json:"tool_use_id"`
	Error       string          `json:"error"`
	IsInterrupt bool            `json:"is_interrupt,omitempty"`
	// DurationMs is the tool execution time in milliseconds, excluding
	// permission-prompt and hook time. Nil when the CLI omits it.
	DurationMs *int `json:"duration_ms,omitempty"`
}

// Hook-specific outputs are typed payloads for HookOutput.HookSpecificOutput.

// PreToolUseHookSpecificOutput is the typed hook-specific output for
// PreToolUse events. PermissionDecision is "allow", "deny", "ask", or "defer".
type PreToolUseHookSpecificOutput struct {
	HookEventName            string          `json:"hookEventName"` // "PreToolUse"
	PermissionDecision       string          `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string          `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage `json:"updatedInput,omitempty"`
	AdditionalContext        string          `json:"additionalContext,omitempty"`
}

// PostToolUseHookSpecificOutput is the typed hook-specific output for
// PostToolUse events. UpdatedToolOutput replaces a built-in tool's output and
// UpdatedMCPToolOutput an MCP tool's.
type PostToolUseHookSpecificOutput struct {
	HookEventName        string          `json:"hookEventName"` // "PostToolUse"
	AdditionalContext    string          `json:"additionalContext,omitempty"`
	UpdatedToolOutput    json.RawMessage `json:"updatedToolOutput,omitempty"`
	UpdatedMCPToolOutput json.RawMessage `json:"updatedMCPToolOutput,omitempty"`
}

// UserPromptSubmitHookSpecificOutput is the typed hook-specific output for
// UserPromptSubmit events.
type UserPromptSubmitHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "UserPromptSubmit"
	AdditionalContext string `json:"additionalContext,omitempty"`
	// SessionTitle sets the session title.
	SessionTitle string `json:"sessionTitle,omitempty"`
	// SuppressOriginalPrompt, when Decision is "block", omits the original
	// prompt from the block message.
	SuppressOriginalPrompt bool `json:"suppressOriginalPrompt,omitempty"`
}

// UserPromptExpansionHookSpecificOutput is the typed hook-specific output for
// UserPromptExpansion events.
type UserPromptExpansionHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "UserPromptExpansion"
	AdditionalContext string `json:"additionalContext,omitempty"`
	// SuppressOriginalPrompt, when Decision is "block", omits the original
	// prompt from the block message.
	SuppressOriginalPrompt bool `json:"suppressOriginalPrompt,omitempty"`
}

// SessionStartHookSpecificOutput is the typed hook-specific output for
// SessionStart events.
type SessionStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "SessionStart"
	AdditionalContext string `json:"additionalContext,omitempty"`
	// InitialUserMessage is used as the first user message of the session.
	InitialUserMessage string `json:"initialUserMessage,omitempty"`
	// SessionTitle sets the session title. Ignored when the session starts
	// from clear or compact.
	SessionTitle string `json:"sessionTitle,omitempty"`
	// WatchPaths are absolute paths to watch for FileChanged events during
	// this session.
	WatchPaths []string `json:"watchPaths,omitempty"`
	// ReloadSkills re-scans skill and command directories after SessionStart
	// hooks complete, so skills the hook installed are available in the same
	// session.
	ReloadSkills bool `json:"reloadSkills,omitempty"`
}

// NotificationHookSpecificOutput is the typed hook-specific output for
// Notification events.
type NotificationHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "Notification"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// SubagentStartHookSpecificOutput is the typed hook-specific output for
// subagent-start events.
type SubagentStartHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "SubagentStart"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// PostToolUseFailureHookSpecificOutput is the typed hook-specific output for
// post-tool-use-failure events.
type PostToolUseFailureHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "PostToolUseFailure"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// PermissionRequestHookSpecificOutput is the typed hook-specific output for
// permission-request events.
type PermissionRequestHookSpecificOutput struct {
	HookEventName string          `json:"hookEventName"` // "PermissionRequest"
	Decision      json.RawMessage `json:"decision"`
}

// PostToolBatchHookSpecificOutput is the typed hook-specific output for
// PostToolBatch events.
type PostToolBatchHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "PostToolBatch"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// StopHookSpecificOutput is the typed hook-specific output for Stop events.
// AdditionalContext is non-error feedback delivered to the model; the
// conversation continues so the model can act on it.
type StopHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "Stop"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// SubagentStopHookSpecificOutput is the typed hook-specific output for
// SubagentStop events. AdditionalContext is non-error feedback delivered to the
// subagent; the subagent continues so it can act on it.
type SubagentStopHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"` // "SubagentStop"
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// PermissionDeniedHookSpecificOutput is the typed hook-specific output for
// PermissionDenied events.
type PermissionDeniedHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PermissionDenied"
	// Retry tells the model it may retry the denied tool call. Ignored for
	// denials without a classifier verdict.
	Retry bool `json:"retry,omitempty"`
}

// PreModelSwitchHookSpecificOutput is the typed hook-specific output for
// PreModelSwitch events. PermissionDecision is "allow" (proceed), "deny"
// (cancel the switch), or "ask", which is treated as a refusal outside an
// interactive session.
type PreModelSwitchHookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"` // "PreModelSwitch"
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// PostModelSwitchHookSpecificOutput is the typed hook-specific output for
// PostModelSwitch events.
type PostModelSwitchHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "PostModelSwitch"
	// AdditionalContext reaches the model with the next request the new model
	// serves.
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// ElicitationHookSpecificOutput is the typed hook-specific output for
// Elicitation events. Return it to accept or decline an MCP elicitation
// request programmatically. Action is "accept", "decline", or "cancel".
type ElicitationHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "Elicitation"
	Action        string `json:"action,omitempty"`
	// Content holds the form field values to submit, as a JSON object. Only
	// used when Action is "accept".
	Content json.RawMessage `json:"content,omitempty"`
}

// ElicitationResultHookSpecificOutput is the typed hook-specific output for
// ElicitationResult events. Return it to override the action or content before
// the response is sent to the MCP server. Action is "accept", "decline", or
// "cancel".
type ElicitationResultHookSpecificOutput struct {
	HookEventName string          `json:"hookEventName"` // "ElicitationResult"
	Action        string          `json:"action,omitempty"`
	Content       json.RawMessage `json:"content,omitempty"`
}

// CwdChangedHookSpecificOutput is the typed hook-specific output for
// CwdChanged events.
type CwdChangedHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "CwdChanged"
	// WatchPaths are absolute paths to watch for FileChanged events. They
	// replace the current dynamic watch list.
	WatchPaths []string `json:"watchPaths,omitempty"`
}

// FileChangedHookSpecificOutput is the typed hook-specific output for
// FileChanged events.
type FileChangedHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "FileChanged"
	// WatchPaths are absolute paths to watch for FileChanged events. They
	// replace the current dynamic watch list.
	WatchPaths []string `json:"watchPaths,omitempty"`
}

// WorktreeCreateHookSpecificOutput is the typed hook-specific output for
// WorktreeCreate events.
type WorktreeCreateHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "WorktreeCreate"
	// WorktreePath is the absolute path to the created worktree directory.
	WorktreePath string `json:"worktreePath"`
}

// MessageDisplayHookSpecificOutput is the typed hook-specific output for
// MessageDisplay events. It is display-only: it replaces the text on screen
// without changing the stored message or what the model sees.
type MessageDisplayHookSpecificOutput struct {
	HookEventName string `json:"hookEventName"` // "MessageDisplay"
	// DisplayContent is the text displayed in place of the delta. Leave it
	// empty to display the original.
	DisplayContent string `json:"displayContent,omitempty"`
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

// DecodeSubagentStop decodes a [HookSubagentStop] payload.
func DecodeSubagentStop(raw json.RawMessage) (SubagentStopHookInput, error) {
	var in SubagentStopHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}

// DecodePostToolUseFailure decodes a post-tool-use-failure payload.
func DecodePostToolUseFailure(raw json.RawMessage) (PostToolUseFailureHookInput, error) {
	var in PostToolUseFailureHookInput
	err := json.Unmarshal(raw, &in)
	return in, err
}
