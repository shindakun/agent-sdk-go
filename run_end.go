package claude

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// runEndCeilingEnv is the CLI's own wait for background work once stdin is
// closed; [Query] bounds its wait for the CLI's "idle" by the same value.
const runEndCeilingEnv = "CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS"

// defaultRunEndCeiling is the CLI's default background-wait ceiling.
const defaultRunEndCeiling = 600_000 * time.Millisecond

// maxRunEndCeiling is the longest ceiling honored (about 24.8 days), as in the
// official SDKs, whose timers cannot run longer.
const maxRunEndCeiling = (1<<31 - 1) * time.Millisecond

// runEndCeiling reads CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS as the CLI will see
// it: env (the [WithEnv] map) overrides the inherited environment, as it does
// for the subprocess. 0 means no limit; anything that is not a plain
// non-negative integer falls back to the CLI's default of 10 minutes.
func runEndCeiling(env map[string]string) time.Duration {
	raw, ok := env[runEndCeilingEnv]
	if !ok {
		raw, ok = os.LookupEnv(runEndCeilingEnv)
	}
	if !ok {
		return defaultRunEndCeiling
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	switch {
	case errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(strings.TrimSpace(raw), "-"):
		return maxRunEndCeiling
	case err != nil, ms < 0:
		return defaultRunEndCeiling
	case ms > int64(maxRunEndCeiling/time.Millisecond):
		return maxRunEndCeiling
	}
	return time.Duration(ms) * time.Millisecond
}

// sessionState is the decoded part of a system/session_state_changed frame.
type sessionState struct {
	State       string `json:"state"`
	SDKHostOnly bool   `json:"sdk_host_only"`
}

// parseSessionState returns the state carried by a session_state_changed
// frame, and nil for any other message.
func parseSessionState(msg Message) *sessionState {
	sm, ok := msg.(*SystemMessage)
	if !ok || sm.Subtype != "session_state_changed" {
		return nil
	}
	var st sessionState
	if json.Unmarshal(sm.Raw, &st) != nil {
		return nil
	}
	return &st
}

// runTracker decides when a one-shot [Query] that serves control requests
// (hooks, a permission callback, SDK MCP servers) may close stdin.
//
// A result ends a turn, not necessarily the run: a background agent that
// finished just before it still wakes the session for a follow-up turn, whose
// hook, permission and SDK MCP requests need stdin. The CLI reports when no
// further turn is owed through session_state_changed frames (asked for with
// CLAUDE_CODE_SDK_READS_SESSION_STATE): it stays "running" while such a turn is
// owed and reports "idle" once the session is done. So the run ends:
//
//   - at the "idle" that follows a result;
//   - at a result that follows "idle";
//   - at a result when the CLI sent no state (an older CLI);
//   - never at an "idle" that comes before any result.
//
// A tracked background agent still in flight ([inflightTasks]) holds the run
// open in every case. The wait between turns is bounded by
// CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS, since the CLI's own ceiling only counts
// once stdin is closed.
//
// The zero value is not usable; build one with newRunTracker. A nil
// *runTracker ignores every call, so the session can call it unconditionally.
type runTracker struct {
	mu      sync.Mutex
	ceiling time.Duration

	// ended is closed when the run is over; reopen swaps in a fresh channel
	// for work the CLI takes up afterwards.
	ended    chan struct{}
	isEnded  bool
	inflight inflightTasks
	// state is the CLI's latest session state, or "" while it sends none.
	state          string
	resultReceived bool
	// turnInProgress is set while a main-thread turn is under way (its
	// assistant/stream_event frames have started and its result has not
	// arrived): the ceiling counts only the wait between turns.
	turnInProgress bool
	// final is set once stdin is closed or the reader is gone: the run then
	// stays ended, since nothing can wait on a reopened one.
	final bool
	// timer ends the run if no new turn starts within the ceiling after a
	// result. gen tells a timer that fired after it was cleared or re-armed
	// to stand down.
	timer *time.Timer
	gen   int
	// backlogged is set while the read loop is blocked handing a message to
	// a slow caller: frames behind it (such as the start of a new turn) are
	// not observed yet, so the ceiling must not end the run meanwhile.
	backlogged bool
}

func newRunTracker(ceiling time.Duration) *runTracker {
	return &runTracker{ceiling: ceiling, ended: make(chan struct{})}
}

// observe updates the run from one message the CLI emitted, before it reaches
// the caller. st is the message's session state, from parseSessionState.
func (r *runTracker) observe(msg Message, st *sessionState) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	hadTasks := !r.inflight.empty()
	r.inflight.track(msg)
	if hadTasks && r.inflight.empty() {
		if r.state == "idle" && r.resultReceived {
			// "idle" was held back only by this agent. Its completion
			// normally wakes a follow-up turn, which must not find stdin
			// closed, so do not end the run here; but if no turn comes,
			// nothing else would end it, so the ceiling bounds the wait.
			r.armLocked()
		} else {
			// The ceiling left the last tracked agent alone; the wait
			// between turns starts over now that it settled.
			r.rearmBetweenTurnsLocked()
		}
	}
	if st != nil {
		r.onStateLocked(st.State)
		return
	}

	switch m := msg.(type) {
	case *ResultMessage:
		r.resultReceived = true
		r.turnInProgress = false
		switch r.state {
		case "", "idle":
			r.maybeEndLocked()
		case "requires_action":
			// The SDK is still answering a request; the ceiling waits for
			// the "running" that follows.
		default:
			r.armLocked()
		}
	case *AssistantMessage:
		if m.ParentToolUseID == "" {
			r.turnStartedLocked()
		}
	case *StreamEvent:
		if m.ParentToolUseID == "" {
			r.turnStartedLocked()
		}
	}
}

// turnStartedLocked marks a main-thread turn under way: the ceiling stops (it
// counts only the wait between turns, as the CLI's does) and the run reopens
// even if the ceiling ended it while no state changed.
func (r *runTracker) turnStartedLocked() {
	r.turnInProgress = true
	r.reopenLocked()
	r.clearLocked()
}

func (r *runTracker) onStateLocked(state string) {
	r.state = state
	if state == "idle" {
		if r.resultReceived {
			r.maybeEndLocked()
		}
		return
	}
	// Work the CLI took up after the run ended (a finished background task
	// woke it) reopens the run until the next "idle".
	r.reopenLocked()
	if state == "requires_action" {
		// The SDK is answering a request; stdin must outlast it.
		r.clearLocked()
	} else {
		r.rearmBetweenTurnsLocked()
	}
}

// promptWritten is called before each prompt is written: the prompt owes a run
// of its own, result included, so an earlier run having ended does not end it.
func (r *runTracker) promptWritten() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reopenLocked()
	r.resultReceived = false
	r.clearLocked()
}

// setBacklogged records whether the read loop is blocked on a slow caller.
func (r *runTracker) setBacklogged(b bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.backlogged = b
	r.mu.Unlock()
}

// done returns the channel closed when the current run ends. A waiter it
// already woke still closes stdin even if the run later reopens.
func (r *runTracker) done() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ended
}

// finish ends the run for good: stdin is closing or the reader is gone.
func (r *runTracker) finish() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.final = true
	r.endLocked()
}

// maybeEndLocked ends the run unless a tracked background task is still in
// flight. Such a task may still need hook and SDK MCP control responses over
// stdin, and its completion wakes the parent for a follow-up turn, so a later
// result (or "idle") ends the run then.
func (r *runTracker) maybeEndLocked() {
	if !r.inflight.empty() {
		return
	}
	r.endLocked()
}

func (r *runTracker) endLocked() {
	r.clearLocked()
	if !r.isEnded {
		r.isEnded = true
		close(r.ended)
	}
}

func (r *runTracker) reopenLocked() {
	if r.isEnded && !r.final {
		r.isEnded = false
		r.ended = make(chan struct{})
	}
}

// armLocked ends the run anyway once the ceiling passes with no new turn.
// Without it, work that never finishes would hold "running", and stdin, open
// forever. It is restarted at each result and whenever the CLI reports
// "running" again, cleared by main-thread turn activity and by
// "requires_action", and never armed while a turn is under way.
func (r *runTracker) armLocked() {
	r.clearLocked()
	if r.ceiling <= 0 || r.isEnded || r.final || r.turnInProgress {
		return
	}
	gen := r.gen
	r.timer = time.AfterFunc(r.ceiling, func() { r.ceilingReached(gen) })
}

// rearmBetweenTurnsLocked restarts the ceiling if the run is between turns,
// past a result, with the CLI still reporting work.
func (r *runTracker) rearmBetweenTurnsLocked() {
	switch r.state {
	case "", "idle", "requires_action":
		return
	}
	if r.resultReceived {
		r.armLocked()
	}
}

func (r *runTracker) ceilingReached(gen int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Cleared or re-armed while this timer was already firing.
	if gen != r.gen {
		return
	}
	r.timer = nil
	// A tracked background agent still running may still need stdin, so it
	// is not cut off; the ceiling starts over once it settles (observe).
	if !r.inflight.empty() {
		return
	}
	// Frames the read loop has not reached yet may start a new turn; wait a
	// full ceiling again rather than close stdin under it.
	if r.backlogged {
		r.armLocked()
		return
	}
	r.endLocked()
}

func (r *runTracker) clearLocked() {
	r.gen++
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}
