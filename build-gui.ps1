﻿﻿﻿﻿﻿# build-gui.ps1：ChonkPilot GUI 客户端形态构建（GUI 客户端 + 独立 server，`-tags split`，无参数）
#
# 命名（2026-09-21，D-27 形态命名重整）：本脚本原名 `build-split.ps1`；
#   形态名（分离形态 / team）→ **`gui`（GUI 客户端 + 独立 server）**，
#   产物目录 `dist/split/` → `dist/gui/`。**编译期 tag 仍为 `split`**（源文件名
#   `sweep_split.go` / `*_inprocess.go` 与之绑定，未改名），脚本名与产物路径**仅机械改名**，
#   构建流程/行为**零变更**。
#   **（第二刀，2026-09-21）产物名**：客户端 `chonkpilot-gui.exe` → **`chonkpilot-gui-client.exe`**、
#   `chonkpilot-cli.exe` → **`chonkpilot-cli-client.exe`**（服务端 `chonkpilot-server.exe` 名不变，
#   与 browser 形态**同一份 exe**）。
#   **（D-28，2026-09-21）源与产物分区**：客户端壳 = **`src/gui`**（嵌前端 dist + 形态 `gui.FormGui`），
#   客户端 CLI = **`src/desktop/cli`**（同 lib 集，`-tags split`），服务端 = **`src/server`**；
#   宿主实现唯一一份在 `src/lib/gui`。**产物分区**：客户端 exe → `dist/gui/`；
#   服务端 exe + 引擎 + capability → **`dist/server/`**（browser 与 gui **共用**；browser 静态页
#   由 `build-browser.ps1` 投放到 **`src/server/frontend/dist`**，随本脚本第 4 步 go:embed 进
#   `chonkpilot-server.exe`——2026-09-21 起静态面内嵌，不再依赖外部 `--web-root` 目录）。
#
# 形态口径（用户 2026-09-16，见 [42 §2 (72)] · [40 T-24]）：
#   客户端 = 前端 + GUI；服务端 = llm + gateway + filesys + data 合并为一个 exe；
#   消息经 bridge(HTTP) 在两端路由 —— 分阶段落地，当前**形态开关 = 编译期 tag `split`**。
#   `-tags split` 编入的是**分离专属逻辑**（默认构建不编入，走同目录 *_inprocess.go 空实现）：
#     - chonkpilot-plugin/instance ：StartHeartbeat（30s 发布 instance-heartbeat）+ Manager.Sweep（90s 超时判定）
#     - chonkpilot-llm/server      ：sweep_split.go（30s ticker → Sweep(90s) → 既有 exitInstance 清理）
#     - chonkpilot-data/persist    ：sweep_split.go（30s 扫描 → LastBeat ≥90s → 既有 instanceGone 解绑）
#     - chonkpilot-plugin-{codegraph,vfts}：sweep_split.go（实例超时 → 既有 instanceGone）
#   心跳取值单一来源 = chonkpilot-lib/heartbeat（周期 30s / 超时 90s）。
#
# 产物：
#   dist/gui/                    – 客户端（与 dist/desktop/ **隔离**、互不覆盖）
#     ├── chonkpilot-gui-client.exe   # 客户端：webview2 宿主（前端 dist go:embed）+ 心跳发布侧
#     └── chonkpilot-cli-client.exe   # console 宿主（同 lib 集，AsyncMode: never）
#   dist/server/                 – 服务端（browser 与 gui **共用**；browser 静态页 = 内嵌面，
#                                  来源 src/server/frontend/dist，由 build-browser.ps1 投放）
#     ├── chonkpilot-server.exe             # 服务端：llm + gateway + filesys + data 合一 exe
#     ├── chonkpilot-codegraph-mcp-server.exe  # 内置 MCP 引擎（形态无关）
#     ├── chonkpilot-vfts-mcp-server.exe       # 内置 MCP 引擎（形态无关）
#     ├── zvec_c_api.dll                       # vfts 运行库（必须与 vfts 引擎 exe 同目录）
#     ├── capability/                          # 契约 + executor×3
#     └── scenarios/                           # app 级场景（仅出厂场景「开发场景」default/，随发布只读资源；
#                                              #   与 capability/ 平级，25-MCP与场景分层模型 §6 / T6）
#
# 与 build-desktop.ps1 的关系：本脚本**只重编受 tag 影响的宿主 exe**（client / cli-client / server）；
#   capability/ 与内置引擎 exe 属**形态无关资产**，优先从 dist/desktop 复用（缺失才回退既有
#   build-mcp-server.ps1 / build-codegraph.ps1 / build-vfts.ps1），避免重复 CGO 构建。
#
# 用法：.\build-gui.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
# 客户端壳 / 客户端 CLI / 服务端（D-28：壳 → src/{gui,desktop,server}，lib → src/lib/*）
$client = Join-Path $root "src\gui"
$cli = Join-Path $root "src\desktop\cli"
$srv = Join-Path $root "src\server"
# 前端源码工程已独立为 src/frontend（决策 [42 §2 (115)]）；embed 产物落
# src/gui/frontend/dist = 客户端壳的 `//go:embed all:frontend/dist` 落点。
$frontend = Join-Path $root "src\frontend"
$embedDist = Join-Path $client "frontend\dist"
$dist = Join-Path $root "dist\gui"
$srvDist = Join-Path $root "dist\server"
$srcDist = Join-Path $root "dist\desktop"
$outGuiExe = Join-Path $dist "chonkpilot-gui-client.exe"
$outCliExe = Join-Path $dist "chonkpilot-cli-client.exe"
$outSrvExe = Join-Path $srvDist "chonkpilot-server.exe"

# -- 0) 结束运行中的旧实例（释放 exe 占用；与 build-desktop.ps1 同列表） --
foreach ($name in @("chonkpilot", "chonkpilot-cli", "chonkpilot-gui-client", "chonkpilot-cli-client", "chonkpilot-server")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Host "==> kill [$name] PID=$($_.Id)"
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

# -- 1) 前端构建（工程 src/frontend；embed 入口 → src/gui/frontend/dist 供 go:embed） --
Write-Host "==> [1/6] build frontend (embed entry)"
Push-Location $frontend
try {
    if (-not (Test-Path "node_modules")) {
        Write-Host "    node_modules 缺失，npm ci ..."
        npm ci 2>&1 | ForEach-Object { Write-Host "    $_" }
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
    }
    # stderr warning 在 $ErrorActionPreference=Stop 下会被当作 terminating ErrorRecord 中断脚本，
    # 与真实构建成败无关；构建段临时降为 Continue，以 $LASTEXITCODE 为唯一判定（同 desktop）。
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    npm run build:embed 2>&1 | Select-Object -Last 8
    $ErrorActionPreference = $prevEAP
    if ($LASTEXITCODE -ne 0) { throw "npm run build:embed failed" }
} finally { Pop-Location }

# embed.html → 复制改名 index.html（GUI/WebView2 入口；go:embed 路径与注入逻辑不变）
$embedHtml = Join-Path $embedDist "embed.html"
if (-not (Test-Path $embedHtml)) { throw "embed.html 未产出: $embedHtml" }
Copy-Item $embedHtml (Join-Path $embedDist "index.html") -Force
[System.IO.File]::Delete($embedHtml)
Write-Host "    ok: $embedDist (embed.html -> index.html)"

# -- 2) 客户端 GUI 编译（分离形态：-tags split 编入心跳发布侧；-H windowsgui：GUI 模式无控制台） --
Write-Host "==> [2/6] build chonkpilot-gui-client.exe (-tags split)"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $client
try {
    go build -tags split -ldflags "-H windowsgui" -o $outGuiExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
    Write-Host "    ok: $outGuiExe ($mb MB)"
} finally { Pop-Location }

# -- 3) CLI 编译（console 模式，无 -H windowsgui） --
Write-Host "==> [3/6] build chonkpilot-cli-client.exe (-tags split)"
Push-Location $cli
try {
    go build -tags split -o $outCliExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outCliExe).Length / 1MB, 1)
    Write-Host "    ok: $outCliExe ($mb MB)"
} finally { Pop-Location }

# -- 4) 服务端编译（dist/server；分离形态服务端 = llm + gateway + filesys + data 合一 exe；
#        本 exe 同时编入 llm-server 与 data persist 的 sweep_split.go 超时清理） --
Write-Host "==> [4/6] build chonkpilot-server.exe (-tags split) -> dist/server"
New-Item -ItemType Directory -Force -Path $srvDist | Out-Null
Push-Location $srv
try {
    go build -tags split -o $outSrvExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outSrvExe).Length / 1MB, 1)
    Write-Host "    ok: $outSrvExe ($mb MB)"
} finally { Pop-Location }

# -- 5) 形态无关资产：capability/ 契约 + executor×3（优先复用 dist/desktop，缺失回退构建） --
Write-Host "==> [5/6] stage capability/ to dist/server"
$capSrc = Join-Path $srcDist "capability"
if (-not (Test-Path $capSrc)) {
    Write-Host "    dist/desktop 无 capability/ → 回退 build-mcp-server.ps1"
    & (Join-Path $root "build-mcp-server.ps1")
    if ($LASTEXITCODE -ne 0) { throw "build-mcp-server.ps1 failed" }
    $capSrc = Join-Path $root "dist\other\capability"
}
$capDst = Join-Path $srvDist "capability"
if (Test-Path $capDst) {
    [System.IO.Directory]::Delete($capDst, $true)
}
Copy-Item $capSrc $capDst -Recurse -Force
Write-Host "    ok: capability/ ($((Get-ChildItem $capDst -Recurse -File | Measure-Object).Count) files)"

# scenarios/（app 级场景，随发布只读资源；与 capability/ 平级）随服务端 exe 同目录投放：
# 优先复用 dist/desktop/scenarios，缺失回退仓库源 src/lib/data/scenarios。
Write-Host "    -> stage scenarios/ to dist/server"
$scnSrc = Join-Path $srcDist "scenarios"
if (-not (Test-Path $scnSrc)) {
    $scnSrc = Join-Path $root "src\lib\data\scenarios"
}
if (-not (Test-Path $scnSrc)) { throw "scenarios 资源缺失: $scnSrc" }
$scnDst = Join-Path $srvDist "scenarios"
if (Test-Path $scnDst) {
    [System.IO.Directory]::Delete($scnDst, $true)
}
Copy-Item $scnSrc $scnDst -Recurse -Force
Write-Host "    ok: scenarios/ ($((Get-ChildItem $scnDst -Recurse -File | Measure-Object).Count) files)"

# -- 6) 形态无关资产：内置 MCP 引擎（codegraph / vfts）+ zvec 运行库（优先复用，缺失回退构建） --
Write-Host "==> [6/6] stage built-in MCP engines to dist/server"
$cgExe = "chonkpilot-codegraph-mcp-server.exe"
$cgSrc = Join-Path $srcDist $cgExe
if (-not (Test-Path $cgSrc)) {
    Write-Host "    dist/desktop 无 $cgExe → 回退 build-codegraph.ps1"
    & (Join-Path $root "build-codegraph.ps1")
    if ($LASTEXITCODE -ne 0) { throw "build-codegraph.ps1 failed" }
    $cgSrc = Join-Path $root "dist\plugins\codegraph\$cgExe"
}
Copy-Item $cgSrc (Join-Path $srvDist $cgExe) -Force
$mbCg = [Math]::Round((Get-Item (Join-Path $srvDist $cgExe)).Length / 1MB, 1)
Write-Host "    ok: $cgExe ($mbCg MB)"

$vfExe = "chonkpilot-vfts-mcp-server.exe"
$vfSrc = Join-Path $srcDist $vfExe
$dllSrc = Join-Path $srcDist "zvec_c_api.dll"
if (-not (Test-Path $vfSrc) -or -not (Test-Path $dllSrc)) {
    Write-Host "    dist/desktop 无 $vfExe / zvec_c_api.dll → 回退 build-vfts.ps1"
    & (Join-Path $root "build-vfts.ps1")
    if ($LASTEXITCODE -ne 0) { throw "build-vfts.ps1 failed" }
    $vfDist = Join-Path $root "dist\plugins\vfts"
    $vfSrc = Join-Path $vfDist $vfExe
    $dllSrc = Join-Path $vfDist "zvec_c_api.dll"
}
Copy-Item $vfSrc (Join-Path $srvDist $vfExe) -Force
Copy-Item $dllSrc (Join-Path $srvDist "zvec_c_api.dll") -Force
$mbVf = [Math]::Round((Get-Item (Join-Path $srvDist $vfExe)).Length / 1MB, 1)
$mbDll = [Math]::Round((Get-Item (Join-Path $srvDist "zvec_c_api.dll")).Length / 1MB, 1)
Write-Host "    ok: $vfExe ($mbVf MB) + zvec_c_api.dll ($mbDll MB)"

# -- 清单 --
$mbGui = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
$mbCli = [Math]::Round((Get-Item $outCliExe).Length / 1MB, 1)
$mbSrv = [Math]::Round((Get-Item $outSrvExe).Length / 1MB, 1)
$capCount = (Get-ChildItem (Join-Path $srvDist "capability") -Recurse -File | Measure-Object).Count
Write-Host "==> done (gui 形态 = GUI 客户端 + 独立 server，-tags split)"
Write-Host "    $dist"
Write-Host "      chonkpilot-gui-client.exe         $mbGui MB   # 客户端"
Write-Host "      chonkpilot-cli-client.exe         $mbCli MB"
Write-Host "    $srvDist"
Write-Host "      chonkpilot-server.exe             $mbSrv MB   # 服务端（llm+gateway+filesys+data）"
Write-Host "      chonkpilot-codegraph-mcp-server.exe $mbCg MB"
Write-Host "      chonkpilot-vfts-mcp-server.exe    $mbVf MB"
Write-Host "      zvec_c_api.dll                    $mbDll MB"
Write-Host "      capability/                       $capCount files"
Write-Host "      (browser 静态页已 go:embed 进 chonkpilot-server.exe；源 = src/server/frontend/dist，由 build-browser.ps1 投放)"
Write-Host "    run: cd $srvDist ; .\chonkpilot-server.exe    (服务端，另开) ; cd $dist ; .\chonkpilot-gui-client.exe    (客户端)"
Write-Host "    cli: cd $dist ; .\chonkpilot-cli-client.exe --prompt 'hello' --work-dir ."
