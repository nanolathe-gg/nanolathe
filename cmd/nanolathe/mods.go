package main

// The mod library, mutators and the Mods & Mutators screen, per
// docs/DESIGN_MODS_MUTATORS.md §4 (library and selection), §5 (catalogue),
// §6 (mutators) and §8 (screens). Save sidecars (§7) live in save_sidecar.go.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/install"
	"github.com/nanolathe-gg/nanolathe/internal/modfetch"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// ---------------------------------------------------------------------------
// Selection at the mount boundary (§4.3).

// modSelection is what the mount boundary settled on: the mod to append as
// the last root (nil for none), whether the saved choice rather than --mod
// selected it, whether the command line stacked its own roots, and a
// one-line notice for the main menu.
type modSelection struct {
	mod    *modlibrary.Mod
	saved  bool
	manual bool
	notice string
}

func openModLibrary() (*modlibrary.Library, error) {
	root, err := modlibrary.DefaultRoot()
	if err != nil {
		return nil, err
	}
	return modlibrary.Open(root)
}

func modSelectorOf(id, version string) string {
	if id == "" {
		return "none"
	}
	if version == "" {
		return id
	}
	return id + "@" + version
}

func modSettingSelector(s settings.ModSelection) string { return modSelectorOf(s.ID, s.Version) }

// ignoresSavedSelection reports a capture, benchmark or displayless run.
// Those take the mod and the mutators from their own command line alone,
// never from the saved choice, so a run reproduces from its flags
// (docs/DESIGN_MODS_MUTATORS.md §4.3, §6.6).
func (o Options) ignoresSavedSelection() bool {
	return o.Headless || o.Shot != "" || o.ShotModel != "" || o.ShotDebris != "" || o.Film != "" || o.BattleBenchmark != ""
}

// resolveModSelection applies §4.3's precedence: several explicit roots are
// a manual stack that disables selection; otherwise the --mod flag wins over
// the saved choice, which the non-interactive modes ignore. A flag naming a
// missing mod, or one whose base requirements (§4.2) the install does not
// meet, fails; a saved choice naming one starts without a mod and says so.
// base is the resolved base install.
func resolveModSelection(opts Options, explicit, base []string) (modSelection, error) {
	if len(explicit) >= 2 && !opts.modBaseRoots {
		if opts.ModSet {
			if id, _, _ := modlibrary.ParseSelector(opts.Mod); id != "" {
				return modSelection{}, &missingProductError{what: "mod selection conflicts with a manual root stack", logical: "<command line>", providers: explicit, expected: "either several --root flags or one --mod"}
			}
		}
		return modSelection{manual: true}, nil
	}
	selector, fromFlag := opts.Mod, opts.ModSet
	if !fromFlag {
		if opts.ignoresSavedSelection() {
			return modSelection{}, nil
		}
		stored, _ := settings.Load()
		selector = modSettingSelector(stored.Mod)
	}
	id, version, err := modlibrary.ParseSelector(selector)
	if err != nil {
		if fromFlag {
			return modSelection{}, err
		}
		return modSelection{notice: "Saved mod choice is unreadable"}, nil
	}
	if id == "" {
		return modSelection{}, nil
	}
	lib, err := openModLibrary()
	if err != nil {
		if fromFlag {
			return modSelection{}, err
		}
		return modSelection{notice: "Mod library is unavailable"}, nil
	}
	mod, ok, err := lib.Lookup(id, version)
	if err != nil || !ok {
		if fromFlag {
			return modSelection{}, &missingProductError{what: "mod is not installed", logical: selector, providers: []string{lib.Root}, expected: "an installed mod (see --install-mod or the Mods & Mutators screen)"}
		}
		return modSelection{notice: fmt.Sprintf("Mod %s is not installed", selector)}, nil
	}
	if missing := unmetModRequirements(base, mod); len(missing) > 0 {
		if fromFlag {
			return modSelection{}, &missingProductError{what: "mod " + selector + " requires content the base install does not supply", logical: strings.Join(missing, ", "), providers: base, expected: "a base install that resolves every path the mod requires"}
		}
		return modSelection{notice: fmt.Sprintf("Mod %s needs %s from the base install", selector, missing[0])}, nil
	}
	return modSelection{mod: &mod, saved: !fromFlag}, nil
}

// unmetModRequirements lists the mod's `requires` paths that the base
// install alone does not resolve (§4.2), the check the Mods & Mutators screen
// makes before it lets a mod be selected. A base that cannot be mounted
// resolves none of them.
func unmetModRequirements(base []string, mod modlibrary.Mod) []string {
	return modlibrary.UnmetRequirements(base, mod.Metadata)
}

// startWithoutSavedMod reopens a start whose SAVED mod failed to open, build
// or bind (cause) with no mod, and names the mod and the reason on the main
// menu, as a saved mod that has gone does (§4.3 "A missing mod at start"):
// start-up never fails because of the saved choice. The saved setting is
// kept. launch is the command line as given; its roots are the base install.
func startWithoutSavedMod(launch Options, mod modlibrary.Mod, cause error) (*contentSet, error) {
	launch.Mod, launch.ModSet = "none", true
	fresh, err := openContent(launch)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "nanolathe: the saved mod %s did not start, so the game starts without it: %v\n", modSelectorOf(mod.ID, mod.Version), cause)
	fresh.modNotice = fmt.Sprintf("%s %s did not start: %s", mod.Name, mod.Version, noticeReason(cause))
	return fresh, nil
}

// noticeReason shortens a diagnostic to the failure and the logical path it
// names, for a one-line notice; the full diagnostic goes to standard error.
func noticeReason(err error) string {
	text := strings.TrimPrefix(err.Error(), "nanolathe: ")
	if what, rest, ok := strings.Cut(text, ": logical path "); ok {
		logical, _, _ := strings.Cut(rest, ", providers searched")
		return what + " (" + logical + ")"
	}
	return text
}

// modSelectorFor names the running mod for a restart or a remount.
func (c *contentSet) modSelector() string {
	if c == nil || c.mod == nil {
		return "none"
	}
	return modSelectorOf(c.mod.ID, c.mod.Version)
}

// runInstallMod is --install-mod: a command-line stand-in for dropping a zip
// or folder on the window (§4.5).
func runInstallMod(opts Options, out *os.File) error {
	explicit := opts.Roots
	if len(explicit) == 0 && opts.Root != "" {
		explicit = []string{opts.Root}
	}
	base, err := install.Resolve(explicit)
	if err != nil {
		return err
	}
	lib, err := openModLibrary()
	if err != nil {
		return err
	}
	mod, err := lib.InstallLocal(opts.InstallMod, base)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "installed %s (%s) in %s\n", modSelectorOf(mod.ID, mod.Version), mod.Name, mod.Dir)
	return err
}

// ---------------------------------------------------------------------------
// Mutators (§6).

// resolveStartupMutators applies §6.6: --mutator flags win over the saved
// set, which the non-interactive modes ignore.
func resolveStartupMutators(opts Options) (content.Mutators, error) {
	if len(opts.MutatorArgs) > 0 {
		return content.ParseMutators(opts.MutatorArgs)
	}
	if opts.ignoresSavedSelection() {
		return content.Mutators{}, nil
	}
	stored, _ := settings.Load()
	m, err := content.ParseMutators(stored.Mutators)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: ignoring saved mutators: %v\n", err)
		return content.Mutators{}, nil
	}
	return m, nil
}

// mutatorText is ASCII because the retail fonts carry no multiplication sign.
func mutatorText(label string, f content.Factor) string { return label + " x" + f.String() }

func mutatorSummary(m content.Mutators) string { return strings.Join(m.DescribeASCII(), ", ") }

func mutatorCount(m content.Mutators) int { return len(m.DescribeASCII()) }

// ---------------------------------------------------------------------------
// Gameplay minimum (§4.3).

func modMinimumGameplay(mod *modlibrary.Mod) (gameplay.Mode, bool) {
	if mod == nil || mod.MinimumGameplay == "" {
		return "", false
	}
	mode, err := gameplay.Parse(mod.MinimumGameplay)
	if err != nil {
		return "", false
	}
	return mode, true
}

func gameplayBelow(mode, minimum gameplay.Mode) bool {
	return gameplayOptionStage(mode) < gameplayOptionStage(minimum)
}

// gameplayLabel is the options control's caption for a reserved layer.
func gameplayLabel(mode gameplay.Mode) string {
	return [...]string{"Strict 3.1", "Community 3.9", "Modern"}[gameplayOptionStage(mode)]
}

// enforceModGameplayMinimum raises a selection below the running mod's
// minimum and says so on the main menu; nothing changes silently (D8).
func (g *gameShell) enforceModGameplayMinimum() {
	if g == nil || g.cs == nil {
		return
	}
	minimum, ok := modMinimumGameplay(g.cs.mod)
	if !ok || !gameplayBelow(g.gameplay, minimum) {
		return
	}
	g.setGameplay(minimum)
	g.cs.modNotice = fmt.Sprintf("%s requires %s or Modern: gameplay set to %s", g.cs.mod.Name, gameplayLabel(minimum), gameplayLabel(minimum))
}

// ---------------------------------------------------------------------------
// Content reload (§4.4), in process.

// contentReloadRequest is a mod switch the Mods & Mutators screen applied.
// It carries the whole pending selection, so the running shell and the
// settings file stay as they are until the new shell is bound: the request is
// applied to the new shell, and a reload that fails changes nothing (§4.4).
type contentReloadRequest struct {
	selector string                // the mod to mount, "none" for no mod
	mod      settings.ModSelection // the saved choice the switch writes
	mutators content.Mutators
	gameplay gameplay.Mode // the gameplay selection raised to the mod's minimum, "" to keep it
	controls string        // the controls preset to write once (§4.3, P10), "" for none
	// offered is the mod whose preset the switch offered, accepted or not;
	// the main menu does not offer it again (§4.3).
	offered string
	// loadSave is a save to load once the new content is bound: a game saved
	// under another mod switches to it first (§7.3 step 2).
	loadSave string
}

// pendingContentReload is the switch a screen asked for. The window loop
// performs it after the current step returns, never inside one, so no shell
// is replaced while its own code is running.
var pendingContentReload *contentReloadRequest

// shellHost is what the window loop's callbacks hold instead of the shell
// itself, so a reload can swap the shell between two steps.
type shellHost struct{ shell *gameShell }

func (h *shellHost) step(delta float64, cl *client.Client) {
	h.shell.step(delta, cl)
	if pendingContentReload != nil {
		request := *pendingContentReload
		pendingContentReload = nil
		h.reload(request, cl)
	}
}

// reload mounts the base install plus the chosen mod, builds a fresh shell on
// it, applies the pending selection to that shell and rebinds the client, all
// before anything of the running shell is released or the settings file is
// written. A failure at any step leaves the running shell and the settings
// file as they were, releases what the attempt acquired, and says why where
// the player is looking: on the Mods & Mutators screen, else on the main menu.
func (h *shellHost) reload(request contentReloadRequest, cl *client.Client) {
	old := h.shell
	// Loading a game leaves the battle it was loaded from, whatever mod
	// the game needs; leave it before the switch so neither shell's battle
	// holds the client while the other binds it.
	if request.loadSave != "" && old.battle != nil {
		old.teardownBattle(cl)
		old.bindFrontendClient(cl)
		old.openMenu(modeMenuMain)
	}
	fail := func(err error) {
		fmt.Fprintf(os.Stderr, "nanolathe: mod switch failed: %v\n", err)
		notice := "Mod switch failed: " + noticeReason(err)
		if modsUI != nil {
			modsUI.notice = notice
			old.refreshModsPanel()
			return
		}
		old.cs.modNotice = notice
		if p := old.activePanel(); p != nil && old.frontend.Mode == modeMenuMain {
			old.refreshMainMenuModStatus(p)
		}
	}
	opts := old.opts
	opts.Roots, opts.Mod, opts.ModSet, opts.modBaseRoots = old.cs.baseRoots, request.selector, true, true
	if len(opts.Roots) > 0 {
		opts.Root = opts.Roots[0]
	}
	// The running set's resolved profile must not follow it to another mod;
	// the new selection resolves its own (D12).
	opts.ContentProfile, opts.LoadSave, opts.Map = "", "", ""
	cs, err := openContent(opts)
	if err != nil {
		fail(err)
		return
	}
	// Opening the content installed its material annotation, which is
	// process-wide; a failure from here on puts the running content's back.
	abandon := func(err error) {
		_ = cs.Close()
		if matErr := client.LoadMaterialTable(old.cs.fs); matErr != nil {
			fmt.Fprintln(os.Stderr, matErr)
		}
		fail(err)
	}
	opts.Root, opts.Roots, opts.ContentProfile = cs.root, cs.roots, cs.profile
	shell, err := newGameShell(opts, cs)
	if err != nil {
		abandon(err)
		return
	}
	// The new shell starts from the running shell's live preferences, which
	// the settings file may not hold yet, and then takes the pending
	// selection.
	shell.applySettings(old.captureSettings())
	shell.settingsWritable = old.settingsWritable
	// The window belongs to the process, not to the content: the new shell
	// takes over the running shell's window state, which the window adapter
	// polls through the host from the next update.
	shell.fullscreen, shell.windowSize, shell.fpsVisible = old.fullscreen, old.windowSize, old.fpsVisible
	shell.modSetting = request.mod
	shell.opts.Mutators, shell.mutatorSetting = request.mutators, request.mutators.Map()
	if request.gameplay != "" {
		shell.setGameplay(request.gameplay)
	}
	shell.enforceModGameplayMinimum()
	shell.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	if err := bindShellContent(shell, cl); err != nil {
		_ = bindShellContent(old, cl)
		shell.releaseAudio()
		abandon(err)
		return
	}
	// Committed. The controls preset and the settings file follow the new
	// shell from here.
	if request.controls != "" {
		shell.applyControlsPreset(request.controls)
	}
	shell.markControlsOffered(request.offered)
	shell.saveSettings()
	// Release the old set only now: its dialogs, its voices and music, and
	// its archive handles.
	if mutatorsUI != nil {
		old.closeMutatorsDialog(true)
	}
	if modsFetchUI != nil {
		old.closeModsFetch()
	}
	if modsUI != nil {
		old.closeModsScreen()
	}
	if controlsOfferUI != nil {
		old.releaseControlsOffer()
	}
	optionsPanel, optionsAssets, optionsState = nil, nil, nil
	saveLoadUI, saveLoadPanel, saveLoadAssets = nil, nil, nil
	old.releaseAudio()
	_ = old.cs.Close()
	h.shell = shell
	if request.loadSave != "" {
		h.loadAfterReload(request.loadSave)
	}
}

// loadAfterReload loads the game a mod switch was made for. The new content
// is the recorded mod, so the load does not ask for another switch; if it
// somehow does, the request is dropped rather than repeated.
func (h *shellHost) loadAfterReload(path string) {
	err := h.shell.loadRetailSavePath(path)
	if err == nil && pendingContentReload != nil && pendingContentReload.loadSave == path {
		pendingContentReload = nil
		err = refuseLoad("This game's mod could not be selected")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: loading %s after the mod switch: %v\n", path, err)
		reportRetailMessageError(h.shell.showRetailMessage(loadFailureMessage(err)))
	}
}

// releaseAudio stops a shell's voices and closes its music. The audio service
// is the shell's own; nothing else holds it.
func (g *gameShell) releaseAudio() {
	g.stopOrdinaryAudio()
	if g.audioOwner != nil && g.audioOwner.Music != nil {
		g.audioOwner.Music.Close()
	}
}

// ---------------------------------------------------------------------------
// Main-menu button and status line (§8.1).

// modStatusLine is the chip text: mod, gameplay layer and mutator count.
func (g *gameShell) modStatusLine() string {
	name := "Total Annihilation"
	if g.cs != nil && g.cs.manualRoots {
		name = "Custom content"
	} else if g.cs != nil && g.cs.mod != nil {
		name = g.cs.mod.Name + " " + g.cs.mod.Version
	}
	line := name + " - " + gameplayLabel(g.gameplay)
	switch n := mutatorCount(g.opts.Mutators); n {
	case 0:
	case 1:
		line += " - 1 mutator"
	default:
		line += fmt.Sprintf(" - %d mutators", n)
	}
	return line
}

// installMainMenuModsButton appends the Nanolathe-owned MODS button and
// status line to a fresh MAINMENU clone, above the logo (D11 places it at a
// fixed position; the position is a capture-review choice).
func installMainMenuModsButton(window *gui.Window) {
	single, status := window.GadgetIndex("SINGLE"), window.GadgetIndex("DebugString")
	if single < 0 {
		return
	}
	button := window.Gadgets[single]
	button.Name, button.SourceName, button.Text, button.QuickKey = "MODS", "MODS", "MODS", 0
	button.Rect.X, button.Rect.Y = (retailScreenW-button.Rect.W)/2, 8
	window.Gadgets = append(window.Gadgets, button)
	if status >= 0 {
		label := window.Gadgets[status]
		label.Name, label.SourceName, label.Text = "MODSTATUS", "MODSTATUS", ""
		label.Rect.X, label.Rect.Y, label.Rect.W = 0, button.Rect.Y+button.Rect.H+4, retailScreenW
		window.Gadgets = append(window.Gadgets, label)
	}
}

// refreshMainMenuModStatus writes the status line, centred like retail
// centres its version literal.
func (g *gameShell) refreshMainMenuModStatus(p *ui.Panel) {
	if p == nil || p.Window == nil {
		return
	}
	index := p.Window.GadgetIndex("MODSTATUS")
	if index < 0 {
		return
	}
	text := g.modStatusLine()
	if g.cs != nil && g.cs.modNotice != "" {
		// A notice may carry a failure's reason; it keeps to one line.
		text = g.fitDetail(g.cs.modNotice, retailScreenW-16, 1)
	}
	p.SetActive("MODSTATUS", true)
	p.SetText("MODSTATUS", text)
	p.Window.Gadgets[index].Rect.X = int32((retailScreenW - g.retailTextWidth(text)) / 2)
}

// ---------------------------------------------------------------------------
// The Mods & Mutators screen and the Get more mods dialog (§8.2).

const (
	modsTemplateGUI      = "guis/selmap.gui"
	modsTemplateBackdrop = "bitmaps/dselectmap2.pcx"
)

type modsScreen struct {
	lib *modlibrary.Library
	// base is the base install alone, mounted once per screen. A mod's
	// `requires` paths are checked against it (§4.2), and the screen and its
	// dialogs read their window template from it.
	base      *vfs.FS
	baseRoots []string
	installed []modlibrary.Mod
	selected  int // list row; row 0 is the original game
	mutators  content.Mutators
	usePreset bool
	notice    string
	// recommended caches each listed mod's controls preset and gameplay
	// minimum with its content profile's filled in (§4.3), keyed by
	// id@version, so a row's profile is resolved once per screen.
	recommended map[string]modlibrary.Mod
	// installs is the download job's install count the list was read at; a
	// later count means a download joined the library (§8.2).
	installs int
}

// modsFetch is the Get more mods dialog's own state: the catalogue fetch and
// the list. The download it starts is the process's modDownload job, which
// outlives the dialog.
type modsFetch struct {
	mu       sync.Mutex
	status   string
	entries  []modfetch.Entry
	selected int
	cancel   context.CancelFunc // stops the catalogue fetch when the dialog closes
	dirty    bool
}

// modDownloadJob is the one catalogue download and install the process runs
// at a time (§8.2). It outlives the Get more mods dialog: closing the dialog
// leaves it running, and an install, once started, finishes whether or not
// the dialog or the Mods & Mutators screen is still open, because the
// library cannot interrupt one (§5.3). The dialog's Cancel and leaving the
// Mods & Mutators screen stop the network part.
type modDownloadJob struct {
	mu          sync.Mutex
	entry       modfetch.Entry
	running     bool
	installing  bool // past the network part; nothing cancels it
	done, total int64
	cancel      context.CancelFunc
	cancelled   bool   // the player cancelled the network part
	outcome     string // the last finished job's result
	unseen      bool   // no Get more mods dialog has shown the outcome yet
	installs    int    // completed installs, so an open screen re-reads the library
	dirty       bool
}

var modDownload modDownloadJob

// modDownloadView is one consistent reading of the job for the dialog.
type modDownloadView struct {
	entry       modfetch.Entry
	running     bool
	installing  bool
	done, total int64
	outcome     string // set while a finished job's outcome is unseen
	installs    int
}

func (j *modDownloadJob) view() modDownloadView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := modDownloadView{entry: j.entry, running: j.running, installing: j.installing, done: j.done, total: j.total, installs: j.installs}
	if !j.running && j.unseen {
		v.outcome = j.outcome
	}
	return v
}

// start runs download and then install for entry, unless a job is already
// running. download receives the context the network part is cancelled
// through; install runs after a successful download and is never cancelled.
func (j *modDownloadJob) start(entry modfetch.Entry, download func(ctx context.Context, progress func(done, total int64)) error, install func() error) bool {
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.entry, j.running, j.installing, j.cancelled = entry, true, false, false
	j.done, j.total, j.cancel, j.unseen, j.dirty = 0, entry.Archive.Size, cancel, false, true
	j.mu.Unlock()
	go func() {
		defer cancel()
		err := download(ctx, func(done, total int64) {
			j.mu.Lock()
			j.done, j.total, j.dirty = done, total, true
			j.mu.Unlock()
		})
		if err == nil {
			j.mu.Lock()
			j.installing, j.dirty = true, true
			j.mu.Unlock()
			err = install()
		}
		j.mu.Lock()
		defer j.mu.Unlock()
		j.running, j.installing, j.cancel, j.unseen, j.dirty = false, false, nil, true, true
		switch {
		case err != nil && j.cancelled:
			// Only the player's own cancel reads as one. A stall also ends
			// in a cancelled context, but its error says what happened and
			// that the download resumes next time.
			j.outcome = "Download cancelled"
		case err != nil:
			j.outcome = "Failed: " + err.Error()
		default:
			j.outcome = entry.Name + " " + entry.Version + " installed"
			j.installs++
		}
	}()
	return true
}

// cancelNetwork is the player cancelling the running download. It reports
// whether there was a network part left to stop.
func (j *modDownloadJob) cancelNetwork() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.running || j.installing || j.cancel == nil {
		return false
	}
	j.cancelled, j.dirty = true, true
	j.cancel()
	return true
}

// acknowledge records that a dialog has shown the finished job's outcome.
func (j *modDownloadJob) acknowledge() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.running {
		j.unseen = false
	}
}

func (j *modDownloadJob) takeDirty() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	dirty := j.dirty
	j.dirty = false
	return dirty
}

var (
	modsUI          *modsScreen
	modsPanel       *ui.Panel
	modsAssets      *retailPanelAssets
	modsFetchUI     *modsFetch
	modsFetchPanel  *ui.Panel
	modsFetchAssets *retailPanelAssets
)

func (g *gameShell) modsPanelActive() bool {
	return g != nil && modsUI != nil && modsPanel != nil && g.activePanel() == modsPanel
}

func (g *gameShell) modsFetchPanelActive() bool {
	return g != nil && modsFetchUI != nil && modsFetchPanel != nil && g.activePanel() == modsFetchPanel
}

// modsPanelAssets supplies the background of either Nanolathe window.
func modsPanelAssets(p *ui.Panel) *retailPanelAssets {
	switch {
	case p != nil && p == modsPanel:
		return modsAssets
	case p != nil && p == modsFetchPanel:
		return modsFetchAssets
	case p != nil && p == mutatorsPanel:
		return mutatorsAssets
	case p != nil && p == controlsOfferPanel:
		return controlsOfferAssets
	}
	return nil
}

// modsListHeadingColor is the colour Nanolathe's own windows draw list
// headings in: the window's authored colour (gold on the SELMAP template),
// drawn over the darkened band. Retail darkens a heading's text along with
// its row [07 R-WGT-01 §4], which leaves the Mutators dialog's group headings
// too dim to read. Retail windows keep the retail heading.
func modsListHeadingColor(g *gameShell, p *ui.Panel) (byte, bool) {
	if modsPanelAssets(p) == nil || p.Window == nil || len(p.Window.Gadgets) == 0 {
		return 0, false
	}
	return g.guiColor(byte(p.Window.Gadgets[0].ColorF & 0xff)), true
}

// modsBackdrop copies the map-select backdrop and paints plain texture over
// its baked-in title, so the window can carry its own caption.
func modsBackdrop(from vfs.FSOps) (*formats.PCX, error) {
	source, err := formats.LoadPCXFile(from, modsTemplateBackdrop)
	if err != nil {
		return nil, err
	}
	pcx := *source
	pcx.Pixels = append([]byte(nil), source.Pixels...)
	const fromX, fromY, toX, toY, w, h = 250, 28, 40, 28, 190, 44
	width := int(pcx.Width)
	for y := 0; y < h; y++ {
		src := (fromY+y)*width + fromX
		dst := (toY+y)*width + toX
		if src+w <= len(pcx.Pixels) && dst+w <= len(pcx.Pixels) {
			copy(pcx.Pixels[dst:dst+w], source.Pixels[src:src+w])
		}
	}
	return &pcx, nil
}

// modsWindowKind names the four Nanolathe windows built on the SELMAP
// template: the Mods & Mutators screen, the Get more mods dialog, the
// Mutators dialog and the recommended-settings offer.
type modsWindowKind int

const (
	modsWindowMain modsWindowKind = iota
	modsWindowFetch
	modsWindowMutators
	modsWindowOffer
)

// mutatorSummaryLines is how many active mutators the main screen lists
// before it says "and N more".
const mutatorSummaryLines = 4

// buildModsWindow shapes a SELMAP clone into one of the three windows. The
// list, scrollbar, detail labels and the two action buttons keep their
// authored rectangles; the map picture goes, and each window adds its own
// controls in the right-hand column.
func buildModsWindow(window *gui.Window, kind modsWindowKind) {
	var kept []gui.Gadget
	var action, label gui.Gadget
	for _, gad := range window.Gadgets {
		switch gad.Name {
		case "MAPPIC":
			continue
		case "LOAD":
			action = gad
		case "DESCRIPTION":
			label = gad
		}
		kept = append(kept, gad)
	}
	title := label
	title.Name, title.SourceName, title.Text = "NTITLE", "NTITLE", "MODS & MUTATORS"
	title.Rect.X, title.Rect.Y, title.Rect.W, title.Rect.H = 60, 44, 240, 18
	switch kind {
	case modsWindowFetch:
		title.Text = "GET MORE MODS"
	case modsWindowMutators:
		title.Text = "MUTATORS"
	case modsWindowOffer:
		title.Text = "RECOMMENDED SETTINGS"
	}
	kept = append(kept, title)
	button := func(name, text string, x, y int32) {
		b := action
		b.Name, b.SourceName, b.Text, b.QuickKey = name, name, text, 0
		b.Rect.X, b.Rect.Y = x, y
		kept = append(kept, b)
	}
	caption := func(name, text string, x, y, w, h int32) {
		c := label
		c.Name, c.SourceName, c.Text = name, name, text
		c.Rect.X, c.Rect.Y, c.Rect.W, c.Rect.H = x, y, w, h
		kept = append(kept, c)
	}
	switch kind {
	case modsWindowFetch:
		caption("STATUS", "", 352, 150, 116, 48)
		caption("STATUS2", "", 352, 206, 116, 16)
		caption("STATUS3", "", 352, 222, 116, 16)
	case modsWindowOffer:
		// The list names every setting, the right-hand column explains the
		// offer, and the two buttons answer it.
		caption("OFFERTEXT", "", 352, 150, 116, 112)
	case modsWindowMutators:
		// The selected mutator's value and its controls. The list on the
		// left scrolls, so the catalogue can grow without a layout change.
		// The name and its factor take a line each, so no label and decimal
		// together outgrow the column.
		caption("MUTVALUE", "", 352, 150, 116, 16)
		caption("MUTFACTOR", "", 352, 166, 116, 16)
		button("MUTRAISE", "Raise", 357, 188)
		button("MUTLOWER", "Lower", 357, 214)
		button("MUTDEFAULT", "Default", 357, 240)
		button("MUTRESETALL", "Reset all", 357, 86)
	default:
		button("GETMORE", "Get more", 357, 86)
		button("REMOVE", "Remove", 357, 112)
		// The right column summarises the active mutators; editing them is
		// the Mutators dialog's job, so the column never grows with the
		// catalogue.
		caption("MUTLABEL", "Mutators", 352, 150, 120, 16)
		for i := 0; i < mutatorSummaryLines; i++ {
			caption(fmt.Sprintf("MUTSUM%d", i), "", 352, 166+int32(i)*14, 118, 14)
		}
		button("MUTEDIT", "Change...", 357, 228)
		button("MUTRESET", "Reset", 357, 252)
		// The preset toggle and its caption read as a checkbox: Yes writes
		// the selected mod's recommended settings on Apply (§4.3, P10).
		button("PRESET", "Yes", 60, 366)
		caption("PRESETLABEL", "Use recommended settings", 164, 370, 200, 16)
	}
	window.Gadgets = kept
}

// modsTemplateFS is where the three windows read their SELMAP template: the
// base install alone, so the screen that switches mods keeps one layout
// whichever mod is running. A mod may ship its own map-select window (ProTA
// ships a full-screen one), and this layout is placed against the base art.
// The mounted overlay is the fallback when the base could not be mounted.
func (g *gameShell) modsTemplateFS() vfs.FSOps {
	if modsUI != nil && modsUI.base != nil {
		return modsUI.base
	}
	if controlsOfferUI != nil && controlsOfferUI.base != nil {
		return controlsOfferUI.base
	}
	return g.cs.fs
}

func (g *gameShell) loadModsPanel(kind modsWindowKind) (*ui.Panel, *retailPanelAssets, error) {
	from := g.modsTemplateFS()
	window, err := gui.LoadWithTranslation(from, modsTemplateGUI, g.cs.translations)
	if err != nil {
		return nil, nil, retailFrontendAssetError(g.cs, "mods screen GUI unavailable", modsTemplateGUI, "the authored map-select window used as the template", err)
	}
	background, err := modsBackdrop(from)
	if err != nil {
		return nil, nil, retailFrontendAssetError(g.cs, "mods screen bitmap", modsTemplateBackdrop, "the authored map-select backdrop", err)
	}
	buildModsWindow(window, kind)
	g.installRetailWindowButtonArt(window, nil)
	g.installRetailListScrollbars(window, nil)
	panel := ui.NewPanel(window)
	if panel == nil {
		return nil, nil, fmt.Errorf("nanolathe: mods screen: panel construction failed")
	}
	return panel, &retailPanelAssets{window: window, background: background}, nil
}

func (g *gameShell) openModsScreenReporting() {
	if err := g.openModsScreen(); err != nil {
		reportRetailMessageError(g.showRetailMessage(err.Error()))
	}
}

func (g *gameShell) openModsScreen() error {
	if g == nil || g.cs == nil {
		return fmt.Errorf("nanolathe: mods screen: no mounted content")
	}
	lib, err := openModLibrary()
	if err != nil {
		return err
	}
	base := vfs.New()
	if err := base.MountGameDirectories(g.cs.baseRoots); err != nil {
		base.Close()
		base = nil
	}
	modsUI = &modsScreen{lib: lib, base: base, baseRoots: append([]string(nil), g.cs.baseRoots...), mutators: g.opts.Mutators, usePreset: true, installs: modDownload.view().installs}
	panel, assets, err := g.loadModsPanel(modsWindowMain)
	if err != nil {
		if base != nil {
			base.Close()
		}
		modsUI = nil
		return err
	}
	modsUI.reload(g.cs.mod)
	modsPanel, modsAssets = panel, assets
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	g.refreshModsPanel()
	return nil
}

// reload re-reads the installed list and selects the running mod's row.
func (s *modsScreen) reload(running *modlibrary.Mod) {
	installed, err := s.lib.Installed()
	if err != nil {
		s.notice = err.Error()
	}
	s.installed = installed
	s.selected = 0
	if running != nil {
		for i, mod := range installed {
			if mod.ID == running.ID && mod.Version == running.Version {
				s.selected = i + 1
			}
		}
	}
}

func (s *modsScreen) selectedMod() *modlibrary.Mod {
	if s == nil || s.selected <= 0 || s.selected > len(s.installed) {
		return nil
	}
	return &s.installed[s.selected-1]
}

// missingFor lists a mod's unmet base-install requirements (§4.2).
func (s *modsScreen) missingFor(meta modlibrary.Metadata) []string {
	if s == nil || s.base == nil {
		return modlibrary.MissingRequirements(nil, meta)
	}
	return modlibrary.MissingRequirements(s.base, meta)
}

func sameMod(a, b *modlibrary.Mod) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.ID == b.ID && a.Version == b.Version
}

func (g *gameShell) closeModsScreen() {
	// Leaving the screen cancels a download's network part; an install
	// already under way finishes (§8.2).
	modDownload.cancelNetwork()
	if g != nil && modsPanel != nil && g.frontend.Panels.Top() == modsPanel {
		g.frontend.Panels.Pop()
	}
	if modsUI != nil && modsUI.base != nil {
		modsUI.base.Close()
	}
	modsUI, modsPanel, modsAssets = nil, nil, nil
	if p := g.activePanel(); p != nil && g.frontend.Mode == modeMenuMain {
		g.refreshMainMenuModStatus(p)
	}
}

func (g *gameShell) refreshModsPanel() {
	if modsUI == nil || modsPanel == nil {
		return
	}
	p := modsPanel
	items := []string{"Total Annihilation"}
	for _, mod := range modsUI.installed {
		row := mod.Name + " " + mod.Version
		if mod.Local {
			row += " (local)"
		}
		if sameMod(&mod, g.cs.mod) {
			row += " *"
		}
		items = append(items, row)
	}
	// setListItems writes to the active panel. A dialog opened from this
	// screen is active by the time the screen refreshes itself, and must not
	// receive the mods list.
	if g.activePanel() == p {
		g.setListItems("MAPNAMES", items, modsUI.selected)
	}
	// The selected mod as the mount will see it: a controls preset or
	// gameplay minimum its metadata lacks comes from its content profile.
	selected := modsUI.modRecommendations(modsUI.selectedMod())
	description, detail := "The original game with no mod mounted.", "Any gameplay mode"
	if selected != nil {
		description = selected.Summary
		if description == "" {
			description = selected.Name
		}
		detail = modRequirement(selected.Version, selected.MinimumGameplay)
		if minimum, ok := modMinimumGameplay(selected); ok && gameplayBelow(g.gameplay, minimum) {
			detail += " (will switch)"
		}
		if missing := modsUI.missingFor(selected.Metadata); len(missing) > 0 {
			detail = "Base install lacks " + missing[0]
		}
	}
	if modsUI.notice != "" {
		detail = modsUI.notice
	}
	p.SetText("DESCRIPTION", g.fitDetail(description, 230, 2))
	p.SetText("SIZE", g.fitDetail(detail, 230, 1))
	p.SetText("LOAD", "Apply")
	active := modsUI.mutators.DescribeASCII()
	for i := 0; i < mutatorSummaryLines; i++ {
		line := ""
		switch {
		case i == 0 && len(active) == 0:
			line = "None"
		case i == mutatorSummaryLines-1 && len(active) > mutatorSummaryLines:
			line = fmt.Sprintf("and %d more", len(active)-i)
		case i < len(active):
			line = active[i]
		}
		p.SetText(fmt.Sprintf("MUTSUM%d", i), g.fitDetail(line, 118, 1))
	}
	retailGreyGadget(p.Window, "MUTRESET", len(active) == 0)
	// A mod's recommended settings are offered only when switching to a mod
	// that names a preset (§4.3, P10); the button toggles whether Apply
	// writes them.
	offer := selected != nil && selected.Controls != "" && !sameMod(selected, g.cs.mod)
	p.SetActive("PRESET", offer)
	p.SetActive("PRESETLABEL", offer)
	if offer {
		p.SetText("PRESET", presetToggleText(modsUI.usePreset))
	}
	running := sameMod(selected, g.cs.mod)
	// A mod whose base requirements are unmet is listed but not selectable.
	retailGreyGadget(p.Window, "LOAD", selected != nil && len(modsUI.missingFor(selected.Metadata)) > 0)
	retailGreyGadget(p.Window, "REMOVE", selected == nil || running)
	retailGreyGadget(p.Window, "GETMORE", g.cs.manualRoots)
	if g.cs.manualRoots && !running {
		p.SetText("SIZE", "Custom --root stack: restart without extra roots to switch")
	}
}

func (g *gameShell) selectModsRow(index int) {
	if modsUI == nil {
		return
	}
	modsUI.selected = index
	modsUI.notice = ""
	g.refreshModsPanel()
}

// applyModsScreen commits both columns. With the running mod kept, the
// mutators take effect from the next battle and are saved at once. A
// different mod is requested as one reload that carries the whole selection
// (§4.4): nothing changes, and nothing is saved, unless the new content binds.
func (g *gameShell) applyModsScreen() {
	target := modsUI.modRecommendations(modsUI.selectedMod())
	if sameMod(target, g.cs.mod) || g.cs.manualRoots {
		g.opts.Mutators = modsUI.mutators
		g.mutatorSetting = modsUI.mutators.Map()
		g.saveSettings()
		g.closeModsScreen()
		return
	}
	request := contentReloadRequest{selector: "none", mutators: modsUI.mutators}
	if target != nil {
		request.selector = modSelectorOf(target.ID, target.Version)
		request.mod = settings.ModSelection{ID: target.ID, Version: target.Version}
		if minimum, ok := modMinimumGameplay(target); ok && gameplayBelow(g.gameplay, minimum) {
			request.gameplay = minimum
		}
		if target.Controls != "" {
			request.offered = target.ID
			if modsUI.usePreset {
				request.controls = target.Controls
			}
		}
	}
	pendingContentReload = &request
}

func (g *gameShell) removeSelectedMod() {
	target := modsUI.selectedMod()
	if target == nil || sameMod(target, g.cs.mod) {
		return
	}
	if err := modsUI.lib.Remove(target.ID, target.Version); err != nil {
		modsUI.notice = err.Error()
	}
	modsUI.reload(g.cs.mod)
	g.refreshModsPanel()
}

// activateModsGadget routes a button on either Nanolathe window.
func (g *gameShell) activateModsGadget(name string) bool {
	if g.activateControlsOfferGadget(name) {
		return true
	}
	if g.mutatorsPanelActive() {
		g.activateMutatorsGadget(name)
		return true
	}
	if g.modsFetchPanelActive() {
		switch name {
		case "LOAD":
			// The action button is Cancel while a download runs (greyed once
			// it installs), and Download otherwise.
			if modDownload.view().running {
				modDownload.cancelNetwork()
			} else {
				g.startModDownload()
			}
			g.refreshModsFetch()
		case "PREVMENU":
			g.closeModsFetch()
		}
		return true
	}
	if !g.modsPanelActive() {
		return false
	}
	switch name {
	case "LOAD":
		g.applyModsScreen()
	case "PREVMENU":
		g.closeModsScreen()
	case "GETMORE":
		if err := g.openModsFetch(); err != nil {
			modsUI.notice = err.Error()
			g.refreshModsPanel()
		}
	case "REMOVE":
		g.removeSelectedMod()
	case "MUTEDIT":
		if err := g.openMutatorsDialog(); err != nil {
			modsUI.notice = err.Error()
		}
	case "MUTRESET":
		modsUI.mutators = content.Mutators{}
	case "PRESET":
		modsUI.usePreset = !modsUI.usePreset
	}
	g.refreshModsPanel()
	return true
}

func (g *gameShell) commitModsListSelection(name string, index int) bool {
	if name != "MAPNAMES" {
		return false
	}
	if g.controlsOfferActive() {
		controlsOfferUI.selected = index
		g.refreshControlsOffer()
		return true
	}
	if g.mutatorsPanelActive() {
		g.selectMutatorRow(index)
		return true
	}
	if g.modsFetchPanelActive() {
		modsFetchUI.mu.Lock()
		modsFetchUI.selected = index
		modsFetchUI.mu.Unlock()
		g.refreshModsFetch()
		return true
	}
	if g.modsPanelActive() {
		g.selectModsRow(index)
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Get more mods (§5, §8.2).

func (g *gameShell) openModsFetch() error {
	if modsUI == nil {
		return nil
	}
	panel, assets, err := g.loadModsPanel(modsWindowFetch)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	state := &modsFetch{status: "Fetching the catalogue...", cancel: cancel}
	modsFetchUI, modsFetchPanel, modsFetchAssets = state, panel, assets
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	installed := append([]modlibrary.Mod(nil), modsUI.installed...)
	client := &modfetch.Client{CatalogURL: modfetch.CatalogURL(), CacheDir: modsUI.lib.Root}
	go func() {
		defer cancel()
		result, err := client.FetchManifest(ctx)
		state.mu.Lock()
		defer state.mu.Unlock()
		state.dirty = true
		if err != nil {
			state.status = "Catalogue unavailable: " + err.Error()
			return
		}
		state.entries = state.entries[:0]
		for _, entry := range result.Manifest.Mods {
			if !modInstalled(installed, entry.ID, entry.Version) {
				state.entries = append(state.entries, entry)
			}
		}
		switch {
		case result.FromCache:
			state.status = "Offline: catalogue from " + result.FetchedAt.Local().Format("2 Jan 15:04")
		case len(state.entries) == 0:
			state.status = "Every listed mod is installed"
		default:
			state.status = fmt.Sprintf("%d available", len(state.entries))
		}
	}()
	g.refreshModsFetch()
	return nil
}

func modInstalled(installed []modlibrary.Mod, id, version string) bool {
	for _, mod := range installed {
		if mod.ID == id && mod.Version == version {
			return true
		}
	}
	return false
}

// closeModsFetch closes the dialog and stops its catalogue fetch. A download
// it started keeps running (§8.2); a finished one's outcome counts as seen.
func (g *gameShell) closeModsFetch() {
	if modsFetchUI != nil {
		modsFetchUI.mu.Lock()
		if modsFetchUI.cancel != nil {
			modsFetchUI.cancel()
		}
		modsFetchUI.mu.Unlock()
		modDownload.acknowledge()
	}
	if g != nil && modsFetchPanel != nil && g.frontend.Panels.Top() == modsFetchPanel {
		g.frontend.Panels.Pop()
	}
	modsFetchUI, modsFetchPanel, modsFetchAssets = nil, nil, nil
	g.refreshModsPanel()
}

func (g *gameShell) startModDownload() {
	state := modsFetchUI
	if state == nil || modsUI == nil {
		return
	}
	state.mu.Lock()
	if state.selected < 0 || state.selected >= len(state.entries) {
		state.mu.Unlock()
		return
	}
	entry := state.entries[state.selected]
	state.mu.Unlock()
	lib, base := modsUI.lib, append([]string(nil), g.cs.baseRoots...)
	client := &modfetch.Client{CatalogURL: modfetch.CatalogURL(), CacheDir: lib.Root}
	// Downloads live in .downloads, outside staging, and a transfer that
	// stops part-way keeps its part file there so the next attempt resumes it
	// (§5.3).
	dst := filepath.Join(lib.Root, ".downloads", entry.ArchiveName())
	modDownload.start(entry, func(ctx context.Context, progress func(done, total int64)) error {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return client.Download(ctx, entry, dst, progress)
	}, func() error {
		options := entry.InstallOptions()
		options.Validate = modlibrary.ContentValidator(base)
		_, err := lib.InstallArchive(dst, options)
		_ = os.Remove(dst)
		return err
	})
}

// pollModsFetch runs once per update: a worker marks the dialog or the
// download dirty and the render thread repaints. A download that installed
// a mod re-reads the screen's installed list and drops the mod from the
// dialog's catalogue list.
func (g *gameShell) pollModsFetch() {
	jobDirty := modDownload.takeDirty()
	if modsUI != nil {
		if installs := modDownload.view().installs; installs != modsUI.installs {
			modsUI.installs = installs
			// Re-read the list but keep the player's row, including the
			// original game's row 0.
			var keep *modlibrary.Mod
			if selected := modsUI.selectedMod(); selected != nil {
				copied := *selected
				keep = &copied
			}
			modsUI.reload(g.cs.mod)
			modsUI.selected = 0
			for i := range modsUI.installed {
				if keep != nil && sameMod(&modsUI.installed[i], keep) {
					modsUI.selected = i + 1
				}
			}
			jobDirty = true
			g.refreshModsPanel()
		}
	}
	state := modsFetchUI
	if state == nil {
		return
	}
	state.mu.Lock()
	dirty := state.dirty || jobDirty
	state.dirty = false
	if dirty && modsUI != nil {
		kept := state.entries[:0]
		for _, entry := range state.entries {
			if !modInstalled(modsUI.installed, entry.ID, entry.Version) {
				kept = append(kept, entry)
			}
		}
		state.entries = kept
		if state.selected >= len(kept) {
			state.selected = max(len(kept)-1, 0)
		}
	}
	state.mu.Unlock()
	if dirty && g.modsFetchPanelActive() {
		g.refreshModsFetch()
	}
}

func (g *gameShell) refreshModsFetch() {
	state, p := modsFetchUI, modsFetchPanel
	if state == nil || p == nil {
		return
	}
	job := modDownload.view()
	state.mu.Lock()
	defer state.mu.Unlock()
	items := make([]string, 0, len(state.entries))
	for _, e := range state.entries {
		items = append(items, fmt.Sprintf("%s %s  (%.1f MB)", e.Name, e.Version, float64(e.Archive.Size)/(1<<20)))
	}
	if g.activePanel() == p {
		g.setListItems("MAPNAMES", items, state.selected)
	}
	description, detail := "", ""
	if state.selected >= 0 && state.selected < len(state.entries) {
		e := state.entries[state.selected]
		description = e.Summary
		detail = modRequirement(e.Version, e.MinimumGameplay)
		if missing := modsUI.missingFor(e.Metadata); len(missing) > 0 {
			detail = "Base install lacks " + missing[0]
		}
	}
	p.SetText("DESCRIPTION", g.fitDetail(description, 230, 2))
	p.SetText("SIZE", g.fitDetail(detail, 230, 1))
	// The running download, or the outcome of one this dialog has not shown
	// yet, takes the status line from the catalogue's.
	status, status2, status3 := state.status, "", ""
	switch {
	case job.installing:
		status = "Installing " + job.entry.Name
	case job.running:
		status = "Downloading " + job.entry.Name
		if job.total > 0 {
			status2 = fmt.Sprintf("%d%%", job.done*100/job.total)
			status3 = fmt.Sprintf("%.1f / %.1f MB", float64(job.done)/(1<<20), float64(job.total)/(1<<20))
		}
	case job.outcome != "":
		status = job.outcome
	}
	p.SetText("STATUS", g.fitDetail(status, 116, 3))
	p.SetText("STATUS2", status2)
	p.SetText("STATUS3", status3)
	// One download runs at a time: while one is on the network the action
	// button cancels it, and while one installs it is unavailable.
	action := "Download"
	if job.running && !job.installing {
		action = "Cancel"
	}
	p.SetText("LOAD", action)
	p.SetText("PREVMENU", "Close")
	retailGreyGadget(p.Window, "LOAD", job.installing || (!job.running && len(state.entries) == 0))
}

// ---------------------------------------------------------------------------
// The Mutators dialog (§8.2): the whole catalogue as a grouped, scrolling
// list built from content.MutatorCatalog, so a new mutator needs no layout.

type mutatorRow struct {
	key  string // "" for a group heading
	text string
}

type mutatorsDialog struct {
	before   content.Mutators // restored by Cancel
	rows     []mutatorRow
	selected int
}

var (
	mutatorsUI     *mutatorsDialog
	mutatorsPanel  *ui.Panel
	mutatorsAssets *retailPanelAssets
)

func (g *gameShell) mutatorsPanelActive() bool {
	return g != nil && mutatorsUI != nil && mutatorsPanel != nil && g.activePanel() == mutatorsPanel
}

// mutatorRows lays the catalogue out in its own order, with a heading row
// (the retail list's "&G" heading form) wherever the group changes.
func mutatorRows(m content.Mutators) []mutatorRow {
	var rows []mutatorRow
	group := ""
	for _, info := range content.MutatorCatalog() {
		if info.Group != group {
			group = info.Group
			rows = append(rows, mutatorRow{text: "&G" + group})
		}
		f, _ := m.Factor(info.Key)
		rows = append(rows, mutatorRow{key: info.Key, text: "   " + mutatorText(info.Label, f)})
	}
	return rows
}

func (g *gameShell) openMutatorsDialog() error {
	if modsUI == nil {
		return nil
	}
	panel, assets, err := g.loadModsPanel(modsWindowMutators)
	if err != nil {
		return err
	}
	mutatorsUI = &mutatorsDialog{before: modsUI.mutators}
	mutatorsPanel, mutatorsAssets = panel, assets
	g.frontend.Panels.Push(panel)
	flushWindowTokens(clPtr)
	mutatorsUI.rows = mutatorRows(modsUI.mutators)
	g.selectMutatorRow(0)
	return nil
}

func (g *gameShell) closeMutatorsDialog(keep bool) {
	if mutatorsUI != nil && !keep && modsUI != nil {
		modsUI.mutators = mutatorsUI.before
	}
	if g != nil && mutatorsPanel != nil && g.frontend.Panels.Top() == mutatorsPanel {
		g.frontend.Panels.Pop()
	}
	mutatorsUI, mutatorsPanel, mutatorsAssets = nil, nil, nil
	g.refreshModsPanel()
}

// selectMutatorRow selects a row; a heading passes the selection to the
// first mutator beneath it.
func (g *gameShell) selectMutatorRow(index int) {
	if mutatorsUI == nil {
		return
	}
	rows := mutatorsUI.rows
	for index < len(rows) && rows[index].key == "" {
		index++
	}
	if index >= len(rows) {
		index = len(rows) - 1
	}
	mutatorsUI.selected = index
	g.refreshMutatorsDialog()
}

func (g *gameShell) selectedMutator() (content.MutatorInfo, bool) {
	if mutatorsUI == nil || mutatorsUI.selected < 0 || mutatorsUI.selected >= len(mutatorsUI.rows) {
		return content.MutatorInfo{}, false
	}
	key := mutatorsUI.rows[mutatorsUI.selected].key
	for _, info := range content.MutatorCatalog() {
		if info.Key == key {
			return info, true
		}
	}
	return content.MutatorInfo{}, false
}

func (g *gameShell) refreshMutatorsDialog() {
	if mutatorsUI == nil || mutatorsPanel == nil || modsUI == nil {
		return
	}
	p := mutatorsPanel
	mutatorsUI.rows = mutatorRows(modsUI.mutators)
	items := make([]string, len(mutatorsUI.rows))
	for i, row := range mutatorsUI.rows {
		items[i] = row.text
	}
	if g.activePanel() == p {
		g.setListItems("MAPNAMES", items, mutatorsUI.selected)
	}
	info, ok := g.selectedMutator()
	description, value, factor := "", "", ""
	if ok {
		f, _ := modsUI.mutators.Factor(info.Key)
		description, value, factor = info.Description, info.Label, "x"+f.String()
		retailGreyGadget(p.Window, "MUTRAISE", f.String() == content.MutatorSteps[len(content.MutatorSteps)-1].String())
		retailGreyGadget(p.Window, "MUTLOWER", f.String() == content.MutatorSteps[0].String())
		retailGreyGadget(p.Window, "MUTDEFAULT", f.IsIdentity())
	}
	retailGreyGadget(p.Window, "MUTRESETALL", modsUI.mutators.IsZero())
	p.SetText("DESCRIPTION", g.fitDetail(description, 230, 2))
	p.SetText("SIZE", g.fitDetail(fmt.Sprintf("%d active", mutatorCount(modsUI.mutators)), 230, 1))
	p.SetText("MUTVALUE", g.fitDetail(value, 116, 1))
	p.SetText("MUTFACTOR", factor)
	p.SetText("LOAD", "OK")
	p.SetText("PREVMENU", "Cancel")
}

func (g *gameShell) activateMutatorsGadget(name string) {
	info, ok := g.selectedMutator()
	step := func(next func(content.Factor) content.Factor) {
		if !ok {
			return
		}
		f, _ := modsUI.mutators.Factor(info.Key)
		_ = modsUI.mutators.SetFactor(info.Key, next(f))
	}
	switch name {
	case "LOAD":
		g.closeMutatorsDialog(true)
		return
	case "PREVMENU":
		g.closeMutatorsDialog(false)
		return
	case "MUTRAISE":
		step(content.Factor.Next)
	case "MUTLOWER":
		step(content.Factor.Prev)
	case "MUTDEFAULT":
		step(func(content.Factor) content.Factor { return content.Factor{} })
	case "MUTRESETALL":
		modsUI.mutators = content.Mutators{}
	}
	g.refreshMutatorsDialog()
}

// fitDetail trims text to the lines the authored description label holds,
// ending a cut line with "...", so a long summary never runs into the line
// beneath it.
func (g *gameShell) fitDetail(text string, width, lines int) string {
	wrapped := retailWrapLines(text, g.retailTextWidth, width)
	if len(wrapped) <= lines {
		return text
	}
	last := wrapped[lines-1]
	for last != "" && g.retailTextWidth(last+"...") > width {
		last = last[:len(last)-1]
	}
	return strings.Join(append(append([]string(nil), wrapped[:lines-1]...), strings.TrimSpace(last)+"..."), " ")
}

// modRequirement is the short detail line: version and minimum gameplay.
func modRequirement(version, minimum string) string {
	line := "Version " + version
	if minimum != "" {
		if mode, err := gameplay.Parse(minimum); err == nil {
			line += ", " + gameplayLabel(mode) + "+"
		}
	}
	return line
}

// ---------------------------------------------------------------------------
// Loading-screen lines (§8.3).

func (g *gameShell) loadingSelectionLines() []string {
	name := "Total Annihilation"
	if g.cs != nil && g.cs.manualRoots {
		name = "Custom content"
	} else if g.cs != nil && g.cs.mod != nil {
		name = g.cs.mod.Name + " " + g.cs.mod.Version
	}
	first := name + " - " + gameplayLabel(g.gameplay)
	if g.loading != nil && g.loading.mapName != "" && g.setup.UnitLimit > 0 {
		// The line is drawn every frame; the limit is resolved once per load.
		if g.loading.unitLimitText == "" {
			g.loading.unitLimitText = g.unitLimitText()
		}
		first += " - " + g.loading.unitLimitText
	}
	lines := []string{first}
	if summary := mutatorSummary(g.opts.Mutators); summary != "" {
		// Wrap rather than run off the 640-pixel screen as mutators accumulate.
		lines = append(lines, retailWrapLines("Mutators: "+summary, g.retailTextWidth, retailScreenW-80)...)
	}
	return lines
}

// effectiveUnitLimit is the per-player unit limit a skirmish entered now
// would use, resolved from the same inputs battle entry resolves: a
// Community feature table that sets one overrides the player's setting
// (DESIGN_COMMUNITY_PATCH §3.2), and Strict 3.1 ignores every table. source
// names what overrode the setting, "" when nothing did.
func (g *gameShell) effectiveUnitLimit() (limit int, source string) {
	limit = g.setup.UnitLimit
	features, err := session.ResolveCommunity(g.gameplay, communitySources(g.opts, g.cs))
	if err != nil || features.UnitLimit == 0 || features.UnitLimit == limit {
		return limit, ""
	}
	source = "feature table"
	if g.cs != nil {
		fromContent, err := session.ResolveCommunity(g.gameplay, session.CommunitySources{Content: g.cs.gameplayFeatures})
		if err == nil && fromContent.UnitLimit == features.UnitLimit {
			source = "content"
			if g.cs.mod != nil {
				source = g.cs.mod.Name
			}
		}
	}
	return features.UnitLimit, source
}

// unitLimitText is the loading screen's unit-limit field (§8.3): the
// effective limit, naming what set it when that is not the player's setting.
func (g *gameShell) unitLimitText() string {
	limit, source := g.effectiveUnitLimit()
	if source == "" {
		return fmt.Sprintf("Unit limit %d", limit)
	}
	return fmt.Sprintf("Unit limit %d (set by %s)", limit, source)
}
