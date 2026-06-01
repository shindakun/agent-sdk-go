package claude

// McpServerConfig describes an MCP server made available to the agent. The
// concrete types are [StdioMcpServer], [HTTPMcpServer], [SSEMcpServer] (external
// servers, serialized into --mcp-config) and [*SdkMcpServer] (in-process,
// serviced by this SDK over the control protocol).
type McpServerConfig interface {
	isMcpServerConfig()
}

// StdioMcpServer is an external MCP server launched as a subprocess by the CLI.
type StdioMcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (StdioMcpServer) isMcpServerConfig() {}

// HTTPMcpServer is an external MCP server reached over streamable HTTP.
type HTTPMcpServer struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (HTTPMcpServer) isMcpServerConfig() {}

// SSEMcpServer is an external MCP server reached over server-sent events.
type SSEMcpServer struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

func (SSEMcpServer) isMcpServerConfig() {}

// AgentDefinition declares a subagent the main agent can delegate to via the
// Agent tool. Wire keys use the camelCase the CLI expects.
type AgentDefinition struct {
	Description     string   `json:"description"`
	Prompt          string   `json:"prompt"`
	Tools           []string `json:"tools,omitempty"`
	DisallowedTools []string `json:"disallowedTools,omitempty"`
	Model           string   `json:"model,omitempty"`
	Skills          []string `json:"skills,omitempty"`
	Memory          string   `json:"memory,omitempty"` // "user" | "project" | "local"
	MCPServers      []string `json:"mcpServers,omitempty"`
	InitialPrompt   string   `json:"initialPrompt,omitempty"`
	MaxTurns        int      `json:"maxTurns,omitempty"`
	Background      bool     `json:"background,omitempty"`
	Effort          string   `json:"effort,omitempty"`
	PermissionMode  string   `json:"permissionMode,omitempty"`
}

// SettingSource names a filesystem settings source the CLI may load.
type SettingSource string

const (
	SettingSourceUser    SettingSource = "user"
	SettingSourceProject SettingSource = "project"
	SettingSourceLocal   SettingSource = "local"
)

// SdkPluginConfig configures a local plugin directory.
type SdkPluginConfig struct {
	Type string `json:"type"` // always "local"
	Path string `json:"path"`
}
