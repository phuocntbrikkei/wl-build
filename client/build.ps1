# PowerShell script to build Wails client
$ErrorActionPreference = "Stop"

Write-Host "=============================================" -ForegroundColor Cyan
Write-Host "              BUILDING WAILS CLIENT           " -ForegroundColor Cyan
Write-Host "=============================================" -ForegroundColor Cyan

# Tên file output theo wails.json (mặc định "client" nếu không đọc được)
$outputName = "client"
try {
    $wailsConfig = Get-Content "wails.json" -Raw | ConvertFrom-Json
    if ($wailsConfig.outputfilename) {
        $outputName = $wailsConfig.outputfilename
    }
} catch {
    Write-Host "[!] Could not read wails.json, defaulting output name to 'client'." -ForegroundColor Yellow
}
$outputExePath = "build\bin\$outputName.exe"

# Check if wails is installed
$wailsInstalled = $null -ne (Get-Command wails -ErrorAction SilentlyContinue)

if ($wailsInstalled) {
    Write-Host "[*] Found Wails CLI. Building..." -ForegroundColor Green
    wails build -clean -ldflags "-s -w"
    # wails build không luôn ném exception khi lỗi (native exe) — phải tự kiểm tra exit code + file output.
    if ($LASTEXITCODE -eq 0 -and (Test-Path $outputExePath)) {
        Write-Host "[+] Build completed successfully using Wails CLI!" -ForegroundColor Green
        Write-Host "[+] Output: $((Resolve-Path $outputExePath).Path)" -ForegroundColor Green
        exit 0
    } else {
        Write-Host "[-] Wails build failed (exit code $LASTEXITCODE) or output not found at $outputExePath. Attempting manual fallback build..." -ForegroundColor Yellow
    }
} else {
    Write-Host "[!] Wails CLI not found. Falling back to manual build..." -ForegroundColor Yellow
}

# Check for Go
if ($null -eq (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "[!] Go is not installed or not in PATH." -ForegroundColor Red
    exit 1
}

# Check for npm
if ($null -eq (Get-Command npm -ErrorAction SilentlyContinue)) {
    Write-Host "[!] Node.js/npm is not installed or not in PATH." -ForegroundColor Red
    exit 1
}

# Step 1: Build Frontend
Write-Host "[*] Building frontend assets..." -ForegroundColor Cyan
Push-Location frontend

try {
    if (-not (Test-Path "node_modules")) {
        Write-Host "[*] node_modules not found. Running 'npm install'..." -ForegroundColor Cyan
        npm install
        if ($LASTEXITCODE -ne 0) {
            throw "npm install failed (exit code $LASTEXITCODE)"
        }
    }

    Write-Host "[*] Running 'npm run build'..." -ForegroundColor Cyan
    npm run build
    if ($LASTEXITCODE -ne 0) {
        throw "npm run build failed (exit code $LASTEXITCODE)"
    }
} finally {
    Pop-Location
}

# Step 2: Build Backend Go binary
Write-Host "[*] Building Go application..." -ForegroundColor Cyan

$fallbackExe = "$outputName.exe"
go build -ldflags="-s -w -H windowsgui" -o $fallbackExe .
if ($LASTEXITCODE -ne 0 -or -not (Test-Path $fallbackExe)) {
    Write-Host "[-] Go build failed (exit code $LASTEXITCODE)." -ForegroundColor Red
    exit 1
}
Write-Host "[+] Build completed successfully! Generated $((Resolve-Path $fallbackExe).Path)" -ForegroundColor Green
