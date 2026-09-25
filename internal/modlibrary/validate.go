package modlibrary

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// requiredProducts are the logical files whose absence stops the desktop
// command at start-up. The list mirrors openContent in cmd/nanolathe, which
// owns it: MOVEINFO.TDF and SIDEDATA.TDF are the hard requirements, and
// GAMEDATA.TDF is not one (docs/SPEC_CONFLICTS.md SC2).
var requiredProducts = [...]string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"}

// isShippedProfile reports whether a contentProfile value names one of the
// embedded profiles rather than a JSON path inside the mod.
func isShippedProfile(name string) bool {
	for _, shipped := range contentprofiles.Names() {
		if strings.EqualFold(name, shipped) {
			return true
		}
	}
	return false
}

// profileSelector turns a mod's contentProfile into the explicit selector the
// content loader takes (§4.3, D12): a shipped name as written, a relative
// profile path joined to the mod root, and "" — detection — when omitted.
func profileSelector(dir string, meta Metadata) string {
	if meta.ContentProfile == "" || isShippedProfile(meta.ContentProfile) {
		return meta.ContentProfile
	}
	clean, err := cleanRelative(meta.ContentProfile)
	if err != nil || clean == "" {
		return meta.ContentProfile // Validate refused this already; let the lookup report it
	}
	return filepath.Join(dir, filepath.FromSlash(clean))
}

// ContentProfileSelector is the selector to pass as the explicit content
// profile when this mod is mounted. An empty result means detection, exactly
// as with no mod. It is explicit whenever the mod names a profile, so a saved
// contentProfile preference never applies one mod's table to another (D12).
func (m Mod) ContentProfileSelector() string { return profileSelector(m.Dir, m.Metadata) }

// ContentValidator is the standard §5.3 step 4 check for InstallOptions:
// mount the base install plus the staged root in a scratch overlay, resolve
// the mod's content profile, and require what the desktop command requires
// before it will start — the two hard-required game-data files through the
// profile's directory view, and a readable translation table. A mod that
// would not start is never installed. baseRoots are the resolved base
// install roots (install.Resolve), without any mod.
func ContentValidator(baseRoots []string) func(stagedRoot string, meta Metadata) error {
	base := append([]string(nil), baseRoots...)
	return func(stagedRoot string, meta Metadata) error {
		roots := append(append([]string(nil), base...), stagedRoot)
		fileSystem := vfs.New()
		defer fileSystem.Close()
		if err := fileSystem.MountGameDirectories(roots); err != nil {
			return diagnostic("mounting the mod for validation failed: "+err.Error(), "<content roots>", roots, "readable content directories and archives")
		}
		profile, err := contentprofiles.Resolve(fileSystem, profileSelector(stagedRoot, meta))
		if err != nil {
			return err
		}
		view := profile.Layout().Apply(fileSystem)
		for _, required := range requiredProducts {
			if _, err := view.Stat(required); err != nil {
				return diagnostic(fmt.Sprintf("mod %s@%s would not start: required content is missing", meta.ID, meta.Version), required, fileSystem.ProviderIDs(), "a mounted archive or loose file supplying it")
			}
		}
		if _, err := content.LoadTranslationTable(view, "english"); err != nil {
			return diagnostic(fmt.Sprintf("mod %s@%s would not start: the translation table does not load: %v", meta.ID, meta.Version, err), "gamedata/translate.tdf", fileSystem.ProviderIDs(), "a readable translation table or none")
		}
		return nil
	}
}

// MissingRequirements lists the mod's `requires` paths that the base install
// does not resolve, in the metadata's order (§4.2). A mod with any is listed
// but cannot be selected. base is the base install's mounted overlay, without
// the mod.
func MissingRequirements(base vfs.FSOps, meta Metadata) []string {
	var missing []string
	for _, required := range meta.Requires {
		if base == nil {
			missing = append(missing, required)
			continue
		}
		if _, err := base.Stat(required); err != nil {
			missing = append(missing, required)
		}
	}
	return missing
}
