# Windows fork: upstream sync, testing and releases

## Daily sync
- Daily at **02:17 UTC (10:17 Beijing time)**, also manually in Actions.
- Merge upstream main locally in the Windows runner. Never reset or force push the fork.
- Run the same complete quality gate used by PR, push and release CI **before** pushing main.
- Stop on conflicts, failed tests or failed builds. No push or release occurs then.
- On a passing merge, push main and explicitly dispatch the Windows release at the exact merged commit.

## Shared Windows quality gate
All three workflows call **.github/scripts/test-windows.ps1**, so there are no drifting or duplicated test command lists:
1. Static analysis with **go vet ./...**.
2. The full native Windows Go suite **go test -count=1 -p 2 -parallel 4 -timeout 30m ./...** (real tests, no -run '^$' compilation-only shortcut). This includes fork-specific Codex, settings, native plugin bridge, LM Detector and Windows tests.
3. Compile Windows GUI with production tag and CLI with nogui tag for both x64 and ARM64. ARM64 is cross-compiled, not run under an ARM64 runner.
4. JavaScript syntax checks for GUI and plugin assets.
5. Chromium Playwright smoke tests for plugin-page, plugin-actions and plugin-tools, using isolated fixtures. Browser dependencies live only under RUNNER_TEMP. Hundreds of unrelated upstream browser scenarios and non-Windows WebKit are intentionally excluded.

Test and sync jobs have a 150-minute limit; release has a 180-minute limit.
CI environment failures will block updates until reviewed.

## Releases
- The Fork Windows Release workflow runs the **same gate** again before publishing, including for direct pushes or manual runs.
- Builds desktop and CLI for Windows x64 and ARM64; publishes EXEs, ZIPs and SHA256SUMS.txt to this fork's GitHub Releases.
- Tags are immutable and tied to source commits; it does not use upstream release artifacts, upstream signing keys or third-party secrets.
- Fork executables are unsigned. The built-in upstream self-updater may replace them with upstream binaries; install from this fork to retain custom features.
- Docker automatic triggers are disabled separately; the Docker workflow is manual-only.

If pushing or publishing is denied, inspect repository Actions workflow permissions and main branch rules.
