package claude

import (
	"context"
	"encoding/json"
	"time"
)

// HookEvent names a point in the agent lifecycle at which a hook may run.
type HookEvent string

const (
	HookPreToolUse       HookEvent = "PreToolUse"
	HookPostToolUse      HookEvent = "PostToolUse"
	HookUserPromptSubmit HookEvent = "UserPromptSubmit"
	HookStop             HookEvent = "Stop"
	HookSubagentStop     HookEvent = "SubagentStop"
	HookPreCompact       HookEvent = "PreCompact"
	HookNotification     HookEvent = "Notification"
	HookSessionStart     HookEvent = "SessionStart"
	HookSessionEnd       HookEvent = "SessionEnd"
)

// HookCallback runs custom logic at a hook point. input is the raw event payload
// (its shape depends on the event); toolUseID identifies the associated tool
// call for tool-related events and is empty otherwise.
type HookCallback func(ctx context.Context, input json.RawMessage, toolUseID string) (HookOutput, error)

// HookMatcher binds a set of callbacks to events whose subject matches Matcher.
// For tool events Matcher is a tool-name pattern (for example "Edit|Write"); an
// empty Matcher matches all subjects.
type HookMatcher struct {
	Matcher   string
	Callbacks []HookCallback
	Timeout   time.Duration
}

// HookOutput is returned by a [HookCallback] to influence agent behavior. A zero
// value is a no-op that lets execution proceed.
type HookOutput struct {
	// Decision, when set (for example "block"), affects whether the action
	// proceeds.
	Decision string
	// SystemMessage is injected into the conversation as a system note.
	SystemMessage string
	// Continue, when non-nil and false, halts the agent.
	Continue *bool
	// SuppressOutput hides the hook's stdout from the transcript.
	SuppressOutput bool
	// HookSpecificOutput carries event-specific structured output, such as a
	// PreToolUse permission decision.
	HookSpecificOutput json.RawMessage
}
