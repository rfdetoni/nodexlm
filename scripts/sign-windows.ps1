param(
    [Parameter(Mandatory=$true)][string]$Directory,
    [Parameter(Mandatory=$true)][string]$CertificateThumbprint,
    [string]$TimestampUrl = 'http://timestamp.digicert.com'
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$Directory = (Resolve-Path $Directory).Path
$certificate = Get-Item "Cert:\CurrentUser\My\$CertificateThumbprint"
if (-not $certificate.HasPrivateKey -or $certificate.NotAfter -le (Get-Date) -or $certificate.NotBefore -gt (Get-Date)) {
    throw 'A current certificate with its private key is required.'
}
$codeSigning = $false
foreach ($extension in $certificate.Extensions) {
    if ($extension.Oid.Value -eq '2.5.29.37') {
        foreach ($usage in $extension.EnhancedKeyUsages) { if ($usage.Value -eq '1.3.6.1.5.5.7.3.3') { $codeSigning = $true } }
    }
}
if (-not $codeSigning) {
    throw 'The certificate must permit Code Signing.'
}
$tool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" | Sort-Object FullName -Descending | Select-Object -First 1
if (-not $tool) { throw 'Windows SDK SignTool is required.' }
$manifest = Get-Content (Join-Path $Directory 'SHA256SUMS.txt')
foreach ($architecture in @('amd64','arm64')) {
    $name = "nodexlm_windows_$architecture.zip"
    $archive = Join-Path $Directory $name
    $entries = @($manifest | Where-Object { $_ -match "^[a-fA-F0-9]{64}\s+\*?$([regex]::Escape($name))$" })
    if ($entries.Count -ne 1) { throw "Missing or duplicate checksum for $name" }
    $expected = ($entries[0] -split '\s+')[0]
    if ((Get-FileHash $archive -Algorithm SHA256).Hash -ne $expected) { throw "Checksum mismatch for $name" }
    $temporaryRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
    $staging = Join-Path $temporaryRoot ("nodexlm-sign-" + [guid]::NewGuid().ToString('N'))
    try {
        Expand-Archive -LiteralPath $archive -DestinationPath $staging
        $executable = Join-Path $staging 'nodexlm.exe'
        $arguments = @('sign','/sha1',$CertificateThumbprint,'/s','My','/fd','SHA256','/tr',$TimestampUrl,'/td','SHA256',$executable)
        & $tool.FullName @arguments
        if ($LASTEXITCODE -ne 0) { throw "SignTool failed for $architecture" }
        & $tool.FullName verify /pa /all $executable
        if ($LASTEXITCODE -ne 0) { throw "Authenticode chain or timestamp verification failed for $architecture" }
        $signature = Get-AuthenticodeSignature $executable
        if ($signature.Status -ne 'Valid' -or $signature.SignerCertificate.Thumbprint -ne $CertificateThumbprint -or -not $signature.TimeStamperCertificate) {
            throw "Invalid signer identity or missing timestamp for $architecture"
        }
        $signed = Join-Path $Directory "$name.signed.zip"
        Compress-Archive -Path (Join-Path $staging '*') -DestinationPath $signed
        Move-Item -LiteralPath $signed -Destination $archive -Force
    } finally {
        if (Test-Path $staging) { Remove-Item $staging -Recurse -Force }
    }
}
Write-Host 'Both Windows archives contain verified, timestamped Authenticode executables.'
