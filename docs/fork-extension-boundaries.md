# Fork extension boundary: Caller Prompt

This fork intentionally keeps a small surface on upstream-owned code. Do not
use a blanket merge strategy or replace upstream files with ours/theirs.

## Trusted client preference registration

`internal/gui/assets/kernel.js` exposes `magpie.agentPrefs` for **trusted,
bundled client scripts**, not for sandboxed community-plugin pages. The fork
registers its Codex preference in
`internal/gui/assets/fork/codex-caller-prompt.js`, loaded between
`kernel.js` and `app.js`. The registry:

- creates the labelled row without duplicating `index.html` markup;
- inserts it before `codexAgentsV1Row` without editing `AGENT_PREFS.codex`;
- composes row IDs through `agentPrefs.rows(agent, coreRows)`;
- renders the boolean Off/On control through the host's segmented UI;
- uses the host's existing `writingPrefs` save/error lifecycle; and
- registers translations through `magpie.strings`, not the shared `i18n.js`.

The host interface is intentionally **narrow** (boolean agent preferences
only). Do not expose arbitrary `settings/*` writes to untrusted iframes.
Unrecognised endpoint changes and downstream API behavior require review.

If upstream adds a new Codex row, the fork's row need not change; tests
assert the upstream row order is preserved. The registry is not an upstream
public extension-point contract. Track upstream kernel and agent preferences
when updating.

## Native Codex request policy

`internal/provider/codex_caller_prompt.go` owns the fork-specific opt-in
decision, cache-key namespace, keyless session-header rewriting and native
passthrough marker. Upstream `codex_request.go` still owns the final JSON
request normalization and signing. The experimental setting is off by
default and may be unsupported by the subscription backend.

Do not relocate this policy into the existing JavaScript gateway middleware
without proving that it executes after Codex request preparation and before
signing, with the same request-scoped decision and safe permissions.

## CI contracts

`node --test internal/gui/tests/fork-agent-prefs.test.cjs` checks the
registration/merge contract without browser installations. The Windows
gate also runs Codex request/headers/cache tests and checks JavaScript
syntax. For visual rendering, optional Playwright tests are still needed;
the fast gate is not comprehensive browser or backend integration coverage.

Keep sync behavior unchanged: merge in an isolated runner, run Windows
checks, and push only the tested merge without force. Unexpected merge
conflicts must halt the sync without changing `main`.
