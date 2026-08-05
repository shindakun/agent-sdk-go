package claude

import "testing"

// TestResultModelUsageDecodes pins the modelUsage wire shape. The key is
// camelCase (the CLI passes its value through verbatim), unlike the snake_case
// used for most result fields, and the previous json:"model_usage" tag meant
// the field never populated. Payload captured from a live CLI 2.1.222 result
// frame.
func TestResultModelUsageDecodes(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","session_id":"s","usage":{"input_tokens":2,"output_tokens":13},` +
		`"modelUsage":{"claude-opus-5[1m]":{"inputTokens":2,"outputTokens":13,"cacheReadInputTokens":19051,` +
		`"cacheCreationInputTokens":9959,"webSearchRequests":0,"costUSD":0.1094505,"contextWindow":1000000,` +
		`"maxOutputTokens":64000,"canonicalModel":"claude-opus-5","provider":"firstParty"}}}`)

	m, err := UnmarshalMessage(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r, ok := m.(*ResultMessage)
	if !ok {
		t.Fatalf("got %T, want *ResultMessage", m)
	}
	if len(r.ModelUsage) != 1 {
		t.Fatalf("ModelUsage = %v, want one entry", r.ModelUsage)
	}
	u, ok := r.ModelUsage["claude-opus-5[1m]"]
	if !ok {
		t.Fatalf("missing the billed model key; got %v", r.ModelUsage)
	}
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"InputTokens", u.InputTokens, 2},
		{"OutputTokens", u.OutputTokens, 13},
		{"CacheReadInputTokens", u.CacheReadInputTokens, 19051},
		{"CacheCreationInputTokens", u.CacheCreationInputTokens, 9959},
		{"WebSearchRequests", u.WebSearchRequests, 0},
		{"ContextWindow", u.ContextWindow, 1000000},
		{"MaxOutputTokens", u.MaxOutputTokens, 64000},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if u.CostUSD != 0.1094505 {
		t.Errorf("CostUSD = %v, want 0.1094505", u.CostUSD)
	}
	if u.CanonicalModel != "claude-opus-5" {
		t.Errorf("CanonicalModel = %q, want claude-opus-5", u.CanonicalModel)
	}
	if u.Provider != "firstParty" {
		t.Errorf("Provider = %q, want firstParty", u.Provider)
	}
}

// A result frame without modelUsage leaves the map nil rather than failing.
func TestResultModelUsageAbsent(t *testing.T) {
	m, err := UnmarshalMessage([]byte(`{"type":"result","subtype":"success","session_id":"s"}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if mu := m.(*ResultMessage).ModelUsage; mu != nil {
		t.Errorf("ModelUsage = %v, want nil", mu)
	}
}
