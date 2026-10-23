# Shared quality gate for the Windows-only fork. Run at the repository root.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

Write-Host '=== Vet every Go package ==='
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

Write-Host '=== Run the complete Windows Go test suite ==='
# Restrict package and test parallelism to reduce contention on hosted Windows.
go test -count=1 -p 2 -parallel 4 -timeout 30m ./...
if ($LASTEXITCODE -ne 0) { throw 'Full Windows Go test suite failed' }

Write-Host '=== Build GUI and CLI for Windows x64 and ARM64 ==='
$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
foreach ($arch in @('amd64', 'arm64')) {
  $env:GOARCH = $arch
  go build -tags production -trimpath -o (Join-Path $env:RUNNER_TEMP "magpie-gui-$arch.exe") .
  if ($LASTEXITCODE -ne 0) { throw "Windows GUI $arch build failed" }
  go build -tags nogui -trimpath -o (Join-Path $env:RUNNER_TEMP "magpie-cli-$arch.exe") .
  if ($LASTEXITCODE -ne 0) { throw "Windows CLI $arch build failed" }
}
Remove-Item Env:GOARCH
Remove-Item Env:GOOS
Remove-Item Env:CGO_ENABLED

Write-Host '=== Check critical Windows UI and plugin JavaScript syntax ==='
foreach ($asset in @(
  'internal/gui/assets/app.js',
  'internal/gui/assets/plugins.js',
  'internal/gui/assets/pluginpages.js',
  'internal/gui/pagebridge.js'
)) {
  node --check $asset
  if ($LASTEXITCODE -ne 0) { throw "JavaScript syntax failed: $asset" }
}

Write-Host '=== Chromium UI smoke tests for fork plugin features ==='
# Playwright is a CI-only temporary dependency, never a runtime dependency.
$deps = Join-Path $env:RUNNER_TEMP 'magpie-playwright'
npm install --prefix $deps --no-save --no-audit --no-fund playwright@1.62.1
if ($LASTEXITCODE -ne 0) { throw 'Playwright setup failed' }
$env:NODE_PATH = Join-Path $deps 'node_modules'
node (Join-Path $env:NODE_PATH 'playwright/cli.js') install chromium
if ($LASTEXITCODE -ne 0) { throw 'Chromium installation failed' }
$env:BROWSER = 'chromium'
node --test --test-concurrency=1 internal/gui/tests/plugin-page.test.cjs internal/gui/tests/plugin-actions.test.cjs internal/gui/tests/plugin-tools.test.cjs
if ($LASTEXITCODE -ne 0) { throw 'Chromium plugin UI tests failed' }
Write-Host 'All Windows quality checks passed.'
