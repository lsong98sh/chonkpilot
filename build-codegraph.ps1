# build-codegraph.ps1：构建并组装 chonkpilot-codegraph-mcp-server 独立部署目录
#
# 产物布局（dist/plugins/codegraph/，见 [41 D-28]）：
#   └── chonkpilot-codegraph-mcp-server.exe   # 进程内 tree-sitter 索引 MCP server（console，CGO）
#
# 源（D-28 src 结构）：`src/plugins/codegraph`（engine 本体；插件侧 = `src/plugins/plugin-codegraph`）。
# 就位：本脚本只产出**插件/引擎构建区**（dist/plugins/*）；宿主发行目录（dist/desktop、
#   dist/server）由 build-desktop.ps1 / build-gui.ps1 从本目录取件后置于**发行根**——
#   插件按「宿主 exe 同目录」解析引擎 exe（codegraph.go 候选路径），故发行根必须有一份。
#
# 用法：.\build-codegraph.ps1
$ErrorActionPreference = "Stop"

# ── 工具链（项目固定）──
# **强制**使用本仓要求的 Go 工具链与 MinGW 路径（不采信外部 GOROOT：本机 shell 常预置
# `e:\GoDev\go`（旧版），沿用会导致 go.mod 版本校验失败）。
$env:GOROOT = "e:\GoDev\go1.26"
$env:CHONK_MSYS64_BIN = "e:\GoDev\msys64\ucrt64\bin"
$env:Path = (Join-Path $env:GOROOT "bin") + ";" + $env:CHONK_MSYS64_BIN + ";" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"
# codegraph 组件 = 独立 console mcp-server（非主模块），允许 CGO（官方 go-tree-sitter）
$env:CGO_ENABLED = "1"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$mod = Join-Path $root "src\plugins\codegraph"
$dist = Join-Path $root "dist\plugins\codegraph"
New-Item -ItemType Directory -Force -Path $dist | Out-Null

# kill 运行中的旧实例，避免 exe 被占用
Get-Process -Name "chonkpilot-codegraph-mcp-server" -ErrorAction SilentlyContinue | Stop-Process -Force

Write-Host "==> build codegraph mcp-server (CGO console exe)"
Push-Location $mod
try {
    go build -o (Join-Path $dist "chonkpilot-codegraph-mcp-server.exe") .
    if ($LASTEXITCODE -ne 0) { throw "build failed" }
    $exe = Get-Item (Join-Path $dist "chonkpilot-codegraph-mcp-server.exe")
    Write-Host "    ok: $($exe.Name) ($([math]::Round($exe.Length/1MB,1)) MB)"
} finally { Pop-Location }

Write-Host "==> done. run (必须显式指定运行形态):"
Write-Host "    HTTP 前台:   cd $dist ; .\chonkpilot-codegraph-mcp-server.exe --http          # 默认 127.0.0.1:5701，端点 /mcp"
Write-Host "    stdio(IDE):  .\chonkpilot-codegraph-mcp-server.exe --stdio                   # Trae 等 spawn"
Write-Host "    自检:        .\chonkpilot-codegraph-mcp-server.exe -probe <dir>              # 索引目录打印符号汇总"
