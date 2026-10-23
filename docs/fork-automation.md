# Windows-only fork: fast daily upstream sync, CI, and releases

## Automatic daily update (once per day)
- At **02:17 UTC / 10:17 Beijing time** the Daily Upstream Sync workflow checks yetone/magpie main.
- It merges locally, preserving this fork's commits. Conflicts abort immediately.
- It executes the same fast Windows quality gate as the Test workflow **before** pushing.
- On any test failure nothing is pushed or released. On success it pushes main
  without force and dispatches Fork Windows Release for the exact tested SHA.
- GitHub Actions commits made with the workflow GITHUB_TOKEN do not normally
  trigger push workflows, so daily sync explicitly dispatches the release.

## Fast Windows quality gate
The shared script is .github/scripts/test-windows.ps1. It runs:
1. go vet ./... and a compile-only pass over all Go tests using go test -run '^$' ./...
2. Actual isolated fork tests: Codex caller prompt, settings retention, GUI Page
   bridge permissions and origin checks, plugin runtime/import rules.
3. Upstream-selected native Windows tests: Bun stderr, network, usage cache,
   Windows installation/rollback, sessions, dsh configuration and VS Code.
4. Windows production GUI x64 build and JavaScript syntax checks of GUI,
   plugin bridge and LM Detector entrypoint.

This follows upstream's Windows policy: its complete Go suite has HOME,
USERPROFILE, Unix file-permissions and timing assumptions and is NOT
reliably runnable on Windows. The full non-Windows suite is therefore not
a prerequisite for this personal Windows fork.

The gate is deliberately focused for daily stability and a short runtime;
it is **not** equivalent to full Go, ARM64 runtime or full browser coverage.
Windows sync/Test jobs are capped at 35 minutes, not a speed guarantee.
New main/PR Test runs cancel stale same-ref Test runs so an obsolete long
run cannot block the latest check; no cancelled run can trigger a release.

## Browser and LM Detector deeper testing
The separate **Optional UI Smoke** workflow is manual-only and checks
Chromium plugin pages, actions and tools. It is not required to sync/release.
The LM Detector's full Bun tests under plugins/lm-detector/tests need an
installed lmfpd reference-data package; they are not part of the daily gate.

## Release is no longer duplicated with Test
- For normal pushes to main, Test runs once. Only its **successful push run**
  triggers Fork Windows Release via workflow_run. PRs and failed tests cannot
  trigger release.
- For daily sync, the pre-push quality gate already ran, so the sync job
  explicitly dispatches the Release. It **does not rerun** the gate.
- A release manually started by the owner runs the gate first (unless the
  release already exists).
- Release builds the actual Windows x64 and ARM64 GUI and CLI, creates ZIP
  archives and SHA256SUMS.txt, then publishes this fork's own binaries with a
  commit-derived tag. ARM64 is cross-compiled, not run on x64 CI.
- Binaries are unsigned, and the upstream self-updater may overwrite fork
  changes. Install from this fork's Releases page to keep custom features.
- The unrelated Docker workflow remains manual-only.

If permissions fail, check Settings > Actions > General > Workflow
permissions and any main branch protection rules.
