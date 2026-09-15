# Windows PowerShell 5.1. Invoke from the website in a child script scope:
# & ([scriptblock]::Create((irm https://nanolathe.gg/install.ps1)))
[CmdletBinding()]
param([string]$Root, [switch]$NoRun, [switch]$Help)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

function Read-NanolatheManifest([string]$Text) {
    $values = @{}
    foreach ($line in ($Text -split "`n")) {
        $line = $line.TrimEnd("`r")
        if ($line -eq '' -or $line.StartsWith('#')) { continue }
        if ($line -cnotmatch '^([a-z][a-z0-9_]*)=([^\r\n]+)$') { throw 'Malformed release manifest line.' }
        $key, $value = $Matches[1], $Matches[2]
        if ($values.ContainsKey($key)) { throw "Duplicate release manifest key: $key" }
        # Future keys remain inert strings; no manifest text is evaluated.
        $values[$key] = $value
    }
    $rules = @{
        version = '^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$'
        source_revision = '^[0-9a-f]{40}$'
        go_version = '^[0-9]+\.[0-9]+\.[0-9]+$'
    }
    foreach ($key in @('source_tar_sha256', 'source_zip_sha256', 'go_darwin_arm64_sha256',
        'go_darwin_amd64_sha256', 'go_linux_amd64_sha256', 'go_linux_arm64_sha256', 'go_windows_amd64_sha256')) {
        $rules[$key] = '^[0-9a-fA-F]{64}$'
    }
    foreach ($key in $rules.Keys) {
        if (!$values.ContainsKey($key) -or $values[$key] -cnotmatch $rules[$key]) {
            throw "Missing or invalid release manifest value: $key"
        }
    }
    return $values
}

function Test-NanolatheChecksum([string]$Path, [string]$Expected) {
    if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash -ine $Expected) {
        throw "SHA-256 verification failed: $Path"
    }
}

function Get-NanolatheDownload([string]$Url, [string]$Path, [string]$Hash) {
    Write-Host "Downloading $Url"
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Path -TimeoutSec 600
    Write-Host 'Download complete; verifying SHA-256...'
    Test-NanolatheChecksum $Path $Hash
}

function Expand-NanolatheArchive([string]$Archive, [string]$Destination) {
    Write-Host "Extracting verified archive: $Archive"
    # Use the Framework ZIP implementation to avoid per-entry PowerShell
    # overhead. Callers supply a fresh directory inside the temporary workspace.
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [IO.Compression.ZipFile]::ExtractToDirectory($Archive, $Destination)
    Write-Host 'Extraction complete.'
}

function Write-NanolatheData([string]$Path, [string]$Value) {
    $temp = "$Path.$([guid]::NewGuid().ToString('N')).tmp"
    $backup = "$temp.previous"
    try {
        [IO.File]::WriteAllText($temp, $Value, (New-Object Text.UTF8Encoding($false)))
        if ([IO.File]::Exists($Path)) { [IO.File]::Replace($temp, $Path, $backup) }
        else { [IO.File]::Move($temp, $Path) }
    } finally {
        foreach ($cleanup in @($temp, $backup)) {
            if ([IO.File]::Exists($cleanup)) { Remove-Item -LiteralPath $cleanup -Force -ErrorAction SilentlyContinue }
        }
    }
}

function Publish-NanolatheRelease([string]$Base, [string]$Stage, [string]$Name, [scriptblock]$Verify, [scriptblock]$PrepareLauncher = {}) {
    # The verifier must finish successfully before touching the active pointer.
    & $Verify (Join-Path $Stage 'nanolathe.exe') | Out-Host
    $destination = Join-Path (Join-Path $Base 'releases') $Name
    & $PrepareLauncher $destination | Out-Host
    [IO.Directory]::Move($Stage, $destination)
    Write-NanolatheData (Join-Path $Base 'current.txt') $Name
    return $destination
}

function Get-NanolatheLauncher {
    return @'
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$Base, [string]$Root)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$settingsBefore = [Environment]::GetEnvironmentVariable('NANOLATHE_SETTINGS', 'Process')
$log = Join-Path $Base ('logs\game-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.log')
try {
    $release = [IO.File]::ReadAllText((Join-Path $Base 'current.txt'))
    if ($release -cnotmatch '^[A-Za-z0-9][A-Za-z0-9._-]+$') { throw 'Invalid current release pointer.' }
    $exe = Join-Path (Join-Path (Join-Path $Base 'releases') $release) 'nanolathe.exe'
    if (!(Test-Path -LiteralPath $exe -PathType Leaf)) { throw 'The installed game is missing. Rerun the installer.' }
    $remembered = Join-Path $Base 'root.txt'
    $explicitRoot = ![string]::IsNullOrWhiteSpace($Root)
    if (!$explicitRoot -and [IO.File]::Exists($remembered)) { $Root = [IO.File]::ReadAllText($remembered) }
    function Test-GameRoot([string]$Candidate) {
        if ([string]::IsNullOrWhiteSpace($Candidate) -or !(Test-Path -LiteralPath $Candidate -PathType Container)) { return $false }
        $ErrorActionPreference = 'Continue'
        & $exe --check-install --root $Candidate 2>&1 | ForEach-Object {
            [IO.File]::AppendAllText($log, $_.ToString() + [Environment]::NewLine)
        }
        return ($LASTEXITCODE -eq 0)
    }
    if (!(Test-GameRoot $Root)) {
        if ($explicitRoot) { throw "The selected Total Annihilation folder is not usable: $Root. See $log" }
        $Root = ''
        $ErrorActionPreference = 'Continue'
        $discovery = @(& $exe --list-installs 2>&1)
        $discoveryExit = $LASTEXITCODE
        $ErrorActionPreference = 'Stop'
        if ($discoveryExit -gt 1) { throw "Game discovery failed. See $log" }
        $candidates = @($discovery | ForEach-Object {
            if ($_ -is [Management.Automation.ErrorRecord]) {
                [IO.File]::AppendAllText($log, $_.ToString() + [Environment]::NewLine)
            } elseif (![string]::IsNullOrWhiteSpace($_)) { $_ }
        })
        if ($candidates.Count -eq 1 -and (Test-GameRoot $candidates[0])) { $Root = $candidates[0] }
        else {
            if ($candidates.Count -gt 1) {
                Write-Host 'Several installations were found. Choose the one to use:'
                $candidates | ForEach-Object { Write-Host "  $_" }
            }
            Write-Host 'Choose your existing Total Annihilation game folder. Game assets are not included.'
            try {
                Add-Type -AssemblyName System.Windows.Forms
                $picker = New-Object System.Windows.Forms.FolderBrowserDialog
                try {
                    $picker.Description = 'Select your Total Annihilation game folder'
                    $picker.ShowNewFolderButton = $false
                    if ($picker.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { $Root = $picker.SelectedPath }
                } finally { $picker.Dispose() }
            } catch { Write-Host 'Folder picker unavailable; enter the path below.' }
            if ([string]::IsNullOrWhiteSpace($Root)) { $Root = Read-Host 'Total Annihilation folder (blank to cancel)' }
            if (!(Test-GameRoot $Root)) { throw "No usable game folder selected. Launch again to choose a folder. See $log" }
        }
    }
    [IO.File]::WriteAllText($remembered, $Root, (New-Object Text.UTF8Encoding($false)))
    [Environment]::SetEnvironmentVariable('NANOLATHE_SETTINGS', (Join-Path $Base 'settings.json'), 'Process')
    Write-Host "Starting Nanolathe. Log: $log"
    $ErrorActionPreference = 'Continue'
    & $exe --root $Root --save-dir (Join-Path $Base 'saves') 2>&1 | ForEach-Object {
        $message = $_.ToString()
        [IO.File]::AppendAllText($log, $message + [Environment]::NewLine)
        Write-Host $message
    }
    $gameExit = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
    if ($gameExit -ne 0) { throw "Nanolathe exited with code $gameExit. See $log" }
} catch {
    Write-Host "Nanolathe: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "Log: $log"
    if ([Environment]::UserInteractive) { [void](Read-Host 'Press Enter to close') }
    throw
} finally {
    [Environment]::SetEnvironmentVariable('NANOLATHE_SETTINGS', $settingsBefore, 'Process')
}
'@
}

function Get-NanolatheLaunchCommand([string]$Base) {
    # Encode a fixed command plus a quoted literal path, avoiding cmd.exe and
    # execution-policy changes. current.txt and root.txt are only read as data.
    $literal = "'" + $Base.Replace("'", "''") + "'"
    $command = @'
$ErrorActionPreference = 'Stop'
try {
    $base = BASE_LITERAL
    $release = [IO.File]::ReadAllText((Join-Path $base 'current.txt'))
    if ($release -cnotmatch '^[A-Za-z0-9][A-Za-z0-9._-]+$') { throw 'Invalid current release pointer.' }
    $launcher = Join-Path (Join-Path (Join-Path $base 'releases') $release) 'launch.ps1'
    & ([scriptblock]::Create([IO.File]::ReadAllText($launcher))) -Base $base
} catch { Write-Host $_ -ForegroundColor Red; [void](Read-Host 'Press Enter to close'); exit 1 }
'@
    $command = $command.Replace('BASE_LITERAL', $literal)
    return '-NoProfile -STA -EncodedCommand ' + [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
}

function Invoke-NanolatheInstaller {
    # Windows PowerShell 5.1 renders progress for each download chunk. Suppress
    # that overhead only in this invocation; explicit stage messages remain.
    $ProgressPreference = 'SilentlyContinue'
    if ($Help) {
        Write-Host @'
Nanolathe source installer for x64 Windows (PowerShell 5.1 or later).
Usage: & ([scriptblock]::Create((irm https://nanolathe.gg/install.ps1))) [-Root PATH] [-NoRun]
Rerun the same command to update. -NoRun skips game-folder selection and launch.
Set NANOLATHE_INSTALL_DIR to override the default LOCALAPPDATA\Nanolathe folder.
Downloads verified source and a private Go compiler. Original game assets are required to play.
'@
        return
    }
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'This installer supports x64 Windows only.' }
    $architecture = $env:PROCESSOR_ARCHITECTURE
    if ($env:PROCESSOR_ARCHITEW6432) { $architecture = $env:PROCESSOR_ARCHITEW6432 }
    # Check the host as well as this process: x64 emulation on ARM reports an
    # AMD64 process, which does not establish support for this alpha.
    $nativeArchitectures = @(Get-CimInstance -ClassName Win32_Processor | Select-Object -ExpandProperty Architecture)
    if ($architecture -ine 'AMD64' -or ![Environment]::Is64BitProcess -or
        $nativeArchitectures.Count -eq 0 -or @($nativeArchitectures | Where-Object { $_ -ne 9 }).Count -ne 0) {
        throw 'This alpha supports x64 Windows only. Run 64-bit PowerShell on an x64 PC; ARM64 is not supported.'
    }
    $base = $env:NANOLATHE_INSTALL_DIR
    if ([string]::IsNullOrWhiteSpace($base)) { $base = Join-Path $env:LOCALAPPDATA 'Nanolathe' }
    $base = [IO.Path]::GetFullPath($base)
    foreach ($folder in @('', 'logs', 'saves', 'releases', 'toolchains', 'cache', 'cache\gopath', 'cache\build')) {
        [void][IO.Directory]::CreateDirectory((Join-Path $base $folder))
    }
    $lock = $null
    $stage = $null
    $work = $null
    $transcribing = $false
    $log = Join-Path $base ('logs\install-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.log')
    $tlsBefore = [Net.ServicePointManager]::SecurityProtocol
    $savedEnvironment = @{}
    try {
        $lock = [IO.File]::Open((Join-Path $base 'install.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
        Start-Transcript -LiteralPath $log | Out-Null
        $transcribing = $true
        Write-Host "Installing Nanolathe into $base"
        [Net.ServicePointManager]::SecurityProtocol = $tlsBefore -bor [Net.SecurityProtocolType]::Tls12
        Write-Host 'Downloading release manifest...'
        $manifestText = (Invoke-WebRequest -UseBasicParsing -Uri 'https://nanolathe.gg/install/release.txt' -TimeoutSec 60).Content
        $manifest = Read-NanolatheManifest $manifestText
        Write-Host "Release manifest verified: $($manifest.version)"
        $work = Join-Path $base ('work-' + [guid]::NewGuid().ToString('N'))
        [void][IO.Directory]::CreateDirectory($work)
        $toolchainName = 'go-' + $manifest.go_version + '-' + $manifest.go_windows_amd64_sha256.Substring(0,16).ToLowerInvariant()
        $toolchain = Join-Path (Join-Path $base 'toolchains') $toolchainName
        $go = Join-Path $toolchain 'go\bin\go.exe'
        if (!(Test-Path -LiteralPath $go -PathType Leaf)) {
            $archive = Join-Path $work 'go.zip'
            Get-NanolatheDownload "https://go.dev/dl/go$($manifest.go_version).windows-amd64.zip" $archive $manifest.go_windows_amd64_sha256
            $unpack = Join-Path $work 'toolchain'
            Expand-NanolatheArchive $archive $unpack
            if (!(Test-Path -LiteralPath (Join-Path $unpack 'go\bin\go.exe'))) { throw 'Go archive is missing go.exe.' }
            [IO.Directory]::Move($unpack, $toolchain)
        }
        $archive = Join-Path $work 'source.zip'
        Get-NanolatheDownload "https://codeload.github.com/nanolathe-gg/nanolathe/zip/$($manifest.source_revision)" $archive $manifest.source_zip_sha256
        $unpack = Join-Path $work 'source'
        Expand-NanolatheArchive $archive $unpack
        $source = Join-Path $unpack ('nanolathe-' + $manifest.source_revision)
        if (!(Test-Path -LiteralPath (Join-Path $source 'go.sum'))) { throw 'Source archive is missing go.sum.' }
        $sumBefore = (Get-FileHash -LiteralPath (Join-Path $source 'go.sum') -Algorithm SHA256).Hash
        $environment = @{
            GOENV = 'off'; GOTOOLCHAIN = 'local'; CGO_ENABLED = '0'; GOOS = 'windows'; GOARCH = 'amd64'; GO111MODULE = 'on'
            GOPATH = (Join-Path $base 'cache\gopath'); GOCACHE = (Join-Path $base 'cache\build')
            GOROOT = (Join-Path $toolchain 'go'); GOFLAGS = ''; GOWORK = 'off'
            GOMODCACHE = (Join-Path $base 'cache\gopath\pkg\mod')
            GOPROXY = 'https://proxy.golang.org'; GOSUMDB = 'sum.golang.org'
            GOPRIVATE = ''; GONOPROXY = ''; GONOSUMDB = ''; GOEXPERIMENT = ''; GOAMD64 = 'v1'
        }
        foreach ($key in $environment.Keys) {
            $savedEnvironment[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
            [Environment]::SetEnvironmentVariable($key, $environment[$key], 'Process')
        }
        $name = $manifest.version + '-' + $manifest.source_revision + '-' + [guid]::NewGuid().ToString('N')
        $stage = Join-Path $base ('stage-' + [guid]::NewGuid().ToString('N'))
        [void][IO.Directory]::CreateDirectory($stage)
        Push-Location -LiteralPath $source
        try {
            Write-Host 'Building Nanolathe (the first build can take several minutes)...'
            $ErrorActionPreference = 'Continue'
            & $go build -mod=readonly -trimpath -buildvcs=false -o (Join-Path $stage 'nanolathe.exe') ./cmd/nanolathe
            $buildExit = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            if ($buildExit -ne 0) { throw "Go build failed with code $buildExit." }
            $ErrorActionPreference = 'Continue'
            & $go mod verify
            $verifyExit = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            if ($verifyExit -ne 0) { throw 'Go module checksum verification failed.' }
            if ((Get-FileHash -LiteralPath (Join-Path $source 'go.sum') -Algorithm SHA256).Hash -ne $sumBefore) {
                throw 'Build changed the pinned go.sum.'
            }
        } finally { Pop-Location }
        [IO.File]::WriteAllText((Join-Path $stage 'launch.ps1'), (Get-NanolatheLauncher), (New-Object Text.UTF8Encoding($false)))
        [IO.File]::WriteAllText((Join-Path $stage 'release.txt'), $manifestText, (New-Object Text.UTF8Encoding($false)))
        $release = Publish-NanolatheRelease $base $stage $name {
            param($exe)
            $ErrorActionPreference = 'Continue'
            & $exe --help
            $verifyExit = $LASTEXITCODE
            $ErrorActionPreference = 'Stop'
            if ($verifyExit -ne 0) { throw "Built game verification failed with code $verifyExit." }
        } {
            param($destination)
            # Finish shortcut creation before promotion. The shortcut's fixed
            # loader continues to use the previous current.txt until the switch.
            $shell = New-Object -ComObject WScript.Shell
            try {
                $temporaryShortcut = Join-Path $work 'Nanolathe.lnk'
                $shortcut = $shell.CreateShortcut($temporaryShortcut)
                $shortcut.TargetPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
                $shortcut.Arguments = Get-NanolatheLaunchCommand $base
                $shortcut.WorkingDirectory = $base
                $shortcut.Description = 'Play Nanolathe using your Total Annihilation game assets'
                $shortcut.IconLocation = (Join-Path $destination 'nanolathe.exe') + ',0'
                $shortcut.Save()
                [IO.File]::Copy($temporaryShortcut, (Join-Path ([Environment]::GetFolderPath('Programs')) 'Nanolathe.lnk'), $true)
            } finally { [void][Runtime.InteropServices.Marshal]::ReleaseComObject($shell) }
        }
        $stage = $null
        Write-Host "Installed $($manifest.version). Launch Nanolathe from the Start Menu."
        Write-Host "Settings and saves: $base"
    } catch {
        Write-Host "Nanolathe installation failed: $($_.Exception.Message)" -ForegroundColor Red
        Write-Host "Log: $log"
        throw
    } finally {
        foreach ($key in $savedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key, $savedEnvironment[$key], 'Process') }
        [Net.ServicePointManager]::SecurityProtocol = $tlsBefore
        if ($stage -and [IO.Directory]::Exists($stage)) { Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue }
        if ($work -and [IO.Directory]::Exists($work)) { Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue }
        if ($transcribing) { Stop-Transcript -ErrorAction SilentlyContinue | Out-Null }
        if ($lock) { $lock.Dispose() }
    }
    if (!$NoRun) { & ([scriptblock]::Create((Get-NanolatheLauncher))) -Base $base -Root $Root }
}

# Dot-sourcing exposes the pure helpers for offline tests without running setup.
if ($MyInvocation.InvocationName -ne '.') { Invoke-NanolatheInstaller }
