package claude

import (
	"context"
	"encoding/json"
	"testing"
)

// TestInflightTasksTrack covers the lifecycle transitions that decide whether a
// result frame ends the run. A task in flight must keep stdin open; anything
// that cannot reliably reach a terminal status must never enter the set, or the
// query would hang waiting for a completion that never arrives.
func TestInflightTasksTrack(t *testing.T) {
	t.Run("zero value is empty", func(t *testing.T) {
		var i inflightTasks
		if !i.empty() {
			t.Error("zero value should be empty")
		}
	})

	t.Run("deferring task type is tracked until notification", func(t *testing.T) {
		var i inflightTasks
		i.track(&TaskStartedMessage{TaskID: "t1", TaskType: "local_agent"})
		if i.empty() {
			t.Fatal("local_agent should be tracked")
		}
		i.track(&TaskNotificationMessage{TaskID: "t1"})
		if !i.empty() {
			t.Error("task_notification should clear the task")
		}
	})

	t.Run("terminal task_updated clears", func(t *testing.T) {
		var i inflightTasks
		i.track(&TaskStartedMessage{TaskID: "t1", TaskType: "local_workflow"})
		i.track(&TaskUpdatedMessage{TaskID: "t1", Status: "running"})
		if i.empty() {
			t.Error("a non-terminal status must not clear the task")
		}
		i.track(&TaskUpdatedMessage{TaskID: "t1", Status: "completed"})
		if !i.empty() {
			t.Error("a terminal status should clear the task")
		}
	})

	// Background shells and monitors may never reach a terminal status, and the
	// CLI only exits on stdin EOF, so tracking one would withhold the close
	// forever rather than briefly.
	t.Run("non-deferring task types are not tracked", func(t *testing.T) {
		for _, typ := range []string{"background_shell", "teammate", "remote_agent", ""} {
			var i inflightTasks
			i.track(&TaskStartedMessage{TaskID: "t1", TaskType: typ})
			if !i.empty() {
				t.Errorf("task_type %q must not be tracked", typ)
			}
		}
	})

	t.Run("missing task id is ignored", func(t *testing.T) {
		var i inflightTasks
		i.track(&TaskStartedMessage{TaskType: "local_agent"})
		if !i.empty() {
			t.Error("a frame with no task_id must not be tracked")
		}
	})

	t.Run("clearing an unknown id is a no-op", func(t *testing.T) {
		var i inflightTasks
		i.track(&TaskNotificationMessage{TaskID: "nope"})
		i.track(&TaskUpdatedMessage{TaskID: "nope", Status: "completed"})
		if !i.empty() {
			t.Error("clearing an untracked id should not add it")
		}
	})

	t.Run("multiple tasks settle independently", func(t *testing.T) {
		var i inflightTasks
		i.track(&TaskStartedMessage{TaskID: "a", TaskType: "local_agent"})
		i.track(&TaskStartedMessage{TaskID: "b", TaskType: "local_agent"})
		i.track(&TaskNotificationMessage{TaskID: "a"})
		if i.empty() {
			t.Error("b is still in flight")
		}
		i.track(&TaskNotificationMessage{TaskID: "b"})
		if !i.empty() {
			t.Error("both tasks settled; set should be empty")
		}
	})

	t.Run("other messages are ignored", func(t *testing.T) {
		var i inflightTasks
		i.track(&ResultMessage{})
		i.track(&AssistantMessage{})
		if !i.empty() {
			t.Error("non-lifecycle messages must not affect tracking")
		}
	})
}

// TestResultWithInflightTaskKeepsStdinOpen is the behavioral contract: a result
// frame arriving while a background task is in flight ends one turn, not the
// run, so stdin must stay open for the task's hook and SDK MCP control
// responses. Stdin closes on the later result that arrives with nothing in
// flight. Parametrized over both drain frames, since a terminal task may report
// via task_notification or a terminal task_updated.
func TestResultWithInflightTaskKeepsStdinOpen(t *testing.T) {
	drains := map[string][]byte{
		"task_notification": []byte(`{"type":"system","subtype":"task_notification","task_id":"t1","status":"completed","session_id":"s1"}`),
		"task_updated":      []byte(`{"type":"system","subtype":"task_updated","task_id":"t1","patch":{"status":"completed"},"session_id":"s1"}`),
	}
	for name, drain := range drains {
		t.Run(name, func(t *testing.T) {
			st := newScriptedTransport(
				[]byte(`{"type":"system","subtype":"init","session_id":"s1","tools":["Task"]}`),
				[]byte(`{"type":"system","subtype":"task_started","task_id":"t1","task_type":"local_agent","session_id":"s1"}`),
				// Turn-ending result: a task is still in flight, so stdin must stay open.
				[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"started","session_id":"s1"}`),
				drain,
				// Run-ending result: nothing in flight, so stdin closes here.
				[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}`),
			)
			restore := installScriptedTransport(st)
			defer restore()

			// A hook makes the session bidirectional, which is the mode that
			// defers the close.
			hook := func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error) {
				return HookOutput{}, nil
			}

			var afterFirstResult int
			results := 0
			for msg, err := range Query(context.Background(), "go",
				WithHooks(map[HookEvent][]HookMatcher{
					HookPreToolUse: {{Callbacks: []HookCallback{hook}}},
				}),
			) {
				if err != nil {
					t.Fatalf("query err: %v", err)
				}
				if _, ok := msg.(*ResultMessage); ok {
					results++
					if results == 2 {
						// Sampled at the second result: by now the loop body
						// has fully processed the first one. With the bug,
						// that first result closed stdin and this reads 1.
						afterFirstResult = st.endInputCount()
					}
				}

			}

			if results != 2 {
				t.Fatalf("saw %d results, want 2", results)
			}
			if afterFirstResult != 0 {
				t.Errorf("stdin was closed on the turn-ending result (%d EndInput calls); "+
					"a task was in flight and still needed the control channel", afterFirstResult)
			}
			if got := st.endInputCount(); got == 0 {
				t.Error("stdin never closed; the run-ending result must close it")
			}
		})
	}
}

// With no tasks in flight the first result still closes stdin, so the ordinary
// one-shot path is unchanged.
func TestResultWithNoTasksClosesStdin(t *testing.T) {
	st := newScriptedTransport(
		[]byte(`{"type":"system","subtype":"init","session_id":"s1","tools":["Read"]}`),
		[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"hi","session_id":"s1"}`),
	)
	restore := installScriptedTransport(st)
	defer restore()

	hook := func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error) {
		return HookOutput{}, nil
	}
	for _, err := range Query(context.Background(), "hi",
		WithHooks(map[HookEvent][]HookMatcher{
			HookPreToolUse: {{Callbacks: []HookCallback{hook}}},
		}),
	) {
		if err != nil {
			t.Fatalf("query err: %v", err)
		}
	}
	if st.endInputCount() == 0 {
		t.Error("the first result must close stdin when nothing is in flight")
	}
}

// TestTaskNotificationSystemSubtype pins the wire shape: the CLI emits
// task_notification as a system subtype (captured from 2.1.222), not as a
// top-level frame type. Decoding it as a generic SystemMessage meant
// TaskNotificationMessage was never delivered for real traffic, and the
// in-flight task ledger never drained. The top-level form is still accepted.
func TestTaskNotificationSystemSubtype(t *testing.T) {
	for name, raw := range map[string]string{
		"system subtype": `{"type":"system","subtype":"task_notification","task_id":"t1","status":"completed","session_id":"s1"}`,
		"top level":      `{"type":"task_notification","task_id":"t1","status":"completed","session_id":"s1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			m, err := UnmarshalMessage([]byte(raw))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tn, ok := m.(*TaskNotificationMessage)
			if !ok {
				t.Fatalf("got %T, want *TaskNotificationMessage", m)
			}
			if tn.TaskID != "t1" {
				t.Errorf("TaskID = %q, want t1", tn.TaskID)
			}
			if !IsTerminalTaskStatus(string(tn.Status)) {
				t.Errorf("Status = %q, want a terminal status", tn.Status)
			}
		})
	}
}
