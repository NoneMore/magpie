//go:build lm_detector_e2e

package detector_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/lmdetector"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

// This test exercises the real Magpie Gateway with the native Go SDK.
// No Bun process, external model provider or paid key is involved.
func TestMain(m *testing.M) {
	testenv.Offline()
	testenv.Main(m)
}

func TestLMDetectorNativeGatewayIntegration(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	sequence := strings.Repeat("200, ", 400)
	plain := strings.Repeat("199, ", 300)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "expected POST", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Model string `json:"model"`
			Stream bool `json:"stream"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil {
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
		if in.Model != kind+"-fixture" || r.Header.Get("Authorization") != "Bearer upstream-test-key" {
			http.Error(w, "wrong model, route or upstream key", http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests[fmt.Sprintf("%s/%t", kind, in.Stream)]++
		mu.Unlock()
		if !in.Stream {
			w.Header().Set("Content-Type", "application/json")
			if kind == "chat" {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"finish_reason": "stop", "message": map[string]any{"content": plain},
				}}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "output": []any{
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
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		} else {
			data, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": sequence})
			_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\n", data)
			_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
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
	for _, kind := range []string{"chat", "responses"} {
		if err := provider.SetModelAPI("fpdci/"+kind+"-fixture", kind); err != nil {
			t.Fatal(err)
		}
	}
	gw := httptest.NewServer(gateway.New().Handler())
	defer gw.Close()
	t.Setenv("MAGPIE_FPD_GATEWAY_URL", gw.URL+"/v1")
	t.Setenv("MAGPIE_FPD_GATEWAY_KEY", "magpie")
	lmdetector.Enable()
	defer lmdetector.Disable()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	models, err := lmdetector.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fpdci/chat-fixture", "fpdci/responses-fixture"} {
		found := false
		for _, got := range models {
			if got == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("Gateway missing direct model %s in %v", name, models)
		}
	}
	for _, kind := range []string{"chat", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", kind, stream), func(t *testing.T) {
				job, err := lmdetector.Start(ctx, lmdetector.Options{
					Model: "fpdci/" + kind + "-fixture", Format: kind,
					Stream: stream, Count: 1, Parallel: 1, Repeat: 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				if job.JobID == "" {
					t.Fatal("missing native job ID")
				}
				deadline := time.Now().Add(20 * time.Second)
				for job.State == "running" && time.Now().Before(deadline) {
					time.Sleep(40 * time.Millisecond)
					job, err = lmdetector.Status(job.JobID)
					if err != nil {
						t.Fatal(err)
					}
				}
				if job.State != "done" || job.Completed != 1 || len(job.Rounds) != 1 ||
					job.Rounds[0].Analysis == nil || job.Rounds[0].Analysis.Decision != "not_model" {
					t.Fatalf("unexpected native result: %+v", job)
				}
				if len(job.Samples) != 1 || job.Samples[0].MinimumValidNumber == nil ||
					job.Samples[0].StartedAt == nil || job.Samples[0].DurationMs == nil {
					t.Fatalf("missing sample diagnostics: %+v", job.Samples)
				}
				minimum, anomaly := 199, false
				if stream {
					minimum, anomaly = 200, true
					if job.Samples[0].FirstByteMs == nil {
						t.Fatalf("missing SSE first-byte time: %+v", job.Samples[0])
					}
				}
				if *job.Samples[0].MinimumValidNumber != minimum ||
					job.Samples[0].AnomalousDistribution != anomaly {
					t.Fatalf("sample distribution differs: %+v", job.Samples[0])
				}
				mu.Lock()
				calls := requests[fmt.Sprintf("%s/%t", kind, stream)]
				mu.Unlock()
				if calls != 1 {
					t.Fatalf("Gateway forwarded %d requests, want one", calls)
				}
			})
		}
	}
}
