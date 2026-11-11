# build-mcp-server.ps1：一键构建并组装独立 MCP server 部署目录
#
# 产物布局（dist/other/ —— 与 mcp-gateway 同目录共用，见 [41 D-28]）：
#   ├── chonkpilot-mcp-server.exe    # MCP server（独立 exe，Streamable HTTP，默认 127.0.0.1:5700/mcp）
#   ├── chonkpilot-mcp-gateway.exe   # gateway exe（由 build-mcp-gateway.ps1 产出到同一目录）
#   ├── chonkpilot-core-mcp-server.exe / chonkpilot-self-tools.exe  # 工具前门 + app 根工具外部注册（宿主取件）
#   ├── config.json                  # 运行配置（首次生成，已有不覆盖；两 exe 同款自动探测）
#   └── capability/                  # 能力目录（mcp-server 默认 root = exe 目录/capability）
#       ├── tools/<cat>/     # 工具契约（*.tool.md；file 域 11 工具随 file 插件投放，不在此）
#       │   └── server/        #   域工具（2026-11-08 起只剩 tool_result；其余已迁 task 插件）
#       ├── skills/      # 技能契约（*.skill.md；按 core|ux 分级嵌套，se/ 为未发布草稿，见下不投放）
#       ├── prompts/     # 提示词契约（*.prompt.md；分级嵌套）
#       ├── resources/   # 资源契约（*.resource.md；按 core|go|ui 分级嵌套）
#       ├── agents/      # 智能体契约（*.agent.md；扁平）
#       ├── system/      # 非原语系统文档（如 summary.md；OP-01/OP-02，2026-10-06）
#       └── executors/   # （已退场，2026-10-31 D-72：无内置执行器；能力随插件投放）
#
# 源（出厂数据唯一源 = src/initdata）：契约 = src/initdata/capability/{prompts,tools,resources,skills,agents,system}
#   （2026-10-01 P2 扁平化：删除旧 knowledge/ 归并层）；
#   exe 外壳 = src/others/mcp-server（lib = src/lib/mcp-server）；executor 已全部退场（D-72）。
#
# 注（2026-10-28 P2-3）：脚本执行（`script_run` → `task_run`）已随 task 插件迁出主仓，
#   `chonkpilot-script-executor.exe` 不再产出。
#
# 用法：.\build-mcp-server.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

# 结束运行中的旧实例（编译前清理，释放 exe 占用）：本脚本重建 mcp-server / core-mcp-server /
# self-tools，同目录 gateway 共用本 capability，故一并清理。
# 2026-10-21（GATEWAY-2/L3）：meta/domain（含 ask-user / tool-result / tool-stop / next-run /
#   subchat-open / subchat-finish）已进程内化，9 个 executor 不再产出，从清理列表移除。
# 2026-10-31（D-72）：最后一个内置 executor（web-exec）也退场。
foreach ($name in @("chonkpilot-core-executor", "chonkpilot-filesys-executor", "chonkpilot-script-executor", "chonkpilot-web-executor", "chonkpilot-desktop-executor", "chonkpilot-browser-executor", "chonkpilot-mcp-server", "chonkpilot-core-mcp-server", "chonkpilot-mcp-gateway")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
}

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$cmd = Join-Path $root "src\others\mcp-server"
$initCap = Join-Path $root "src\initdata\capability"
$dist = Join-Path $root "dist\other"
$cap = Join-Path $dist "capability"

New-Item -ItemType Directory -Force -Path $cap | Out-Null
# 历史残留清理：旧布局 executor 曾放 capability 根；skills/prompts/resources 曾在 knowledge/ 归并层下
foreach ($legacy in @("chonkpilot-core-executor.exe", "chonkpilot-desktop-executor.exe", "chonkpilot-browser-executor.exe")) {
    $legacyPath = Join-Path $cap $legacy
    if (Test-Path $legacyPath) { [System.IO.File]::Delete($legacyPath) }
}
$legacyKnowledge = Join-Path $cap "knowledge"
if (Test-Path $legacyKnowledge) { [System.IO.Directory]::Delete($legacyKnowledge, $true) }

# 契约 embed 副本（GATEWAY-2 C，2026-10-21）：唯一源 = src/initdata/capability/{tools,prompts,skills,resources}，
# 覆盖式同步到 mcp-server 包的 `//go:embed all:contracts` 落点（磁盘 capability 根缺失时的 app 层兜底）。
# ⚠️ 目标目录为**构建生成物（勿手改，不入库；仅 .gitkeep 占位）**；独立 mcp-server exe 亦经此副本兜底。
Write-Host "==> [0/3] sync builtin contracts -> mcp-server embed dir"
$embedCap = Join-Path $root "src\lib\mcp-server\server\contracts"
New-Item -ItemType Directory -Force -Path $embedCap | Out-Null
foreach ($sub in @("tools", "prompts", "skills", "resources")) {
    $subSrc = Join-Path $initCap $sub
    if (-not (Test-Path $subSrc)) { throw "contracts not found: $subSrc" }
    $subDst = Join-Path $embedCap $sub
    if (Test-Path $subDst) { [System.IO.Directory]::Delete($subDst, $true) }
    Copy-Item $subSrc $embedCap -Recurse -Force
}
Write-Host "    ok: embed contracts -> $embedCap ($((Get-ChildItem $embedCap -Recurse -File | Measure-Object).Count) files)"

Write-Host "==> [1/3] build mcp-server (standalone exe)"
Push-Location $cmd
try {
    go build -o (Join-Path $dist "chonkpilot-mcp-server.exe") .
    if ($LASTEXITCODE -ne 0) { throw "build mcp-server failed" }
    Write-Host "    ok: chonkpilot-mcp-server.exe"
} finally { Pop-Location }

Write-Host "==> [1.5/3] build core-mcp-server (P6 / 2026-10-19)"
$msDir = Join-Path $root "src\lib\mcp-server"
Push-Location $msDir
try {
    # 2026-11-02：self-tools 已退役（桌面单体不再拉起，全仓无 spawn 点）；core-mcp-server 仅保留给
    # 独立 gateway 部署（build-mcp-gateway.ps1 以 --stdio 下游接入）。
    go build -o (Join-Path $dist "chonkpilot-core-mcp-server.exe") .\cmd\core-mcp-server
    if ($LASTEXITCODE -ne 0) { throw "build core-mcp-server failed" }
    Write-Host "    ok: chonkpilot-core-mcp-server.exe"
} finally { Pop-Location }

Write-Host "==> [2/3] deploy contracts -> capability (src/initdata -> dist/other)"
# 契约目录覆盖式同步（6 棵契约子树：prompts/tools/resources/skills/agents/system，清理历史 knowledge/ 残留）。
# agents/ = 智能体原语（*.agent.md；场景经 `${exeDir}/capability/agents/...` 引用，P4 2026-10-01）。
# system/ = **非原语**系统文档目录（如 summary.md；OP-01/OP-02，2026-10-06；不属知识库原语树）。
# 出厂数据唯一源 = src/initdata/capability（[42 决策]；2026-10-01 P2 扁平化）。
# 注：chonkpilot-mcp-gateway.exe 与 mcp-server exe **同目录共用** capability（build-mcp-gateway.ps1 不再另建副本）。
foreach ($sub in @("prompts", "tools", "resources", "skills", "agents", "system")) {
    $srcSub = Join-Path $initCap $sub
    if (-not (Test-Path $srcSub)) { throw "contracts not found: $srcSub" }
    $capSub = Join-Path $cap $sub
    if (Test-Path $capSub) { [System.IO.Directory]::Delete($capSub, $true) }
    Copy-Item $srcSub $cap -Recurse -Force
}

# file 系列（11 工具）已插件化（PLUGIN-1，2026-10-23）：契约随 file 插件投放
# （build-plugins.ps1 拷到 capability/plugins/file/tools/file），**不再进入 core capability 根**，
# 否则与插件注册的工具名冲突 → gateway 注册失败。embed 与部署两份都要剔除。
foreach ($dst in @((Join-Path $embedCap "tools\file"), (Join-Path $cap "tools\file"))) {
    if (Test-Path $dst) { [System.IO.Directory]::Delete($dst, $true) }
}

# 未发布草稿排除：skills/se/（用户 2026-10-10 裁决「暂不提交」，源留在 src/initdata）不随产物/出厂包投放。
# 显式删除以保证产物可复现、不误发布。
$seDraft = Join-Path $cap "skills\se"
if (Test-Path $seDraft) { [System.IO.Directory]::Delete($seDraft, $true) }

# 2026-11-08：core 域工具契约已全部迁出/删除（ask_user / next_run / subchat_open /
# subchat_finish → chonkpilot-plugins/task；tool_result 删除，结果读取走 task_status）→
# **不再投放** capability/tools/server；清理旧产物，避免与插件工具面重复。
$serverToolsLegacy = Join-Path $cap "tools\server"
if (Test-Path $serverToolsLegacy) { [System.IO.Directory]::Delete($serverToolsLegacy, $true) }
Write-Host "    ok: capability/tools/server 已移除（core 无域工具契约）"

Write-Host "==> [3/3] executors 已全部退场（2026-10-31 D-72）：不产出 capability/executors/"

# 注（D-72，2026-10-31）：`src/lib/mcp-tools`（`chonkpilot-web-executor.exe` + internal/{cli,fetch,fileops}）
# 整体退场——web 三工具随 `web` 插件、file 随 `file` 插件、task_run 随 `task` 插件，本层已无出厂契约绑定。

# 注（PLUGIN-SPLIT-1）：browser / desktop 常驻引擎已随插件迁入 chonkpilot-plugins
# （<id>/engine），由 build-plugins.ps1 直接构建，本脚本不再产出。

# 首次生成 config.json（已有则不覆盖，保留用户配置）
$cfgPath = Join-Path $dist "config.json"
if (-not (Test-Path $cfgPath)) {
    $cfg = @{
        timeout_sec = 300
        defaults    = @{
            skip_dirs    = @(".git", ".svn", "node_modules", ".trae", ".chonkpilot", "__pycache__", ".venv", "venv", "build", "dist", ".next", ".nuxt")
            ignore_files = @()
            fileext      = @()
        }
    }
    $json = $cfg | ConvertTo-Json -Depth 5
    [System.IO.File]::WriteAllText($cfgPath, $json, [System.Text.UTF8Encoding]::new($false))
    Write-Host "    created: config.json"
}

Write-Host "==> done. run (必须显式指定运行形态):"
Write-Host "    HTTP 前台:   cd $dist ; .\chonkpilot-mcp-server.exe --http          # 默认 127.0.0.1:5700，端点 /mcp"
Write-Host "    指定端口:    .\chonkpilot-mcp-server.exe --http=0.0.0.0:5700"
Write-Host "    stdio(IDE):  .\chonkpilot-mcp-server.exe --stdio                   # Trae 等 spawn：command=exe, args=--stdio"
Write-Host "    Windows 服务: .\chonkpilot-mcp-server.exe --service install        # 需管理员（服务运行 = HTTP）"
Write-Host "    服务自定义地址: .\chonkpilot-mcp-server.exe --service install --http=0.0.0.0:5700"
