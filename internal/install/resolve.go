// Package install finds host game directories before the VFS is mounted.
// Discovery is Nanolathe convenience policy, not recovered retail behavior.
package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type host struct {
	systemRoots                     []string
	home, cwd, executable, platform string
	getenv                          func(string) string
	registryRoots, registrySteam    []string
}

// Resolve preserves explicit root order and otherwise uses NANOLATHE_TA_ROOT
// or discovers installations in likely host locations. Discovery recognizes a
// totala1.hpi file; archive validation remains the mount layer's responsibility.
// It searches all candidates, in deterministic order, without a recursive disk
// scan: registered and standard installations first, nearby portable copies
// next, and ~/TotalAnnihilation last. Arbitrarily relocated installations still
// require --root.
func Resolve(explicit []string) ([]string, error) {
	// Overrides must not depend on probing the host (including its registry).
	if len(explicit) > 0 {
		return explicitRoots(explicit)
	}
	if root := os.Getenv("NANOLATHE_TA_ROOT"); root != "" {
		return explicitRoots([]string{root})
	}
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	executable, _ := os.Executable()
	roots, steam := registryLocations()
	h := host{home: home, cwd: cwd, executable: executable, platform: runtime.GOOS, getenv: os.Getenv, registryRoots: roots, registrySteam: steam}
	h.systemRoots = systemLocations()
	return resolve(nil, h)
}

func explicitRoots(roots []string) ([]string, error) {
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("nanolathe: empty install root: logical path totala1.hpi, providers searched [%s], expected a nonempty --root directory", strings.Join(roots, ", "))
		}
	}
	return append([]string(nil), roots...), nil
}

func resolve(explicit []string, h host) ([]string, error) {
	if len(explicit) > 0 {
		return explicitRoots(explicit)
	}
	if root := h.getenv("NANOLATHE_TA_ROOT"); root != "" {
		return explicitRoots([]string{root})
	}
	d := discovery{platform: h.platform, seen: make(map[string]bool), steamSeen: make(map[string]bool)}
	for _, root := range h.registryRoots {
		d.game(root)
	}
	for _, steam := range h.registrySteam {
		d.steam(steam)
	}
	if h.home != "" {
		for _, name := range []string{"Games", "games", "GOG Games"} {
			d.collection(filepath.Join(h.home, name))
		}
	}
	switch h.platform {
	case "windows":
		for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
			if p := h.getenv(name); p != "" {
				d.windowsPrograms(p)
			}
		}
		// Probe conventional directories on fixed drives only.
		for _, drive := range h.systemRoots {
			d.windowsDrive(drive)
		}
	case "darwin":
		for _, root := range h.systemRoots {
			d.collection(root)
		}
		if h.home != "" {
			d.steam(filepath.Join(h.home, "Library/Application Support/Steam"))
			d.collection(filepath.Join(h.home, "Applications"))
			d.prefixCollection(filepath.Join(h.home, "Library/Application Support/CrossOver/Bottles"))
			d.prefixCollection(filepath.Join(h.home, "Library/Containers/com.isaacmarovitz.Whisky/Bottles"))
		}
	default:
		data := h.getenv("XDG_DATA_HOME")
		if data == "" && h.home != "" {
			data = filepath.Join(h.home, ".local/share")
		}
		if h.home != "" {
			d.steam(filepath.Join(h.home, ".steam/steam"))
			d.steam(filepath.Join(h.home, ".steam/root"))
		}
		if data != "" {
			d.steam(filepath.Join(data, "Steam"))
		}
		if h.home != "" {
			d.steam(filepath.Join(h.home, ".var/app/com.valvesoftware.Steam/.local/share/Steam"))
		}
		if data != "" {
			d.prefixCollection(filepath.Join(data, "lutris/prefixes"))
			d.prefixCollection(filepath.Join(data, "bottles/bottles"))
		}
		if h.home != "" {
			d.prefixCollection(filepath.Join(h.home, ".var/app/com.usebottles.bottles/data/bottles/bottles"))
			d.prefixCollection(filepath.Join(h.home, ".cxoffice"))
		}
	}
	if h.platform != "windows" {
		if p := h.getenv("WINEPREFIX"); p != "" {
			d.prefix(p)
		}
		if h.home != "" {
			d.prefix(filepath.Join(h.home, ".wine"))
			d.prefixCollection(filepath.Join(h.home, "Games"))
			d.prefixCollection(filepath.Join(h.home, "games"))
		}
		for _, p := range filepath.SplitList(h.getenv("CX_BOTTLE_PATH")) {
			if p != "" {
				d.prefixCollection(p)
			}
		}
	}
	// Registered and standard installations precede portable copies. The home
	// data directory remains the final convenience location on every platform.
	d.near(h.cwd)
	if h.executable != "" {
		d.near(filepath.Dir(h.executable))
		if executable, err := filepath.EvalSymlinks(h.executable); err == nil {
			d.near(filepath.Dir(executable))
		}
	}
	if h.home != "" {
		d.game(filepath.Join(h.home, "TotalAnnihilation"))
	}
	if len(d.roots) == 0 {
		return nil, fmt.Errorf("nanolathe: Total Annihilation installation not found: logical path totala1.hpi, providers searched [%s], expected a Total Annihilation installation; supply --root <directory> or NANOLATHE_TA_ROOT", strings.Join(d.searched, ", "))
	}
	return d.roots, nil
}

type discovery struct {
	rootInfo        []os.FileInfo
	platform        string
	seen, steamSeen map[string]bool
	searched, roots []string
}

func (d *discovery) key(path string) string {
	path = filepath.Clean(path)
	if d.platform == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func (d *discovery) game(path string) {
	if path == "" {
		return
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return
	}
	key := d.key(absolute)
	if d.seen[key] {
		return
	}
	d.seen[key] = true
	d.searched = append(d.searched, absolute)
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.EqualFold(entry.Name(), "totala1.hpi") {
			continue
		}
		info, err := os.Stat(filepath.Join(absolute, entry.Name()))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			canonical = absolute
		}
		rootInfo, err := os.Stat(absolute)
		if err != nil {
			return
		}
		for _, earlier := range d.rootInfo {
			if os.SameFile(earlier, rootInfo) {
				return
			}
		}
		d.rootInfo = append(d.rootInfo, rootInfo)
		d.roots = append(d.roots, canonical)
		return
	}
}

var gameNames = []string{"TotalAnnihilation", "Total Annihilation", "Total Annihilation Commander Pack", "Total Annihilation - Commander Pack", "Total Annihilation - Commander Pack (English)", "total-annihilation", "TotalA", "TA"}

func (d *discovery) near(base string) {
	if base == "" {
		return
	}
	d.game(base)
	for _, name := range gameNames {
		d.game(childPath(base, name))
	}
}

// collection visits one level only. This also accepts renamed game folders.
func (d *discovery) collection(base string) {
	if base == "" {
		return
	}
	d.near(base)
	for _, child := range directories(base) {
		d.game(child)
	}
}

func directories(base string) []string {
	entries, _ := os.ReadDir(base)
	var paths []string
	for _, entry := range entries {
		path := filepath.Join(base, entry.Name())
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			paths = append(paths, path)
		}
	}
	return paths
}

// childPath accepts Windows directory casing inside case-sensitive Wine hosts.
func childPath(base, relative string) string {
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		next := filepath.Join(base, part)
		entries, _ := os.ReadDir(base)
		// Prefer an exact match on hosts that can contain both spellings.
		for _, entry := range entries {
			if entry.Name() == part {
				next = filepath.Join(base, entry.Name())
				goto matched
			}
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), part) {
				next = filepath.Join(base, entry.Name())
				break
			}
		}
	matched:
		base = next
	}
	return base
}

func (d *discovery) windowsPrograms(base string) {
	d.near(base)
	d.near(childPath(base, "Cavedog"))
	d.collection(childPath(base, "GOG Galaxy/Games"))
	d.collection(childPath(base, "GOG.com"))
	d.steam(filepath.Join(base, "Steam"))
}

func (d *discovery) windowsDrive(base string) {
	d.near(base)
	d.near(childPath(base, "Cavedog"))
	d.collection(childPath(base, "GOG Games"))
	d.collection(childPath(base, "Games"))
	for _, name := range []string{"Program Files", "Program Files (x86)"} {
		d.windowsPrograms(childPath(base, name))
	}
	d.steam(filepath.Join(base, "Steam"))
	d.steam(filepath.Join(base, "SteamLibrary"))
}

func (d *discovery) prefix(base string) {
	drive := filepath.Join(base, "drive_c")
	if info, err := os.Stat(drive); err == nil && info.IsDir() {
		d.windowsDrive(drive)
	}
}

func (d *discovery) prefixCollection(base string) {
	for _, prefix := range directories(base) {
		d.prefix(prefix)
	}
}

func (d *discovery) steam(base string) {
	if base == "" {
		return
	}
	canonical, err := filepath.EvalSymlinks(base)
	if err != nil {
		canonical = filepath.Clean(base)
	}
	key := d.key(canonical)
	if d.steamSeen[key] {
		return
	}
	d.steamSeen[key] = true
	// Config entries are queued locally; they are not followed recursively.
	libraries := []string{base}
	for _, config := range []string{"steamapps/libraryfolders.vdf", "config/libraryfolders.vdf"} {
		libraries = append(libraries, libraryPaths(filepath.Join(base, config))...)
	}
	for _, library := range libraries {
		d.collection(filepath.Join(library, "steamapps/common"))
		// Proton prefixes can contain an independently installed Windows copy.
		for _, compat := range directories(filepath.Join(library, "steamapps/compatdata")) {
			d.prefix(filepath.Join(compat, "pfx"))
		}
	}
}
