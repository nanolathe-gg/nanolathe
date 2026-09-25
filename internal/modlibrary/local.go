package modlibrary

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// InstallLocal installs a package the player supplied rather than one the
// catalogue describes: a zip or a folder dropped on the window, or named by
// the desktop command's --install-mod (DESIGN_MODS_MUTATORS §4.5). It takes
// the same extraction path as a download (§5.3) and the standard content
// check against the base install, so a local package that would not start is
// never installed. baseRoots are the resolved base install roots, without
// any mod. The receipt records "local:<original name>" as the source.
func (l *Library) InstallLocal(path string, baseRoots []string) (Mod, error) {
	name := filepath.Base(filepath.Clean(path))
	info, err := os.Stat(path)
	if err != nil {
		return Mod{}, diagnostic("mod package is not readable", path, []string{name}, "a .zip file or a folder")
	}
	options := InstallOptions{Source: "local:" + name, Validate: ContentValidator(baseRoots)}
	if info.IsDir() {
		return l.InstallDirectory(path, options)
	}
	return l.InstallArchive(path, options)
}

// Select resolves a command-line mod selector (§4.3) against the installed
// library for a command that mounts it: `none` selects no mod (ok false), a
// missing version selects the newest installed one, and a mod that is not
// installed, or whose `requires` paths the base install does not resolve
// (§4.2), is an error naming what is missing. The mod carries its content
// profile's controls preset and gameplay minimum where its metadata names
// none (ResolveProfileDefaults). baseRoots are the resolved base install
// roots, without any mod.
func (l *Library) Select(selector string, baseRoots []string) (Mod, bool, error) {
	id, version, err := ParseSelector(selector)
	if err != nil || id == "" {
		return Mod{}, false, err
	}
	mod, ok, err := l.Lookup(id, version)
	if err != nil {
		return Mod{}, false, err
	}
	if !ok {
		return Mod{}, false, diagnostic("mod is not installed", selector, []string{l.Root}, "an installed mod (see --install-mod or the Mods & Mutators screen)")
	}
	if missing := UnmetRequirements(baseRoots, mod.Metadata); len(missing) > 0 {
		return Mod{}, false, diagnostic("mod "+selector+" requires content the base install does not supply", strings.Join(missing, ", "), baseRoots, "a base install that resolves every path the mod requires")
	}
	return ResolveProfileDefaults(baseRoots, mod), true, nil
}

// UnmetRequirements mounts the base install alone and lists the mod's
// `requires` paths it does not resolve (§4.2). A base that cannot be mounted
// resolves none of them.
func UnmetRequirements(baseRoots []string, meta Metadata) []string {
	if len(meta.Requires) == 0 {
		return nil
	}
	base := vfs.New()
	defer base.Close()
	if err := base.MountGameDirectories(baseRoots); err != nil {
		return MissingRequirements(nil, meta)
	}
	return MissingRequirements(base, meta)
}
