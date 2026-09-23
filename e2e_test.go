//go:build e2e

// Package claude e2e tests run the full, faithful end-to-end suite against a
// real `claude` binary, mirroring the upstream e2e-tests/ directory. They make
// real (paid) API calls, so they are gated behind the `e2e` build tag and use
// tiny prompts. Run with:
//
//	go test -tags e2e -timeout 900s ./...
//
// The lighter `integration` tier (integration_test.go) covers the smoke set.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func e2eSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not on PATH")
	}
}

func e2eCtx(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 120*time.Second)
}

// runToResult drives a one-shot query and returns the terminal ResultMessage.
func runToResult(t *testing.T, prompt string, opts ...Option) *ResultMessage {
	t.Helper()
	ctx, cancel := e2eCtx(t)
	defer cancel()
	return runToResultCtx(t, ctx, prompt, opts...)
}

// runToResultTimeout is like runToResult with an explicit deadline, for slow
// paths such as subagent delegation.
func runToResultTimeout(t *testing.T, d time.Duration, prompt string, opts ...Option) *ResultMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return runToResultCtx(t, ctx, prompt, opts...)
}

func runToResultCtx(t *testing.T, ctx context.Context, prompt string, opts ...Option) *ResultMessage {
	t.Helper()
	var result *ResultMessage
	for msg, err := range Query(ctx, prompt, opts...) {
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if rm, ok := msg.(*ResultMessage); ok {
			result = rm
		}
	}
	if result == nil {
		t.Fatal("no result message")
	}
	return result
}

// --- Structured output (mirrors test_structured_output.py) -------------------

func TestE2EStructuredOutputSimple(t *testing.T) {
	e2eSkip(t)
	schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"number"},"is_even":{"type":"boolean"}},"required":["answer","is_even"]}`)
	r := runToResult(t, "What is 6 plus 4? Is the result even? Respond using the structured schema.",
		WithJSONSchema(schema))
	if r.IsError {
		t.Fatalf("error: %v", r.Errors)
	}
	if len(r.StructuredOutput) == 0 {
		t.Fatal("no structured_output on result")
	}
	var out struct {
		Answer float64 `json:"answer"`
		IsEven bool    `json:"is_even"`
	}
	if err := json.Unmarshal(r.StructuredOutput, &out); err != nil {
		t.Fatalf("structured_output not valid: %v (%s)", err, r.StructuredOutput)
	}
	if out.Answer != 10 {
		t.Errorf("answer = %v, want 10", out.Answer)
	}
	if !out.IsEven {
		t.Errorf("is_even = false, want true")
	}
}

func TestE2EStructuredOutputEnum(t *testing.T) {
	e2eSkip(t)
	schema := json.RawMessage(`{"type":"object","properties":{"color":{"type":"string","enum":["red","green","blue"]}},"required":["color"]}`)
	r := runToResult(t, "Pick the color of a clear daytime sky from the allowed options.",
		WithJSONSchema(schema))
	if r.IsError || len(r.StructuredOutput) == 0 {
		t.Fatalf("result=%+v", r)
	}
	var out struct {
		Color string `json:"color"`
	}
	_ = json.Unmarshal(r.StructuredOutput, &out)
	if out.Color != "blue" {
		t.Errorf("color = %q, want blue", out.Color)
	}
}

// --- Dynamic control (mirrors test_dynamic_control.py) -----------------------

func TestE2ESetModel(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()
	if err := client.SetModel(ctx, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("set model: %v", err)
	}
}

func TestE2ESetPermissionMode(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()
	if err := client.SetPermissionMode(ctx, PermissionAcceptEdits); err != nil {
		t.Fatalf("set permission mode: %v", err)
	}
}

// --- Hooks (mirrors test_hooks.py / test_hook_events.py) ---------------------

func TestE2EHookFires(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var fired atomic.Bool
	hook := func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error) {
		fired.Store(true)
		return HookOutput{}, nil
	}
	for _, err := range Query(ctx, "Run `echo hi` with the Bash tool.",
		WithAllowedTools("Bash"), WithPermissionMode(PermissionBypass),
		WithHooks(map[HookEvent][]HookMatcher{
			HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{hook}}},
		}),
	) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if !fired.Load() {
		t.Error("hook never fired")
	}
}

func TestE2EHookPermissionDecisionDeny(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var blocked atomic.Bool
	hook := func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error) {
		in, _ := DecodePreToolUse(input)
		if in.ToolName == "Bash" {
			blocked.Store(true)
			return HookOutput{
				SystemMessage:      "blocked by hook",
				HookSpecificOutput: json.RawMessage(`{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"blocked in test"}`),
			}, nil
		}
		return HookOutput{}, nil
	}
	for _, err := range Query(ctx, "Use the Bash tool to run `echo hi`.",
		WithAllowedTools("Bash"),
		WithHooks(map[HookEvent][]HookMatcher{
			HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{hook}}},
		}),
	) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if !blocked.Load() {
		t.Error("PreToolUse hook deny was never invoked")
	}
}

func TestE2EMultipleHooks(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var pre, post atomic.Bool
	preCB := func(ctx context.Context, in json.RawMessage, id string) (HookOutput, error) {
		pre.Store(true)
		return HookOutput{}, nil
	}
	postCB := func(ctx context.Context, in json.RawMessage, id string) (HookOutput, error) {
		post.Store(true)
		return HookOutput{}, nil
	}
	for _, err := range Query(ctx, "Run `echo hi` with Bash.",
		WithAllowedTools("Bash"), WithPermissionMode(PermissionBypass),
		WithHooks(map[HookEvent][]HookMatcher{
			HookPreToolUse:  {{Matcher: "Bash", Callbacks: []HookCallback{preCB}}},
			HookPostToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{postCB}}},
		}),
	) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if !pre.Load() || !post.Load() {
		t.Errorf("hooks fired pre=%v post=%v, want both", pre.Load(), post.Load())
	}
}

// --- Setting sources (mirrors test_agents_and_settings.py) -------------------

func TestE2ESettingSources(t *testing.T) {
	e2eSkip(t)
	for _, sources := range [][]string{nil, {"user"}, {"user", "project"}} {
		opts := []Option{}
		if sources != nil {
			opts = append(opts, WithSettingSources(sources...))
		}
		r := runToResult(t, "Reply with the single word: ok", opts...)
		if r.IsError {
			t.Errorf("setting sources %v: error %v", sources, r.Errors)
		}
	}
}

// --- SDK MCP tools (mirrors test_sdk_mcp_tools.py) ----------------------------

func TestE2ESdkMcpMultipleTools(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	type nums struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	var mu sync.Mutex
	calls := map[string]int{}
	record := func(name string) { mu.Lock(); calls[name]++; mu.Unlock() }
	srv := NewSdkMcpServer("math").
		AddTool(NewTool("add", "Add a and b", func(ctx context.Context, in nums) (ToolResult, error) {
			record("add")
			return TextResult(itoaE2E(in.A + in.B)), nil
		})).
		AddTool(NewTool("mul", "Multiply a and b", func(ctx context.Context, in nums) (ToolResult, error) {
			record("mul")
			return TextResult(itoaE2E(in.A * in.B)), nil
		}))

	for _, err := range Query(ctx,
		"Using the provided tools, compute (3 + 4) then multiply that by 2. Report only the final number.",
		WithSDKMCPServer("math", srv),
		WithAllowedTools("mcp__math__add", "mcp__math__mul"),
	) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["add"] == 0 || calls["mul"] == 0 {
		t.Errorf("expected both tools called; calls=%v", calls)
	}
}

func TestE2ESdkMcpPermissionEnforcement(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	type nums struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	var called atomic.Bool
	srv := NewSdkMcpServer("calc").AddTool(
		NewTool("add", "Add a and b", func(ctx context.Context, in nums) (ToolResult, error) {
			called.Store(true)
			return TextResult(itoaE2E(in.A + in.B)), nil
		}))
	// The tool is NOT in allowed_tools; with a denying CanUseTool it must not run.
	deny := func(ctx context.Context, tool string, input json.RawMessage, pc PermissionContext) (PermissionResult, error) {
		return PermissionDeny{Message: "denied in test"}, nil
	}
	// No settings files: a user settings defaultMode such as auto would approve
	// the call before the callback is asked.
	for _, err := range Query(ctx, "Use the add tool to compute 2 + 2.",
		WithSDKMCPServer("calc", srv),
		WithCanUseTool(deny),
		WithSettingSources(),
	) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if called.Load() {
		t.Error("tool ran despite a denying permission callback")
	}
}

// --- Partial messages (mirrors test_include_partial_messages.py) -------------

func TestE2EPartialMessagesPresentAndAbsent(t *testing.T) {
	e2eSkip(t)

	count := func(opts ...Option) int {
		ctx, cancel := e2eCtx(t)
		defer cancel()
		n := 0
		for msg, err := range Query(ctx, "Say hello in one short sentence.", opts...) {
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if _, ok := msg.(*StreamEvent); ok {
				n++
			}
		}
		return n
	}

	withPartials := count(WithIncludePartialMessages())
	if withPartials == 0 {
		t.Error("expected StreamEvent partials with WithIncludePartialMessages")
	}
	withoutPartials := count()
	if withoutPartials != 0 {
		t.Errorf("expected no StreamEvents without the option; got %d", withoutPartials)
	}
}

// --- Agents (mirrors test_agents_and_settings.py) ----------------------------

func TestE2EAgentDefinition(t *testing.T) {
	e2eSkip(t)
	// Subagent delegation is the slowest path; give it extra headroom so it
	// doesn't flake under load when the whole suite runs sequentially.
	r := runToResultTimeout(t, 240*time.Second,
		"Use the echo-agent to repeat the word 'parity'. Reply with only that word.",
		WithAllowedTools("Agent"),
		WithAgents(map[string]AgentDefinition{
			"echo-agent": {
				Description: "Repeats a given word back.",
				Prompt:      "You repeat the exact word the user gives you, nothing else.",
			},
		}),
	)
	if r.IsError {
		t.Fatalf("error: %v", r.Errors)
	}
}

// --- Plugins -----------------------------------------------------------------

func TestE2EPluginLoaded(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	// Reuse the example's demo-plugin assets.
	pluginPath := "examples/plugins/demo-plugin"
	var loaded []PluginInfo
	for msg, err := range Query(ctx, "Hello!",
		WithPlugins(SdkPluginConfig{Type: "local", Path: pluginPath}),
		WithMaxTurns(1),
		// No settings files: user hooks can add a turn past WithMaxTurns(1).
		WithSettingSources(),
	) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if sm, ok := msg.(*SystemMessage); ok && sm.Subtype == "init" {
			loaded = sm.Plugins
		}
	}
	found := false
	for _, p := range loaded {
		if p.Name == "demo-plugin" {
			found = true
		}
	}
	if !found {
		t.Errorf("demo-plugin not reported in init plugins: %+v", loaded)
	}
}

// --- Stderr (mirrors test_stderr_callback.py) --------------------------------

func TestE2EStderrCallback(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var sb strings.Builder
	var mu sync.Mutex
	w := writerFunc(func(p []byte) (int, error) { mu.Lock(); sb.Write(p); mu.Unlock(); return len(p), nil })
	for _, err := range Query(ctx, "Say hi.", WithStderr(w)) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	// We don't assert specific content (the CLI may emit nothing on success),
	// only that wiring a stderr writer doesn't break the run.
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func itoaE2E(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// --- Client.ReceiveResponse / QuerySession -----------------------------------

func TestE2EReceiveResponse(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()

	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Query(ctx, "Reply with exactly: ok"); err != nil {
		t.Fatalf("query: %v", err)
	}

	var sawResult bool
	var afterResult int
	for msg, err := range client.ReceiveResponse(ctx) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if sawResult {
			afterResult++ // nothing should be yielded after the result
		}
		if _, ok := msg.(*ResultMessage); ok {
			sawResult = true
		}
	}
	if !sawResult {
		t.Error("ReceiveResponse never yielded a ResultMessage")
	}
	if afterResult != 0 {
		t.Errorf("ReceiveResponse yielded %d messages after the result", afterResult)
	}
}

func TestE2EQuerySessionMultiTurn(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// First turn: capture the session id and seed context.
	var sid string
	if err := client.Query(ctx, "Remember the word: kumquat. Reply 'ok'."); err != nil {
		t.Fatalf("query 1: %v", err)
	}
	for msg, err := range client.ReceiveResponse(ctx) {
		if err != nil {
			t.Fatalf("stream 1: %v", err)
		}
		if sm, ok := msg.(*SystemMessage); ok && sm.Subtype == "init" {
			sid = sm.SessionID
		}
	}
	if sid == "" {
		t.Fatal("no session id captured")
	}

	// Second turn addressed explicitly to that session id.
	if err := client.QuerySession(ctx, "What word did I ask you to remember? Reply with only that word.", sid); err != nil {
		t.Fatalf("query session: %v", err)
	}
	var result *ResultMessage
	for msg, err := range client.ReceiveResponse(ctx) {
		if err != nil {
			t.Fatalf("stream 2: %v", err)
		}
		if rm, ok := msg.(*ResultMessage); ok {
			result = rm
		}
	}
	if result == nil || !strings.Contains(strings.ToLower(result.Result), "kumquat") {
		t.Errorf("QuerySession lost context; result = %+v", result)
	}
}

// --- Verbatim prompts (mirrors test_verbatim_prompts.py) ----------------------

// verbatimOptions leaves the model no tools, so the only way it can learn the
// marker is the prompt's @-mention expansion.
func verbatimOptions(cwd string, verbatim bool) []Option {
	opts := []Option{
		WithCwd(cwd),
		WithModel("haiku"),
		WithToolList(),
		WithSettingSources(),
		WithStrictMcpConfig(),
		WithMaxTurns(1),
	}
	if verbatim {
		opts = append(opts, WithVerbatimPrompts())
	}
	return opts
}

// plantMarker writes a random marker to a file outside the working directory
// and returns the prompt that @-mentions it, the marker, and the cwd.
func plantMarker(t *testing.T) (prompt, marker, cwd string) {
	t.Helper()
	dir := t.TempDir()
	marker = fmt.Sprintf("MARKER-%x", time.Now().UnixNano())
	secret := filepath.Join(dir, "outside", "secret.txt")
	cwd = filepath.Join(dir, "cwd")
	for _, d := range []string{filepath.Dir(secret), cwd} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(secret, []byte("The marker is "+marker+".\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt = "What marker does this file contain? Reply with only the marker, or " +
		"UNKNOWN if you cannot see it. @" + secret
	return prompt, marker, cwd
}

func assistantText(t *testing.T, m *AssistantMessage) string {
	t.Helper()
	var b strings.Builder
	for _, c := range m.Content {
		switch blk := c.(type) {
		case *ToolUseBlock:
			t.Fatalf("unexpected tool call %s", blk.Name)
		case *TextBlock:
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

func replyViaQuery(t *testing.T, prompt string, opts ...Option) string {
	t.Helper()
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var reply string
	for msg, err := range Query(ctx, prompt, opts...) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if m, ok := msg.(*AssistantMessage); ok {
			reply += assistantText(t, m)
		}
	}
	return reply
}

func TestE2EAtPathExpandedByDefault(t *testing.T) {
	e2eSkip(t)
	prompt, marker, cwd := plantMarker(t)
	if reply := replyViaQuery(t, prompt, verbatimOptions(cwd, false)...); !strings.Contains(reply, marker) {
		t.Errorf("control: the @-mention was not expanded, so the verbatim tests prove nothing; reply=%q", reply)
	}
}

func TestE2EVerbatimQueryNotExpanded(t *testing.T) {
	e2eSkip(t)
	prompt, marker, cwd := plantMarker(t)
	if reply := replyViaQuery(t, prompt, verbatimOptions(cwd, true)...); strings.Contains(reply, marker) {
		t.Errorf("the @-mentioned file was read despite WithVerbatimPrompts; reply=%q", reply)
	}
}

func TestE2EVerbatimClientNotExpanded(t *testing.T) {
	e2eSkip(t)
	prompt, marker, cwd := plantMarker(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	client := NewClient(verbatimOptions(cwd, true)...)
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Query(ctx, prompt); err != nil {
		t.Fatal(err)
	}
	var reply string
	for msg, err := range client.ReceiveResponse(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		switch m := msg.(type) {
		case *AssistantMessage:
			reply += assistantText(t, m)
		case *ResultMessage:
			if m.IsError {
				t.Fatalf("result error: %+v", m)
			}
		}
	}
	if strings.Contains(reply, marker) {
		t.Errorf("the @-mentioned file was read despite WithVerbatimPrompts; reply=%q", reply)
	}
}

// --- SessionStore resume (mirrors test_session_store_resume_settings.py) -----

// callerConfigDir returns a fresh CLAUDE_CONFIG_DIR carrying the developer's
// login (refresh token removed, so the throwaway dir can never consume the
// real login's single-use refresh token). CI authenticates through env vars,
// which the subprocess inherits.
func callerConfigDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	home, _ := os.UserHomeDir()
	creds, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		creds = keychainCredentials()
	}
	writeRedactedCredentials(creds, filepath.Join(d, ".credentials.json"))
	return d
}

func runInitAndResult(t *testing.T, prompt string, opts ...Option) (map[string]any, *ResultMessage) {
	t.Helper()
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var (
		init   map[string]any
		result *ResultMessage
	)
	for msg, err := range Query(ctx, prompt, opts...) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		switch m := msg.(type) {
		case *SystemMessage:
			if m.Subtype == "init" {
				if err := json.Unmarshal(m.Data, &init); err != nil {
					t.Fatal(err)
				}
			}
		case *ResultMessage:
			result = m
		}
	}
	if init == nil || result == nil {
		t.Fatalf("init=%v result=%v", init != nil, result)
	}
	if result.IsError {
		t.Fatalf("result error: %+v", result)
	}
	return init, result
}

func TestE2ESessionStoreResumeAppliesUserSettings(t *testing.T) {
	e2eSkip(t)
	store := NewInMemorySessionStore()
	cwd := t.TempDir()
	cfg := callerConfigDir(t)
	opts := func(extra ...Option) []Option {
		return append([]Option{
			WithCwd(cwd),
			WithSessionStore(store, FlushBatched),
			WithEnv(map[string]string{"CLAUDE_CONFIG_DIR": cfg}),
			WithMaxTurns(1),
		}, extra...)
	}

	// Seed a session into the store with no user settings present.
	firstInit, first := runInitAndResult(t, "Reply with exactly: one", opts()...)
	if firstInit["output_style"] != "default" {
		t.Fatalf("first output_style = %v", firstInit["output_style"])
	}

	// Add user settings and drop the on-disk transcript, so the resume can
	// only succeed through the store, under a different temporary config dir
	// seeded from this one.
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(`{"outputStyle":"Explanatory"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cfg, "projects")); err != nil {
		t.Fatal(err)
	}

	resumedInit, resumed := runInitAndResult(t, "Reply with exactly: two", opts(WithResume(first.SessionID))...)
	if resumed.SessionID != first.SessionID {
		t.Errorf("did not resume: session %s, want %s", resumed.SessionID, first.SessionID)
	}
	if resumedInit["output_style"] != "Explanatory" {
		t.Errorf("user settings.json not applied on store resume: output_style = %v", resumedInit["output_style"])
	}
}

// --- Conversation reset and origin (mirror test_conversation_reset.py and
// test_message_origin.py) ------------------------------------------------------

// receiveResult drains one turn and returns its result, collecting the other
// messages into seen.
func receiveResult(t *testing.T, ctx context.Context, c *Client, seen *[]Message) *ResultMessage {
	t.Helper()
	for msg, err := range c.ReceiveResponse(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		if seen != nil {
			*seen = append(*seen, msg)
		}
		if r, ok := msg.(*ResultMessage); ok {
			return r
		}
	}
	t.Fatal("turn ended without a result")
	return nil
}

func TestE2EClearEmitsConversationReset(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	c := NewClient(WithMaxTurns(1))
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Query(ctx, "Reply with exactly: one"); err != nil {
		t.Fatal(err)
	}
	first := receiveResult(t, ctx, c, nil)

	if err := c.Query(ctx, "/clear"); err != nil {
		t.Fatal(err)
	}
	var seen []Message
	cleared := receiveResult(t, ctx, c, &seen)
	var reset *ConversationResetMessage
	for _, m := range seen {
		if r, ok := m.(*ConversationResetMessage); ok {
			reset = r
		}
	}
	if reset == nil {
		t.Fatal("no ConversationResetMessage after /clear")
	}
	if reset.SessionID != first.SessionID || reset.NewConversationID == "" || reset.UUID == "" {
		t.Errorf("reset = %+v, first session %s", reset, first.SessionID)
	}
	if cleared.SessionID == first.SessionID {
		t.Errorf("result after /clear kept the old session id %s", cleared.SessionID)
	}
}

func TestE2EResultOriginRoundTrip(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	c := NewClient(WithMaxTurns(1))
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	// The public API sends unstamped prompts; write a host-stamped frame
	// directly, as upstream's test streams one.
	stamped := []byte(`{"type":"user","message":{"role":"user","content":"Reply with exactly: one"},` +
		`"parent_tool_use_id":null,"session_id":"default","origin":{"kind":"human"}}`)
	if err := c.sess.t.Write(ctx, stamped); err != nil {
		t.Fatal(err)
	}
	r1 := receiveResult(t, ctx, c, nil)
	if err := c.Query(ctx, "Reply with exactly: two"); err != nil {
		t.Fatal(err)
	}
	r2 := receiveResult(t, ctx, c, nil)

	if r1.Origin == nil || r1.Origin.Kind != OriginHuman {
		t.Errorf("stamped turn origin = %+v (is_error=%v)", r1.Origin, r1.IsError)
	}
	if r2.Origin != nil {
		t.Errorf("unstamped turn origin = %+v", r2.Origin)
	}
}

// --- Truncating resume (mirrors test_truncating_resume.py) --------------------

const (
	truncTurn1 = "Reply with exactly: one"
	truncTurn2 = "Reply with exactly: two"
	truncTurn3 = "Reply with exactly: three"
)

// isolatedOpts loads no settings files, matching upstream's CI, where no user
// hooks add turns past WithMaxTurns(1).
func isolatedOpts(cwd string, extra ...Option) []Option {
	return append([]Option{WithCwd(cwd), WithMaxTurns(1), WithSettingSources()}, extra...)
}

// userPrompts returns the string prompts of a session's user entries, in order.
func userPrompts(t *testing.T, sessionID, cwd string) []string {
	t.Helper()
	msgs, err := GetSessionMessages(sessionID, cwd, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range msgs {
		var body struct {
			Content json.RawMessage `json:"content"`
		}
		var s string
		if m.Type == "user" && json.Unmarshal(m.Message, &body) == nil && json.Unmarshal(body.Content, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// twoTurnSession returns the session id, the last entry of turn 1, and the
// prompt UUID of turn 2.
func twoTurnSession(t *testing.T, cwd string) (sessionID, keepAt, turn2Prompt string) {
	t.Helper()
	ctx, cancel := e2eCtx(t)
	defer cancel()
	c := NewClient(isolatedOpts(cwd)...)
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	var seen []Message
	if err := c.Query(ctx, truncTurn1); err != nil {
		t.Fatal(err)
	}
	r1 := receiveResult(t, ctx, c, &seen)
	if err := c.Query(ctx, truncTurn2); err != nil {
		t.Fatal(err)
	}
	r2 := receiveResult(t, ctx, c, nil)
	_ = c.Close()
	if r1.IsError || r2.IsError {
		t.Fatalf("setup turns failed: %+v %+v", r1, r2)
	}
	for _, m := range seen {
		if a, ok := m.(*AssistantMessage); ok && a.UUID != "" {
			keepAt = a.UUID
		}
	}
	sessionID = r2.SessionID

	msgs, err := GetSessionMessages(sessionID, cwd, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		if m.UUID != keepAt {
			continue
		}
		if i+1 >= len(msgs) || msgs[i+1].Type != "user" {
			t.Fatalf("turn 2 prompt does not follow turn 1's last entry: %+v", msgs)
		}
		return sessionID, keepAt, msgs[i+1].UUID
	}
	t.Fatalf("turn 1 assistant %s not in transcript", keepAt)
	return
}

func TestE2ETruncatingResumeMatchingDropsTurn(t *testing.T) {
	e2eSkip(t)
	cwd := t.TempDir()
	sid, keepAt, turn2 := twoTurnSession(t, cwd)

	r := runToResult(t, truncTurn3, isolatedOpts(cwd,
		WithResume(sid), WithForkSession(), WithResumeSessionAt(keepAt), WithResumeDropsTurn(turn2))...)
	if r.IsError || r.SessionID == sid {
		t.Fatalf("fork result = %+v", r)
	}
	if got := userPrompts(t, r.SessionID, cwd); strings.Join(got, "|") != truncTurn1+"|"+truncTurn3 {
		t.Errorf("forked prompts = %q, want turn 2 dropped", got)
	}
	if got := userPrompts(t, sid, cwd); strings.Join(got, "|") != truncTurn1+"|"+truncTurn2 {
		t.Errorf("source prompts = %q, want it untouched", got)
	}
}

func TestE2ETruncatingResumeWrongDropsTurnRefused(t *testing.T) {
	e2eSkip(t)
	cwd := t.TempDir()
	sid, keepAt, _ := twoTurnSession(t, cwd)

	ctx, cancel := e2eCtx(t)
	defer cancel()
	var got error
	for _, err := range Query(ctx, truncTurn3, isolatedOpts(cwd,
		WithResume(sid), WithForkSession(), WithResumeSessionAt(keepAt),
		WithResumeDropsTurn("00000000-0000-4000-8000-000000000000"))...) {
		if err != nil {
			got = err
		}
	}
	var pe *ProcessError
	if !errors.As(got, &pe) || !strings.Contains(fmt.Sprint(got), "Resume rejected by --resume-drops-turn:") {
		t.Fatalf("err = %v, want a ProcessError carrying the refusal", got)
	}
	if prompts := userPrompts(t, sid, cwd); strings.Join(prompts, "|") != truncTurn1+"|"+truncTurn2 {
		t.Errorf("source prompts = %q, want nothing appended", prompts)
	}
}

// --- Error results (mirrors test_error_results.py) ---------------------------

func TestE2EAPIErrorYieldsResultError(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	var (
		result *ResultMessage
		got    error
	)
	for msg, err := range Query(ctx, "Reply with: pong", isolatedOpts(t.TempDir(),
		WithModel("claude-nonexistent-model-for-sdk-e2e"))...) {
		if err != nil {
			got = err
			break
		}
		if r, ok := msg.(*ResultMessage); ok {
			result = r
		}
	}
	if result == nil || !result.IsError {
		t.Fatalf("the error result was not delivered first: %+v", result)
	}
	var re *ResultError
	if !errors.As(got, &re) {
		t.Fatalf("err = %T %v, want a *ResultError", got, got)
	}
	var pe *ProcessError
	if !errors.As(got, &pe) {
		t.Error("ResultError does not unwrap to ProcessError")
	}
	if strings.Contains(re.Error(), "error result: success") || !strings.Contains(re.Error(), strings.TrimSpace(result.Result)) {
		t.Errorf("text = %q, result = %q", re.Error(), result.Result)
	}
	var raw struct {
		UUID string `json:"uuid"`
	}
	_ = json.Unmarshal(re.Data, &raw)
	if re.Subtype != result.Subtype || re.Result != result.Result || re.SessionID != result.SessionID ||
		re.TerminalReason != result.TerminalReason || raw.UUID != result.UUID {
		t.Errorf("payload %+v does not match result %+v", re, result)
	}
	if re.Subtype != "success" || re.TerminalReason != "api_error" {
		t.Errorf("subtype=%q terminal_reason=%q, want the mid-turn API failure shape", re.Subtype, re.TerminalReason)
	}
}

// --- Forwarding subagent text (mirrors test_forward_subagent_text.py) ---------

func forwardSubagentRun(t *testing.T, forward bool) (agentIDs map[string]bool, attributed []*AssistantMessage) {
	t.Helper()
	opts := isolatedOpts(t.TempDir(),
		WithAgents(map[string]AgentDefinition{"greeter": {
			Description: "Replies with a short greeting. Use for greeting tasks.",
			Prompt:      "Reply with one short friendly sentence. Do not use any tools.",
			Model:       "haiku",
		}}),
		WithAllowedTools("Agent", "Task"),
		WithMaxTurns(4),
	)
	if forward {
		opts = append(opts, WithForwardSubagentText())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	agentIDs = map[string]bool{}
	var result *ResultMessage
	for msg, err := range Query(ctx, "Use the Agent tool exactly once with subagent_type 'greeter', prompt "+
		"'say hi', and run_in_background set to false. Then reply with the single word DONE.", opts...) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		switch m := msg.(type) {
		case *AssistantMessage:
			if m.ParentToolUseID != "" {
				attributed = append(attributed, m)
				continue
			}
			for _, b := range m.Content {
				if tu, ok := b.(*ToolUseBlock); ok && (tu.Name == "Agent" || tu.Name == "Task") {
					agentIDs[tu.ID] = true
				}
			}
		case *ResultMessage:
			result = m
		}
	}
	if result == nil || result.IsError || len(agentIDs) == 0 {
		t.Fatalf("run failed or never called the Agent tool: %+v, agent calls %v", result, agentIDs)
	}
	return agentIDs, attributed
}

func hasText(m *AssistantMessage) bool {
	for _, b := range m.Content {
		if _, ok := b.(*TextBlock); ok {
			return true
		}
	}
	return false
}

func TestE2EForwardSubagentTextDeliversAttributedText(t *testing.T) {
	e2eSkip(t)
	ids, attributed := forwardSubagentRun(t, true)
	n := 0
	for _, m := range attributed {
		if hasText(m) {
			n++
			if !ids[m.ParentToolUseID] {
				t.Errorf("text attributed to %q, not an Agent call %v", m.ParentToolUseID, ids)
			}
		}
	}
	if n == 0 {
		t.Error("no subagent text message with ParentToolUseID set")
	}
}

func TestE2ESubagentTextNotForwardedByDefault(t *testing.T) {
	e2eSkip(t)
	_, attributed := forwardSubagentRun(t, false)
	for _, m := range attributed {
		if hasText(m) {
			t.Errorf("subagent text forwarded without the option: %+v", m.Content)
		}
	}
}

// --- System prompt snapshot (behavior documented on upstream's
// SystemPromptPreset.snapshot; upstream has no e2e test) ----------------------

func TestE2ESystemPromptSnapshot(t *testing.T) {
	e2eSkip(t)
	prompt := func(word string) Option {
		return WithSystemPrompt("Your secret word is " + word + ". When asked for your word, reply with only that word.")
	}
	ask := "What is your word?"
	for _, tc := range []struct {
		name string
		keep []Option
		want string
	}{
		{"default keeps the recorded prompt", nil, "ALPHA"},
		{"rebuild uses the new prompt", []Option{WithSystemPromptSnapshot(false)}, "BETA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			first := runToResult(t, ask, append(isolatedOpts(cwd, prompt("ALPHA")), tc.keep...)...)
			if first.IsError || !strings.Contains(first.Result, "ALPHA") {
				t.Fatalf("first turn = %+v", first)
			}
			resumed := runToResult(t, ask, append(isolatedOpts(cwd, prompt("BETA"), WithResume(first.SessionID)), tc.keep...)...)
			if !strings.Contains(resumed.Result, tc.want) {
				t.Errorf("resumed answer = %q, want %s", resumed.Result, tc.want)
			}
		})
	}
}

// --- Subagent session reads (mirrors test_subagent_session_reads.py) ----------

func TestE2ESubagentMessagesCarryParentToolUseID(t *testing.T) {
	e2eSkip(t)
	store := NewInMemorySessionStore()
	cwd := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	agentCalls := map[string]bool{}
	var sessionID string
	for msg, err := range Query(ctx, "Use the Agent tool exactly once with subagent_type 'greeter', prompt "+
		"'say hi', and run_in_background set to false. Then reply with the single word DONE.",
		isolatedOpts(cwd,
			WithSessionStore(store, FlushBatched),
			WithAgents(map[string]AgentDefinition{"greeter": {
				Description: "Replies with a short greeting. Use for greeting tasks.",
				Prompt:      "Reply with one short friendly sentence. Do not use any tools.",
				Model:       "haiku",
			}}),
			WithAllowedTools("Agent", "Task"),
			WithMaxTurns(4),
		)...) {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		switch m := msg.(type) {
		case *AssistantMessage:
			if m.ParentToolUseID != "" {
				continue
			}
			for _, b := range m.Content {
				if tu, ok := b.(*ToolUseBlock); ok && (tu.Name == "Agent" || tu.Name == "Task") {
					agentCalls[tu.ID] = true
				}
			}
		case *ResultMessage:
			if m.IsError {
				t.Fatalf("run failed: %+v", m)
			}
			sessionID = m.SessionID
		}
	}
	if sessionID == "" || len(agentCalls) == 0 {
		t.Fatalf("session %q, agent calls %v", sessionID, agentCalls)
	}

	// attributed maps agent id to the parent ids of its messages, for
	// transcripts attributed only to Agent calls seen in the stream.
	attributed := func(byAgent map[string][]SessionMessage) map[string]string {
		out := map[string]string{}
		for id, msgs := range byAgent {
			if len(msgs) == 0 {
				continue
			}
			parent := msgs[0].ParentToolUseID
			ok := agentCalls[parent]
			for _, m := range msgs {
				if m.ParentToolUseID != parent || m.ParentAgentID != "" {
					ok = false
				}
			}
			if ok {
				out[id] = parent
			}
		}
		return out
	}

	local := map[string][]SessionMessage{}
	ids, err := ListSubagents(sessionID, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		msgs, err := GetSubagentMessages(sessionID, id, "", 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		local[id] = msgs
	}
	localAttr := attributed(local)
	if len(localAttr) == 0 {
		t.Fatalf("no local subagent transcript attributed to %v; subagents %v", agentCalls, ids)
	}

	mirrored := map[string][]SessionMessage{}
	sids, err := ListSubagentsFromStore(ctx, store, sessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range sids {
		msgs, err := GetSubagentMessagesFromStore(ctx, store, sessionID, id, cwd, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		mirrored[id] = msgs
	}
	mirroredAttr := attributed(mirrored)
	if len(mirroredAttr) == 0 {
		t.Fatalf("no mirrored subagent transcript attributed to %v; subagents %v", agentCalls, sids)
	}
	both := 0
	for id, parent := range localAttr {
		if mp, ok := mirroredAttr[id]; ok {
			both++
			if mp != parent {
				t.Errorf("agent %s: local parent %s, mirrored parent %s", id, parent, mp)
			}
		}
	}
	if both == 0 {
		t.Errorf("no subagent attributed on both paths: local %v, mirrored %v", localAttr, mirroredAttr)
	}
}

// --- SDK MCP annotations reach the CLI -----------------------------------------

// The CLI reports each connected tool's annotations in mcp_status. Tools must
// advertise MCP's hint names (readOnlyHint, ...) for the CLI to pick them up.
func TestE2ESdkMcpAnnotationsReachCLI(t *testing.T) {
	e2eSkip(t)
	ctx, cancel := e2eCtx(t)
	defer cancel()
	noop := func(ctx context.Context, args json.RawMessage) (ToolResult, error) { return TextResult("ok"), nil }
	srv := NewSdkMcpServer("annot").
		AddTool(Tool{Name: "reader", Description: "Reads", Handler: noop,
			Annotations: &ToolAnnotations{ReadOnlyHint: Bool(true), DestructiveHint: Bool(false), OpenWorldHint: Bool(false)}}).
		AddTool(Tool{Name: "writer", Description: "Writes", Handler: noop,
			Annotations: &ToolAnnotations{ReadOnlyHint: Bool(false), DestructiveHint: Bool(true)}})
	c := NewClient(isolatedOpts(t.TempDir(), WithSDKMCPServer("annot", srv))...)
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	var server *McpServerStatusInfo
	for i := 0; i < 50 && server == nil; i++ {
		st, err := c.McpStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for j := range st.McpServers {
			if st.McpServers[j].Name == "annot" && st.McpServers[j].Status == "connected" && len(st.McpServers[j].Tools) > 0 {
				server = &st.McpServers[j]
			}
		}
		if server == nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if server == nil {
		t.Fatal("annot server never reported connected with tools")
	}
	byName := map[string]*McpToolAnnotations{}
	for _, tl := range server.Tools {
		byName[tl.Name] = tl.Annotations
	}
	if a := byName["reader"]; a == nil || !a.ReadOnly || a.Destructive || a.OpenWorld {
		t.Errorf("reader annotations = %+v", a)
	}
	if a := byName["writer"]; a == nil || a.ReadOnly || !a.Destructive {
		t.Errorf("writer annotations = %+v", a)
	}
}
