# build-desktop.ps1：ChonkPilot 桌面单体（GUI + CLI）构建（无参数）
#
# 命名（2026-09-21，D-27 形态命名重整）：本脚本原名 `build-standalone-gui.ps1`；
#   形态名 `standalone` → **`desktop`（桌面单体）**，产物目录 `dist/standalone/` → `dist/desktop/`。
#   脚本名与内容**仅机械改名**，构建流程/行为**零变更**。
#   **（第二刀，2026-09-21）产物名**：GUI 宿主 `chonkpilot-gui.exe` → **`chonkpilot.exe`**（CLI 名不变）。
#   **（D-28，2026-09-21）源结构**：宿主壳 = **`src/desktop`**（webview2 宿主薄壳，嵌前端 dist +
#   声明形态 `gui.FormDesktop`）；宿主实现唯一一份在 **`src/lib/gui`**（`chonkpilot-gui`）；
#   CLI = **`src/desktop/cli`**（同 lib 集，AsyncMode: never，无前端无 WebView2）。
#
# 架构（单进程内嵌）：chonkpilot.exe = webview2 宿主（`src/desktop` → `src/lib/gui`.Main），内嵌
#   server（会话/任务编排）→ persist（数据面）→ filesys + gateway(内嵌 mcp-server) + 插件，
#   前端 dist 经 go:embed 进 exe（前端源码工程 = 仓库级 src/frontend，embed 入口 embed.html
#   构建后改名 index.html 投放 src/gui/frontend/dist 并**镜像**到 src/desktop/frontend/dist）。
#
# 产物（仓库根 dist\desktop\，决策 [42 §2 (16)]）：
#   ├── chonkpilot.exe                      # webview2 宿主
#   ├── chonkpilot-cli.exe                  # console 宿主
#   ├── chonkpilot-codegraph-mcp-server.exe # codegraph 引擎（出厂内置 MCP）
#   ├── chonkpilot-vfts-mcp-server.exe      # vfts 引擎（出厂内置 MCP）
#   ├── zvec_c_api.dll                      # vfts 运行库（必须与 vfts 引擎 exe 同目录）
#   ├── capability/                         # 契约 + executor×3（来自 dist/other）
#   └── scenarios/                          # app 级场景（出厂默认场景 + 内置智能体集，随发布只读资源；
#                                           #   与 capability/ 平级，25-MCP与场景分层模型 §6 / T6）
# 全量组件：内嵌 lib 插件（compress/history/memory/vfts/codegraph）随 exe 编译；
#   外置引擎 exe（codegraph/vfts）与 zvec_c_api.dll 置于发行根（与 GUI/CLI exe 同级——
#   插件按「宿主 exe 同目录」解析引擎；zvec_c_api.dll 必须与 vfts 引擎 exe 同目录）。
# 用法：.\build-desktop.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
# 宿主壳（GUI 单体）与 CLI 单体（同在 src/desktop 工程内，见 [41 D-28]）
$desktop = Join-Path $root "src\desktop"
$cli = Join-Path $root "src\desktop\cli"
# 前端源码工程已独立为仓库级目录 src/frontend（决策 [42 §2 (115)] / [19 §3.2]）；
# embed 构建落点 = src/gui/frontend/dist（vite build:embed 的 outDir），随后**镜像**到
# src/desktop/frontend/dist —— 桌面单体壳的 `//go:embed all:frontend/dist` 落点。
$frontend = Join-Path $root "src\frontend"
$embedOut = Join-Path $root "src\gui\frontend\dist"
$embedDst = Join-Path $desktop "frontend\dist"
$dist = Join-Path $root "dist\desktop"
$outGuiExe = Join-Path $dist "chonkpilot.exe"
$outCliExe = Join-Path $dist "chonkpilot-cli.exe"

# -- 0) 结束运行中的旧实例（释放 exe 占用；两形态产物同列表） --
foreach ($name in @("chonkpilot", "chonkpilot-cli", "chonkpilot-gui-client", "chonkpilot-cli-client", "chonkpilot-server")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Host "==> kill [$name] PID=$($_.Id)"
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

# -- 1) 前端构建（工程 src/frontend；embed 入口 → 镜像到两个宿主壳的 embed 落点） --
Write-Host "==> [1/6] build frontend (embed entry)"
Push-Location $frontend
try {
    if (-not (Test-Path "node_modules")) {
        Write-Host "    node_modules 缺失，npm ci ..."
        npm ci 2>&1 | ForEach-Object { Write-Host "    $_" }
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
    }
    # stderr warning（如 rollup circular 提示）在 $ErrorActionPreference=Stop 下会被当作
    # terminating ErrorRecord 中断脚本，与真实构建成败无关；构建段临时降为 Continue，
    # 以 $LASTEXITCODE 为唯一判定。
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    npm run build:embed 2>&1 | Select-Object -Last 8
    $ErrorActionPreference = $prevEAP
    if ($LASTEXITCODE -ne 0) { throw "npm run build:embed failed" }
} finally { Pop-Location }

# embed.html → 复制改名 index.html（GUI/WebView2 入口；Go embed 路径与注入逻辑不变）
$embedHtml = Join-Path $embedOut "embed.html"
if (-not (Test-Path $embedHtml)) { throw "embed.html 未产出: $embedHtml" }
Copy-Item $embedHtml (Join-Path $embedOut "index.html") -Force
[System.IO.File]::Delete($embedHtml)
Write-Host "    ok: $embedOut (embed.html -> index.html)"
# 镜像到桌面单体壳的 embed 落点（src/desktop/frontend/dist；go:embed all:frontend/dist）
if (Test-Path $embedDst) { [System.IO.Directory]::Delete($embedDst, $true) }
Copy-Item $embedOut $embedDst -Recurse -Force
Write-Host "    ok: $embedDst ($((Get-ChildItem $embedDst -Recurse -File | Measure-Object).Count) files)"

# -- 2) GUI 单体编译（src/desktop；-H windowsgui：GUI 模式无控制台） --
Write-Host "==> [2/6] build chonkpilot.exe"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $desktop
try {
    go build -ldflags "-H windowsgui" -o $outGuiExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
    Write-Host "    ok: $outGuiExe ($mb MB)"
} finally { Pop-Location }

# -- 3) CLI 单体编译（src/desktop/cli；console 模式，无 -H windowsgui） --
Write-Host "==> [3/6] build chonkpilot-cli.exe"
Push-Location $cli
try {
    go build -o $outCliExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outCliExe).Length / 1MB, 1)
    Write-Host "    ok: $outCliExe ($mb MB)"
} finally { Pop-Location }

# -- 4) 构建 MCP server 契约与 executor（调用 build-mcp-server.ps1 → dist/other） --
Write-Host "==> [4/6] build mcp-server (contracts + executors)"
& (Join-Path $root "build-mcp-server.ps1")
if ($LASTEXITCODE -ne 0) { throw "build-mcp-server.ps1 failed" }

# 复制 capability/ 到发行目录
Write-Host "    -> stage capability/ to dist/desktop"
$capSrc = Join-Path $root "dist\other\capability"
$capDst = Join-Path $dist "capability"
if (Test-Path $capDst) {
    [System.IO.Directory]::Delete($capDst, $true)
}
Copy-Item $capSrc $capDst -Recurse -Force
Write-Host "    ok: capability/ ($((Get-ChildItem $capDst -Recurse -File | Measure-Object).Count) files)"

# 复制 scenarios/ 到发行目录（app 级场景 = 随发布只读资源，与 capability/ **平级**）
Write-Host "    -> stage scenarios/ to dist/desktop"
$scnSrc = Join-Path $root "src\lib\data\scenarios"
if (-not (Test-Path $scnSrc)) { throw "scenarios 资源缺失: $scnSrc" }
$scnDst = Join-Path $dist "scenarios"
if (Test-Path $scnDst) {
    [System.IO.Directory]::Delete($scnDst, $true)
}
Copy-Item $scnSrc $scnDst -Recurse -Force
Write-Host "    ok: scenarios/ ($((Get-ChildItem $scnDst -Recurse -File | Measure-Object).Count) files)"

# -- 5) codegraph 引擎构建并并入发行根（出厂内置 MCP；默认不接入，见 42 §2 (17)） --
Write-Host "==> [5/6] build codegraph engine"
& (Join-Path $root "build-codegraph.ps1")
if ($LASTEXITCODE -ne 0) { throw "build-codegraph.ps1 failed" }
$cgExe = "chonkpilot-codegraph-mcp-server.exe"
Copy-Item (Join-Path $root "dist\plugins\codegraph\$cgExe") (Join-Path $dist $cgExe) -Force
$mbCg = [Math]::Round((Get-Item (Join-Path $dist $cgExe)).Length / 1MB, 1)
Write-Host "    ok: $cgExe ($mbCg MB) -> $dist"

# -- 6) vfts 引擎 + zvec 运行库并入发行根（zvec_c_api.dll 必须与引擎 exe 同目录） --
Write-Host "==> [6/6] build vfts engine"
& (Join-Path $root "build-vfts.ps1")
if ($LASTEXITCODE -ne 0) { throw "build-vfts.ps1 failed" }
$vfExe = "chonkpilot-vfts-mcp-server.exe"
$vftsDist = Join-Path $root "dist\plugins\vfts"
Copy-Item (Join-Path $vftsDist $vfExe) (Join-Path $dist $vfExe) -Force
Copy-Item (Join-Path $vftsDist "zvec_c_api.dll") (Join-Path $dist "zvec_c_api.dll") -Force
$mbVf = [Math]::Round((Get-Item (Join-Path $dist $vfExe)).Length / 1MB, 1)
$mbDll = [Math]::Round((Get-Item (Join-Path $dist "zvec_c_api.dll")).Length / 1MB, 1)
Write-Host "    ok: $vfExe ($mbVf MB) + zvec_c_api.dll ($mbDll MB) -> $dist"

# -- 清单 --
$mbGui = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
$mbCli = [Math]::Round((Get-Item $outCliExe).Length / 1MB, 1)
$capCount = (Get-ChildItem (Join-Path $dist "capability") -Recurse -File | Measure-Object).Count
$scnCount = (Get-ChildItem (Join-Path $dist "scenarios") -Recurse -File | Measure-Object).Count
Write-Host "==> done: $dist"
Write-Host "    chonkpilot.exe                      $mbGui MB"
Write-Host "    chonkpilot-cli.exe                  $mbCli MB"
Write-Host "    chonkpilot-codegraph-mcp-server.exe $mbCg MB"
Write-Host "    chonkpilot-vfts-mcp-server.exe      $mbVf MB"
Write-Host "    zvec_c_api.dll                      $mbDll MB"
Write-Host "    capability/                         $capCount files"
Write-Host "    scenarios/                          $scnCount files"
Write-Host "    run: cd $dist ; .\chonkpilot.exe"
Write-Host "    cli: cd $dist ; .\chonkpilot-cli.exe --prompt 'hello' --work-dir ."
