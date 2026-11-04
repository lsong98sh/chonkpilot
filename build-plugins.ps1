# build-plugins.ps1：构建「manifest 插件」（PLUGIN-1）。
#
# 产物（staging）：build/plugin-apps/<id>/<exe>（2026-11-02：移出 dist —— 发行根只允许
#   chonkpilot.exe · chonkpilot-remote-server.exe(+.db) · themes\ · capability\）
#   ├── web/       chonkpilot-web.exe                          # web_search / web_fetch / web_download
#   ├── codegraph/  chonkpilot-plugin-codegraph.exe + zvec_c_api.dll
#   ├── browser/   chonkpilot-plugin-browser.exe + chonkpilot-browser-plugin.exe（引擎）
#   ├── desktop/   chonkpilot-plugin-desktop.exe + chonkpilot-desktop-plugin.exe（引擎）
#   ├── file/      chonkpilot-plugin-file.exe + tools/file/*.tool.md（11 契约）
#   ├── task/      chonkpilot-plugin-task.exe（执行 / LLM / 状态 / 终止）
#   ├── crontab/   chonkpilot-plugin-crontab.exe（本机全局定时任务）
#   ├── mcp/       chonkpilot-plugin-mcp.exe（检索 / 加载 / 调用）
#   ├── memory/    chonkpilot-memory.exe（记忆沉淀；复用 chonkpilot-plugin-memory 业务包）
#   └── history/   chonkpilot-history.exe（文件检查点链；复用 chonkpilot-plugin-history 业务包）
#
# 注：`compress` 不再是 manifest 插件（SESSION-1，2026-10-23：压缩内建在会话侧，见
# `src/lib/llm/server/compress.go`）——本脚本不再构建 / 投放该插件。
# 注：memory / history 于 2026-10-24 从「宿主编译期内嵌 Hook」改为独立 exe 插件（外挂），
# 行为 / 数据口径 / 消息面不变；宿主不再编译期持有它们（见 appwire.DefaultPlugins）。
#
# 部署：由 build-desktop.ps1 把 staging 拷进 `dist/plugins/<id>/`
# （`plugin.json`（极简启动信息）+ `manifest.json`（完整 manifest，插件自读上报）来自
#  src/initdata/capability/plugins/<id>/，exe 必须与之**同目录**）。
# webui 的投放（PLUGIN-UI-1）：本脚本产出的插件**全部** `ui.selfServe: true` ——
# webui 源随插件源码（`chonkpilot-plugins/<id>/src/webui/`；memory/history 目前无 webui），
# 经 `//go:embed all:webui` 打进 exe，产物**不带** webui/。
# 例外：demo-python 是 Python 样例（runtime.exe = python，product=false，不随产物投放），
# 不支持 embed，其页面仍在 `src/initdata/capability/plugins/demo-python/webui/`。
#
# 目录口径（TOPO-1，2026-10-29）：插件仓 `<id>/src`（Go 源码 + webui/engines）+ `<id>/doc`；
# 本脚本的 `-Mod` 一律指向 `<id>\src`（staging 与运行时路径不变）。
#
# 说明：codegraph 合并 codegraph + vfts 两个引擎（CGO：tree-sitter + zvec），
# 故本脚本对 codegraph 复用 build-vfts.ps1 的 zvec 头/库路径。
#
# 用法：.\build-plugins.ps1 [-SkipTidy]
param(
    [switch]$SkipTidy
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

# ── 工具链（对齐 build-vfts.ps1 / build-codegraph.ps1）──
$env:GOROOT = "e:\GoDev\go1.26"
$env:CHONK_MSYS64_BIN = "e:\GoDev\msys64\ucrt64\bin"
$env:Path = (Join-Path $env:GOROOT "bin") + ";" + $env:CHONK_MSYS64_BIN + ";" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"
$env:CGO_ENABLED = "1"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
# 进程外插件源码仓库（2026-10-24 分离）：web/codegraph/browser/desktop/file/task/mcp/memory/history
# 均与主仓同级；memory/history 的 Go 业务包在同仓 `<id>/lib`。
$pluginsRepo = Join-Path (Split-Path -Parent $root) "chonkpilot-plugins"
$out = Join-Path $root "build\plugin-apps"

$zvec = Join-Path $pluginsRepo "codegraph\src\engines\vfts\third_party\zvec"
$zvecLib = Join-Path $zvec "windows_amd64"
$env:CGO_CFLAGS = "-I$($zvec.Replace('\','/'))/include"
$env:CGO_LDFLAGS = "-L$($zvecLib.Replace('\','/')) -lzvec_c_api"

if (-not (Test-Path (Join-Path $zvecLib "zvec_c_api.dll"))) {
    throw "zvec 运行库缺失：$zvecLib\zvec_c_api.dll"
}

New-Item -ItemType Directory -Force -Path $out | Out-Null

# 旧实例占用 exe/dll → 先停（插件由宿主拉起，构建前应已关闭宿主）
Get-Process -Name "chonkpilot-web", "chonkpilot-plugin-codegraph", "chonkpilot-memory", "chonkpilot-history" -ErrorAction SilentlyContinue | Stop-Process -Force

function Build-Plugin {
    param(
        [string]$Mod,        # 源码目录（绝对路径）
        [string]$ID,         # 插件 id
        [string]$ExeName     # 产物 exe 名
    )
    $dst = Join-Path $out $ID
    New-Item -ItemType Directory -Force -Path $dst | Out-Null
    Push-Location $Mod
    try {
        if (-not $SkipTidy) {
            go mod tidy
            if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed: $Mod" }
        }
        go build -o (Join-Path $dst $ExeName) .
        if ($LASTEXITCODE -ne 0) { throw "build failed: $Mod -> $ExeName" }
    } finally { Pop-Location }
    $exe = Get-Item (Join-Path $dst $ExeName)
    Write-Host ("    ok: {0} ({1} MB)" -f $exe.Name, [math]::Round($exe.Length / 1MB, 1))
}

# 引擎源码随插件（PLUGIN-SPLIT-1）：直接从 plugins 仓构建（GUI 子系统常驻 + 托盘）。
function Build-Engine {
    param(
        [string]$Mod,        # 引擎源码目录（绝对路径）
        [string]$ID,         # 插件 id（产物落到 <out>/<ID>/）
        [string]$ExeName     # 产物 exe 名
    )
    $dst = Join-Path $out $ID
    New-Item -ItemType Directory -Force -Path $dst | Out-Null
    Push-Location $Mod
    try {
        go build -ldflags "-H windowsgui" -o (Join-Path $dst $ExeName) .
        if ($LASTEXITCODE -ne 0) { throw "build engine failed: $Mod -> $ExeName" }
    } finally { Pop-Location }
    $exe = Get-Item (Join-Path $dst $ExeName)
    Write-Host ("    ok: {0} ({1} MB)" -f $exe.Name, [math]::Round($exe.Length / 1MB, 1))
}

Write-Host "==> [1/11] web"
Build-Plugin -Mod (Join-Path $pluginsRepo "web\src") -ID "web" -ExeName "chonkpilot-web.exe"

Write-Host "==> [2/11] codegraph (codegraph + vfts, CGO)"
Build-Plugin -Mod (Join-Path $pluginsRepo "codegraph\src") -ID "codegraph" -ExeName "chonkpilot-plugin-codegraph.exe"
Copy-Item (Join-Path $zvecLib "zvec_c_api.dll") (Join-Path $out "codegraph") -Force
Write-Host "    ok: zvec_c_api.dll"

Write-Host "==> [3/11] browser（桥接层 + 引擎：源码在 plugins 仓 browser/engine）"
Build-Plugin -Mod (Join-Path $pluginsRepo "browser\src") -ID "browser" -ExeName "chonkpilot-plugin-browser.exe"
Build-Engine -Mod (Join-Path $pluginsRepo "browser\src\engine") -ID "browser" -ExeName "chonkpilot-browser-plugin.exe"

Write-Host "==> [4/11] desktop（桥接层 + 引擎：源码在 plugins 仓 desktop/engine）"
Build-Plugin -Mod (Join-Path $pluginsRepo "desktop\src") -ID "desktop" -ExeName "chonkpilot-plugin-desktop.exe"
Build-Engine -Mod (Join-Path $pluginsRepo "desktop\src\engine") -ID "desktop" -ExeName "chonkpilot-desktop-plugin.exe"

# file 插件的工具契约：唯一源已随插件迁至 `chonkpilot-plugins/file/tools/file`（2026-10-25），
# 运行时由插件从 `<pluginRoot>/tools/file/` 解析；主仓 capability 根不再含 file 契约
# （否则与插件重复注册 → gateway 冲突）。
Write-Host "==> [5/11] file（文件系列 11 工具）"
Build-Plugin -Mod (Join-Path $pluginsRepo "file\src") -ID "file" -ExeName "chonkpilot-plugin-file.exe"
$fileContractsSrc = Join-Path $pluginsRepo "file\src\tools\file"
# 运行时由插件从 `<pluginRoot>/tools/file/` 解析（绝不能落到 `<pluginRoot>/src/tools/file/`：
# 2026-11-02 实测该错位导致 file 插件启动即 `加载契约失败` → exit 1 → 重启 3 次后停摆）。
$fileContractsDst = Join-Path $out "file\tools\file"
if (Test-Path $fileContractsSrc) {
    Remove-Item -LiteralPath $fileContractsDst -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $fileContractsDst | Out-Null
    Copy-Item (Join-Path $fileContractsSrc "*.tool.md") $fileContractsDst -Force
    Write-Host ("    ok: tools/file (" + (Get-ChildItem $fileContractsDst -File).Count + " 契约)")
} else {
    throw "file 契约缺失：$fileContractsSrc"
}

Write-Host "==> [6/11] task（执行 / LLM / 状态 / 终止）"
Build-Plugin -Mod (Join-Path $pluginsRepo "task\src") -ID "task" -ExeName "chonkpilot-plugin-task.exe"

Write-Host "==> [7/11] crontab（本机全局定时任务）"
Build-Plugin -Mod (Join-Path $pluginsRepo "crontab\src") -ID "crontab" -ExeName "chonkpilot-plugin-crontab.exe"

Write-Host "==> [8/11] mcp（检索 / 加载 / 调用）"
Build-Plugin -Mod (Join-Path $pluginsRepo "mcp\src") -ID "mcp" -ExeName "chonkpilot-plugin-mcp.exe"

Write-Host "==> [9/11] memory（记忆沉淀；业务包在 plugins 仓 memory/lib）"
Build-Plugin -Mod (Join-Path $pluginsRepo "memory\src") -ID "memory" -ExeName "chonkpilot-memory.exe"

Write-Host "==> [10/11] history（文件检查点链；业务包在 plugins 仓 history/lib）"
Build-Plugin -Mod (Join-Path $pluginsRepo "history\src") -ID "history" -ExeName "chonkpilot-history.exe"

# Remote 插件（2026-11-01）：源码在 plugins 仓 remote/client（与 remote/server 同目录、分两个工程）。
Write-Host "==> [11/11] remote（远程接入：出站 WSS 到 Remote Server）"
Build-Plugin -Mod (Join-Path $pluginsRepo "remote\client") -ID "remote" -ExeName "chonkpilot-plugin-remote.exe"

# ── 部署到桌面单体产物（存在才部署）──
$desktopPlugins = Join-Path $root "dist\plugins"
if (Test-Path $desktopPlugins) {
    Write-Host "==> deploy -> $desktopPlugins"
    foreach ($id in @("web", "codegraph", "browser", "desktop", "file", "task", "crontab", "mcp", "memory", "history", "remote")) {
        $srcDir = Join-Path $out $id
        $dstDir = Join-Path $desktopPlugins $id
        if (-not (Test-Path $dstDir)) {
            Write-Host "    skip (未发现 manifest 目录): $dstDir"
            continue
        }
        # 递归：file 插件带 tools/file/ 契约目录
        Copy-Item (Join-Path $srcDir "*") $dstDir -Recurse -Force
        Write-Host "    ok: $id"
    }
} else {
    Write-Host "==> skip deploy（dist\plugins 不存在；先跑 .\build-desktop.ps1）"
}

Write-Host "==> done. staging: $out"
