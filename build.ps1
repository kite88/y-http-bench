<#
.SYNOPSIS
    Cross-compile y-http-bench for every mainstream OS/arch into dist/.

.DESCRIPTION
    The project only uses the Go standard library, so CGO is not needed: every
    artifact is built with CGO_ENABLED=0 as a static executable.
    Runs on Windows PowerShell 5.1 and PowerShell 7 (pwsh); Linux / macOS users
    should use the shell counterpart build.sh instead - both scripts produce
    byte-identical archives.

    Output layout in dist/:
        y-http-bench-windows-<arch>.zip      contains y-http-bench.exe
        y-http-bench-<os>-<arch>.tar.gz      contains y-http-bench (mode 0755)
        y-http-bench(.exe)                   host platform only, uncompressed,
                                             ready to run without unzipping
        checksums.txt                        SHA256 of every artifact above

    zip for Windows and tar.gz everywhere else is the usual convention: a zip
    does not carry the Unix executable bit.

    NOTE: keep this file ASCII-only. Windows PowerShell 5.1 decodes scripts
    without a BOM using the system code page, which mangles UTF-8 comments.

.PARAMETER OutDir
    Output directory, default "dist" (relative to this script). If it already
    exists, only its files are removed - never directories.

.EXAMPLE
    .\build.ps1

.EXAMPLE
    .\build.ps1 -OutDir D:\tmp\bench-dist
#>
[CmdletBinding()]
param(
    [string]$OutDir = 'dist'
)

$ErrorActionPreference = 'Stop'

# Target matrix: 'os/arch' or 'os/arch/goarm' (GOARM only matters for arm).
# Add or remove one line to change the matrix.
$targets = @(
    'windows/amd64', 'windows/arm64', 'windows/386',
    'linux/amd64', 'linux/arm64', 'linux/386', 'linux/arm/7',
    'linux/ppc64le', 'linux/s390x', 'linux/riscv64', 'linux/loong64',
    'darwin/amd64', 'darwin/arm64',
    'freebsd/amd64', 'freebsd/arm64'
)

Push-Location $PSScriptRoot
$staging = Join-Path ([System.IO.Path]::GetTempPath()) ('y-http-bench-' + [Guid]::NewGuid().ToString('N').Substring(0, 8))
try {
    if (-not [System.IO.Path]::IsPathRooted($OutDir)) {
        $OutDir = Join-Path $PSScriptRoot $OutDir
    }

    # Host platform: its binary is left uncompressed so it can be run directly.
    $hostOS = (& go env GOOS).Trim()
    $hostArch = (& go env GOARCH).Trim()

    if (-not (Test-Path $OutDir)) {
        New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
    }
    else {
        # Remove files only (no recursion) so nothing outside dist/ can be hit.
        Get-ChildItem -Path $OutDir -File -Force | Remove-Item -Force
    }
    New-Item -ItemType Directory -Path $staging -Force | Out-Null

    $env:CGO_ENABLED = '0'

    # Archives are written by tools/pack instead of the bundled tar.exe or
    # Compress-Archive: Windows' bsdtar cannot write the Unix mode (it fakes
    # 0666 and has no --mode), and having a single packer keeps zip and tar.gz
    # byte-identical between build.ps1 and build.sh.
    # Built here, before GOOS/GOARCH are switched, so it runs on the host.
    $packer = Join-Path $staging 'pack'
    if ($hostOS -eq 'windows') { $packer += '.exe' }
    & go build -o $packer ./tools/pack
    if ($LASTEXITCODE -ne 0) { throw 'build failed: ./tools/pack' }

    $artifacts = @()

    foreach ($target in $targets) {
        $parts = $target.Split('/')
        $goos = $parts[0]
        $goarch = $parts[1]
        $goarm = $null
        if ($parts.Count -gt 2) { $goarm = $parts[2] }

        $env:GOOS = $goos
        $env:GOARCH = $goarch
        if ($goarm) { $env:GOARM = $goarm } else { Remove-Item Env:GOARM -ErrorAction SilentlyContinue }

        # Short name inside the archive: no os/arch/version.
        $binName = 'y-http-bench'
        if ($goos -eq 'windows') { $binName += '.exe' }
        $binPath = Join-Path $staging $binName

        $archLabel = $goarch
        if ($goarm) { $archLabel += "v$goarm" }

        & go build -trimpath -ldflags '-s -w' -o $binPath .
        if ($LASTEXITCODE -ne 0) { throw "build failed: $target" }

        if ($goos -eq 'windows') {
            $archiveName = "y-http-bench-${goos}-${archLabel}.zip"
        }
        else {
            $archiveName = "y-http-bench-${goos}-${archLabel}.tar.gz"
        }
        & $packer -in $binPath -out (Join-Path $OutDir $archiveName) -name $binName -mode 0755
        if ($LASTEXITCODE -ne 0) { throw "pack failed: $target" }

        $isHost = ($goos -eq $hostOS) -and ($goarch -eq $hostArch)
        $note = ''
        if ($isHost) {
            Copy-Item -Path $binPath -Destination (Join-Path $OutDir $binName) -Force
            $note = '  + ' + $binName
        }
        Write-Host ("  build {0,-20} -> {1}{2}" -f $target, $archiveName, $note)

        $artifacts += [pscustomobject]@{
            Target   = $target
            Archive  = $archiveName
            Contains = $binName
            SizeMB   = [math]::Round((Get-Item (Join-Path $OutDir $archiveName)).Length / 1MB, 2)
        }
    }

    # SHA256 checksums, LF line endings so `sha256sum -c` works on Linux.
    $sums = foreach ($file in (Get-ChildItem -Path $OutDir -File | Where-Object { $_.Name -ne 'checksums.txt' } | Sort-Object Name)) {
        '{0}  {1}' -f (Get-FileHash -Path $file.FullName -Algorithm SHA256).Hash.ToLower(), $file.Name
    }
    [System.IO.File]::WriteAllText((Join-Path $OutDir 'checksums.txt'), ($sums -join "`n") + "`n")

    Write-Host ''
    $artifacts | Format-Table -AutoSize
    Write-Host ("{0} archives + checksums.txt -> {1}" -f $artifacts.Count, $OutDir)
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:GOARM, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    if (Test-Path $staging) { Remove-Item $staging -Recurse -Force }
    Pop-Location
}
