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
	"os/exec"
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
	defer client.Close()
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
	defer client.Close()
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
	for _, err := range Query(ctx, "Use the add tool to compute 2 + 2.",
		WithSDKMCPServer("calc", srv),
		WithCanUseTool(deny),
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
	r := runToResult(t,
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
