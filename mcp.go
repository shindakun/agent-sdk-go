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
// Agent tool.
type AgentDefinition struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
}
