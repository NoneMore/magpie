# Fast, native Windows gate used by upstream sync and Test.
# Use this from the repository root with Go, Bun and Node 22 available.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

Write-Host '=== Prepare offline LM Detector resources (build-time npm only) ==='
node .github/scripts/prepare-lm-detector.mjs
if ($LASTEXITCODE -ne 0) { throw 'LM Detector reference preparation failed' }

Write-Host '=== Vet and compile all Go tests (same Windows policy as upstream) ==='
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
go test -run '^$' ./...
if ($LASTEXITCODE -ne 0) { throw 'Windows compile-all tests failed' }

Write-Host '=== Offline bundle bootstrap and migration regressions ==='
go test -count=1 ./internal/plugin -run '^(TestBundled|TestMaterialize|TestPreparedBundle)'
if ($LASTEXITCODE -ne 0) { throw 'bundled plugin registration failed' }

Write-Host '=== Fork Codex, GUI and plugin permission regressions ==='
# Deliberately do not execute TestCoreScriptBudget: this is an upstream
# product-size policy, not a Windows correctness test; fork Page Bridge
# adds intended script bytes.
go test -count=1 -timeout 5m ./internal/provider -run '^(TestCodexBodyCallerPromptOptIn|TestCodexCallerPromptSessionHeadersWithoutKey|TestCodexSignSessionHeaders|TestCodexPromptCacheKey)$'
if ($LASTEXITCODE -ne 0) { throw 'Codex regression failed' }
go test -count=1 -timeout 5m ./internal/gui -run '^(TestCodexCallerPromptPreferenceAPI|TestSettingsSaveKeepsWhatItDoesNotSend|TestPluginPageServedShutIn|TestPluginPageReadsWhatItDeclared|TestPluginPageNoAPI|TestPluginPageWebGuard|TestPluginPageActions|TestPluginPageToolRequiresPermission)$'
if ($LASTEXITCODE -ne 0) { throw 'GUI / Page bridge regression failed' }
go test -count=1 -timeout 5m ./internal/pluginrt -run '^(TestReconcile|TestCompatible|TestPluginsImportOnlyCore)$'
if ($LASTEXITCODE -ne 0) { throw 'Plugin runtime regression failed' }

Write-Host '=== Fork agent preference registration contract (no browser needed) ==='
node --test internal/gui/tests/fork-agent-prefs.test.cjs
if ($LASTEXITCODE -ne 0) { throw 'Fork agent preference registration failed' }

Write-Host '=== Upstream-maintained Windows native regression cases ==='
bun --version
if ($LASTEXITCODE -ne 0) { throw 'Bun unavailable' }
go test -count=1 -timeout 5m ./internal/plugin -run '^(TestHostStartsPastAnOverlongStderrLine|TestDrainStderrKeepsTheChildGoing)$'
if ($LASTEXITCODE -ne 0) { throw 'Windows plugin stderr test failed' }
go test -count=1 -timeout 2m ./internal/library -run '^(TestNetHealth|TestCheckHTTP)$'
if ($LASTEXITCODE -ne 0) { throw 'Windows network checks failed' }
go test -count=1 -timeout 5m ./internal/usage -run '^(TestWindows|TestLogChange|TestLogFileInfoPreservesIdentity$|TestUsageSameSizeAndMtimeRewrite$|TestLogGrowingRewriteThenAppend$|TestUsagePageCacheAfterUncachedQuery$)'
if ($LASTEXITCODE -ne 0) { throw 'Windows usage checks failed' }
go test -count=1 -timeout 2m ./internal/update -run '^(TestInstallBinary|TestMoveAside|TestTrialStart)'
if ($LASTEXITCODE -ne 0) { throw 'Windows update/rollback checks failed' }
go test -count=1 -timeout 5m ./internal/sessions -run '^Test(WindowsSession|Move|CopyAll|CodexArchived|CodexSameTick|CodexPlainTwin)'
if ($LASTEXITCODE -ne 0) { throw 'Windows session checks failed' }
go test -count=1 -timeout 5m ./internal/agent -run '^(TestDsh|TestKeepDsh|TestVSCodeDetected)'
if ($LASTEXITCODE -ne 0) { throw 'Windows agent checks failed' }

Write-Host '=== Build actual Windows x64 desktop application ==='
go build -tags production -trimpath -o (Join-Path $env:RUNNER_TEMP 'magpie-gui-amd64.exe') .
if ($LASTEXITCODE -ne 0) { throw 'Windows x64 desktop compile failed' }

Write-Host '=== Validate key GUI and LM Detector JavaScript syntax ==='
foreach ($asset in @(
  'internal/gui/assets/app.js',
  'internal/gui/assets/kernel.js',
  'internal/gui/assets/fork/codex-caller-prompt.js',
  'internal/gui/assets/plugins.js',
  'internal/gui/assets/pluginpages.js',
  'internal/gui/pagebridge.js',
  'plugins/lm-detector/index.js'
)) {
  node --check $asset
  if ($LASTEXITCODE -ne 0) { throw "JavaScript syntax failed: $asset" }
}
Write-Host 'Fast Windows quality gate passed.'
