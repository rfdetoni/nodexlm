# NodexLM installer for Windows PowerShell 5.1+ or PowerShell 7+.
# Usage: irm https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.ps1 | iex
# Default channel: edge. For stable versions download and run with -Channel stable.
param(
    [ValidateSet('edge','stable')]
    [string]$Channel = 'edge',
    [string]$InstallDir = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

try {
    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        $InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\NodexLM'
    }
    $repository = 'https://github.com/rfdetoni/nodexlm'
    $releaseUrl = if ($Channel -eq 'stable') { "$repository/releases/latest/download" } else { "$repository/releases/download/nodexlm-edge" }
    $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    switch ($architecture) {
        'x64'   { $arch = 'amd64' }
        'arm64' { $arch = 'arm64' }
        default { throw "Unsupported Windows architecture: $architecture (x64 and ARM64 are supported)" }
    }

    $archiveName = "nodexlm_windows_${arch}.zip"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $temporary = Join-Path $env:TEMP ("nodexlm-installer-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $temporary -Force | Out-Null
    try {
        $archivePath = Join-Path $temporary $archiveName
        $checksumsPath = Join-Path $temporary 'SHA256SUMS.txt'
        Write-Host "NodexLM: Downloading $archiveName ($Channel)"
        # Invoke-WebRequest validates TLS and follows GitHub release redirects.
        Invoke-WebRequest -Uri "$releaseUrl/SHA256SUMS.txt" -OutFile $checksumsPath -UseBasicParsing
        Invoke-WebRequest -Uri "$releaseUrl/$archiveName" -OutFile $archivePath -UseBasicParsing
        $checksumLine = Get-Content -LiteralPath $checksumsPath | Where-Object {
            $_ -match '^([a-fA-F0-9]{64})\s+\*?(\S+)$' -and $Matches[2] -ceq $archiveName
        } | Select-Object -First 1
        if (-not $checksumLine -or -not ($checksumLine -match '^([a-fA-F0-9]{64})\s+')) {
            throw "Checksum missing for $archiveName"
        }
        $expected = $Matches[1].ToLowerInvariant()
        $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -cne $expected) { throw 'SHA-256 mismatch: download rejected' }
        $extracted = Join-Path $temporary 'extracted'
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extracted -Force
        $binary = Join-Path $extracted 'nodexlm.exe'
        if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Release ZIP does not contain nodexlm.exe' }
        & $binary version | Out-Host
        if ($LASTEXITCODE -ne 0) { throw 'Downloaded binary did not start successfully' }
        $destination = Join-Path $InstallDir 'nodexlm.exe'
        Copy-Item -LiteralPath $binary -Destination $destination -Force
        Write-Host "NodexLM installed: $destination"

        $existing = [Environment]::GetEnvironmentVariable('Path', 'User')
        $segments = @($existing -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
        if (-not ($segments | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) {
            $updated = (($segments + $InstallDir) -join ';')
            [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
            Write-Host 'User PATH updated; restart the terminal to use the nodexlm command.'
        }
        if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') })) {
            $env:Path = "$InstallDir;$env:Path"
        }
        Write-Host 'Run: nodexlm host  (or nodexlm help)'
    }
    finally {
        Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
    }
}
catch {
    Write-Error "NodexLM installer failed: $($_.Exception.Message)"
    throw
}
