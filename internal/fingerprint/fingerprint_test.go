package fingerprint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelIDsMustBeExactProviderModels(t *testing.T) {
	for _, model := range []string{"provider/model", "openai/gpt-6", "vendor/a:b", "vendor/path/model"} {
		if !validModel(model) {
			t.Errorf("expected valid model: %q", model)
		}
	}
	for _, model := range []string{"", "model-only", "/model", " provider/model", "vendor/model\nbad", "vendor/" + strings.Repeat("x", 255)} {
		if validModel(model) {
			t.Errorf("accepted invalid model: %q", model)
		}
	}
}

func TestGatewayModelListOnlyReturnsIDs(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/z","secret":"DO_NOT_EXPOSE"},{"id":"vendor/a"},{"id":"vendor/z"},{"id":"model-without-provider"}]}`))
	}))
	defer s.Close()
	t.Setenv("MAGPIE_FPD_GATEWAY_URL", s.URL)
	t.Setenv("MAGPIE_FPD_GATEWAY_KEY", "test-key")
	models, err := Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "vendor/a" || models[1] != "vendor/z" {
		t.Fatalf("unexpected model list: %v", models)
	}
}

func TestGatewayRejectsRemoteWithoutExplicitKey(t *testing.T) {
	t.Setenv("MAGPIE_FPD_GATEWAY_URL", "https://vendor.example.invalid")
	t.Setenv("MAGPIE_FPD_GATEWAY_KEY", "")
	if _, _, err := gateway(); err == nil {
		t.Fatal("should refuse using Magpie's default local key remotely")
	}
}

func TestGatewayRejectsEmbeddedCredentials(t *testing.T) {
	t.Setenv("MAGPIE_FPD_GATEWAY_URL", "http://user:secret@127.0.0.1:3425")
	t.Setenv("MAGPIE_FPD_GATEWAY_KEY", "")
	if _, _, err := gateway(); err == nil {
		t.Fatal("accepted credentials inside Gateway URL")
	}
}
