# The local test signing certificate for sideload packages (Xbox Dev Mode,
# a PC). Subject must match Package.appxmanifest's Publisher. Writes
# OnScreen.Xbox_TemporaryKey.pfx (password in the csproj) and the public
# OnScreen.Xbox_TemporaryKey.cer next to this script; both are gitignored.
# A Store build is signed by the Store, not with this.
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$pfx = Join-Path $here 'OnScreen.Xbox_TemporaryKey.pfx'
$cer = Join-Path $here 'OnScreen.Xbox_TemporaryKey.cer'
if (Test-Path $pfx) { Write-Host "exists: $pfx"; return }

$cert = New-SelfSignedCertificate -Type Custom -Subject 'CN=OnScreen Dev' `
    -KeyUsage DigitalSignature -FriendlyName 'OnScreen Xbox test signing' `
    -CertStoreLocation 'Cert:\CurrentUser\My' -NotAfter (Get-Date).AddYears(3) `
    -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.3', '2.5.29.19={text}')
$pw = ConvertTo-SecureString -String 'onscreen-dev' -Force -AsPlainText
Export-PfxCertificate -Cert $cert -FilePath $pfx -Password $pw | Out-Null
Export-Certificate -Cert $cert -FilePath $cer | Out-Null
Write-Host "wrote $pfx and $cer (thumbprint $($cert.Thumbprint))"
