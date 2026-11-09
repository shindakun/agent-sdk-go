package claude

import (
	"encoding/json"
	"errors"
	"fmt"
)

// callbackRegistry assigns stable ids to hook callbacks and produces the
// initialize hooks-config payload. It is built once per session from the
// Options and consulted by the inbound dispatch handler.
type callbackRegistry struct {
	hooks  map[string]HookCallback // callback_id -> callback
	nextID int
	// blocksOnFailure is set when a matcher's OnFailure is anything but
	// "continue" (the CLI reads an unknown value as "block").
	blocksOnFailure bool
}

func newCallbackRegistry() *callbackRegistry {
	return &callbackRegistry{hooks: map[string]HookCallback{}}
}

// build registers all hook callbacks from opts and returns the serialized hooks
// config (or nil when there are no hooks). The config shape mirrors the
// official SDK: {event: [{matcher, hookCallbackIds, timeout}]}.
func (r *callbackRegistry) build(opts *Options) (json.RawMessage, error) {
	if len(opts.hooks) == 0 {
		return nil, nil
	}

	config := map[string][]map[string]any{}
	for event, matchers := range opts.hooks {
		for _, m := range matchers {
			ids := make([]string, 0, len(m.Callbacks))
			for _, cb := range m.Callbacks {
				id := fmt.Sprintf("hook_%d", r.nextID)
				r.nextID++
				r.hooks[id] = cb
				ids = append(ids, id)
			}
			entry := map[string]any{"hookCallbackIds": ids}
			if m.Matcher != "" {
				entry["matcher"] = m.Matcher
			}
			if m.Timeout > 0 {
				// The CLI reads a matcher's timeout in seconds, as upstream sends it.
				entry["timeout"] = m.Timeout.Seconds()
			}
			if m.OnFailure != "" {
				entry["onFailure"] = m.OnFailure
				if m.OnFailure != HookOnFailureContinue {
					r.blocksOnFailure = true
				}
			}
			config[string(event)] = append(config[string(event)], entry)
		}
	}

	return json.Marshal(config)
}

// checkHookOnFailure reports, for a session whose hooks block on failure,
// whether the initialize response shows the guard will hold. A responder that
// doesn't honor onFailure, such as a CLI that predates it, drops the field and
// answers normally, so the guard would fail open; hooks_applied false means
// nothing registered these callbacks, as on a process that was already running.
func (r *callbackRegistry) checkHookOnFailure(initResult json.RawMessage) error {
	if !r.blocksOnFailure {
		return nil
	}
	var resp struct {
		HookOnFailureSupported bool  `json:"hook_on_failure_supported"`
		HooksApplied           *bool `json:"hooks_applied"`
	}
	_ = json.Unmarshal(initResult, &resp)
	if !resp.HookOnFailureSupported {
		return errors.New("the process answering initialize does not honor OnFailure " +
			"on hook matchers, so a hook that fails would not block; update Claude Code " +
			"if it is old, or remove OnFailure \"block\" from your hooks")
	}
	if resp.HooksApplied != nil && !*resp.HooksApplied {
		return errors.New("this session was already running and did not register your " +
			"hooks, so a hook set to OnFailure \"block\" would never run; start a new " +
			"session, or remove OnFailure \"block\" from your hooks")
	}
	return nil
}

// lookupHook returns the callback for id, or nil.
func (r *callbackRegistry) lookupHook(id string) HookCallback {
	return r.hooks[id]
}
