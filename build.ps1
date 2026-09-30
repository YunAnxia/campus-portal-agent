<#
.SYNOPSIS
    Build portalagent: gofmt check + go vet + compile.
.EXAMPLE
    .\build.ps1
    .\build.ps1 -Out C:\ProgramData\CampusPortalAgent\portalagent.exe
.NOTES
    If PowerShell blocks this script due to execution policy, either use
    build.cmd instead, or run:
        powershell -ExecutionPolicy Bypass -File .\build.ps1
#>
[CmdletBinding()]
param(
    [string]$Out = 'portalagent.exe'
)

$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    Write-Host '--- gofmt ---' -ForegroundColor Cyan
    $unformatted = gofmt -l .
    if ($unformatted) {
        Write-Warning "not formatted, run 'gofmt -w':`n$($unformatted -join "`n")"
    } else {
        Write-Host 'all formatted'
    }

    Write-Host '--- go vet ---' -ForegroundColor Cyan
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }

    Write-Host '--- go build ---' -ForegroundColor Cyan
    $env:CGO_ENABLED = '0'
    go build -trimpath -ldflags '-s -w' -o $Out .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

    $fi = Get-Item $Out
    Write-Host ("built: {0} ({1:N1} KB)" -f $fi.FullName, ($fi.Length / 1KB)) -ForegroundColor Green
}
finally {
    Pop-Location
}
