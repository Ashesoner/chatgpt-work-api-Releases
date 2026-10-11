$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$go = Get-Command go.exe -ErrorAction SilentlyContinue
if (-not $go) { $go = Get-Command go -ErrorAction SilentlyContinue }
if (-not $go) {
    Write-Host 'FAIL: Go was not found in PATH.'
    exit 2
}

Push-Location $repoRoot
try {
    & $go.Source test -count=1 ./internal/v2/tunnel -run '^TestTunnelChildDoesNotOutliveAbruptParent$' -v
    $rc = $LASTEXITCODE
} finally {
    Pop-Location
}
if ($rc -eq 0) {
    Write-Host 'PASS: Tunnel child was reclaimed after abrupt parent termination.'
} else {
    Write-Host ("FAIL: Tunnel orphan regression test returned exit code {0}." -f $rc)
}
exit $rc