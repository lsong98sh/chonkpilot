﻿# sync-contracts.ps1：契约「单一数据源 → capability」覆盖式同步（不重建、不动 exe）
#
# 背景：契约 md 有「权威源 → capability 部署副本」两处存在形态，同源但可静默分叉：
#   权威源（出厂数据唯一源 = src/initdata/capability）：
#        tools/                     <- src/initdata/capability/tools
#        {skills|prompts|resources} <- src/initdata/capability/<prim>（2026-10-01 P2 扁平化：旧 knowledge/<prim> 已删）
#   部署副本（mcp-server 运行时扫描的 capability/）由 build-mcp-server.ps1 覆盖式拷贝而来。
#   executor **不再内嵌契约**（`--help` 亦从磁盘 capability/tools/<cat>/ 读）→ 无 embed 副本可漂移。
# 直接手改部署目录 capability/ 会让副本与源分叉（曾现于 file_diff.tool.md：改完静默分叉、无人察觉）。
# 本脚本把源**覆盖式**同步回 capability/，先打印将被改动的文件清单（含漂移项），再逐文件核对哈希。
#
# 部署副本（D-28 产物分区，见 [41 D-28]）：
#   other   = dist/other/capability    （mcp-server / mcp-gateway 共用，权威副本）
#   desktop = dist/desktop/capability  （桌面单体发行根）
#   server  = dist/server/capability   （gui/browser 共用服务端发行根）
#
# 用法：
#   .\sync-contracts.ps1                        # 默认：dist/other + dist/desktop 都同步
#   .\sync-contracts.ps1 -Dist desktop          # 只同步 dist/desktop（也接受 other / server）
#   .\sync-contracts.ps1 -Check                 # 只报漂移，不写盘（有漂移时退出码 1）
#
# 幂等：重复执行无副作用；只写 4 类契约 md（*.tool.md/*.skill.md/*.prompt.md/*.resource.md），
# 不触碰 capability/ 下的 executor exe。
param(
    [ValidateSet("other", "desktop", "server", "dist-other", "dist-desktop", "dist-server")]
    [string[]]$Dist = @("other", "desktop"),
    [switch]$Check
)
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent $MyInvocation.MyCommand.Path

# 发行目标 -> capability 目录
$allTargets = [ordered]@{
    "other" = Join-Path $root "dist\other\capability"
    "desktop" = Join-Path $root "dist\desktop\capability"
    "server" = Join-Path $root "dist\server\capability"
}
$targetNames = @()
foreach ($d in $Dist) {
    $n = $d -replace "^dist-", ""
    if ($targetNames -notcontains $n) { $targetNames += $n }
}

$contractFilter = @("*.tool.md", "*.skill.md", "*.prompt.md", "*.resource.md")

# -- 1) 权威源清单（相对 capability 根的路径 -> 源文件全路径） --
$map = [ordered]@{}
$initCap = Join-Path $root "src\initdata\capability"
$toolsSrc = Join-Path $initCap "tools"
if (-not (Test-Path $toolsSrc)) { throw "tools contracts not found: $toolsSrc" }
Get-ChildItem $toolsSrc -Recurse -File -Include "*.tool.md" | ForEach-Object {
    $map["tools\$($_.FullName.Substring($toolsSrc.Length + 1))"] = $_.FullName
}
foreach ($prim in @("skills", "prompts", "resources")) {
    $src = Join-Path $initCap $prim
    if (-not (Test-Path $src)) { throw "$prim contracts not found: $src" }
    Get-ChildItem $src -Recurse -File -Include "*.skill.md", "*.prompt.md", "*.resource.md" | ForEach-Object {
        $map["$prim\$($_.FullName.Substring($src.Length + 1))"] = $_.FullName
    }
}
$srcHash = @{}
foreach ($k in $map.Keys) { $srcHash[$k] = (Get-FileHash $map[$k] -Algorithm SHA256).Hash }
$toolKeys = @($map.Keys | Where-Object { $_ -like "tools\*" })
Write-Host "==> source: $($map.Count) contracts (tools=$($toolKeys.Count), 其余 skills/prompts/resources)"
if ($Check) { Write-Host "==> mode: -Check（只报漂移，不写盘）" }

# -- 2) 逐目标：先报将改动的文件，再覆盖，再核对 --
$checkDrift = 0
foreach ($n in $targetNames) {
    $cap = $allTargets[$n]
    Write-Host ""
    Write-Host "==> target [$n] $cap"
    if (-not (Test-Path $cap)) {
        Write-Host "    skip: capability 不存在（首次请先跑 .\build-mcp-server.ps1）"
        continue
    }

    $pending = @()
    foreach ($k in $map.Keys) {
        $t = Join-Path $cap $k
        if (-not (Test-Path $t)) {
            $pending += [pscustomobject]@{ kind = "missing"; rel = $k }
        } elseif ((Get-FileHash $t -Algorithm SHA256).Hash -ne $srcHash[$k]) {
            $pending += [pscustomobject]@{ kind = "drift"; rel = $k }
        }
    }
    $orphan = @()
    Get-ChildItem $cap -Recurse -File -Include $contractFilter | ForEach-Object {
        $rel = $_.FullName.Substring($cap.Length + 1)
        if (-not $map.Contains($rel)) { $orphan += $rel }
    }

    if ($pending.Count -eq 0 -and $orphan.Count -eq 0) {
        Write-Host "    [plan] 无待同步项（capability 已与源一致）"
    } else {
        if ($pending.Count -gt 0) {
            Write-Host "    [plan] 将被覆盖 $($pending.Count) 个契约（手改/旧版将回退到源）:"
            foreach ($p in $pending) { Write-Host ("           {0,-8} {1}" -f $p.kind, $p.rel) }
        }
        if ($orphan.Count -gt 0) {
            Write-Host "    [plan] 将删除 $($orphan.Count) 个源中已不存在的孤儿契约:"
            foreach ($o in $orphan) { Write-Host ("           {0,-8} {1}" -f "orphan", $o) }
        }
    }

    if (-not $Check -and ($pending.Count -gt 0 -or $orphan.Count -gt 0)) {
        foreach ($p in $pending) {
            $t = Join-Path $cap $p.rel
            $dir = Split-Path -Parent $t
            if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
            Copy-Item $map[$p.rel] $t -Force
        }
        foreach ($o in $orphan) {
            $orphanPath = Join-Path $cap $o
            if (Test-Path $orphanPath) { [System.IO.File]::Delete($orphanPath) }
        }
        Write-Host "    [sync] 已覆盖 $($pending.Count) / 已删除 $($orphan.Count)"
    }

    # 同步后逐文件核对哈希（幂等性的判据）
    $bad = 0
    foreach ($k in $map.Keys) {
        $t = Join-Path $cap $k
        if (-not (Test-Path $t)) { $bad++; continue }
        if ((Get-FileHash $t -Algorithm SHA256).Hash -ne $srcHash[$k]) { $bad++ }
    }
    if ($bad -eq 0) {
        Write-Host "    [verify] identical（$($map.Count)/$($map.Count) 文件与源一致）"
    } else {
        Write-Host "    [verify] drifted-$bad（$($map.Count - $bad)/$($map.Count) 文件与源一致）"
        $checkDrift += $bad
    }
}

# -- 3) executor 侧：**不再内嵌契约**（`--help` 亦从磁盘 capability/tools/<cat>/ 读）→ 无 embed 副本 --
Write-Host ""
Write-Host "==> executor 侧（不再 embed：`--help` 从磁盘 capability/tools/<cat>/ 读同一份契约）"
Write-Host "    无内嵌副本可漂移；改了 src/initdata 契约 → 跑 .\build-mcp-server.ps1 重铺 capability 即可。"

Write-Host ""
if ($Check -and $checkDrift -gt 0) {
    Write-Host "==> done: 检测到 $checkDrift 项漂移（未写盘；去掉 -Check 即覆盖回源）"
    exit 1
}
Write-Host "==> done"
