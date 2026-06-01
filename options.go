package claude

import (
	"encoding/json"
	"io"
	"time"
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
	model                    string
	fallbackModel            string
	systemPrompt             systemPromptConfig
	allowedTools             []string
	disallowedTools          []string
	maxTurns                 int
	maxBudgetUSD             float64
	betas                    []string
	thinking                 thinkingConfig
	settings                 string
	addDirs                  []string
	permissionMode           PermissionMode
	permissionPromptToolName string
	resume                   string
	forkSession              bool
	continueConversation     bool
	includePartialMessages   bool
	settingSources           []string
	cwd                      string
	env                      map[string]string
	pluginDirs               []string
	jsonSchema               json.RawMessage
	extraArgs                map[string]*string

	// initialize-request-mapped configuration.
	hooks                  map[HookEvent][]HookMatcher
	agents                 map[string]AgentDefinition
	skills                 []string
	excludeDynamicSections bool
	mcpServers             map[string]McpServerConfig

	sandbox *SandboxSettings

	tools                   []string
	toolsPreset             bool // emit --tools default
	toolsSet                bool
	sessionID               string
	strictMcpConfig         bool
	includeHookEvents       bool
	effort                  string
	taskBudget              *TaskBudget
	maxBufferSize           int
	loadTimeout             time.Duration
	enableFileCheckpointing bool
	userUID                 *int
	userGID                 int

	sessionStore      SessionStore
	sessionStoreFlush SessionStoreFlushMode

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

// WithContinueConversation continues the most recent conversation.
func WithContinueConversation() Option {
	return func(o *Options) { o.continueConversation = true }
}

// WithIncludePartialMessages enables partial/streaming message events
// ([StreamEvent]).
func WithIncludePartialMessages() Option {
	return func(o *Options) { o.includePartialMessages = true }
}

// WithSettingSources controls which filesystem settings sources the CLI loads
// (for example "user", "project", "local"). When unset, the CLI default
// applies.
func WithSettingSources(sources ...string) Option {
	return func(o *Options) { o.settingSources = append(o.settingSources, sources...) }
}

// WithSandbox configures the CLI command sandbox.
func WithSandbox(s SandboxSettings) Option {
	return func(o *Options) { o.sandbox = &s }
}

// WithPermissionPromptToolName sets the MCP tool the CLI uses to prompt for
// permission decisions. This is independent of [WithCanUseTool], which receives
// decisions over the control protocol.
func WithPermissionPromptToolName(name string) Option {
	return func(o *Options) { o.permissionPromptToolName = name }
}

// WithToolList sets the explicit tool list (maps to --tools). An empty slice
// disables all tools. (Named WithToolList to avoid colliding with the
// SdkMcpServer option WithTools.)
func WithToolList(tools ...string) Option {
	return func(o *Options) {
		o.tools = tools
		o.toolsSet = true
		o.toolsPreset = false
	}
}

// WithToolsPreset selects the default tool preset (maps to --tools default).
func WithToolsPreset() Option {
	return func(o *Options) {
		o.toolsPreset = true
		o.toolsSet = true
	}
}

// WithSessionID sets an explicit session id (maps to --session-id).
func WithSessionID(id string) Option {
	return func(o *Options) { o.sessionID = id }
}

// WithStrictMcpConfig restricts MCP servers to those in the provided config
// (maps to --strict-mcp-config).
func WithStrictMcpConfig() Option {
	return func(o *Options) { o.strictMcpConfig = true }
}

// WithIncludeHookEvents surfaces hook lifecycle events on the stream (maps to
// --include-hook-events).
func WithIncludeHookEvents() Option {
	return func(o *Options) { o.includeHookEvents = true }
}

// WithEffort sets the reasoning effort independently of thinking config (maps
// to --effort).
func WithEffort(level EffortLevel) Option {
	return func(o *Options) { o.effort = string(level) }
}

// WithTaskBudget caps the task token budget (maps to --task-budget).
func WithTaskBudget(b TaskBudget) Option {
	return func(o *Options) { o.taskBudget = &b }
}

// WithMaxBufferSize caps the size of a single stream-json line the transport
// will buffer before erroring. Zero uses the default.
func WithMaxBufferSize(bytes int) Option {
	return func(o *Options) { o.maxBufferSize = bytes }
}

// WithLoadTimeout overrides the initialize-handshake timeout.
func WithLoadTimeout(d time.Duration) Option {
	return func(o *Options) { o.loadTimeout = d }
}

// WithEnableFileCheckpointing enables SDK file checkpointing (sets
// CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true for the subprocess).
func WithEnableFileCheckpointing() Option {
	return func(o *Options) { o.enableFileCheckpointing = true }
}

// WithUser runs the CLI subprocess as the given OS user/group id (Unix only).
func WithUser(uid, gid int) Option {
	return func(o *Options) {
		u := uid
		o.userUID = &u
		o.userGID = gid
	}
}

// WithSessionStore mirrors the live transcript into store using the given flush
// mode. Append failures surface as a [MirrorErrorMessage] on the stream.
func WithSessionStore(store SessionStore, flush SessionStoreFlushMode) Option {
	return func(o *Options) {
		o.sessionStore = store
		o.sessionStoreFlush = flush
	}
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

// WithPlugins registers structured local plugin configs. Each adds its path as
// a plugin directory.
func WithPlugins(plugins ...SdkPluginConfig) Option {
	return func(o *Options) {
		for _, p := range plugins {
			if p.Path != "" {
				o.pluginDirs = append(o.pluginDirs, p.Path)
			}
		}
	}
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
