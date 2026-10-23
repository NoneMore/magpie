# LM Detector — Magpie native Page plugin

This plugin lives in the Magpie fork source tree under `plugins/lm-detector`. It is a **native Magpie Page + Provider Action** that calls its own Bun tool backend using the fork's existing permissioned `magpie.tool.run` bridge. **No CLI subprocess, MCP UI, or `fpd` execution is involved.**

**Installation (development build of NoneMore/magpie):**

```sh
cd plugins/lm-detector
npm install --ignore-scripts
magpie plugin add "$(pwd)"
```

The npm dependency `lmfpd` supplies **only** `data/unified_bank.json` and `data/shared_detector.bin`; the challenge generator, features, positional classifier, detector decoding and scoring are vendored directly from `Ikaleio/lm-detector/shared` and executed in the plugin host. No CLI imports or subprocesses are used.

The fork already includes Page tool bridge. Grant the plugin `providers:list` and `tool:run` permissions. Open **Model Fingerprint** from the sidebar or click **Detect model fingerprint** on a Provider row. Select `OpenAI Responses` or `Chat Completions` according to Gateway/model compatibility. The model is requested explicitly from the Gateway; avoid routing groups/fallbacks, since they mix underlying models.

The Gateway defaults to `http://127.0.0.1:3425/v1` and its local development token `magpie`. To override when launching Magpie:

```sh
export MAGPIE_FPD_GATEWAY_URL='http://127.0.0.1:3425/v1'
export MAGPIE_FPD_GATEWAY_KEY='YOUR_GATEWAY_KEY'
```

Non-loopback gateways require HTTPS and an explicit key. No upstream provider key is sent into the Page iframe. The Page remains network-isolated.

## Design and limitations

- One run makes three independent challenges (292–332 integers each), sends up to three requests concurrently and calls the real upstream `analyzeSharedOutputs` scorer, with partial-sample support.
- The reference bank + detector are loaded together and checked for matching metadata; a mismatch fails safely. The npm `lmfpd` dependency is currently `latest`; for a reproducible distribution, test and pin it with a lockfile before release.
- A run can be cancelled. Jobs and final reports are in-memory only and are lost when the plugin host restarts. At most two run concurrently.
- The current adapter reads **non-streaming** Responses or Chat Completions JSON. Models/Gateway routes that only support SSE need a streaming adapter in a follow-up. Tokenizer probing is not implemented.
- The `magpie.tool.run` bridge is a capability exposed by this fork, not by upstream Magpie. Existing plugin tools are also visible to the agent-facing plugin tools MCP server.
- The plugin is bundled **in source**, but it is **not yet registered as a Go `pluginrt` built-in toggle** (those use a separate lifecycle/UI API). Until that integration exists, install it with `magpie plugin add` as above.
- Each run can incur model tokens/cost. The ranking's calibrated confidence is **closed-set** against the included reference identities and cannot prove the true model identity.

## Third-party source

The files in `shared/` are copied from `Ikaleio/lm-detector` at commit `70410db4522387c7d4a6dff8c7a4b6ce0f0239f1` under its MIT license. See `LICENSE-LM-DETECTOR`. Keep the copied algorithm and the data package in sync.

## Verification

Static files can be checked with `node --check index.js && node --check page/app.js`. This draft has not been tested end-to-end against a running Magpie Gateway; a full test needs the fork binary, Bun plugin host, installed `lmfpd` data and a model.
