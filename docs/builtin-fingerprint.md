# Built-in Model Fingerprint

**Status:** Built-in Magpie UI and job backend with the unchanged, version-pinned
lm-detector scoring engine and its calibration bank. The Windows Actions build
bundles the engine next to the GUI executable for offline use.

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

**Important limitation:** the classifier is the published **lmfpd package**
(v0.0.1792416845676) executed using Magpie's Bun runtime, not a native Go
reimplementation. CI's Windows ZIP includes `fingerprint-engine/node_modules`
with the reference data. Keep that folder beside `magpie-windows-amd64.exe`
(or `magpie-windows-arm64.exe`). In that layout there is no runtime npm
installation; Magpie still needs its Bun runtime, which it downloads if
not already cached. Developer-built standalone executables without the
adjacent engine folder fall back to downloading the pinned npm package.
A completely Go-native implementation would be a later rewrite.

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
