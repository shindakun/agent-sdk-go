package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// TestMain lets the test binary double as a fake `claude` CLI when the
// FAKE_CLAUDE env var is set. The fake reads stream-json from stdin, answers the
// initialize control_request, and on the first user message emits a scripted
// assistant + result, then exits.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CLAUDE") == "1" {
		runFakeCLI()
		return
	}
	os.Exit(m.Run())
}

func runFakeCLI() {
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var probe struct {
				Type      string `json:"type"`
				RequestID string `json:"request_id"`
				Request   struct {
					Subtype string `json:"subtype"`
				} `json:"request"`
			}
			_ = json.Unmarshal(line, &probe)

			switch {
			case probe.Type == "control_request" && probe.Request.Subtype == "initialize":
				_, _ = fmt.Fprintf(out, `{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`+"\n", probe.RequestID)
			case probe.Type == "user":
				_, _ = fmt.Fprintln(out, `{"type":"system","subtype":"init","session_id":"s1","tools":["Read"]}`)
				_, _ = fmt.Fprintln(out, `{"type":"assistant","message":{"model":"m","content":[{"type":"text","text":"hi"}]}}`)
				_, _ = fmt.Fprintln(out, `{"type":"result","subtype":"success","is_error":false,"result":"hi"}`)
				os.Exit(0)
			}
		}
		if err != nil {
			os.Exit(0)
		}
	}
}

func TestSubprocessTransportRoundTrip(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}

	tr := New(Config{
		CLIPath: self,
		Env:     map[string]string{"FAKE_CLAUDE": "1"},
	})
	ctx := context.Background()
	if err := tr.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })

	// Send the initialize handshake, then a user prompt — same order the real
	// session driver uses.
	initReq := `{"type":"control_request","request_id":"req_1_aabbccdd","request":{"subtype":"initialize"}}`
	if err := tr.Write(ctx, []byte(initReq)); err != nil {
		t.Fatalf("write init: %v", err)
	}

	// Wait for the init control_response before sending the prompt.
	var sawInitResp bool
	for line := range tr.Read() {
		if line.Err != nil {
			t.Fatalf("unexpected read err before prompt: %v", line.Err)
		}
		if strings.Contains(string(line.Data), "control_response") {
			sawInitResp = true
			break
		}
	}
	if !sawInitResp {
		t.Fatal("never received init control_response")
	}

	if err := tr.Write(ctx, []byte(`{"type":"user","message":{"role":"user","content":"hi"},"parent_tool_use_id":null,"session_id":"default"}`)); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	var types []string
	for line := range tr.Read() {
		if line.Err != nil {
			if line.Err == io.EOF {
				break
			}
			t.Fatalf("read err: %v", line.Err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(line.Data, &probe)
		types = append(types, probe.Type)
	}

	want := []string{"system", "assistant", "result"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Errorf("got message types %v, want %v", types, want)
	}
}

func TestDiscoverExplicitMissing(t *testing.T) {
	_, err := discoverCLI("/nonexistent/claude/binary")
	var nf *CLINotFoundError
	if err == nil || !asCLINotFound(err, &nf) {
		t.Fatalf("want CLINotFoundError, got %v", err)
	}
}

func asCLINotFound(err error, target **CLINotFoundError) bool {
	e, ok := err.(*CLINotFoundError)
	if ok {
		*target = e
	}
	return ok
}
