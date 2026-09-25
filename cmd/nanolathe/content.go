package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// contentSet is the mounted install plus the notes gathered while mounting.
type contentSet struct {
	// fs is the read view every content reader takes: the mounted overlay
	// with the resolved content profile's directory table applied, so a
	// loader keeps asking for `units/` whatever the content set spells it
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles"). A retail content
	// set has an empty table, and an empty table is the overlay itself.
	fs vfs.FSOps
	// unmappedMount is the concrete overlay, kept for the jobs that are about
	// what is on disk rather than about content — mounting an override,
	// listing providers for a diagnostic, closing — and for the two reader
	// surfaces still typed on the overlay (§5 names them). Reading a content
	// product through it bypasses the profile's directory table, so a read
	// here is wrong unless one of those cases applies.
	unmappedMount *vfs.FS
	// profile is the resolved content profile's name, for the reports.
	profile string
	// limits are the table sizes that profile compiles under: the size of the
	// unit-definition ID domain and of the weapon record table. Every compile
	// this command runs, and every session constructor it hands a filesystem
	// without a catalog, takes them, so the window admits exactly the content
	// the displayless command does (docs/DESIGN_CONTENT_VFS.md §5 "Content
	// profiles"). A retail content set resolves to the retail baseline.
	limits           content.Limits
	presentation     contentprofiles.Presentation
	gameplayFeatures []community.Overrides

	root         string
	roots        []string
	notes        []string
	translations *content.TranslationTable

	// mod is the selected installed mod mounted as the last root, or nil.
	// baseRoots are the roots without it, and manualRoots marks a command
	// line that stacked its own roots, which disables mod selection
	// (docs/DESIGN_MODS_MUTATORS.md §4.3).
	mod         *modlibrary.Mod
	baseRoots   []string
	manualRoots bool
	// savedMod marks a mod the saved choice selected rather than --mod. It
	// must never stop the game from starting (docs/DESIGN_MODS_MUTATORS.md
	// §4.3 "A missing mod at start").
	savedMod bool
	// modNotice is a one-line player-facing notice about the mod selection,
	// shown on the main menu (for example a saved mod that has gone).
	modNotice string
	// profileControls is the resolved content profile's recommended controls
	// preset. It is offered once when the profile is mounted without a mod
	// (docs/DESIGN_MODS_MUTATORS.md §4.3); a mod carries its own in mod.
	profileControls string
}

func (c *contentSet) Close() error {
	if c.unmappedMount == nil {
		return nil
	}
	return c.unmappedMount.Close()
}

// missingProductError is the standard diagnostic shape from
// AGENTS.md §Diagnostics: what failed, the logical path, the providers
// searched, and what was expected.
type missingProductError struct {
	what      string
	logical   string
	providers []string
	expected  string
}

func (e *missingProductError) Error() string {
	providers := "none"
	if len(e.providers) > 0 {
		providers = strings.Join(e.providers, ", ")
	}
	return fmt.Sprintf("nanolathe: %s: logical path %s, providers searched [%s], expected %s",
		e.what, e.logical, providers, e.expected)
}

// savedModError is a failure to open content with the mod the saved choice
// selected. It reads as its cause; the windowed start recognises it and
// starts without the mod instead (docs/DESIGN_MODS_MUTATORS.md §4.3 "A
// missing mod at start").
type savedModError struct {
	mod modlibrary.Mod
	err error
}

func (e *savedModError) Error() string { return e.err.Error() }
func (e *savedModError) Unwrap() error { return e.err }

// openContent mounts a retail install. The archives live at the install root;
// the loose gamedata directory on a real install is empty, so never probe for
// it on disk (PLAN_00 C6).
func openContent(opts Options) (*contentSet, error) {
	explicit := opts.Roots
	if len(explicit) == 0 && opts.Root != "" {
		explicit = []string{opts.Root}
	}
	roots, err := install.Resolve(explicit)
	if err != nil {
		return nil, err
	}
	selection, err := resolveModSelection(opts, explicit, roots)
	if err != nil {
		return nil, err
	}
	set, err := mountContent(opts, roots, selection)
	if err != nil && selection.saved {
		return nil, &savedModError{mod: *selection.mod, err: err}
	}
	return set, err
}

// mountContent mounts the resolved base roots plus the selected mod, if any,
// resolves the content profile and checks the required products.
func mountContent(opts Options, baseRoots []string, selection modSelection) (*contentSet, error) {
	roots := append([]string(nil), baseRoots...)
	if selection.mod != nil {
		roots = append(roots, selection.mod.Dir)
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, &missingProductError{
				what: "install root is not readable", logical: root,
				providers: roots,
				expected:  "a Total Annihilation content directory (set --root or $NANOLATHE_TA_ROOT)",
			}
		}
	}
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectories(roots); err != nil {
		fileSystem.Close()
		return nil, &missingProductError{what: "mounting content failed: " + err.Error(), logical: "<content roots>", providers: roots, expected: "readable content directories and archives"}
	}

	if opts.Remaster != "" {
		if err := mountRemaster(fileSystem, opts.Remaster); err != nil {
			fileSystem.Close()
			return nil, err
		}
	}
	// The content profile is resolved after mounting and before anything
	// reads content, because detection asks the mounted overlay for its
	// markers. Precedence is the explicit flag, then the saved preference,
	// then detection — the same order the displayless command follows
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles"). With a mod the
	// mod's own profile takes the saved preference's place.
	selector := opts.ContentProfile
	if strings.TrimSpace(selector) == "" && selection.mod != nil {
		// A selected mod names its profile explicitly, or means detection
		// when it names none, as a metadata-less local package does
		// (docs/DESIGN_MODS_MUTATORS.md §4.5). The saved preference never
		// applies another content set's directory table to a mod (D12).
		selector = selection.mod.ContentProfileSelector()
	} else if strings.TrimSpace(selector) == "" {
		stored, _ := settings.Load()
		selector = stored.ContentProfile
	}
	profile, err := contentprofiles.Resolve(fileSystem, selector)
	if err != nil {
		fileSystem.Close()
		return nil, err
	}
	mod := selection.mod
	if mod != nil {
		// A mod whose metadata names no controls preset or gameplay minimum,
		// such as a metadata-less local package, takes its content profile's
		// (docs/DESIGN_MODS_MUTATORS.md §4.3).
		withDefaults := mod.WithProfileDefaults(profile)
		mod = &withDefaults
	}
	set := &contentSet{
		fs:               profile.Layout().Apply(fileSystem),
		unmappedMount:    fileSystem,
		profile:          profile.Name,
		limits:           content.LimitsFromProfile(profile.Limits),
		presentation:     profile.Presentation,
		gameplayFeatures: profile.GameplaySources(),
		root:             roots[0], roots: append([]string(nil), roots...), notes: fileSystem.Notes(),
		mod: mod, baseRoots: append([]string(nil), baseRoots...), manualRoots: selection.manual, modNotice: selection.notice,
		savedMod: selection.saved, profileControls: profile.Controls,
	}

	// One required product proves the mount produced game data rather than an
	// empty directory. MOVEINFO.TDF and SIDEDATA.TDF are the hard requirements
	// [02 §1]; GAMEDATA.TDF is not — it does not exist in a real install
	// (docs/SPEC_CONFLICTS.md SC2). The probe goes through the profile view,
	// so a content set that ships `gamedata` under another name satisfies it.
	for _, required := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		if _, err := set.fs.Stat(required); err != nil {
			set.Close()
			return nil, &missingProductError{
				what:      "required content is missing",
				logical:   required,
				providers: fileSystem.ProviderIDs(),
				expected:  "a mounted archive or loose file supplying it",
			}
		}
	}
	// The modeled startup state selects retail's literal lowercase English
	// default before GUI parsing. TODO(T25): host command-line/registry
	// non-default language selection has not been integrated yet.
	translations, err := content.LoadTranslationTable(set.fs, "english")
	if err != nil {
		set.Close()
		return nil, fmt.Errorf("nanolathe: loading default GUI translation table: %w", err)
	}
	set.translations = translations

	// The modern renderer's material annotation is authored presentation data
	// (docs/DESIGN_GPU_RENDERER.md §29.1). An install may replace the embedded
	// table by supplying client.MaterialTablePath; a broken override is
	// reported and ignored, because presentation art must never fail a load.
	if err := client.LoadMaterialTable(set.fs); err != nil {
		set.notes = append(set.notes, err.Error())
		fmt.Fprintln(os.Stderr, err)
	}
	return set, nil
}

// contentProfileName is the resolved profile's name for a report. A benchmark
// or capture written without a mounted content set names none rather than
// claiming the retail profile.
func (c *contentSet) contentProfileName() string {
	if c == nil {
		return ""
	}
	return c.profile
}

// compileCatalog compiles the one immutable catalog under the resolved
// profile's limits. It is the command's own compile seam: a caller that needs
// a catalog before a session exists takes this rather than content.Compile,
// which would silently admit only what the retail tables hold
// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
func (c *contentSet) compileCatalog(report content.Progress) (*content.Catalog, error) {
	if c == nil || c.fs == nil {
		return nil, fmt.Errorf("nanolathe: catalog compile: no mounted content")
	}
	return content.CompileWithOptions(c.fs, content.Options{Limits: c.limits, Progress: report})
}

func (c *contentSet) loadGUI(name string) (*gui.Window, error) {
	if c == nil {
		return nil, fmt.Errorf("nanolathe: GUI load: no mounted content")
	}
	return gui.LoadWithTranslation(c.fs, name, c.translations)
}

// remasterPriority is the legacy minimum override priority. mountRemaster
// raises it above the highest root when the root list spans more tiers.
const remasterPriority = 1000

// mountRemaster mounts a user-supplied loose art override or packed archive.
// Art-only overrides leave the retail unit definitions unchanged.
func mountRemaster(fileSystem *vfs.FS, path string) error {
	priority := remasterPriority
	for _, provider := range fileSystem.Providers() {
		priority = max(priority, provider.Priority+1)
	}
	info, err := os.Stat(path)
	if err != nil {
		return &missingProductError{what: "remaster override is not readable", logical: path, expected: "a loose art directory or .hpi archive"}
	}
	if info.IsDir() {
		if err := fileSystem.MountDirectory(path, priority); err != nil {
			return fmt.Errorf("nanolathe: mounting remaster directory %s: %w", path, err)
		}
		return nil
	}
	if _, err := fileSystem.MountArchive(path, priority); err != nil {
		return fmt.Errorf("nanolathe: mounting remaster archive %s: %w", path, err)
	}
	return nil
}
