#requires -Version 7
<#
.SYNOPSIS
    Build, vet, and test wininfopanel.

.DESCRIPTION
    The project targets Windows 11 x64 and builds without cgo, so no C
    toolchain is required.

    Vet runs with -unsafeptr=false. Memory-mapped regions (HWiNFO's sensor
    table, plugin image buffers) require converting an address returned by
    MapViewOfFile into a slice, and the unsafeptr analyzer cannot verify that
    such an address refers to real memory outside the Go heap. Every other vet
    check stays enabled.
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'build', 'vet', 'test', 'clean')]
    [string]$Task = 'all',

    [switch]$Release
)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$env:CGO_ENABLED = '0'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'

$binDir = Join-Path $PSScriptRoot 'bin'

function Invoke-Build {
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null

    $ldflags = @()
    if ($Release) {
        # Strip the symbol table and DWARF data for a smaller shipping binary.
        $ldflags += '-s', '-w'
    }

    foreach ($cmd in @('wininfopanel', 'panelctl')) {
        $src = "./cmd/$cmd"
        if (-not (Test-Path $src)) { continue }

        $out = Join-Path $binDir "$cmd.exe"
        Write-Host "building $cmd -> $out"
        if ($ldflags.Count -gt 0) {
            go build -ldflags ($ldflags -join ' ') -o $out $src
        } else {
            go build -o $out $src
        }
        if ($LASTEXITCODE -ne 0) { throw "build failed for $cmd" }
    }
}

function Invoke-Vet {
    Write-Host 'vetting'
    go vet -unsafeptr=false ./...
    if ($LASTEXITCODE -ne 0) { throw 'vet failed' }
}

function Invoke-Test {
    Write-Host 'testing'
    go test -vet=off ./...
    if ($LASTEXITCODE -ne 0) { throw 'tests failed' }
}

function Invoke-Clean {
    if (Test-Path $binDir) { Remove-Item -Recurse -Force $binDir }
    go clean -cache -testcache
}

switch ($Task) {
    'build' { Invoke-Build }
    'vet'   { Invoke-Vet }
    'test'  { Invoke-Test }
    'clean' { Invoke-Clean }
    'all'   { Invoke-Vet; Invoke-Test; Invoke-Build }
}

Write-Host "$Task complete" -ForegroundColor Green
