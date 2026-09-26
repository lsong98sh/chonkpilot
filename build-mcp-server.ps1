# build-mcp-server.ps1：一键构建并组装独立 MCP server 部署目录
#
# 产物布局（dist/other/ —— 与 mcp-gateway 同目录共用，见 [41 D-28]）：
#   ├── chonkpilot-mcp-server.exe    # MCP server（独立 exe，Streamable HTTP，默认 127.0.0.1:5700/mcp）
#   ├── chonkpilot-mcp-gateway.exe   # gateway exe（由 build-mcp-gateway.ps1 产出到同一目录）
#   ├── config.json                  # 运行配置（首次生成，已有不覆盖；两 exe 同款自动探测）
#   └── capability/                  # 能力目录（mcp-server 默认 root = exe 目录/capability）
#       ├── tools/       # 工具契约 + 执行器（*.tool.md 与 executor 同目录，runtime 相对 md 解析）
#       │   ├── core/       #   chonkpilot-core-executor.exe + file_find/file_read/... .tool.md
#       │   ├── desktop/    #   chonkpilot-desktop-executor.exe + desktop_run.tool.md
#       │   └── browser/    #   chonkpilot-browser-executor.exe + browser_run.tool.md
#       ├── skills/      # 技能契约（*.skill.md）
#       ├── prompts/     # 提示词契约（*.prompt.md）
#       └── resources/   # 资源契约（*.resource.md）
#
# 源（D-28 src 结构）：exe 外壳 = src/others/mcp-server（lib = src/lib/mcp-server）；
#   契约 = src/lib/mcp-tools/internal/contracts/tools + src/lib/mcp-server/contracts。
#
# 用法：.\build-mcp-server.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$tools = Join-Path $root "src\lib\mcp-tools"
$server = Join-Path $root "src\lib\mcp-server"
$cmd = Join-Path $root "src\others\mcp-server"
$dist = Join-Path $root "dist\other"
$cap = Join-Path $dist "capability"

New-Item -ItemType Directory -Force -Path $cap | Out-Null
# 历史残留清理：旧布局 executor 曾放 capability 根（现随 tools/<cat>/ 部署）
foreach ($legacy in @("chonkpilot-core-executor.exe", "chonkpilot-desktop-executor.exe", "chonkpilot-browser-executor.exe")) {
    $legacyPath = Join-Path $cap $legacy
    if (Test-Path $legacyPath) { [System.IO.File]::Delete($legacyPath) }
}

Write-Host "==> [1/3] build mcp-server (standalone exe)"
Push-Location $cmd
try {
    go build -o (Join-Path $dist "chonkpilot-mcp-server.exe") .
    if ($LASTEXITCODE -ne 0) { throw "build mcp-server failed" }
    Write-Host "    ok: chonkpilot-mcp-server.exe"
} finally { Pop-Location }

Write-Host "==> [2/3] deploy contracts -> capability"
# 契约目录覆盖式同步（tools/skills/prompts/resources 整树，清理历史残留）
# tools 源 = mcp-tools（internal/contracts/tools，executor --help 的同一份嵌入源）；
# skills/prompts/resources 源 = mcp-server（lib）。
# 注：chonkpilot-mcp-gateway.exe 与 mcp-server exe **同目录共用** capability（build-mcp-gateway.ps1 不再另建副本）。
$toolsContract = Join-Path $tools "internal\contracts\tools"
if (Test-Path $toolsContract) {
    $capTools = Join-Path $cap "tools"
    if (Test-Path $capTools) { [System.IO.Directory]::Delete($capTools, $true) }
    Copy-Item $toolsContract $cap -Recurse -Force
} else {
    throw "tools contracts not found: $toolsContract"
}
foreach ($prim in @("skills", "prompts", "resources")) {
    $primPath = Join-Path $cap $prim
    if (Test-Path $primPath) { [System.IO.Directory]::Delete($primPath, $true) }
    Copy-Item (Join-Path $server "contracts\$prim") $cap -Recurse -Force
}

Write-Host "==> [3/3] build executors -> capability/tools/<cat>/ (chonkpilot-mcp-tools)"
Push-Location $tools
try {
    # 契约与执行器同目录部署：*.tool.md 的 runtime 相对 md 解析（draft：executor 贴近契约）
    foreach ($pair in @(@("core", "chonkpilot-core-executor.exe"), @("desktop", "chonkpilot-desktop-executor.exe"), @("browser", "chonkpilot-browser-executor.exe"))) {
        $cat = $pair[0]
        $exeName = $pair[1]
        New-Item -ItemType Directory -Force -Path (Join-Path $cap "tools\$cat") | Out-Null
        go build -o (Join-Path $cap "tools\$cat\$exeName") ./$cat
        if ($LASTEXITCODE -ne 0) { throw "build $exeName failed" }
        Write-Host "    ok: $exeName (tools/$cat)"
    }
} finally { Pop-Location }

Write-Host "==> verify executors"
& (Join-Path $cap "tools\core\chonkpilot-core-executor.exe") --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "core executor --help failed" }
& (Join-Path $cap "tools\desktop\chonkpilot-desktop-executor.exe") --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "desktop executor --help failed" }
& (Join-Path $cap "tools\browser\chonkpilot-browser-executor.exe") --help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "browser executor --help failed" }

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
