# build-vfts.ps1：构建并组装 chonkpilot-vfts-mcp-server 独立部署目录
#
# 产物布局（dist/plugins/vfts/，见 [41 D-28]）：
#   ├── chonkpilot-vfts-mcp-server.exe   # 进程内 zvec FTS 全文索引 MCP server（console，CGO）
#   └── zvec_c_api.dll                   # zvec C-API 原生动态库（必须与 exe 同目录，运行时加载）
#
# 源（D-28 src 结构）：`src/plugins/vfts`（engine 本体；插件侧 = `src/plugins/plugin-vfts`）。
# 就位：本脚本只产出**插件/引擎构建区**（dist/plugins/*）；宿主发行目录（dist/desktop、
#   dist/server）由 build-desktop.ps1 / build-gui.ps1 从本目录取件后置于**发行根**——
#   插件按「宿主 exe 同目录」解析引擎 exe，且 zvec_c_api.dll **必须与 vfts 引擎 exe 同目录**。
#
# 说明（契约落点）：vfts 与 codegraph 同构——工具经官方 go-sdk AddTool 直挂、不落 .tool.md，
# 故本目录不产出 capability 契约（与 build-codegraph.ps1 现状一致）。
#
# 用法：.\build-vfts.ps1
$ErrorActionPreference = "Stop"

# ── 工具链（对齐 build-codegraph.ps1；vfts 需 CGO 链接 zvec C-API）──
# 路径：**强制**使用本仓要求的 Go 工具链与 MinGW 路径（不采信外部 GOROOT：本机 shell 常
# 预置 `e:\GoDev\go`（旧版），沿用会导致 go.mod 版本校验失败）。
$env:GOROOT = "e:\GoDev\go1.26"
$env:CHONK_MSYS64_BIN = "e:\GoDev\msys64\ucrt64\bin"
$env:Path = (Join-Path $env:GOROOT "bin") + ";" + $env:CHONK_MSYS64_BIN + ";" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"
$env:CGO_ENABLED = "1"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$mod = Join-Path $root "src\plugins\vfts"
$dist = Join-Path $root "dist\plugins\vfts"
$zvec = Join-Path $mod "third_party\zvec"
$zvecLib = Join-Path $zvec "windows_amd64"

# zvec C-API 头/库（zvec-go 模块自带 `-L${SRCDIR}/lib/windows_amd64` 但模块缓存无 lib/，
# 故用以下环境变量把搜索路径指向本仓 third_party；MinGW ld 可直接读 MSVC 的 .lib）
$env:CGO_CFLAGS = "-I$($zvec.Replace('\','/'))/include"
$env:CGO_LDFLAGS = "-L$($zvecLib.Replace('\','/')) -lzvec_c_api"

if (-not (Test-Path (Join-Path $zvecLib "zvec_c_api.dll"))) {
    throw "zvec 运行库缺失：$zvecLib\zvec_c_api.dll（该运行库随源码入库；缺失多为检出/清理不完整，请检查 src/plugins/vfts/third_party/zvec/windows_amd64/）"
}
if (-not (Test-Path (Join-Path $zvecLib "zvec_c_api.lib"))) {
    throw "zvec 链接库缺失：$zvecLib\zvec_c_api.lib（CGO 链接需 -lzvec_c_api）"
}

New-Item -ItemType Directory -Force -Path $dist | Out-Null

# kill 运行中的旧实例，避免 exe/dll 被占用
Get-Process -Name "chonkpilot-vfts-mcp-server" -ErrorAction SilentlyContinue | Stop-Process -Force

Write-Host "==> build vfts mcp-server (CGO console exe)"
Push-Location $mod
try {
    go build -o (Join-Path $dist "chonkpilot-vfts-mcp-server.exe") .
    if ($LASTEXITCODE -ne 0) { throw "build failed（检查 gcc 与 zvec 头/库路径：$zvec）" }
    $exe = Get-Item (Join-Path $dist "chonkpilot-vfts-mcp-server.exe")
    Write-Host "    ok: $($exe.Name) ($([math]::Round($exe.Length/1MB,1)) MB)"
} finally { Pop-Location }

Write-Host "==> deploy zvec_c_api.dll -> dist/plugins/vfts (必须与 exe 同目录)"
Copy-Item (Join-Path $zvecLib "zvec_c_api.dll") $dist -Force
$dll = Get-Item (Join-Path $dist "zvec_c_api.dll")
Write-Host "    ok: $($dll.Name) ($([math]::Round($dll.Length/1MB,1)) MB)"

Write-Host "==> done. run (必须显式指定运行形态):"
Write-Host "    HTTP 前台:   cd $dist ; .\chonkpilot-vfts-mcp-server.exe --http          # 默认 127.0.0.1:5702，端点 /mcp"
Write-Host "    stdio(IDE):  .\chonkpilot-vfts-mcp-server.exe --stdio                   # Trae 等 spawn"
Write-Host "    自检:        .\chonkpilot-vfts-mcp-server.exe -probe <dir>              # 索引目录打印汇总（可附 -match/-query）"
