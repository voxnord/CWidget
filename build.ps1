<#
.SYNOPSIS
  Automated build script for CWidget (Windows).
.DESCRIPTION
  Single entry point: VERSION -> sync icons -> exe version info (winres)
  -> wails build with the version injected via ldflags.
  The version lands in three places: exe properties, the binary itself,
  and the "About" tab in the UI.
.EXAMPLE
  .\build.ps1                    # regular build
.EXAMPLE
  .\build.ps1 -Installer         # + NSIS installer
.EXAMPLE
  .\build.ps1 -Installer -Archive  # + zip with exe and installer in build\dist
.EXAMPLE
  .\build.ps1 -Version 2.0.0     # one-off build with a different version
#>
param(
    [string]$Version = "",
    [switch]$Installer,
    [switch]$Archive,
    [switch]$SkipIcons
)

 $ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# --- Product constants (visible in exe Properties -> Details) ---
 $ProductName     = "CWidget"
 $FileDescription = "CWidget - currency converter tray widget"
 $CompanyName     = ""
 $LegalCopyright  = ""

function Fail([string]$msg) {
    Write-Host "ERROR: $msg" -ForegroundColor Red
    exit 1
}

# --- 1. Environment ---
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Fail "go not found in PATH" }
if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
    Fail "wails CLI not found. Install it, then reopen the terminal:`n  go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0"
}

# --- 2. Version: from -Version or the VERSION file ---
if (-not $Version) {
    if (-not (Test-Path "VERSION")) { Fail "VERSION file not found in project root" }
    $Version = (Get-Content "VERSION" -TotalCount 1).Trim()
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') { Fail "version '$Version' is not in 1.2.3 format" }

Write-Host ""
Write-Host "== Building CWidget v$Version ==" -ForegroundColor Cyan

# --- 3. Icons: assets/ -> build/ ---
if (-not $SkipIcons) {
    Write-Host "[1/4] Syncing icons (assets/ -> build/)..." -ForegroundColor Cyan
    & go run ./cmd/icons sync
    if ($LASTEXITCODE -ne 0) { Fail "icon sync failed" }
} else {
    if (-not ((Test-Path "assets/icon.png") -and (Test-Path "assets/icon.ico"))) {
        Fail "-SkipIcons given, but assets/ has no icons (the project will not compile without them)"
    }
}

# --- 4. exe version info (winres): exe Properties -> Details ---
Write-Host "[2/4] Updating build/windows/info.json..." -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path "build/windows" | Out-Null
 $info = [ordered]@{
    fixed = [ordered]@{ file_version = $Version }
    info  = [ordered]@{
        "0409" = [ordered]@{
            ProductVersion  = $Version
            ProductName     = $ProductName
            FileDescription = $FileDescription
            CompanyName     = $CompanyName
            LegalCopyright  = $LegalCopyright
            Comments        = ""
        }
    }
}
 $json = $info | ConvertTo-Json -Depth 10
# UTF-8 WITHOUT BOM: a BOM breaks the winres generator's JSON parser
[System.IO.File]::WriteAllText((Join-Path (Get-Location) "build/windows/info.json"), $json)

# --- 5. Build ---
Write-Host "[3/4] wails build..." -ForegroundColor Cyan
 $module  = (go list -m).Trim()
 $ldflags = "-s -w -X $module/app.Version=$Version"
 $wailsArgs = @("build", "-platform", "windows/amd64", "-trimpath", "-ldflags", $ldflags, "-clean")
if ($Installer) { $wailsArgs += "-nsis" }
& wails @wailsArgs
if ($LASTEXITCODE -ne 0) { Fail "wails build failed" }
if (-not (Test-Path "build/bin/CWidget.exe")) { Fail "build/bin/CWidget.exe was not produced" }

# --- 6. Packaging ---
if ($Archive) {
    Write-Host "[4/4] Packaging..." -ForegroundColor Cyan
    $dist = "build/dist"
    New-Item -ItemType Directory -Force -Path $dist | Out-Null
    $zip = Join-Path $dist "CWidget_${Version}_windows_amd64.zip"
    Compress-Archive -Force -Path "build/bin/CWidget.exe" -DestinationPath $zip
    Write-Host "      $zip" -ForegroundColor Green

    # The NSIS installer lives in build/bin; copy it next to the zip so the
    # CI artifact glob (build/dist/*) picks both up.
    if ($Installer -and (Test-Path "build/bin/CWidget-amd64-installer.exe")) {
        $inst = Join-Path $dist "CWidget_${Version}_windows_amd64-installer.exe"
        Copy-Item "build/bin/CWidget-amd64-installer.exe" $inst -Force
        Write-Host "      $inst" -ForegroundColor Green
    }
}

Write-Host ""
Write-Host "Done: build\bin\CWidget.exe (v$Version)" -ForegroundColor Green