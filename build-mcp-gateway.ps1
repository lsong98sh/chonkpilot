# build-mcp-gateway.ps1：一键构建独立 MCP gateway 部署目录
#
# 产物布局（dist/other/ —— 与 chonkpilot-mcp-server.exe **同目录共用** capability，见 [41 D-28]）：
#   ├── chonkpilot-mcp-gateway.exe  # gateway exe（官方 SDK 门面，对外 stdio/http/sse；
#   │                               #   -capability 缺省探测 exe 目录/capability；
#   │                               #   config.json 同 mcp-server 自动探测加载）
#   ├── chonkpilot-mcp-server.exe   # 独立 mcp-server exe（由 build-mcp-server.ps1 产出，同目录）
#   ├── config.json                 # 运行配置（build-mcp-server.ps1 生成）
#   └── capability/                 # 能力目录（tools/ + skills/ + prompts/ + resources/ + executors/，扁平）
#
# 来源 = dist/other（共享资产由 build-mcp-server.ps1 铺好）：gateway exe 内嵌
#   chonkpilot-mcp-server lib（进程内扫描 capability 契约根 + spawn 其中 executor），
#   不依赖独立 chonkpilot-mcp-server.exe —— 两 exe 同目录**共用同一份 capability**，不再另建副本。
# 源（D-28 src 结构）：exe 外壳 = src/others/mcp-gateway（lib = src/lib/gateway）。
#
# 用法：.\build-mcp-gateway.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$gwMod = Join-Path $root "src\others\mcp-gateway"
$dist = Join-Path $root "dist\other"
$dstExe = "chonkpilot-mcp-gateway.exe"

if (-not (Test-Path (Join-Path $dist "capability"))) {
    throw "dist/other/capability 不存在：请先运行 .\build-mcp-server.ps1 生成共享能力资产"
}
# 历史残留清理（旧名 chonkpilot-gateway.exe 曾随 mcp-server 部署）
$legacy = Join-Path $dist "chonkpilot-gateway.exe"
if (Test-Path $legacy) {
    [System.IO.File]::Delete($legacy)
    Write-Host "    removed legacy chonkpilot-gateway.exe from dist/other"
}

Write-Host "==> [1/2] build chonkpilot-mcp-gateway.exe -> dist/other"
Push-Location $gwMod
try {
    go build -o (Join-Path $dist $dstExe) .
    if ($LASTEXITCODE -ne 0) { throw "build $dstExe failed" }
    Write-Host "    ok: $dstExe"
} finally { Pop-Location }

Write-Host "==> [2/2] verify"
$probe = Join-Path $dist $dstExe
if (-not (Test-Path $probe)) { throw "$dstExe missing in $dist" }
if (-not (Test-Path (Join-Path $dist "capability\executors\chonkpilot-core-executor.exe"))) {
    throw "capability executors missing in $dist"
}
Write-Host "    ok: $dstExe + capability staged in $dist"

Write-Host "==> done. run (默认 -capability 探测 exe 目录/capability):"
Write-Host "    stdio:       cd $dist ; .\chonkpilot-mcp-gateway.exe -transport stdio"
Write-Host "    HTTP:        cd $dist ; .\chonkpilot-mcp-gateway.exe -transport http://127.0.0.1:5556"
Write-Host "    SSE:         cd $dist ; .\chonkpilot-mcp-gateway.exe -transport sse://127.0.0.1:5556"
Write-Host "    接入列表:    .\chonkpilot-mcp-gateway.exe -transport http://127.0.0.1:5556 -servers-file servers.list"
