package claude

import (
	"encoding/json"
	"fmt"
)

// callbackRegistry assigns stable ids to hook callbacks and produces the
// initialize hooks-config payload. It is built once per session from the
// Options and consulted by the inbound dispatch handler.
type callbackRegistry struct {
	hooks  map[string]HookCallback // callback_id -> callback
	nextID int
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
				entry["timeout"] = int(m.Timeout.Milliseconds())
			}
			config[string(event)] = append(config[string(event)], entry)
		}
	}

	return json.Marshal(config)
}

// lookupHook returns the callback for id, or nil.
func (r *callbackRegistry) lookupHook(id string) HookCallback {
	return r.hooks[id]
}
