﻿﻿﻿﻿# build-desktop.ps1：ChonkPilot 桌面单体（GUI + CLI）构建（无参数）
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
#   ├── capability/                         # 契约 + executor×3 + 出厂场景（来自 dist/other + 场景源）
#   │   ├── prompts/<...>/*.prompt.md
#   │   ├── tools/<cat>/*.tool.md
#   │   ├── resources/<...>/*.resource.md
#   │   ├── skills/<...>/*.skill.md
#   │   ├── system/summary.md               # 非原语系统文档（源 src/initdata/capability/system，OP-02）
#   │   ├── scenarios/<场景id>/…            # 出厂场景（源 src/initdata/capability/scenarios，覆盖式同步）
#   │   └── executors/chonkpilot-{core,desktop,browser,dsl}-executor.exe
#   ├── mcps/                               # 内置 MCP 引擎 + 可选文档转换器
#   │   ├── codebase/chonkpilot-codegraph-mcp-server.exe
#   │   ├── vfts/chonkpilot-vfts-mcp-server.exe + zvec_c_api.dll（必须与 vfts 引擎同目录）
#   │   └── markitdown/…（可选：文档转换，dist/other/mcps 兜底镜像；缺则告警、不中断）
#   └── (无其它)                            # 根仅 chonkpilot.exe / chonkpilot-cli.exe 两个 exe
#
# 另产**出厂数据包**到 dist\initdata\（不进 dist\desktop）：
#   capability-<ver>.zip（= dist/desktop/capability/ **全量**，含 executors exe）·
#   initial.zip（完整出厂包，内含 exe，供用户自行恢复）。
#   <ver> = 日期时间戳（无独立版本源）；不做哈希/清单校验。
#
# **出厂数据唯一源** = src/initdata/（capability/{prompts,tools,resources,skills,agents,scenarios,system}），不再 embed
# （例外：system/ 另经 embed 落点编入 data 模块，供磁盘缺失兜底，见 [0.5/9]）。
# 全量组件：内嵌 lib 插件（compress/history/memory/vfts/codegraph）随 exe 编译；
#   外置引擎 exe（codegraph/vfts）+ zvec_c_api.dll 置于 dist/desktop/mcps/{codebase,vfts}\——
#   插件按「宿主 exe 同目录/mcps/<engine>/」解析引擎。
# 用法：.\build-desktop.ps1
$ErrorActionPreference = "Stop"

# 工具链（项目固定）：**强制**使用本仓要求的 Go 工具链。
# 注意：不采信外部 GOROOT —— 本机 shell 常预置 `e:\GoDev\go`（旧版），若沿用会导致
# 「go.mod requires go >= 1.26.0」校验失败，故此处直接固定。
$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = (Join-Path $env:GOROOT "bin") + ";" + $env:Path
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
# 出厂数据唯一源 + 出厂数据包落点
$initScenarios = Join-Path $root "src\initdata\capability\scenarios"
$initdataDist = Join-Path $root "dist\initdata"
# 版本 = 日期时间戳（无独立版本源，见头注）
$ver = Get-Date -Format "yyyyMMdd-HHmmss"

# -- 0) 生成消息面键常量（契约唯一源 = docs/spec/60-reference/61-messages.schema.json） --
# genmsg → src/lib/core/msgkeys/msgkeys_gen.go + src/frontend/src/events/msgkeys.js（**勿手改**）。
# 先跑生成再编译，保证键常量与契约一致；漂移另有 src/tools/genmsg/gen_test.go 兜底。
Write-Host "==> [0/9] genmsg (message key constants)"
Push-Location (Join-Path $root "src\tools\genmsg")
try {
    go run .
    if ($LASTEXITCODE -ne 0) { throw "genmsg failed" }
} finally { Pop-Location }

# -- 0) 结束运行中的旧实例（释放 exe 占用；两形态产物同列表） --
foreach ($name in @("chonkpilot", "chonkpilot-cli", "chonkpilot-gui-client", "chonkpilot-cli-client", "chonkpilot-server")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Host "==> kill [$name] PID=$($_.Id)"
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

# -- 0.5) 出厂 system 文档 → data 模块 embed 落点（OP-02，2026-10-06）--
#     唯一源 = src/initdata/capability/system/**；覆盖式同步到 data 模块内 embed 落点，
#     供 `//go:embed all:embedded/system` 编入 exe（摘要提示词等系统文档的磁盘缺失兜底）。
Write-Host "==> [0.5/9] sync factory system docs -> data embed dir"
$sysSrc = Join-Path $root "src\initdata\capability\system"
$sysEmbed = Join-Path $root "src\lib\data\internal\systemfs\embedded\system"
if (-not (Test-Path $sysSrc)) { throw "factory system docs not found: $sysSrc" }
if (Test-Path $sysEmbed) { [System.IO.Directory]::Delete($sysEmbed, $true) }
New-Item -ItemType Directory -Force -Path $sysEmbed | Out-Null
Copy-Item (Join-Path $sysSrc "*") $sysEmbed -Recurse -Force
Write-Host "    ok: embed system docs -> $sysEmbed ($((Get-ChildItem $sysEmbed -Recurse -File | Measure-Object).Count) files)"

# -- 1) 前端构建（工程 src/frontend；embed 入口 → 镜像到两个宿主壳的 embed 落点） --
Write-Host "==> [1/9] build frontend (embed entry)"
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
Write-Host "==> [2/9] build chonkpilot.exe"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $desktop
try {
    go build -ldflags "-H windowsgui" -o $outGuiExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
    Write-Host "    ok: $outGuiExe ($mb MB)"
} finally { Pop-Location }

# -- 3) CLI 单体编译（src/desktop/cli；console 模式，无 -H windowsgui） --
Write-Host "==> [3/9] build chonkpilot-cli.exe"
Push-Location $cli
try {
    go build -o $outCliExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outCliExe).Length / 1MB, 1)
    Write-Host "    ok: $outCliExe ($mb MB)"
} finally { Pop-Location }

# -- 4) 构建 MCP server 契约与 executor（调用 build-mcp-server.ps1 → dist/other） --
Write-Host "==> [4/9] build mcp-server (contracts + executors)"
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

# dsl_run 统一编排执行器（决策 42 §2 (247)）随 capability/ 一并就位：capability/executors/
# 下的 chonkpilot-dsl-executor.exe（由 build-mcp-server.ps1 → build-dsl-executor.ps1 产出）；
# 源缺失时上一步已告警跳过，此处仅守卫式提示，不中断构建。
$dslExe = Join-Path $capDst "executors\chonkpilot-dsl-executor.exe"
if (Test-Path $dslExe) {
    Write-Host "    ok: capability/executors/chonkpilot-dsl-executor.exe"
} else {
    Write-Host "    [提示] capability/executors/chonkpilot-dsl-executor.exe 缺失（dslexec 源未落地）；dsl_run 运行期不可用"
}

# -- 5) 出厂场景：src/initdata/capability/scenarios → dist/desktop/capability/scenarios（覆盖式同步，不删目录本身） --
Write-Host "==> [5/9] stage scenarios/ (src/initdata/capability/scenarios)"
if (-not (Test-Path $initScenarios)) { throw "scenarios not found: $initScenarios" }
$scnDst = Join-Path $dist "capability\scenarios"
New-Item -ItemType Directory -Force -Path $scnDst | Out-Null
# 覆盖式：镜像源内容（先清空源内已删除项的残留 → 逐场景目录覆盖），但**保留 scenarios 目录本身**
Get-ChildItem -Path $scnDst -Force | Where-Object { $_.Name -notin (Get-ChildItem -Path $initScenarios -Force | ForEach-Object { $_.Name }) } | Remove-Item -Recurse -Force
Copy-Item (Join-Path $initScenarios "*") $scnDst -Recurse -Force
$scnCount = (Get-ChildItem $scnDst -Recurse -File | Measure-Object).Count
Write-Host "    ok: capability/scenarios/ ($scnCount files)"

# -- 6) codegraph 引擎构建并置于 dist/desktop/mcps/codebase/（出厂内置 MCP；默认不接入，见 42 §2 (17)） --
Write-Host "==> [6/9] build codegraph engine -> mcps/codebase"
& (Join-Path $root "build-codegraph.ps1")
if ($LASTEXITCODE -ne 0) { throw "build-codegraph.ps1 failed" }
$cgExe = "chonkpilot-codegraph-mcp-server.exe"
$cgDir = Join-Path $dist "mcps\codebase"
New-Item -ItemType Directory -Force -Path $cgDir | Out-Null
Copy-Item (Join-Path $root "dist\plugins\codegraph\$cgExe") $cgDir -Force
$mbCg = [Math]::Round((Get-Item (Join-Path $cgDir $cgExe)).Length / 1MB, 1)
Write-Host "    ok: mcps/codebase/$cgExe ($mbCg MB)"

# -- 7) vfts 引擎 + zvec 运行库置于 dist/desktop/mcps/vfts/（zvec_c_api.dll 必须与引擎 exe 同目录） --
Write-Host "==> [7/9] build vfts engine -> mcps/vfts"
& (Join-Path $root "build-vfts.ps1")
if ($LASTEXITCODE -ne 0) { throw "build-vfts.ps1 failed" }
$vfExe = "chonkpilot-vfts-mcp-server.exe"
$vftsDist = Join-Path $root "dist\plugins\vfts"
$vfDir = Join-Path $dist "mcps\vfts"
New-Item -ItemType Directory -Force -Path $vfDir | Out-Null
Copy-Item (Join-Path $vftsDist $vfExe) $vfDir -Force
Copy-Item (Join-Path $vftsDist "zvec_c_api.dll") $vfDir -Force
$mbVf = [Math]::Round((Get-Item (Join-Path $vfDir $vfExe)).Length / 1MB, 1)
$mbDll = [Math]::Round((Get-Item (Join-Path $vfDir "zvec_c_api.dll")).Length / 1MB, 1)
Write-Host "    ok: mcps/vfts/$vfExe ($mbVf MB) + zvec_c_api.dll ($mbDll MB)"

# -- 8) mcps 转换器兜底并入 dist/desktop/mcps/（dist\other\mcps → dist\desktop\mcps；可选，**幂等**）--
#    兜底语义：源存在 → 覆盖镜像（**仅覆盖子项，保留 codebase/vfts 引擎目录**）；源不存在但目标已存在
#    → 保留并提示；两处都无 → 醒目告警（**不中断构建**）。转换器（文档索引 vfts.docs）为可选组件，
#    产物由 .\src\mcps\markitdown\build-mcps.ps1 独立冻结（不打入本脚本必经路径，见该脚本头注）。
Write-Host "==> [8/9] stage mcps/ doc converter (optional)"
$mcpsSrc = Join-Path $root "dist\other\mcps"
$mcpsDst = Join-Path $dist "mcps"
$mcpsCount = 0
if (Test-Path $mcpsSrc) {
    New-Item -ItemType Directory -Force -Path $mcpsDst | Out-Null
    Copy-Item (Join-Path $mcpsSrc "*") $mcpsDst -Recurse -Force
    $mcpsCount = (Get-ChildItem $mcpsDst -Recurse -File | Measure-Object).Count
    Write-Host "    ok: mcps/ ($mcpsCount files) <- $mcpsSrc"
} elseif (Test-Path $mcpsDst) {
    $mcpsCount = (Get-ChildItem $mcpsDst -Recurse -File | Measure-Object).Count
    Write-Host "    [提示] $mcpsSrc 不存在；保留既有 $mcpsDst（$mcpsCount files）"
} else {
    Write-Host "    [警告] mcps 转换器产物缺失（$mcpsSrc 与 $mcpsDst 均不存在）！文档索引（vfts.docs）将不可用"
    Write-Host "           如需：.\src\mcps\markitdown\build-mcps.ps1（产物 = dist\desktop\mcps\markitdown\markitdown-mcp.exe）"
}

# -- 8.5) 根目录陈旧产物清理（不占步号）：运行根**只允许** chonkpilot.exe / chonkpilot-cli.exe
#    引擎与运行库已归位 mcps/{codebase,vfts}（zvec_c_api.dll 随 vfts 同目录），
#    根下遗留的旧引擎 exe / 旧 dll 属历史残留，必须清掉，否则会被 initial.zip 带入出厂包。
Write-Host "==> clean stale root artifacts (root 仅允许 chonkpilot.exe / chonkpilot-cli.exe)"
$rootExeAllow = @("chonkpilot.exe", "chonkpilot-cli.exe")
Get-ChildItem -Path $dist -File -Force -ErrorAction SilentlyContinue |
    Where-Object { ($_.Extension -in @(".exe", ".dll")) -and ($rootExeAllow -notcontains $_.Name) } |
    ForEach-Object {
        Write-Host "    [清理] 根下陈旧产物: $($_.Name)"
        [System.IO.File]::Delete($_.FullName)
    }
# 旧「与 capability 平级的独立 scenarios 根」已随 P2 移入 capability/scenarios → 清根下遗留 scenarios/ 目录
$legacyScenarios = Join-Path $dist "scenarios"
if (Test-Path $legacyScenarios) {
    Write-Host "    [清理] 根下陈旧目录: scenarios/（已移入 capability/scenarios/）"
    [System.IO.Directory]::Delete($legacyScenarios, $true)
}

# -- 9) 出厂数据包 → dist/initdata/（单包 capability-<ver>.zip + initial.zip 完整出厂包，**不进 dist/desktop**）--
#    来源 = dist/desktop 已铺好的 capability/ 全量（prompts/tools/resources/skills/scenarios/executors）
#    + 全部产物（initial.zip 内含 exe）。不做任何哈希/版本清单校验（<ver> = 时间戳）。
Write-Host "==> [9/9] pack factory data -> dist/initdata/"
New-Item -ItemType Directory -Force -Path $initdataDist | Out-Null
# 先清旧包：<ver> 是时间戳，不清会累积历史 zip（只保留本次构建的 capability-<ver>.zip + initial.zip）
Get-ChildItem -Path $initdataDist -Filter "*.zip" -File -ErrorAction SilentlyContinue | ForEach-Object {
    Write-Host "    [清理] 旧出厂包: $($_.Name)"
    [System.IO.File]::Delete($_.FullName)
}
function New-ZipFromDir {
    param([string]$Src, [string]$Zip)
    if (Test-Path $Zip) { [System.IO.File]::Delete($Zip) }
    Compress-Archive -Path $Src -DestinationPath $Zip -CompressionLevel Optimal
}
# 单包 = dist/desktop/capability/ 全量（含 executors exe；旧 4 分包 capability-tools/-knowledge/-executors/-scenarios 已撤）
New-ZipFromDir (Join-Path $dist "capability") (Join-Path $initdataDist "capability-$ver.zip")
# initial.zip：完整出厂包（**内含 exe**），= dist/desktop 全部内容（根平铺），供用户自行恢复安装
$initialZip = Join-Path $initdataDist "initial.zip"
if (Test-Path $initialZip) { [System.IO.File]::Delete($initialZip) }
# 排除 `.chonkpilot/`（运行时数据：会话/记忆/索引等，**绝不入出厂包**）
$packItems = Get-ChildItem -Path $dist -Force | Where-Object { $_.Name -ne ".chonkpilot" } | ForEach-Object { $_.FullName }
Compress-Archive -Path $packItems -DestinationPath $initialZip -CompressionLevel Optimal
$zipCount = (Get-ChildItem $initdataDist -File | Measure-Object).Count
Write-Host "    ok: dist/initdata/ ($zipCount zips, ver=$ver)"

# -- 清单 --
$mbGui = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
$mbCli = [Math]::Round((Get-Item $outCliExe).Length / 1MB, 1)
$capCount = (Get-ChildItem (Join-Path $dist "capability") -Recurse -File | Measure-Object).Count
Write-Host "==> done: $dist"
Write-Host "    chonkpilot.exe                      $mbGui MB"
Write-Host "    chonkpilot-cli.exe                  $mbCli MB"
Write-Host "    capability/                         $capCount files (prompts/tools/resources/skills/agents/system/scenarios/executors)"
Write-Host "    capability/scenarios/               $scnCount files"
Write-Host "    mcps/codebase/                      $cgExe ($mbCg MB)"
Write-Host "    mcps/vfts/                          $vfExe ($mbVf MB) + zvec_c_api.dll ($mbDll MB)"
Write-Host "    mcps/                               $mcpsCount files (可选：文档转换)"
Write-Host "    run: cd $dist ; .\chonkpilot.exe"
Write-Host "    cli: cd $dist ; .\chonkpilot-cli.exe --prompt 'hello' --work-dir ."
Write-Host "    packs: $initdataDist (*.zip, ver=$ver)"
