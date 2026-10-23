# LM Detector — Magpie native Page plugin

This plugin lives in the Magpie fork source tree under `plugins/lm-detector`. It is a **native Magpie Page + Provider Action** that calls its own Bun tool backend using the fork's existing permissioned `magpie.tool.run` bridge. **No CLI subprocess, MCP UI, or `fpd` execution is involved.**

**Installation (development build of NoneMore/magpie):**

```sh
cd plugins/lm-detector
npm install --ignore-scripts
magpie plugin add "$(pwd)"
```

The npm dependency `lmfpd` supplies **only** `data/unified_bank.json` and `data/shared_detector.bin`; the challenge generator, features, positional classifier, detector decoding and scoring are vendored directly from `Ikaleio/lm-detector/shared` and executed in the plugin host. No CLI imports or subprocesses are used.

The fork already includes Page tool bridge. Grant the plugin `providers:list` and `tool:run` permissions. Open **Model Fingerprint** from the sidebar or click **Detect model fingerprint** on a Provider row. Select `OpenAI Responses` or `Chat Completions` according to Gateway/model compatibility. The Page enables SSE streaming by default, with a checkbox to switch back to non-streaming JSON. The backend `start` tool accepts an optional `stream: boolean` argument (default `false` for compatibility). The Page also exposes CLI-equivalent `effort`, `timeout`, `count`, `parallel`, `repeat`, and `strict` options. The model is requested explicitly from the Gateway; avoid routing groups/fallbacks, since they mix underlying models.

The Gateway defaults to `http://127.0.0.1:3425/v1` and its local development token `magpie`. To override when launching Magpie:

```sh
export MAGPIE_FPD_GATEWAY_URL='http://127.0.0.1:3425/v1'
export MAGPIE_FPD_GATEWAY_KEY='YOUR_GATEWAY_KEY'
```

Non-loopback gateways require HTTPS and an explicit key. No upstream provider key is sent into the Page iframe. The Page remains network-isolated.

## UI detection options

These settings follow the [LM Detector CLI detect options](https://lm.ikale.io/docs/cli/detect/) without executing the CLI. They are passed through the Page's `magpie.tool.run("start", ...)` call and validated again in the Bun backend.

| UI option | Backend field | Default | Behavior |
| --- | --- | --- | --- |
| `-e, --effort` | `effort` | omitted | Optional provider-specific reasoning effort, passed as `reasoning.effort` (Responses) or `reasoning_effort` (Chat). |
| `--timeout` | `timeout` | 120 seconds | 0.1–600 seconds. SSE times out until the first nonempty body chunk; JSON times out until the complete body has arrived. |
| `--count` | `count` | 3 | 1–3 independent challenges per round. One or two samples yield uncalibrated rankings when model-like. |
| `-p, --parallel` | `parallel` | 3 | 1–3 simultaneous requests per round, capped at `count`; does not change total requests. |
| `-n, --repeat` | `repeat` | 1 | Sequential independent rounds (1–20 in this plugin to bound token cost). Each round is scored separately. |
| `-s, --strict` | `strict` | false | Requires `count=3`, disables SSE integer-count capping and JSON trimming, and scores only if all three naturally complete with valid answers. |

The plugin runs at most two *jobs* at once; each job can make at most `count × repeat` paid requests. The Page shows a per-sample integer progress bar and aggregate request progress across rounds. Streaming samples show first-body-byte timing (diagnostics), visible-text TTFT and an approximate tokens/second decode rate when enough unbuffered text events arrive. It uses the upstream throughput meter; empty heartbeat and metadata chunks do not establish TTFT, and short or buffered streams show TPS as unavailable. JSON samples expose total wall time only (TTFT/TPS are unavailable). After completion, selecting a round shows its saved timings and sampling progress. No extra Gateway credentials are exposed to the Page.

## Design and limitations

- Each round makes 1–3 independent challenges (292–332 integers each), sends up to three requests concurrently and calls the real upstream `analyzeSharedOutputs` scorer, with partial-sample support when strict mode is off. Routing groups are excluded because they can sample different models; configure model fallbacks carefully.
- Each completed sample reports its minimum valid integer and a display-only abnormal-distribution flag when that minimum is at least 200, matching upstream LM Detector. The Page warns that the round may be unreliable and recommends a fresh run with new random prompts; the warning does not change ranking or probabilities.
- The adapter supports both non-streaming JSON and SSE for Responses / Chat Completions. In relaxed mode, SSE consumes incremental events and caps once it has enough complete integers; strict mode waits for normal completion. SSE timeout covers the first body chunk only; JSON timeout covers the complete response. The Bun transport disables its separate idle timeout, preserving the upstream CLI's unbounded wait after the first SSE body chunk. Chat SSE requests optional usage metrics, but the relaxed cap normally ends the stream before its final usage event.
- The reference bank + detector are loaded together and checked for matching metadata; a mismatch fails safely. The npm `lmfpd` reference-data package is pinned to `0.0.1792416845676`, which passed the shared detector compatibility tests; a lockfile is still recommended for fully reproducible transitive dependencies.
- A run can be cancelled. Jobs and final reports are in-memory only and are lost when the plugin host restarts. At most two run concurrently.
- Tokenizer probing is not implemented. The SSE adapter stops at the required integer count rather than waiting for a post-cap usage report; fingerprint scoring does not use token usage.
- The `magpie.tool.run` bridge is a capability exposed by this fork, not by upstream Magpie. Existing plugin tools are also visible to the agent-facing plugin tools MCP server.
- The plugin is bundled **in source**, but it is **not yet registered as a Go `pluginrt` built-in toggle** (those use a separate lifecycle/UI API). Until that integration exists, install it with `magpie plugin add` as above.
- Each run can incur model tokens/cost. The ranking's calibrated confidence is **closed-set** against the included reference identities and cannot prove the true model identity.

## Third-party source

The files in `shared/` are copied from `Ikaleio/lm-detector` at commit `70410db4522387c7d4a6dff8c7a4b6ce0f0239f1` under its MIT license. See `LICENSE-LM-DETECTOR`. Keep the copied algorithm and the data package in sync.

## Verification

Static files can be checked with `node --check index.js && node --check page/app.js`. The CI runs Bun unit tests and an end-to-end backend integration test using the real Go Magpie Gateway handler, Bun plugin host and vendored scorer against a loopback-only mock upstream, covering Responses and Chat in both JSON and SSE modes. It does not require an external model account. A manual Page + provider-action + real-model check remains a separate acceptance step.
