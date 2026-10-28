// Package lmdetector adapts the upstream Go SDK to Magpie's native plugin UI.
// It never launches the legacy Bun detector or exposes Gateway credentials to the page.
package lmdetector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/plugin"
)

const maxJobs = 2
const maxSaved = 64

type Options struct {
	Model    string  `json:"model"`
	Format   string  `json:"format"`
	Stream   bool    `json:"stream"`
	Effort   string  `json:"effort"`
	Timeout  float64 `json:"timeout"`
	Count    int     `json:"count"`
	Parallel int     `json:"parallel"`
	Repeat   int     `json:"repeat"`
	Strict   bool    `json:"strict"`
}

type Sample struct {
	Index                 int    `json:"index"`
	State                 string `json:"state"`
	Count                 int    `json:"count"`
	ExpectedCount         int    `json:"expectedCount"`
	MinimumValidNumber    *int   `json:"minimumValidNumber"`
	AnomalousDistribution bool   `json:"anomalousDistribution"`
	StartedAt             *int64 `json:"startedAt"`
	FinishedAt            *int64 `json:"finishedAt"`
	DurationMs            *int64 `json:"durationMs"`
	FirstByteMs           *int64 `json:"firstByteMs"`
	Error                 string `json:"error,omitempty"`
}

type Round struct {
	Round    int             `json:"round"`
	Samples  []Sample        `json:"samples"`
	Analysis *lmfpd.Analysis `json:"analysis"`
	Error    string          `json:"error,omitempty"`
}

type Job struct {
	JobID string `json:"jobId"`
	Options
	State      string          `json:"state"`
	StartedAt  string          `json:"startedAt"`
	FinishedAt *string         `json:"finishedAt"`
	Completed  int             `json:"completed"`
	Round      int             `json:"round"`
	Rounds     []Round         `json:"rounds"`
	Samples    []Sample        `json:"samples"`
	Analysis   *lmfpd.Analysis `json:"analysis"`
	Error      string          `json:"error,omitempty"`
	cancel     context.CancelFunc
}

type manager struct {
	mu       sync.Mutex
	enabled  bool
	starting int
	active   int // Actual execute goroutines; cancelled jobs retain their slot until exit.
	epoch    uint64
	next     uint64
	pending  map[uint64]context.CancelFunc
	jobs     map[string]*Job
	detector *lmfpd.Detector
}

var service = manager{jobs: make(map[string]*Job)}

func Enable() {
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.enabled {
		service.epoch++
	}
	service.enabled = true
}

// Disable cancels active work, and the next API request will be refused.
func Disable() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.enabled = false
	service.epoch++
	for _, cancel := range service.pending {
		cancel() // Also stop Start requests still loading models or references.
	}
	for _, j := range service.jobs {
		if j.State == "running" {
			j.State = "cancelled"
			finished := time.Now().UTC().Format(time.RFC3339Nano)
			j.FinishedAt = &finished
			j.cancel()
		}
	}
}

func Enabled() bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.enabled
}

func id() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func gatewayConfig() (string, string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("MAGPIE_FPD_GATEWAY_URL")), "/")
	if base == "" {
		base = gateway.URL()
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("invalid Gateway URL")
	}
	loopback := false
	if host := u.Hostname(); host == "localhost" {
		loopback = true
	} else if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", "", errors.New("remote Gateway requires HTTPS")
	}
	if !strings.HasSuffix(u.Path, "/v1") {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1"
	}
	key := strings.TrimSpace(os.Getenv("MAGPIE_FPD_GATEWAY_KEY"))
	if key == "" && loopback {
		key = "magpie"
	}
	if key == "" {
		return "", "", errors.New("remote Gateway requires MAGPIE_FPD_GATEWAY_KEY")
	}
	return strings.TrimRight(u.String(), "/"), key, nil
}

func Models(ctx context.Context) ([]string, error) {
	base, key, err := gatewayConfig()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Gateway models: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, errors.New("Gateway response is too large")
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(b, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	for _, v := range payload.Data {
		if !validModel(v.ID) {
			continue
		}
		models = append(models, v.ID)
	}
	slices.Sort(models)
	return slices.Compact(models), nil
}

func validModel(v string) bool {
	if v == "" || strings.HasPrefix(v, "group/") || strings.HasPrefix(v, "routing/") || strings.HasPrefix(v, "auto/") || strings.ContainsAny(v, " \n\t\r") {
		return false
	}
	p, m, ok := strings.Cut(v, "/")
	return ok && p != "" && m != "" && p != "group"
}

func (o *Options) check() error {
	if !validModel(o.Model) {
		return errors.New("select an explicit provider/model, not a routing group")
	}
	if o.Format == "" {
		o.Format = "responses"
	}
	if o.Format != "responses" && o.Format != "chat" {
		return errors.New("invalid API format")
	}
	if len(o.Effort) > 64 || strings.TrimSpace(o.Effort) != o.Effort || strings.ContainsAny(o.Effort, "\r\n") {
		return errors.New("invalid reasoning effort")
	}
	if o.Timeout == 0 {
		o.Timeout = 120
	}
	if o.Timeout < 0.1 || o.Timeout > 600 {
		return errors.New("timeout must be 0.1–600 seconds")
	}
	if o.Count == 0 {
		o.Count = 3
	}
	if o.Count < 1 || o.Count > 3 {
		return errors.New("count must be 1–3")
	}
	if o.Parallel == 0 {
		o.Parallel = 3
	}
	if o.Parallel < 1 || o.Parallel > 3 {
		return errors.New("parallel must be 1–3")
	}
	if o.Parallel > o.Count {
		o.Parallel = o.Count
	}
	if o.Repeat == 0 {
		o.Repeat = 1
	}
	if o.Repeat < 1 || o.Repeat > 20 {
		return errors.New("repeat must be 1–20")
	}
	if o.Strict && o.Count != 3 {
		return errors.New("strict mode requires 3 challenges")
	}
	return nil
}

func reference(ctx context.Context) (*lmfpd.Detector, error) {
	service.mu.Lock()
	d := service.detector
	service.mu.Unlock()
	if d != nil {
		return d, nil
	}
	dir, err := plugin.BundledLMDetectorDataDir()
	if err != nil {
		return nil, err
	}
	loaded, err := lmfpd.Load(ctx, lmfpd.LoadOptions{Sources: []string{dir}})
	if err != nil {
		return nil, fmt.Errorf("offline fingerprint references: %w", err)
	}
	service.mu.Lock()
	if service.detector == nil {
		service.detector = loaded
	}
	d = service.detector
	service.mu.Unlock()
	return d, nil
}

func (o Options) config(base, key string) lmfpd.DetectConfig {
	api := lmfpd.APIResponses
	if o.Format == "chat" {
		api = lmfpd.APIChatCompletions
	}
	return lmfpd.DetectConfig{BaseURL: base, APIKey: key, Model: o.Model, API: api, NoStream: !o.Stream,
		Effort: o.Effort, Timeout: time.Duration(o.Timeout * float64(time.Second)), Strict: o.Strict,
		Count: o.Count, Parallel: o.Parallel}
}

// publicAnalysis trims the transport payload without altering the SDK result used for scoring.
func publicAnalysis(a *lmfpd.Analysis) *lmfpd.Analysis {
	if a == nil {
		return nil
	}
	out := *a
	out.Results = slices.Clone(a.Results)
	if len(out.Results) > 20 {
		out.Results = out.Results[:20]
	}
	return &out
}

func snapshot(j *Job) Job {
	out := *j
	out.cancel = nil
	out.Rounds = make([]Round, len(j.Rounds))
	for i, r := range j.Rounds {
		out.Rounds[i] = r
		out.Rounds[i].Analysis = publicAnalysis(r.Analysis)
		out.Rounds[i].Samples = slices.Clone(r.Samples)
	}
	out.Samples = slices.Clone(j.Samples)
	out.Analysis = publicAnalysis(j.Analysis)
	return out
}

func Status(jobID string) (Job, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	j := service.jobs[jobID]
	if j == nil {
		return Job{}, errors.New("fingerprint job not found")
	}
	return snapshot(j), nil
}

func Start(ctx context.Context, opts Options) (Job, error) {
	if err := opts.check(); err != nil {
		return Job{}, err
	}
	service.mu.Lock()
	if !service.enabled {
		service.mu.Unlock()
		return Job{}, errors.New("native LM Detector is disabled")
	}
	running := service.starting + service.active
	if running >= maxJobs {
		service.mu.Unlock()
		return Job{}, errors.New("two detections are already running")
	}
	service.starting++
	service.next++
	ticket := service.next
	epoch := service.epoch
	initCtx, initCancel := context.WithCancel(ctx)
	if service.pending == nil {
		service.pending = make(map[uint64]context.CancelFunc)
	}
	service.pending[ticket] = initCancel
	service.mu.Unlock()
	defer func() {
		initCancel()
		service.mu.Lock()
		delete(service.pending, ticket)
		service.starting--
		service.mu.Unlock()
	}()
	list, err := Models(initCtx)
	if err != nil {
		return Job{}, err
	}
	if !slices.Contains(list, opts.Model) {
		return Job{}, errors.New("model is not currently exposed by Gateway")
	}
	d, err := reference(initCtx)
	if err != nil {
		return Job{}, err
	}
	base, key, err := gatewayConfig()
	if err != nil {
		return Job{}, err
	}
	uid, err := id()
	if err != nil {
		return Job{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.enabled || service.epoch != epoch || initCtx.Err() != nil {
		return Job{}, errors.New("native LM Detector was disabled during start")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	j := &Job{JobID: uid, Options: opts, State: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Rounds: []Round{}, Samples: []Sample{}, cancel: cancel}
	service.jobs[uid] = j
	service.active++
	if len(service.jobs) > maxSaved {
		for key, old := range service.jobs {
			if key != uid && old.State != "running" {
				delete(service.jobs, key)
				break
			}
		}
	}
	go execute(runCtx, j, d, opts.config(base, key))
	return snapshot(j), nil
}

func Cancel(jobID string) (Job, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	j := service.jobs[jobID]
	if j == nil {
		return Job{}, errors.New("fingerprint job not found")
	}
	if j.State == "running" {
		j.State = "cancelled"
		finished := time.Now().UTC().Format(time.RFC3339Nano)
		j.FinishedAt = &finished
		j.cancel()
	}
	return snapshot(j), nil
}

// execute keeps Magpie's bounded, authenticated loopback transport while using
// the upstream SDK for challenge generation, sampling, progress and scoring.
func execute(ctx context.Context, j *Job, d *lmfpd.Detector, cfg lmfpd.DetectConfig) {
	defer func() {
		j.cancel()
		service.mu.Lock()
		service.active--
		service.mu.Unlock()
	}()
	fail := func(err error) {
		service.mu.Lock()
		defer service.mu.Unlock()
		if j.State != "running" {
			return
		}
		j.State = "failed"
		j.Error = err.Error()
		now := time.Now().UTC().Format(time.RFC3339Nano)
		j.FinishedAt = &now
	}
	bounded, closeProxy, err := boundedSamplingGateway(cfg)
	if err != nil {
		fail(err)
		return
	}
	defer closeProxy()
	cfg = bounded
	for round := 1; round <= j.Repeat; round++ {
		if ctx.Err() != nil {
			return
		}
		service.mu.Lock()
		if j.State != "running" {
			service.mu.Unlock()
			return
		}
		j.Round = round
		j.Samples = make([]Sample, j.Count)
		for i := range j.Samples {
			j.Samples[i] = Sample{Index: i + 1, State: "queued"}
		}
		service.mu.Unlock()

		// The SDK's timeout guards time to first byte; this additional
		// deadline bounds the whole stream, including slow event dribbles.
		// Serial batches need one timeout budget for each batch.
		batches := (j.Count + j.Parallel - 1) / j.Parallel
		roundCtx, cancelRound := context.WithTimeout(ctx, time.Duration(batches)*cfg.Timeout)
		result, detectErr := d.Detect(roundCtx, cfg, lmfpd.OnSample(func(index int, sample lmfpd.Sample) {
			now := time.Now().UnixMilli()
			service.mu.Lock()
			defer service.mu.Unlock()
			if j.State != "running" || j.Round != round || index < 0 || index >= len(j.Samples) {
				return
			}
			s := &j.Samples[index]
			if sample.State == "waiting" && s.StartedAt == nil {
				started := now
				s.StartedAt = &started
			}
			s.State = sample.State
			s.Count = sample.Count
			s.ExpectedCount = sample.ExpectedCount
			s.FirstByteMs = sample.FirstByteMs
			if sample.Error != nil {
				s.Error = *sample.Error
			}
			switch sample.State {
			case "complete", "truncated", "failed", "cancelled":
				if s.FinishedAt != nil {
					return
				}
				s.FinishedAt = &now
				if s.StartedAt != nil {
					elapsed := now - *s.StartedAt
					s.DurationMs = &elapsed
				}
				numbers := lmfpd.ParseNumbers(sample.Text)
				if len(numbers) > 0 {
					minimum := slices.Min(numbers)
					s.MinimumValidNumber = &minimum
					s.AnomalousDistribution = minimum >= 200
				}
				j.Completed++
			}
		}))
		cancelRound()
		if ctx.Err() != nil {
			return
		}
		if detectErr != nil {
			fail(detectErr)
			return
		}
		if result == nil {
			fail(errors.New("SDK returned no detection result"))
			return
		}
		why := ""
		if result.Error != nil {
			why = *result.Error
		}
		service.mu.Lock()
		if j.State != "running" {
			service.mu.Unlock()
			return
		}
		j.Analysis = result.Analysis
		j.Rounds = append(j.Rounds, Round{Round: round, Samples: slices.Clone(j.Samples), Analysis: result.Analysis, Error: why})
		service.mu.Unlock()
	}
	service.mu.Lock()
	if j.State == "running" {
		scored := false
		var reasons []string
		for _, round := range j.Rounds {
			if round.Analysis != nil {
				scored = true // Includes the valid not_model verdict.
			} else if round.Error != "" {
				reasons = append(reasons, round.Error)
			}
		}
		if scored {
			j.State = "done"
		} else {
			j.State = "failed"
			j.Error = strings.Join(reasons, "; ")
			if j.Error == "" {
				j.Error = "No detection round could be scored"
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		j.FinishedAt = &now
	}
	service.mu.Unlock()
}
