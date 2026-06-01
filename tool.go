package claude

import (
	"context"
	"encoding/json"
)

// ToolHandler executes an in-process tool. args holds the raw JSON arguments
// provided by the model.
type ToolHandler func(ctx context.Context, args json.RawMessage) (ToolResult, error)

// Tool is an in-process tool exposed to the agent through an [SdkMcpServer].
type Tool struct {
	Name        string
	Description string
	// InputSchema is the tool's JSON Schema. When nil, an empty object schema
	// is advertised.
	InputSchema json.RawMessage
	Handler     ToolHandler
}

// ToolResult is the outcome of a [ToolHandler].
type ToolResult struct {
	// Content is the result payload, typically one or more [TextBlock]s.
	Content []ContentBlock
	IsError bool
}

// SdkMcpServer is an in-process MCP server whose tools run inside the host
// program. Register it with [WithSDKMCPServer]; the SDK answers the CLI's MCP
// requests for this server over the control protocol.
type SdkMcpServer struct {
	Name    string
	Version string
	tools   map[string]Tool
	order   []string
}

func (*SdkMcpServer) isMcpServerConfig() {}

// SdkMcpServerOption configures an [SdkMcpServer].
type SdkMcpServerOption func(*SdkMcpServer)

// WithServerVersion sets the server's advertised version.
func WithServerVersion(v string) SdkMcpServerOption {
	return func(s *SdkMcpServer) { s.Version = v }
}

// WithTools adds tools to the server.
func WithTools(tools ...Tool) SdkMcpServerOption {
	return func(s *SdkMcpServer) {
		for _, t := range tools {
			s.addTool(t)
		}
	}
}

// NewSdkMcpServer creates an in-process MCP server named name.
func NewSdkMcpServer(name string, opts ...SdkMcpServerOption) *SdkMcpServer {
	s := &SdkMcpServer{Name: name, Version: "1.0.0", tools: map[string]Tool{}}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// AddTool registers a tool and returns the server for chaining.
func (s *SdkMcpServer) AddTool(t Tool) *SdkMcpServer {
	s.addTool(t)
	return s
}

func (s *SdkMcpServer) addTool(t Tool) {
	if _, exists := s.tools[t.Name]; !exists {
		s.order = append(s.order, t.Name)
	}
	s.tools[t.Name] = t
}
