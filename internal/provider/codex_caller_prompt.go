package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yetone/magpie/internal/settings"
)

// The personal fork's caller-owned Codex prompt policy lives away from the
// upstream request conversion. Codex's native signing/serialization remains
// in codex_request.go; opt-in must preserve the cache/session partition.
func codexCallerEnabled(instructions string) bool {
	return settings.Shared().CodexCallerPrompt && strings.TrimSpace(instructions) != ""
}

// The request is signed after body preparation; rewrite only keyless caller
// sessions and only once. Native Codex passthrough never uses this policy.
func signCodexCallerHeaders(req *http.Request, body []byte) {
	if !settings.Shared().CodexCallerPrompt ||
		req.Context().Value(codexNativePassthroughKey{}) != nil ||
		req.Context().Value(codexCallerSessionSignedKey{}) != nil {
		return
	}
	if callerSessionHeaders(req.Header, body) {
		*req = *req.WithContext(context.WithValue(req.Context(), codexCallerSessionSignedKey{}, true))
	}
}

// Keep the two prompt policies' caches distinct even when the caller gave
// an explicit cache key; the mapping is deterministic across turns.
func codexCallerCache(body map[string]any) {
	if key, _ := body["prompt_cache_key"].(string); key != "" {
		body["prompt_cache_key"] = callerCacheKey(key)
	}
}

type codexCallerSessionSignedKey struct{}

// codexNativePassthroughKey marks a native Codex relay. It does not go through
// codexBody, so its existing session and thread IDs must not be rewritten.
type codexNativePassthroughKey struct{}

// WithNativeCodexPassthrough marks a request relayed without codexBody.
func WithNativeCodexPassthrough(ctx context.Context) context.Context {
	return context.WithValue(ctx, codexNativePassthroughKey{}, true)
}

// callerCacheKey isolates the caller-owned prompt's cache without changing
// its identity between turns.
func callerCacheKey(key string) string {
	hash := sha256.Sum256([]byte("codex-caller-prompt\x00" + key))
	id := hex.EncodeToString(hash[:])
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32]
}

// callerSessionHeaders partitions a caller-owned, keyless request's existing
// session identity. Without a cache key, codexBody cannot use the session
// headers because it only receives the body. This runs after sessionHeaders
// and never fabricates a session for requests with no session headers.
func callerSessionHeaders(h http.Header, body []byte) bool {
	var v struct {
		Key          string `json:"prompt_cache_key"`
		Instructions string `json:"instructions"`
		Model        string `json:"model"`
	}
	if json.Unmarshal(body, &v) != nil || v.Key != "" || strings.TrimSpace(v.Instructions) == "" ||
		v.Instructions == codexInstructions(v.Model) {
		// An empty original caller prompt was already replaced by the
		// native one, and must retain the native session identity.
		return false
	}
	// Preserve distinct session and thread identities for callers that
	// supplied both; unlike body keys, their two headers need not agree.
	changed := false
	for _, name := range []string{"session-id", "thread-id"} {
		if key := h.Get(name); key != "" {
			h.Set(name, callerCacheKey(key))
			changed = true
		}
	}
	return changed
}

