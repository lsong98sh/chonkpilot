param(
    [string[]]$Target = @('all')
)

$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$env:GOROOT = "e:\GoDev\go"
$goPathBin = (go env GOPATH 2>$null) + "\bin"
$env:Path = "e:\GoDev\go\bin;" + $goPathBin + ";" + $env:Path
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOTOOLCHAIN = "local"

$startTime = Get-Date

function Build-Executor {
    Write-Host "=== Build executor.exe ===" -ForegroundColor Cyan
    Set-Location $ProjectRoot
    go build -o (Join-Path $ProjectRoot "build\executor.exe") ./cmd/executor/ 2>&1
    if ($LASTEXITCODE -ne 0) { throw "executor build failed" }
    $f = Get-Item (Join-Path $ProjectRoot "build\executor.exe")
    $mb = [Math]::Round($f.Length/1MB, 1)
    Write-Host "  executor.exe: $mb MB" -ForegroundColor Green

    # Copy to internal/embed so the IDE binary embeds the real executor
    $embedDir = Join-Path $ProjectRoot "internal\embed"
    $null = New-Item -ItemType Directory -Path $embedDir -Force
    Copy-Item (Join-Path $ProjectRoot "build\executor.exe") (Join-Path $embedDir "executor.exe") -Force
    Write-Host "  -> internal/embed/executor.exe ($mb MB)" -ForegroundColor Green
}

function Build-IDE {
    Write-Host "=== Build IDE (chonkpilot.exe) ===" -ForegroundColor Cyan
    Set-Location $ProjectRoot

    Write-Host "--- Frontend deps ---" -ForegroundColor Cyan
    Set-Location (Join-Path $ProjectRoot "frontend")
    npm install 2>&1 | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }

    $wailsCmd = Get-Command wails -ErrorAction SilentlyContinue
    if (-not $wailsCmd) {
        Write-Host "Install wails CLI..." -ForegroundColor Yellow
        Set-Location $ProjectRoot
        go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
        if ($LASTEXITCODE -ne 0) { throw "wails install failed" }
    }

    Set-Location $ProjectRoot
    wails build -clean -tags devtools -ldflags "-H windowsgui" -o chonkpilot.exe 2>&1
    if ($LASTEXITCODE -ne 0) { throw "wails build failed" }
    Move-IDEOutput
}

function Move-IDEOutput {
    $wailsOut = Join-Path $ProjectRoot "build\bin\chonkpilot.exe"
    if (Test-Path $wailsOut) {
        $dest = Join-Path $ProjectRoot "build\chonkpilot.exe"
        Remove-Item $dest -Force -ErrorAction SilentlyContinue
        Move-Item $wailsOut $dest
        Remove-Item (Join-Path $ProjectRoot "build\bin") -Recurse -ErrorAction SilentlyContinue
    }
    $f = Get-Item (Join-Path $ProjectRoot "build\chonkpilot.exe")
    $mb = [Math]::Round($f.Length/1MB, 1)
    Write-Host "  chonkpilot.exe: $mb MB" -ForegroundColor Green
}

function Build-Web {
    Write-Host "=== Build frontend ===" -ForegroundColor Cyan
    Set-Location (Join-Path $ProjectRoot "frontend")
    npm install 2>&1 | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
    npm run build 2>&1 | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    Write-Host "  Frontend build done" -ForegroundColor Green
}

# --- Resolve & Execute ---
$all = $Target -eq 'all'
$valid = @('ide', 'executor', 'web', 'all')
foreach ($t in $Target) {
    if ($t -notin $valid) {
        Write-Host "Unknown: '$t'. Valid: ide, executor, web (all = all three)" -ForegroundColor Red
        exit 1
    }
}

try {
    Set-Location $ProjectRoot
    $null = New-Item -ItemType Directory -Path (Join-Path $ProjectRoot "build") -Force

    # Resolve build order: executor first (for IDE embedding), then ide
    $ordered = @()
    if ($all) {
        $ordered = @('executor', 'ide')
    } else {
        if ('executor' -in $Target) { $ordered += 'executor' }
        if ('web' -in $Target) { $ordered += 'web' }
        if ('ide' -in $Target) { $ordered += 'ide' }
    }

    Write-Host "=== Build order: $($ordered -join ' → ') ===" -ForegroundColor Cyan
    foreach ($t in $ordered) {
        switch ($t) {
            'web'      { Build-Web }
            'ide'      { Build-IDE }
            'executor' { Build-Executor }
        }
    }

    $elapsed = [Math]::Round(((Get-Date) - $startTime).TotalSeconds, 1)
    Write-Host "=== Build done ($elapsed s) ===" -ForegroundColor Green
}
catch {
    Write-Host "!!! Build failed: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
