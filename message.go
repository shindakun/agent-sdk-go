package claude

import (
	"encoding/json"
	"fmt"
)

// Message is a single item in the stream emitted by the CLI. The concrete types
// are [AssistantMessage], [UserMessage], [SystemMessage], [ResultMessage],
// [StreamEvent], [RateLimitEvent], [ConversationResetMessage], the task
// lifecycle messages ([TaskStartedMessage], [TaskProgressMessage],
// [TaskNotificationMessage], [TaskUpdatedMessage]), [HookEventMessage], and
// [MirrorErrorMessage]. Frame types this version does not know are skipped.
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
	MessageID       string `json:"message_id,omitempty"`
	StopReason      string `json:"stop_reason,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	UUID            string `json:"uuid,omitempty"`
	Usage           *Usage `json:"usage,omitempty"`
	// Error is set when the turn failed, such as ErrorRateLimit.
	Error AssistantMessageError `json:"error,omitempty"`
	// Raw is the undecoded JSON of the message for forward-compatibility.
	Raw json.RawMessage
}

func (*AssistantMessage) isMessage() {}

// AssistantMessageError classifies a failed assistant turn.
type AssistantMessageError string

const (
	ErrorAuthenticationFailed AssistantMessageError = "authentication_failed"
	ErrorBillingError         AssistantMessageError = "billing_error"
	ErrorRateLimit            AssistantMessageError = "rate_limit"
	ErrorInvalidRequest       AssistantMessageError = "invalid_request"
	ErrorServerError          AssistantMessageError = "server_error"
	ErrorUnknown              AssistantMessageError = "unknown"
)

// UserMessage is a user turn, including synthesized tool-result turns.
type UserMessage struct {
	Content         []ContentBlock
	ParentToolUseID string
	UUID            string          `json:"uuid,omitempty"`
	ToolUseResult   json.RawMessage `json:"tool_use_result,omitempty"`
	// Origin is the provenance of this message, or nil when the CLI did not
	// attribute it. It is set on turns the session injects (task
	// notifications, channel and peer messages, ...) and on user messages the
	// CLI replays; tool-result messages never carry it.
	Origin *MessageOrigin `json:"origin,omitempty"`
	Raw    json.RawMessage
}

func (*UserMessage) isMessage() {}

// PluginInfo describes a plugin loaded into the session, as reported in the
// "init" system message.
type PluginInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
	// Version is the version declared in the plugin's manifest, verbatim.
	// Omitted when the manifest declares none.
	Version string `json:"version,omitempty"`
}

// SystemInitPlugin is a loaded plugin listed in [SystemInitData].
type SystemInitPlugin = PluginInfo

// SystemInitMcpServer is an MCP server listed in [SystemInitData].
type SystemInitMcpServer struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Source is where the server definition came from: "sdk", "plugin", or a
	// config scope such as "user" or "project". Empty on CLIs that predate the
	// field.
	Source string `json:"source,omitempty"`
}

// SystemInitPluginError is a plugin load failure listed in [SystemInitData].
type SystemInitPluginError struct {
	// Plugin is "name@marketplace", or a positional tag such as "inline[0]" or
	// "synced[0]" when a directory entry failed before it had a name.
	Plugin string `json:"plugin"`
	// Type is the failure category from an open set (for example
	// "path-not-found", "manifest-validation-error"). Treat unknown values as
	// a generic failure.
	Type    string `json:"type"`
	Message string `json:"message"`
	// Path is set only when a plugins, --plugin-dir or synced directory entry
	// did not load at all: that entry's path, resolved against the cwd. Use it
	// to match a positional Plugin tag to your entry.
	Path string `json:"path,omitempty"`
}

// FastModeState is the fast mode state reported in [SystemInitData].
type FastModeState string

const (
	FastModeOff      FastModeState = "off"
	FastModeCooldown FastModeState = "cooldown"
	FastModeOn       FastModeState = "on"
)

// FastModeDisabledReason names why fast mode can't serve right now, as
// reported in [SystemInitData]. Newer CLIs may send values not listed here.
type FastModeDisabledReason string

const (
	FastModeDisabledFree               FastModeDisabledReason = "free"
	FastModeDisabledPreference         FastModeDisabledReason = "preference"
	FastModeDisabledExtraUsageDisabled FastModeDisabledReason = "extra_usage_disabled"
	FastModeDisabledNetworkError       FastModeDisabledReason = "network_error"
	FastModeDisabledUnknown            FastModeDisabledReason = "unknown"
	FastModeDisabledNotFirstParty      FastModeDisabledReason = "not_first_party"
	FastModeDisabledByEnv              FastModeDisabledReason = "disabled_by_env"
	FastModeDisabledModelNotAllowed    FastModeDisabledReason = "model_not_allowed"
	FastModeDisabledSdkOptInRequired   FastModeDisabledReason = "sdk_opt_in_required"
	FastModeDisabledPending            FastModeDisabledReason = "pending"
)

// SystemInitData is the payload of a system/init message: the session metadata
// the CLI emits at the start of each turn (model, working directory, tools, MCP
// servers, slash commands, permission mode, and the capabilities list for
// feature detection). JSON keys match the wire. Decode it with
// [SystemMessage.InitData].
//
// The parser does not validate the payload, so this describes what the CLI
// sends rather than a guarantee. Fields marked optional are empty when the CLI
// omits them.
type SystemInitData struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
	// Agents is optional.
	Agents []string `json:"agents,omitempty"`
	// APIKeySource is where the API credential came from: "ANTHROPIC_API_KEY",
	// "apiKeyHelper", "/login managed key", or "none" (no API key in use, for
	// example a claude.ai login or a third-party cloud provider).
	APIKeySource string `json:"apiKeySource"`
	// Betas is optional.
	Betas             []string              `json:"betas,omitempty"`
	ClaudeCodeVersion string                `json:"claude_code_version"`
	Cwd               string                `json:"cwd"`
	Tools             []string              `json:"tools"`
	McpServers        []SystemInitMcpServer `json:"mcp_servers"`
	Model             string                `json:"model"`
	PermissionMode    PermissionMode        `json:"permissionMode"`
	SlashCommands     []string              `json:"slash_commands"`
	// TerminalSlashCommands lists the SlashCommands entries whose interface is
	// bound to the local terminal (for example "exit"). Present only when
	// non-empty.
	TerminalSlashCommands []string           `json:"terminal_slash_commands,omitempty"`
	OutputStyle           string             `json:"output_style"`
	Skills                []string           `json:"skills"`
	Plugins               []SystemInitPlugin `json:"plugins"`
	// PluginErrors lists plugin load failures. The CLI omits the key when
	// nothing failed, but an empty list does not by itself assert a clean load
	// (older CLIs and some hosted sessions never send it).
	PluginErrors []SystemInitPluginError `json:"plugin_errors,omitempty"`
	// FastModeState is optional.
	FastModeState FastModeState `json:"fast_mode_state,omitempty"`
	// FastModeDisabledReason names the check that blocks fast mode, when
	// something does. Optional.
	FastModeDisabledReason FastModeDisabledReason `json:"fast_mode_disabled_reason,omitempty"`
	// Capabilities lists protocol capabilities this CLI supports (for example
	// "interrupt_receipt_v1"), for feature detection instead of comparing
	// ClaudeCodeVersion. Open set: ignore values you don't recognize. Absent on
	// CLIs before 2.1.205.
	Capabilities []string `json:"capabilities,omitempty"`
}

// SystemMessage carries out-of-band events from the CLI. Subtype distinguishes
// the payload (for example "init" or "session_state_changed"); subtype-specific
// fields are populated where known, and Data holds the full payload. For the
// "init" subtype, [SystemMessage.InitData] decodes Data as [SystemInitData].
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

// InitData decodes Data as the [SystemInitData] payload of a system/init
// message. It returns an error if Subtype is not "init" or Data does not
// decode.
func (m *SystemMessage) InitData() (*SystemInitData, error) {
	if m.Subtype != "init" {
		return nil, fmt.Errorf("claude: InitData on system message with subtype %q, want \"init\"", m.Subtype)
	}
	var d SystemInitData
	if err := json.Unmarshal(m.Data, &d); err != nil {
		return nil, fmt.Errorf("claude: decode system/init data: %w", err)
	}
	return &d, nil
}

// ModelUsage reports per-model token usage and cost, keyed in
// [ResultMessage.ModelUsage] by the raw model string the CLI billed.
//
// The JSON keys here are camelCase, unlike the snake_case used elsewhere in the
// stream: the CLI passes its modelUsage value through verbatim. Verified
// against a live result frame from CLI 2.1.222.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`

	// CanonicalModel is the model id used for the pricing lookup (for example
	// "claude-opus-5"). It may differ from the raw model string this entry is
	// keyed by, which can carry provider-specific ids or aliases.
	CanonicalModel string `json:"canonicalModel,omitempty"`

	// Provider is the API provider that served this model ("firstParty",
	// "bedrock", "vertex", "foundry", "anthropicAws", "anthropicGoogleCloud",
	// "mantle", "gateway").
	Provider string `json:"provider,omitempty"`
}

// Usage reports token usage for a turn or result.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ResultMessage terminates a turn, summarizing cost, duration, and outcome.
//
// TerminalReason reports why the query loop ended. "aborted_streaming" or
// "aborted_tools" mean the turn was cancelled (via [Client.Interrupt] or an
// interrupt control request), which is how a cancelled turn is told apart from
// one that ran to completion. It is empty when the CLI reported no terminal
// reason: older CLI versions, or a result that bypassed the query loop such as
// a local slash command.
type ResultMessage struct {
	Subtype           string                `json:"subtype"`
	IsError           bool                  `json:"is_error"`
	Errors            []string              `json:"errors,omitempty"`
	DurationMs        int                   `json:"duration_ms"`
	DurationAPIMs     int                   `json:"duration_api_ms,omitempty"`
	NumTurns          int                   `json:"num_turns"`
	StopReason        string                `json:"stop_reason,omitempty"`
	TerminalReason    string                `json:"terminal_reason,omitempty"`
	TotalCostUSD      float64               `json:"total_cost_usd,omitempty"`
	Usage             Usage                 `json:"usage"`
	ModelUsage        map[string]ModelUsage `json:"modelUsage,omitempty"`
	Result            string                `json:"result,omitempty"`
	StructuredOutput  json.RawMessage       `json:"structured_output,omitempty"`
	PermissionDenials json.RawMessage       `json:"permission_denials,omitempty"`
	DeferredToolUse   *DeferredToolUse      `json:"deferred_tool_use,omitempty"`
	APIErrorStatus    *int                  `json:"api_error_status,omitempty"`
	SessionID         string                `json:"session_id"`
	UUID              string                `json:"uuid,omitempty"`
	// Origin is the origin of the user message that triggered this turn. It
	// tells the result of your own prompt (nil) from results of turns the
	// session injected, such as background-task notifications.
	Origin *MessageOrigin `json:"origin,omitempty"`
	Raw    json.RawMessage
}

func (*ResultMessage) isMessage() {}

// ConversationResetMessage reports that the session's conversation was
// replaced without ending the connection, for example after /clear. The reset
// clears the history and zeroes the running totals on later [ResultMessage]s
// (such as TotalCostUSD), so snapshot accumulated totals when it arrives.
type ConversationResetMessage struct {
	// NewConversationID identifies the fresh conversation, for keying an empty
	// transcript. It is not the SessionID of later messages; read that from
	// the next message.
	NewConversationID string `json:"new_conversation_id"`
	UUID              string `json:"uuid"`
	// SessionID is the session that was reset; later messages carry a new one.
	SessionID string `json:"session_id"`
	Raw       json.RawMessage
}

func (*ConversationResetMessage) isMessage() {}

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

// TaskUpdatedMessage is emitted as a background task's state changes
// (system/task_updated). Patch carries the changed fields (e.g. status,
// end_time); Status mirrors patch.status when present. A task's terminal state
// can arrive *only* here (no accompanying TaskNotificationMessage) — for example
// a task stopped via StopTask reports Status "killed". Use [IsTerminalTaskStatus]
// to detect completion across both message types.
type TaskUpdatedMessage struct {
	TaskID    string            `json:"task_id"`
	Patch     json.RawMessage   `json:"patch"`
	Status    TaskUpdatedStatus `json:"-"` // extracted from patch.status
	SessionID string            `json:"session_id,omitempty"`
	UUID      string            `json:"uuid,omitempty"`
	Raw       json.RawMessage
}

func (*TaskUpdatedMessage) isMessage() {}

// HookEventMessage is a hook lifecycle event emitted on the stream when
// [WithIncludeHookEvents] is set. It arrives as a system message with subtype
// "hook_started" (a hook begins) or "hook_response" (a hook completes).
// HookEventName identifies the event (PreToolUse, PostToolUse, Stop, …); the
// full payload is in Data.
type HookEventMessage struct {
	Subtype       string // "hook_started" | "hook_response"
	HookEventName string
	SessionID     string
	UUID          string
	Data          json.RawMessage
	Raw           json.RawMessage
}

func (*HookEventMessage) isMessage() {}

// MirrorErrorMessage is emitted when a [SessionStore] append fails during live
// mirroring. It is non-fatal: the on-disk transcript is already durable.
type MirrorErrorMessage struct {
	Key   *SessionKey `json:"key,omitempty"`
	Error string      `json:"error"`
	Raw   json.RawMessage
}

func (*MirrorErrorMessage) isMessage() {}
