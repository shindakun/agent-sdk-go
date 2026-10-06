package claude

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Frames for the run-end tests. state() builds a session_state_changed frame,
// marked sdk_host_only (as the CLI sends it when the SDK asks) or not (as it
// sends it when the caller set CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS).
var (
	frameInit     = []byte(`{"type":"system","subtype":"init","session_id":"s1","tools":["Agent"]}`)
	frameStarted  = []byte(`{"type":"system","subtype":"task_started","task_id":"t1","task_type":"local_agent","session_id":"s1"}`)
	frameSettled  = []byte(`{"type":"system","subtype":"task_notification","task_id":"t1","status":"completed","session_id":"s1"}`)
	frameResult   = []byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`)
	frameAssist   = []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"model":"m"},"parent_tool_use_id":null,"session_id":"s1"}`)
	frameSubAgent = []byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"sub"}],"model":"m"},"parent_tool_use_id":"toolu_1","session_id":"s1"}`)
)

func state(s string, hostOnly bool) []byte {
	if hostOnly {
		return []byte(`{"type":"system","subtype":"session_state_changed","state":"` + s + `","sdk_host_only":true,"session_id":"s1"}`)
	}
	return []byte(`{"type":"system","subtype":"session_state_changed","state":"` + s + `","session_id":"s1"}`)
}

// runGated runs a hooked one-shot Query over script and returns every message
// the caller saw. The replay starts once the prompt is written; it stops at
// line gate (if gate >= 0) until check has run, and it holds the EOF back
// until stdin has closed, so the run must end on its own frames: at EOF the
// read loop ends it anyway, which would hide a run that never ended.
func runGated(t *testing.T, script [][]byte, gate int, check func(st *scriptedTransport), opts ...Option) (*scriptedTransport, []Message) {
	t.Helper()
	st := newScriptedTransport(script...)
	st.afterPrompt = true
	st.reached = make(chan int, 2)
	release, eof := make(chan struct{}), make(chan struct{})
	st.gates = map[int]chan struct{}{len(script): eof}
	if gate >= 0 {
		st.gates[gate] = release
	}
	restore := installScriptedTransport(st)
	defer restore()

	var seen []Message
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg, err := range Query(context.Background(), "go", append([]Option{withTestHook()}, opts...)...) {
			if err != nil {
				t.Errorf("query err: %v", err)
				return
			}
			seen = append(seen, msg)
		}
	}()
	waitReached := func(i int) {
		select {
		case got := <-st.reached:
			if got != i {
				t.Fatalf("replay stopped at line %d, want %d", got, i)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("replay never reached line %d", i)
		}
	}
	if gate >= 0 {
		waitReached(gate)
		check(st)
		close(release)
	}
	waitReached(len(script))
	waitStdinClosed(t, st, 2*time.Second, "the run must end before EOF")
	close(eof)
	<-done
	return st, seen
}

// A background agent that settled before the turn's result still wakes the
// session for a follow-up turn. The CLI reports "running" until that turn is
// done, so stdin stays open through the first result and closes at "idle".
func TestStdinStaysOpenUntilIdle(t *testing.T) {
	script := [][]byte{
		frameInit,
		state("running", true),
		frameStarted,
		frameSettled,
		frameResult,
		// gate: the follow-up turn
		frameAssist,
		frameResult,
		state("idle", true),
	}
	_, seen := runGated(t, script, 5, func(st *scriptedTransport) {
		assertStdinOpen(t, st, "the CLI still reports running after the first result")
	})
	for _, m := range seen {
		if parseSessionState(m) != nil {
			t.Errorf("an sdk_host_only frame reached the caller: %s", m.(*SystemMessage).Raw)
		}
	}
}

// Frames the caller opted into (unmarked) reach the caller, and drive the run
// end the same way as marked ones.
func TestSessionStateUnmarkedFramesPassThrough(t *testing.T) {
	script := [][]byte{
		frameInit,
		state("running", false),
		frameResult,
		// gate
		state("idle", false),
	}
	_, seen := runGated(t, script, 3, func(st *scriptedTransport) {
		assertStdinOpen(t, st, "running at the result")
	})
	var states []string
	for _, m := range seen {
		if s := parseSessionState(m); s != nil {
			states = append(states, s.State)
		}
	}
	if strings.Join(states, ",") != "running,idle" {
		t.Errorf("caller saw states %v, want [running idle]", states)
	}
}

// Some hosts send "idle" just before the result; the result then ends the run.
func TestIdleBeforeResultEndsAtResult(t *testing.T) {
	runGated(t, [][]byte{frameInit, state("running", true), state("idle", true), frameResult}, -1, nil)
}

// An "idle" that comes before any result does not end the run.
func TestIdleBeforeAnyResultDoesNotEnd(t *testing.T) {
	script := [][]byte{frameInit, state("idle", true), frameResult}
	runGated(t, script, 2, func(st *scriptedTransport) {
		assertStdinOpen(t, st, "idle arrived before any result")
	})
}

// "idle" with a tracked agent still in flight does not end the run; the
// result after it settles does.
func TestIdleWithTaskInFlightKeepsOpen(t *testing.T) {
	script := [][]byte{frameInit, frameStarted, frameResult, state("idle", true), frameSettled, frameResult}
	runGated(t, script, 4, func(st *scriptedTransport) {
		assertStdinOpen(t, st, "a tracked agent was in flight at idle")
	})
}

// The ceiling ends the run when the CLI reports running past a result and no
// new turn starts.
func TestRunEndCeilingEndsRun(t *testing.T) {
	script := [][]byte{frameInit, state("running", true), frameResult, state("idle", true)}
	st, _ := runGated(t, script, 3, func(st *scriptedTransport) {
		waitStdinClosed(t, st, 2*time.Second, "the ceiling passed with no new turn")
	}, WithEnv(map[string]string{runEndCeilingEnv: "20"}))
	if got := st.endInputCount(); got != 1 {
		t.Errorf("EndInput calls = %d, want 1", got)
	}
}

// Subagent messages do not stop the ceiling; a main-thread turn does.
func TestRunEndCeilingSubagentVsMainThread(t *testing.T) {
	t.Run("subagent", func(t *testing.T) {
		script := [][]byte{frameInit, state("running", true), frameResult, frameSubAgent, state("idle", true)}
		runGated(t, script, 4, func(st *scriptedTransport) {
			waitStdinClosed(t, st, 2*time.Second, "a subagent message must not stop the ceiling")
		}, WithEnv(map[string]string{runEndCeilingEnv: "60"}))
	})
	t.Run("main thread", func(t *testing.T) {
		script := [][]byte{frameInit, state("running", true), frameResult, frameAssist, frameResult, state("idle", true)}
		runGated(t, script, 4, func(st *scriptedTransport) {
			assertStdinOpen(t, st, "a main-thread turn is under way")
		}, WithEnv(map[string]string{runEndCeilingEnv: "20"}))
	})
}

// requires_action stops the ceiling: the SDK is answering a request.
func TestRunEndCeilingRequiresAction(t *testing.T) {
	script := [][]byte{frameInit, state("running", true), frameResult, state("requires_action", true), state("idle", true)}
	runGated(t, script, 4, func(st *scriptedTransport) {
		assertStdinOpen(t, st, "requires_action must stop the ceiling")
	}, WithEnv(map[string]string{runEndCeilingEnv: "20"}))
}

// A ceiling of 0 waits for idle however long it takes.
func TestRunEndCeilingZeroWaitsForIdle(t *testing.T) {
	script := [][]byte{frameInit, state("running", true), frameResult, state("idle", true)}
	runGated(t, script, 3, func(st *scriptedTransport) {
		assertStdinOpen(t, st, "a ceiling of 0 means no limit")
	}, WithEnv(map[string]string{runEndCeilingEnv: "0"}))
}

func TestRunTracker(t *testing.T) {
	ended := func(r *runTracker) bool {
		select {
		case <-r.done():
			return true
		default:
			return false
		}
	}
	// feed decodes a frame and observes it, as the read loop does.
	feed := func(t *testing.T, r *runTracker, b []byte) {
		t.Helper()
		m, err := UnmarshalMessage(b)
		if err != nil {
			t.Fatal(err)
		}
		r.observe(m, parseSessionState(m))
	}

	t.Run("no state ends at the first result", func(t *testing.T) {
		r := newRunTracker(0)
		feed(t, r, frameResult)
		if !ended(r) {
			t.Error("an older CLI sends no state; the result ends the run")
		}
	})

	t.Run("running after idle reopens the run", func(t *testing.T) {
		r := newRunTracker(0)
		feed(t, r, frameResult)
		feed(t, r, state("idle", true))
		if !ended(r) {
			t.Fatal("idle after a result ends the run")
		}
		feed(t, r, state("running", true))
		if ended(r) {
			t.Error("work taken up after the run ended must reopen it")
		}
		feed(t, r, state("idle", true))
		if !ended(r) {
			t.Error("the next idle ends the reopened run")
		}
	})

	t.Run("requires_action after idle reopens the run", func(t *testing.T) {
		r := newRunTracker(0)
		feed(t, r, frameResult)
		feed(t, r, state("idle", true))
		feed(t, r, state("requires_action", true))
		if ended(r) {
			t.Error("requires_action must reopen the run")
		}
	})

	t.Run("a later prompt waits for its own run", func(t *testing.T) {
		r := newRunTracker(0)
		r.promptWritten()
		r.promptWritten()
		feed(t, r, state("running", true))
		feed(t, r, state("idle", true))
		if ended(r) {
			t.Error("idle before the prompt's result must not end the run")
		}
		feed(t, r, frameResult)
		if !ended(r) {
			t.Error("the result after idle ends the run")
		}
	})

	t.Run("finished runs stay ended", func(t *testing.T) {
		r := newRunTracker(0)
		r.finish()
		feed(t, r, state("running", true))
		feed(t, r, frameAssist)
		if !ended(r) {
			t.Error("once stdin is closed the run cannot reopen")
		}
	})

	t.Run("ceiling leaves a tracked agent alone and restarts when it settles", func(t *testing.T) {
		r := newRunTracker(20 * time.Millisecond)
		feed(t, r, state("running", true))
		feed(t, r, frameStarted)
		feed(t, r, frameResult)
		time.Sleep(80 * time.Millisecond)
		if ended(r) {
			t.Fatal("the ceiling must not cut off a tracked agent in flight")
		}
		feed(t, r, frameSettled)
		select {
		case <-r.done():
		case <-time.After(2 * time.Second):
			t.Error("the ceiling must start over once the agent settles")
		}
	})

	t.Run("ceiling ended run reopens for a main-thread turn", func(t *testing.T) {
		r := newRunTracker(10 * time.Millisecond)
		feed(t, r, state("running", true))
		feed(t, r, frameResult)
		<-r.done()
		feed(t, r, frameAssist)
		if ended(r) {
			t.Error("a main-thread turn must reopen a run the ceiling ended")
		}
	})

	t.Run("agent settling after idle is bounded by the ceiling", func(t *testing.T) {
		r := newRunTracker(20 * time.Millisecond)
		feed(t, r, frameStarted)
		feed(t, r, frameResult)
		feed(t, r, state("idle", true))
		if ended(r) {
			t.Fatal("idle with a tracked agent in flight must not end the run")
		}
		// Settling must not end the run outright: its completion may still
		// wake a follow-up turn that needs stdin.
		feed(t, r, frameSettled)
		if ended(r) {
			t.Fatal("the run ended as the agent settled, before its follow-up turn")
		}
		// No follow-up turn comes: the ceiling ends the run.
		select {
		case <-r.done():
		case <-time.After(2 * time.Second):
			t.Error("with no follow-up turn the ceiling must end the run")
		}
	})

	t.Run("follow-up turn after a settle while idle clears the ceiling", func(t *testing.T) {
		r := newRunTracker(20 * time.Millisecond)
		feed(t, r, frameStarted)
		feed(t, r, frameResult)
		feed(t, r, state("idle", true))
		feed(t, r, frameSettled)
		feed(t, r, state("running", true))
		feed(t, r, frameAssist)
		time.Sleep(80 * time.Millisecond)
		if ended(r) {
			t.Fatal("the ceiling fired during the follow-up turn")
		}
		feed(t, r, frameResult)
		feed(t, r, state("idle", true))
		if !ended(r) {
			t.Error("idle after the follow-up result ends the run")
		}
	})

	t.Run("ceiling waits while the read loop is backlogged", func(t *testing.T) {
		r := newRunTracker(20 * time.Millisecond)
		feed(t, r, state("running", true))
		feed(t, r, frameResult)
		r.setBacklogged(true)
		time.Sleep(80 * time.Millisecond)
		if ended(r) {
			t.Fatal("unread frames may start a new turn; the ceiling must not end the run")
		}
		r.setBacklogged(false)
		select {
		case <-r.done():
		case <-time.After(2 * time.Second):
			t.Error("once the backlog clears the ceiling must end the run")
		}
	})

	t.Run("nil tracker is a no-op", func(t *testing.T) {
		var r *runTracker
		feed(t, r, frameResult)
		r.promptWritten()
		r.finish()
	})
}

func TestRunEndCeilingFromEnv(t *testing.T) {
	cases := []struct {
		name string
		opt  map[string]string
		proc string // "" leaves the process env unset
		want time.Duration
	}{
		{"default", nil, "", defaultRunEndCeiling},
		{"zero", map[string]string{runEndCeilingEnv: "0"}, "", 0},
		{"options over process", map[string]string{runEndCeilingEnv: "5"}, "9", 5 * time.Millisecond},
		{"process env", nil, "9", 9 * time.Millisecond},
		{"invalid", map[string]string{runEndCeilingEnv: "soon"}, "", defaultRunEndCeiling},
		{"negative", map[string]string{runEndCeilingEnv: "-1"}, "", defaultRunEndCeiling},
		{"float", map[string]string{runEndCeilingEnv: "1.5"}, "", defaultRunEndCeiling},
		{"huge", map[string]string{runEndCeilingEnv: "99999999999999999999999"}, "", maxRunEndCeiling},
		{"above cap", map[string]string{runEndCeilingEnv: "9999999999"}, "", maxRunEndCeiling},
		{"spaces", map[string]string{runEndCeilingEnv: " 7 "}, "", 7 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// t.Setenv restores the variable afterwards; unset it when the
			// case wants it absent, since an empty value is still a value.
			t.Setenv(runEndCeilingEnv, c.proc)
			if c.proc == "" {
				_ = os.Unsetenv(runEndCeilingEnv)
			}
			if got := runEndCeiling(c.opt); got != c.want {
				t.Errorf("runEndCeiling = %v, want %v", got, c.want)
			}
		})
	}
}

// An interactive Client also keeps sdk_host_only frames out of the stream.
func TestClientDropsHostOnlyState(t *testing.T) {
	it := newInteractiveTransport()
	it.onUser = func(turn int, prompt string) [][]byte {
		return [][]byte{state("running", true), state("running", false), frameResult, state("idle", true)}
	}
	restore := installInteractive(it)
	defer restore()

	ctx := context.Background()
	c := NewClient()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Query(ctx, "hi"); err != nil {
		t.Fatal(err)
	}
	var states []Message
	for msg, err := range c.ReceiveResponse(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		if parseSessionState(msg) != nil {
			states = append(states, msg)
		}
	}
	if len(states) != 1 {
		t.Fatalf("saw %d state frames, want only the unmarked one", len(states))
	}
	if parseSessionState(states[0]).SDKHostOnly {
		t.Error("the sdk_host_only frame leaked")
	}
}
