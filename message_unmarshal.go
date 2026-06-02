package claude

import (
	"encoding/json"
	"errors"
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
		var tn TaskNotificationMessage
		if err := json.Unmarshal(b, &tn); err != nil {
			return nil, &MessageParseError{Type: "task_notification", Raw: clone(b), Err: err}
		}
		tn.Raw = clone(b)
		return &tn, nil
	case "rate_limit_event":
		return decodeRateLimitEvent(b)
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
	var nae *notAMessageError
	return errors.As(err, &nae)
}

func decodeAssistant(b []byte) (Message, error) {
	// Anthropic nests the model message under "message"; session_id/uuid/error
	// and parent_tool_use_id are top-level.
	var env struct {
		Message struct {
			Content    json.RawMessage `json:"content"`
			Model      string          `json:"model"`
			ID         string          `json:"id"`
			StopReason string          `json:"stop_reason"`
			Usage      *Usage          `json:"usage"`
		} `json:"message"`
		ParentToolUseID string          `json:"parent_tool_use_id"`
		SessionID       string          `json:"session_id"`
		UUID            string          `json:"uuid"`
		Error           json.RawMessage `json:"error"`
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
		MessageID:       env.Message.ID,
		StopReason:      env.Message.StopReason,
		SessionID:       env.SessionID,
		UUID:            env.UUID,
		Usage:           env.Message.Usage,
		Error:           env.Error,
		Raw:             clone(b),
	}, nil
}

func decodeUser(b []byte) (Message, error) {
	var env struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		ParentToolUseID string          `json:"parent_tool_use_id"`
		UUID            string          `json:"uuid"`
		ToolUseResult   json.RawMessage `json:"tool_use_result"`
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
		UUID:            env.UUID,
		ToolUseResult:   env.ToolUseResult,
		Raw:             clone(b),
	}, nil
}

func decodeSystem(b []byte) (Message, error) {
	var env struct {
		Subtype   string          `json:"subtype"`
		SessionID string          `json:"session_id"`
		Tools     []string        `json:"tools"`
		Plugins   []PluginInfo    `json:"plugins"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, &MessageParseError{Type: "system", Raw: clone(b), Err: err}
	}

	// Task lifecycle frames are modeled as their own message types.
	switch env.Subtype {
	case "task_started":
		var m TaskStartedMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, &MessageParseError{Type: "system/task_started", Raw: clone(b), Err: err}
		}
		m.Raw = clone(b)
		return &m, nil
	case "task_progress":
		var m TaskProgressMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, &MessageParseError{Type: "system/task_progress", Raw: clone(b), Err: err}
		}
		m.Raw = clone(b)
		return &m, nil
	case "mirror_error":
		var m MirrorErrorMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, &MessageParseError{Type: "system/mirror_error", Raw: clone(b), Err: err}
		}
		m.Raw = clone(b)
		return &m, nil
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
		Plugins:   env.Plugins,
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

// decodeRateLimitEvent decodes a rate_limit_event frame. The nested
// rate_limit_info uses camelCase wire keys (resetsAt, rateLimitType, ...) even
// though the public RateLimitInfo type documents snake_case names, so the wire
// shape is decoded explicitly here.
func decodeRateLimitEvent(b []byte) (Message, error) {
	var wire struct {
		UUID          string `json:"uuid"`
		SessionID     string `json:"session_id"`
		RateLimitInfo struct {
			Status                RateLimitStatus  `json:"status"`
			ResetsAt              *int64           `json:"resetsAt"`
			RateLimitType         *RateLimitType   `json:"rateLimitType"`
			Utilization           *float64         `json:"utilization"`
			OverageStatus         *RateLimitStatus `json:"overageStatus"`
			OverageResetsAt       *int64           `json:"overageResetsAt"`
			OverageDisabledReason *string          `json:"overageDisabledReason"`
		} `json:"rate_limit_info"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil, &MessageParseError{Type: "rate_limit_event", Raw: clone(b), Err: err}
	}
	info := wire.RateLimitInfo
	return &RateLimitEvent{
		UUID:      wire.UUID,
		SessionID: wire.SessionID,
		RateLimitInfo: RateLimitInfo{
			Status:                info.Status,
			ResetsAt:              info.ResetsAt,
			RateLimitType:         info.RateLimitType,
			Utilization:           info.Utilization,
			OverageStatus:         info.OverageStatus,
			OverageResetsAt:       info.OverageResetsAt,
			OverageDisabledReason: info.OverageDisabledReason,
			Raw:                   clone(b),
		},
	}, nil
}

func clone(b []byte) json.RawMessage {
	return append(json.RawMessage(nil), b...)
}
