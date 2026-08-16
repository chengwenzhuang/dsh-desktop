# install.ps1 — 在（新）机器上安装 / 迁移 dsh-session-delete 插件。
#
# 用法：把整个 dsh-session-delete 文件夹拷到新机器任意位置，然后
#       powershell -ExecutionPolicy Bypass -File install.ps1
#
# 脚本自动完成三件事：
#   1) 在 web profile 的 node_modules 下建 junction（默认指向脚本所在目录，
#      以后改代码只改一处；失败时回退为直接复制）
#   2) 在 cordis.patch.yml 注册 - id: session-delete（幂等，已存在则跳过）
#   3) 运行 patch-ui-workspace.js 重打 dsh-client-ui-workspace 菜单补丁（幂等）
# 最后重启 DSH 服务（托盘图标 → 重启服务）生效。
param(
    [string]$DshHome = ""
)
$ErrorActionPreference = "Stop"

$pluginDir = $PSScriptRoot

# 确定 DSH 数据目录（$DSH_HOME 优先，其次 ~/.dsh）
if ($DshHome -eq "") {
    if ($env:DSH_HOME -and $env:DSH_HOME.Trim() -ne "") { $DshHome = $env:DSH_HOME }
    else { $DshHome = Join-Path $HOME ".dsh" }
}
$profileDir = Join-Path $DshHome "profiles\web"
$nodeModules = Join-Path $profileDir "node_modules"
$link = Join-Path $nodeModules "dsh-session-delete"
$patchFile = Join-Path $profileDir "cordis.patch.yml"

Write-Host "== dsh-session-delete install =="
Write-Host "plugin dir : $pluginDir"
Write-Host "dsh home   : $DshHome"

if (-not (Test-Path $profileDir)) {
    Write-Host "ERROR: web profile not found at $profileDir" -ForegroundColor Red
    Write-Host "请先在该机器上至少启动过一次 dsh web（或 DSH 桌面版），让它自动生成 profile。" -ForegroundColor Red
    exit 1
}
New-Item -ItemType Directory -Force -Path $nodeModules | Out-Null

# ── 1) junction / copy ─────────────────────────────────────────────────────
if (Test-Path $link) {
    $item = Get-Item $link -Force
    if ($item.LinkType -eq "Junction" -or $item.LinkType -eq "SymbolicLink") {
        # rmdir 只删联接本身，绝不删除目标目录
        cmd /c rmdir "$link"
        Write-Host "[1/3] removed stale junction at $link"
    } else {
        Remove-Item $link -Recurse -Force
        Write-Host "[1/3] removed stale copy at $link"
    }
}
$linked = $false
try {
    New-Item -ItemType Junction -Path $link -Target $pluginDir -ErrorAction Stop | Out-Null
    $linked = $true
    Write-Host "[1/3] junction created: $link -> $pluginDir"
} catch {
    Copy-Item -Path $pluginDir -Destination $link -Recurse -Force
    Write-Host "[1/3] junction failed, fell back to a copy at $link" -ForegroundColor Yellow
}

# ── 2) register in cordis.patch.yml ────────────────────────────────────────
$raw = if (Test-Path $patchFile) { Get-Content $patchFile -Raw } else { "" }
if ($raw -match "dsh-session-delete") {
    Write-Host "[2/3] already registered in cordis.patch.yml"
} else {
    $block = @(
        "",
        "# ── dsh-session-delete: 会话 ⋮ 菜单删除 ────────────────────────────────",
        "- insert:",
        "    - id: session-delete",
        "      name: 'dsh-session-delete'"
    ) -join [Environment]::NewLine
    Add-Content -Path $patchFile -Value $block
    Write-Host "[2/3] registered in cordis.patch.yml"
}

# ── 3) patch the ui-workspace bundle ───────────────────────────────────────
Write-Host "[3/3] patching dsh-client-ui-workspace bundle..."
Push-Location $pluginDir
try { node patch-ui-workspace.js } finally { Pop-Location }

Write-Host ""
Write-Host "done. 重启 DSH 服务生效（托盘图标右键 → 重启服务，或重开 DSH.exe）。"
if (-not $linked) {
    Write-Host "提示：当前是复制安装，以后改插件代码需重新复制；若能建 junction 则无需同步。" -ForegroundColor Yellow
}