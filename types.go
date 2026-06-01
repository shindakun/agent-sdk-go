package claude

import "encoding/json"

// This file mirrors the remaining public types of the official SDK so callers
// have name-for-name equivalents. Wire keys use the CLI's camelCase where the
// type is sent to or received from the CLI.

// EffortLevel selects the model's reasoning effort.
type EffortLevel string

const (
	EffortLow    EffortLevel = "low"
	EffortMedium EffortLevel = "medium"
	EffortHigh   EffortLevel = "high"
	EffortXHigh  EffortLevel = "xhigh"
	EffortMax    EffortLevel = "max"
)

// SdkBeta names an API beta the SDK understands.
type SdkBeta string

const SdkBetaContext1M SdkBeta = "context-1m-2025-08-07"

// TaskBudget caps a task's token budget.
type TaskBudget struct {
	Total int `json:"total"`
}

// ThinkingDisplay controls how thinking output is shown.
type ThinkingDisplay string

const (
	ThinkingDisplaySummarized ThinkingDisplay = "summarized"
	ThinkingDisplayOmitted    ThinkingDisplay = "omitted"
)

// ThinkingConfig is the union of thinking configuration shapes:
// [ThinkingConfigAdaptive], [ThinkingConfigEnabled], [ThinkingConfigDisabled].
type ThinkingConfig interface {
	isThinkingConfig()
}

// ThinkingConfigAdaptive enables adaptive thinking.
type ThinkingConfigAdaptive struct {
	Type    string          `json:"type"` // "adaptive"
	Display ThinkingDisplay `json:"display,omitempty"`
}

func (ThinkingConfigAdaptive) isThinkingConfig() {}

// ThinkingConfigEnabled enables thinking with a fixed token budget.
type ThinkingConfigEnabled struct {
	Type         string          `json:"type"` // "enabled"
	BudgetTokens int             `json:"budget_tokens"`
	Display      ThinkingDisplay `json:"display,omitempty"`
}

func (ThinkingConfigEnabled) isThinkingConfig() {}

// ThinkingConfigDisabled disables thinking.
type ThinkingConfigDisabled struct {
	Type string `json:"type"` // "disabled"
}

func (ThinkingConfigDisabled) isThinkingConfig() {}

// TaskNotificationStatus is the terminal state of a task.
type TaskNotificationStatus string

const (
	TaskCompleted TaskNotificationStatus = "completed"
	TaskFailed    TaskNotificationStatus = "failed"
	TaskStopped   TaskNotificationStatus = "stopped"
)

// --- Sandbox configuration ---------------------------------------------------

// SandboxSettings configures the CLI's command sandbox.
type SandboxSettings struct {
	Enabled                   bool                     `json:"enabled,omitempty"`
	AutoAllowBashIfSandboxed  bool                     `json:"autoAllowBashIfSandboxed,omitempty"`
	ExcludedCommands          []string                 `json:"excludedCommands,omitempty"`
	AllowUnsandboxedCommands  bool                     `json:"allowUnsandboxedCommands,omitempty"`
	Network                   *SandboxNetworkConfig    `json:"network,omitempty"`
	IgnoreViolations          *SandboxIgnoreViolations `json:"ignoreViolations,omitempty"`
	EnableWeakerNestedSandbox bool                     `json:"enableWeakerNestedSandbox,omitempty"`
}

// SandboxNetworkConfig configures sandbox network access.
type SandboxNetworkConfig struct {
	AllowedDomains          []string `json:"allowedDomains,omitempty"`
	DeniedDomains           []string `json:"deniedDomains,omitempty"`
	AllowManagedDomainsOnly bool     `json:"allowManagedDomainsOnly,omitempty"`
	AllowUnixSockets        []string `json:"allowUnixSockets,omitempty"`
	AllowAllUnixSockets     bool     `json:"allowAllUnixSockets,omitempty"`
	AllowLocalBinding       bool     `json:"allowLocalBinding,omitempty"`
	AllowMachLookup         []string `json:"allowMachLookup,omitempty"`
	HTTPProxyPort           int      `json:"httpProxyPort,omitempty"`
	SOCKSProxyPort          int      `json:"socksProxyPort,omitempty"`
}

// SandboxIgnoreViolations lists violations the sandbox should ignore.
type SandboxIgnoreViolations struct {
	File    []string `json:"file,omitempty"`
	Network []string `json:"network,omitempty"`
}

// --- Rate limits -------------------------------------------------------------

// RateLimitStatus is the state of a rate limit.
type RateLimitStatus string

const (
	RateLimitAllowed        RateLimitStatus = "allowed"
	RateLimitAllowedWarning RateLimitStatus = "allowed_warning"
	RateLimitRejected       RateLimitStatus = "rejected"
)

// RateLimitType identifies which rate-limit window applies.
type RateLimitType string

const (
	RateLimitFiveHour       RateLimitType = "five_hour"
	RateLimitSevenDay       RateLimitType = "seven_day"
	RateLimitSevenDayOpus   RateLimitType = "seven_day_opus"
	RateLimitSevenDaySonnet RateLimitType = "seven_day_sonnet"
	RateLimitOverage        RateLimitType = "overage"
)

// RateLimitInfo describes the current rate-limit posture.
type RateLimitInfo struct {
	Status                RateLimitStatus  `json:"status"`
	ResetsAt              *int64           `json:"resets_at,omitempty"`
	RateLimitType         *RateLimitType   `json:"rate_limit_type,omitempty"`
	Utilization           *float64         `json:"utilization,omitempty"`
	OverageStatus         *RateLimitStatus `json:"overage_status,omitempty"`
	OverageResetsAt       *int64           `json:"overage_resets_at,omitempty"`
	OverageDisabledReason *string          `json:"overage_disabled_reason,omitempty"`
	Raw                   json.RawMessage  `json:"-"`
}

// RateLimitEvent wraps a rate-limit update. It is also a [Message] emitted on
// the stream as a rate_limit_event frame.
type RateLimitEvent struct {
	RateLimitInfo RateLimitInfo `json:"rate_limit_info"`
	UUID          string        `json:"uuid"`
	SessionID     string        `json:"session_id"`
}

func (*RateLimitEvent) isMessage() {}

// --- Context usage -----------------------------------------------------------

// ContextUsageCategory is one slice of the context-window breakdown.
type ContextUsageCategory struct {
	Name       string `json:"name"`
	Tokens     int    `json:"tokens"`
	Color      string `json:"color"`
	IsDeferred bool   `json:"isDeferred,omitempty"`
}

// ContextUsageResponse is the typed form of a get_context_usage result. Decode
// it from [ContextUsage.Raw] with [ContextUsage.Typed].
type ContextUsageResponse struct {
	Categories           []ContextUsageCategory `json:"categories"`
	TotalTokens          int                    `json:"totalTokens"`
	MaxTokens            int                    `json:"maxTokens"`
	RawMaxTokens         int                    `json:"rawMaxTokens"`
	Percentage           float64                `json:"percentage"`
	Model                string                 `json:"model"`
	IsAutoCompactEnabled bool                   `json:"isAutoCompactEnabled"`
	MemoryFiles          json.RawMessage        `json:"memoryFiles,omitempty"`
	McpTools             json.RawMessage        `json:"mcpTools,omitempty"`
	Agents               json.RawMessage        `json:"agents,omitempty"`
	GridRows             json.RawMessage        `json:"gridRows,omitempty"`
	AutoCompactThreshold int                    `json:"autoCompactThreshold,omitempty"`
	DeferredBuiltinTools json.RawMessage        `json:"deferredBuiltinTools,omitempty"`
	SystemTools          json.RawMessage        `json:"systemTools,omitempty"`
	SystemPromptSections json.RawMessage        `json:"systemPromptSections,omitempty"`
	SlashCommands        json.RawMessage        `json:"slashCommands,omitempty"`
	Skills               json.RawMessage        `json:"skills,omitempty"`
	MessageBreakdown     json.RawMessage        `json:"messageBreakdown,omitempty"`
	APIUsage             json.RawMessage        `json:"apiUsage,omitempty"`
}

// --- MCP status --------------------------------------------------------------

// McpServerConnectionStatus is an MCP server's connection state.
type McpServerConnectionStatus string

// McpServerInfo is an MCP server's reported identity.
type McpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// McpToolAnnotations describes a tool's behavior hints.
type McpToolAnnotations struct {
	ReadOnly    bool `json:"readOnly,omitempty"`
	Destructive bool `json:"destructive,omitempty"`
	OpenWorld   bool `json:"openWorld,omitempty"`
}

// ToolAnnotations is an alias of [McpToolAnnotations] for parity with the
// official naming.
type ToolAnnotations = McpToolAnnotations

// McpToolInfo describes one tool offered by an MCP server.
type McpToolInfo struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Annotations *McpToolAnnotations `json:"annotations,omitempty"`
}

// McpServerStatusInfo is the typed status of one MCP server (the official SDK
// calls this McpServerStatus; that name is used here for the control-method
// return type, so the typed status carries the "Info" suffix).
type McpServerStatusInfo struct {
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	ServerInfo *McpServerInfo  `json:"serverInfo,omitempty"`
	Error      string          `json:"error,omitempty"`
	Scope      string          `json:"scope,omitempty"`
	Tools      []McpToolInfo   `json:"tools,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// McpStatusResponse is the typed form of an mcp_status result.
type McpStatusResponse struct {
	McpServers []McpServerStatusInfo `json:"mcpServers"`
}

// --- Server tool blocks ------------------------------------------------------

// ServerToolName names a built-in server-side tool.
type ServerToolName string

const (
	ServerToolAdvisor            ServerToolName = "advisor"
	ServerToolWebSearch          ServerToolName = "web_search"
	ServerToolWebFetch           ServerToolName = "web_fetch"
	ServerToolCodeExecution      ServerToolName = "code_execution"
	ServerToolBashCodeExecution  ServerToolName = "bash_code_execution"
	ServerToolTextEditorCodeExec ServerToolName = "text_editor_code_execution"
	ServerToolSearchRegex        ServerToolName = "tool_search_tool_regex"
	ServerToolSearchBM25         ServerToolName = "tool_search_tool_bm25"
)

// ServerToolUseBlock is a server-side tool invocation.
type ServerToolUseBlock struct {
	ID    string          `json:"id"`
	Name  ServerToolName  `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (*ServerToolUseBlock) isContentBlock() {}

// ServerToolResultBlock is a server-side tool result.
type ServerToolResultBlock struct {
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

func (*ServerToolResultBlock) isContentBlock() {}

// DeferredToolUse is a tool call deferred to the caller, surfaced on a result.
type DeferredToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// --- Permission updates ------------------------------------------------------

// PermissionUpdate describes a runtime change to the permission ruleset.
type PermissionUpdate struct {
	Type        string          `json:"type"` // addRules | replaceRules | removeRules | setMode | addDirectories | removeDirectories
	Rules       json.RawMessage `json:"rules,omitempty"`
	Behavior    string          `json:"behavior,omitempty"`
	Mode        PermissionMode  `json:"mode,omitempty"`
	Directories []string        `json:"directories,omitempty"`
	Destination string          `json:"destination,omitempty"`
}
