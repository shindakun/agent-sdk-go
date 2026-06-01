package claude

import (
	"encoding/json"
	"fmt"
)

// ContentBlock is one block of content within a message. The concrete types are
// [TextBlock], [ThinkingBlock], [ToolUseBlock], and [ToolResultBlock].
type ContentBlock interface {
	isContentBlock()
}

// TextBlock is plain text content.
type TextBlock struct {
	Text string `json:"text"`
}

func (*TextBlock) isContentBlock() {}

// ThinkingBlock is extended-thinking content emitted by the model.
type ThinkingBlock struct {
	Thinking  string `json:"thinking"`
	Signature string `json:"signature,omitempty"`
}

func (*ThinkingBlock) isContentBlock() {}

// ToolUseBlock is a request by the model to invoke a tool. Input holds the raw
// JSON arguments; decode it into a concrete type as needed.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (*ToolUseBlock) isContentBlock() {}

// ToolResultBlock is the result of a tool invocation, referenced back to the
// originating [ToolUseBlock] by ToolUseID. Content is the raw JSON of the
// result, which the CLI may encode as a string or as an array of nested content
// blocks; use [ToolResultBlock.Text] or decode Content directly.
type ToolResultBlock struct {
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

func (*ToolResultBlock) isContentBlock() {}

// Text returns the textual content of the result when it is encoded as a plain
// string. The second return value reports whether Content was a JSON string.
func (b *ToolResultBlock) Text() (string, bool) {
	if len(b.Content) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s, true
	}
	return "", false
}

// contentBlocks is an unmarshalling helper for a slice of ContentBlock. It is
// not part of the public API; message types embed []ContentBlock and delegate
// to decodeContentBlocks.
func decodeContentBlocks(raw json.RawMessage) ([]ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Content may be a bare string (some user messages) or an array of blocks.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ContentBlock{&TextBlock{Text: s}}, nil
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("content is neither string nor array: %w", err)
	}

	blocks := make([]ContentBlock, 0, len(arr))
	for _, item := range arr {
		b, err := decodeContentBlock(item)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, nil
}

func decodeContentBlock(raw json.RawMessage) (ContentBlock, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("content block missing type: %w", err)
	}

	switch probe.Type {
	case "text":
		var b TextBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return &b, nil
	case "thinking":
		var b ThinkingBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return &b, nil
	case "tool_use":
		var b ToolUseBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return &b, nil
	case "tool_result":
		var b ToolResultBlock
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return &b, nil
	default:
		// Forward-compatibility: surface unknown block types as text holding
		// their raw JSON so callers are not blocked by CLI additions.
		return &TextBlock{Text: string(raw)}, nil
	}
}
