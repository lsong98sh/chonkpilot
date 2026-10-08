# build-mcp-server.ps1：一键构建并组装独立 MCP server 部署目录
#
# 产物布局（dist/other/ —— 与 mcp-gateway 同目录共用，见 [41 D-28]）：
#   ├── chonkpilot-mcp-server.exe    # MCP server（独立 exe，Streamable HTTP，默认 127.0.0.1:5700/mcp）
#   ├── chonkpilot-mcp-gateway.exe   # gateway exe（由 build-mcp-gateway.ps1 产出到同一目录）
#   ├── config.json                  # 运行配置（首次生成，已有不覆盖；两 exe 同款自动探测）
#   └── capability/                  # 能力目录（mcp-server 默认 root = exe 目录/capability）
#       ├── tools/<cat>/     # 工具契约（*.tool.md；runtime 相对 md 写 ../../executors/<cat>.exe）
#       │   ├── core/          #   file_find/file_read/... .tool.md
#       │   ├── desktop/       #   desktop_run.tool.md
#       │   └── browser/       #   browser_run.tool.md
#       ├── skills/      # 技能契约（*.skill.md；扁平）
#       ├── prompts/     # 提示词契约（*.prompt.md；扁平）
#       ├── resources/   # 资源契约（*.resource.md；扁平）
#       ├── agents/      # 智能体契约（*.agent.md；扁平）
#       ├── system/      # 非原语系统文档（如 summary.md；OP-01/OP-02，2026-10-06）
#       └── executors/   # 执行器（chonkpilot-{core,desktop,browser,dsl}-executor.exe；dsl 由 build-dsl-executor.ps1 产出）
#
# 源（出厂数据唯一源 = src/initdata）：契约 = src/initdata/capability/{prompts,tools,resources,skills,agents,system}
#   （2026-10-01 P2 扁平化：删除旧 knowledge/ 归并层）；
#   exe 外壳 = src/others/mcp-server（lib = src/lib/mcp-server）+ executor = src/lib/mcp-tools。
#
# 用法：.\build-mcp-server.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

# 结束运行中的旧实例（编译前清理，释放 exe 占用）：本脚本重建 mcp-server 与 3 个 executor，
# 同目录 gateway 共用本 capability，故一并清理（对齐 build-dsl-executor/codegraph/vfts 写法）。
foreach ($name in @("chonkpilot-core-executor", "chonkpilot-desktop-executor", "chonkpilot-browser-executor", "chonkpilot-mcp-server", "chonkpilot-mcp-gateway")) {
    Get-Process -Name $name -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
}

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$tools = Join-Path $root "src\lib\mcp-tools"
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

Write-Host "==> [1/3] build mcp-server (standalone exe)"
Push-Location $cmd
try {
    go build -o (Join-Path $dist "chonkpilot-mcp-server.exe") .
    if ($LASTEXITCODE -ne 0) { throw "build mcp-server failed" }
    Write-Host "    ok: chonkpilot-mcp-server.exe"
} finally { Pop-Location }

Write-Host "==> [2/3] deploy contracts -> capability (src/initdata -> dist/other)"
# 契约目录覆盖式同步（6 棵平坦子树：prompts/tools/resources/skills/agents/system，清理历史 knowledge/ 残留）。
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

Write-Host "==> [3/3] build executors -> capability/executors/ (chonkpilot-mcp-tools)"
$execDir = Join-Path $cap "executors"
New-Item -ItemType Directory -Force -Path $execDir | Out-Null
Push-Location $tools
try {
    # 内置执行器统一落 capability/executors/；契约 *.tool.md 的 runtime 相对 md 写 ../../executors/<cat>.exe
    foreach ($pair in @(@("core", "chonkpilot-core-executor.exe"), @("desktop", "chonkpilot-desktop-executor.exe"), @("browser", "chonkpilot-browser-executor.exe"))) {
        $cat = $pair[0]
        $exeName = $pair[1]
        go build -o (Join-Path $execDir $exeName) ./$cat
        if ($LASTEXITCODE -ne 0) { throw "build $exeName failed" }
        Write-Host "    ok: $exeName (executors)"
    }
} finally { Pop-Location }

Write-Host "==> verify executors (--help 从磁盘读契约：<exeDir>/capability/tools/<cat>/)"
& (Join-Path $execDir "chonkpilot-core-executor.exe") --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "core executor --help failed" }
& (Join-Path $execDir "chonkpilot-desktop-executor.exe") --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "desktop executor --help failed" }
& (Join-Path $execDir "chonkpilot-browser-executor.exe") --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "browser executor --help failed" }

# dsl_run 统一编排执行器（决策 42 §2 (247)）：与内置 executor 同目录；源缺失时脚本内部跳过。
Write-Host "==> build dsl executor -> capability/executors/"
& (Join-Path $root "build-dsl-executor.ps1")
if ($LASTEXITCODE -ne 0) { throw "build-dsl-executor.ps1 failed" }

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
