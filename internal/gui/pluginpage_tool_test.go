package gui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Neither an on Page that omitted tool:run nor a switched-off plugin
// can call a Bun tool, including through a direct HTTP request.
func TestPluginPageToolRequiresPermission(t *testing.T) {
	on, off := pagePlugins(t)
	h := pageMux()
	for _, spec := range []string{on, off, "/unknown-plugin"} {
		body, err := json.Marshal(map[string]any{
			"spec": spec, "name": "models", "args": map[string]any{},
		})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/plugin-pages/tool", bytes.NewReader(body))
		w := do(h, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("spec %q: expected 403, got %d: %s", spec, w.Code, w.Body.String())
		}
	}
}
