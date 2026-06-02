package claude

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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
	// Annotations advertises behavior hints (read-only, destructive, ...).
	Annotations *ToolAnnotations
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

// NewTool builds a typed tool. The input JSON Schema is derived from T's struct
// fields (using `json` tags for names), and the handler receives T already
// decoded from the model's arguments. This is the ergonomic way to define a
// tool; for full control over the schema, construct a [Tool] directly.
func NewTool[T any](name, description string, fn func(ctx context.Context, in T) (ToolResult, error)) Tool {
	var zero T
	schema := schemaFor(reflect.TypeOf(zero))
	return Tool{
		Name:        name,
		Description: description,
		InputSchema: schema,
		Handler: func(ctx context.Context, args json.RawMessage) (ToolResult, error) {
			var in T
			if len(args) > 0 {
				if err := json.Unmarshal(args, &in); err != nil {
					return ToolResult{}, err
				}
			}
			return fn(ctx, in)
		},
	}
}

// TextResult is a convenience constructor for a successful text [ToolResult].
func TextResult(text string) ToolResult {
	return ToolResult{Content: []ContentBlock{&TextBlock{Text: text}}}
}

// ErrorResult is a convenience constructor for an error text [ToolResult].
func ErrorResult(text string) ToolResult {
	return ToolResult{Content: []ContentBlock{&TextBlock{Text: text}}, IsError: true}
}

// schemaFor produces a minimal JSON Schema object for a struct type. Non-struct
// types yield a permissive empty-object schema.
func schemaFor(t reflect.Type) json.RawMessage {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return json.RawMessage(`{"type":"object"}`)
	}

	schema := structSchema(t, map[reflect.Type]bool{})
	b, err := json.Marshal(schema)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return b
}

// structSchema builds a JSON Schema object for a struct type, recursing into
// nested struct/slice/map fields. seen guards against recursive types.
func structSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	if seen[t] {
		return map[string]any{"type": "object"}
	}
	seen[t] = true
	defer delete(seen, t)

	props := map[string]any{}
	var required []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		props[name] = fieldSchema(f.Type, seen)
		if !strings.Contains(opts, "omitempty") {
			required = append(required, name)
		}
	}

	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// fieldSchema returns the JSON Schema for a single field type, recursing into
// nested structs, slice element types, and map value types.
func fieldSchema(t reflect.Type, seen map[reflect.Type]bool) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": fieldSchema(t.Elem(), seen)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": fieldSchema(t.Elem(), seen)}
	case reflect.Struct:
		return structSchema(t, seen)
	default:
		return map[string]any{"type": "string"}
	}
}
