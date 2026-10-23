package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

// The Codex agent's setting must work without restarting magpie, persist
// across unrelated settings saves, and explicitly override the old env flag.
func TestCodexCallerPromptPreferenceAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("MAGPIE_CODEX_CALLER_PROMPT", "1")

	h := Handler(nil, nil)
	var state struct {
		On     bool  `json:"codexCallerPromptOn"`
		Choice *bool `json:"codexCallerPrompt"`
	}
	call := func(method, path, body string) int {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code
	}
	if code := call("GET", "/api/settings", ""); code != http.StatusOK || !state.On || state.Choice != nil {
		t.Fatalf("env fallback: status %d state %+v", code, state)
	}
	if code := call("POST", "/api/settings/codex-caller-prompt", `{"on":false}`); code != http.StatusOK ||
		state.On || state.Choice == nil || *state.Choice {
		t.Fatalf("explicit off: status %d state %+v", code, state)
	}
	if settings.CodexCallerPromptOn() {
		t.Fatal("environment overrode explicit UI off")
	}
	if code := call("POST", "/api/settings", `{"theme":"dark"}`); code != http.StatusOK ||
		state.On || state.Choice == nil || *state.Choice {
		t.Fatalf("unrelated save reset opt-out: status %d state %+v", code, state)
	}
	if code := call("POST", "/api/settings/codex-caller-prompt", `{}`); code != http.StatusBadRequest {
		t.Fatalf("missing on: status %d", code)
	}
	t.Setenv("MAGPIE_CODEX_CALLER_PROMPT", "0")
	if code := call("POST", "/api/settings/codex-caller-prompt", `{"on":true}`); code != http.StatusOK ||
		!state.On || state.Choice == nil || !*state.Choice {
		t.Fatalf("explicit on: status %d state %+v", code, state)
	}
	if !settings.CodexCallerPromptOn() {
		t.Fatal("explicit UI on did not override environment")
	}
	if code := call("GET", "/api/settings", ""); code != http.StatusOK || !state.On || state.Choice == nil || !*state.Choice {
		t.Fatalf("saved state not reloaded: status %d state %+v", code, state)
	}
}
