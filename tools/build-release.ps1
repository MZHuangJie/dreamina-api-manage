<#
.SYNOPSIS
  Build a distributable release: Go core + Node browser sidecar + web assets.

.NOTES
  This script is intentionally ASCII-only. Windows PowerShell 5.1 reads
  BOM-less .ps1 files as ANSI, which mangles non-ASCII text. All localized
  content lives in tools/release-templates/ instead.
#>
param(
  [switch]$SkipSidecar
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$release = Join-Path $root "dist\release"
$templates = Join-Path $PSScriptRoot "release-templates"

Write-Host "==> Cleaning $release" -ForegroundColor Cyan
if (Test-Path $release) { Remove-Item $release -Recurse -Force }
New-Item -ItemType Directory -Force -Path $release | Out-Null

# ---------- 1. web ----------
Write-Host "==> Building web assets" -ForegroundColor Cyan
Push-Location $root
try {
  & pnpm build
  if ($LASTEXITCODE -ne 0) { throw "web build failed" }
} finally { Pop-Location }
Copy-Item (Join-Path $root "dist\web") (Join-Path $release "web") -Recurse -Force
Write-Host "    web/ ready"

# ---------- 2. Go core ----------
Write-Host "==> Compiling Go core" -ForegroundColor Cyan
Push-Location (Join-Path $root "go")
try {
  $env:CGO_ENABLED = "0"   # modernc.org/sqlite is pure Go; keeps it a single binary
  & go build -trimpath -ldflags "-s -w" -o (Join-Path $release "manager.exe") ./cmd/manager
  if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally { Pop-Location }

$exeSize = [math]::Round((Get-Item (Join-Path $release "manager.exe")).Length / 1MB, 1)
Write-Host "    manager.exe ready ($exeSize MB)"

# ---------- 3. sidecar ----------
Write-Host "==> Preparing browser sidecar" -ForegroundColor Cyan
$sidecarSrc = Join-Path $root "sidecar"
$sidecarDst = Join-Path $release "sidecar"
New-Item -ItemType Directory -Force -Path $sidecarDst | Out-Null
Copy-Item (Join-Path $sidecarSrc "server.mjs") $sidecarDst -Force
Copy-Item (Join-Path $sidecarSrc "package.json") $sidecarDst -Force

$existingModules = Join-Path $sidecarSrc "node_modules"
if (Test-Path $existingModules) {
  Copy-Item $existingModules (Join-Path $sidecarDst "node_modules") -Recurse -Force
  Write-Host "    node_modules copied from source tree"
} elseif (-not $SkipSidecar) {
  Push-Location $sidecarDst
  try {
    & pnpm install --prod 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "sidecar install failed" }
  } finally { Pop-Location }
  Write-Host "    dependencies installed"
} else {
  throw "sidecar/node_modules missing; re-run without -SkipSidecar"
}
Write-Host "    sidecar/ ready"

# ---------- 4. launcher scripts (from templates) ----------
Write-Host "==> Copying launcher scripts" -ForegroundColor Cyan
foreach ($name in @("start.bat", "stop.bat", "README.txt")) {
  $src = Join-Path $templates $name
  if (-not (Test-Path $src)) { throw "missing template: $src" }
  Copy-Item $src (Join-Path $release $name) -Force
}

# ---------- 5. summary ----------
Write-Host ""
Write-Host "==> Done: $release" -ForegroundColor Green
Get-ChildItem $release | ForEach-Object {
  if ($_.PSIsContainer) {
    $bytes = (Get-ChildItem $_.FullName -Recurse -File | Measure-Object Length -Sum).Sum
    $size = "{0:N1} MB" -f ($bytes / 1MB)
  } else {
    $size = "{0:N1} KB" -f ($_.Length / 1KB)
  }
  Write-Host ("    {0,-20} {1}" -f $_.Name, $size)
}
