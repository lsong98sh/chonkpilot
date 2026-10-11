# build-mcps.ps1：冻结 ChonkPilot 文档转换服务（mcps/markitdown）为 PyInstaller 产物
#
# 产物布局（dist/desktop/mcps/markitdown/）：
#   ├── markitdown-mcp.exe        # PyInstaller 冻结产物（MCP + /vfts 双入口，只监听 127.0.0.1）
#   │                             #   默认 --onedir（目录模式）；-Onefile 时改为单文件
#   ├── _internal/                # onedir 依赖目录（仅默认模式产出；-Onefile 时不产出）
#   ├── README.md                 # 启动方式 / 状态文件 / 两个入口 / MCP 注册指引文案
#   └── THIRD-PARTY-NOTICES.txt   # 第三方组件许可清单
#
# 说明：
#  - 本脚本**独立**，**不接入** build-desktop.ps1 的必经路径（冻结慢）。
#  - 前置：已在 src/mcps/markitdown 建好 .venv 并 pip install -r requirements.txt。
#  - 产物随产品发，但**默认不启动**（拉起/注册属插件工作）。
#  - **冻结模式**：
#      * onedir（默认）≈ 目录模式（exe + _internal/），**冷启动 ≈4.4s**（实测，无解压步骤）；
#        代价是产物为目录而非单文件（投放时 exe 与 _internal/ 须一并保留相对布局）。
#      * onefile（`-Onefile`）≈ 单文件、便于分发，但**冷启动慢**——实测 **≈9.6s** 到 /vfts/health 可用
#        （自解压载荷到临时目录，占其中约 9s）。
#      * 插件侧探测逻辑对两种模式无感（状态文件恒与 exe 同目录）。
#
# 用法：
#   .\src\mcps\markitdown\build-mcps.ps1            # onedir（默认，冷启动快）
#   .\src\mcps\markitdown\build-mcps.ps1 -Onefile   # onefile（单文件，冷启动慢）

param(
    [switch]$Onefile
)

$ErrorActionPreference = "Stop"

$here = Split-Path -Parent $MyInvocation.MyCommand.Path                                   # src\mcps\markitdown
$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $here))            # 仓库根
$venvPy = Join-Path $here ".venv\Scripts\python.exe"
$dist = Join-Path $repoRoot "dist\desktop\mcps\markitdown"
$work = Join-Path $here "build"

# ── 破坏性操作防护（2026-10-11；本仓曾发生误删用户主目录事故，见 .trae/rules 全局规则）──
# 删除目标必须落在**仓库根 $repoRoot 之内**（严格子路径），越界即 throw 中止。
# 目标一律以**字面量绝对路径**传入（禁止调用方用变量拼接/通配符指向用户目录）。
function Remove-SafeItem {
    param([Parameter(Mandatory)][string]$LiteralPath, [switch]$Recurse)
    $resolved = Resolve-Path -LiteralPath $LiteralPath -ErrorAction SilentlyContinue
    if ($null -eq $resolved) { return }   # 目标不存在 = 无操作
    $p = $resolved.Path.TrimEnd('\')
    $rootPath = (Resolve-Path -LiteralPath $repoRoot).Path.TrimEnd('\')
    if ($p -notlike "$rootPath\*") {      # 白名单：仅仓库根之内的严格子路径
        throw "拒绝删除仓库外路径: $p（仅允许 $rootPath 之内）"
    }
    Remove-Item -LiteralPath $p -Recurse:$Recurse -Force
}

if (-not (Test-Path $venvPy)) {
    throw "venv 缺失：$venvPy`n先执行： python -m venv .venv ; .\.venv\Scripts\python.exe -m pip install -r requirements.txt"
}

New-Item -ItemType Directory -Force -Path $dist | Out-Null
New-Item -ItemType Directory -Force -Path $work | Out-Null

# 释放旧实例对 exe 的占用
Get-Process -Name "markitdown-mcp" -ErrorAction SilentlyContinue | Stop-Process -Force

$freezeMode = if ($Onefile) { "--onefile" } else { "--onedir" }
Write-Host "==> freeze markitdown-mcp (PyInstaller $freezeMode)"
# 说明：
#  - markitdown / mcp / pptx / docx / openpyxl / onnxruntime / pypdfium2 / lxml / cryptography
#    均有静态 import 或 PyInstaller 内置 hook，无需 --collect-all（mcp.cli 依赖可选的 typer，
#    collect-all 会因缺包失败，故一律不做 collect-all mcp）。
#  - 必须显式收集的两处**数据**：magika 的 ONNX 模型、pdfminer.six 的 cmap 表。
#  - uvicorn 用 importlib 动态加载 loops/protocols/lifespan 子模块，静态分析看不到，必须 collect-all。
$pyiArgs = @(
    "--noconfirm", "--clean", $freezeMode,
    "--name", "markitdown-mcp",
    "--distpath", $dist,
    "--workpath", $work,
    "--specpath", $work,
    "--collect-data", "magika",
    "--collect-data", "pdfminer",
    "--collect-all", "uvicorn",
    "--hidden-import", "mcp_types",
    "--exclude-module", "tkinter",
    (Join-Path $here "server.py")
)
& $venvPy -m PyInstaller @pyiArgs
if ($LASTEXITCODE -ne 0) { throw "PyInstaller 冻结失败（exit $LASTEXITCODE）" }

if ($freezeMode -eq "--onedir") {
    # onedir → PyInstaller 产出 $dist\markitdown-mcp\ { markitdown-mcp.exe + _internal\ }
    # 平铺到 $dist，使投放布局与 onefile 完全一致
    # （状态文件 = 与 exe 同目录，插件探测路径依赖该布局，故不能留子目录）。
    $appDir = Join-Path $dist "markitdown-mcp"
    if (-not (Test-Path $appDir)) { throw "onedir 未产出 $appDir" }
    foreach ($it in (Get-ChildItem -Force $appDir)) {
        $dst = Join-Path $dist $it.Name
        Remove-SafeItem -LiteralPath $dst -Recurse   # 覆盖上一次 onedir 残留
        Move-Item -Force $it.FullName -Destination $dist
    }
    Remove-SafeItem -LiteralPath $appDir -Recurse
} else {
    # onefile：清理上一次 onedir 可能残留的 _internal\（避免新旧模式产物叠加）
    $stale = Join-Path $dist "_internal"
    Remove-SafeItem -LiteralPath $stale -Recurse
}

$exe = Join-Path $dist "markitdown-mcp.exe"
if (-not (Test-Path $exe)) { throw "未产出 $exe" }

Copy-Item (Join-Path $here "README.md") $dist -Force
Copy-Item (Join-Path $here "THIRD-PARTY-NOTICES.txt") $dist -Force

$item = Get-Item $exe
Write-Host ("    ok: markitdown-mcp.exe ({0:N1} MB, mode={1})" -f ($item.Length / 1MB), $freezeMode)
Write-Host "==> done. 产物目录：$dist"
Write-Host "    运行： & '$exe'                                  # 默认 127.0.0.1:7317，端口被占自动另选"
Write-Host "    探活： Invoke-RestMethod http://127.0.0.1:7317/vfts/health"
Write-Host "    状态： 状态文件 = 与 exe 同目录的 state.json（目录只读时回落 %LOCALAPPDATA%\chonkpilot\mcps\markitdown）"
if ($freezeMode -eq "--onedir") {
    Write-Host "    提示： onedir 模式冷启动 ≈4.4s（实测）；投放时 $dist 下 exe 与 _internal\ 须一并保留相对布局"
} else {
    Write-Host "    提示： PyInstaller --onefile 产物可能被杀软启发式误报，需要时加白名单；冷启动 ≈9.6s"
}
