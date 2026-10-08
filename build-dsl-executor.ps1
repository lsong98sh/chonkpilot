# build-dsl-executor.ps1：构建 dsl_run 统一编排执行器（chonkpilot-dsl-executor）
#
# 产物布局（与内置 executor 同目录约定）：dist/other/capability/executors/chonkpilot-dsl-executor.exe
#   —— build-mcp-server.ps1 的 capability 同步会把它带进发行目录（dist/desktop、dist/gui、dist/server）。
# gateway（src/lib/gateway）在 dsl_run 调用时解析该路径（候选：<exeDir>/chonkpilot-dsl-executor.exe、
#   <exeDir>/capability/executors/chonkpilot-dsl-executor.exe；见 gateway/dslrun.go dslExecutorPath），
#   并以**自建 stdio 行协议**（非 MCP）交互；LLM 步骤经 gateway→MQ 执行。
#
# 源：`src/lib/mcp-tools/dslexec`（module chonkpilot-mcp-tools 内，可导入 internal/{fileops,browser,desktop}）。
# 说明：dslexec 源尚未落地时**跳过并告警**（不中断整体构建；由执行器负责人补齐后自动生效）。
#
# 用法：.\build-dsl-executor.ps1
$ErrorActionPreference = "Stop"

$env:GOROOT = "e:\GoDev\go1.26"
$env:Path = "e:\GoDev\go1.26\bin;" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$tools = Join-Path $root "src\lib\mcp-tools"
$src = Join-Path $tools "dslexec"
$execDir = Join-Path $root "dist\other\capability\executors"
$exeName = "chonkpilot-dsl-executor.exe"

if (-not (Test-Path $src)) {
    Write-Warning "dslexec 源目录缺失（$src），跳过 dsl executor 构建（dsl_run 运行期不可用）"
    exit 0
}

New-Item -ItemType Directory -Force -Path $execDir | Out-Null
Get-Process -Name "chonkpilot-dsl-executor" -ErrorAction SilentlyContinue | Stop-Process -Force

Write-Host "==> build dsl executor (chonkpilot-mcp-tools/dslexec)"
Push-Location $tools
try {
    go build -o (Join-Path $execDir $exeName) ./dslexec
    if ($LASTEXITCODE -ne 0) { throw "build failed" }
    $exe = Get-Item (Join-Path $execDir $exeName)
    Write-Host "    ok: $($exe.Name) ($([math]::Round($exe.Length/1MB,1)) MB) -> $execDir"
} finally { Pop-Location }
