# The local test signing certificate for sideload packages (Xbox Dev Mode,
# a PC). Its subject is Package.appxmanifest's Publisher, read from the
# manifest, so the two can't drift; one made for another publisher is
# replaced. Writes OnScreen.Xbox_TemporaryKey.pfx (password in the csproj)
# and the public OnScreen.Xbox_TemporaryKey.cer next to this script; both are
# gitignored. A Store build is re-signed by the Store.
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$pfx = Join-Path $here 'OnScreen.Xbox_TemporaryKey.pfx'
$cer = Join-Path $here 'OnScreen.Xbox_TemporaryKey.cer'
$manifest = [xml](Get-Content (Join-Path $here 'Package.appxmanifest') -Raw)
$subject = $manifest.Package.Identity.Publisher

if ((Test-Path $pfx) -and (Test-Path $cer)) {
    $have = (New-Object System.Security.Cryptography.X509Certificates.X509Certificate2 $cer).Subject
    if ($have -eq $subject) { Write-Host "exists: $pfx ($subject)"; return }
    Write-Host "replacing the test certificate for $have with one for $subject"
    Remove-Item $pfx, $cer
}

$cert = New-SelfSignedCertificate -Type Custom -Subject $subject `
    -KeyUsage DigitalSignature -FriendlyName 'OnScreen Xbox test signing' `
    -CertStoreLocation 'Cert:\CurrentUser\My' -NotAfter (Get-Date).AddYears(3) `
    -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3', '2.5.29.19={text}')
$pw = ConvertTo-SecureString -String 'onscreen-dev' -Force -AsPlainText
Export-PfxCertificate -Cert $cert -FilePath $pfx -Password $pw | Out-Null
Export-Certificate -Cert $cert -FilePath $cer | Out-Null
Write-Host "wrote $pfx and $cer (thumbprint $($cert.Thumbprint))"
