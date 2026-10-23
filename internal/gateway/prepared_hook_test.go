package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

func TestPreparedCodexHookOnlyOnSubscription(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","instructions":"unchanged"}`)
	for _, p := range []provider.Provider{
		{ID: "openai"},
		{ID: "plugin", Account: &provider.Account{Agent: "plugin"}},
		{ID: "codex", Account: &provider.Account{Agent: "codex"}},
	} {
		got := preparedCodexHook(context.Background(), p, provider.Chat, body)
		if !bytes.Equal(got, body) {
			t.Fatalf("%s chat request was modified: %s", p.ID, got)
		}
	}
}

func TestPreparedCodexHookReachesSignedWire(t *testing.T) {
	codexSignedIn(t)
	restore := provider.CodexZstdForTests(true)
	defer restore()
	t.Setenv("MAGPIE_BUN", testenv.Bun(t))
	t.Cleanup(plugin.Settle)

	hook := filepath.Join(t.TempDir(), "prepared.hooks.mjs")
	source := `export function onPreparedRequest(body, ctx) {
  if (ctx.provider !== "codex" || ctx.protocol !== "responses") throw new Error("wrong route")
  const first = body.input?.[0]
  if (first?.role !== "developer" || body.instructions === "Local instructions") throw new Error("not adapted")
  return {...body, instructions:first.content[0].text, input:body.input.slice(1), prompt_cache_key:"after-hook"}
}`
	if err := os.WriteFile(hook, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.Add(t.Context(), hook); err != nil {
		t.Fatal(err)
	}

	type sentRequest struct {
		body []byte
		head http.Header
	}
	sent := make(chan sentRequest, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		body := wire
		if r.Header.Get("Content-Encoding") != "zstd" {
			t.Errorf("request not compressed: %v", r.Header)
		} else {
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Errorf("zstd reader: %v", err)
			} else {
				if decoded, err := dec.DecodeAll(wire, nil); err != nil {
					t.Errorf("decode upstream body: %v", err)
				} else {
					body = decoded
				}
				dec.Close()
			}
		}
		sent <- sentRequest{body: body, head: r.Header.Clone()}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(`data: {"type":"response.completed","response":{"id":"r1","status":"completed","output":[]}}`))
	}))
	defer up.Close()
	oldBase := provider.CodexBase
	provider.CodexBase = up.URL + "/backend-api/codex"
	defer func() { provider.CodexBase = oldBase }()

	p, err := provider.Find("codex")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"model":"gpt-5.5","instructions":"Local instructions","prompt_cache_key":"before-hook","input":"hello"}`)
	res, err := New().forward(t.Context(), *p, provider.Responses, "/responses", p.Prepare(original), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upstream status %d", res.StatusCode)
	}
	got := <-sent
	var body struct {
		Instructions string            `json:"instructions"`
		Key          string            `json:"prompt_cache_key"`
		Input        []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Instructions != "Local instructions" || body.Key != "after-hook" || len(body.Input) != 1 {
		t.Fatalf("prepared hook did not reach upstream: %s", got.body)
	}
	if got.head.Get("session-id") != "after-hook" || got.head.Get("thread-id") != "after-hook" || got.head.Get("Authorization") == "" {
		t.Fatalf("signing ignored prepared body: %v", got.head)
	}
}
