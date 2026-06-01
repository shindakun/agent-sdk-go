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
	"os/exec"
	"strings"
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
	defer client.Close()

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
