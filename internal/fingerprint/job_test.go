package fingerprint

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"net/http"
	"net/http/httptest"
)

// The published classifier is exercised by the CI smoke test. This local
// integration test instead isolates the Gateway/job/JSON contract, including
// job persistence, without paying for real model requests.
func TestJobRunsAgainstGatewayAndCollectsClassifierJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub uses /bin/sh; native Windows builds are checked in CI")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer local-test-key" {
			t.Errorf("invalid request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
			http.Error(w, "invalid request", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"test/model"}]}`))
	}))
	defer server.Close()
	t.Setenv("MAGPIE_FPD_GATEWAY_URL", server.URL)
	t.Setenv("MAGPIE_FPD_GATEWAY_KEY", "local-test-key")
	bun := filepath.Join(home, "fake-bun")
	script := "#!/bin/sh\n" +
		"printf '%s' '{\"schema\":\"fpd-detection-v1\",\"analysis\":{\"prediction_name\":\"reference\",\"probability\":0.95,\"results\":[{\"model\":\"reference\",\"display_name\":\"Reference\",\"probability\":0.95}]},\"rounds\":[{\"samples\":[{\"state\":\"done\"}]}]}'\n"
	if err := os.WriteFile(bun, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGPIE_BUN", bun)

	job, err := Start("test/model")
	if err != nil {
		t.Fatal(err)
	}
	if job.ID == "" || job.State != "preparing" {
		t.Fatalf("start: %+v", job)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		all := Jobs()
		for _, j := range all {
			if j.ID != job.ID {
				continue
			}
			if j.State == "failed" {
				t.Fatalf("detector failed: %+v", j)
			}
			if j.State == "done" {
				if !strings.Contains(string(j.Analysis), `"prediction_name":"reference"`) {
					t.Fatalf("lost classifier result: %s", j.Analysis)
				}
				if strings.Contains(string(j.Analysis), "local-test-key") || j.FinishedAt == nil {
					t.Fatalf("leaked key or lost completion: %+v", j)
				}
				if _, err := os.Stat(filepath.Join(home, ".config", "magpie", historyFile)); err != nil {
					t.Fatalf("history was not saved: %v", err)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}
