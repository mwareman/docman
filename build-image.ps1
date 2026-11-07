<#
.SYNOPSIS
Build the multi-architecture DocMan image (amd64, arm64, arm/v7, 386) as one
image, tag it docman:<version> and docman:latest, and write the archives to dist/.

.PARAMETER Version
Release to build. Defaults to the newest release in NOTES.md.

.PARAMETER NoArchives
Build and load the image only; leave dist/ alone.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [switch]$NoArchives
)

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$platforms = 'linux/amd64,linux/arm64,linux/arm/v7,linux/386'

function Invoke-Native {
    param([scriptblock]$Command)
    # buildx writes progress to stderr; Windows PowerShell 5.1 would turn that
    # into a terminating error, so judge success by the exit code alone.
    $ErrorActionPreference = 'Continue'
    & $Command
    if ($LASTEXITCODE -ne 0) { throw "command failed with exit code ${LASTEXITCODE}: $Command" }
}

if (-not $Version) {
    $row = Select-String -Path 'NOTES.md' -Pattern '^\| (\d+(\.\d+)+) \|' | Select-Object -First 1
    if (-not $row) { throw 'could not read the version from NOTES.md; pass -Version' }
    $Version = $row.Matches[0].Groups[1].Value
}
if ($Version -notmatch '^1\.') {
    throw "refusing to build ${Version}: DocMan stays on 1.x until the owner says otherwise"
}

Write-Host "Building docman:$Version for $platforms"
# --load needs the containerd image store (Docker 25+) to keep every platform in
# one local image.
Invoke-Native { docker buildx build --platform $platforms --build-arg "VERSION=$Version" -t "docman:$Version" -t docman:latest --load . }

if (-not $NoArchives) {
    New-Item -ItemType Directory -Force dist | Out-Null
    $dist = Join-Path $PSScriptRoot 'dist'

    # docker save writes a tar file (never through PowerShell's text pipeline),
    # which is then gzipped with .NET since Windows has no gzip command.
    function Save-Archive {
        param([string]$Path, [string[]]$SaveArgs)
        $tar = [System.IO.Path]::GetTempFileName()
        try {
            Invoke-Native { docker save @SaveArgs -o $tar }
            $in = [System.IO.File]::OpenRead($tar)
            $out = [System.IO.File]::Create($Path)
            try {
                $gz = New-Object System.IO.Compression.GZipStream($out, [System.IO.Compression.CompressionLevel]::Optimal)
                $in.CopyTo($gz)
                $gz.Dispose()
            } finally {
                $in.Dispose(); $out.Dispose()
            }
        } finally {
            Remove-Item $tar -Force -ErrorAction SilentlyContinue
        }
    }

    # Single-architecture archives load on any Docker version.
    $targets = [ordered]@{ amd64 = 'linux/amd64'; arm64 = 'linux/arm64'; armv7 = 'linux/arm/v7'; '386' = 'linux/386' }
    foreach ($name in $targets.Keys) {
        Save-Archive (Join-Path $dist "docman-$Version-$name.tar.gz") @('--platform', $targets[$name], "docman:$Version", 'docman:latest')
    }
    # All four in one archive; the receiving host needs Docker 25+ with containerd.
    Save-Archive (Join-Path $dist "docman-$Version-multiarch.tar.gz") @("docman:$Version", 'docman:latest')

    Get-ChildItem (Join-Path $dist "docman-$Version-*.tar.gz") | Format-Table Name, Length -AutoSize
}

Write-Host "Done: docman:$Version and docman:latest"
