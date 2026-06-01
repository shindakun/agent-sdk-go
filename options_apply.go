package claude

import (
	"encoding/json"
	"fmt"
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

	// Thinking config takes precedence over the deprecated scalar token budget,
	// mirroring the reference: adaptive/disabled emit --thinking <value>, enabled
	// emits --max-thinking-tokens <budget>, and --thinking-display applies to
	// non-disabled configs.
	switch t := o.thinking.(type) {
	case ThinkingConfigAdaptive:
		args = append(args, "--thinking", "adaptive")
		if o.thinkingDisplay != "" || t.Display != "" {
			args = append(args, "--thinking-display", displayOr(t.Display, o.thinkingDisplay))
		}
	case ThinkingConfigEnabled:
		args = append(args, "--max-thinking-tokens", strconv.Itoa(t.BudgetTokens))
		if o.thinkingDisplay != "" || t.Display != "" {
			args = append(args, "--thinking-display", displayOr(t.Display, o.thinkingDisplay))
		}
	case ThinkingConfigDisabled:
		args = append(args, "--thinking", "disabled")
	case nil:
		if o.maxThinkingTokens > 0 {
			args = append(args, "--max-thinking-tokens", strconv.Itoa(o.maxThinkingTokens))
		}
	}

	// Standalone effort (independent of thinking config).
	if o.effort != "" {
		args = append(args, "--effort", o.effort)
	}

	if o.toolsSet {
		if o.toolsPreset {
			args = append(args, "--tools", "default")
		} else {
			args = append(args, "--tools", joinComma(o.tools))
		}
	}
	if o.sessionID != "" {
		args = append(args, "--session-id", o.sessionID)
	}
	if o.strictMcpConfig {
		args = append(args, "--strict-mcp-config")
	}
	if o.includeHookEvents {
		args = append(args, "--include-hook-events")
	}
	if o.taskBudget != nil {
		args = append(args, "--task-budget", strconv.Itoa(o.taskBudget.Total))
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
	// Permission routing. A CanUseTool callback requires the CLI to delegate
	// permission prompts to the SDK over the control protocol; the official SDK
	// does this by setting --permission-prompt-tool to "stdio". CanUseTool and an
	// explicit permission-prompt-tool name are mutually exclusive.
	switch {
	case o.permissionPromptToolName != "" && o.canUseTool != nil:
		return nil, fmt.Errorf("claude: WithCanUseTool and WithPermissionPromptToolName are mutually exclusive")
	case o.canUseTool != nil:
		args = append(args, "--permission-prompt-tool", "stdio")
	case o.permissionPromptToolName != "":
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

// displayOr prefers the per-config display value, falling back to the
// option-level WithThinkingDisplay.
func displayOr(cfg, opt ThinkingDisplay) string {
	if cfg != "" {
		return string(cfg)
	}
	return string(opt)
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
