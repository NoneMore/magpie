//go:build lm_detector_e2e

package detector_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

// Run only from the LM Detector workflow. No real model account or key is needed.
func TestMain(m *testing.M) {
	testenv.Offline()
	testenv.Main(m)
}

type detectedJob struct {
	JobID     string `json:"jobId"`
	State     string `json:"state"`
	Completed int    `json:"completed"`
	Error     string `json:"error"`
	Samples   []struct {
		State                 string `json:"state"`
		MinimumValidNumber    *int   `json:"minimumValidNumber"`
		AnomalousDistribution bool   `json:"anomalousDistribution"`
		StartedAt             *int64 `json:"startedAt"`
		DurationMs            *int64 `json:"durationMs"`
		Throughput            *struct {
			TTFTMs float64 `json:"ttftMs"`
		} `json:"throughput"`
	} `json:"samples"`
	Rounds []struct {
		Analysis *struct {
			Decision string `json:"decision"`
		} `json:"analysis"`
	} `json:"rounds"`
}

func TestLMDetectorGatewayBunIntegration(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Fatal("Bun is required for this integration test")
	}
	t.Setenv("MAGPIE_BUN", bun)
	t.Cleanup(plugin.Settle)

	// The upstream is the only fake: the Gateway, plugin loader, Bun host and
	// plugin tools all run their real implementations over HTTP / IPC.
	var mu sync.Mutex
	requests := map[string]int{}
	sequence := strings.Repeat("200, ", 400)
	plain := strings.Repeat("199, ", 300)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if json.Unmarshal(body, &req) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		kind := "chat"
		if r.URL.Path == "/v1/responses" {
			kind = "responses"
		} else if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if req.Model != kind+"-fixture" || r.Header.Get("Authorization") != "Bearer upstream-test-key" {
			http.Error(w, "wrong model, route or upstream key", http.StatusBadRequest)
			return
		}
		key := fmt.Sprintf("%s/%t", kind, req.Stream)
		mu.Lock()
		requests[key]++
		mu.Unlock()
		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			if kind == "chat" {
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"finish_reason": "stop", "message": map[string]any{"content": plain},
				}}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{
					map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": plain}}},
				}})
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if kind == "chat" {
			data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"content": sequence},
			}}})
			fmt.Fprintf(w, "data: %s\n\n", data)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			data, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": sequence})
			fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", data)
			fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[]}}\n\n")
		}
	}))
	defer upstream.Close()

	if err := provider.Save(provider.Provider{
		ID: "fpdci", Name: "LM Detector test", Key: "upstream-test-key",
		Models: []string{"chat-fixture", "responses-fixture"},
		Chat: upstream.URL + "/v1", Responses: upstream.URL + "/v1",
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"chat", "responses"} {
		if err := provider.SetModelAPI("fpdci/"+p+"-fixture", p); err != nil {
			t.Fatal(err)
		}
	}
	gw := httptest.NewServer(gateway.New().Handler())
	defer gw.Close()
	t.Setenv("MAGPIE_FPD_GATEWAY_URL", gw.URL+"/v1")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules", "lmfpd", "package.json")); err != nil {
		t.Fatalf("reference-data package must be installed before the integration test: %v", err)
	}
	added, err := plugin.Add(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := plugin.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, item := range tools {
		if item.Spec == added.Spec {
			for _, entry := range item.Tools {
				names = append(names, entry.Name)
			}
		}
	}
	for _, name := range []string{"models", "start", "status", "cancel"} {
		if !slices.Contains(names, name) {
			t.Fatalf("Bun host did not register tool %q (got %v)", name, names)
		}
	}

	run := func(name string, args any, dst any) {
		t.Helper()
		encoded, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		result, err := plugin.RunTool(ctx, added.Spec, name, encoded, "magpie-page")
		if err != nil {
			t.Fatalf("%s tool: %v", name, err)
		}
		if err := json.Unmarshal([]byte(result), dst); err != nil {
			t.Fatalf("decode %s tool output %q: %v", name, result, err)
		}
	}
	var listed struct {
		Models []string `json:"models"`
	}
	run("models", map[string]any{}, &listed)
	for _, model := range []string{"fpdci/chat-fixture", "fpdci/responses-fixture"} {
		if !slices.Contains(listed.Models, model) {
			t.Fatalf("Gateway did not expose %q: %v", model, listed.Models)
		}
	}

	for _, kind := range []string{"chat", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", kind, stream), func(t *testing.T) {
				var job detectedJob
				run("start", map[string]any{
					"model": "fpdci/" + kind + "-fixture", "format": kind,
					"stream": stream, "count": 1, "parallel": 1, "repeat": 1,
				}, &job)
				if job.JobID == "" {
					t.Fatal("no job ID from start")
				}
				end := time.Now().Add(20 * time.Second)
				for job.State == "running" && time.Now().Before(end) {
					time.Sleep(50 * time.Millisecond)
					run("status", map[string]any{"jobId": job.JobID}, &job)
				}
				if job.State != "done" || job.Completed != 1 || len(job.Rounds) != 1 || job.Rounds[0].Analysis == nil || job.Rounds[0].Analysis.Decision != "not_model" {
					t.Fatalf("unexpected result: %+v", job)
				}
				if len(job.Samples) != 1 || job.Samples[0].MinimumValidNumber == nil {
					t.Fatalf("missing diagnostics: %+v", job.Samples)
				}
				if job.Samples[0].StartedAt == nil || job.Samples[0].DurationMs == nil {
					t.Fatalf("missing request timestamps: %+v", job.Samples[0])
				}
				if stream && job.Samples[0].Throughput == nil {
					t.Fatalf("streaming sample has no visible-text TTFT: %+v", job.Samples[0])
				}
				if !stream && job.Samples[0].Throughput != nil {
					t.Fatalf("JSON sample must not fabricate stream throughput: %+v", job.Samples[0])
				}
				wantMinimum, wantWarning := 199, false
				if stream {
					wantMinimum, wantWarning = 200, true
				}
				if *job.Samples[0].MinimumValidNumber != wantMinimum || job.Samples[0].AnomalousDistribution != wantWarning {
					t.Fatalf("sample diagnostics mismatch: %+v", job.Samples[0])
				}
				mu.Lock()
				calls := requests[fmt.Sprintf("%s/%t", kind, stream)]
				mu.Unlock()
				if calls != 1 {
					t.Fatalf("upstream got %d calls, want 1", calls)
				}
			})
		}
	}
}
