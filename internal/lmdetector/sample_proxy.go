package lmdetector

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

const (
	maxSamplingResponse = 1 << 20
	maxSamplingSSEFrame = 256 << 10
)

// boundedSamplingGateway is a short-lived, loopback-only adapter for the
// upstream SDK v0.1.0, whose RequestSample HTTP client is not injectable.
// It enforces reply byte/frame limits before the SDK can buffer untrusted
// JSON or SSE. The actual Gateway key never travels to the SDK-facing URL.
// Remove this adapter once the upstream SDK exposes supported bounded IO.
func boundedSamplingGateway(cfg lmfpd.DetectConfig) (lmfpd.DetectConfig, func(), error) {
	realURL, err := url.Parse(cfg.BaseURL)
	if err != nil || realURL.Host == "" || realURL.User != nil || realURL.RawQuery != "" || realURL.Fragment != "" {
		return cfg, nil, errors.New("invalid sampling Gateway URL")
	}
	secret, err := id()
	if err != nil {
		return cfg, nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return cfg, nil, fmt.Errorf("limited sampling transport: %w", err)
	}
	realKey := cfg.APIKey
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Even processes on the same machine may contact loopback ports.
		// Require an unguessable per-job bearer token and a fixed request path.
		bearer := "Bearer " + secret
		allowed := r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/chat/completions"
		if !allowed || r.Method != http.MethodPost || r.URL.RawQuery != "" ||
			subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(bearer)) != 1 {
			http.Error(w, "sampling proxy request denied", http.StatusForbidden)
			return
		}
		upstream := *realURL
		upstream.Path = strings.TrimRight(realURL.Path, "/") + strings.TrimPrefix(r.URL.Path, "/v1")
		upstream.RawPath = ""
		body := http.MaxBytesReader(w, r.Body, 64<<10)
		defer body.Close()
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstream.String(), body)
		if err != nil {
			http.Error(w, "invalid sampling request", http.StatusBadGateway)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", r.Header.Get("Accept"))
		req.Header.Set("Authorization", "Bearer "+realKey)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Gateway request failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Do not leak credentials accidentally echoed by an upstream error.
			http.Error(w, fmt.Sprintf("Gateway HTTP %d", resp.StatusCode), resp.StatusCode)
			return
		}
		media := resp.Header.Get("Content-Type")
		w.Header().Set("Content-Type", media)
		w.Header().Set("Cache-Control", "no-store")
		stream := strings.Contains(strings.ToLower(media), "text/event-stream")
		if !stream && r.URL.Path == "/v1/chat/completions" {
			data, err := io.ReadAll(io.LimitReader(resp.Body, maxSamplingResponse+1))
			if err != nil || len(data) > maxSamplingResponse {
				http.Error(w, "Gateway JSON response exceeds sampling limit", http.StatusBadGateway)
				return
			}
			// Upstream SDK v0.1.0 expects a string. Some providers send
			// Chat message.content as [{text: "..."}], supported by the old UI.
			var payload map[string]any
			if json.Unmarshal(data, &payload) == nil {
				if choices, ok := payload["choices"].([]any); ok {
					for _, item := range choices {
						choice, _ := item.(map[string]any)
						message, _ := choice["message"].(map[string]any)
						parts, ok := message["content"].([]any)
						if !ok {
							continue
						}
						var text strings.Builder
						for _, item := range parts {
							part, _ := item.(map[string]any)
							if value, ok := part["text"].(string); ok {
								text.WriteString(value)
							}
						}
						message["content"] = text.String()
					}
				}
				if encoded, err := json.Marshal(payload); err == nil && len(encoded) <= maxSamplingResponse {
					data = encoded
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(data)
			return
		}
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 8<<10)
		var total, frame int
		var tail string
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				allowed := n
				for i := 0; i < n; i++ {
					if total >= maxSamplingResponse {
						allowed = i
						break
					}
					total++
					if stream {
						frame++
						seq := tail + string(buf[i])
						if strings.HasSuffix(seq, "\n\n") || strings.HasSuffix(seq, "\r\r") || strings.HasSuffix(seq, "\r\n\r\n") {
							frame = 0
						}
						if len(seq) > 3 {
							seq = seq[len(seq)-3:]
						}
						tail = seq
						if frame > maxSamplingSSEFrame {
							allowed = i
							break
						}
					}
				}
				if allowed > 0 {
					if _, err := w.Write(buf[:allowed]); err != nil {
						return
					}
					if stream && flusher != nil {
						flusher.Flush()
					}
				}
				if allowed < n {
					return // bounded EOF makes an unfinished JSON/SSE sample fail
				}
			}
			if readErr != nil {
				return
			}
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10}
	go func() { _ = server.Serve(listener) }()
	cfg.BaseURL = "http://" + listener.Addr().String() + "/v1"
	cfg.APIKey = secret
	return cfg, func() { _ = server.Close() }, nil
}
