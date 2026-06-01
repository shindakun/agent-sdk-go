package claude

import (
	"encoding/json"
	"io"
)

// SystemPromptMode selects how the system prompt is configured.
type SystemPromptMode int

const (
	// systemPromptUnset means no system prompt configuration.
	systemPromptUnset SystemPromptMode = iota
	// systemPromptReplace replaces the system prompt with a literal string.
	systemPromptReplace
	// systemPromptAppend appends to the default system prompt.
	systemPromptAppend
)

// systemPromptConfig holds the system-prompt configuration.
type systemPromptConfig struct {
	mode SystemPromptMode
	text string
}

// thinkingConfig holds extended-thinking configuration.
type thinkingConfig struct {
	enabled   bool
	maxTokens int
	effort    string // "low" | "medium" | "high"
}

// Options configures a [Query] or [Client]. Construct it with the With*
// functional options rather than setting fields directly.
type Options struct {
	// CLI-flag-mapped configuration.
	model           string
	fallbackModel   string
	systemPrompt    systemPromptConfig
	allowedTools    []string
	disallowedTools []string
	maxTurns        int
	maxBudgetUSD    float64
	betas           []string
	thinking        thinkingConfig
	settings        string
	addDirs         []string
	permissionMode  PermissionMode
	resume          string
	forkSession     bool
	cwd             string
	env             map[string]string
	pluginDirs      []string
	jsonSchema      json.RawMessage
	extraArgs       map[string]*string

	// initialize-request-mapped configuration.
	hooks                  map[HookEvent][]HookMatcher
	agents                 map[string]AgentDefinition
	skills                 []string
	excludeDynamicSections bool
	mcpServers             map[string]McpServerConfig

	// runtime callbacks.
	canUseTool CanUseTool

	// transport configuration.
	cliPath string
	stderr  io.Writer
}

// Option mutates an [Options].
type Option func(*Options)

func newOptions(opts ...Option) *Options {
	o := &Options{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// WithModel sets the model (for example "claude-sonnet-4-6" or an alias like
// "opus").
func WithModel(model string) Option {
	return func(o *Options) { o.model = model }
}

// WithFallbackModel sets a fallback model used if the primary is unavailable.
func WithFallbackModel(model string) Option {
	return func(o *Options) { o.fallbackModel = model }
}

// WithSystemPrompt replaces the system prompt with prompt.
func WithSystemPrompt(prompt string) Option {
	return func(o *Options) {
		o.systemPrompt = systemPromptConfig{mode: systemPromptReplace, text: prompt}
	}
}

// WithAppendSystemPrompt appends prompt to the default system prompt.
func WithAppendSystemPrompt(prompt string) Option {
	return func(o *Options) {
		o.systemPrompt = systemPromptConfig{mode: systemPromptAppend, text: prompt}
	}
}

// WithAllowedTools pre-approves the named tools.
func WithAllowedTools(tools ...string) Option {
	return func(o *Options) { o.allowedTools = append(o.allowedTools, tools...) }
}

// WithDisallowedTools blocks the named tools.
func WithDisallowedTools(tools ...string) Option {
	return func(o *Options) { o.disallowedTools = append(o.disallowedTools, tools...) }
}

// WithMaxTurns caps the number of agent turns.
func WithMaxTurns(n int) Option {
	return func(o *Options) { o.maxTurns = n }
}

// WithMaxBudgetUSD caps the total spend for the session in US dollars.
func WithMaxBudgetUSD(usd float64) Option {
	return func(o *Options) { o.maxBudgetUSD = usd }
}

// WithBetas enables the named API beta flags.
func WithBetas(betas ...string) Option {
	return func(o *Options) { o.betas = append(o.betas, betas...) }
}

// WithThinking enables extended thinking. maxTokens and effort are optional
// (pass 0 / "" to omit).
func WithThinking(maxTokens int, effort string) Option {
	return func(o *Options) {
		o.thinking = thinkingConfig{enabled: true, maxTokens: maxTokens, effort: effort}
	}
}

// WithSettings points the CLI at a settings file or JSON string.
func WithSettings(settings string) Option {
	return func(o *Options) { o.settings = settings }
}

// WithAddDir grants the agent access to additional directories.
func WithAddDir(dirs ...string) Option {
	return func(o *Options) { o.addDirs = append(o.addDirs, dirs...) }
}

// WithPermissionMode sets the initial permission mode.
func WithPermissionMode(mode PermissionMode) Option {
	return func(o *Options) { o.permissionMode = mode }
}

// WithResume resumes a prior session by ID.
func WithResume(sessionID string) Option {
	return func(o *Options) { o.resume = sessionID }
}

// WithForkSession, combined with WithResume, forks the resumed session rather
// than continuing it in place.
func WithForkSession() Option {
	return func(o *Options) { o.forkSession = true }
}

// WithCwd sets the working directory for the CLI subprocess.
func WithCwd(dir string) Option {
	return func(o *Options) { o.cwd = dir }
}

// WithEnv adds environment variables for the CLI subprocess.
func WithEnv(env map[string]string) Option {
	return func(o *Options) {
		if o.env == nil {
			o.env = map[string]string{}
		}
		for k, v := range env {
			o.env[k] = v
		}
	}
}

// WithPluginDir adds plugin directories.
func WithPluginDir(dirs ...string) Option {
	return func(o *Options) { o.pluginDirs = append(o.pluginDirs, dirs...) }
}

// WithJSONSchema constrains the final result to the given JSON Schema.
func WithJSONSchema(schema json.RawMessage) Option {
	return func(o *Options) { o.jsonSchema = schema }
}

// WithExtraArgs passes raw flags through to the CLI for forward-compatibility.
// A nil value yields a boolean flag (--name); a non-nil value yields
// --name value.
func WithExtraArgs(args map[string]*string) Option {
	return func(o *Options) {
		if o.extraArgs == nil {
			o.extraArgs = map[string]*string{}
		}
		for k, v := range args {
			o.extraArgs[k] = v
		}
	}
}

// WithHooks registers lifecycle hooks.
func WithHooks(hooks map[HookEvent][]HookMatcher) Option {
	return func(o *Options) {
		if o.hooks == nil {
			o.hooks = map[HookEvent][]HookMatcher{}
		}
		for ev, matchers := range hooks {
			o.hooks[ev] = append(o.hooks[ev], matchers...)
		}
	}
}

// WithAgents registers subagent definitions.
func WithAgents(agents map[string]AgentDefinition) Option {
	return func(o *Options) {
		if o.agents == nil {
			o.agents = map[string]AgentDefinition{}
		}
		for name, def := range agents {
			o.agents[name] = def
		}
	}
}

// WithSkills enables the named skills.
func WithSkills(skills ...string) Option {
	return func(o *Options) { o.skills = append(o.skills, skills...) }
}

// WithExcludeDynamicSections omits dynamic system-prompt sections.
func WithExcludeDynamicSections() Option {
	return func(o *Options) { o.excludeDynamicSections = true }
}

// WithMCPServers registers MCP servers by name. Values may be external configs
// ([StdioMcpServer], [HTTPMcpServer], [SSEMcpServer]) or in-process
// [*SdkMcpServer] instances.
func WithMCPServers(servers map[string]McpServerConfig) Option {
	return func(o *Options) {
		if o.mcpServers == nil {
			o.mcpServers = map[string]McpServerConfig{}
		}
		for name, cfg := range servers {
			o.mcpServers[name] = cfg
		}
	}
}

// WithSDKMCPServer registers a single in-process MCP server under name.
func WithSDKMCPServer(name string, server *SdkMcpServer) Option {
	return func(o *Options) {
		if o.mcpServers == nil {
			o.mcpServers = map[string]McpServerConfig{}
		}
		o.mcpServers[name] = server
	}
}

// WithCanUseTool registers a permission callback.
func WithCanUseTool(fn CanUseTool) Option {
	return func(o *Options) { o.canUseTool = fn }
}

// WithCLIPath overrides discovery of the `claude` binary.
func WithCLIPath(path string) Option {
	return func(o *Options) { o.cliPath = path }
}

// WithStderr directs the CLI subprocess's stderr to w.
func WithStderr(w io.Writer) Option {
	return func(o *Options) { o.stderr = w }
}
