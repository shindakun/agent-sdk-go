package claude

import (
	"context"
	"encoding/json"
	"time"
)

// HookEvent names a point in the agent lifecycle at which a hook may run.
type HookEvent string

const (
	HookPreToolUse         HookEvent = "PreToolUse"
	HookPostToolUse        HookEvent = "PostToolUse"
	HookPostToolUseFailure HookEvent = "PostToolUseFailure"
	HookUserPromptSubmit   HookEvent = "UserPromptSubmit"
	HookStop               HookEvent = "Stop"
	HookSubagentStop       HookEvent = "SubagentStop"
	HookSubagentStart      HookEvent = "SubagentStart"
	HookPreCompact         HookEvent = "PreCompact"
	HookNotification       HookEvent = "Notification"
	HookPermissionRequest  HookEvent = "PermissionRequest"
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
	// OnFailure is what happens when a callback in this matcher fails. With
	// [HookOnFailureBlock], a callback that returns an error, or a value the
	// CLI rejects (such as permissionDecision "Deny"), blocks the action, so a
	// callback with no opinion must return a zero HookOutput. Unrecognized keys
	// are not a failure. Stop, SubagentStop, TaskCompleted and TeammateIdle
	// ignore this option, and a PermissionRequest block counts only if it lands
	// before CanUseTool answers. Empty or [HookOnFailureContinue] counts a
	// failure as no decision. With anything but those two, connecting to a CLI
	// too old to honor it fails with a [ConnectionError].
	OnFailure HookOnFailure
}

// HookOnFailure is a [HookMatcher.OnFailure] mode.
type HookOnFailure string

const (
	// HookOnFailureContinue counts a failing callback as no decision.
	HookOnFailureContinue HookOnFailure = "continue"
	// HookOnFailureBlock blocks the action when a callback fails.
	HookOnFailureBlock HookOnFailure = "block"
)

// HookOutput is returned by a [HookCallback] to influence agent behavior. A zero
// value is a no-op that lets execution proceed.
type HookOutput struct {
	// Decision, when set to "block", blocks the action.
	Decision string
	// SystemMessage is a warning shown to the user.
	SystemMessage string
	// Reason is feedback for Claude about the decision.
	Reason string
	// Continue, when non-nil and false, halts the agent.
	Continue *bool
	// StopReason is the message shown when Continue is false.
	StopReason string
	// SuppressOutput hides the hook's stdout from the transcript.
	SuppressOutput bool
	// HookSpecificOutput carries event-specific structured output, such as a
	// [PreToolUseHookSpecificOutput] permission decision.
	HookSpecificOutput json.RawMessage
	// Async defers the hook: the CLI continues without waiting for it.
	// AsyncTimeout bounds the deferred run, in milliseconds.
	Async        bool
	AsyncTimeout int
}
