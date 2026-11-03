package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/addonlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/internal/maplibrary"
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
	// profile is the mounted content's report name (`content_profile`):
	// the running config's id, or `retail` for content without one.
	profile string
	// limits are the table sizes that profile compiles under: the size of the
	// unit-definition ID domain and of the weapon record table. Every compile
	// this command runs, and every session constructor it hands a filesystem
	// without a catalog, takes them, so the window admits exactly the content
	// the displayless command does (docs/DESIGN_CONTENT_VFS.md §5 "Content
	// profiles"). A retail content set resolves to the retail baseline.
	limits content.Limits
	// profileLimits are the running config's own limits, before downloaded
	// map packages raise the terrain cap; an online room's limits start from
	// them (docs/DESIGN_MULTIPLAYER.md §16.8).
	profileLimits content.Limits
	// layout is the running config's directory table, which every view of
	// this content applies, an online room's included.
	layout           vfs.Layout
	presentation     contentprofiles.Presentation
	gameplayFeatures []community.Overrides
	// config is the running Nanolathe config: the mounted mod's own, or the
	// file --mod-config (or the saved contentProfile path) named; nil for
	// plain content. Its settings, keys and locks are exposed here for the
	// shell to layer; the mount applies only its content section and its
	// Community table (docs/DESIGN_MODS_MUTATORS.md §4.2).
	config *modlibrary.Config
	// configPath is the explicit config file this mount used, "" when the
	// config is a mod's own or there is none. A remount passes it back as
	// --mod-config so a manual stack keeps its config.
	configPath string

	root         string
	roots        []string
	notes        []string
	translations *content.TranslationTable
	// Reports follow the running set across saved-mod fallback and save-load
	// mod switches, without repeating providers for a set already reported.
	loadDuration    time.Duration
	startupReported bool
	// preview retains only immutable authored products for this mount. Every
	// staged battle owns its catalog clone and animation state.
	preview nlPreviewContent

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
	// shown on the main menu (for example a saved mod that has gone, or
	// content mounted without a Nanolathe config).
	modNotice string
	// configNotice is the plain-content notice when the mounted content has
	// no Nanolathe config (docs/DESIGN_MODS_MUTATORS.md §4.5), "" otherwise.
	// It is also modNotice unless the selection had its own notice to show.
	configNotice string
	// skippedMaps are downloaded map packages this mount left out, with the
	// reason; they stay removable from the More maps catalogue.
	skippedMaps []maplibrary.Skipped
	// mapRoots are the downloaded map packages this mount holds, below the
	// base install, in mount order.
	mapRoots []string
	// generated are the generated maps this mount holds from memory, below
	// everything else (docs/DESIGN_MAP_GENERATOR.md §10).
	generated []*generatedMap
	// addons are the official add-ons this mount holds from the library,
	// in the base install's folder at its *.UFO tier; addonSelection is what
	// the mount was asked for, which a remount asks for again; addonsOff
	// marks a running config that leaves them off
	// (docs/DESIGN_MODS_MUTATORS.md §5.7).
	addons         []addonlibrary.Addon
	addonSelection []settings.AddonSelection
	addonsOff      bool
	// profileControls is the controls preset of a config mounted without a
	// mod (--mod-config on a manual stack). It is offered once
	// (docs/DESIGN_MODS_MUTATORS.md §4.3); a mod carries its own in mod.
	profileControls string
}

// buildMenuPageSize is the running content's build page lock: a mounted
// mod's config, else the content section of the config a manual stack
// named (interface design §3.3 "Build page lock").
func (c *contentSet) buildMenuPageSize() int {
	if c == nil {
		return 0
	}
	if c.mod != nil && c.mod.BuildMenuPageSize > 0 {
		return c.mod.BuildMenuPageSize
	}
	return c.presentation.BuildMenuPageSize
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
	// baseRoots are the base install this mount resolved, which the start
	// without the mod mounts again rather than resolving it a second time.
	baseRoots []string
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
		return nil, &savedModError{mod: *selection.mod, err: err, baseRoots: roots}
	}
	return set, err
}

// mountContent mounts the resolved base roots plus the selected mod, if any,
// applies the running Nanolathe config's content section and checks the
// required products.
func mountContent(opts Options, baseRoots []string, selection modSelection) (*contentSet, error) {
	started := time.Now()
	config, err := resolveMountConfig(opts, selection)
	if err != nil {
		return nil, err
	}
	profile := config.meta.Content()
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
	// Downloaded maps never stop a start: a package that fails its mount-time
	// audit, or no longer validates against this base and mod stack, is left
	// out and named in a notice, and the rest still mount below the base
	// (docs/DESIGN_CONTENT_VFS.md "Downloaded community maps").
	var mapRoots, mapNotes []string
	var skippedMaps []maplibrary.Skipped
	if mapLibraryLayoutSupported(profile.Layout()) {
		installed, skipped, err := installedMapRoots(opts)
		if err != nil {
			mapNotes = append(mapNotes, "nanolathe: downloaded maps not mounted: "+noticeReason(err))
		}
		kept, rejected := maplibrary.SelectRoots(installed, roots)
		mapRoots, skippedMaps = kept, append(skipped, rejected...)
	}
	for _, s := range skippedMaps {
		mapNotes = append(mapNotes, mapSkipNote(s))
	}
	for _, note := range mapNotes {
		fmt.Fprintln(os.Stderr, note)
	}
	// Official add-ons mount in the base install's folder, at its *.UFO
	// tier, whatever mod is selected (docs/DESIGN_MODS_MUTATORS.md §5.7).
	addons, err := resolveAddons(opts, profile, baseRoots, selection.mod)
	if err != nil {
		return nil, err
	}
	printNotes(addons.notes)
	mapRootsCount := len(mapRoots)
	roots = append(mapRoots, roots...)
	gameRoots := make([]vfs.GameRoot, len(roots))
	for i, root := range roots {
		gameRoots[i] = vfs.GameRoot{Dir: root}
	}
	gameRoots[mapRootsCount].Extra = addonExtras(addons.mounted)
	gameRoots[mapRootsCount].ExtraLayout = addonExtraLayout(profile.Layout(), addons.factions, addons.models)
	gameRoots[mapRootsCount].ExtraTier = addonExtraTier(addons.factions, addons.models)
	fileSystem := vfs.New()
	if err := fileSystem.MountGameRoots(gameRoots); err != nil {
		fileSystem.Close()
		return nil, &missingProductError{what: "mounting content failed: " + err.Error(), logical: "<content roots>", providers: roots, expected: "readable content directories and archives"}
	}

	if opts.Remaster != "" {
		if err := mountRemaster(fileSystem, opts.Remaster); err != nil {
			fileSystem.Close()
			return nil, err
		}
	}
	if err := mountGeneratedMaps(fileSystem, opts.generatedMaps); err != nil {
		fileSystem.Close()
		return nil, err
	}
	// The Nanolathe config is chosen before anything reads content: its
	// content section is the directory table and limits every reader goes
	// through (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	mod := selection.mod
	if mod != nil && config.path != "" {
		// A config named on the command line stands in for the mod's own, so
		// a config can be tried against an installed mod before it is
		// packaged (docs/DESIGN_MODS_MUTATORS.md §4.3).
		withConfig := mod.WithConfig(config.meta)
		mod = &withConfig
	}
	notice := selection.notice
	if config.notice != "" && notice == "" {
		notice = config.notice
	}
	if config.notice != "" {
		fmt.Fprintln(os.Stderr, "nanolathe: "+config.notice)
	}
	set := &contentSet{
		fs:               profile.Layout().Apply(fileSystem),
		unmappedMount:    fileSystem,
		profile:          profile.Name,
		limits:           content.LimitsFromProfile(profile.Limits),
		profileLimits:    content.LimitsFromProfile(profile.Limits),
		layout:           profile.Layout(),
		presentation:     profile.Presentation,
		gameplayFeatures: config.meta.CommunitySources(),
		config:           config.meta.Config,
		configPath:       config.path,
		root:             baseRoots[0], roots: append([]string(nil), roots...), notes: fileSystem.Notes(),
		mod: mod, baseRoots: append([]string(nil), baseRoots...), manualRoots: selection.manual, modNotice: notice,
		configNotice: config.notice, savedMod: selection.saved, skippedMaps: skippedMaps,
		mapRoots:  append([]string(nil), mapRoots...),
		generated: append([]*generatedMap(nil), opts.generatedMaps...),
		addons:    addons.mounted, addonSelection: addons.selection, addonsOff: addons.off,
	}
	set.notes = append(set.notes, mapNotes...)
	set.notes = append(set.notes, addons.notes...)
	if mapRootsCount > 0 {
		set.limits.TNTBytes = max(set.limits.TNTBytes, maplibrary.MaxTNTBytes)
	}
	if mod == nil {
		set.profileControls = config.meta.Controls
	}
	if missing := profile.MissingMarkers(fileSystem); len(missing) > 0 {
		// The markers select nothing; a config paired with other content is
		// reported and mounted as asked, and the required-product check below
		// still decides whether the content can start.
		note := fmt.Sprintf("nanolathe: the %s config names content directory %s, which the mounted content lacks", profile.Name, missing[0])
		set.notes = append(set.notes, note)
		fmt.Fprintln(os.Stderr, note)
	}

	// One required product proves the mount produced game data rather than an
	// empty directory. MOVEINFO.TDF and SIDEDATA.TDF are the hard requirements
	// [02 §1]; GAMEDATA.TDF is not — it does not exist in a real install
	// (docs/SPEC_CONFLICTS.md SC2). The probe goes through the config's view,
	// so a content set that ships `gamedata` under another name satisfies it.
	for _, required := range []string{"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf"} {
		if _, err := set.fs.Stat(required); err != nil {
			// The providers are listed before the close, which empties the
			// mount: listed after it, every such failure read "[none]"
			// (issue #186).
			providers := fileSystem.ProviderIDs()
			set.Close()
			return nil, &missingProductError{
				what:      "required content is missing",
				logical:   required,
				providers: providers,
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
	set.loadDuration = time.Since(started)
	return set, nil
}

// mountConfig is the Nanolathe config one mount applies: the metadata that
// carries it (whose Config is nil for plain content, so its Content is the
// base game's profile and it declares no Community source), the explicit
// file it was read from, and a player-facing notice when content mounts
// without one.
type mountConfig struct {
	meta   modlibrary.Metadata
	path   string
	notice string
}

// resolveMountConfig chooses the config a mount applies
// (docs/DESIGN_MODS_MUTATORS.md §4.3): an explicit --mod-config file first;
// else the selected mod's own config; else, with no mod, the saved
// contentProfile preference when it names a config file. Content left
// without a config mounts as plain content — the retail layout and base
// limits — and says so: a mod without one, a manual root stack without
// one, and a saved preference that names a removed built-in profile. There
// is no detection.
func resolveMountConfig(opts Options, selection modSelection) (mountConfig, error) {
	if path := strings.TrimSpace(opts.ModConfig); path != "" {
		meta, err := modlibrary.ReadConfigFile(path)
		if err != nil {
			return mountConfig{}, err
		}
		return mountConfig{meta: meta, path: path}, nil
	}
	if mod := selection.mod; mod != nil {
		if !mod.HasConfig() {
			return mountConfig{notice: modlibrary.NoConfigNotice(mod.Name)}, nil
		}
		return mountConfig{meta: mod.Metadata}, nil
	}
	// The saved preference is the file --mod-config would name, and like
	// the removed content-profile preference it applies whenever no mod is
	// selected (a selected mod's own config always wins, D12).
	stored, _ := settings.Load()
	if saved := strings.TrimSpace(stored.ContentProfile); saved != "" {
		if modlibrary.IsRemovedProfile(saved) {
			if strings.EqualFold(saved, contentprofiles.RetailName) {
				return mountConfig{}, nil
			}
			return mountConfig{notice: fmt.Sprintf("The saved content profile %q was removed: mods carry their own Nanolathe config now. The content mounts without one and may not load correctly.", saved)}, nil
		}
		meta, err := modlibrary.ReadConfigFile(saved)
		if err != nil {
			return mountConfig{}, err
		}
		return mountConfig{meta: meta, path: saved}, nil
	}
	if selection.manual {
		return mountConfig{notice: "This root stack has no Nanolathe config file; it may not load correctly. Name one with --mod-config."}, nil
	}
	return mountConfig{}, nil
}

// contentProfileName is the mounted content's report name: the running
// config's id, or `retail`. A benchmark or capture written without a mounted
// content set names none rather than claiming the retail profile.
func (c *contentSet) contentProfileName() string {
	if c == nil {
		return ""
	}
	return c.profile
}

// compileCatalog compiles the one immutable catalog under the running
// config's content limits. It is the command's own compile seam: a caller that needs
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
