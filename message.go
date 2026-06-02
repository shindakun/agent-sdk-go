package claude

import "encoding/json"

// Message is a single item in the stream emitted by the CLI. The concrete types
// are [AssistantMessage], [UserMessage], [SystemMessage], [ResultMessage],
// [StreamEvent], and [TaskNotification].
//
// Decode the stream by type-switching on the concrete types; the discriminated
// union is sealed (only this package defines implementations).
type Message interface {
	isMessage()
}

// AssistantMessage is a response turn from the model.
type AssistantMessage struct {
	Content []ContentBlock
	Model   string
	// ParentToolUseID is set when this message originates inside a subagent's
	// context, identifying the Agent tool call that spawned it.
	ParentToolUseID string
	MessageID       string          `json:"message_id,omitempty"`
	StopReason      string          `json:"stop_reason,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	UUID            string          `json:"uuid,omitempty"`
	Usage           *Usage          `json:"usage,omitempty"`
	Error           json.RawMessage `json:"error,omitempty"`
	// Raw is the undecoded JSON of the message for forward-compatibility.
	Raw json.RawMessage
}

func (*AssistantMessage) isMessage() {}

// UserMessage is a user turn, including synthesized tool-result turns.
type UserMessage struct {
	Content         []ContentBlock
	ParentToolUseID string
	UUID            string          `json:"uuid,omitempty"`
	ToolUseResult   json.RawMessage `json:"tool_use_result,omitempty"`
	Raw             json.RawMessage
}

func (*UserMessage) isMessage() {}

// PluginInfo describes a plugin loaded into the session, as reported in the
// "init" system message.
type PluginInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
}

// SystemMessage carries out-of-band events from the CLI. Subtype distinguishes
// the payload (for example "init" or "session_state_changed"); subtype-specific
// fields are populated where known, and Data holds the full payload.
type SystemMessage struct {
	Subtype   string
	SessionID string
	// Tools is populated for the "init" subtype.
	Tools []string
	// Plugins is populated for the "init" subtype with the loaded plugins.
	Plugins []PluginInfo
	// Data is the full system payload for subtype-specific access.
	Data json.RawMessage
	Raw  json.RawMessage
}

func (*SystemMessage) isMessage() {}

// Usage reports token usage for a turn or result.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ResultMessage terminates a turn, summarizing cost, duration, and outcome.
type ResultMessage struct {
	Subtype           string           `json:"subtype"`
	IsError           bool             `json:"is_error"`
	Errors            []string         `json:"errors,omitempty"`
	DurationMs        int              `json:"duration_ms"`
	DurationAPIMs     int              `json:"duration_api_ms,omitempty"`
	NumTurns          int              `json:"num_turns"`
	StopReason        string           `json:"stop_reason,omitempty"`
	TotalCostUSD      float64          `json:"total_cost_usd,omitempty"`
	Usage             Usage            `json:"usage"`
	ModelUsage        json.RawMessage  `json:"model_usage,omitempty"`
	Result            string           `json:"result,omitempty"`
	StructuredOutput  json.RawMessage  `json:"structured_output,omitempty"`
	PermissionDenials json.RawMessage  `json:"permission_denials,omitempty"`
	DeferredToolUse   *DeferredToolUse `json:"deferred_tool_use,omitempty"`
	APIErrorStatus    *int             `json:"api_error_status,omitempty"`
	SessionID         string           `json:"session_id"`
	UUID              string           `json:"uuid,omitempty"`
	Raw               json.RawMessage
}

func (*ResultMessage) isMessage() {}

// StreamEvent is a partial/delta event, emitted only when partial messages are
// enabled. Event holds the raw streaming event payload.
type StreamEvent struct {
	UUID            string          `json:"uuid,omitempty"`
	SessionID       string          `json:"session_id"`
	ParentToolUseID string          `json:"parent_tool_use_id,omitempty"`
	Event           json.RawMessage `json:"event"`
	Raw             json.RawMessage
}

func (*StreamEvent) isMessage() {}

// TaskUsage summarizes resource use for a task.
type TaskUsage struct {
	TotalTokens int `json:"total_tokens"`
	ToolUses    int `json:"tool_uses"`
	DurationMs  int `json:"duration_ms"`
}

// TaskStartedMessage reports that a (sub)task has started.
type TaskStartedMessage struct {
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	UUID        string `json:"uuid"`
	SessionID   string `json:"session_id"`
	ToolUseID   string `json:"tool_use_id,omitempty"`
	TaskType    string `json:"task_type,omitempty"`
	Raw         json.RawMessage
}

func (*TaskStartedMessage) isMessage() {}

// TaskProgressMessage reports incremental progress for a running task.
type TaskProgressMessage struct {
	TaskID       string    `json:"task_id"`
	Description  string    `json:"description"`
	Usage        TaskUsage `json:"usage"`
	UUID         string    `json:"uuid"`
	SessionID    string    `json:"session_id"`
	ToolUseID    string    `json:"tool_use_id,omitempty"`
	LastToolName string    `json:"last_tool_name,omitempty"`
	Raw          json.RawMessage
}

func (*TaskProgressMessage) isMessage() {}

// TaskNotificationMessage reports a task completion/failure/stop from the CLI.
// Typed fields are populated where present; Raw holds the full payload.
type TaskNotificationMessage struct {
	TaskID     string                 `json:"task_id"`
	Status     TaskNotificationStatus `json:"status"`
	Summary    string                 `json:"summary"`
	OutputFile string                 `json:"output_file"`
	SessionID  string                 `json:"session_id"`
	UUID       string                 `json:"uuid,omitempty"`
	ToolUseID  string                 `json:"tool_use_id,omitempty"`
	Usage      *TaskUsage             `json:"usage,omitempty"`
	Raw        json.RawMessage
}

func (*TaskNotificationMessage) isMessage() {}

// MirrorErrorMessage is emitted when a [SessionStore] append fails during live
// mirroring. It is non-fatal: the on-disk transcript is already durable.
type MirrorErrorMessage struct {
	Key   *SessionKey `json:"key,omitempty"`
	Error string      `json:"error"`
	Raw   json.RawMessage
}

func (*MirrorErrorMessage) isMessage() {}
