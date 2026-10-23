// Package fingerprint runs lm-detector's complete, version-pinned scoring
// engine for the built-in Fingerprint page. It never handles upstream provider
// credentials: every probe goes through the user's Magpie Gateway.
//
// The engine is installed in Bun's private cache on first use. The model
// selection, job lifecycle and results are owned by Magpie, not the CLI UI.
package fingerprint

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yetone/magpie/internal/plugin"
	"github.com/yetone/magpie/internal/settings"
)

// Pin a published lmfpd build rather than allowing 'latest' to silently
// change the candidate bank, calibration or prompt generator.
const EngineVersion = "0.0.1790172702647"
const maxJobs = 40
const maxActive = 2
const maxOutput = 8 << 20
const historyFile = "fingerprint-history.json"

type Job struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	State      string          `json:"state"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	Analysis   json.RawMessage `json:"analysis,omitempty"`
	Samples    json.RawMessage `json:"samples,omitempty"`
	Error      string          `json:"error,omitempty"`
	cancel     context.CancelFunc
}

var manager struct {
	sync.Mutex
	jobs []*Job
	once sync.Once
}

func readHistory() {
	b, err := os.ReadFile(filepath.Join(settings.Dir(), historyFile))
	if err != nil || len(b) > maxOutput {
		return
	}
	var jobs []*Job
	if json.Unmarshal(b, &jobs) != nil {
		return
	}
	for _, j := range jobs {
		if j.State == "running" || j.State == "preparing" {
			j.State = "failed"
			j.Error = "Magpie restarted before this detection finished."
			now := time.Now()
			j.FinishedAt = &now
		}
	}
	manager.jobs = jobs
	trimLocked()
}

func trimLocked() {
	if len(manager.jobs) > maxJobs {
		manager.jobs = manager.jobs[:maxJobs]
	}
}

func saveLocked() {
	data, err := json.MarshalIndent(manager.jobs, "", "  ")
	if err != nil || len(data) > maxOutput {
		return
	}
	dir := settings.Dir()
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	f, err := os.CreateTemp(dir, "fingerprint-*.json")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	_ = f.Chmod(0o600)
	if _, err := f.Write(data); err != nil {
		return
	}
	if f.Sync() != nil || f.Close() != nil {
		return
	}
	_ = os.Rename(f.Name(), filepath.Join(dir, historyFile))
}

func initHistory() {
	manager.once.Do(readHistory)
}

func snapshot(j *Job) Job {
	copy := *j
	copy.cancel = nil
	return copy
}

func Jobs() []Job {
	manager.Lock()
	defer manager.Unlock()
	initHistory()
	out := make([]Job, 0, len(manager.jobs))
	for _, j := range manager.jobs {
		out = append(out, snapshot(j))
	}
	return out
}

func activeLocked() int {
	n := 0
	for _, j := range manager.jobs {
		if j.State == "running" || j.State == "preparing" {
			n++
		}
	}
	return n
}

func validModel(model string) bool {
	if len(model) < 3 || len(model) > 256 || strings.TrimSpace(model) != model || !strings.Contains(model, "/") {
		return false
	}
	for _, c := range model {
		if c <= 32 || c == 127 {
			return false
		}
	}
	return true
}

func gateway() (string, string, error) {
	raw := strings.TrimSpace(os.Getenv("MAGPIE_FPD_GATEWAY_URL"))
	if raw == "" {
		port := settings.Load().Port
		if port <= 0 || port > 65535 {
			port = 3425
		}
		raw = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("invalid Gateway URL")
	}
	key := os.Getenv("MAGPIE_FPD_GATEWAY_KEY")
	if key == "" {
		switch u.Hostname() {
		case "127.0.0.1", "localhost", "::1":
			key = "magpie"
		default:
			return "", "", errors.New("a remote Gateway needs MAGPIE_FPD_GATEWAY_KEY")
		}
	}
	base := strings.TrimRight(u.String(), "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base, key, nil
}

var httpClient = &http.Client{
	Timeout: 9 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Gateway redirects are refused")
	},
}

func Models(ctx context.Context) ([]string, error) {
	base, key, err := gateway()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Gateway model listing: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("Gateway model listing HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&envelope); err != nil {
		return nil, errors.New("invalid Gateway model listing")
	}
	models := make([]string, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		if validModel(item.ID) && !slices.Contains(models, item.ID) {
			models = append(models, item.ID)
		}
		if len(models) == 2000 {
			break
		}
	}
	slices.Sort(models)
	return models, nil
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func Start(model string) (Job, error) {
	if !validModel(model) {
		return Job{}, errors.New("choose an exact provider/model ID")
	}
	manager.Lock()
	defer manager.Unlock()
	initHistory()
	if activeLocked() >= maxActive {
		return Job{}, errors.New("two fingerprint runs are already active")
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{
		ID: newID(), Model: model, State: "preparing",
		StartedAt: time.Now(), cancel: cancel,
	}
	manager.jobs = append([]*Job{j}, manager.jobs...)
	trimLocked()
	saveLocked()
	go execute(ctx, j.ID)
	return snapshot(j), nil
}

func finish(id, state, message string, analysis, samples json.RawMessage) {
	manager.Lock()
	defer manager.Unlock()
	for _, j := range manager.jobs {
		if j.ID != id {
			continue
		}
		if j.State == "cancelled" {
			return
		}
		j.State = state
		j.Error = message
		j.Analysis = analysis
		j.Samples = samples
		now := time.Now()
		j.FinishedAt = &now
		j.cancel = nil
		saveLocked()
		return
	}
}

type limitedBuffer struct{ bytes.Buffer }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.Len()+len(p) > maxOutput {
		return 0, errors.New("fingerprint report exceeded 8 MiB")
	}
	return l.Buffer.Write(p)
}

func execute(ctx context.Context, id string) {
	manager.Lock()
	var model string
	for _, j := range manager.jobs {
		if j.ID == id {
			model = j.Model
			break
		}
	}
	manager.Unlock()
	if model == "" {
		return
	}
	if ctx.Err() != nil {
		return
	}
	models, err := Models(ctx)
	if err != nil {
		finish(id, "failed", err.Error(), nil, nil)
		return
	}
	if !slices.Contains(models, model) {
		finish(id, "failed", "model is not currently served by Gateway", nil, nil)
		return
	}
	base, key, err := gateway()
	if err != nil {
		finish(id, "failed", err.Error(), nil, nil)
		return
	}
	bun, err := plugin.Bun(ctx)
	if err != nil {
		finish(id, "failed", "Bun runtime unavailable: "+err.Error(), nil, nil)
		return
	}
	manager.Lock()
	for _, j := range manager.jobs {
		if j.ID == id && j.State == "preparing" {
			j.State = "running"
			saveLocked()
			break
		}
	}
	manager.Unlock()
	if ctx.Err() != nil {
		return
	}
	// bunx downloads the pinned published engine into Bun's cache on first
	// use. The engine includes the reference bank and trained detector.
	// Never put the Gateway key on the process command line or in results.
	cmd := exec.CommandContext(ctx, bun, "x", "--bun", "lmfpd@"+EngineVersion,
		"--model", model, "--baseurl", base, "--api", "responses",
		"--count", "3", "--parallel", "3", "--strict", "--no-stream",
		"--json", "--no-update-check", "--timeout", "180")
	cmd.Env = append(os.Environ(), "API_KEY="+key, "FPD_NO_UPDATE_CHECK=1")
	var out limitedBuffer
	var stderr limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		why := strings.TrimSpace(stderr.String())
		if len(why) > 280 {
			why = why[:280]
		}
		why = strings.ReplaceAll(why, key, "[REDACTED]")
		if why == "" {
			why = err.Error()
		}
		finish(id, "failed", why, nil, nil)
		return
	}
	var result struct {
		Schema   string          `json:"schema"`
		Analysis json.RawMessage `json:"analysis"`
		Rounds   []struct {
			Analysis json.RawMessage `json:"analysis"`
			Samples  json.RawMessage `json:"samples"`
		} `json:"rounds"`
	}
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Schema != "fpd-detection-v1" {
		finish(id, "failed", "invalid lm-detector JSON report", nil, nil)
		return
	}
	var samples json.RawMessage
	if len(result.Rounds) > 0 {
		samples = result.Rounds[0].Samples
		if len(result.Analysis) == 0 || string(result.Analysis) == "null" {
			result.Analysis = result.Rounds[0].Analysis
		}
	}
	if len(result.Analysis) == 0 || string(result.Analysis) == "null" {
		finish(id, "failed", "lm-detector could not score these samples", nil, samples)
		return
	}
	finish(id, "done", "", result.Analysis, samples)
}

func Cancel(id string) (Job, error) {
	manager.Lock()
	defer manager.Unlock()
	initHistory()
	for _, j := range manager.jobs {
		if j.ID != id {
			continue
		}
		if j.State == "preparing" || j.State == "running" {
			j.State = "cancelled"
			if j.cancel != nil {
				j.cancel()
			}
			j.cancel = nil
			now := time.Now()
			j.FinishedAt = &now
			saveLocked()
		}
		return snapshot(j), nil
	}
	return Job{}, errors.New("fingerprint run not found")
}

func StopAll() {
	manager.Lock()
	defer manager.Unlock()
	initHistory()
	for _, j := range manager.jobs {
		if j.State == "preparing" || j.State == "running" {
			if j.cancel != nil {
				j.cancel()
			}
			j.cancel = nil
			j.State = "cancelled"
			now := time.Now()
			j.FinishedAt = &now
		}
	}
	saveLocked()
}
