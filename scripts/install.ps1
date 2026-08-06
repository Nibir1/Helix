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
if (-Not (Test-Path ".\dist\$BinaryName")) {
    Write-Host "Building Helix from source..."
    make windows
}

# 2. Install binary
Write-Host "Installing binary to $TargetBinary..."
if (-Not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
}
Copy-Item ".\dist\$BinaryName" -Destination $TargetBinary -Force

# 3. Create config directories
Write-Host "Initializing Helix home at $HelixHome..."
$Dirs = @("models", "rag_index", "vector_index", "man_index")
foreach ($dir in $Dirs) {
    $path = Join-Path $HelixHome $dir
    if (-Not (Test-Path $path)) {
        New-Item -ItemType Directory -Force -Path $path | Out-Null
    }
}

# 4. Add to PATH
$CurrentPath = [Environment]::GetEnvironmentVariable("Path", "Machine")
if ($CurrentPath -notlike "*$InstallDir*") {
    Write-Host "Adding $InstallDir to system PATH..."
    [Environment]::SetEnvironmentVariable("Path", "$CurrentPath;$InstallDir", "Machine")
    Write-Host "Added to PATH. You may need to restart your terminal."
}

# 5. Optional Bootstrapping
Write-Host ""
Write-Host "AI Runtime Bootstrapping"
$installOllama = Read-Host "Install Ollama for local AI inference via Winget? (y/N)"
if ($installOllama -match '^[Yy]$') {
    if (-Not (Get-Command ollama -ErrorAction SilentlyContinue)) {
        Write-Host "Installing Ollama via Winget..."
        winget install --id Ollama.Ollama -e
        Write-Host "Pulling default model (phi4-mini)..."
        & ollama pull phi4-mini
    } else {
        Write-Host "Ollama is already installed."
    }
}

Write-Host ""
Write-Host "⚡ Installation complete! Run 'helix' to start." -ForegroundColor Green