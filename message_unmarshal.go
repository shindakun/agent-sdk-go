package claude

import (
	"encoding/json"
	"fmt"
)

// UnmarshalMessage decodes a single stream-json line into the concrete [Message]
// type identified by its "type" (and where relevant "subtype") field.
//
// Control-protocol envelopes (control_request, control_response,
// control_cancel_request) and the transcript_mirror frame are not [Message]s;
// they are handled by the transport/protocol layers and UnmarshalMessage
// reports them via [ErrNotAMessage] so callers can skip them.
func UnmarshalMessage(b []byte) (Message, error) {
	var probe struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, &JSONDecodeError{Line: append([]byte(nil), b...), Err: err}
	}

	switch probe.Type {
	case "assistant":
		return decodeAssistant(b)
	case "user":
		return decodeUser(b)
	case "system":
		return decodeSystem(b)
	case "result":
		return decodeResult(b)
	case "stream_event":
		return decodeStreamEvent(b)
	case "task_notification":
		return &TaskNotification{Raw: clone(b)}, nil
	case "control_request", "control_response", "control_cancel_request",
		"transcript_mirror", "end", "error":
		return nil, &notAMessageError{typ: probe.Type}
	default:
		return nil, &MessageParseError{
			Type: probe.Type,
			Raw:  clone(b),
			Err:  fmt.Errorf("unknown message type %q", probe.Type),
		}
	}
}

// notAMessageError reports that a line is a valid stream-json frame but not a
// user-facing [Message]. Match it with [IsNotAMessage].
type notAMessageError struct{ typ string }

func (e *notAMessageError) Error() string {
	return fmt.Sprintf("claude: frame of type %q is not a message", e.typ)
}

// IsNotAMessage reports whether err indicates a non-message stream frame (a
// control-protocol envelope or sentinel) rather than a decode failure.
func IsNotAMessage(err error) bool {
	_, ok := err.(*notAMessageError)
	return ok
}

func decodeAssistant(b []byte) (Message, error) {
	// Anthropic nests the model message under "message".
	var env struct {
		Message struct {
			Content json.RawMessage `json:"content"`
			Model   string          `json:"model"`
		} `json:"message"`
		ParentToolUseID string `json:"parent_tool_use_id"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, &MessageParseError{Type: "assistant", Raw: clone(b), Err: err}
	}
	blocks, err := decodeContentBlocks(env.Message.Content)
	if err != nil {
		return nil, &MessageParseError{Type: "assistant", Raw: clone(b), Err: err}
	}
	return &AssistantMessage{
		Content:         blocks,
		Model:           env.Message.Model,
		ParentToolUseID: env.ParentToolUseID,
		Raw:             clone(b),
	}, nil
}

func decodeUser(b []byte) (Message, error) {
	var env struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		ParentToolUseID string `json:"parent_tool_use_id"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, &MessageParseError{Type: "user", Raw: clone(b), Err: err}
	}
	blocks, err := decodeContentBlocks(env.Message.Content)
	if err != nil {
		return nil, &MessageParseError{Type: "user", Raw: clone(b), Err: err}
	}
	return &UserMessage{
		Content:         blocks,
		ParentToolUseID: env.ParentToolUseID,
		Raw:             clone(b),
	}, nil
}

func decodeSystem(b []byte) (Message, error) {
	var env struct {
		Subtype   string          `json:"subtype"`
		SessionID string          `json:"session_id"`
		Tools     []string        `json:"tools"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, &MessageParseError{Type: "system", Raw: clone(b), Err: err}
	}
	// "init" carries session_id and tools at the top level rather than in data.
	data := env.Data
	if len(data) == 0 {
		data = clone(b)
	}
	return &SystemMessage{
		Subtype:   env.Subtype,
		SessionID: env.SessionID,
		Tools:     env.Tools,
		Data:      data,
		Raw:       clone(b),
	}, nil
}

func decodeResult(b []byte) (Message, error) {
	var m ResultMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, &MessageParseError{Type: "result", Raw: clone(b), Err: err}
	}
	m.Raw = clone(b)
	return &m, nil
}

func decodeStreamEvent(b []byte) (Message, error) {
	var m StreamEvent
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, &MessageParseError{Type: "stream_event", Raw: clone(b), Err: err}
	}
	m.Raw = clone(b)
	return &m, nil
}

func clone(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
