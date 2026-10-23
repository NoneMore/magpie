package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/provider"
)

type codexPreparedOrigin struct {
	instructions string
	injected     bool
}

type codexPreparedKey struct{}

// forwardPrepared retains the unadapted instructions only in request-local
// context, so a privileged hook can remove precisely the developer item
// added by codexBody without ever guessing based on the item's role.
func (s *Server) forwardPrepared(ctx context.Context, p provider.Provider, to provider.Protocol, path string, body []byte, in http.Header) (*http.Response, error) {
	prepared := p.Prepare(body)
	if p.Account != nil && p.Account.Agent == "codex" && to == provider.Responses {
		own := gjson.GetBytes(body, "instructions").String()
		top := gjson.GetBytes(prepared, "instructions").String()
		var m struct {
			Input []any `json:"input"`
		}
		_ = json.Unmarshal(prepared, &m)
		origin := codexPreparedOrigin{instructions: own}
		origin.injected = own != "" && top != own && len(m.Input) > 0 && generatedDeveloper(m.Input[0], own)
		ctx = context.WithValue(ctx, codexPreparedKey{}, origin)
	}
	return s.forward(ctx, p, to, path, prepared, in)
}

func generatedDeveloper(raw any, own string) bool {
	item, ok := raw.(map[string]any)
	if !ok || len(item) != 3 || item["type"] != "message" || item["role"] != "developer" {
		return false
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) != 1 {
		return false
	}
	text, ok := content[0].(map[string]any)
	return ok && len(text) == 2 && text["type"] == "input_text" && text["text"] == own
}

func decodePrepared(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, fmt.Errorf("not a JSON object")
	}
	return m, nil
}

// safePreparedCodexChange accepts only top-level instructions and the removal
// of the one known synthetic developer item. Never take plugin-supplied model,
// tools, role edits, cache keys, or an arbitrary deletion of conversation items.
func safePreparedCodexChange(before, after []byte, origin codexPreparedOrigin) ([]byte, bool) {
	old, err := decodePrepared(before)
	if err != nil {
		return before, false
	}
	next, err := decodePrepared(after)
	if err != nil {
		return before, false
	}
	value, ok := next["instructions"].(string)
	if !ok || strings.TrimSpace(value) == "" || len(value) > 256<<10 {
		return before, false
	}
	oldInput, ok := old["input"].([]any)
	if !ok {
		return before, false
	}
	nextInput, ok := next["input"].([]any)
	if !ok {
		return before, false
	}
	if !reflect.DeepEqual(oldInput, nextInput) {
		if !origin.injected || len(oldInput) == 0 ||
			!generatedDeveloper(oldInput[0], origin.instructions) ||
			!reflect.DeepEqual(oldInput[1:], nextInput) {
			return before, false
		}
	}
	// Model, streaming, tools, reasoning and all other fields are immutable.
	for _, k := range []string{"instructions", "input"} {
		delete(old, k)
		delete(next, k)
	}
	if !reflect.DeepEqual(old, next) {
		return before, false
	}
	if gjson.GetBytes(before, "instructions").String() == value && reflect.DeepEqual(oldInput, nextInput) {
		return before, false
	}
	key := gjson.GetBytes(before, "prompt_cache_key").String()
	if key == "" {
		return before, false
	}
	// Use the actual validated plugin body, but mint a separate deterministic
	// cache namespace. Codex signing reads this updated key afterward.
	var changed map[string]any
	if changed, err = decodePrepared(after); err != nil {
		return before, false
	}
	// A removed synthetic developer message changes the prompt prefix too.
	// Partition that case from a hook that changes only instructions.
	mode := "keep"
	if !reflect.DeepEqual(oldInput, nextInput) {
		mode = "drop"
	}
	seed := sha256.Sum256([]byte("magpie-prepared-v1\x00" + key + "\x00" + value + "\x00" + mode))
	h := hex.EncodeToString(seed[:])
	changed["prompt_cache_key"] = h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	out, err := json.Marshal(changed)
	if err != nil {
		return before, false
	}
	return out, true
}

func preparedCodexHook(ctx context.Context, p provider.Provider, to provider.Protocol, body []byte) []byte {
	if p.Account == nil || p.Account.Agent != "codex" || to != provider.Responses || plugin.PreparedHookSpec() == "" || len(body) > 16<<20 {
		return body
	}
	origin, _ := ctx.Value(codexPreparedKey{}).(codexPreparedOrigin)
	out := plugin.GatewayPrepared(ctx, body, plugin.PreparedContext{
		Provider: p.ID, Protocol: string(to),
		Model:             gjson.GetBytes(body, "model").String(),
		InjectedDeveloper: origin.injected,
	})
	if bytes.Equal(out, body) {
		return body
	}
	if safe, ok := safePreparedCodexChange(body, out, origin); ok {
		return safe
	}
	return body
}
