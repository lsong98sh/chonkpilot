# build-browser.ps1：ChonkPilot browser 形态前端构建（无参数）
#
# browser 形态 = 服务端 HTTP 入口（`chonkpilot-server.exe --http-addr=...`）托管的纯浏览器前端
#   （19 §8.8/§8.9；U1 跨进程入口方案 A）。
#   - 前端源码工程 = 仓库级 src/frontend（决策 [42 §2 (115)] / [19 §3.2]）
#   - 入口 = index.html（browser 形态）；embed/GUI 入口 = embed.html，见 build-desktop.ps1
#   - 产物 = **src/server/frontend/dist/**（2026-09-21 用户拍板：browser 静态面
#     **go:embed 进 chonkpilot-server.exe**——vite outDir 与该 embed 落点对齐，
#     embed 声明 = src/server/frontend.go 的 `//go:embed all:frontend/dist`）
#   - `--web-root` **保留为覆盖**：显式指定时才读外部目录；未指定 = 用内嵌静态面
#   - 服务端 exe（chonkpilot-server.exe）由 build-gui.ps1 产出到 dist/server/
#     （**先跑本脚本**，再跑 build-gui.ps1，静态页才会被 embed 进 exe）
#
# 用法：.\build-browser.ps1
#   （构建后直接启动服务端即可，无需 --web-root：
#      dist/server\chonkpilot-server.exe --http-addr=127.0.0.1:5668 --work-dir <工程目录>
#    如需改用外部目录覆盖内嵌面，再显式加 --web-root=<目录>）
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$frontend = Join-Path $root "src\frontend"
# embed 落点（= src/server 的 //go:embed all:frontend/dist 目标目录）
$outDir = Join-Path $root "src\server\frontend\dist"

Write-Host "==> [1/1] build frontend (browser entry) -> src/server/frontend/dist (go:embed)"
Push-Location $frontend
try {
    if (-not (Test-Path "node_modules")) {
        Write-Host "    node_modules 缺失，npm ci ..."
        npm ci 2>&1 | ForEach-Object { Write-Host "    $_" }
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
    }
    # stderr warning（如 rollup circular / chunk 体积提示）在 $ErrorActionPreference=Stop 下会被当作
    # terminating ErrorRecord 中断脚本，与真实构建成败无关；构建段临时降为 Continue，
    # 以 $LASTEXITCODE 为唯一判定（同 desktop / gui）。
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    npm run build 2>&1 | Select-Object -Last 8
    $ErrorActionPreference = $prevEAP
    if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
} finally { Pop-Location }

$indexHtml = Join-Path $outDir "index.html"
if (-not (Test-Path $indexHtml)) { throw "index.html 未产出: $indexHtml" }
$count = (Get-ChildItem $outDir -Recurse -File | Measure-Object).Count
Write-Host "==> done: $outDir ($count files) — 下一步 .\build-gui.ps1 把静态页 embed 进 chonkpilot-server.exe"
Write-Host "    serve: dist/server\chonkpilot-server.exe --http-addr=127.0.0.1:5668 --work-dir <workdir>   (静态面 = 内嵌；--web-root 可覆盖)"
