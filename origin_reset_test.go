package claude

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeConversationReset(t *testing.T) {
	msg, err := UnmarshalMessage([]byte(`{"type":"conversation_reset","new_conversation_id":"c2","uuid":"u","session_id":"s1"}`))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := msg.(*ConversationResetMessage)
	if !ok || r.NewConversationID != "c2" || r.UUID != "u" || r.SessionID != "s1" || r.Raw == nil {
		t.Fatalf("got %#v", msg)
	}

	_, err = UnmarshalMessage([]byte(`{"type":"conversation_reset","uuid":"u","session_id":"s"}`))
	var pe *MessageParseError
	if !errors.As(err, &pe) {
		t.Errorf("missing new_conversation_id: err = %v, want a MessageParseError", err)
	}
}

func TestUserMessageOrigin(t *testing.T) {
	peer := `{"kind":"peer","from":"peer-addr","name":"other-session","verifiedPeerPid":4242,"someFutureField":true}`
	for _, content := range []string{`"hi"`, `[{"type":"text","text":"hi"}]`} {
		msg, err := UnmarshalMessage([]byte(`{"type":"user","message":{"content":` + content + `},"origin":` + peer + `}`))
		if err != nil {
			t.Fatal(err)
		}
		o := msg.(*UserMessage).Origin
		if o == nil || o.Kind != OriginPeer || o.From != "peer-addr" || o.Name != "other-session" ||
			o.VerifiedPeerPid == nil || *o.VerifiedPeerPid != 4242 {
			t.Fatalf("origin = %+v", o)
		}
		var raw map[string]any
		if err := json.Unmarshal(o.Raw, &raw); err != nil || raw["someFutureField"] != true {
			t.Errorf("unmodeled key lost from Raw: %s", o.Raw)
		}
	}
}

func TestUserMessageOriginAbsentOrMalformed(t *testing.T) {
	for _, extra := range []string{``, `,"origin":null`, `,"origin":"human"`, `,"origin":{}`, `,"origin":{"kind":null}`, `,"origin":{"kind":5}`} {
		msg, err := UnmarshalMessage([]byte(`{"type":"user","message":{"content":"hi"}` + extra + `}`))
		if err != nil {
			t.Fatalf("%q: %v", extra, err)
		}
		if o := msg.(*UserMessage).Origin; o != nil {
			t.Errorf("%q: origin = %+v, want nil", extra, o)
		}
	}
}

func TestResultMessageOrigin(t *testing.T) {
	base := `{"type":"result","subtype":"success","duration_ms":1000,"duration_api_ms":500,"is_error":false,"num_turns":2,"session_id":"session_123"`
	decode := func(extra string) *ResultMessage {
		t.Helper()
		msg, err := UnmarshalMessage([]byte(base + extra + `}`))
		if err != nil {
			t.Fatalf("%q: %v", extra, err)
		}
		return msg.(*ResultMessage)
	}
	if o := decode(``).Origin; o != nil {
		t.Errorf("absent origin = %+v", o)
	}
	if o := decode(`,"origin":{"kind":"human"}`).Origin; o == nil || o.Kind != OriginHuman {
		t.Errorf("human origin = %+v", o)
	}
	for _, sub := range []TaskNotificationOriginSubkind{SubkindScheduledTrigger, SubkindPeerSendMessage} {
		o := decode(`,"origin":{"kind":"task-notification","subkind":"` + string(sub) + `"}`).Origin
		if o == nil || o.Kind != OriginTaskNotification || o.Subkind != sub {
			t.Errorf("task-notification origin = %+v", o)
		}
	}
	if o := decode(`,"origin":{"kind":"unclassified"}`).Origin; o == nil || o.Kind != OriginUnclassified {
		t.Errorf("unclassified origin = %+v", o)
	}
	// A malformed origin never fails the result.
	for _, bad := range []string{`,"origin":"x"`, `,"origin":{"kind":"peer","verifiedPeerPid":"not a number"}`} {
		r := decode(bad)
		if bad == `,"origin":"x"` && r.Origin != nil {
			t.Errorf("%q: origin = %+v", bad, r.Origin)
		}
		if bad != `,"origin":"x"` && (r.Origin == nil || r.Origin.Kind != OriginPeer) {
			t.Errorf("%q: kind lost when another field is malformed: %+v", bad, r.Origin)
		}
	}
}

// An unknown frame type must not end the stream: the official SDK skips it so
// a newer CLI does not break an older SDK.
func TestUnknownFrameTypeSkipped(t *testing.T) {
	st := newScriptedTransport(
		[]byte(`{"type":"some_future_frame","x":1}`),
		[]byte(`{"type":"conversation_reset","new_conversation_id":"c2","uuid":"u","session_id":"s1"}`),
		[]byte(`{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`),
	)
	defer installScriptedTransport(st)()

	var reset, result bool
	for msg, err := range Query(context.Background(), "hi") {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		switch msg.(type) {
		case *ConversationResetMessage:
			reset = true
		case *ResultMessage:
			result = true
		}
	}
	if !reset || !result {
		t.Errorf("reset=%v result=%v", reset, result)
	}

	// UnmarshalMessage still reports the unknown type to direct callers.
	var pe *MessageParseError
	if _, err := UnmarshalMessage([]byte(`{"type":"some_future_frame"}`)); !errors.As(err, &pe) {
		t.Errorf("UnmarshalMessage err = %v, want a MessageParseError", err)
	}
}
