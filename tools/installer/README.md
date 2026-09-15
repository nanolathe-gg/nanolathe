# Source installer

The public alpha installer builds a pinned source release on the player's own
computer. It downloads a private Go toolchain and dependencies, creates a
shortcut, and starts the game. Original Total Annihilation assets are supplied
by the player. This is Nanolathe host installation policy, not retail behavior.

## Install and update

Mac or Linux, in Terminal:

```sh
curl -fsSL https://nanolathe.gg/install.sh | bash
```

Windows x64, in PowerShell:

```powershell
& ([scriptblock]::Create((irm https://nanolathe.gg/install.ps1)))
```

Close the game and rerun the same command to update. Each successful install
creates a versioned release directory; ordinary launches use the selected build
without downloading or compiling. Build failures leave the previous build
selected. Saves and settings are outside those release directories.

Supported installer targets: Mac Intel/Apple Silicon, Linux x86-64/ARM64, and
Windows x64. Linux requires a graphical desktop and the window-system, graphics,
and audio runtime libraries used by Ebitengine. Go alone builds the desktop
engine; a separate C compiler is not required. Windows ARM64 is not supported by
this first installer.

These builds have no paid publisher signature. Locally building the game does
not override OS execution policies. The ordinary Windows SmartScreen dialog may
offer **More info → Run anyway**; Smart App Control and managed-device policies
can block it without that option. The installers do not change security policy.

## Files and shortcuts

| Platform | Default installation directory | Shortcut |
|---|---|---|
| Mac | `~/Library/Application Support/Nanolathe` | `~/Applications/Nanolathe.app` |
| Linux | `$XDG_DATA_HOME/nanolathe`, or `~/.local/share/nanolathe` | `$XDG_DATA_HOME/applications/nanolathe.desktop`, or `~/.local/share/applications/nanolathe.desktop` |
| Windows | `%LOCALAPPDATA%\Nanolathe` | Nanolathe in the user's Start Menu |

Within the installation directory:

- `saves`: newly written `.SAV` files.
- `settings.json`: launcher-specific preferences.
- `logs`: installation and game diagnostics.
- `releases`: versioned builds, each with the exact `release.txt` manifest.
- `toolchains` and `cache`: private Go installation, dependencies, and build cache.
- Unix `current` symlink or Windows `current.txt`: selected release.
- Unix `game-root` or Windows `root.txt`: the remembered game-data directory.

The launcher validates and explicitly selects one content root. Several detected
installations require a selection; they are not automatically overlaid. Missing
or moved data triggers selection again. Mac app launches use a native picker;
Terminal/Linux launches can prompt in the terminal; Windows offers a folder
picker. `--check-install` validates startup mounts and required products, not
every asset in the game. A later content error is recorded in the game log.

Existing retail saves and manual-build settings are not moved or overwritten.
To import a save, copy its `.SAV` file into the new `saves` directory with the
game closed. Save compatibility remains a work in progress. The launcher passes
`--save-dir` and `NANOLATHE_SETTINGS`; manual engine launches preserve their
existing defaults unless given overrides.

## Options

To install without launching or selecting game data:

```sh
curl -fsSL https://nanolathe.gg/install.sh | bash -s -- --no-run
```

```powershell
& ([scriptblock]::Create((irm https://nanolathe.gg/install.ps1))) -NoRun
```

Pass `--root "/path/to/TotalAnnihilation"` on Unix or
`-Root 'C:\Games\Total Annihilation'` on Windows to select a folder explicitly.
Unix validates and remembers an explicit root during installation, including
with `--no-run`; Windows applies `-Root` when launching, so omit `-NoRun` when
selecting data. Set `NANOLATHE_INSTALL_DIR` to an absolute path to choose another
installation directory. It does not change the shortcut location.

On Unix, `<installation directory>/launch.sh --root PATH` changes the remembered
folder without rebuilding. Removing the remembered-root file on either platform
makes the next launch select again. Deleting a stale Unix `.install-lock`
directory is appropriate only after confirming no installer is still running.

## Remove

Close the game, remove its shortcut, and delete the installation directory.
Copy out any saves/settings you want to keep first. This removes the private
compiler and cache too. The original TA installation remains separate.

## Release maintenance

The canonical scripts live here; the website serves byte-for-byte copies as
`/install.sh` and `/install.ps1`. A website-owned `/install/release.txt` pins the
source commit, source archive hashes, Go patch version, and per-platform Go
archive hashes. Manifest data is never evaluated as code. Downloads use HTTPS;
hashes pin the expected bytes but are not a separate publisher signature.

1. Integrate changes and run `tools/check`, `tools/check-retail`, and the native
   **Source installer** CI workflow. Offline tests run with
   `python3 tools/installer/test_install.py` and
   `powershell -NoProfile -File tools/installer/test-install.ps1`.
2. Push the tested engine commit. In the website checkout, run
   `python3 scripts/prepare-source-release.py --engine /path/to/nanolathe --revision FULL_COMMIT --version RELEASE_LABEL --go-version GO_PATCH`.
   It checks public archive scripts against that local commit and reads official
   Go checksums. Review the scripts and manifest together.
3. Push the website changes to a review branch. Dispatch the engine's
   **Source installer** workflow with that branch's raw HTTPS manifest URL to
   test actual toolchain download and installation on native runners. These
   checks omit game launch and require no retail data.
4. On a desktop with retail assets, run the installer, select data, start a
   battle, and check saving/loading. A successful compiler run alone does not
   establish a playable installation. Check platform-specific shortcuts and
   folder selection on the platforms being promoted.
5. Run website `make check` and publish its reviewed commit. Future installs
   resolve the newly published manifest. Prior builds stay installed until the
   user removes them.

The first install has a cold compiler/dependency cache, so measure its time
separately from updates. No first-install duration is promised.
