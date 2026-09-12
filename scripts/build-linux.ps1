param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',

    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'

$projectRoot = Split-Path -Parent $PSScriptRoot
$outputDir = Join-Path $projectRoot 'bin'
$outputFile = Join-Path $outputDir 'matchmind-api'

$previousGoos = $env:GOOS
$previousGoarch = $env:GOARCH
$previousCgo = $env:CGO_ENABLED

try {
    Set-Location $projectRoot

    if (-not $SkipTests) {
        Write-Host 'Running Go tests for the local platform...'
        go test -buildvcs=false ./...
        if ($LASTEXITCODE -ne 0) {
            throw "Go tests failed with exit code $LASTEXITCODE"
        }
    }

    $env:GOOS = 'linux'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'

    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null

    Write-Host "Building Linux/$Arch binary..."
    go build `
        -buildvcs=false `
        -trimpath `
        -ldflags='-s -w' `
        -o $outputFile `
        ./cmd/server

    if ($LASTEXITCODE -ne 0) {
        throw "Go build failed with exit code $LASTEXITCODE"
    }

    $file = Get-Item $outputFile
    Write-Host ''
    Write-Host 'Build completed:' -ForegroundColor Green
    Write-Host "  Target: linux/$Arch"
    Write-Host "  File:   $($file.FullName)"
    Write-Host "  Size:   $([Math]::Round($file.Length / 1MB, 2)) MB"
    Write-Host ''
    Write-Host 'Upload example:'
    Write-Host "  scp `"$($file.FullName)`" root@SERVER_IP:/opt/match-mind/matchmind-api"
} finally {
    if ($null -eq $previousGoos) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $previousGoos }
    if ($null -eq $previousGoarch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $previousGoarch }
    if ($null -eq $previousCgo) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $previousCgo }
}
