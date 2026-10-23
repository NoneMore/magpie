# Built-in Model Fingerprint

**Status:** Initial built-in integration for the Magpie fork. The UI and jobs
run in Magpie; the actual classifier is the published lm-detector engine,
pinned to a version and downloaded into Bun's package cache on first run.

## User flow

1. Build Magpie with the fingerprint built-in enabled (the default for the
   All-plugins preset). Open the **Model fingerprint** page.
2. Start Magpie Gateway and configure a model to be served at its exact
   `provider/model` ID.
3. Click **Refresh**, select the model and **Run fingerprint**.
4. Magpie creates a background job, samples three numeric challenges with
   `lmfpd` and displays its closed-set calibrated ranking. The browser can
   close during sampling; finished metadata and rankings are saved to
   `settings.Dir()/fingerprint-history.json` with private permissions.
5. Running jobs may be cancelled. When a run cannot be scored, the UI shows
   the error instead of fabricating a model identity.

All requests to model providers go through the *existing* Magpie Gateway
using the credentials already configured there. The feature does not read
or store upstream provider API keys. The engine's local Gateway key is
inherited through its environment, never through command-line flags or
page state. The local Gateway fallback uses `http://127.0.0.1:3425`
and the local `magpie` key. For non-default addresses, set
`MAGPIE_FPD_GATEWAY_URL` and `MAGPIE_FPD_GATEWAY_KEY` on the Magpie
process. The first lookup prefers the saved Gateway port in Magpie settings.

**Important limitation:** this implementation runs the **published lmfpd
package** using Magpie's Bun runtime. It contains the original reference
bank and detector parameters, so it does not approximate the scoring
algorithm; however, its algorithm has *not yet been rewritten as native Go*
and the package is not embedded inside the Magpie executable. The first
run needs network access for the pinned package and Bun (if Bun was not
already cached). Offline use requires priming both caches. Packaging
lm-detector's version-locked assets into release builds is a separate
follow-up task.

## Security and behavior

- Model choices come from a local Gateway `GET /v1/models` request, limited
  to 2,000 unique `provider/model` IDs. Groups and automatic failovers are
  not eligible, since mixed upstreams ruin the fingerprint.
- Each job submits three probes using the Responses API, with
  `--strict --count 3 --parallel 3 --timeout 180 --no-stream`. An
  upstream provider that refuses Responses or numeric formatting can
  produce an unscorable report, not proof of spoofing.
- Only two jobs run at once. The bounded JSON result is parsed by the Go
  backend and held in a small history (40 jobs, 8 MiB maximum file).
  Finished job metadata excludes Gateway and provider keys.
- Disable the plugin under **Plugins › Built-in** to hide its page and
  cancel running jobs. Routes are denied while the plugin is off.
- The GUI's existing same-origin and management API restrictions protect
  the four `/api/fingerprint/*` routes; no second listening port is opened.
- Results are *closed-set relative similarities*. Neither the leading
  probability nor a family rank authenticates the underlying vendor.

## Files

- `plugins/fingerprint/fingerprint.go` registers the built-in manifest.
- `internal/fingerprint/fingerprint.go` handles Gateway discovery, jobs,
  cancellation and calls into the upstream scoring engine.
- `internal/gui/fingerprint.go` adds authenticated window routes.
- `internal/gui/assets/plugins/fingerprint/` contains the page and words.

## Validation

`go test -tags nogui ./internal/fingerprint ./internal/gui ./plugins/fingerprint`

`node --check internal/gui/assets/plugins/fingerprint/client.js`

`make release-windows` builds the Windows desktop program, including this
built-in page. The accompanying PR's GitHub Actions build the same targets.
