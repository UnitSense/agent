# UnitSense Agent — Windows installer
# Usage: irm https://app.unitsense.ai/install-agent.ps1 | iex
# Or:    .\install-agent.ps1

$ErrorActionPreference = "Stop"

$repo      = "UnitSense/agent"
$binary    = "unitsense-agent.exe"
$installDir = Join-Path $env:LOCALAPPDATA "Programs\unitsense-agent"

Write-Host "Installing UnitSense Agent..."

# Get latest release tag
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$tag = $release.tag_name
Write-Host "Latest version: $tag"

# Build download URL — windows_amd64 zip
$zipName = "unitsense-agent_${tag}_windows_amd64.zip"
$downloadUrl = "https://github.com/$repo/releases/download/$tag/$zipName"

# Download to temp
$tmp = Join-Path $env:TEMP $zipName
Write-Host "Downloading $downloadUrl..."
Invoke-WebRequest -Uri $downloadUrl -OutFile $tmp -UseBasicParsing

# Extract
$extractDir = Join-Path $env:TEMP "unitsense-agent-extract"
if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force }
Expand-Archive -Path $tmp -DestinationPath $extractDir -Force

# Install
if (-not (Test-Path $installDir)) { New-Item -ItemType Directory -Path $installDir -Force | Out-Null }
$src = Join-Path $extractDir $binary
if (-not (Test-Path $src)) {
    # Binary may be in a subdirectory inside the zip
    $src = Get-ChildItem -Path $extractDir -Filter $binary -Recurse | Select-Object -First 1 -ExpandProperty FullName
}
Copy-Item $src (Join-Path $installDir $binary) -Force

# Add to PATH for current user if not already present
$currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($currentPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$currentPath;$installDir", "User")
    $env:PATH += ";$installDir"
    Write-Host "Added $installDir to PATH"
}

# Cleanup
Remove-Item $tmp -Force
Remove-Item $extractDir -Recurse -Force

Write-Host ""
Write-Host "UnitSense Agent $tag installed to $installDir"
Write-Host ""
Write-Host "Next steps:"
Write-Host "  1. Open a new terminal (to pick up PATH change)"
Write-Host "  2. unitsense-agent.exe setup"
Write-Host "  3. unitsense-agent.exe install --schedule=10m"
