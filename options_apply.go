package claude

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

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
	case systemPromptUnset:
		// Matching the official SDK: with no system prompt configured, the CLI
		// is given an empty one (suppressing Claude Code's default), not no flag.
		args = append(args, "--system-prompt", "")
	case systemPromptReplace:
		args = append(args, "--system-prompt", o.systemPrompt.text)
	case systemPromptAppend:
		args = append(args, "--append-system-prompt", o.systemPrompt.text)
	case systemPromptFile:
		args = append(args, "--system-prompt-file", o.systemPrompt.text)
	}

	// Apply skills defaults: when skills are configured, the CLI needs the
	// Skill(name) tool(s) in allowedTools and a setting-sources default so it
	// can discover installed skills (matching the official SDK). Names are
	// validated first: they are formatted into --allowedTools, whose tokenizer
	// would silently mis-split a name carrying a delimiter.
	for _, name := range o.skills {
		if err := validateSkillName(name); err != nil {
			return nil, err
		}
	}
	allowed, settingSources := o.effectiveSkillsDefaults()
	if len(allowed) > 0 {
		args = append(args, "--allowedTools", joinComma(allowed))
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
		if err := rejectWindowsCmdMetacharacters("sessionID", o.sessionID); err != nil {
			return nil, err
		}
		args = append(args, "--session-id="+o.sessionID)
	}
	if o.strictMcpConfig {
		args = append(args, "--strict-mcp-config")
	}
	if o.includeHookEvents {
		args = append(args, "--include-hook-events")
	}
	// When a live SessionStore mirror is configured, tell the CLI to emit
	// transcript_mirror frames. Without this flag no mirror frames arrive.
	if o.sessionStore != nil {
		args = append(args, "--session-mirror")
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
	// Pass these as --flag=value rather than as two argv tokens. The CLI
	// declares --resume with an optional value, so in the two-token form a
	// dash-leading value is not bound to the flag and is parsed as a separate
	// CLI flag instead, letting an untrusted value inject arbitrary flags.
	// The equals form always binds the value to the flag.
	if o.resume != "" {
		if err := rejectWindowsCmdMetacharacters("resume", o.resume); err != nil {
			return nil, err
		}
		args = append(args, "--resume="+o.resume)
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
	if len(settingSources) > 0 {
		args = append(args, "--setting-sources", joinComma(settingSources))
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
			v := o.extraArgs[k]
			if v == nil {
				// A valueless flag must stay a bare token: --flag=  would be
				// an error for flags the CLI declares without a value.
				args = append(args, "--"+k)
				continue
			}
			// Bind a dash-leading value to its flag so it cannot be parsed as
			// a separate CLI flag (the same class the --resume equals form
			// closes). Other values keep the two-token form, which is what
			// string-driven boolean flags rely on.
			if strings.HasPrefix(*v, "-") {
				args = append(args, "--"+k+"="+*v)
			} else {
				args = append(args, "--"+k, *v)
			}
		}
	}

	return args, nil
}

// effectiveSkillsDefaults computes the allowedTools and setting-sources after
// applying skills defaults, matching the official SDK's _apply_skills_defaults:
// each configured skill injects a Skill(name) tool, and setting-sources defaults
// to ["user","project"] when unset so the CLI discovers installed skills. The
// original Options is not mutated.
func (o *Options) effectiveSkillsDefaults() (allowed, settingSources []string) {
	allowed = append([]string(nil), o.allowedTools...)
	if len(o.settingSources) > 0 {
		settingSources = append([]string(nil), o.settingSources...)
	}

	if len(o.skills) == 0 {
		return allowed, settingSources
	}

	for _, name := range o.skills {
		pattern := "Skill(" + name + ")"
		if !contains(allowed, pattern) {
			allowed = append(allowed, pattern)
		}
	}
	if settingSources == nil {
		settingSources = []string{"user", "project"}
	}
	return allowed, settingSources
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
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
	// A raw file path / JSON string is passed through directly.
	if o.mcpConfigRaw != "" {
		return o.mcpConfigRaw, nil
	}
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

// validateSkillName rejects skill names that cannot ride safely in a
// Skill(name) rule.
//
// Names from WithSkills are formatted into the --allowedTools value, which the
// CLI splits into rules on commas and spaces outside parentheses. That
// tokenizer does not honor escape sequences (escaping exists only in the
// per-rule grammar, applied after splitting), so a name carrying a delimiter
// cannot be passed through reliably: what it tokenizes into depends on what
// surrounds it.
//
// Names that tokenize cleanly but can never match the listed skill are
// rejected too, so a dead rule fails loudly here instead of silently granting
// nothing. Each check states its own reason.
//
// Upstream also guards against a bare string being passed where a list is
// expected; that is unreachable here, since WithSkills is variadic over string.
// Its unpaired-surrogate check becomes a UTF-8 validity check: a Go string is a
// byte slice that may hold arbitrary bytes, and an invalid encoding can never
// match a name the CLI discovered.
func validateSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("invalid skill name %q: skill names must be non-empty", name)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("invalid skill name %q: leading or trailing whitespace can never match, "+
			"since the Skill tool trims the invoked name", name)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("invalid skill name %q: contains invalid UTF-8, "+
			"which can never match a skill the CLI discovered", name)
	}
	if i := strings.IndexFunc(name, isSkillNameInvalidRune); i >= 0 {
		return fmt.Errorf("invalid skill name %q: parentheses, commas, control characters, and "+
			"byte-order marks are not allowed. Names match the skill's directory name, or "+
			"'plugin:skill' for plugin-qualified skills", name)
	}
	if name == "*" {
		return fmt.Errorf(`invalid skill name "*": enable every skill by leaving WithSkills unset`)
	}
	if strings.HasSuffix(name, ":*") || strings.HasSuffix(name, " *") {
		return fmt.Errorf("invalid skill name %q: wildcard-suffix names are not allowed; "+
			"list each skill by its exact name", name)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("invalid skill name %q: skill names may not start with '/'. "+
			"WithSkills takes the canonical name, not the slash-command form", name)
	}
	if strings.Contains(name, `\\`) {
		return fmt.Errorf("invalid skill name %q: consecutive backslashes are not allowed, "+
			"since the per-rule parser collapses them and the rule would name a different skill", name)
	}
	if strings.HasSuffix(name, `\`) {
		return fmt.Errorf("invalid skill name %q: names may not end with an unpaired backslash", name)
	}
	return nil
}

// isSkillNameInvalidRune reports whether r may not appear in a skill name.
// Parentheses and commas are delimiters to the --allowedTools tokenizer;
// control characters (C0, DEL, C1) never appear in a skill directory name.
// U+FEFF is included because the CLI trims it as whitespace while
// strings.TrimSpace does not.
func isSkillNameInvalidRune(r rune) bool {
	switch {
	case r == '(' || r == ')' || r == ',':
		return true
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0xfeff:
		return true
	}
	return false
}

// cmdExeMetacharacters are the characters cmd.exe treats specially.
const cmdExeMetacharacters = `&|<>^%!"`

// rejectWindowsCmdMetacharacters is defense in depth for Windows. With batch
// script spawning refused (see the transport's isWindowsBatchCLI), these
// characters are harmless: Go quotes correctly for native executables. They are
// rejected anyway so that resume and sessionID values, which applications
// commonly take from external input, stay inert even if a cmd.exe hop is ever
// reintroduced between the SDK and the CLI. No format is imposed beyond this
// (resume values may be arbitrary session titles, not only UUIDs), and POSIX
// behavior is unchanged.
func rejectWindowsCmdMetacharacters(optionName, value string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	var bad []string
	seen := map[rune]bool{}
	for _, r := range value {
		if seen[r] {
			continue
		}
		if strings.ContainsRune(cmdExeMetacharacters, r) || r == '\r' || r == '\n' {
			seen[r] = true
			bad = append(bad, strconv.QuoteRune(r))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s value %q contains characters that are unsafe to pass on a Windows command line: %s",
		optionName, value, strings.Join(bad, ", "))
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
