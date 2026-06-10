# UnitSense Agent — Windows installer
# Usage: irm https://app.unitsense.ai/install-agent.ps1 | iex
# Or:    .\install-agent.ps1

$ErrorActionPreference = "Stop"

$repo      = "UnitSense/agent"
$binary    = "unitsense-agent.exe"
$installDir = Join-Path $env:LOCALAPPDATA "Programs\unitsense-agent"

Write-Host "Installing UnitSense Agent..."

# Get latest release tag
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" -TimeoutSec 30
$tag = $release.tag_name
if ($tag -notmatch '^v[\d.]+$') {
    throw "Unexpected release tag format: '$tag' — aborting"
}
Write-Host "Latest version: $tag"

# Build download URLs
$zipName     = "unitsense-agent_${tag}_windows_amd64.zip"
$downloadUrl = "https://github.com/$repo/releases/download/$tag/$zipName"
$checksumUrl = "https://github.com/$repo/releases/download/$tag/checksums.txt"

# Unique temp paths to avoid races
$tmp        = Join-Path $env:TEMP $zipName
$extractDir = Join-Path $env:TEMP ([System.IO.Path]::GetRandomFileName())

try {
    Write-Host "Downloading $downloadUrl..."
    Invoke-WebRequest -Uri $downloadUrl -OutFile $tmp -UseBasicParsing -TimeoutSec 120

    # Verify SHA256 against goreleaser-published checksums.txt
    Write-Host "Verifying checksum..."
    $checksumTmp = Join-Path $env:TEMP "unitsense-checksums-$tag.txt"
    Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumTmp -UseBasicParsing -TimeoutSec 30
    $checksumLine = Get-Content $checksumTmp | Where-Object { $_ -match ([regex]::Escape($zipName)) }
    Remove-Item $checksumTmp -Force -ErrorAction SilentlyContinue
    if (-not $checksumLine) {
        throw "Checksum entry for '$zipName' not found in checksums.txt"
    }
    $expected = ($checksumLine -split '\s+')[0].ToLower()
    $actual   = (Get-FileHash -Path $tmp -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $expected) {
        throw "Checksum mismatch for '$zipName':`n  expected: $expected`n  actual:   $actual"
    }
    Write-Host "Checksum OK"

    # Extract
    Expand-Archive -Path $tmp -DestinationPath $extractDir -Force

    # Install
    if (-not (Test-Path $installDir)) { New-Item -ItemType Directory -Path $installDir -Force | Out-Null }
    $src = Join-Path $extractDir $binary
    if (-not (Test-Path $src)) {
        # Binary may be in a subdirectory inside the zip
        $src = Get-ChildItem -Path $extractDir -Filter $binary -Recurse |
               Select-Object -First 1 -ExpandProperty FullName
    }
    if (-not $src) {
        throw "Binary '$binary' not found in archive '$zipName'"
    }
    Copy-Item $src (Join-Path $installDir $binary) -Force

    # Add to PATH for current user if not already present
    $currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if (-not $currentPath.Contains($installDir)) {
        [Environment]::SetEnvironmentVariable("PATH", "$currentPath;$installDir", "User")
        $env:PATH += ";$installDir"
        Write-Host "Added $installDir to PATH"
    }

    Write-Host ""
    Write-Host "UnitSense Agent $tag installed to $installDir"
    Write-Host ""
    Write-Host "Next steps:"
    Write-Host "  1. Open a new terminal (to pick up PATH change)"
    Write-Host "  2. unitsense-agent.exe setup"
    Write-Host "  3. unitsense-agent.exe install --schedule=10m"
} finally {
    if (Test-Path $tmp)        { Remove-Item $tmp -Force -ErrorAction SilentlyContinue }
    if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force -ErrorAction SilentlyContinue }
}
