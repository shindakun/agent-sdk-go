package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// userFrames returns the decoded "user" frames among written lines.
func userFrames(t *testing.T, lines [][]byte) []map[string]json.RawMessage {
	t.Helper()
	var out []map[string]json.RawMessage
	for _, l := range lines {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(l, &m); err != nil {
			continue
		}
		if string(m["type"]) == `"user"` {
			out = append(out, m)
		}
	}
	return out
}

func TestVerbatimPromptsQuery(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want string // "" means the key must be absent
	}{
		{"off", nil, ""},
		{"on", []Option{WithVerbatimPrompts()}, "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newScriptedTransport(
				[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`),
			)
			defer installScriptedTransport(st)()
			for _, err := range Query(context.Background(), "read @/etc/hosts", tc.opts...) {
				if err != nil {
					t.Fatal(err)
				}
			}
			frames := userFrames(t, st.writtenLines())
			if len(frames) != 1 {
				t.Fatalf("got %d user frames, want 1", len(frames))
			}
			got, ok := frames[0]["client_composed"]
			if tc.want == "" && ok {
				t.Errorf("client_composed present with the option off: %s", got)
			}
			if tc.want != "" && string(got) != tc.want {
				t.Errorf("client_composed = %q, want %s", got, tc.want)
			}
		})
	}
}

func TestVerbatimPromptsClientEveryTurn(t *testing.T) {
	tr := newInteractiveTransport()
	defer installInteractive(tr)()

	ctx := context.Background()
	client := NewClient(WithVerbatimPrompts())
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Query(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if err := client.QuerySession(ctx, "/two", "s2"); err != nil {
		t.Fatal(err)
	}
	frames := userFrames(t, tr.writtenLines())
	if len(frames) != 2 {
		t.Fatalf("got %d user frames, want 2", len(frames))
	}
	for i, f := range frames {
		if string(f["client_composed"]) != "true" {
			t.Errorf("frame %d: client_composed = %q, want true", i, f["client_composed"])
		}
	}
}

func stubVersionProbe(t *testing.T, version string, err error) {
	t.Helper()
	prev := cliVersionProbe
	cliVersionProbe = func(ctx context.Context, cliPath string) (string, string, error) {
		return version, "/opt/claude", err
	}
	t.Cleanup(func() { cliVersionProbe = prev })
}

func TestCLIVersionWarnings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  string
		err      error
		verbatim bool
		want     []string // substrings, one per expected warning
	}{
		{"current, verbatim on", "2.1.280", nil, true, nil},
		{"exact verbatim minimum", "2.1.248", nil, true, nil},
		{"older than verbatim minimum", "2.1.247", nil, true, []string{"verbatim prompts are enabled"}},
		{"older, verbatim off", "2.1.247", nil, false, nil},
		{"below SDK minimum", "1.9.9", nil, false, []string{"Minimum required version is 2.0.0"}},
		{"below both", "1.9.9", nil, true, []string{"Minimum required version", "Claude Code 2.1.248 or later"}},
		{"unparseable", "dev-build", nil, true, nil},
		{"probe error", "", errors.New("no cli"), true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubVersionProbe(t, tc.version, tc.err)
			o := newOptions()
			o.verbatimPrompts = tc.verbatim
			got := o.cliVersionWarnings(context.Background())
			if len(got) != len(tc.want) {
				t.Fatalf("got %d warnings %q, want %d", len(got), got, len(tc.want))
			}
			for i, sub := range tc.want {
				if !strings.Contains(got[i], sub) {
					t.Errorf("warning %d = %q, want it to contain %q", i, got[i], sub)
				}
			}
		})
	}
}

func TestVersionWarningWrittenOnConnect(t *testing.T) {
	stubVersionProbe(t, "2.1.200", nil)
	for _, skip := range []bool{false, true} {
		if skip {
			t.Setenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK", "1")
		}
		st := newScriptedTransport(
			[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`),
		)
		restore := installScriptedTransport(st)
		var stderr bytes.Buffer
		for _, err := range Query(context.Background(), "hi", WithVerbatimPrompts(), WithStderr(&stderr)) {
			if err != nil {
				t.Fatal(err)
			}
		}
		restore()
		warned := strings.Contains(stderr.String(), "verbatim prompts are enabled")
		if warned == skip {
			t.Errorf("skip=%v: warned=%v; stderr=%q", skip, warned, stderr.String())
		}
	}
}
