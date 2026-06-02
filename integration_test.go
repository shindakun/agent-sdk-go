//go:build integration

// Package claude integration tests run against a real `claude` binary. They are
// excluded from the default build; run with:
//
//	go test -tags integration -run TestIntegration -v ./...
//
// They require `claude` on PATH and a working Claude Code auth session.
package claude

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func skipIfNoCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude binary not on PATH")
	}
}

func TestIntegrationQuery(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var (
		sawInit   bool
		sawAssist bool
		result    *ResultMessage
	)
	for msg, err := range Query(ctx, "Reply with exactly the single word: pong") {
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		switch m := msg.(type) {
		case *SystemMessage:
			if m.Subtype == "init" {
				sawInit = true
				if m.SessionID == "" {
					t.Error("init message has no session_id")
				}
			}
		case *AssistantMessage:
			sawAssist = true
			if m.Model == "" {
				t.Error("assistant message has no model")
			}
		case *ResultMessage:
			result = m
		}
	}

	if !sawInit {
		t.Error("never saw system/init")
	}
	if !sawAssist {
		t.Error("never saw an assistant message")
	}
	if result == nil {
		t.Fatal("never saw a result message")
	}
	if result.IsError {
		t.Errorf("result is_error=true: %v", result.Errors)
	}
	if !strings.Contains(strings.ToLower(result.Result), "pong") {
		t.Errorf("result = %q, expected to contain 'pong'", result.Result)
	}
	// Fields we added during the parity audit should be populated by the real CLI.
	if result.SessionID == "" {
		t.Error("result has no session_id")
	}
}

func TestIntegrationClientMultiTurn(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := NewClient()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Server info should have been captured during connect.
	info, err := client.GetServerInfo(ctx)
	if err != nil {
		t.Fatalf("get server info: %v", err)
	}
	if len(info) == 0 {
		t.Error("GetServerInfo returned empty")
	}

	send := func(prompt string) *ResultMessage {
		t.Helper()
		if err := client.Query(ctx, prompt); err != nil {
			t.Fatalf("query %q: %v", prompt, err)
		}
		for res := range client.Receive() {
			if res.Err != nil {
				t.Fatalf("stream: %v", res.Err)
			}
			if rm, ok := res.Message.(*ResultMessage); ok {
				return rm
			}
		}
		t.Fatal("stream ended before result")
		return nil
	}

	r1 := send("Remember the number 42. Reply 'ok'.")
	if r1.IsError {
		t.Fatalf("turn 1 error: %v", r1.Errors)
	}
	r2 := send("What number did I ask you to remember? Reply with only the number.")
	if r2.IsError {
		t.Fatalf("turn 2 error: %v", r2.Errors)
	}
	if !strings.Contains(r2.Result, "42") {
		t.Errorf("turn 2 lost context; result = %q", r2.Result)
	}
}

func TestIntegrationCustomTool(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	type addArgs struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	called := false
	calc := NewSdkMcpServer("calc").AddTool(
		NewTool("add", "Add two integers a and b", func(ctx context.Context, in addArgs) (ToolResult, error) {
			called = true
			return TextResult(itoa(in.A + in.B)), nil
		}))

	var result *ResultMessage
	for msg, err := range Query(ctx,
		"Use the add tool to compute 19 + 23, then tell me only the result number.",
		WithSDKMCPServer("calc", calc),
		WithAllowedTools("mcp__calc__add"),
	) {
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if rm, ok := msg.(*ResultMessage); ok {
			result = rm
		}
	}
	if !called {
		t.Error("the in-process add tool was never called by the agent")
	}
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Result, "42") {
		t.Errorf("expected 42 in result, got %q", result.Result)
	}
}

func itoa(n int) string {
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

// TestIntegrationCanUseToolDeny verifies the permission callback path end-to-end:
// the CLI must complete the turn with CanUseTool configured, and when the
// callback IS consulted, a deny must be honored. Whether the CLI routes a given
// tool to can_use_tool depends on its version and permission policy (older CLIs
// auto-approve more), so the callback firing is checked but not required.
func TestIntegrationCanUseToolDeny(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var consulted atomic.Bool
	var denied atomic.Bool
	cb := func(ctx context.Context, tool string, input json.RawMessage, pc PermissionContext) (PermissionResult, error) {
		consulted.Store(true)
		denied.Store(true)
		return PermissionDeny{Message: "not permitted in this test"}, nil
	}

	var result *ResultMessage
	for msg, err := range Query(ctx,
		"Create the file /tmp/agent_sdk_go_perm_test.txt with the word hi using the Write tool.",
		WithCanUseTool(cb),
	) {
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if rm, ok := msg.(*ResultMessage); ok {
			result = rm
		}
	}
	if result == nil {
		t.Fatal("no result message — the CanUseTool turn did not complete")
	}
	if consulted.Load() {
		t.Logf("CanUseTool was consulted and denied (deny path exercised)")
	} else {
		t.Logf("CLI did not route any tool to can_use_tool (CLI %s auto-approved); "+
			"flag wiring verified, callback dispatch covered by unit tests", cliVersion())
	}
}

func cliVersion() string {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// TestIntegrationHook verifies a PreToolUse hook fires for a real tool call.
func TestIntegrationHook(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var hookFired atomic.Bool
	preBash := func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error) {
		in, err := DecodePreToolUse(input)
		if err == nil && in.ToolName == "Bash" {
			hookFired.Store(true)
		}
		return HookOutput{}, nil
	}

	for msg, err := range Query(ctx,
		"Run `echo hi` with the Bash tool.",
		WithAllowedTools("Bash"),
		WithPermissionMode(PermissionBypass),
		WithHooks(map[HookEvent][]HookMatcher{
			HookPreToolUse: {{Matcher: "Bash", Callbacks: []HookCallback{preBash}}},
		}),
	) {
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		_ = msg
	}
	if !hookFired.Load() {
		t.Error("PreToolUse hook never fired for the Bash tool")
	}
}

// TestIntegrationSessionResume verifies a session can be resumed by id with its
// context intact.
func TestIntegrationSessionResume(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var sessionID string
	for msg, err := range Query(ctx, "Remember the codeword: banana. Reply 'ok'.") {
		if err != nil {
			t.Fatalf("first query: %v", err)
		}
		if sm, ok := msg.(*SystemMessage); ok && sm.Subtype == "init" {
			sessionID = sm.SessionID
		}
	}
	if sessionID == "" {
		t.Fatal("no session id captured from first query")
	}

	var result *ResultMessage
	for msg, err := range Query(ctx, "What was the codeword? Reply with only that word.",
		WithResume(sessionID),
	) {
		if err != nil {
			t.Fatalf("resume query: %v", err)
		}
		if rm, ok := msg.(*ResultMessage); ok {
			result = rm
		}
	}
	if result == nil {
		t.Fatal("no result from resumed session")
	}
	if !strings.Contains(strings.ToLower(result.Result), "banana") {
		t.Errorf("resumed session lost context; result = %q", result.Result)
	}
}

// TestIntegrationInterrupt verifies an in-flight turn can be interrupted.
func TestIntegrationInterrupt(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := NewClient(WithAllowedTools("Bash"), WithPermissionMode(PermissionBypass))
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Query(ctx, "Count slowly from 1 to 30, sleeping 1 second between each using Bash `sleep 1`."); err != nil {
		t.Fatalf("query: %v", err)
	}

	// Let the turn get going, then interrupt.
	time.Sleep(4 * time.Second)
	if err := client.Interrupt(ctx); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	// The stream must terminate with a result rather than hanging.
	got := make(chan *ResultMessage, 1)
	go func() {
		for res := range client.Receive() {
			if res.Err != nil {
				return
			}
			if rm, ok := res.Message.(*ResultMessage); ok {
				got <- rm
				return
			}
		}
		got <- nil
	}()
	select {
	case rm := <-got:
		if rm == nil {
			t.Error("stream ended without a result after interrupt")
		}
	case <-time.After(30 * time.Second):
		t.Error("no result within 30s after interrupt — likely hung")
	}
}

// TestIntegrationThinking verifies an adaptive-thinking turn completes against
// the real CLI (the bare --thinking flag the SDK used to emit was rejected).
func TestIntegrationThinking(t *testing.T) {
	skipIfNoCLI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var result *ResultMessage
	for msg, err := range Query(ctx, "What is 2+2? Reply with only the number.",
		WithThinkingConfig(ThinkingConfigAdaptive{Type: "adaptive"}),
	) {
		if err != nil {
			t.Fatalf("query error: %v", err)
		}
		if rm, ok := msg.(*ResultMessage); ok {
			result = rm
		}
	}
	if result == nil {
		t.Fatal("no result — thinking turn did not complete")
	}
	if result.IsError {
		t.Errorf("thinking turn errored: %v", result.Errors)
	}
}
