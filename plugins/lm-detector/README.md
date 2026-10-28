# LM Detector — native Magpie Go built-in

The model fingerprint detector uses the pinned upstream [Go SDK v0.2.0](https://github.com/Ikaleio/lm-detector/tree/sdk/go/v0.2.0/sdk/go) through Magpie's `internal/pluginrt`. The native lifecycle, bounded job manager and HTTP routes are in `internal/lmdetector`; the built-in page lives under `internal/gui/assets/plugins/lm-detector`. The native feature **does not execute** the legacy Bun detector.

The Gateway credential stays in Go and never reaches the page or the SDK's temporary local adapter: an unpredictable per-job bearer token authenticates the loopback transport. Only direct Gateway `provider/model` IDs from `/v1/models` can be checked; `auto/`, `group/`, and `routing/` groups are refused. Supported options: Responses/Chat, JSON/SSE, 1–3 challenges per round, parallel 1–3, strict 3/3, 1–20 rounds, reasoning effort and configurable timeouts. Only two jobs can execute concurrently, and turning the built-in Off cancels running and pending starts. Closing/restarting Magpie does not persist jobs.

## Live sampling and result limits

The SDK `Detector.Detect(ctx, config, OnSample(callback))` reports queued, waiting, streaming integer counts and settled samples, with `first_byte_ms`. The page polls the native status endpoint and displays progress, **first-byte latency** for SSE (or **response-ready latency** for non-streaming JSON), plus the **average integer output rate (numbers/s)** over each sample's full elapsed time. First byte is **not TTFT**: heartbeat/metadata may precede generated text. The SDK currently does not expose a reliable first-content timestamp, so the UI does not claim to measure TTFT. Integer rate is *not* token throughput (TPS), which cannot be calculated accurately without token usage. Public status errors redact the real Gateway key, including if an upstream 2xx JSON/SSE error echoes it. Full raw outputs/keys are not exposed by the status API.

The SDK's default HTTP reader remains unbounded, so Magpie retains a short-lived authenticated loopback adapter that caps the **entire** Gateway sampling response to 1 MiB and any unfinished SSE frame to 256 KiB. `Timeout (seconds)` limits the time to first SSE byte (or full response in JSON mode). Separately, each detection round has a total deadline of `ceil(challenges / parallelism) × Timeout`, including the time spent streaming **after** the first byte; the deadline is not reset on heartbeat or content. This intentionally bounded total time may truncate slow streams. The SDK still performs challenge creation, parsing and scoring. Only the 20 highest-ranked candidates per analysis are sent to the page; SDK calculations are not truncated.

## Build and offline reference data

Run `node .github/scripts/prepare-lm-detector.mjs` before direct `go build`. The script pins `lmfpd@0.0.1792416845676`, checks both upstream asset SHA-256 values against audited constants, and stages **only** `unified_bank.json` and `shared_detector.bin` in the Go embed. The old Bun executor, Page and shared JavaScript are **not packaged in the binary**. Go SDK `LoadOptions.Sources` points to the extracted local reference directory; no runtime download occurs. Supported Makefile release targets (`release`, `release-cli`, `release-linux`, `release-windows`) prepare data automatically; ordinary development commands still require manual preparation.

The legacy Bun detector, TypeScript scoring modules, Page and tests have been removed. A native Gateway integration test now covers Chat/Responses × JSON/SSE through the actual Gateway using only mock upstream data. Legacy registration entries in `plugins.json` are precisely retired without disturbing other plugins; the new built-in is independently managed in `plugins.yml` (old Off/uninstall preference is intentionally **not** mapped over). See [tracking issue #9](https://github.com/NoneMore/magpie/issues/9).

## Native local API

The routes are within Magpie's existing authenticated UI boundary and additionally enforce same-origin requests and JSON Content-Type:

- `POST /api/lm-detector/models` — list direct Gateway models
- `POST /api/lm-detector/start` — start a job with the options above
- `POST /api/lm-detector/status` — status with `{"jobId":"..."}`
- `POST /api/lm-detector/cancel` — cancel a job

## Verification

The dedicated CI prepares verified offline assets, tests SDK v0.2.0 sampling against mock Gateway responses (Chat/Responses × JSON/SSE), live SSE progress, cancellation, same-origin protections, size limits and offline `Analyze`, then exercises the real Magpie Gateway with a native Go SDK integration test (no Bun process). The Page Bridge Windows path also prepares and checks embedded references before creating its binaries.

The Provider-row context-menu action and Chromium/WebKit native UI behavior still require browser-level validation; CI results should be checked before marking the PR ready. Detection makes paid API calls and **closed-set fingerprint rankings do not prove a model's identity**.
