# build-gui.ps1：ChonkPilot GUI 客户端形态构建（GUI 客户端 + 独立 server，`-tags split`，无参数）
#
# 命名（2026-09-21，D-27 形态命名重整）：本脚本原名 `build-split.ps1`；
#   形态名（分离形态 / team）→ **`gui`（GUI 客户端 + 独立 server）**，
#   产物目录 `dist/split/` → `dist/gui/`。**编译期 tag 仍为 `split`**（源文件名
#   `sweep_split.go` / `*_inprocess.go` 与之绑定，未改名），脚本名与产物路径**仅机械改名**，
#   构建流程/行为**零变更**。
#   **（第二刀，2026-09-21）产物名**：客户端 `chonkpilot-gui.exe` → **`chonkpilot-gui-client.exe`**；
#   服务端 `chonkpilot-server.exe` 名不变，与 browser 形态**同一份 exe**。
#   **（PRODSIM-1 L4，2026-10-22）CLI 单体已删除**：`chonkpilot-cli.exe` / `chonkpilot-cli-client.exe` /
#   `src/desktop/cli` / `src/lib/cli` 全量移除，本脚本不再产出 CLI。
#   **（D-28，2026-09-21）源与产物分区**：客户端壳 = **`src/gui`**（嵌前端 dist + 形态 `gui.FormGui`），
#   服务端 = **`src/server`**；
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
#     ├── chonkpilot-flash.exe        # 薄壳客户端（webview2 宿主，远程总线 UDS；🆕 2026-10-10）
#     ├── chonkpilot-flash-cli.exe    # 薄壳客户端 console 宿主（headless 远程；🆕 2026-10-10）
#     └── frontend/dist/              # flash 前端（**非 embed**，flash 以 os.DirFS(<exeDir>) 加载；🆕 2026-10-10）
#   （dist/gui **不放** server exe：flash 定位 server = **同级 ../server 优先** → 同目录；
#    server 需其随附资产（capability/mcps/plugins），完整部署在 dist/server/，裸复制会拉起失败）
#   dist/server/                 – 服务端（browser 与 gui **共用**；browser 静态页 = 内嵌面，
#                                  来源 src/server/frontend/dist，由 build-browser.ps1 投放）
#     ├── chonkpilot-server.exe             # 服务端：llm + gateway + filesys + data 合一 exe
#     │                                     #   （-H windowsgui：GUI 子系统 + 托盘常驻（--tray 默认开）；日志 → 同目录 logs/server.log）
#     ├── capability/                       # 契约 + executor×4（prompts/tools/resources/skills/agents/system/executors）
#     │   └── scenarios/                    # 出厂场景（源 src/initdata/capability/scenarios）
#     └── mcps/                             # 内置 MCP 引擎
#         ├── codebase/chonkpilot-codegraph-mcp-server.exe
#         └── vfts/chonkpilot-vfts-mcp-server.exe + chonkpilot-vfts-indexer.exe + zvec_c_api.dll
#
# 与 build-desktop.ps1 的关系：本脚本**只重编受 tag 影响的宿主 exe**（client / server）；
#   capability/ 基座 + 内置引擎 exe 属**形态无关资产**（executor exe 优先复用 dist/desktop，缺失或
#   executor 早于契约源时才回退既有 build-mcp-server.ps1 / build-codegraph.ps1 / build-vfts.ps1，避免重复 CGO 构建）；
#   契约（prompts/tools/resources/skills/agents/system）与 scenarios 每次从唯一源 src/initdata 覆盖同步。
#
# 用法：.\build-gui.ps1 [-Debug]
#
# -Debug：调试构建（在 `-tags split` 基础上附加 `debugport`，即 `-tags "split debugport"`），
#         使客户端/薄壳宿主壳编入 `--test-port` 测试通道（e2e harness 用；见 src/lib/gui/debugport_*.go）。
#         默认（发布构建）不含该通道。
param([switch]$Debug)
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
# 宿主壳（客户端 / flash 薄壳）编译标签：-Debug 时在 split 基础上附加 debugport（编入 --test-port）。
$guiTags = if ($Debug) { "split debugport" } else { "split" }
# 客户端壳 / 服务端（D-28：壳 → src/{gui,server}，lib → src/lib/*）
$client = Join-Path $root "src\gui"
$srv = Join-Path $root "src\server"
# flash 薄壳客户端壳（desktop 分体常驻版；🆕 2026-10-10，见 62 §3A / 19 §8.11）
$flash = Join-Path $root "src\flash"
$flashCli = Join-Path $flash "cli"
# 前端源码工程已独立为 src/frontend（决策 [42 §2 (115)]）；embed 产物落
# src/gui/frontend/dist = 客户端壳的 `//go:embed all:frontend/dist` 落点。
$frontend = Join-Path $root "src\frontend"
$embedDist = Join-Path $client "frontend\dist"
$dist = Join-Path $root "dist\gui"
$srvDist = Join-Path $root "dist\server"
$srcDist = Join-Path $root "dist\desktop"
$outGuiExe = Join-Path $dist "chonkpilot-gui-client.exe"
$outFlashExe = Join-Path $dist "chonkpilot-flash.exe"
$outFlashCliExe = Join-Path $dist "chonkpilot-flash-cli.exe"
$outSrvExe = Join-Path $srvDist "chonkpilot-server.exe"

# ── 破坏性操作防护（2026-10-11；本仓曾发生误删用户主目录事故，见 .trae/rules 全局规则）──
# 统一约束：任何删除/覆盖目标必须落在**仓库根 $root 之内**（严格子路径），越界即 throw 中止。
# 目标一律以**字面量绝对路径**传入（禁止调用方用变量拼接/通配符指向用户目录）。
function Remove-SafeItem {
    param([Parameter(Mandatory)][string]$LiteralPath, [switch]$Recurse)
    $resolved = Resolve-Path -LiteralPath $LiteralPath -ErrorAction SilentlyContinue
    if ($null -eq $resolved) { return }   # 目标不存在 = 无操作
    $p = $resolved.Path.TrimEnd('\')
    $rootPath = (Resolve-Path -LiteralPath $root).Path.TrimEnd('\')
    if ($p -notlike "$rootPath\*") {      # 白名单：仅仓库根之内的严格子路径
        throw "拒绝删除仓库外路径: $p（仅允许 $rootPath 之内）"
    }
    Remove-Item -LiteralPath $p -Recurse:$Recurse -Force
}

# -- 0) 结束运行中的旧实例（释放 exe 占用；与 build-desktop.ps1 同列表） --
foreach ($name in @("chonkpilot", "chonkpilot-gui-client", "chonkpilot-flash", "chonkpilot-flash-cli", "chonkpilot-server")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Host "==> kill [$name] PID=$($_.Id)"
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

# -- 0.5) 出厂 system 文档 → data 模块 embed 落点（OP-02，2026-10-06）--
#     唯一源 = src/initdata/capability/system/**；覆盖式同步到 data 模块内 embed 落点，
#     供 `//go:embed all:embedded/system` 编入 client/server exe（摘要提示词等系统文档的磁盘缺失兜底）。
#     ⚠️ src/lib/data/internal/systemfs/embedded/system/** 为**构建生成物（勿手改）**：其唯一源 =
#     src/initdata/capability/system/**，两者须逐字节一致（本步骤每次覆盖式重建；直接改本目录 =
#     与源静默分叉，改动会在下次构建被覆盖丢失）。
Write-Host "==> [0.5/6] sync factory system docs -> data embed dir"
$sysSrc = Join-Path $root "src\initdata\capability\system"
$sysEmbed = Join-Path $root "src\lib\data\internal\systemfs\embedded\system"
if (-not (Test-Path $sysSrc)) { throw "factory system docs not found: $sysSrc" }
Remove-SafeItem -LiteralPath $sysEmbed -Recurse
New-Item -ItemType Directory -Force -Path $sysEmbed | Out-Null
Copy-Item (Join-Path $sysSrc "*") $sysEmbed -Recurse -Force
Write-Host "    ok: embed system docs -> $sysEmbed ($((Get-ChildItem $sysEmbed -Recurse -File | Measure-Object).Count) files)"

# -- 0.6) 契约 embed 副本（GATEWAY-2 C，2026-10-21）--
#     唯一源 = src/initdata/capability/{tools,prompts,skills,resources}；覆盖式同步到 mcp-server 包的
#     `//go:embed all:contracts` 落点（磁盘 capability 根缺失时的 app 层契约兜底）。
#     ⚠️ 该目录为**构建生成物（勿手改，不入库；仅 .gitkeep 占位）**，直接改会被下次构建覆盖。
Write-Host "==> [0.6/6] sync builtin contracts -> mcp-server embed dir"
$embedCap = Join-Path $root "src\lib\mcp-server\server\contracts"
$initCap = Join-Path $root "src\initdata\capability"
New-Item -ItemType Directory -Force -Path $embedCap | Out-Null
foreach ($sub in @("tools", "prompts", "skills", "resources")) {
    $subSrc = Join-Path $initCap $sub
    if (-not (Test-Path $subSrc)) { throw "contracts not found: $subSrc" }
    Remove-SafeItem -LiteralPath (Join-Path $embedCap $sub) -Recurse
    Copy-Item $subSrc $embedCap -Recurse -Force
}
Write-Host "    ok: embed contracts -> $embedCap ($((Get-ChildItem $embedCap -Recurse -File | Measure-Object).Count) files)"

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
Remove-SafeItem -LiteralPath $embedHtml
Write-Host "    ok: $embedDist (embed.html -> index.html)"
# 前端 dist 部署到 flash 薄壳同目录（`<exeDir>/frontend/dist`）——flash **不 embed 前端**，
# 运行时以 os.DirFS(<exeDir>) 加载（见 src/flash/main.go）。gui-client 仍 embed。
$flashFront = Join-Path $dist "frontend\dist"
Remove-SafeItem -LiteralPath $flashFront -Recurse
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $flashFront) | Out-Null
Copy-Item $embedDist $flashFront -Recurse -Force
Write-Host "    ok: $flashFront ($((Get-ChildItem $flashFront -Recurse -File | Measure-Object).Count) files)"

# -- 2) 客户端 GUI 编译（分离形态：-tags split 编入心跳发布侧；-H windowsgui：GUI 模式无控制台） --
Write-Host "==> [2/6] build chonkpilot-gui-client.exe (-tags split)"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $client
try {
    go build -tags $guiTags -ldflags "-H windowsgui" -o $outGuiExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
    Write-Host "    ok: $outGuiExe ($mb MB)"
} finally { Pop-Location }

# -- 3) （已撤销，PRODSIM-1 L4 2026-10-22：CLI 单体 chonkpilot-cli-client.exe 全量删除） --

# -- 3.5) flash 薄壳客户端编译（src/flash → dist/gui；远程总线模式：
#        不内嵌 server/persist/filesys，经 UDS 连常驻 chonkpilot-server.exe；-H windowsgui） --
Write-Host "==> [3.5/6] build chonkpilot-flash.exe (-tags split)"
Push-Location $flash
try {
    go build -tags $guiTags -ldflags "-H windowsgui" -o $outFlashExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outFlashExe).Length / 1MB, 1)
    Write-Host "    ok: $outFlashExe ($mb MB)"
} finally { Pop-Location }

# -- 3.6) flash 薄壳客户端 console 宿主编译（src/flash/cli → dist/gui；headless 远程：
#        无窗口无 WebView2，连 UDS 经常驻 server 执行会话；无 -H windowsgui）--
Write-Host "==> [3.6/6] build chonkpilot-flash-cli.exe (-tags split)"
Push-Location $flashCli
try {
    go build -tags $guiTags -o $outFlashCliExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outFlashCliExe).Length / 1MB, 1)
    Write-Host "    ok: $outFlashCliExe ($mb MB)"
} finally { Pop-Location }

# -- 4) 服务端编译（dist/server；分离形态服务端 = llm + gateway + filesys + data 合一 exe；
#        本 exe 同时编入 llm-server 与 data persist 的 sweep_split.go 超时清理；
#        -H windowsgui：GUI 子系统（无控制台）→ 常驻 + 系统托盘（--tray 默认开；--tray=false 关），
#        进程日志落 exe 同目录 logs/server.log（按大小滚动，见 winlog；不入库）） --
# 前置校验：src/server 的 `//go:embed all:frontend/dist` 要求静态页产物已就位，
# clean clone 缺失会裸报编译错 → 提前给出可操作提示。
if (-not (Test-Path (Join-Path $srv "frontend\dist\index.html"))) {
    throw "src/server/frontend/dist/index.html 缺失：请先运行 .\build-browser.ps1 生成前端产物"
}
Write-Host "==> [4/6] build chonkpilot-server.exe (-tags split) -> dist/server"
New-Item -ItemType Directory -Force -Path $srvDist | Out-Null
Push-Location $srv
try {
    go build -tags split -ldflags "-H windowsgui" -o $outSrvExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outSrvExe).Length / 1MB, 1)
    Write-Host "    ok: $outSrvExe ($mb MB)"
} finally { Pop-Location }

# -- 4.2) 进程外 server 域工具（BRIDGE-2 T7；与 server exe 同目录）--
Write-Host "==> [4.2/6] build chonkpilot-server-tools.exe -> dist/server"
$llmDir = Join-Path $root "src\lib\llm"
$outToolsExe = Join-Path $srvDist "chonkpilot-server-tools.exe"
Push-Location $llmDir
try {
    go build -o $outToolsExe .\cmd\server-tools
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outToolsExe).Length / 1MB, 1)
    Write-Host "    ok: $outToolsExe ($mb MB)"
} finally { Pop-Location }

# -- 4.3) 系统级 app 根工具的进程外实现（P6；server 按 <exeDir>/mcps/self-tools/ 查找）--
Write-Host "==> [4.3/6] stage chonkpilot-self-tools.exe -> dist/server/mcps/self-tools"
$selfToolDst = Join-Path $srvDist "mcps\self-tools"
New-Item -ItemType Directory -Force -Path $selfToolDst | Out-Null
$selfToolSrc = Join-Path $root "dist\other\chonkpilot-self-tools.exe"
if (Test-Path $selfToolSrc) {
    Copy-Item $selfToolSrc $selfToolDst -Force
} else {
    Write-Host "    dist/other 无 self-tools → 直接构建（src/lib/mcp-server/cmd/self-tools）"
    Push-Location (Join-Path $root "src\lib\mcp-server")
    try {
        go build -o (Join-Path $selfToolDst "chonkpilot-self-tools.exe") .\cmd\self-tools
        if ($LASTEXITCODE -ne 0) { throw "build self-tools failed" }
    } finally { Pop-Location }
}
$mbSelf = [Math]::Round((Get-Item (Join-Path $selfToolDst "chonkpilot-self-tools.exe")).Length / 1MB, 1)
Write-Host "    ok: mcps/self-tools/chonkpilot-self-tools.exe ($mbSelf MB)"

# -- 4.4) 工具唯一前门（2026-10-19；server 按 <exeDir>/mcps/core-mcp/ 查找）--
Write-Host "==> [4.4/6] stage chonkpilot-core-mcp-server.exe -> dist/server/mcps/core-mcp"
$coreMcpDst = Join-Path $srvDist "mcps\core-mcp"
New-Item -ItemType Directory -Force -Path $coreMcpDst | Out-Null
$coreMcpSrc = Join-Path $root "dist\other\chonkpilot-core-mcp-server.exe"
if (Test-Path $coreMcpSrc) {
    Copy-Item $coreMcpSrc $coreMcpDst -Force
} else {
    Write-Host "    dist/other 无 core-mcp-server → 直接构建（src/lib/mcp-server/cmd/core-mcp-server）"
    Push-Location (Join-Path $root "src\lib\mcp-server")
    try {
        go build -o (Join-Path $coreMcpDst "chonkpilot-core-mcp-server.exe") .\cmd\core-mcp-server
        if ($LASTEXITCODE -ne 0) { throw "build core-mcp-server failed" }
    } finally { Pop-Location }
}
$mbCore = [Math]::Round((Get-Item (Join-Path $coreMcpDst "chonkpilot-core-mcp-server.exe")).Length / 1MB, 1)
Write-Host "    ok: mcps/core-mcp/chonkpilot-core-mcp-server.exe ($mbCore MB)"

# -- 4.5) dist/gui 陈旧产物清理（**不**把 server 复制进 dist/gui）--
#   背景：flash 的 server 候选 = **同级 `server/` 优先** → 同目录（见 flash/server.go:resolveServerExe）。
#   server 需其随附资产（capability/mcps/plugins 在其 exe 同目录），裸复制 server exe 会因缺资产
#   拉起失败（实测）。故 dist/gui 不放 server —— 由同级 `dist/server/` 提供完整部署。
#   此处仅清理旧构建遗留的误导性产物（含历史上的裸 server exe 副本）。
Write-Host "==> [4.5/6] clean dist/gui stale"
foreach ($stale in @("chonkpilot-server.exe", "chonkpilot-gui.exe", "chonkpilot-cli.exe", "chonkpilot-cli-client.exe", "chonkpilot-codegraph-mcp-server.exe", "chonkpilot-codegraph-indexer.exe", "chonkpilot-vfts-mcp-server.exe", "chonkpilot-vfts-indexer.exe", "chonkpilot-self-tools.exe", "zvec_c_api.dll")) {
    $p = Join-Path $dist $stale
    if (Test-Path $p) { Remove-SafeItem -LiteralPath $p; Write-Host "    clean stale: $stale" }
}

# -- 5) 形态无关资产：capability/（executor×4 + 契约）+ scenarios/（出厂场景） --
Write-Host "==> [5/6] stage capability/ + scenarios/ to dist/server"
# 基座（executor exe 等形态无关资产）优先复用 dist/desktop/capability；缺失、或 executor 早于契约源
# （会产出「旧 executor 配新契约」）时才回退 build-mcp-server.ps1 重编，保证执行器与契约一致。
$initCap = Join-Path $root "src\initdata\capability"
$capSrc = Join-Path $srcDist "capability"
$execSrc = Join-Path $capSrc "executors"
$srcTools = Join-Path $initCap "tools"
$staleExec = $false
if ((Test-Path $execSrc) -and (Test-Path $srcTools)) {
    $execTime = (Get-ChildItem $execSrc -Filter *.exe -File | Sort-Object LastWriteTime -Descending | Select-Object -First 1).LastWriteTime
    $toolTime = (Get-ChildItem $srcTools -Recurse -File | Sort-Object LastWriteTime -Descending | Select-Object -First 1).LastWriteTime
    if ($execTime -and $toolTime -and ($toolTime -gt $execTime)) { $staleExec = $true }
}
if ((-not (Test-Path $capSrc)) -or $staleExec) {
    Write-Host "    dist/desktop 无 capability/ 或 executor 早于契约源 → 回退 build-mcp-server.ps1"
    & (Join-Path $root "build-mcp-server.ps1")
    if ($LASTEXITCODE -ne 0) { throw "build-mcp-server.ps1 failed" }
    $capSrc = Join-Path $root "dist\other\capability"
}
$capDst = Join-Path $srvDist "capability"
Remove-SafeItem -LiteralPath $capDst -Recurse
Copy-Item $capSrc $capDst -Recurse -Force
# 契约子树每次从唯一源 src/initdata 覆盖同步（避免复用 dist/desktop 旧产物把过期契约/场景带入 dist/server）
foreach ($sub in @("prompts", "tools", "resources", "skills", "agents", "system")) {
    $srcSub = Join-Path $initCap $sub
    if (-not (Test-Path $srcSub)) { throw "contracts not found: $srcSub" }
    $capSub = Join-Path $capDst $sub
    Remove-SafeItem -LiteralPath $capSub -Recurse
    Copy-Item $srcSub $capDst -Recurse -Force
}
# 未发布草稿排除：skills/se/（用户 2026-10-10 裁决「暂不提交」）不随产物投放。
$seDraft = Join-Path $capDst "skills\se"
Remove-SafeItem -LiteralPath $seDraft -Recurse
# 域工具契约（唯一源 = src/lib/llm/contracts/tools；core-only → 仅 core 执行）
$domainToolsSrc = Join-Path $root "src\lib\llm\contracts\tools"
$serverToolsDst = Join-Path $capDst "tools\server"
New-Item -ItemType Directory -Force -Path $serverToolsDst | Out-Null
Copy-Item (Join-Path $domainToolsSrc "*.tool.md") $serverToolsDst -Force
Write-Host "    ok: capability/tools/server ($((Get-ChildItem $serverToolsDst -Filter *.tool.md -File | Measure-Object).Count) files)"
# 出厂场景：每次从唯一源 src/initdata/capability/scenarios 覆盖同步
$scnSrc = Join-Path $initCap "scenarios"
$scnDst = Join-Path $capDst "scenarios"
Remove-SafeItem -LiteralPath $scnDst -Recurse
Copy-Item $scnSrc $scnDst -Recurse -Force
Write-Host "    ok: capability/ ($((Get-ChildItem $capDst -Recurse -File | Measure-Object).Count) files)"
Write-Host "    ok: capability/scenarios/ ($((Get-ChildItem $scnDst -Recurse -File | Measure-Object).Count) files)"

# -- 6) 形态无关资产：内置 MCP 引擎（codegraph / vfts）+ zvec 运行库 → mcps/{codebase,vfts}（优先复用，缺失回退构建） --
Write-Host "==> [6/6] stage built-in MCP engines to dist/server/mcps"
$cgExe = "chonkpilot-codegraph-mcp-server.exe"
$cgSrc = Join-Path $srcDist "mcps\codebase\$cgExe"
if (-not (Test-Path $cgSrc)) {
    Write-Host "    dist/desktop 无 mcps/codebase/$cgExe → 回退 build-codegraph.ps1"
    & (Join-Path $root "build-codegraph.ps1")
    if ($LASTEXITCODE -ne 0) { throw "build-codegraph.ps1 failed" }
    $cgSrc = Join-Path $root "dist\plugins\codegraph\$cgExe"
}
$cgDst = Join-Path $srvDist "mcps\codebase"
New-Item -ItemType Directory -Force -Path $cgDst | Out-Null
Copy-Item $cgSrc $cgDst -Force
$mbCg = [Math]::Round((Get-Item (Join-Path $cgDst $cgExe)).Length / 1MB, 1)
Write-Host "    ok: mcps/codebase/$cgExe ($mbCg MB)"
# codegraph 查询工具一工具一 exe（优先 dist/desktop/mcps/codebase，缺失回退 dist/plugins/codegraph）
$cgExecNames = @(
    "chonkpilot-codegraph-symbol-search-executor.exe",
    "chonkpilot-codegraph-symbol-info-executor.exe",
    "chonkpilot-codegraph-callers-executor.exe",
    "chonkpilot-codegraph-callees-executor.exe",
    "chonkpilot-codegraph-dependency-graph-executor.exe",
    "chonkpilot-codegraph-circular-deps-executor.exe",
    "chonkpilot-codegraph-complexity-executor.exe",
    "chonkpilot-codegraph-module-summary-executor.exe")
foreach ($e in $cgExecNames) {
    $src = Join-Path $srcDist "mcps\codebase\$e"
    if (-not (Test-Path $src)) { $src = Join-Path $root "dist\plugins\codegraph\$e" }
    if (-not (Test-Path $src)) { throw "missing codegraph query executor: $e（先跑 build-codegraph.ps1）" }
    Copy-Item $src $cgDst -Force
}
Write-Host "    ok: mcps/codebase/ codegraph query executors ($($cgExecNames.Count))"

# resident codegraph indexer（P5）：与引擎同目录；优先复用 dist/desktop/mcps/codebase，缺失回退 dist/plugins/codegraph。
$cgIdxExe = "chonkpilot-codegraph-indexer.exe"
$cgIdxSrc = Join-Path $srcDist "mcps\codebase\$cgIdxExe"
if (-not (Test-Path $cgIdxSrc)) {
    $cgIdxSrc = Join-Path $root "dist\plugins\codegraph\$cgIdxExe"
}
if (Test-Path $cgIdxSrc) {
    Copy-Item $cgIdxSrc $cgDst -Force
    $mbCgIdx = [Math]::Round((Get-Item (Join-Path $cgDst $cgIdxExe)).Length / 1MB, 1)
    Write-Host "    ok: mcps/codebase/$cgIdxExe ($mbCgIdx MB)"
} else {
    Write-Host "    [警告] 未找到 $cgIdxExe（dist/desktop/mcps/codebase 与 dist/plugins/codegraph 均缺失）；常驻索引器将不可用"
}

$vfExe = "chonkpilot-vfts-mcp-server.exe"
$vfSrc = Join-Path $srcDist "mcps\vfts\$vfExe"
$dllSrc = Join-Path $srcDist "mcps\vfts\zvec_c_api.dll"
if (-not (Test-Path $vfSrc) -or -not (Test-Path $dllSrc)) {
    Write-Host "    dist/desktop 无 mcps/vfts/$vfExe / zvec_c_api.dll → 回退 build-vfts.ps1"
    & (Join-Path $root "build-vfts.ps1")
    if ($LASTEXITCODE -ne 0) { throw "build-vfts.ps1 failed" }
    $vfDist = Join-Path $root "dist\plugins\vfts"
    $vfSrc = Join-Path $vfDist $vfExe
    $dllSrc = Join-Path $vfDist "zvec_c_api.dll"
}
$vfDst = Join-Path $srvDist "mcps\vfts"
New-Item -ItemType Directory -Force -Path $vfDst | Out-Null
Copy-Item $vfSrc $vfDst -Force
Copy-Item $dllSrc $vfDst -Force
# vfts_query 一工具一 exe（与 zvec_c_api.dll 同目录）
$vfQuerySrc = Join-Path $srcDist "mcps\vfts\chonkpilot-vfts-query-executor.exe"
if (-not (Test-Path $vfQuerySrc)) { $vfQuerySrc = Join-Path $root "dist\plugins\vfts\chonkpilot-vfts-query-executor.exe" }
if (Test-Path $vfQuerySrc) {
    Copy-Item $vfQuerySrc $vfDst -Force
    Write-Host "    ok: mcps/vfts/chonkpilot-vfts-query-executor.exe"
} else {
    Write-Host "    [警告] 未找到 chonkpilot-vfts-query-executor.exe；vfts_query 运行期不可用"
}
# resident indexer（P4b-2b）：与引擎同目录；优先复用 dist/desktop/mcps/vfts，缺失回退 dist/plugins/vfts。
$vfIdxExe = "chonkpilot-vfts-indexer.exe"
$vfIdxSrc = Join-Path $srcDist "mcps\vfts\$vfIdxExe"
if (-not (Test-Path $vfIdxSrc)) {
    $vfIdxSrc = Join-Path $root "dist\plugins\vfts\$vfIdxExe"
}
if (Test-Path $vfIdxSrc) {
    Copy-Item $vfIdxSrc $vfDst -Force
    $mbVfIdx = [Math]::Round((Get-Item (Join-Path $vfDst $vfIdxExe)).Length / 1MB, 1)
    Write-Host "    ok: mcps/vfts/$vfIdxExe ($mbVfIdx MB)"
} else {
    Write-Host "    [警告] 未找到 $vfIdxExe（dist/desktop/mcps/vfts 与 dist/plugins/vfts 均缺失）；常驻索引器将不可用"
}
$mbVf = [Math]::Round((Get-Item (Join-Path $vfDst $vfExe)).Length / 1MB, 1)
$mbDll = [Math]::Round((Get-Item (Join-Path $vfDst "zvec_c_api.dll")).Length / 1MB, 1)
Write-Host "    ok: mcps/vfts/$vfExe ($mbVf MB) + zvec_c_api.dll ($mbDll MB)"

# -- 清单 --
#   dist/gui **不含** server exe（server 由同级 `dist/server/` 提供；flash 以同级 server/ 优先定位）
#   dist/gui 另含（下面未逐行输出）：frontend/dist/（flash 前端，非 embed；flash 以 os.DirFS 加载）
$mbGui = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
$mbFlash = [Math]::Round((Get-Item $outFlashExe).Length / 1MB, 1)
$mbFlashCli = [Math]::Round((Get-Item $outFlashCliExe).Length / 1MB, 1)
$mbSrv = [Math]::Round((Get-Item $outSrvExe).Length / 1MB, 1)
$capCount = (Get-ChildItem (Join-Path $srvDist "capability") -Recurse -File | Measure-Object).Count
Write-Host "==> done (gui 形态 = GUI 客户端 + 独立 server，-tags split)"
Write-Host "    $dist"
Write-Host "      chonkpilot-gui-client.exe         $mbGui MB   # 客户端"
Write-Host "      chonkpilot-flash.exe              $mbFlash MB   # 薄壳客户端（远程总线 UDS）"
Write-Host "      chonkpilot-flash-cli.exe          $mbFlashCli MB   # 薄壳客户端 console 宿主（headless）"
Write-Host "    $srvDist"
Write-Host "      chonkpilot-server.exe             $mbSrv MB   # 服务端（llm+gateway+filesys+data）"
Write-Host "      capability/                       $capCount files (prompts/tools/resources/skills/agents/system/scenarios/executors)"
Write-Host "      capability/scenarios/             出厂场景"
Write-Host "      mcps/codebase/chonkpilot-codegraph-mcp-server.exe $mbCg MB"
Write-Host "      mcps/codebase/chonkpilot-codegraph-indexer.exe    $mbCgIdx MB"
Write-Host "      mcps/vfts/chonkpilot-vfts-mcp-server.exe          $mbVf MB"
Write-Host "      mcps/vfts/zvec_c_api.dll                          $mbDll MB"
Write-Host "      (browser 静态页已 go:embed 进 chonkpilot-server.exe；源 = src/server/frontend/dist，由 build-browser.ps1 投放)"
Write-Host "    run: cd $srvDist ; .\chonkpilot-server.exe    (服务端，GUI 子系统 + 托盘常驻，日志 logs/server.log) ; cd $dist ; .\chonkpilot-gui-client.exe    (客户端)"
Write-Host "    flash: cd $srvDist ; .\chonkpilot-server.exe --autoexit --client-exe ..\gui\chonkpilot-flash.exe ; cd $dist ; .\chonkpilot-flash.exe --work-dir .   ( --tray 默认开，无头可 --tray=false )"
Write-Host "    flash-cli: cd $dist ; .\chonkpilot-flash-cli.exe --prompt 'hello' --work-dir .   (需 server 在跑；缺失则自动拉起)"
