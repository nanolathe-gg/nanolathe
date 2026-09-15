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

Assert-Equal (Resolve-NanolatheWindowsArchitecture @(9) $true) 'amd64' 'Native x64 selection'
Assert-Equal (Resolve-NanolatheWindowsArchitecture @(12) $true) 'arm64' 'Native ARM64 selection, including emulated shells'
Assert-Equal (Resolve-NanolatheWindowsArchitecture @(12, 12) $true) 'arm64' 'Multiple ARM64 processors'
Assert-Throws { Resolve-NanolatheWindowsArchitecture @(9) $false } '32-bit PowerShell'
Assert-Throws { Resolve-NanolatheWindowsArchitecture @() $true } 'missing CPU information'
Assert-Throws { Resolve-NanolatheWindowsArchitecture @(0) $true } 'unsupported CPU'
Assert-Throws { Resolve-NanolatheWindowsArchitecture @(9, 12) $true } 'inconsistent CPU information'

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
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = Join-Path $base 'authored.zip'
    $zip = [IO.Compression.ZipFile]::Open($archive, [IO.Compression.ZipArchiveMode]::Create)
    try {
        $entry = $zip.CreateEntry("nested O'Brien & [alpha]/sentinel.txt")
        $writer = New-Object IO.StreamWriter($entry.Open())
        try { $writer.Write('authored archive content') } finally { $writer.Dispose() }
    } finally { $zip.Dispose() }
    $unpack = Join-Path $base 'extracted [alpha]'
    Expand-NanolatheArchive $archive $unpack
    Assert-Equal ([IO.File]::ReadAllText((Join-Path $unpack "nested O'Brien & [alpha]/sentinel.txt"))) 'authored archive content' 'ZIP extraction round trip'
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
    # A real child host with -NonInteractive must never display an update UI.
    $installerLiteral = "'" + $installer.Replace("'", "''") + "'"
    $noninteractiveCommand = ". $installerLiteral; if (Confirm-NanolatheUpdate 'alpha-1') { throw 'Noninteractive update accepted.' }"
    $noninteractiveEncoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($noninteractiveCommand))
    & (Get-Process -Id $PID).Path -NoProfile -NonInteractive -EncodedCommand $noninteractiveEncoded
    Assert-Equal $LASTEXITCODE 0 'Noninteractive host keeps current version without prompting'

    # Exercise the emitted launcher with authored downloads and a native-game
    # command seam. No network request, native game, or dialog runs in this test.
    $updateTest = @{
        Mode = 'unchanged'; Accept = $false; Downloads = @(); Offers = 0
        CurrentLaunches = 0; NewLaunches = 0; Installed = $false
        Root = (Join-Path $base 'game assets'); Base = $base
    }
    [void][IO.Directory]::CreateDirectory($updateTest.Root)
    [void][IO.Directory]::CreateDirectory((Join-Path $base 'logs'))
    $authoredInstaller = @'
param([switch]$NoRun)
if (!$NoRun) { throw 'Update must not launch from the installer.' }
if ($env:NANOLATHE_INSTALL_DIR -cne $updateTest.Base) { throw 'Wrong update installation base.' }
$updateTest.Installed = $true
$ErrorActionPreference = 'SilentlyContinue'
$ProgressPreference = 'Continue'
if ($updateTest.Mode -eq 'failed') { throw 'Authored build failed.' }
$destination = Join-Path (Join-Path $env:NANOLATHE_INSTALL_DIR 'releases') 'updated-release'
[void][IO.Directory]::CreateDirectory($destination)
[IO.File]::WriteAllText((Join-Path $destination 'nanolathe.exe'), 'authored new binary')
[IO.File]::WriteAllText((Join-Path $destination 'launch.ps1'), @"
param([string]`$Base, [string]`$Root)
if (`$env:NANOLATHE_SKIP_UPDATE_CHECK -ne '1') { throw 'Missing relaunch guard.' }
if (`$Base -cne `$updateTest.Base -or `$Root -cne `$updateTest.Root) { throw 'Relaunch lost original paths.' }
`$updateTest.NewLaunches++
if (`$updateTest.Mode -eq 'new-game-failed') { throw 'Authored new game failed.' }
"@)
[IO.File]::WriteAllText((Join-Path $env:NANOLATHE_INSTALL_DIR 'current.txt'), 'updated-release')
'@
    # BOM + CRLF detects hashing after decode/reencode instead of the raw file.
    $installerBytes = [byte[]](@(239, 187, 191) + [Text.Encoding]::UTF8.GetBytes($authoredInstaller.Replace("`n", "`r`n")))
    $authoredPath = Join-Path $base 'authored-installer.ps1'
    [IO.File]::WriteAllBytes($authoredPath, $installerBytes)
    $installerHash = (Get-FileHash -LiteralPath $authoredPath -Algorithm SHA256).Hash
    $updateTest.Manifest = $manifest.Replace(('b' * 40), ('c' * 40)) + "`ninstaller_ps1_sha256=$installerHash"
    function Get-NanolatheUpdateDownload([string]$Url, [string]$Path, [int]$TimeoutSeconds) {
        $updateTest.Downloads += $Url
        if ($Url -ceq 'https://nanolathe.gg/install/release.txt') {
            Assert-Equal $TimeoutSeconds 3 'Manifest wait is bounded'
            if ($updateTest.Mode -eq 'offline') { throw 'Authored network unavailable.' }
            $text = $updateTest.Manifest
            if ($updateTest.Mode -eq 'unchanged') { $text = $text.Replace(('c' * 40), ('b' * 40)) }
            if ($updateTest.Mode -eq 'malformed') { $text += "`nversion=duplicate" }
            if ($updateTest.Mode -eq 'missing-hash') { $text = $text.Replace("installer_ps1_sha256=$installerHash", 'future=1') }
            if ($updateTest.Mode -eq 'bad-hash') { $text = $text.Replace($installerHash, ('0' * 64)) }
            [IO.File]::WriteAllText($Path, $text)
        } elseif ($Url -ceq 'https://nanolathe.gg/install.ps1') {
            Assert-Equal $TimeoutSeconds 60 'Installer download is bounded'
            [IO.File]::WriteAllBytes($Path, $installerBytes)
        } else { throw "Unexpected update URL: $Url" }
    }
    function Confirm-NanolatheUpdate([string]$Version) {
        Assert-Equal $Version 'alpha-1' 'Same-version revision change is offered'
        $updateTest.Offers++
        return $updateTest.Accept
    }
    function Invoke-TestGame {
        if ($args[0] -eq '--check-install') {
            Assert-Equal $args[2] $updateTest.Root 'Game root validation keeps the original root'
        } elseif ($args[0] -eq '--root') {
            Assert-Equal $args[1] $updateTest.Root 'Current launch keeps original root'
            Assert-Equal $args[2] '--save-dir' 'Current launch keeps save directory option'
            Assert-Equal $args[3] (Join-Path $base 'saves') 'Current launch keeps save directory'
            Assert-Equal $env:NANOLATHE_SETTINGS (Join-Path $base 'settings.json') 'Current launch keeps settings'
            $updateTest.CurrentLaunches++
        } else { throw 'Unexpected game invocation.' }
        $global:LASTEXITCODE = 0
    }
    function Read-Host { return '' }
    $testLauncher = (Get-NanolatheLauncher).Replace('& $exe ', '& Invoke-TestGame ')
    $environmentBefore = @{}
    foreach ($key in @('NANOLATHE_INSTALL_DIR', 'NANOLATHE_SKIP_UPDATE_CHECK', 'NANOLATHE_SETTINGS')) {
        $environmentBefore[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, 'authored-existing-value', 'Process')
    }
    try {
        foreach ($mode in @('unchanged', 'offline', 'malformed', 'missing-hash', 'decline', 'bad-hash', 'failed', 'accepted', 'new-game-failed', 'skip')) {
            $updateTest.Mode = $mode
            $updateTest.Accept = $mode -notin @('unchanged', 'offline', 'malformed', 'missing-hash', 'decline')
            $updateTest.Downloads = @()
            $updateTest.Offers = 0
            $updateTest.Installed = $false
            $updateTest.CurrentLaunches = 0
            $updateTest.NewLaunches = 0
            [IO.File]::WriteAllText((Join-Path $old 'release.txt'), $manifest)
            Write-NanolatheData (Join-Path $base 'current.txt') 'old-release'
            $skipValue = 'authored-existing-value'
            if ($mode -eq 'skip') { $skipValue = '1' }
            [Environment]::SetEnvironmentVariable('NANOLATHE_SKIP_UPDATE_CHECK', $skipValue, 'Process')
            $errorBefore = $ErrorActionPreference
            $progressBefore = $ProgressPreference
            if ($mode -eq 'new-game-failed') {
                Assert-Throws { & ([scriptblock]::Create($testLauncher)) -Base $base -Root $updateTest.Root } 'New-game failure must not fall back and launch twice'
            } else { & ([scriptblock]::Create($testLauncher)) -Base $base -Root $updateTest.Root }
            Assert-Equal $ErrorActionPreference $errorBefore "$mode restores error preference"
            Assert-Equal $ProgressPreference $progressBefore "$mode restores progress preference"
            Assert-Equal $env:NANOLATHE_INSTALL_DIR 'authored-existing-value' "$mode restores installation environment"
            Assert-Equal $env:NANOLATHE_SKIP_UPDATE_CHECK $skipValue "$mode restores skip environment"
            Assert-Equal $env:NANOLATHE_SETTINGS 'authored-existing-value' "$mode restores settings environment"
            $expectedOffers = [int]($mode -in @('decline', 'bad-hash', 'failed', 'accepted', 'new-game-failed'))
            Assert-Equal $updateTest.Offers $expectedOffers "$mode update offers"
            $expectedDownloads = [int]($mode -ne 'skip') + [int]($mode -in @('bad-hash', 'failed', 'accepted', 'new-game-failed'))
            Assert-Equal $updateTest.Downloads.Count $expectedDownloads "$mode download count"
            Assert-Equal $updateTest.Installed ($mode -in @('failed', 'accepted', 'new-game-failed')) "$mode only executes a verified installer"
            $newLaunches = [int]($mode -in @('accepted', 'new-game-failed'))
            Assert-Equal $updateTest.NewLaunches $newLaunches "$mode new launch count"
            Assert-Equal $updateTest.CurrentLaunches (1 - $newLaunches) "$mode current launch count"
            if (!$newLaunches) {
                Assert-Equal ([IO.File]::ReadAllText((Join-Path $base 'current.txt'))) 'old-release' "$mode retains selected release"
            }
        }
    } finally {
        foreach ($key in $environmentBefore.Keys) { [Environment]::SetEnvironmentVariable($key, $environmentBefore[$key], 'Process') }
    }
    Write-Host 'Windows installer offline tests: OK'
} finally {
    if ([IO.Directory]::Exists($base)) { Remove-Item -LiteralPath $base -Recurse -Force }
}
