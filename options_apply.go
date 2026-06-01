package claude

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/shindakun/agent-sdk-go/internal/protocol"
)

// buildArgs maps Options onto the CLI argument vector, excluding the fixed base
// flags supplied by the transport. External MCP servers are serialized into a
// --mcp-config JSON blob; in-process SdkMcpServers are advertised by name with
// type "sdk" so the CLI routes their tool calls back over the control protocol.
func (o *Options) buildArgs() ([]string, error) {
	var args []string

	if o.model != "" {
		args = append(args, "--model", o.model)
	}
	if o.fallbackModel != "" {
		args = append(args, "--fallback-model", o.fallbackModel)
	}

	switch o.systemPrompt.mode {
	case systemPromptReplace:
		args = append(args, "--system-prompt", o.systemPrompt.text)
	case systemPromptAppend:
		args = append(args, "--append-system-prompt", o.systemPrompt.text)
	}

	if len(o.allowedTools) > 0 {
		args = append(args, "--allowedTools", joinComma(o.allowedTools))
	}
	if len(o.disallowedTools) > 0 {
		args = append(args, "--disallowedTools", joinComma(o.disallowedTools))
	}
	if o.maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(o.maxTurns))
	}
	if o.maxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(o.maxBudgetUSD, 'f', -1, 64))
	}
	if len(o.betas) > 0 {
		args = append(args, "--betas", joinComma(o.betas))
	}

	if o.thinking.enabled {
		args = append(args, "--thinking")
		if o.thinking.maxTokens > 0 {
			args = append(args, "--max-thinking-tokens", strconv.Itoa(o.thinking.maxTokens))
		}
		if o.thinking.effort != "" {
			args = append(args, "--effort", o.thinking.effort)
		}
	}

	settings, err := o.buildSettings()
	if err != nil {
		return nil, err
	}
	if settings != "" {
		args = append(args, "--settings", settings)
	}
	for _, dir := range o.addDirs {
		args = append(args, "--add-dir", dir)
	}
	if o.permissionMode != "" {
		args = append(args, "--permission-mode", string(o.permissionMode))
	}
	if o.resume != "" {
		args = append(args, "--resume", o.resume)
	}
	if o.forkSession {
		args = append(args, "--fork-session")
	}
	for _, dir := range o.pluginDirs {
		args = append(args, "--plugin-dir", dir)
	}
	if len(o.jsonSchema) > 0 {
		args = append(args, "--json-schema", string(o.jsonSchema))
	}
	// In stream-json mode the CLI routes permission requests to the SDK over
	// the control protocol automatically when a CanUseTool callback is present;
	// no flag is required for that (matching the official SDKs). The
	// --permission-prompt-tool flag is a separate, explicitly-configured option.
	if o.permissionPromptToolName != "" {
		args = append(args, "--permission-prompt-tool", o.permissionPromptToolName)
	}
	if o.continueConversation {
		args = append(args, "--continue")
	}
	if o.includePartialMessages {
		args = append(args, "--include-partial-messages")
	}
	if len(o.settingSources) > 0 {
		args = append(args, "--setting-sources", joinComma(o.settingSources))
	}

	mcpArg, err := o.buildMcpConfig()
	if err != nil {
		return nil, err
	}
	if mcpArg != "" {
		args = append(args, "--mcp-config", mcpArg)
	}

	// Forward-compat passthrough, sorted for deterministic output.
	if len(o.extraArgs) > 0 {
		keys := make([]string, 0, len(o.extraArgs))
		for k := range o.extraArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "--"+k)
			if v := o.extraArgs[k]; v != nil {
				args = append(args, *v)
			}
		}
	}

	return args, nil
}

// buildSettings returns the value for --settings. When a sandbox is configured,
// it is merged into the settings object; if the user-supplied settings is a file
// path (not JSON), the sandbox is wrapped into a fresh settings object alongside
// an "extends" pointer so the file still loads.
func (o *Options) buildSettings() (string, error) {
	if o.sandbox == nil {
		return o.settings, nil
	}

	merged := map[string]any{}
	if o.settings != "" {
		// Try to parse existing settings as inline JSON; if it isn't JSON treat
		// it as a file path the CLI should still load via "extends".
		if json.Valid([]byte(o.settings)) {
			if err := json.Unmarshal([]byte(o.settings), &merged); err != nil {
				return "", err
			}
		} else {
			merged["extends"] = o.settings
		}
	}
	merged["sandbox"] = o.sandbox

	b, err := json.Marshal(merged)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// buildInitializeRequest assembles the SDK->CLI initialize handshake payload.
// The registry registers hook callbacks and yields the hooks config; agents and
// skills are serialized here as the CLI expects them.
func (o *Options) buildInitializeRequest(reg *callbackRegistry) (protocol.InitializeRequest, error) {
	var req protocol.InitializeRequest

	hooks, err := reg.build(o)
	if err != nil {
		return req, err
	}
	req.Hooks = hooks

	if len(o.agents) > 0 {
		b, err := json.Marshal(o.agents)
		if err != nil {
			return req, err
		}
		req.Agents = b
	}
	if len(o.skills) > 0 {
		b, err := json.Marshal(o.skills)
		if err != nil {
			return req, err
		}
		req.Skills = b
	}
	if o.excludeDynamicSections {
		v := true
		req.ExcludeDynamicSections = &v
	}
	return req, nil
}

// buildMcpConfig serializes the configured MCP servers into the JSON blob the
// CLI expects via --mcp-config. In-process SdkMcpServers are emitted with type
// "sdk"; external servers carry their command/url configuration.
func (o *Options) buildMcpConfig() (string, error) {
	if len(o.mcpServers) == 0 {
		return "", nil
	}

	servers := make(map[string]any, len(o.mcpServers))
	for name, cfg := range o.mcpServers {
		switch c := cfg.(type) {
		case *SdkMcpServer:
			servers[name] = map[string]any{"type": "sdk", "name": c.Name}
		case StdioMcpServer:
			m := map[string]any{"type": "stdio", "command": c.Command}
			if len(c.Args) > 0 {
				m["args"] = c.Args
			}
			if len(c.Env) > 0 {
				m["env"] = c.Env
			}
			servers[name] = m
		case HTTPMcpServer:
			m := map[string]any{"type": "http", "url": c.URL}
			if len(c.Headers) > 0 {
				m["headers"] = c.Headers
			}
			servers[name] = m
		case SSEMcpServer:
			m := map[string]any{"type": "sse", "url": c.URL}
			if len(c.Headers) > 0 {
				m["headers"] = c.Headers
			}
			servers[name] = m
		}
	}

	blob, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", err
	}
	return string(blob), nil
}

// sdkMcpServers returns the in-process MCP servers keyed by their registered
// name, for control-protocol routing.
func (o *Options) sdkMcpServers() map[string]*SdkMcpServer {
	var out map[string]*SdkMcpServer
	for name, cfg := range o.mcpServers {
		if s, ok := cfg.(*SdkMcpServer); ok {
			if out == nil {
				out = map[string]*SdkMcpServer{}
			}
			out[name] = s
		}
	}
	return out
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
