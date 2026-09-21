# scripts/install.ps1
# Purpose: Install Helix on Windows, initialize config directories,
# add to PATH, and optionally bootstrap local AI runtimes (Ollama).
# Run this in an elevated PowerShell prompt (Run as Administrator).

$ErrorActionPreference = "Stop"
$BinaryName = "helix.exe"
$InstallDir = "C:\Program Files\Helix"
$TargetBinary = Join-Path $InstallDir $BinaryName
$HelixHome = Join-Path $env:USERPROFILE ".helix"

Write-Host "⚡ Helix Shell Installer (Windows)" -ForegroundColor Cyan
Write-Host "────────────────────────────────────────"

# 1. Build or locate binary
#
# THE NAMES DO NOT MATCH, AND NEVER DID. This script looked for dist\helix.exe,
# but `make windows` writes dist\helix-windows-amd64.exe and `make current`
# writes dist\helix with no extension at all (go build only supplies .exe when
# it picks the output name itself, not when -o gives one). On a fresh checkout
# the build therefore succeeded and the copy that followed it failed.
#
# So look for what the build scripts actually produce, and if none of it is
# there, build directly with go — which is present on any machine that could
# have built Helix, whereas make is not.
$Candidates = @(
    ".\dist\helix.exe",
    ".\dist\helix-windows-amd64.exe",
    ".\dist\helix"
)
$Source = $Candidates | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-Not $Source) {
    Write-Host "Building Helix from source..."
    if (Get-Command go -ErrorAction SilentlyContinue) {
        & go build -o ".\dist\helix.exe" ./cmd/helix
        if ($LASTEXITCODE -ne 0) { throw "go build failed with exit code $LASTEXITCODE" }
    } elseif (Get-Command make -ErrorAction SilentlyContinue) {
        & make windows
        if ($LASTEXITCODE -ne 0) { throw "make windows failed with exit code $LASTEXITCODE" }
    } else {
        throw "Neither go nor make is on PATH; cannot build Helix. Install Go from https://go.dev/dl/."
    }
    $Source = $Candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (-Not $Source) { throw "Build reported success but produced no binary in .\dist" }
}
Write-Host "Using binary: $Source"

# 2. Install binary
Write-Host "Installing binary to $TargetBinary..."
if (-Not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
}
Copy-Item $Source -Destination $TargetBinary -Force

# 3. Create config directories
Write-Host "Initializing Helix home at $HelixHome..."
$Dirs = @("models", "rag_index", "vector_index", "man_index")
foreach ($dir in $Dirs) {
    $path = Join-Path $HelixHome $dir
    if (-Not (Test-Path $path)) {
        New-Item -ItemType Directory -Force -Path $path | Out-Null
    }
}

# FIX: Detect and save the underlying shell preference.
# This ensures Helix always knows to use PowerShell or cmd for child processes,
# even if Helix is set as the default Windows Terminal profile.
$UnderlyingShell = "powershell.exe"
if ($PSVersionTable.PSVersion.Major -ge 6) {
    $UnderlyingShell = "pwsh.exe"
}
$ShellPrefPath = Join-Path $HelixHome "shell_pref"
Set-Content -Path $ShellPrefPath -Value $UnderlyingShell -Force
Write-Host "🧠 Saved underlying shell preference: $UnderlyingShell"

# 4. Add to PATH
$CurrentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($CurrentPath -notlike "*$InstallDir*") {
    Write-Host "Adding $InstallDir to system PATH..."
    [Environment]::SetEnvironmentVariable("Path", "$CurrentPath;$InstallDir", "Machine")
    Write-Host "Added to PATH. You may need to restart your terminal."
}

# 5. Optional Bootstrapping (interactive ONLY, never fatal)
Write-Host ""
Write-Host "🤖 AI Runtime Bootstrapping"
$installOllama = ""
if (-not [Console]::IsInputRedirected) {
    $installOllama = Read-Host "Install Ollama for local AI inference via Winget? (y/N)"
} else {
    Write-Host "   (non-interactive stdin detected — skipping Ollama bootstrap)"
}
if ($installOllama -match '^[Yy]$') {
    if (-Not (Get-Command ollama -ErrorAction SilentlyContinue)) {
        Write-Host "Installing Ollama via Winget..."
        winget install --id Ollama.Ollama -e
        Write-Host "Pulling default model (gemma4:e2b)..."
        & ollama pull gemma4:e2b
    } else {
        Write-Host "Ollama is already installed."
    }
}

Write-Host ""
Write-Host "⚡ Installation complete! Run 'helix' to start." -ForegroundColor Green