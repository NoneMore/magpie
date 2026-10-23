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

func TestCodexCallerPromptPreferenceAPI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	h := Handler(nil, nil)
	var state struct {
		On bool `json:"codexCallerPrompt"`
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
	if code := call("GET", "/api/settings", ""); code != http.StatusOK || state.On {
		t.Fatalf("default: status %d state %+v", code, state)
	}
	if code := call("POST", "/api/settings/codex-caller-prompt", `{"on":true}`); code != http.StatusOK || !state.On {
		t.Fatalf("enable: status %d state %+v", code, state)
	}
	if code := call("POST", "/api/settings", `{"theme":"dark"}`); code != http.StatusOK || !state.On {
		t.Fatalf("unrelated save reset preference: status %d state %+v", code, state)
	}
	if code := call("POST", "/api/settings/codex-caller-prompt", `{}`); code != http.StatusBadRequest {
		t.Fatalf("missing on: status %d", code)
	}
	if code := call("POST", "/api/settings/codex-caller-prompt", `{"on":false}`); code != http.StatusOK || state.On {
		t.Fatalf("disable: status %d state %+v", code, state)
	}
	if code := call("GET", "/api/settings", ""); code != http.StatusOK || state.On || settings.Load().CodexCallerPrompt {
		t.Fatalf("saved off state: status %d state %+v", code, state)
	}
}
