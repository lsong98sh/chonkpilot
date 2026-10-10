# prepare-browser-fixture.ps1 —— browser 形态端到端测试夹具（幂等，可重复执行）
#
# 用途：为 browser 形态（chonkpilot-server.exe --http-addr …）准备一套可复现的测试数据：
#   ① 一个测试用户（在 server 的 auth 库；经 `login-register` 注册 / 重名则 `login-in` 登录）；
#   ② 3 个测试 workdir（落在 `--user-data-root` 允许根之下：proj-a / proj-b / proj-c，各含 1-2 个小文件）；
#   ③ 向该用户的 usr 配置自由键 `recent_dirs`（JSON 数组字符串）写入上述 3 个绝对路径
#      → 用于验证工具栏「最近目录」下拉（`gui.recent.list`）。
#
# 幂等保证：
#   - workdir / 文件：存在即复用，文件内容覆盖写为固定值（New-Item -Force / WriteAllText）；
#   - 用户：`login-register` 失败（login-username-taken）→ 回落 `login-in`；
#   - 认领（instance-claim）/ recent_dirs 写入：同一入参重复执行结果一致（last-write-wins）。
#
# 用法（先启动 server，见下「启动」；或传 -ServerExe 由本脚本拉起）：
#   pwsh -File src/test/manual/prepare-browser-fixture.ps1 -UserDataRoot <测试数据根>
#   pwsh -File src/test/manual/prepare-browser-fixture.ps1 -UserDataRoot <root> -ServerExe dist/server/chonkpilot-server.exe -WebRoot dist/server/www
#
# 启动（三选一；本脚本在被调用时若 server 未监听且给了 -ServerExe 会自动拉起）：
#   dist\server\chonkpilot-server.exe --http-addr=127.0.0.1:5668 --web-root=dist\server\www --user-data-root=<测试数据根>
#     （--web-root 可省略 → 用内嵌静态面；--project-roots 省略 → 缺省允许根 = --user-data-root）
#
# 清理：
#   Stop-Process -Name chonkpilot-server -ErrorAction SilentlyContinue
#   Remove-Item -Recurse -Force <测试数据根>            # 删除数据根（含 auth 库外的用户数据）
#   另：auth 库（默认 <server exe 目录>\auth.db，或 --auth-db 指定）如与数据根不同目录，需一并删除。
#
# 说明：本脚本只经 HTTP `POST /publish` 驱动（不直接写库 / 不碰 DB schema），故对实现无侵入。

[CmdletBinding()]
param(
    # 用户数据根 = server 的 `--user-data-root`（允许根缺省即此根）。默认落在 .gitignore 忽略的 systest `_*` 目录下。
    [string]$UserDataRoot = (Join-Path $PSScriptRoot '../chonkpilot-gui/systest/_browserfix_data'),
    # server HTTP 入口（须与 --http-addr 一致）。
    [string]$ServerBase = 'http://127.0.0.1:5668',
    # server exe 路径：给出且 server 未监听 → 本脚本拉起（可选）。
    [string]$ServerExe = '',
    # 静态面根：随 -ServerExe 拉起时传给 --web-root（空 = 用内嵌静态面）。
    [string]$WebRoot = '',
    # 测试用户（固定用户名 / 密码；占位口令，非真实凭据）。
    [string]$Username = 'fixture-user',
    [string]$Password = 'fixture-pass-123'
)

$ErrorActionPreference = 'Stop'

# ── 0. 解析 / 准备数据根（绝对化）────────────────────────────────────────────
$UserDataRoot = [System.IO.Path]::GetFullPath($UserDataRoot)
New-Item -ItemType Directory -Force -Path $UserDataRoot | Out-Null

$projects = @('proj-a', 'proj-b', 'proj-c')
$utf8 = New-Object System.Text.UTF8Encoding($false)

# ── 1. 三个测试 workdir（幂等）──────────────────────────────────────────────
foreach ($p in $projects) {
    $dir = Join-Path $UserDataRoot $p
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $dir 'README.md'), "# $p`nfixture project`n", $utf8)
    [System.IO.File]::WriteAllText((Join-Path $dir 'hello.txt'), "hello from $p`n", $utf8)
}

# ── 2. 确保 server 可达（可选：自动拉起）─────────────────────────────────────
function Test-ServerUp {
    try {
        Invoke-WebRequest -Uri "$ServerBase/" -UseBasicParsing -TimeoutSec 2 | Out-Null
        return $true
    } catch {
        return $false
    }
}

if (-not (Test-ServerUp)) {
    if (-not $ServerExe) {
        throw "server 未在 $ServerBase 监听。请先启动（或传 -ServerExe 由本脚本拉起）：`n" +
              "  <exe> --http-addr=127.0.0.1:5668 --web-root=<前端 dist> --user-data-root=$UserDataRoot"
    }
    # --http-addr 要**裸 host:port**（不带 scheme；带 http:// 会报 "too many colons in address"）。
    $serverAddr = $ServerBase -replace '^https?://', '' -replace '/+$', ''
    $spawnArgs = @("--http-addr=$serverAddr", "--user-data-root=$UserDataRoot")
    if ($WebRoot) { $spawnArgs += "--web-root=$WebRoot" }
    Write-Host "[fixture] 拉起 server: $ServerExe $($spawnArgs -join ' ')"
    Start-Process -FilePath $ServerExe -ArgumentList $spawnArgs | Out-Null
    $deadline = (Get-Date).AddSeconds(20)
    while (-not (Test-ServerUp)) {
        if ((Get-Date) -gt $deadline) { throw "server 启动超时（$ServerBase 未监听）" }
        Start-Sleep -Milliseconds 300
    }
}

# ── 3. HTTP 助手（会话 cookie 由 WebSession 持有）────────────────────────────
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession

function Publish {
    param([string]$Type, [hashtable]$Payload)
    $payloadJson = if ($null -eq $Payload) { '{}' } else { ($Payload | ConvertTo-Json -Compress -Depth 8) }
    $body = @{ type = $Type; payload = $payloadJson } | ConvertTo-Json -Compress
    return Invoke-RestMethod -Method Post -Uri "$ServerBase/publish" -Body $body -ContentType 'application/json' -WebSession $session
}

# ── 4. 注册 / 登录（幂等：重名 → 登录）──────────────────────────────────────
$reg = Publish 'login-register' @{ username = $Username; password = $Password }
if ($reg.ok -ne $true) {
    $login = Publish 'login-in' @{ username = $Username; password = $Password }
    if ($login.ok -ne $true) {
        throw "登录失败（$Username）：$($login.errors -join '; ')"
    }
    Write-Host "[fixture] 用户已存在 → 登录成功：$Username"
} else {
    Write-Host "[fixture] 注册成功：$Username"
}

# ── 5. 认领实例（instance-claim）：绑定到 proj-a（服务端按 auth.projectRoots 校验）──
# instance_id = 入口绑定值（index.html 内联注入 window.__chonkpilotInstanceId）；与前端 SPA 同口径，
# 使 usr 配置读写落到**该用户的数据根**（61 §4.6：业务数据每用户一套库）。
$html = (Invoke-WebRequest -Uri "$ServerBase/" -UseBasicParsing).Content
if ($html -notmatch '__chonkpilotInstanceId="([^"]+)"') {
    throw "未能从 GET / 解析 window.__chonkpilotInstanceId（静态面未就绪？）"
}
$instanceId = $Matches[1]
$claim = Publish 'instance-claim' @{ instance_id = $instanceId; work_dir = (Join-Path $UserDataRoot 'proj-a') }
if ($claim.ok -ne $true) {
    throw "instance-claim 失败：$($claim.errors -join '; ')"
}

# ── 6. 写 usr 配置自由键 recent_dirs（JSON 数组字符串；幂等）─────────────────
$dirs = $projects | ForEach-Object { Join-Path $UserDataRoot $_ }
$recentJson = ConvertTo-Json -InputObject @($dirs) -Compress -Depth 4
$save = Publish 'data-user-config-save' @{ data = @{ recent_dirs = $recentJson } }
if ($save.ok -ne $true) {
    throw "写入 recent_dirs 失败：$($save.errors -join '; ')"
}
# 回读校验（gui.recent.list）
$list = Publish 'gui.recent.list' @{}
$listed = @()
if ($list.result -and $list.result.dirs) { $listed = @($list.result.dirs) }

# ── 7. 摘要 + 清理说明 ──────────────────────────────────────────────────────
Write-Host ''
Write-Host '=== browser 测试夹具就绪 ===' -ForegroundColor Green
Write-Host "  用户数据根 : $UserDataRoot"
Write-Host "  测试用户   : $Username / $Password"
Write-Host "  workdir    : $($dirs -join ', ')"
Write-Host "  recent_dirs: $recentJson"
Write-Host "  recent.list 回读 : $($listed -join ', ')"
Write-Host ''
Write-Host '  打开浏览器验证：http://127.0.0.1:5668/（登录 → 选目录 proj-a → 工具栏「最近目录」应含 3 项）'
Write-Host ''
Write-Host '  清理：'
Write-Host '    Stop-Process -Name chonkpilot-server -ErrorAction SilentlyContinue'
Write-Host "    Remove-Item -Recurse -Force '$UserDataRoot'"
