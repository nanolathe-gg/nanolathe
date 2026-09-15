# Offline tests; no Windows, network, Go compiler, or retail assets required.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$installer = Join-Path $PSScriptRoot 'install.ps1'
$tokens = $null
$errors = $null
[void][Management.Automation.Language.Parser]::ParseFile($installer, [ref]$tokens, [ref]$errors)
if ($errors.Count) { throw ($errors | Out-String) }
. $installer

function Assert-Throws([scriptblock]$Action, [string]$Message) {
    $threw = $false
    try { & $Action | Out-Null } catch { $threw = $true }
    if (!$threw) { throw "Expected rejection: $Message" }
}
function Assert-Equal($Actual, $Expected, [string]$Message) {
    if ($Actual -cne $Expected) { throw "$Message (actual: $Actual; expected: $Expected)" }
}

$hash = 'a' * 64
$manifest = @"
version=alpha-1
source_revision=$('b' * 40)
source_tar_sha256=$hash
source_zip_sha256=$hash
go_version=1.25.0
go_darwin_arm64_sha256=$hash
go_darwin_amd64_sha256=$hash
go_linux_amd64_sha256=$hash
go_linux_arm64_sha256=$hash
go_windows_amd64_sha256=$hash
"@
Assert-Equal (Read-NanolatheManifest $manifest).version 'alpha-1' 'Valid manifest'
Assert-Equal (Read-NanolatheManifest ($manifest.Replace("`n", "`r`n"))).go_version '1.25.0' 'CRLF manifest'
Assert-Equal (Read-NanolatheManifest ($manifest + "`nfuture_key=`$(throw 'must stay inert')")).future_key "`$(throw 'must stay inert')" 'Unknown keys remain data'
Assert-Throws { Read-NanolatheManifest ($manifest + "`nversion=evil") } 'duplicate key'
Assert-Throws { Read-NanolatheManifest ($manifest + "`nthrow 'execute'") } 'code instead of key=value'
Assert-Throws { Read-NanolatheManifest ($manifest.Replace('alpha-1', '../other')) } 'path traversal'
Assert-Throws { Read-NanolatheManifest ($manifest.Replace('alpha-1', "`$(throw 'execute')")) } 'code in version'
Assert-Throws { Read-NanolatheManifest ($manifest.Replace(('b' * 40), ('B' * 40))) } 'uppercase revision'
Assert-Throws { Read-NanolatheManifest ($manifest.Replace('go_version=1.25.0', 'go_version=latest')) } 'unpinned toolchain'
Assert-Throws { Read-NanolatheManifest ($manifest.Replace("source_zip_sha256=$hash", 'source_zip_sha256=abc')) } 'bad digest'
Assert-Throws { Read-NanolatheManifest ($manifest.Replace("source_tar_sha256=$hash", '')) } 'missing digest'

$base = Join-Path ([IO.Path]::GetTempPath()) ("nanolathe-test O'Brien & `$x; [alpha] " + [guid]::NewGuid().ToString('N'))
try {
    [void][IO.Directory]::CreateDirectory((Join-Path $base 'releases'))
    $old = Join-Path (Join-Path $base 'releases') 'old-release'
    [void][IO.Directory]::CreateDirectory($old)
    [IO.File]::WriteAllText((Join-Path $old 'nanolathe.exe'), 'working binary')
    Write-NanolatheData (Join-Path $base 'current.txt') 'old-release'
    $stage = Join-Path $base 'staging'
    [void][IO.Directory]::CreateDirectory($stage)
    $binary = Join-Path $stage 'nanolathe.exe'
    [IO.File]::WriteAllText($binary, 'unverified binary')
    $digest = (Get-FileHash -LiteralPath $binary -Algorithm SHA256).Hash
    Test-NanolatheChecksum $binary $digest
    Assert-Throws { Test-NanolatheChecksum $binary ('0' * 64) } 'checksum mismatch'
    Assert-Throws { Publish-NanolatheRelease $base $stage 'new-release' { throw 'failed build or --help' } } 'failed verification'
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $base 'current.txt'))) 'old-release' 'Failure preserves active release'
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $old 'nanolathe.exe'))) 'working binary' 'Failure preserves old binary'
    Assert-Throws { Publish-NanolatheRelease $base $stage 'new-release' {} { throw 'shortcut creation failed' } } 'failed shortcut preparation'
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $base 'current.txt'))) 'old-release' 'Shortcut failure preserves active release'
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $old 'nanolathe.exe'))) 'working binary' 'Shortcut failure preserves old binary'
    $published = Publish-NanolatheRelease $base $stage 'new-release' {
        param($candidate)
        if ([IO.File]::ReadAllText($candidate) -cne 'unverified binary') { throw 'Wrong candidate' }
        Write-Output 'Authored --help output'
    }
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $base 'current.txt'))) 'new-release' 'Success switches active release'
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $old 'nanolathe.exe'))) 'working binary' 'Success retains previous binary'
    Assert-Equal $published (Join-Path (Join-Path $base 'releases') 'new-release') 'Published directory'

    $launcher = Get-NanolatheLauncher
    [void][Management.Automation.Language.Parser]::ParseInput($launcher, [ref]$tokens, [ref]$errors)
    if ($errors.Count) { throw ($errors | Out-String) }
    # Run the encoded shortcut command against an authored launcher, proving the
    # base path survives spaces, apostrophes, brackets and shell metacharacters.
    [IO.File]::WriteAllText((Join-Path $published 'launch.ps1'), 'param($Base); $Base')
    $arguments = Get-NanolatheLaunchCommand $base
    $encoded = ($arguments -split ' ')[-1]
    $command = [Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($encoded))
    Assert-Equal (& ([scriptblock]::Create($command))) $base 'Shortcut literal path round trip'
    Write-Host 'Windows installer offline tests: OK'
} finally {
    if ([IO.Directory]::Exists($base)) { Remove-Item -LiteralPath $base -Recurse -Force }
}
