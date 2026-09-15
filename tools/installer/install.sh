#!/bin/bash
# Per-user source installer. This is Nanolathe host policy, not retail behavior.
set -euo pipefail
umask 077

fail() { printf 'Nanolathe installer: %s\n' "$*" >&2; exit 1; }
root_arg= no_run=false
while [ "$#" -gt 0 ]; do
    case "$1" in
        --root) [ "$#" -ge 2 ] || fail '--root requires a directory'; root_arg=$2; shift 2 ;;
        --no-run) no_run=true; shift ;;
        --help) printf '%s\n' 'Usage: install.sh [--root DIRECTORY] [--no-run]' 'Install or update Nanolathe for this user; existing game data is required.'; exit 0 ;;
        *) fail "unknown option: $1" ;;
    esac
done
case "$(uname -s)" in
    Darwin) os=darwin; default_base="$HOME/Library/Application Support/Nanolathe" ;;
    Linux) os=linux; default_base="${XDG_DATA_HOME:-$HOME/.local/share}/nanolathe" ;;
    *) fail 'supported systems are macOS and Linux' ;;
esac
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; x86_64|amd64) arch=amd64 ;; *) fail 'supported CPUs are arm64 and amd64' ;; esac
base=${NANOLATHE_INSTALL_DIR:-$default_base}
case "$base" in /*) ;; *) fail 'installation directory must be absolute' ;; esac
case "$base$root_arg" in *$'\n'*|*$'\r'*) fail 'directory names cannot contain newlines' ;; esac
for cmd in curl tar; do command -v "$cmd" >/dev/null || fail "required command missing: $cmd"; done
if command -v shasum >/dev/null; then hash_cmd=(shasum -a 256); elif command -v sha256sum >/dev/null; then hash_cmd=(sha256sum); else fail 'shasum or sha256sum is required'; fi
mkdir -p "$base/logs" "$base/saves" "$base/releases" "$base/toolchains" "$base/cache"
base=$(cd "$base" && pwd -P)
log="$base/logs/install-$(date +%Y%m%d-%H%M%S)-$$.log"
mkdir "$base/.install-lock" 2>/dev/null || fail "another installer is running (lock: $base/.install-lock)"
stage=
cleanup() {
    result=$?
    [ -z "$stage" ] || rm -rf "$stage"
    rmdir "$base/.install-lock" 2>/dev/null || true
    if [ "$result" -ne 0 ]; then printf 'Installation failed. Your previous release is preserved. Log: %s\n' "$log" >&4; tail -n 15 "$log" >&4; fi
}
trap cleanup EXIT
exec 3>&1 4>&2
printf 'Installing Nanolathe. Build and download log: %s\n' "$log"

fetch() { curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error --retry 2 --output "$2" "$1"; }
verify() {
    local actual
    actual=$("${hash_cmd[@]}" "$1"); actual=${actual%% *}
    [ "$actual" = "$2" ] || fail "checksum mismatch: $1"
}
# Rename the pointer itself, even when its old target is a directory.
replace_pointer() {
    if [ "$os" = darwin ]; then mv -fh "$1" "$2"; else mv -fT "$1" "$2"; fi
}
install_release() {
    stage=$(mktemp -d "$base/.install-XXXXXXXX")
    fetch https://nanolathe.gg/install/release.txt "$stage/release.txt"
    local line key value seen='|' version= revision= source_hash= go_version= go_da= go_dx= go_la= go_lx= zip_hash= go_wx=
    while IFS= read -r line || [ -n "$line" ]; do
        [ -n "$line" ] || fail 'empty manifest line'
        case "$line" in *=*) ;; *) fail 'malformed manifest line' ;; esac
        key=${line%%=*}; value=${line#*=}
        case "$seen" in *"|$key|"*) fail "duplicate manifest key: $key" ;; esac
        seen="$seen$key|"
        case "$key" in
            version) [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$ ]] || fail 'invalid version'; version=$value ;;
            source_revision) [[ "$value" =~ ^[0-9a-f]{40}$ ]] || fail 'invalid source revision'; revision=$value ;;
            go_version) [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'invalid Go version'; go_version=$value ;;
            installer_sh_sha256|installer_ps1_sha256|source_tar_sha256|source_zip_sha256|go_darwin_arm64_sha256|go_darwin_amd64_sha256|go_linux_arm64_sha256|go_linux_amd64_sha256|go_windows_amd64_sha256)
                [[ "$value" =~ ^[0-9a-f]{64}$ ]] || fail "invalid checksum: $key"
                case "$key" in
                    source_tar_sha256) source_hash=$value ;; source_zip_sha256) zip_hash=$value ;;
                    go_darwin_arm64_sha256) go_da=$value ;; go_darwin_amd64_sha256) go_dx=$value ;;
                    go_linux_arm64_sha256) go_la=$value ;; go_linux_amd64_sha256) go_lx=$value ;;
                    go_windows_amd64_sha256) go_wx=$value ;;
                esac ;;
            *) fail "unknown manifest key: $key" ;;
        esac
    done < "$stage/release.txt"
    for value in "$version" "$revision" "$source_hash" "$go_version" "$zip_hash" "$go_da" "$go_dx" "$go_la" "$go_lx" "$go_wx"; do
        [ -n "$value" ] || fail 'manifest is missing a required key'
    done
    local go_hash toolchain release
    case "$os-$arch" in darwin-arm64) go_hash=$go_da ;; darwin-amd64) go_hash=$go_dx ;; linux-arm64) go_hash=$go_la ;; linux-amd64) go_hash=$go_lx ;; esac
    toolchain="$base/toolchains/go$go_version-$os-$arch-$go_hash"
    if [ ! -x "$toolchain/bin/go" ]; then
        printf 'Downloading private Go %s toolchain…\n' "$go_version" >&3
        fetch "https://go.dev/dl/go$go_version.$os-$arch.tar.gz" "$stage/go.tar.gz"
        verify "$stage/go.tar.gz" "$go_hash"
        tar -xzf "$stage/go.tar.gz" -C "$stage"
        [ -x "$stage/go/bin/go" ] || fail 'Go archive is missing its executable'
        mv "$stage/go" "$toolchain"
    fi
    fetch "https://codeload.github.com/nanolathe-gg/nanolathe/tar.gz/$revision" "$stage/source.tar.gz"
    verify "$stage/source.tar.gz" "$source_hash"
    mkdir "$stage/source" "$stage/release"
    tar -xzf "$stage/source.tar.gz" -C "$stage/source"
    local source="$stage/source/nanolathe-$revision"
    [ -f "$source/go.sum" ] || fail 'source archive is missing go.sum'
    export CGO_ENABLED=0 GOTOOLCHAIN=local GOPATH="$base/cache/gopath" GOMODCACHE="$base/cache/gopath/pkg/mod" GOCACHE="$base/cache/go-build" GOENV=off
    export GOROOT="$toolchain" GOWORK=off GOFLAGS= GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org GOPRIVATE= GONOSUMDB= GONOPROXY=
    unset GOOS GOARCH GOAMD64 GOARM64 GOEXPERIMENT
    printf 'Building Nanolathe %s (the first build can take several minutes)…\n' "$version" >&3
    (cd "$source" && "$toolchain/bin/go" build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w' -o "$stage/release/nanolathe" ./cmd/nanolathe)
    "$stage/release/nanolathe" --help
    if [ -n "$root_arg" ]; then
        root_arg=$(cd -- "$root_arg" && pwd -P)
        "$stage/release/nanolathe" --check-install --root "$root_arg"
    fi
    cp "$stage/release.txt" "$stage/release/release.txt"
    # A unique directory avoids modifying the running release when reinstalling.
    release="$base/releases/$version-$revision-$(date +%Y%m%d%H%M%S)-$$"
    write_launcher "$stage/release/launch.sh"
    mv "$stage/release" "$release"
    ln -s "$release" "$stage/current"
    if [ -n "$root_arg" ]; then printf '%s\n' "$root_arg" > "$stage/game-root"; mv -f "$stage/game-root" "$base/game-root"; fi
    # This stable entry point follows the atomically switched release.
    cat > "$stage/launch.sh" <<'ENTRY'
#!/bin/bash
set -e
base=$(cd "$(dirname "$0")" && pwd -P)
exec /bin/bash "$base/current/launch.sh" "$base" "$@"
ENTRY
    chmod +x "$stage/launch.sh"
    mv -f "$stage/launch.sh" "$base/launch.sh"
    create_shortcut
    replace_pointer "$stage/current" "$base/current"
    printf 'Installed %s. Launcher: %s\n' "$version" "$base/launch.sh" >&3
}

write_launcher() {
    cat > "$1" <<'LAUNCHER'
#!/bin/bash
set -euo pipefail
umask 077
base=$1; shift
# Keep the installed executable selected if any update step fails.
release=$(cd "$base/current" && pwd -P)
binary="$release/nanolathe"
read_update_manifest() {
    local line key value seen='|'
    manifest_revision= manifest_installer= manifest_version=
    # Bash discards NUL bytes when reading; reject non-text before parsing.
    [ "$(wc -c < "$1")" -le 16384 ] || return 1
    [ "$(LC_ALL=C tr -d '\12\40-\176' < "$1" | wc -c)" -eq 0 ] || return 1
    while IFS= read -r line || [ -n "$line" ]; do
        case "$line" in *=*) ;; *) return 1 ;; esac
        key=${line%%=*}; value=${line#*=}
        case "$seen" in *"|$key|"*) return 1 ;; esac
        seen="$seen$key|"
        case "$key" in
            version) [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$ ]] || return 1; manifest_version=$value ;;
            source_revision) [[ "$value" =~ ^[0-9a-f]{40}$ ]] || return 1; manifest_revision=$value ;;
            go_version) [[ "$value" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1 ;;
            installer_sh_sha256|installer_ps1_sha256|source_tar_sha256|source_zip_sha256|go_darwin_arm64_sha256|go_darwin_amd64_sha256|go_linux_arm64_sha256|go_linux_amd64_sha256|go_windows_amd64_sha256)
                [[ "$value" =~ ^[0-9a-f]{64}$ ]] || return 1
                if [ "$key" = installer_sh_sha256 ]; then manifest_installer=$value; fi ;;
            *) return 1 ;;
        esac
    done < "$1"
    for key in version source_revision go_version source_tar_sha256 source_zip_sha256 go_darwin_arm64_sha256 go_darwin_amd64_sha256 go_linux_arm64_sha256 go_linux_amd64_sha256 go_windows_amd64_sha256; do
        case "$seen" in *"|$key|"*) ;; *) return 1 ;; esac
    done
}
offer_update() (
    # A subshell owns temporary files and terminal descriptors, including on failure.
    update_stage=$(mktemp -d "$base/.update-XXXXXXXX") || return 1
    trap 'rm -rf "$update_stage"' EXIT
    curl --disable --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --silent \
        --connect-timeout 2 --max-time 3 --max-filesize 16384 \
        --output "$update_stage/release.txt" https://nanolathe.gg/install/release.txt 2>/dev/null || return 1
    read_update_manifest "$release/release.txt" || return 1
    installed_revision=$manifest_revision
    read_update_manifest "$update_stage/release.txt" || return 1
    [ -n "$manifest_installer" ] && [ "$manifest_revision" != "$installed_revision" ] || return 1
    answer=
    if { exec 6<> /dev/tty; } 2>/dev/null; then
        printf '\nNanolathe %s has an update. Update & play? [y/N] (N: Play current version): ' "$manifest_version" >&6
        IFS= read -r answer <&6 || return 1
        exec 6>&-
        case "$answer" in y|Y|yes|YES) ;; *) return 1 ;; esac
    elif [ "$(uname -s)" = Darwin ]; then
        answer=$(/usr/bin/osascript - "$manifest_version" <<'APPLE' 2>/dev/null
on run argv
    set choice to display dialog ("Nanolathe " & item 1 of argv & " has an update. Building it may take several minutes.") with title "Nanolathe" buttons {"Play current version", "Update & play"} default button "Play current version" cancel button "Play current version"
    return button returned of choice
end run
APPLE
) || return 1
        [ "$answer" = 'Update & play' ] || return 1
    else
        return 1
    fi
    printf 'Downloading the Nanolathe updater…\n'
    if ! curl --disable --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --silent --show-error \
        --connect-timeout 5 --max-time 60 --max-filesize 1048576 \
        --output "$update_stage/install.sh" https://nanolathe.gg/install.sh; then
        printf 'Update download failed; playing the current version.\n' >&2; return 1
    fi
    if command -v shasum >/dev/null; then
        actual=$(shasum -a 256 < "$update_stage/install.sh") || return 1
    elif command -v sha256sum >/dev/null; then
        actual=$(sha256sum < "$update_stage/install.sh") || return 1
    else
        return 1
    fi
    if [ "${actual%% *}" != "$manifest_installer" ]; then
        printf 'Update checksum failed; playing the current version.\n' >&2; return 1
    fi
    if ! NANOLATHE_INSTALL_DIR="$base" /bin/bash "$update_stage/install.sh" --no-run; then
        printf 'Update failed; playing the current version.\n' >&2; return 1
    fi
)
if [ "${NANOLATHE_SKIP_UPDATE_CHECK:-}" = 1 ]; then
    unset NANOLATHE_SKIP_UPDATE_CHECK
elif offer_update; then
    NANOLATHE_SKIP_UPDATE_CHECK=1 exec /bin/bash "$base/launch.sh" "$@"
fi
log="$base/logs/run-$(date +%Y%m%d-%H%M%S)-$$.log"
export NANOLATHE_SETTINGS="$base/settings.json"
root= explicit=false desktop=false
if [ "${1:-}" = --desktop ]; then desktop=true; shift; fi
if [ "${1:-}" = --root ]; then
    [ "$#" -ge 2 ] || { echo '--root requires a directory' >&2; exit 1; }
    root=$2; explicit=true; shift 2
fi
notice() {
    printf '%s\n' "$1" >&2
    if [ "$(uname -s)" = Darwin ] && [ ! -t 2 ]; then
        /usr/bin/osascript - "$1" <<'APPLE' >/dev/null 2>&1 || true
on run argv
    display dialog (item 1 of argv) with title "Nanolathe" buttons {"OK"} default button "OK"
end run
APPLE
    fi
}
runtime_exit() {
    status=$?
    if [ "$status" -ne 0 ]; then
        notice "Nanolathe could not start. Details: $log"
        if [ "$desktop" = true ] && { exec 5<> /dev/tty; } 2>/dev/null; then
            printf 'Press Enter to close this window.' >&5
            IFS= read -r answer <&5 || true
        fi
    fi
}
trap runtime_exit EXIT
valid_root() { [ -d "$1" ] && "$binary" --check-install --root "$1" >> "$log" 2>&1; }
if [ "$explicit" = false ] && [ -f "$base/game-root" ]; then IFS= read -r root < "$base/game-root" || true; fi
if [ -n "$root" ] && ! valid_root "$root"; then
    if [ "$explicit" = true ]; then exit 1; fi
    root=
fi
if [ -z "$root" ]; then
    candidates=()
    # A failing discovery is expected when no game data has been installed.
    found=$("$binary" --list-installs 2>> "$log") || found=
    while IFS= read -r candidate; do [ -z "$candidate" ] || candidates[${#candidates[@]}]=$candidate; done <<< "$found"
    if [ "${#candidates[@]}" -eq 1 ] && valid_root "${candidates[0]}"; then root=${candidates[0]}; fi
    while [ -z "$root" ]; do
        use_tty=false
        if { exec 5<> /dev/tty; } 2>/dev/null; then use_tty=true; fi
        if [ "$(uname -s)" = Darwin ] && [ "$use_tty" = false ]; then
            root=$(/usr/bin/osascript - ${candidates[@]+"${candidates[@]}"} <<'APPLE'
on run argv
    if (count of argv) > 1 then
        set choices to argv & {"Choose another folder…"}
        set picked to choose from list choices with title "Nanolathe" with prompt "Choose one Total Annihilation installation:"
        if picked is false then error number -128
        if item 1 of picked is not "Choose another folder…" then return item 1 of picked
    end if
    return POSIX path of (choose folder with prompt "Choose your Total Annihilation folder (containing totala1.hpi):")
end run
APPLE
) || exit 1
        else
            # curl | bash owns stdin; interactive answers always use the terminal.
            if [ "$use_tty" = false ]; then
                notice 'No interactive terminal. Run the installer with --root DIRECTORY --no-run, then launch Nanolathe.'; exit 1
            fi
            printf '\nChoose one Total Annihilation installation.\n' >&5
            i=0
            while [ "$i" -lt "${#candidates[@]}" ]; do printf '  %s) %s\n' "$((i+1))" "${candidates[$i]}" >&5; i=$((i+1)); done
            printf 'Enter a number or the full folder path (blank cancels): ' >&5
            IFS= read -r answer <&5 || exit 1
            [ -n "$answer" ] || exit 1
            root=$answer
            if [[ "$answer" =~ ^[0-9]{1,6}$ ]]; then
                number=$((10#$answer))
                if [ "$number" -ge 1 ] && [ "$number" -le "${#candidates[@]}" ]; then root=${candidates[$((number-1))]}; fi
            fi
            exec 5>&-
        fi
        if ! valid_root "$root"; then notice 'That folder is not a usable Total Annihilation installation. Please choose another.'; root=; fi
    done
fi
case "$root" in *$'\n'*|*$'\r'*) notice 'Game folder names cannot contain newlines.'; exit 1 ;; esac
root=$(cd -- "$root" && pwd -P)
root_file=$(mktemp "$base/.game-root-XXXXXXXX")
printf '%s\n' "$root" > "$root_file"
mv -f "$root_file" "$base/game-root"
printf 'Nanolathe log: %s\n' "$log"
# Device/library errors go to this log.
"$binary" --root "$root" --save-dir "$base/saves" "$@" >> "$log" 2>&1
LAUNCHER
    chmod +x "$1"
}

create_shortcut() {
    if [ "$os" = darwin ]; then
        local app="$HOME/Applications/Nanolathe.app"
        mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
        printf '%s\n' "$base" > "$app/Contents/Resources/install-dir"
        cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>gg.nanolathe.game</string>
<key>CFBundleName</key><string>Nanolathe</string>
<key>CFBundleExecutable</key><string>Nanolathe</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
        cat > "$app/Contents/MacOS/Nanolathe" <<'APP'
#!/bin/bash
set -e
resources=$(cd "$(dirname "$0")/../Resources" && pwd -P)
IFS= read -r base < "$resources/install-dir"
exec /bin/bash "$base/launch.sh"
APP
        chmod +x "$app/Contents/MacOS/Nanolathe"
        if command -v codesign >/dev/null; then codesign --force --deep --sign - "$app" || printf 'Shortcut signing failed; use %s directly.\n' "$base/launch.sh" >&3; fi
        printf 'Application shortcut: %s\n' "$app" >&3
    else
        local menu="${XDG_DATA_HOME:-$HOME/.local/share}/applications" escaped
        mkdir -p "$menu"
        # Desktop Exec quoting has both string and command-line escape layers.
        escaped=$(printf '%s' "$base/launch.sh" | sed 's/\\/\\\\\\\\/g; s/"/\\\\"/g; s/`/\\\\`/g; s/\$/\\\\$/g; s/%/%%/g')
        cat > "$menu/nanolathe.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Nanolathe
Comment=Play Total Annihilation with your installed game data
Exec=/bin/bash "$escaped" --desktop
Terminal=true
Categories=Game;StrategyGame;
DESKTOP
        chmod 644 "$menu/nanolathe.desktop"
        printf 'Application menu shortcut: %s\n' "$menu/nanolathe.desktop" >&3
    fi
}
install_release >> "$log" 2>&1
# The install lock is released before the game runs.
rm -rf "$stage"; stage=
rmdir "$base/.install-lock"
trap - EXIT
if [ "$no_run" = false ]; then NANOLATHE_SKIP_UPDATE_CHECK=1 exec /bin/bash "$base/launch.sh"; fi
