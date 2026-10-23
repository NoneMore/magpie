package gateway

import (
	"encoding/json"
	"reflect"
	"testing"
)

func preparedSample(t *testing.T) ([]byte, codexPreparedOrigin) {
	t.Helper()
	own := "You are Pi."
	added := map[string]any{
		"type": "message", "role": "developer",
		"content": []any{map[string]any{"type": "input_text", "text": own}},
	}
	sample := map[string]any{
		"model": "gpt-5.5", "instructions": "You are Codex.",
		"prompt_cache_key": "the-thread",
		"stream":           true, "store": false,
		"tool_choice": "auto",
		"tools":       []any{map[string]any{"type": "function", "name": "read"}},
		"input":       []any{added, map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	body, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	return body, codexPreparedOrigin{instructions: own, injected: true}
}

func TestPreparedCodexScopeAndCacheIsolation(t *testing.T) {
	before, origin := preparedSample(t)
	var m map[string]any
	if err := json.Unmarshal(before, &m); err != nil {
		t.Fatal(err)
	}
	// With a verified synthesized first developer item, the only legal
	// input edit is deleting that item without changing any other turn.
	m["instructions"] = origin.instructions
	m["input"] = m["input"].([]any)[1:]
	after, _ := json.Marshal(m)
	got, ok := safePreparedCodexChange(before, after, origin)
	if !ok {
		t.Fatal("valid opt-in change was refused")
	}
	var changed map[string]any
	_ = json.Unmarshal(got, &changed)
	if changed["instructions"] != origin.instructions || len(changed["input"].([]any)) != 1 {
		t.Fatalf("wrong prepared prompt: %s", got)
	}
	if changed["prompt_cache_key"] == "the-thread" {
		t.Fatalf("cache key was not isolated: %s", got)
	}
	// The same instructions with the developer item retained must not
	// share a cache namespace with the version that drops that item.
	var original map[string]any
	_ = json.Unmarshal(before, &original)
	m["input"] = original["input"]
	withDeveloper, _ := json.Marshal(m)
	kept, accepted := safePreparedCodexChange(before, withDeveloper, origin)
	if !accepted {
		t.Fatal("changing only instructions was refused")
	}
	var keptBody map[string]any
	_ = json.Unmarshal(kept, &keptBody)
	if keptBody["prompt_cache_key"] == changed["prompt_cache_key"] {
		t.Fatal("different prompt prefixes reused a cache namespace")
	}
	got2, ok := safePreparedCodexChange(before, after, origin)
	if !ok || !reflect.DeepEqual(got, got2) {
		t.Fatal("cache key must be stable across attempts")
	}
	// The initial request must not be modified by the validator.
	var raw map[string]any
	_ = json.Unmarshal(before, &raw)
	if raw["instructions"] != "You are Codex." || len(raw["input"].([]any)) != 2 {
		t.Fatal("the original request was mutated")
	}
}

func TestPreparedCodexRejectsPrivilegedEdits(t *testing.T) {
	before, origin := preparedSample(t)
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		meta codexPreparedOrigin
	}{
		{"model", func(m map[string]any) { m["model"] = "other"; m["instructions"] = "custom" }, origin},
		{"stream", func(m map[string]any) { m["stream"] = false; m["instructions"] = "custom" }, origin},
		{"tools", func(m map[string]any) { m["tools"] = []any{}; m["instructions"] = "custom" }, origin},
		{"cache key", func(m map[string]any) { m["prompt_cache_key"] = "other"; m["instructions"] = "custom" }, origin},
		{"arbitrary turn", func(m map[string]any) { m["input"] = []any{}; m["instructions"] = "custom" }, origin},
		{"native developer", func(m map[string]any) { m["input"] = m["input"].([]any)[1:]; m["instructions"] = "custom" }, codexPreparedOrigin{}},
		{"empty prompt", func(m map[string]any) { m["instructions"] = "" }, origin},
		{"remove prompt", func(m map[string]any) { delete(m, "instructions") }, origin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(before, &m)
			tc.edit(m)
			after, _ := json.Marshal(m)
			got, ok := safePreparedCodexChange(before, after, tc.meta)
			if ok || !reflect.DeepEqual(got, before) {
				t.Fatalf("forbidden edit accepted: %s", got)
			}
		})
	}
}

func TestPreparedCodexRequiresCacheKey(t *testing.T) {
	before, origin := preparedSample(t)
	var m map[string]any
	_ = json.Unmarshal(before, &m)
	delete(m, "prompt_cache_key")
	initial, _ := json.Marshal(m)
	m["instructions"] = "custom"
	after, _ := json.Marshal(m)
	if _, ok := safePreparedCodexChange(initial, after, origin); ok {
		t.Fatal("a keyless session was modified without isolation")
	}
}

func TestGeneratedDeveloperProvenance(t *testing.T) {
	before, origin := preparedSample(t)
	var m map[string]any
	_ = json.Unmarshal(before, &m)
	first := m["input"].([]any)[0]
	if !generatedDeveloper(first, origin.instructions) {
		t.Fatal("synthetic developer not recognized")
	}
	if generatedDeveloper(first, "not Pi") || generatedDeveloper(m["input"].([]any)[1], origin.instructions) {
		t.Fatal("unrelated developer or user item misidentified")
	}
}
