# LM Detector — native Magpie Go built-in (draft)

The fingerprint detector has been moved to the `plugins/lm-detector` built-in plugin registered through `internal/pluginrt`. Its model-fingerprint engine is now the pinned upstream **Go SDK** (`github.com/Ikaleio/lm-detector/sdk/go`); the native management endpoints and job manager live under `internal/lmdetector` and the embedded first-party window page is under `internal/gui/assets/plugins/lm-detector`.

The native detector does not run `fpd` or the old Bun Page/tool backend. The Gateway key is kept on the Go side, and only direct `provider/model` IDs in the Gateway's `/v1/models` listing may be checked. It offers Chat/Responses, streaming or JSON, count 1–3, parallel 1–3, strict (3/3), repeat 1–20, reasoning effort and per-request timeout. At most two jobs run concurrently, jobs are in memory, and turning the built-in off cancels active work.

## Build and offline reference data

Before `go build`, run `node .github/scripts/prepare-lm-detector.mjs`. This build-time-only command fetches pinned paired `unified_bank.json` and `shared_detector.bin` data and stages them in the existing Go embedded bundle. The Go SDK opens **only** the extracted local reference directory through `LoadOptions.Sources`; it does not fetch reference data at user runtime. The existing content-addressed extraction validates its payload and refuses corrupted files.

The obsolete JS implementation remains in the repository **only for the existing compatibility tests and build-time asset staging** during this draft. The runtime no longer installs it for new users. A one-time migration removes registered legacy entries from `plugins.json` and carries forward their Off or explicitly uninstalled state into `plugins.yml`; unrelated entries are not touched. See [tracking issue #9](https://github.com/NoneMore/magpie/issues/9).

## Native local API

All endpoints are in the same auth boundary as other Magpie UI APIs:

- `POST /api/lm-detector/models` — list available direct Gateway models.
- `POST /api/lm-detector/start` — create a job using the JSON options above.
- `POST /api/lm-detector/status` — read job status using `{"jobId":"..."}`.
- `POST /api/lm-detector/cancel` — cancel an active job.

## Known gaps before marking this PR ready for review

- The upstream Go `RequestSample` only returns when an HTTP sample finishes; therefore this initial Go UI can show queued, waiting and completed samples, but **not token-by-token integer progress, TTFT or tokens/sec**. Do not remove the old tests that assert these until an upstream callback or a native adapter restores equivalent behavior.
- Provider row right-click action needs native menu integration; the new built-in page currently offers a Provider/Model picker but no row action.
- The old Bun source/artifacts and old compatibility tests still need to be retired after native parity tests cover all cases.
- Compilation/CI and live-provider UI parity still need verification. The PR remains a **draft** until those checks pass.

## Security

Non-loopback Gateway URLs must be HTTPS and explicitly supply `MAGPIE_FPD_GATEWAY_KEY`. Local loopback uses the previous `magpie` default when no key is set. Keys never enter browser JavaScript or the detector's result JSON. Fingerprinting sends paid model requests and its closed-set ranking is not proof of identity.
