param(
    [string]$UpstreamCommit = ""
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$dest = Join-Path $repoRoot "prompts\skills\agent-reach"
$destRefs = Join-Path $dest "references"
$hostProfile = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
$venvPy = Join-Path $hostProfile ".agent-reach-venv\Scripts\python.exe"

if (-not (Test-Path $venvPy)) {
    throw "Agent Reach venv Python not found: $venvPy"
}

$probe = & $venvPy -X utf8 -c "import pathlib, agent_reach; p=pathlib.Path(agent_reach.__file__).resolve().parent/'skill'; print(p); print(getattr(agent_reach,'__version__','unknown'))"
if ($LASTEXITCODE -ne 0 -or $probe.Count -lt 2) {
    throw "Unable to locate installed Agent Reach skill package"
}

$source = [string]$probe[0]
$version = [string]$probe[1]
$sourceRefs = Join-Path $source "references"

if (-not (Test-Path (Join-Path $source "SKILL.md"))) {
    throw "Installed Agent Reach SKILL.md not found: $source"
}

New-Item -ItemType Directory -Force -Path $destRefs | Out-Null
Copy-Item -LiteralPath (Join-Path $source "SKILL.md") -Destination (Join-Path $dest "SKILL.md") -Force

$sourceRefFiles = @(Get-ChildItem -LiteralPath $sourceRefs -Filter "*.md" -File | Sort-Object Name)
$sourceNames = @($sourceRefFiles | ForEach-Object { $_.Name })

Get-ChildItem -LiteralPath $destRefs -Filter "*.md" -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -notin $sourceNames } |
    Remove-Item -Force

foreach ($file in $sourceRefFiles) {
    Copy-Item -LiteralPath $file.FullName -Destination (Join-Path $destRefs $file.Name) -Force
}

$files = [ordered]@{}
$skillHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $dest "SKILL.md")).Hash.ToLowerInvariant()
$files["SKILL.md"] = $skillHash
foreach ($file in (Get-ChildItem -LiteralPath $destRefs -Filter "*.md" -File | Sort-Object Name)) {
    $files["references/$($file.Name)"] = (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLowerInvariant()
}

$manifest = [ordered]@{
    repository = "https://github.com/Panniantong/Agent-Reach"
    package_version = $version
    upstream_commit = $UpstreamCommit
    source = "installed agent_reach package"
    files = $files
}

$json = $manifest | ConvertTo-Json -Depth 6
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[IO.File]::WriteAllText((Join-Path $dest "UPSTREAM.json"), $json + "`n", $utf8NoBom)

Write-Output "Synced Agent Reach Skill"
Write-Output "Version: $version"
if ($UpstreamCommit) {
    Write-Output "Upstream commit: $UpstreamCommit"
}
Write-Output "Source: $source"
Write-Output "Destination: $dest"
