# build-plugins.ps1：构建「manifest 插件」（PLUGIN-1）。
#
# 产物（staging）：dist/plugin-apps/<id>/<exe>
#   ├── websearch/ chonkpilot-websearch.exe                  # web_search / web_fetch / web_download
#   ├── codebase/  chonkpilot-plugin-codebase.exe + zvec_c_api.dll
#   ├── browser/   chonkpilot-plugin-browser.exe + chonkpilot-browser-plugin.exe（引擎）
#   ├── desktop/   chonkpilot-plugin-desktop.exe + chonkpilot-desktop-plugin.exe（引擎）
#   ├── file/      chonkpilot-plugin-file.exe + tools/file/*.tool.md（11 契约）
#   ├── task/      chonkpilot-plugin-task.exe（执行 / LLM / 状态 / 终止）
#   ├── mcp/       chonkpilot-plugin-mcp.exe（检索 / 加载 / 调用）
#   ├── memory/    chonkpilot-memory.exe（记忆沉淀；复用 chonkpilot-plugin-memory 业务包）
#   └── history/   chonkpilot-history.exe（文件检查点链；复用 chonkpilot-plugin-history 业务包）
#
# 注：`compress` 不再是 manifest 插件（SESSION-1，2026-10-23：压缩内建在会话侧，见
# `src/lib/llm/server/compress.go`）——本脚本不再构建 / 投放该插件。
# 注：memory / history 于 2026-10-24 从「宿主编译期内嵌 Hook」改为独立 exe 插件（外挂），
# 行为 / 数据口径 / 消息面不变；宿主不再编译期持有它们（见 appwire.DefaultPlugins）。
#
# 部署：由 build-desktop.ps1 把 staging 拷进 `dist/desktop/capability/plugins/<id>/`
# （plugin.json 来自 src/initdata/capability/plugins/<id>/，exe 必须与之**同目录**，
#  manifest 的 runtime.exe 是插件目录内相对路径）。
# webui 的投放（PLUGIN-UI-1）：本脚本产出的插件**全部** `ui.selfServe: true` ——
# webui 源随插件源码（进程外插件在 `chonkpilot-plugins/<id>/webui/`，memory/history 在
# `src/plugins/<id>/webui/`），经 `//go:embed all:webui` 打进 exe，产物**不带** webui/。
# 例外：demo-python 是 Python 样例（runtime.exe = python，product=false，不随产物投放），
# 不支持 embed，其页面仍在 `src/initdata/capability/plugins/demo-python/webui/`。
#
# 说明：codebase 合并 codegraph + vfts 两个引擎（CGO：tree-sitter + zvec），
# 故本脚本对 codebase 复用 build-vfts.ps1 的 zvec 头/库路径。
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
$srcPlugins = Join-Path $root "src\plugins"
# 进程外插件源码仓库（2026-10-24 分离）：websearch/codebase/browser/desktop/file/task/mcp/memory/history
# 均与主仓同级；memory/history 的 Go 业务包在同仓 `<id>/lib`。
$pluginsRepo = Join-Path (Split-Path -Parent $root) "chonkpilot-plugins"
$out = Join-Path $root "dist\plugin-apps"

$zvec = Join-Path $srcPlugins "vfts\third_party\zvec"
$zvecLib = Join-Path $zvec "windows_amd64"
$env:CGO_CFLAGS = "-I$($zvec.Replace('\','/'))/include"
$env:CGO_LDFLAGS = "-L$($zvecLib.Replace('\','/')) -lzvec_c_api"

if (-not (Test-Path (Join-Path $zvecLib "zvec_c_api.dll"))) {
    throw "zvec 运行库缺失：$zvecLib\zvec_c_api.dll"
}

New-Item -ItemType Directory -Force -Path $out | Out-Null

# 旧实例占用 exe/dll → 先停（插件由宿主拉起，构建前应已关闭宿主）
Get-Process -Name "chonkpilot-websearch", "chonkpilot-plugin-codebase", "chonkpilot-memory", "chonkpilot-history" -ErrorAction SilentlyContinue | Stop-Process -Force

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

# 引擎 exe 由 build-mcp-server.ps1 产出（capability/executors/），拷进插件目录（与桥接层同目录）。
function Copy-Engine {
    param([string]$ID, [string]$EngineName)
    $candidates = @(
        (Join-Path $root "dist\other\capability\executors\$EngineName"),
        (Join-Path $root "dist\desktop\capability\executors\$EngineName")
    )
    $src = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-not $src) { throw "引擎产物缺失：$EngineName（先跑 .\build-mcp-server.ps1）" }
    Copy-Item $src (Join-Path $out $ID) -Force
    Write-Host "    ok: $EngineName (engine)"
}

Write-Host "==> [1/9] websearch"
Build-Plugin -Mod (Join-Path $pluginsRepo "websearch") -ID "websearch" -ExeName "chonkpilot-websearch.exe"

Write-Host "==> [2/9] codebase (codegraph + vfts, CGO)"
Build-Plugin -Mod (Join-Path $pluginsRepo "codebase") -ID "codebase" -ExeName "chonkpilot-plugin-codebase.exe"
Copy-Item (Join-Path $zvecLib "zvec_c_api.dll") (Join-Path $out "codebase") -Force
Write-Host "    ok: zvec_c_api.dll"

Write-Host "==> [3/9] browser（桥接层 + 引擎）"
Build-Plugin -Mod (Join-Path $pluginsRepo "browser") -ID "browser" -ExeName "chonkpilot-plugin-browser.exe"
Copy-Engine -ID "browser" -EngineName "chonkpilot-browser-plugin.exe"

Write-Host "==> [4/9] desktop（桥接层 + 引擎）"
Build-Plugin -Mod (Join-Path $pluginsRepo "desktop") -ID "desktop" -ExeName "chonkpilot-plugin-desktop.exe"
Copy-Engine -ID "desktop" -EngineName "chonkpilot-desktop-plugin.exe"

# file 插件的工具契约：唯一源仍是 src/initdata/capability/tools/file（本插件运行时解析），
# 但**不再进入 core capability 根**（否则与插件重复注册 → gateway 冲突）。
Write-Host "==> [5/9] file（文件系列 11 工具）"
Build-Plugin -Mod (Join-Path $pluginsRepo "file") -ID "file" -ExeName "chonkpilot-plugin-file.exe"
$fileContractsSrc = Join-Path $root "src\initdata\capability\tools\file"
$fileContractsDst = Join-Path $out "file\tools\file"
if (Test-Path $fileContractsSrc) {
    Remove-Item -LiteralPath $fileContractsDst -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $fileContractsDst | Out-Null
    Copy-Item (Join-Path $fileContractsSrc "*.tool.md") $fileContractsDst -Force
    Write-Host ("    ok: tools/file (" + (Get-ChildItem $fileContractsDst -File).Count + " 契约)")
} else {
    throw "file 契约缺失：$fileContractsSrc"
}

Write-Host "==> [6/9] task（执行 / LLM / 状态 / 终止）"
Build-Plugin -Mod (Join-Path $pluginsRepo "task") -ID "task" -ExeName "chonkpilot-plugin-task.exe"

Write-Host "==> [7/9] mcp（检索 / 加载 / 调用）"
Build-Plugin -Mod (Join-Path $pluginsRepo "mcp") -ID "mcp" -ExeName "chonkpilot-plugin-mcp.exe"

Write-Host "==> [8/9] memory（记忆沉淀；业务包在 plugins 仓 memory/lib）"
Build-Plugin -Mod (Join-Path $pluginsRepo "memory") -ID "memory" -ExeName "chonkpilot-memory.exe"

Write-Host "==> [9/9] history（文件检查点链；业务包在 plugins 仓 history/lib）"
Build-Plugin -Mod (Join-Path $pluginsRepo "history") -ID "history" -ExeName "chonkpilot-history.exe"

# ── 部署到桌面单体产物（存在才部署）──
$desktopPlugins = Join-Path $root "dist\desktop\capability\plugins"
if (Test-Path $desktopPlugins) {
    Write-Host "==> deploy -> $desktopPlugins"
    foreach ($id in @("websearch", "codebase", "browser", "desktop", "file", "task", "mcp", "memory", "history")) {
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
    Write-Host "==> skip deploy（dist/desktop 不存在；先跑 .\build-desktop.ps1）"
}

Write-Host "==> done. staging: $out"
