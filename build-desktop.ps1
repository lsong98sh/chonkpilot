# build-desktop.ps1：ChonkPilot 桌面单体（GUI）构建（无参数）
#
# 命名（2026-09-21，D-27 形态命名重整）：本脚本原名 `build-standalone-gui.ps1`；
#   形态名 `standalone` → **`desktop`（桌面单体）**，产物目录 `dist/standalone/` → `dist/desktop/`。
#   脚本名与内容**仅机械改名**，构建流程/行为**零变更**。
#   **（第二刀，2026-09-21）产物名**：GUI 宿主 `chonkpilot-gui.exe` → **`chonkpilot.exe`**。
#   **（PRODSIM-1 L4，2026-10-22）CLI 单体已删除**：`chonkpilot-cli.exe` / `src/desktop/cli` /
#   `src/lib/cli` 全量移除，本脚本不再产出 CLI。
#   **（D-28，2026-09-21）源结构**：宿主壳 = **`src/desktop`**（webview2 宿主薄壳，嵌前端 dist +
#   声明形态 `gui.FormDesktop`）；宿主实现唯一一份在 **`src/lib/gui`**（`chonkpilot-gui`）。
#
# 架构（单进程内嵌）：chonkpilot.exe = webview2 宿主（`src/desktop` → `src/lib/gui`.Main），内嵌
#   server（会话/任务编排）→ persist（数据面）→ filesys + gateway(内嵌 mcp-server) + 插件，
#   前端 dist 经 go:embed 进 exe（前端源码工程 = 仓库级 src/frontend，vite 产物直投
#   `src/desktop/frontend/dist`；PRODSIM-1 后不再有 gui/browser 的第二份投放目录）。
#
# 产物（仓库根 dist\，2026-11-02 目录扁平化：不再有 dist\desktop / dist\other / dist\initdata）：
#   ├── chonkpilot.exe                      # webview2 宿主
#   ├── chonkpilot-remote-server.exe        # 远端服务（H5/后台管理已 embed；源 = 同级仓 chonkpilot-plugins/remote）
#   ├── chonkpilot-remote-server.db         # 远端服务 SQLite 库（首启自动建；构建末尾执行一次 user list 生成）
#   ├── themes/themes.css                   # 主题资产（源 src/initdata/themes，覆盖式同步；运行期可编辑）
#   └── capability/                         # 契约 + 出厂场景 + manifest 插件
#       ├── prompts/<...>/*.prompt.md
#       ├── tools/<cat>/*.tool.md
#       ├── resources/<...>/*.resource.md
#       ├── skills/<...>/*.skill.md
#       ├── agents/                         # 智能体契约（*.agent.md，扁平；源 src/initdata/capability/agents）
#       ├── system/                         # 非原语系统文档全量（源 src/initdata/capability/system，OP-02）
#       ├── scenarios/<场景id>/…            # 出厂场景（源 src/initdata/capability/scenarios，覆盖式同步）
#       └── plugins/<id>/plugin.json + <exe>  # manifest 插件（manifest 源 src/initdata/capability/plugins，exe 由 build-plugins.ps1 产出）
#                                           # ⚠️ `ui.selfServe: true` 的插件把 webui embed 进 exe（唯一源 = chonkpilot-plugins/<id>/webui/），
#                                           #    产物内**不带** <id>/webui/；demo-python 为样例（product=false，不随产物投放）
#
# **不再产出**（2026-11-02 按用户口径精简：重复/历史形态全部移除）：
#   - dist\other\（mcp-server / gateway / executors 部署区）——桌面单体不依赖；
#   - dist\desktop\（旧宿主子目录）——exe 直接落 dist 根；
#   - dist\initdata\（出厂 zip 包）——按需另跑打包步骤，不随常规构建；
#   - dist\...\mcps\（内置 codegraph/vfts 引擎 + 文档转换器）——默认不接入；需要内置 MCP 时再单独投放。
#
# **出厂数据唯一源** = src/initdata/（capability/{prompts,tools,resources,skills,agents,scenarios,system}），不再 embed
# （例外：system/ 另经 embed 落点编入 data 模块，供磁盘缺失兜底，见 [0.5/7]）。
# 全量组件：业务能力**全部进程外**（`capability/plugins/<id>/`，由 build-plugins.ps1 产出）；
#   压缩已内建在会话侧（SESSION-1，2026-10-23：`src/lib/llm/server/compress.go`，无独立插件 / exe）。
# 用法：.\build-desktop.ps1 [-Debug]
#
# -Debug：调试构建（附带 `-tags debugport`），使 GUI 单体壳编入 `--test-port` 测试通道
#         （e2e harness 用；见 src/lib/gui/debugport_*.go）。默认（发布构建）不含该通道。
param([switch]$Debug)
$ErrorActionPreference = "Stop"

# 工具链（项目固定）：**强制**使用本仓要求的 Go 工具链。
# 注意：不采信外部 GOROOT —— 本机 shell 常预置 `e:\GoDev\go`（旧版），若沿用会导致
# 「go.mod requires go >= 1.26.0」校验失败，故此处直接固定。
$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = (Join-Path $env:GOROOT "bin") + ";" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
# GUI 单体壳的编译标签：-Debug 时附加 debugport（编入 --test-port 测试通道）。
$desktopTags = if ($Debug) { "debugport" } else { "" }
# 宿主壳（GUI 单体，见 [41 D-28]）
$desktop = Join-Path $root "src\desktop"
# 前端源码工程已独立为仓库级目录 src/frontend（决策 [42 §2 (115)] / [19 §3.2]）；
# PRODSIM-1 后**单入口单产物**：vite 构建直投 `src/desktop/frontend/dist`（桌面壳的
# `//go:embed all:frontend/dist` 落点），不再有 gui/browser 的第二份投放目录。
$frontend = Join-Path $root "src\frontend"
$embedDst = Join-Path $desktop "frontend\dist"
$dist = Join-Path $root "dist"
$outGuiExe = Join-Path $dist "chonkpilot.exe"
# 出厂数据唯一源（契约/场景/主题/插件 manifest）
$initScenarios = Join-Path $root "src\initdata\capability\scenarios"
$initCap = Join-Path $root "src\initdata\capability"
# 远端服务（同仓外挂：chonkpilot-plugins\remote；由本脚本调用其 build.ps1 后拷入 dist 根）
$remoteRepo = [System.IO.Path]::GetFullPath((Join-Path $root "..\chonkpilot-plugins\remote"))
# 版本 = 日期时间戳（无独立版本源，见头注）

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

# 调用子构建脚本：子脚本以 throw（$ErrorActionPreference=Stop 下即终止）或 exit 传播失败；
# 此处以 $? 与 $LASTEXITCODE 双判并统一 throw（仅靠 $LASTEXITCODE 在子脚本未 exit 时可能陈旧/误导）。
function Invoke-ChildBuild {
    param([Parameter(Mandatory)][string]$ScriptName)
    & (Join-Path $root $ScriptName)
    if (-not $? -or $LASTEXITCODE -ne 0) {
        throw "$ScriptName 失败 (LASTEXITCODE=$LASTEXITCODE)"
    }
}

# -- 0) 生成消息面键常量（契约唯一源 = docs/spec/60-reference/61-messages.schema.json） --
# genmsg → 框架仓 host/core/msgkeys/msgkeys_gen.go（framework 自用键）+ 主仓 msgkeys_gen.go
#       （业务键）+ src/frontend/src/events/msgkeys.js（**勿手改**）——TOPO-1 §2.22 msgkeys 双输出。
# 先跑生成再编译，保证键常量与契约一致；漂移另有 src/tools/genmsg/gen_test.go 兜底。
Write-Host "==> [0/9] genmsg (message key constants)"
Push-Location (Join-Path $root "src\tools\genmsg")
try {
    go run .
    if ($LASTEXITCODE -ne 0) { throw "genmsg failed" }
} finally { Pop-Location }

# -- 0) 结束运行中的旧实例（释放 exe 占用；唯一产物 = chonkpilot.exe） --
foreach ($name in @("chonkpilot")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
        Write-Host "==> kill [$name] PID=$($_.Id)"
        Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
    }
}

# -- 0.5) 出厂 system 文档 → data 模块 embed 落点（OP-02，2026-10-06）--
#     唯一源 = src/initdata/capability/system/**；覆盖式同步到 data 模块内 embed 落点，
#     供 `//go:embed all:embedded/system` 编入 exe（摘要提示词等系统文档的磁盘缺失兜底）。
#     ⚠️ src/lib/data/internal/systemfs/embedded/system/** 为**构建生成物（勿手改）**：其唯一源 =
#     src/initdata/capability/system/**，两者须逐字节一致（本步骤每次覆盖式重建；直接改本目录 =
#     与源静默分叉，改动会在下次构建被覆盖丢失）。
Write-Host "==> [0.5/9] sync factory system docs -> data embed dir"
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
#     ⚠️ src/lib/mcp-server/server/contracts/** 为**构建生成物（勿手改，不入库；仅 .gitkeep 占位）**：
#     其唯一源 = src/initdata/capability，直接改本目录会在下次构建被覆盖丢失。
Write-Host "==> [0.6/9] sync builtin contracts -> mcp-server embed dir"
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

# -- 0.7) 主题资产（THEME-1，2026-10-23）--
#     唯一源 = src/initdata/themes/themes.css（单文件多主题块）；覆盖式同步到
#     ① src/lib/gui/internal/themefs/embedded/：`//go:embed embedded/themes.css` 的内置兜底副本
#        （⚠️ 与源逐字节一致，勿手改；磁盘 `themes/themes.css` 缺失 / 不可读时用它，永不 404）；
#     ② dist/desktop/themes/：随产物投放的**可编辑**资产（运行期 `GET /themes.css` 优先读它）。
Write-Host "==> [0.7/9] sync themes.css -> gui embed dir + dist/desktop/themes"
$initThemes = Join-Path $root "src\initdata\themes"
if (-not (Test-Path $initThemes)) { throw "themes not found: $initThemes" }
$themeEmbed = [System.IO.Path]::GetFullPath((Join-Path $root "..\chonkpilot-framework\host\src\themefs\embedded"))
# 该落点在同级仓（framework），不在本仓白名单内 → 显式校验目标尾部，避免误删。
if ($themeEmbed -notlike "*\chonkpilot-framework\host\src\themefs\embedded") { throw "拒绝删除意外路径: $themeEmbed" }
if (Test-Path -LiteralPath $themeEmbed) { Remove-Item -LiteralPath $themeEmbed -Recurse -Force }
New-Item -ItemType Directory -Force -Path $themeEmbed | Out-Null
Copy-Item (Join-Path $initThemes "*") $themeEmbed -Recurse -Force
Write-Host "    ok: embed themes -> $themeEmbed ($((Get-ChildItem $themeEmbed -Recurse -File | Measure-Object).Count) files)"
$themeDst = Join-Path $dist "themes"
New-Item -ItemType Directory -Force -Path $themeDst | Out-Null
Copy-Item (Join-Path $initThemes "*") $themeDst -Recurse -Force
Write-Host "    ok: dist themes -> $themeDst ($((Get-ChildItem $themeDst -Recurse -File | Measure-Object).Count) files)"

# -- 0.8) 前端静态图投放（唯一源 image/*.png → 前端 public，前端按 URL 引用） --
#   chonkpilot.png = 顶栏品牌 logo；cat1/cat2.png = 输入框上沿小猫（睁眼/闭眼）。
Write-Host "==> [0.8/9] sync static images -> frontend public"
foreach ($name in @("chonkpilot.png", "cat1.png", "cat2.png")) {
    $imgSrc = Join-Path $root "image\$name"
    $imgDst = Join-Path $frontend "public\$name"
    if (-not (Test-Path -LiteralPath $imgSrc)) {
        Write-Host "    [warn] 图片源缺失，跳过: $imgSrc"
        continue
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $imgDst) | Out-Null
    $imgStale = (-not (Test-Path -LiteralPath $imgDst)) -or
        ((Get-Item -LiteralPath $imgSrc).LastWriteTimeUtc -gt (Get-Item -LiteralPath $imgDst).LastWriteTimeUtc)
    if ($imgStale) { Copy-Item -LiteralPath $imgSrc -Destination $imgDst -Force }
    Write-Host "    ok: $imgDst"
}

# -- 1) 前端构建（工程 src/frontend；产物直投桌面壳 embed 落点） --
Write-Host "==> [1/9] build frontend"
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
    npm run build 2>&1 | Select-Object -Last 8
    $ErrorActionPreference = $prevEAP
    if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
} finally { Pop-Location }
if (-not (Test-Path (Join-Path $embedDst "index.html"))) { throw "前端 dist 未产出: $embedDst" }
Write-Host "    ok: $embedDst ($((Get-ChildItem $embedDst -Recurse -File | Measure-Object).Count) files)"

# -- 2) GUI 单体编译（src/desktop；-H windowsgui：GUI 模式无控制台） --
Write-Host "==> [2/9] build chonkpilot.exe"
New-Item -ItemType Directory -Force -Path $dist | Out-Null
Push-Location $desktop
try {
    # Windows 图标（2026-11-02）：`-ldflags` 无法指定图标，必须靠**包目录内的 .syso 资源**自动链接。
    # 唯一图标源 = 仓库根 `image\favicon-multi-size.ico`；ico 比 syso 新（或 syso 缺失）时用 rsrc 重新生成。
    # 生成失败时沿用已有 syso（其余构建内容不受影响）；两者都没有才报错。
    $icoSrc = Join-Path $root "image\favicon-multi-size.ico"
    $icoSyso = Join-Path $desktop "rsrc_windows_amd64.syso"
    if (Test-Path -LiteralPath $icoSrc) {
        $sysoStale = (-not (Test-Path -LiteralPath $icoSyso)) -or
            ((Get-Item -LiteralPath $icoSrc).LastWriteTimeUtc -gt (Get-Item -LiteralPath $icoSyso).LastWriteTimeUtc)
        if ($sysoStale) {
            Write-Host "    生成 Windows 图标资源: $icoSyso"
            go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico $icoSrc -o $icoSyso
            if ($LASTEXITCODE -ne 0) {
                if (-not (Test-Path -LiteralPath $icoSyso)) { throw "图标资源生成失败且无可用 syso: $icoSyso" }
                Write-Host "    [warn] rsrc 生成失败，沿用已有 syso: $icoSyso"
            }
        }
        Write-Host "    ok: icon = $icoSrc"
    } else {
        Write-Host "    [warn] 图标源缺失，跳过: $icoSrc"
    }

    # 仅在非空时传 -tags：Windows PowerShell 5.1 会丢弃空串实参，
    # 使 `-tags` 误吞 `-ldflags`，进而把 `-H windowsgui` 当成未知顶层参数（见 2026-10-14 修复）。
    $tagsArg = if ($desktopTags) { @("-tags", $desktopTags) } else { @() }
    go build @tagsArg -ldflags "-H windowsgui" -o $outGuiExe .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
    $mb = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
    Write-Host "    ok: $outGuiExe ($mb MB)"
} finally { Pop-Location }

# -- 2.5) 旧布局残留清理（2026-11-02 目录扁平化）--
#   历史产物 dist\desktop / dist\other / dist\initdata 与根下其它 exe/dll 全部清掉，
#   保证 dist 根恒为：chonkpilot.exe · chonkpilot-remote-server.exe(+.db) · themes\ · capability\。
Write-Host "==> [2.5/7] clean legacy layout artifacts"
foreach ($legacyDir in @((Join-Path $dist "desktop"), (Join-Path $dist "other"), (Join-Path $dist "initdata"), (Join-Path $dist "plugin-apps"))) {
    if (Test-Path -LiteralPath $legacyDir) {
        Write-Host "    [清理] 旧布局目录: $legacyDir"
        Remove-SafeItem -LiteralPath $legacyDir -Recurse
    }
}
$rootFileAllow = @("chonkpilot.exe", "chonkpilot-remote-server.exe", "chonkpilot-remote-server.db")
Get-ChildItem -Path $dist -File -Force -ErrorAction SilentlyContinue |
    Where-Object { ($_.Extension -in @(".exe", ".dll")) -and ($rootFileAllow -notcontains $_.Name) } |
    ForEach-Object {
        Write-Host "    [清理] 根下陈旧产物: $($_.Name)"
        Remove-SafeItem -LiteralPath $_.FullName
    }


# -- 3.5) 2026-10-19（G-68 第二步）：server-tools 已撤销 —— meta/domain 控制面工具改为
#         core-mcp-server 的一工具一 exe（由 build-mcp-server.ps1 产出到 capability/executors）。 --

# -- 4) stage capability/（契约全量，直接从出厂数据唯一源铺；不再经 dist\other） --
#    2026-11-02：目录扁平化后 capability 直接落 dist\capability —— 与旧 build-mcp-server.ps1 的
#    「契约部署」等价（同 6 棵子树 + 剔除规则），但**不产出** mcp-server/gateway/executors。
Write-Host "==> [3/7] stage capability/ (src/initdata/capability)"
$capDst = Join-Path $dist "capability"
Remove-SafeItem -LiteralPath $capDst -Recurse
New-Item -ItemType Directory -Force -Path $capDst | Out-Null
foreach ($sub in @("prompts", "tools", "resources", "skills", "agents", "system")) {
    $subSrc = Join-Path $initCap $sub
    if (-not (Test-Path $subSrc)) { throw "capability 源缺失: $subSrc" }
    Copy-Item $subSrc $capDst -Recurse -Force
}
# 插件化剔除：file 域 11 工具随 file 插件投放（PLUGIN-1），契约不得进 capability 根（否则工具名冲突）。
# embed 兜底副本（[0.6/7] 已同步）同步剔除。
foreach ($dst in @((Join-Path $capDst "tools\file"), (Join-Path $root "src\lib\mcp-server\server\contracts\tools\file"))) {
    if (Test-Path $dst) { Remove-SafeItem -LiteralPath $dst -Recurse }
}
# 未发布草稿剔除：skills/se/（源留在 src/initdata，不随产物投放）。
$seDraft = Join-Path $capDst "skills\se"
if (Test-Path $seDraft) { Remove-SafeItem -LiteralPath $seDraft -Recurse }
# 2026-11-08：core 域工具契约已全部迁出/删除（ask_user / next_run / subchat_open /
# subchat_finish → chonkpilot-plugins/task；tool_result 删除，结果读取走 task_status）→
# 不再投放 capability/tools/server；清理旧产物。
$serverToolsLegacy = Join-Path $capDst "tools\server"
if (Test-Path $serverToolsLegacy) { Remove-SafeItem -LiteralPath $serverToolsLegacy -Recurse }
Write-Host "    ok: capability/tools/server 已移除（core 无域工具契约）"

# -- 4.5) manifest 插件（PLUGIN-1）：manifest 来自 src/initdata，exe 由 build-plugins.ps1 产出 --
#   插件目录 = dist/capability/plugins/<id>/（plugin.json + exe 同目录；
#   manifest 的 runtime.exe 是插件目录内相对路径）。
#   ⚠️ PLUGIN-UI-1：`ui.selfServe: true` 的插件（本轮起 = browser/codegraph/desktop/file/mcp/task/websearch）
#   自持 webui —— 资源 embed 在插件 exe 内，webui 唯一源 = `chonkpilot-plugins/<id>/webui/`，
#   产物内**不得**出现 `<id>/webui/`（避免双源漂移；判定见下方通用规则）。
Write-Host "==> [4/7] stage plugins (manifest + exe)"
$initPlugins = Join-Path $root "src\initdata\capability\plugins"
if (Test-Path $initPlugins) {
    $plgDst = Join-Path $dist "plugins"
    Remove-SafeItem -LiteralPath $plgDst -Recurse
    Copy-Item $initPlugins $plgDst -Recurse -Force
    # 演示插件不随产物投放：demo-python 是「插件规范全功能演示」样例（manifest product=false），
    # 其 mcp.origin=builtin 与 product=false 的组合会被加载器拒绝（每次启动告警跳过）。
    # 需要演示时从 src/initdata 手工安装到用户级 capability 即可。
    $demoPlg = Join-Path $plgDst "demo-python"
    if (Test-Path $demoPlg) { Remove-SafeItem -LiteralPath $demoPlg -Recurse }
    # 自持 webui（PLUGIN-UI-1）：manifest 声明 `ui.selfServe: true` 的插件，资源已 embed 在
    # 插件 exe 内（唯一源 = `chonkpilot-plugins/<id>/webui/`），产物内**不得**出现 `<id>/webui/`。
    # 通用规则：遍历全部插件 manifest 判定，不按插件 id 逐个列举（新增 selfServe 插件自动覆盖）。
    Get-ChildItem -Path $plgDst -Directory -Force -ErrorAction SilentlyContinue | ForEach-Object {
        # PLUGIN-LOAD-1：plugin.json 已极简（无 ui 段），selfServe 判定改读插件自带的 manifest.json。
        $ckManifest = Join-Path $_.FullName "manifest.json"
        if (-not (Test-Path -LiteralPath $ckManifest)) { return }
        $ckManifestJSON = $null
        try {
            $ckManifestJSON = Get-Content -LiteralPath $ckManifest -Raw -Encoding UTF8 | ConvertFrom-Json
        } catch {
            Write-Host ("    [警告] manifest 解析失败（跳过 selfServe 判定）: " + $ckManifest)
            return
        }
        # PSObject.Properties 取键（避免 StrictMode 下缺键取属性抛异常）。
        $ckUIProp = $ckManifestJSON.PSObject.Properties['ui']
        if (-not $ckUIProp -or -not $ckUIProp.Value) { return }
        $ckSelfServeProp = $ckUIProp.Value.PSObject.Properties['selfServe']
        if (-not $ckSelfServeProp -or ($ckSelfServeProp.Value -ne $true)) { return }
        $ckSelfServeWebui = Join-Path $_.FullName "webui"
        if (Test-Path -LiteralPath $ckSelfServeWebui) {
            Remove-SafeItem -LiteralPath $ckSelfServeWebui -Recurse
        }
        Write-Host ("    selfServe: " + $_.Name + "（webui 由插件 exe 自持，产物不带 webui/）")
    }
    Invoke-ChildBuild "build-plugins.ps1"
    Write-Host "    ok: capability/plugins/"
} else {
    Write-Host "    skip（src/initdata/capability/plugins 不存在）"
}

# -- 5) 出厂场景：src/initdata/capability/scenarios → dist/capability/scenarios（覆盖式同步，不删目录本身） --
Write-Host "==> [5/7] stage scenarios/ (src/initdata/capability/scenarios)"
if (-not (Test-Path $initScenarios)) { throw "scenarios not found: $initScenarios" }
$scnDst = Join-Path $dist "capability\scenarios"
New-Item -ItemType Directory -Force -Path $scnDst | Out-Null
# 覆盖式：镜像源内容（先清空源内已删除项的残留 → 逐场景目录覆盖），但**保留 scenarios 目录本身**
$scnKeep = Get-ChildItem -Path $initScenarios -Force | ForEach-Object { $_.Name }
Get-ChildItem -Path $scnDst -Force | Where-Object { $_.Name -notin $scnKeep } | ForEach-Object {
    Remove-SafeItem -LiteralPath $_.FullName -Recurse
}
Copy-Item (Join-Path $initScenarios "*") $scnDst -Recurse -Force
$scnCount = (Get-ChildItem $scnDst -Recurse -File | Measure-Object).Count
Write-Host "    ok: capability/scenarios/ ($scnCount files)"

# -- 6) 远端服务（chonkpilot-remote-server.exe）：调用同级仓 build.ps1 后拷入 dist 根 --
#    2026-11-02：远端服务随桌面发行根一起投放（H5 + 后台管理页已 embed）；
#    数据库 chonkpilot-remote-server.db 由 exe 自己落「exe 所在目录」，此处跑一次 user list 生成。
Write-Host "==> [6/7] build chonkpilot-remote-server.exe"
if (-not (Test-Path (Join-Path $remoteRepo "build.ps1"))) {
    Write-Host "    [警告] 同级仓缺失: $remoteRepo（跳过远端服务投放）"
} else {
    & (Join-Path $remoteRepo "build.ps1")
    if (-not $? -or $LASTEXITCODE -ne 0) { throw "remote build.ps1 失败 (LASTEXITCODE=$LASTEXITCODE)" }
    $remoteExe = Join-Path $remoteRepo "dist\chonkpilot-remote-server.exe"
    if (-not (Test-Path $remoteExe)) { throw "远端服务产物缺失: $remoteExe" }
    Copy-Item $remoteExe (Join-Path $dist "chonkpilot-remote-server.exe") -Force
    # 生成 SQLite 库（无账号时首启会建默认 admin/admin；这里只建表 + 建库，不写账号）
    & (Join-Path $dist "chonkpilot-remote-server.exe") user list | Out-Null
    $mbRemote = [Math]::Round((Get-Item (Join-Path $dist "chonkpilot-remote-server.exe")).Length / 1MB, 1)
    Write-Host "    ok: chonkpilot-remote-server.exe ($mbRemote MB) + chonkpilot-remote-server.db"
}

# -- 清单 --
$mbGui = [Math]::Round((Get-Item $outGuiExe).Length / 1MB, 1)
$capCount = (Get-ChildItem (Join-Path $dist "capability") -Recurse -File | Measure-Object).Count
Write-Host "==> done: $dist"
Write-Host "    chonkpilot.exe                      $mbGui MB"
if (Test-Path (Join-Path $dist "chonkpilot-remote-server.exe")) {
    Write-Host "    chonkpilot-remote-server.exe        $mbRemote MB (+ chonkpilot-remote-server.db)"
}
Write-Host "    capability/                         $capCount files (prompts/tools/resources/skills/agents/system)"
Write-Host "    plugins/                            $((Get-ChildItem (Join-Path $dist 'plugins') -Directory -ErrorAction SilentlyContinue | Measure-Object).Count) 个（与 capability 同级）"
Write-Host "    capability/scenarios/               $scnCount files"
Write-Host "    themes/themes.css"
Write-Host "    run: cd $dist ; .\chonkpilot.exe"
Write-Host "    remote: cd $dist ; .\chonkpilot-remote-server.exe serve   → 后台管理 /admin（默认 admin/admin）"
