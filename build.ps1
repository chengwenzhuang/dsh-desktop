# Builds DSH.exe (Windows GUI, single file, no console window).
#   .\build.ps1            -> slim DSH.exe (uses system Node.js + auto dsh bootstrap)
#   .\build.ps1 -Full      -> DSH-full.exe with embedded Node.js + dsh runtime
#                             (fully standalone, works without any installed Node)
param([switch]$Full)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

# --- Locate the Go toolchain -----------------------------------------------
$goExe = $env:DSH_GO
if (-not $goExe) {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { $goExe = $cmd.Source }
}
if (-not $goExe) {
    $candidates = @(
        "$root\tools\go\bin\go.exe",
        "$env:ProgramFiles\Go\bin\go.exe",
        "$env:LOCALAPPDATA\Programs\Go\bin\go.exe"
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { $goExe = $c; break }
    }
}
if (-not $goExe) { throw "未找到 Go 工具链。请安装 Go，或设置环境变量 DSH_GO 指向 go.exe。" }
Write-Host "Using Go: $goExe"

# --- Regenerate the icon if missing ----------------------------------------
if (-not (Test-Path "$root\icon.png") -or -not (Test-Path "$root\icon.ico")) {
    & "$root\tools\make-icon.ps1"
    & $goExe run ./tools/png2ico -src "$root" -out "$root\icon.ico"
}

# --- Regenerate Windows resources (icon/manifest/version) ------------------
# The committed rsrc_windows_amd64.syso is used when go-winres is unavailable.
$winres = Get-Command go-winres -ErrorAction SilentlyContinue
if ($winres) {
    Write-Host "Regenerating resources with go-winres..."
    & $winres.Source make --in winres.json --arch amd64
} else {
    Write-Host "go-winres not found; keeping existing rsrc_windows_amd64.syso"
}

# --- Rebuild the embedded runtime bundle for -Full -------------------------
$tags = @()
if ($Full) {
    if (-not (Test-Path "$root\runtime.zip")) {
        throw "runtime.zip 不存在。请先准备 third_party\dsh-runtime 并执行: go run ./tools/mkzip -src third_party\dsh-runtime -out runtime.zip"
    }
    $tags += "embedded"
    Write-Host "Building DSH-full.exe with embedded Node.js + dsh runtime..."
}

# --- Build -----------------------------------------------------------------
$out = if ($Full) { "DSH-full.exe" } else { "DSH.exe" }
$tagArg = if ($tags.Count -gt 0) { @("-tags", ($tags -join ",")) } else { @() }
& $goExe build -mod=vendor $tagArg -ldflags "-H windowsgui -s -w" -o $out .
if ($LASTEXITCODE -ne 0) { throw "go build failed" }
$size = (Get-Item "$root\$out").Length
Write-Host "Built: $root\$out ($([math]::Round($size/1MB, 1)) MB)"
