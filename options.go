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
	// systemPromptFile loads the system prompt from a file.
	systemPromptFile
)

// systemPromptConfig holds the system-prompt configuration.
type systemPromptConfig struct {
	mode SystemPromptMode
	text string
}

// Options configures a [Query] or [Client]. Construct it with the With*
// functional options rather than setting fields directly.
type Options struct {
	// CLI-flag-mapped configuration.
	model                    string
	fallbackModel            string
	systemPrompt             systemPromptConfig
	systemPromptSnapshot     *bool
	allowedTools             []string
	disallowedTools          []string
	maxTurns                 int
	maxBudgetUSD             float64
	betas                    []string
	thinking                 ThinkingConfig // typed union, or nil
	maxThinkingTokens        int            // deprecated scalar path
	thinkingDisplay          ThinkingDisplay
	settings                 string
	addDirs                  []string
	permissionMode           PermissionMode
	permissionPromptToolName string
	resume                   string
	resumeSessionAt          string
	resumeDropsTurn          *string
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
	forwardSubagentText    bool
	mcpServers             map[string]McpServerConfig
	mcpConfigRaw           string // a file path or JSON string, passed to --mcp-config directly

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

	// SDK-side behavior, not sent to the CLI as flags.
	verbatimPrompts bool

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

// WithSystemPromptSnapshot sets whether the session keeps the system prompt it
// recorded on its first request. It applies to [WithSystemPrompt] and
// [WithAppendSystemPrompt]. With keep true (the CLI default, except in bare
// mode), every later request, including after a resume, sends the recorded
// prompt, so a changed prompt has no effect until the session is compacted or
// a new one starts. With keep false the prompt is rebuilt on every request,
// for example while iterating on its wording across resumes of one session.
//
// Requires Claude Code 2.1.257 or later. Before 2.1.265, a session with an
// appended or custom prompt recorded it only when keep was true.
func WithSystemPromptSnapshot(keep bool) Option {
	return func(o *Options) { o.systemPromptSnapshot = &keep }
}

// WithSystemPromptFile loads the system prompt from the file at path (maps to
// --system-prompt-file).
func WithSystemPromptFile(path string) Option {
	return func(o *Options) {
		o.systemPrompt = systemPromptConfig{mode: systemPromptFile, text: path}
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

// WithThinkingConfig sets the extended-thinking configuration. Pass one of
// [ThinkingConfigAdaptive], [ThinkingConfigEnabled], or [ThinkingConfigDisabled].
// It takes precedence over [WithMaxThinkingTokens].
func WithThinkingConfig(cfg ThinkingConfig) Option {
	return func(o *Options) { o.thinking = cfg }
}

// WithMaxThinkingTokens sets the thinking token budget via the deprecated
// scalar path (maps to --max-thinking-tokens). Prefer [WithThinkingConfig] with
// [ThinkingConfigEnabled].
func WithMaxThinkingTokens(n int) Option {
	return func(o *Options) { o.maxThinkingTokens = n }
}

// WithThinkingDisplay controls how thinking output is shown (maps to
// --thinking-display). Applies to adaptive and enabled thinking.
func WithThinkingDisplay(d ThinkingDisplay) Option {
	return func(o *Options) { o.thinkingDisplay = d }
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

// WithResumeSessionAt resumes only up to and including the transcript entry
// with this UUID. Use it with [WithResume] (usually also [WithForkSession]) to
// branch from an earlier point. It accepts any transcript-entry UUID, such as
// an [AssistantMessage] UUID seen live or a [SessionMessage] UUID from
// [GetSessionMessages]. See [WithResumeDropsTurn] for choosing the point.
func WithResumeSessionAt(uuid string) Option {
	return func(o *Options) { o.resumeSessionAt = uuid }
}

// WithResumeDropsTurn, with [WithResumeSessionAt], names the user prompt whose
// turn the truncating resume means to discard. The CLI then checks that every
// transcript entry after the truncation point belongs to that turn, and
// refuses the resume otherwise (for example when the discarded range holds a
// queued message or task notification the caller never saw). A refusal fails
// the connect with a [*ResultError] whose message contains
// "Resume rejected by --resume-drops-turn:". Treat it as final: clear the
// fork target and resume plainly rather than retrying.
//
// Set WithResumeSessionAt to the last entry of the turn you keep, and this to
// the prompt UUID of the turn right after it. An empty uuid is still sent, so
// the CLI rejects it as malformed rather than the check being silently off.
func WithResumeDropsTurn(uuid string) Option {
	return func(o *Options) { o.resumeDropsTurn = &uuid }
}

// WithForkSession forks the resumed session (when combined with WithResume)
// rather than continuing it in place.
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

// WithVerbatimPrompts delivers every prompt to Claude as written. Each user
// message the SDK sends is marked client_composed, so Claude Code does not
// expand @path file mentions or dispatch slash commands in it. Use it when
// prompt text includes content the end user did not type (prior turns, tool
// results, third-party text): otherwise an @/absolute/path inside it makes
// Claude Code read that file into the prompt, whatever tools are allowed.
//
// A turn delivered this way also skips Claude Code's turn-start attachment
// pass: @server:resource MCP mentions are not expanded, and the prompt goes
// without the context normally attached to it (nested CLAUDE.md and rules
// files, skill and tool listings, per-turn reminders). The pass Claude Code
// runs between tool calls is unaffected, so most of that context arrives after
// the turn's first tool call.
//
// Requires Claude Code 2.1.248 or later; older versions ignore the marker.
// When [WithStderr] is set, connecting to an older CLI with this option on
// writes a warning there.
func WithVerbatimPrompts() Option {
	return func(o *Options) { o.verbatimPrompts = true }
}

// WithSettingSources controls which filesystem settings sources the CLI loads
// (for example "user", "project", "local"). When unset, the CLI default
// applies. Called with no sources, the CLI loads no settings files.
func WithSettingSources(sources ...string) Option {
	return func(o *Options) {
		if o.settingSources == nil {
			o.settingSources = []string{}
		}
		o.settingSources = append(o.settingSources, sources...)
	}
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

// WithLoadTimeout bounds each SessionStore call (Load, ListSessions,
// ListSubkeys) made while resuming a session from the store (see
// [WithSessionStore]). The default is 60 seconds; zero or less uses it. A
// store that does not answer in time fails the connect with an error instead
// of hanging.
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
//
// Combined with [WithResume] or [WithContinueConversation], the session is
// resumed from the store rather than from a local transcript: it is written to
// a temporary config dir the CLI runs against (CLAUDE_CONFIG_DIR), seeded with
// the caller's credentials and user settings, and removed on close. The resume
// value must be a session UUID. Cannot be combined with
// [WithEnableFileCheckpointing].
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

// WithForwardSubagentText forwards subagent text and thinking blocks on the
// stream. By default a subagent spawned with the Agent tool surfaces only its
// tool_use and tool_result blocks, as [AssistantMessage] and [UserMessage]
// values whose ParentToolUseID is the spawning Agent call. With this option its
// text and thinking arrive the same way, so the nested transcript can be
// rendered in full.
func WithForwardSubagentText() Option {
	return func(o *Options) { o.forwardSubagentText = true }
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

// WithMCPConfig passes an MCP configuration directly to --mcp-config as either a
// file path or a JSON string, mirroring the official SDK's string/path form of
// mcp_servers. It takes precedence over [WithMCPServers] / [WithSDKMCPServer].
func WithMCPConfig(pathOrJSON string) Option {
	return func(o *Options) { o.mcpConfigRaw = pathOrJSON }
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
