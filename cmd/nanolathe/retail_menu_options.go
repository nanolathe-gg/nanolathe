package main

// The options family [07 R-FE-01 §6].
//
// `SINGLE`'s `Options` opens the options root as a child window over `SINGLE`;
// `PREV`/`CANCEL` pop it again [07 R-FE-01 §2]. The root is `STARTOPT.GUI`
// over the `options4x` background. Its four page buttons open their own `.GUI`
// with the merge flag: the page's gadgets are appended to the open window
// [07 R-FE-01 §6]. All four pages are built here — `SOUND`, `MUSIC`,
// `SPEEDS` (whose root button is captioned `INTERFACE`) and `VISUALS`.
//
// In battle the same routines run with one arm changed at each step: the root
// is `PREFS.GUI`, the four pages are the `…RT.GUI` variants, no page bitmap is
// installed at all, the root window is widened by 150 columns with a `PANEL`
// gadget synthesised over them, and gadgets whose names begin `MAP` or `VID`
// are hidden [07 R-FE-01 §6]. `ARMOPT`'s `PREFS` button is the only way in,
// and it has already set the pause bit [07 R-FE-01 §7].

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

const (
	retailOptionsGUI      = "guis/startopt.gui"
	retailOptionsBackdrop = "bitmaps/options4x.pcx"
	// retailBattleOptionsGUI is the in-battle root. It authors no background
	// bitmap and no `RESTORE`/`UNDO`; its four page buttons, `PREV` and
	// `CANCEL` are the same names the front-end root carries
	// [07 R-FE-01 §6].
	retailBattleOptionsGUI = "guis/prefs.gui"
	// retailBattleOptionsWiden is the column count the in-battle arm adds to
	// the root window before the first page merge [07 R-FE-01 §6]. On the
	// stock file it takes the window from 128 to 278 columns, which is the
	// width the unfold's saturation counter is measured against
	// [07 R-HUD-04 §2].
	retailBattleOptionsWiden = 150
	// retailBattleOptionsPanel is the name the merge searches the open window
	// for; the in-battle arm is the one that supplies it [07 R-FE-01 §6].
	retailBattleOptionsPanel = "PANEL"
)

// retailOptionsPage is one of the root's four page buttons: the authored `.GUI`
// it merges and the full-screen plate it hands to the bitmap cache.
//
// The background is Established as the page's own step: each of the four page
// routines calls the merge-flag window open and then, in the very next
// statement, hands its bitmap to the shared cache — it is not an argument of
// the opener and not the root's repaint. The in-battle arm of the same four
// routines opens the `…RT.GUI` variant and hands no bitmap at all, keeping the
// battle visible behind the page [07 R-FE-01 §6].
type retailOptionsPage struct {
	gui      string
	battle   string
	backdrop string
}

// retailOptionsPages is the root-button name to page mapping. The `SOUND`
// button opens `SOUNDS`, not the unopened `SOUND.GUI` beside it, and the
// `SPEEDS` button is the one the file captions `INTERFACE` [07 R-FE-01 §6].
var retailOptionsPages = map[string]retailOptionsPage{
	"sound":   {gui: "guis/sounds.gui", battle: "guis/soundsrt.gui", backdrop: "bitmaps/optsound4x.pcx"},
	"music":   {gui: "guis/music.gui", battle: "guis/musicrt.gui", backdrop: "bitmaps/optmusic4x.pcx"},
	"speeds":  {gui: "guis/speeds.gui", battle: "guis/speedsrt.gui", backdrop: "bitmaps/optinterface4x.pcx"},
	"visuals": {gui: "guis/visuals.gui", battle: "guis/visualrt.gui", backdrop: "bitmaps/optvisual4x.pcx"},
}

// source is the `.GUI` the named button merges: the front-end page, or the
// `…RT.GUI` variant in battle [07 R-FE-01 §6].
func (p retailOptionsPage) source(inBattle bool) string {
	if inBattle {
		return p.battle
	}
	return p.gui
}

// retailOptionsPageSource prefixes the provenance name of every gadget a
// merged page contributed, so opening another page can remove the previous
// page's controls before appending its own. Retail shows one page at a time;
// the removal is this build's bookkeeping for that, not an authored record.
//
// The page's own section name is kept after the prefix. Panel state is keyed
// by gadget name and the provenance name is what tells two same-named gadgets
// apart — `VISUALS.GUI` authors three separate labels all called `TEXT` — so
// collapsing them to one shared source would make all three show the first
// one's text.
const retailOptionsPageSource = "RETAIL_OPTIONS_PAGE:"

// retailOptionsPageGadget reports whether a gadget came from a merged page.
func retailOptionsPageGadget(gad gui.Gadget) bool {
	return strings.HasPrefix(gad.SourceName, retailOptionsPageSource)
}

// The options root is one window over the screen that opened it, so its state
// is a process singleton for the same reason the save/load dialog's is.
var (
	optionsPanel  *ui.Panel
	optionsAssets *retailPanelAssets
	optionsState  *retailOptionsState
)

// retailOptionsSnapshot is the entry copy the root takes when it opens: the
// display block, the audio block, the message-column block, the scroll speed,
// the game speed and the `Interface Type` word. `CANCEL` restores all of it and
// leaves; each page's `UNDO` restores the part that page writes and reopens the
// page [07 R-FE-01 §6].
//
// Retail's snapshot is the 83-byte preference block plus the session mapping
// and LOS bits, the game speed, the scroll speed, the mixer state and the
// 100-entry CD list. The two session bits are a battle's, so the front-end root
// has none to copy; the CD list is the per-track category array below.
//
// TODO(question): the in-battle root snapshots the session's mapping and
// line-of-sight bits as well, and `CANCEL` writes them back. No control on any
// of the four pages writes either bit — `GAMEOPTIONS.GUI` displays them
// read-only [07 R-FE-01 §7] — so nothing in this build can change them while
// the window is open and there is nothing for the restore to undo. What would
// settle it: a writer of either bit reachable from the options family; until
// one is found, copying them here would be two dead fields.
type retailOptionsSnapshot struct {
	display       settings.Display
	audio         settings.Audio
	messages      settings.Messages
	scrollSpeed   int
	gameSpeed     int
	interfaceType int
	categories    [retailMusicCategoryCount]int
}

// retailMusicCategoryCount is the length of the per-track category array: one
// byte per track index 0..99 [03 R-AUD-01 §4].
const retailMusicCategoryCount = 100

// retailOptionsState is the live options session: which page is merged, the
// entry snapshot `CANCEL` and `UNDO` restore, and the knob position of every
// kind-4 slider on the open page [07 R-FE-01 §6].
type retailOptionsState struct {
	page     string // "" for the root with no page merged
	snapshot retailOptionsSnapshot
	// Sliders are keyed by their window-record index. Their display names are
	// only for the bounded first-match lookup helpers; two records that happen
	// to share a name retain separate knob state [07 R-WGT-02 §2].
	sliders map[int]*retailSliderState
	drag    retailSliderDrag
	modes   []retailDisplayMode

	// inBattle selects the in-battle arm of every routine in this file: the
	// `PREFS.GUI` root, the `…RT.GUI` pages, no page bitmap, and the writes
	// that reach the running session rather than only the stored block
	// [07 R-FE-01 §6].
	inBattle bool
	// serviceStageIndex identifies the staged button only while
	// serviceStageActive marks a shared-widget handoff. Keyboard and direct
	// screen calls leave it clear and advance exactly once themselves.
	// callbackIndex retains the fired record through pointer and key callbacks.
	// Zero is the header, so it also represents a direct named invocation.
	callbackIndex      int
	serviceStageIndex  int
	serviceStageActive bool

	// tracks is the music object's audio-track count and track the `MUSIC`
	// page's selected track, 1-based, 0 when there is none. Retail keeps the
	// selection in a global beside the CD object rather than in the object
	// [03 R-AUD-01 §4].
	tracks int
	track  int
	// categories is the per-track category byte, initialised to the `(i mod
	// 4) + 1` cycle the CD object builds at first run and edited by
	// `TRACKTYPE` [03 R-AUD-01 §4].
	//
	// Not consumed: the category branch of the music tick is the reader, and
	// this build's music controller keeps its own copy of the same array with
	// no accessor, so an edit here changes what the page shows and nothing
	// else. There is also no store for it — retail persists the array in the
	// 20-entry `CDLISTS` ring keyed by the drive's volume serial, and this
	// build has neither a drive nor a serial.
	categories [retailMusicCategoryCount]int
}

// retailDefaultCategories is the cycle the CD object initialises its category
// array to before any disc is identified [03 R-AUD-01 §4].
func retailDefaultCategories() [retailMusicCategoryCount]int {
	var out [retailMusicCategoryCount]int
	for i := range out {
		out[i] = i%4 + 1
	}
	return out
}

// retailDisplayMode is one row of the table `VIDSLDR` indexes.
type retailDisplayMode struct{ W, H int }

// retailDisplayModes builds that table.
//
// Retail's windowed list and desktop gates are established [07 R-FE-02 §9].
// Nanolathe additionally offers 1280x720, 1600x900 and 1920x1080 as host
// presentation choices (DESIGN_PRESENTATION_CLIENT §2.1). These logical render
// sizes remain available on any desktop: fullscreen scales them to the monitor,
// whose device-independent dimensions need not match its physical pixel count.
//
// The options page still sorts by width then height and drops modes below
// 640x480 [07 R-FE-01 §6]. The original modes retain their desktop gates.
func retailDisplayModes(desktopW, desktopH int) []retailDisplayMode {
	modes := []retailDisplayMode{{640, 480}, {800, 600}, {1024, 768}, {1280, 720}, {1600, 900}, {1920, 1080}}
	if desktopW >= 1280 && desktopH >= 1024 {
		modes = append(modes, retailDisplayMode{1280, 1024})
	}
	if desktopW >= 1600 && desktopH >= 1200 {
		modes = append(modes, retailDisplayMode{1600, 1200})
	}
	sort.SliceStable(modes, func(a, b int) bool {
		if modes[a].W != modes[b].W {
			return modes[a].W < modes[b].W
		}
		return modes[a].H < modes[b].H
	})
	kept := modes[:0]
	for _, m := range modes {
		if m.W < settings.MinDisplaymodeWidth || m.H < settings.MinDisplaymodeHeight {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

// retailDisplayModeIndex is the slider position the stored size names. A size
// the table does not carry — a hand-edited settings file, or a desktop that
// shrank since the mode was chosen — falls back to the first row, which the
// filter above guarantees is 640x480.
func retailDisplayModeIndex(modes []retailDisplayMode, width, height int) int {
	for i, m := range modes {
		if m.W == width && m.H == height {
			return i
		}
	}
	return 0
}

// retailSliderState is one kind-4 slider's runtime position. `travel` and
// `knobSize` come from the SLIDERS art at open, `max` from the per-slider
// maximum of [07 R-FE-01 §6], and `knob` is the authored `knobpos` word.
type retailSliderState struct {
	knob     int
	travel   int
	knobSize int
	arrowW   int
	max      int
}

// retailSliderDrag is the pointer capture a press inside the knob starts
// [07 R-WGT-01 §5 "Pointer"].
type retailSliderDrag struct {
	active     bool
	index      int
	startCoord int
	startKnob  int
	// ended marks the pass on which the capture was freed. Release frees the
	// capture and ends the drag; it does not also run the synthesised arrow
	// step, even when the pointer has been dragged past the track's end
	// [07 R-WGT-01 §5 "Pointer"].
	ended bool
}

// retailSliderValue is the read-out the change callback computes on every knob
// move: `value = trunc(pos / (travel - 1) * max)`, and 0 when travel is under
// two [07 R-FE-01 §6 "slider arithmetic"].
func retailSliderValue(knob, travel, max int) int {
	if travel < 2 || max <= 0 {
		return 0
	}
	if knob < 0 {
		knob = 0
	}
	if knob > travel-1 {
		knob = travel - 1
	}
	// Truncation toward zero is the conversion, not rounding; every operand
	// here is non-negative so int() is that truncation.
	return int(float64(knob) / float64(travel-1) * float64(max))
}

// retailSliderKnob is the position the page open computes from a stored value:
// `x = min(value, max) * (travel - 1) / max`, `pos = trunc(x)`, and when
// `x - pos` is non-zero `pos = trunc(x + 1)` — a ceiling for a non-integral x,
// not a rounding. `VIDSLDR` divides by its maximum rather than multiplying by a
// stored reciprocal, because its maximum is the mode count and not a constant
// [07 R-FE-01 §6 "slider arithmetic"].
func retailSliderKnob(value, travel, max int) int {
	if travel < 2 || max <= 0 {
		return 0
	}
	if value > max {
		value = max
	}
	if value < 0 {
		value = 0
	}
	x := float64(value) * float64(travel-1) / float64(max)
	pos := int(x)
	if x-float64(pos) != 0 {
		pos = int(x + 1)
	}
	if pos > travel-1 {
		pos = travel - 1
	}
	return pos
}

// retailSliderMetrics is the kind-4 builder's synthesis for a horizontal
// slider carrying SLIDERS art: two arrow buttons are appended, the bar shrinks
// by twice the arrow width and shifts right by one arrow width, the knob takes
// the width of frame base+5, and `travel := w' - knobsize - 4` over the
// shrunken width [07 R-WGT-01 §5 "Synthesis at open"]. Frame base is 10 for a
// horizontal gadget.
//
// Vertical sliders keep their authored `range` as travel and are not built
// here: the only vertical kind-4 gadgets in the front end are list scrollbars,
// which take their travel from the list instead.
func (g *gameShell) retailSliderMetrics(gad gui.Gadget) (travel, knobSize, arrowW int, ok bool) {
	if g == nil || g.assets == nil || g.assets.common == nil {
		return 0, 0, 0, false
	}
	e, found := g.assets.common.Find("SLIDERS")
	if !found || len(e.Frames) < 20 {
		return 0, 0, 0, false
	}
	if gad.Rect.H >= gad.Rect.W {
		return 0, 0, 0, false
	}
	const base = 10
	knob := e.Frames[base+5].Frame
	arrow0 := e.Frames[base+6].Frame
	arrow1 := e.Frames[base+8].Frame
	if knob == nil || arrow0 == nil || arrow1 == nil {
		return 0, 0, 0, false
	}
	arrowW = int(arrow0.Width)
	if int(arrow1.Width) > arrowW {
		arrowW = int(arrow1.Width)
	}
	knobSize = int(knob.Width)
	travel = int(gad.Rect.W) - 2*arrowW - knobSize - 4
	if travel < 2 {
		return 0, 0, 0, false
	}
	return travel, knobSize, arrowW, true
}

// retailOptionsSlider returns the tracked slider for a gadget on the open
// options page, or nil. Every other kind-4 gadget stays on the list-scrollbar
// path.
func (g *gameShell) retailOptionsSlider(name string) *retailSliderState {
	if optionsPanel == nil {
		return nil
	}
	return g.retailOptionsSliderAt(optionsPanel.Index(name))
}

// retailOptionsSliderAt uses the caller's already-selected record. This is
// the callback path; it must not turn that record back into a name lookup.
func (g *gameShell) retailOptionsSliderAt(index int) *retailSliderState {
	if optionsState == nil || optionsPanel == nil || g == nil || g.activePanel() != optionsPanel {
		return nil
	}
	return optionsState.sliders[index]
}

// openRetailOptionsScreen builds the options root and pushes it over the
// current surface, snapshotting the preference block the way entering the root
// does [07 R-FE-01 §6].
//
// `inBattle` selects `PREFS.GUI` over `STARTOPT.GUI`, drops the background
// bitmap, widens the window by 150 columns with a synthesised `PANEL` over
// them, and hides the `MAP*` / `VID*` gadgets.
func (g *gameShell) openRetailOptionsScreen(inBattle bool) error {
	root := retailOptionsGUI
	if inBattle {
		root = retailBattleOptionsGUI
	}
	if g == nil || g.cs == nil || g.cs.fs == nil {
		return fmt.Errorf("nanolathe: options screen: no mounted content: logical path %s, providers searched [], expected the authored options root", root)
	}
	if clPtr != nil && clPtr.Input() != nil {
		clPtr.Input().DrainTokens()
	}
	window, err := g.cs.loadGUI(root)
	if err != nil {
		return retailFrontendAssetError(g.cs, "retail options GUI unavailable", root, "the authored options root", err)
	}
	// In battle no bitmap is installed at any step, so the battle stays visible
	// behind the window and behind every merged page [07 R-FE-01 §6].
	var background *formats.PCX
	if !inBattle {
		if background, err = formats.LoadPCXFile(g.cs.fs, retailOptionsBackdrop); err != nil {
			return retailFrontendAssetError(g.cs, "retail options bitmap", retailOptionsBackdrop, "the authored options background", err)
		}
	}
	if inBattle {
		widenRetailBattleOptionsRoot(window)
	}
	desktopW, desktopH := ebitenapp.DesktopSize()
	optionsAssets = &retailPanelAssets{window: window, background: background}
	optionsState = &retailOptionsState{
		sliders:           map[int]*retailSliderState{},
		modes:             retailDisplayModes(desktopW, desktopH),
		categories:        retailDefaultCategories(),
		inBattle:          inBattle,
		serviceStageIndex: -1,
	}
	// Retail's stored game speed and the session's requested speed are one
	// word: the `GAME` slider reads it, the speed setter writes it, and the
	// battle's own speed keys write the same one [07 R-CAM-01 §3]
	// [07 R-CAM-01 §7]. This build keeps them apart, because the shell's word
	// outlives any session, so the in-battle root adopts the live value on
	// entry. That is what makes the knob open over the speed the battle is
	// actually running and the entry snapshot restore that speed rather than a
	// stale one.
	if inBattle {
		if b := g.battleOptionsSession(); b != nil && b.sess != nil && b.sess.Clock != nil {
			g.gameSpeed = int(b.sess.Clock.Requested)
		}
	}
	optionsState.snapshot = g.retailOptionsSnapshot()
	optionsState.tracks = g.retailMusicTrackCount()
	if optionsState.tracks > 0 {
		// A nonzero count sets the next track to 1, which is what the `MUSIC`
		// page then shows as its selection [03 R-AUD-01 §4].
		optionsState.track = 1
	}
	// The in-battle arm hides every gadget whose name begins `MAP` or `VID`
	// before the window is built, so the hidden state is what the panel records
	// [07 R-FE-01 §6]. On the stock files nothing matches: the in-battle
	// visuals page authors neither `VIDSLDR` nor `VIDVAL`, and no page in the
	// family authors a `MAP…` control. The pass runs anyway, because the names
	// are what retail tests and an install whose page authors one would hide it.
	if inBattle {
		hideRetailBattleOptionsGadgets(window)
	}
	g.installRetailWindowButtonArt(window, nil)
	optionsPanel = ui.NewPanel(window)
	if optionsPanel == nil {
		return retailFrontendAssetError(g.cs, "retail options GUI unavailable", root, "the authored options root", nil)
	}
	// `MUSIC` is greyed when the CD object never opened. This build's music
	// object is the audio service's controller, so the arm the grey test names
	// is reachable only when the service itself could not be built — a missing
	// mount — and never merely because no tracks were found: a drive with no
	// disc still opens, and the page shows `NO DISC` [07 R-FE-01 §6]
	// [03 R-AUD-01 §4].
	if g.retailMusicController() == nil {
		retailGreyGadget(window, "MUSIC", true)
	}
	g.frontend.Panels.Push(optionsPanel)
	return nil
}

// widenRetailBattleOptionsRoot applies the in-battle arm's two window edits:
// the root grows by 150 columns and a `PANEL` gadget is synthesised over them
// [07 R-FE-01 §6]. The synthesised gadget is what makes every later page merge
// take the centring branch instead of the origin-add branch — the branch the
// front-end root never reaches, because `STARTOPT.GUI` authors no `PANEL`.
//
// TODO(question): the rectangle the opener writes into the synthesised record
// is not traced. This build gives it the new columns beside the root's own
// plate — x at the authored width, and the plate's own y and height — because
// the traced centring formula then places each stock `…RT.GUI` page at exactly
// its own authored header origin on both axes: `(150−150)/2 + 128 = 128` and
// `(352−352)/2 + 2 = 2`, which with the window origin is the page header's
// (128, 128). Two coordinates agreeing exactly is why this shape was chosen
// over any other; what would settle it is the rectangle the opener writes.
func widenRetailBattleOptionsRoot(window *gui.Window) {
	if window == nil || len(window.Gadgets) == 0 {
		return
	}
	panel := gui.Rect{X: window.Rect.W, Y: 0, W: retailBattleOptionsWiden, H: window.Rect.H}
	// The root's own plate is its single picture box (`IGOPT` on the stock
	// file); the new columns sit beside it and share its vertical extent.
	for _, gad := range window.Gadgets[1:] {
		if gad.Kind == gui.KindPicture {
			panel.Y, panel.H = gad.Rect.Y, gad.Rect.H
			break
		}
	}
	window.Rect.W += retailBattleOptionsWiden
	window.Gadgets[0].Rect.W += retailBattleOptionsWiden
	window.Gadgets = append(window.Gadgets, gui.Gadget{
		Kind:       gui.KindPanel,
		Name:       retailBattleOptionsPanel,
		SourceName: retailBattleOptionsPanel,
		Rect:       panel,
		Active:     1,
	})
}

// retailOptionsPanelRect returns the open window's `PANEL` rectangle and
// whether it has one. The merge searches the open window's gadgets from index
// 1 for the name [07 R-FE-01 §6].
func retailOptionsPanelRect(window *gui.Window) (gui.Rect, int, bool) {
	if window == nil {
		return gui.Rect{}, -1, false
	}
	if i := window.GadgetIndex(retailBattleOptionsPanel); i >= 0 {
		return window.Gadgets[i].Rect, i, true
	}
	return gui.Rect{}, -1, false
}

// hideRetailBattleOptionsGadgets clears the active byte of every gadget whose
// name begins `MAP` or `VID`, which is the in-battle arm's last window edit
// [07 R-FE-01 §6].
func hideRetailBattleOptionsGadgets(window *gui.Window) {
	if window == nil {
		return
	}
	for i := 1; i < len(window.Gadgets); i++ {
		name := gui.GadgetName(window.Gadgets[i].Name)
		if strings.HasPrefix(name, "MAP") || strings.HasPrefix(name, "VID") {
			window.Gadgets[i].Active = 0
		}
	}
}

// retailOptionsSnapshot copies the live preference values the options root
// snapshots on entry [07 R-FE-01 §6].
func (g *gameShell) retailOptionsSnapshot() retailOptionsSnapshot {
	s := retailOptionsSnapshot{
		display:       g.display,
		audio:         g.audioPrefs,
		messages:      g.messages,
		scrollSpeed:   g.scrollSpeed,
		gameSpeed:     g.gameSpeed,
		interfaceType: g.interfaceType,
		categories:    retailDefaultCategories(),
	}
	if optionsState != nil {
		s.categories = optionsState.categories
	}
	return s
}

// restoreRetailOptionsSnapshot writes the entry copy back and re-applies
// everything it drives. `CANCEL` restores the snapshot and re-applies gamma and
// the volumes before it leaves [07 R-FE-01 §6].
func (g *gameShell) restoreRetailOptionsSnapshot(s retailOptionsSnapshot) {
	g.display = s.display
	g.audioPrefs = s.audio
	g.messages = s.messages
	g.scrollSpeed = s.scrollSpeed
	g.gameSpeed = s.gameSpeed
	g.interfaceType = s.interfaceType
	if optionsState != nil {
		optionsState.categories = s.categories
	}
	g.applyRetailVisualOptions(clPtr)
	applyGammaOption(clPtr, g.display.Gamma)
	g.applyRetailAudioOptions()
	// In battle the restored game speed, scroll speed and message-column
	// values have live consumers, so the same writes the page made have to be
	// taken back from them too [07 R-CAM-01 §3][07 R-CAM-01 §7][07 §10].
	g.applyRetailOptionsToBattle()
}

// retailGreyGadget sets or clears one gadget's greyed word by name. "Greyed" is
// bit 0 of a per-gadget word that is not `attribs`; a greyed control rejects
// its own hit test and never captures [07 R-WGT-01 §13].
func retailGreyGadget(window *gui.Window, name string, greyed bool) {
	if window == nil {
		return
	}
	i := window.GadgetIndex(name)
	if i < 0 {
		return
	}
	if greyed {
		window.Gadgets[i].GrayedOut = 1
	} else {
		window.Gadgets[i].GrayedOut = 0
	}
}

// openRetailOptionsScreenReporting is the front-end caller: `SINGLE`'s
// `Options` [07 R-FE-01 §2]. The in-battle caller is `ARMOPT`'s `PREFS`, in
// battle_options.go.
func (g *gameShell) openRetailOptionsScreenReporting() {
	g.openRetailOptionsScreenReportingIn(false)
}

func (g *gameShell) openRetailOptionsScreenReportingIn(inBattle bool) {
	if err := g.openRetailOptionsScreen(inBattle); err != nil {
		optionsPanel, optionsAssets, optionsState = nil, nil, nil
		reportRetailMessageError(g.showRetailMessage(err.Error()))
	}
}

// closeRetailOptionsScreen pops the root and drops the page.
func (g *gameShell) closeRetailOptionsScreen() {
	if g != nil && optionsPanel != nil && g.frontend.Panels.Top() == optionsPanel {
		g.frontend.Panels.Pop()
	}
	optionsPanel = nil
	optionsAssets = nil
	optionsState = nil
}

// retailOptionsActive reports whether the options root is the active panel.
func (g *gameShell) retailOptionsActive() bool {
	return g != nil && optionsState != nil && optionsPanel != nil && g.activePanel() == optionsPanel
}

// openRetailOptionsPage merges one page into the open options window. The
// page's gadgets are appended to the root's [07 R-FE-01 §6].
//
// Two placements, chosen by whether the open window carries a gadget named
// `PANEL` [07 R-FE-01 §6]:
//
//   - No `PANEL` — the front-end arm, since `STARTOPT.GUI` authors none. The
//     opener adds the page header's own x and y to each of the page's control
//     rectangles before appending them, and the root's origin is then added at
//     draw time as it is for any gadget. `SOUNDS.GUI` is authored at (0,1) and
//     moves its whole page down a pixel; the other three pages are at (0,0).
//   - A `PANEL` — the in-battle arm, which synthesised the gadget at open. The
//     panel's own active byte is zeroed, and each page gadget is offset by
//     `(panel.w − page.w)/2 + panel.x` and `(panel.h − page.h)/2 + panel.y`, a
//     signed C divide that truncates toward zero rather than an arithmetic
//     shift, so an odd difference biases toward the origin on both signs.
//
// The page header (index 0) is the page's own window record, not a control; it
// is discarded either way, because the merged list belongs to the root window
// from here on and only the root's origin is applied when the panel is drawn.
func (g *gameShell) openRetailOptionsPage(page string) {
	if !g.retailOptionsActive() || optionsAssets == nil || optionsAssets.window == nil {
		return
	}
	source, ok := retailOptionsPages[page]
	if !ok {
		return
	}
	inBattle := optionsState.inBattle
	pageGUI := source.source(inBattle)
	pageWindow, err := g.cs.loadGUI(pageGUI)
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(
			retailFrontendAssetError(g.cs, "retail options page GUI unavailable", pageGUI, "the authored options page", err).Error()))
		return
	}
	// In battle the page routine hands no bitmap to the cache at all, so the
	// battle stays visible behind the page [07 R-FE-01 §6].
	var background *formats.PCX
	if !inBattle {
		if background, err = formats.LoadPCXFile(g.cs.fs, source.backdrop); err != nil {
			reportRetailMessageError(g.showRetailMessage(
				retailFrontendAssetError(g.cs, "retail options page bitmap", source.backdrop, "the authored options page background", err).Error()))
			return
		}
	}

	root := optionsAssets.window
	panelRect, panelIndex, centred := retailOptionsPanelRect(root)
	dx, dy := pageWindow.OriginX, pageWindow.OriginY
	if centred {
		dx = (panelRect.W-pageWindow.Rect.W)/2 + panelRect.X
		dy = (panelRect.H-pageWindow.Rect.H)/2 + panelRect.Y
		root.Gadgets[panelIndex].Active = 0
	}
	kept := make([]gui.Gadget, 0, len(root.Gadgets)+len(pageWindow.Gadgets))
	for _, gad := range root.Gadgets {
		if retailOptionsPageGadget(gad) {
			continue
		}
		kept = append(kept, gad)
	}
	for _, gad := range pageWindow.Gadgets[1:] {
		gad.SourceName = retailOptionsPageSource + gad.SourceName
		gad.Rect.X += dx
		gad.Rect.Y += dy
		kept = append(kept, gad)
	}
	root.Gadgets = kept
	if inBattle {
		hideRetailBattleOptionsGadgets(root)
	}
	g.installRetailWindowButtonArt(root, nil)
	optionsAssets.background = background
	optionsState.page = page
	optionsState.sliders = map[int]*retailSliderState{}

	// The panel is rebuilt over the widened gadget list, then the entry values
	// are pushed into it: after the page opens every slider's value callback
	// runs once so the labels match [07 R-FE-01 §6].
	rebuilt := ui.NewPanel(root)
	if rebuilt == nil {
		return
	}
	if g.frontend.Panels.Top() == optionsPanel {
		g.frontend.Panels.Pop()
	}
	optionsPanel = rebuilt
	g.frontend.Panels.Push(optionsPanel)
	flushWindowTokens(clPtr)
	g.refreshRetailOptionsPage()
}

// retailSliderMax is the runtime maximum the page opener writes into one
// slider's record, and whether the named gadget is an options slider at all.
//
// Every maximum but `VIDSLDR`'s is a stored constant the opener writes
// verbatim; `VIDSLDR`'s is the display-mode table's count minus one, computed
// out of the table the opener has just built [07 R-FE-01 §6][07 R-CAM-01 §7]
// [03 R-AUD-01 §2].
func (g *gameShell) retailSliderMax(key string) (int, bool) {
	switch key {
	case "vidsldr":
		return len(optionsState.modes) - 1, true
	case "gamma":
		return settings.MaxGamma, true
	case "fxvol":
		return settings.MaxFXVol, true
	case "musicvol":
		return settings.MaxMusicVol, true
	case "game":
		return settings.GameSliderMax, true
	case "screen":
		return settings.ScrollSliderMax, true
	case "txtscrol":
		return settings.TextScrollSliderMax, true
	case "maxlines":
		return settings.MaxLinesSliderMax, true
	}
	return 0, false
}

// retailSliderStoredValue is the preference each slider opens over.
func (g *gameShell) retailSliderStoredValue(key string) int {
	switch key {
	case "vidsldr":
		return retailDisplayModeIndex(optionsState.modes, g.display.Width, g.display.Height)
	case "gamma":
		return g.display.Gamma
	case "fxvol":
		return g.audioPrefs.FXVol
	case "musicvol":
		return g.audioPrefs.MusicVol
	case "game":
		return g.gameSpeed
	case "screen":
		return g.scrollSpeed
	case "txtscrol":
		return g.messages.TextScroll
	case "maxlines":
		return g.messages.TextLines
	}
	return 0
}

// refreshRetailOptionsPage installs every page control's state from the live
// preference block and runs each slider's value callback once, which is what
// the page open does [07 R-FE-01 §6].
func (g *gameShell) refreshRetailOptionsPage() {
	if !g.retailOptionsActive() {
		return
	}
	p := optionsPanel
	optionsState.sliders = map[int]*retailSliderState{}
	// Gadget order, not map order: the value callbacks below run in the order
	// the opener walks the window's gadget array [07 R-FE-01 §6] [I1].
	order := make([]int, 0, len(p.Window.Gadgets))
	for i, gad := range p.Window.Gadgets {
		if !retailOptionsPageGadget(gad) || gad.Kind != gui.KindScrollBar {
			continue
		}
		key, trackedName := retailSliderKey(gui.CallbackName(gad.Name))
		if !trackedName {
			continue
		}
		max, tracked := g.retailSliderMax(key)
		if !tracked {
			continue
		}
		travel, knobSize, arrowW, ok := g.retailSliderMetrics(gad)
		if !ok {
			continue
		}
		optionsState.sliders[i] = &retailSliderState{
			travel:   travel,
			knobSize: knobSize,
			arrowW:   arrowW,
			max:      max,
			knob:     retailSliderKnob(g.retailSliderStoredValue(key), travel, max),
		}
		order = append(order, i)
	}
	switch optionsState.page {
	case "visuals":
		// Two-stage buttons: stage 1 is the "On" label of the authored `Off|On`
		// pair. `ANTI`, `BSHADOWS` and `SHADING` are bits 1, 4 and 5 of the
		// display option word, and `BSHADOWS` drives all three shadow values
		// together [07 R-FE-01 §6].
		p.SetStageAt(p.Index("ANTI"), boolInt(g.display.AntiAlias != 0))
		p.SetStageAt(p.Index("SHADING"), boolInt(g.display.Shading != 0))
		p.SetStageAt(p.Index("BSHADOWS"), boolInt(g.display.FeatureShadows != 0))
		g.syncRetailVideoLabel()
	case "sound":
		g.syncRetailSoundPage()
	case "music":
		g.syncRetailMusicPage()
	case "speeds":
		// `UNITCHAT` is the acknowledgement **text** gauge, displayed as the
		// stored byte divided by five; `LEFTCLICK` shows the `Interface Type`
		// word directly [07 R-CAM-01 §7][07 R-CAM-01 §5].
		p.SetStageAt(p.Index("UNITCHAT"), g.messages.UnitChatText/5)
		p.SetStageAt(p.Index("LEFTCLICK"), g.interfaceType)
		g.syncRetailMaxLinesLabel()
	}
	// After the page opens, every kind-4 gadget's value callback runs once, so
	// the labels and the stored values match the knobs the opener just placed
	// [07 R-FE-01 §6].
	for _, index := range order {
		g.commitRetailSliderValue(index, optionsState.sliders[index])
	}
}

// syncRetailVideoLabel writes the `VIDVAL` read-out. Retail formats it as
// `%d X %d` [07 R-FE-01 §6].
func (g *gameShell) syncRetailVideoLabel() {
	if optionsPanel == nil {
		return
	}
	optionsPanel.SetText("VIDVAL", fmt.Sprintf("%d X %d", g.display.Width, g.display.Height))
}

// syncRetailMaxLinesLabel writes the two interface-page read-outs.
//
// `MAXLINES` writes `%d`, or the translated `None` at zero, into a gadget named
// `MAXLINESTEXT`; `TXTSCROL` writes `%d secs` into one named `TEXTSCROLLTEXT`
// [07 R-CAM-01 §7]. Neither `SPEEDS.GUI` nor `SPEEDSRT.GUI` authors a gadget of
// either name, so on the stock files the setter finds nothing and the read-outs
// are never drawn. The writes are made anyway, because the name is what retail
// hands the setter and an install whose page authors the gadget would show it.
func (g *gameShell) syncRetailMaxLinesLabel() {
	if optionsPanel == nil {
		return
	}
	text := retailNoneText
	if g.messages.TextLines != 0 {
		text = fmt.Sprintf("%d", g.messages.TextLines)
	}
	optionsPanel.SetText("MAXLINESTEXT", text)
	optionsPanel.SetText("TEXTSCROLLTEXT", fmt.Sprintf("%d secs", g.messages.TextScroll))
}

// retailNoneText is the zero-line label of the `MAXLINES` read-out
// [07 R-CAM-01 §7].
const retailNoneText = "None"

// syncRetailSoundPage installs the `SOUNDS` page's non-slider state.
//
// `MODE` shows the sound-flags byte's low three bits; `SPEECH` shows the
// acknowledgement voice gauge divided by five, or `Off` when the `speechfx` bit
// is clear. With the mode `Off` the `VOLTEXT` caption is deactivated and
// `FXVOL`, `TEST` and `SPEECH` are greyed [03 R-AUD-01 §2].
func (g *gameShell) syncRetailSoundPage() {
	p := optionsPanel
	if p == nil || optionsAssets == nil {
		return
	}
	p.SetStageAt(p.Index("MODE"), g.audioPrefs.SoundMode)
	speech := 0
	if g.audioPrefs.SpeechFX != 0 {
		speech = g.audioPrefs.UnitChat / 5
	}
	p.SetStageAt(p.Index("SPEECH"), speech)
	off := !g.audioPrefs.SoundEnabled()
	p.SetActive("VOLTEXT", !off)
	for _, name := range []string{"FXVOL", "TEST", "SPEECH"} {
		retailGreyGadget(optionsAssets.window, name, off)
	}
}

// syncRetailMusicPage installs the `MUSIC` page's non-slider state.
//
// `NOTRAK` shows `musicmode` bit 0 and `TRACKMODE` shows `cdmode` minus one.
// With music off the gauge, the four transport buttons and `TRACKMODE` are
// disabled; `TRACKTYPE` is active only while music is on **and** the mode is
// `Custom` [03 R-AUD-01 §4].
func (g *gameShell) syncRetailMusicPage() {
	p := optionsPanel
	if p == nil || optionsAssets == nil {
		return
	}
	on := g.audioPrefs.MusicMode != 0
	p.SetStatus("NOTRAK", boolInt(on))
	p.SetStageAt(p.Index("TRACKMODE"), g.audioPrefs.CDMode-1)
	for _, name := range []string{"MUSICVOL", "CDPREV", "CDSTOP", "CDPLAY", "CDNEXT", "TRACKMODE"} {
		retailGreyGadget(optionsAssets.window, name, !on)
	}
	retailGreyGadget(optionsAssets.window, "TRACKTYPE", !(on && g.audioPrefs.CDMode == settings.MaxCDMode))
	p.SetStageAt(p.Index("TRACKTYPE"), retailTrackCategory(optionsState.track))
	g.syncRetailTrackLabel()
}

// retailTrackCategory reads one track's category byte. Track 0 is "no
// selection" and has no entry [03 R-AUD-01 §4].
func retailTrackCategory(track int) int {
	if optionsState == nil || track < 1 || track > retailMusicCategoryCount {
		return 0
	}
	return optionsState.categories[track-1]
}

// syncRetailTrackLabel writes the `TRACKNUM` read-out: the selected track as
// `%d`, or `NO DISC` when the selection is 0 [03 R-AUD-01 §4].
func (g *gameShell) syncRetailTrackLabel() {
	if optionsPanel == nil || optionsState == nil {
		return
	}
	if optionsState.track == 0 {
		optionsPanel.SetText("TRACKNUM", retailNoDiscText)
		return
	}
	optionsPanel.SetText("TRACKNUM", fmt.Sprintf("%d", optionsState.track))
}

// retailNoDiscText is the `TRACKNUM` label with no selection [03 R-AUD-01 §4].
const retailNoDiscText = "NO DISC"

// retailMusicController is this build's stand-in for the CD object the options
// family queries: the shared audio service's music controller. It is nil only
// when no audio service could be built at all.
func (g *gameShell) retailMusicController() *audio.Controller {
	svc := g.ensureFrontendAudio()
	if svc == nil {
		return nil
	}
	return svc.Music
}

// retailMusicTrackCount is the audio-track count the options root reads when it
// opens, the analogue of retail's `status cdaudio number of tracks`
// [03 R-AUD-01 §4]. This build's tracks are the authored music files the VFS
// carries, so a stock install — whose music lived on the CD — reports zero and
// the page shows `NO DISC`.
func (g *gameShell) retailMusicTrackCount() int {
	if g == nil || g.cs == nil || g.cs.fs == nil {
		return 0
	}
	if c := g.retailMusicController(); c != nil && c.NumTracks() > 0 {
		return c.NumTracks()
	}
	return audio.ProbeMusicTracks(g.cs.fs)
}

// retailWaveVolumeScale converts a stored gauge into the presentation backend's
// effects scale.
//
// Retail pushes `waveOutSetVolume(dev, (v << 10) · 0x10001)` to every waveOut
// device, clamped to `0..0xFFFF` per channel — the system wave mixer, not a
// per-buffer attenuation [03 R-AUD-01 §2]. Nanolathe owns no system mixer, so
// the same level is applied as the backend's own 0..1 output scale: the gauge's
// maximum of 64 saturates the 16-bit word exactly as it does there.
func retailWaveVolumeScale(v int) float64 {
	if v <= 0 {
		return 0
	}
	level := v << 10
	if level > 0xFFFF {
		level = 0xFFFF
	}
	return float64(level) / float64(0xFFFF)
}

// applyRetailAudioOptions pushes the wave gates and selected Sound Mode into
// the presentation backend and the music level into the music controller.
//
// The two gates are retail's own: every play requires a nonzero sound mode and
// a nonzero `fxvol`; the exact value 2 also selects positional 3-D, while all
// other values select Mono placement [03 R-AUD-01 §2][03 R-AUD-01 §1].
func (g *gameShell) applyRetailAudioOptions() {
	applyRetailAudioOptions(g.audioPrefs)
	if c := g.retailMusicController(); c != nil {
		c.SetVolume(g.audioPrefs.MusicVol)
	}
}

// applyRetailAudioOptions is the shell-free half, so the startup read can push
// the gates before any screen exists. It takes the block rather than reading a
// shell field because the startup read runs before the shell owns one.
func applyRetailAudioOptions(a settings.Audio) {
	audio.ConfigureOutput(audio.OutputConfig{
		MasterEnabled: a.SoundEnabled(),
		EffectsVolume: retailWaveVolumeScale(a.FXVol),
		SoundMode:     audio.SpatialModeFromPreference(a.SoundMode),
		MixingBuffers: a.MixingBuffers,
	})
}

// retailCycleStage advances one staged button by a stage, wrapping.
func retailCycleStage(value, stages int) int {
	return cycleInt(clampMenuStage(value, stages), 0, stages-1, 1)
}

// playRetailSoundTest is the sound page's `TEST` button: it plays
// `sounds\explode.wav` under the ordinary gates, and plays no family cue
// [03 R-AUD-01 §2].
func (g *gameShell) playRetailSoundTest() {
	svc := g.ensureFrontendAudio()
	if svc == nil || svc.Cache == nil || !g.audioPrefs.SoundEnabled() || g.audioPrefs.FXVol == 0 {
		return
	}
	sample, err := svc.Cache.LoadPath(retailTestSound)
	if err != nil || sample == nil {
		return
	}
	if output := audio.GlobalOutput(); output != nil {
		_ = output.PlaySample(sample, audio.VolumeFromCentibel(retailTestAttenuation), 0)
	}
}

// retailTestAttenuation is the `TEST` sample's attenuation argument
// [03 R-AUD-01 §2].
const retailTestAttenuation = -585

// setRetailMusicEnabled applies the `NOTRAK` toggle: disabling stops the music
// object and resets it [03 R-AUD-01 §4].
func (g *gameShell) setRetailMusicEnabled(on bool) {
	c := g.retailMusicController()
	if c == nil {
		return
	}
	c.SetEnabled(on)
	if !on {
		c.Stop()
	}
}

// applyRetailMusicMode applies `cdmode` to the music object. `Repeat` copies the
// current selection into the requested track [03 R-AUD-01 §4].
func (g *gameShell) applyRetailMusicMode() {
	c := g.retailMusicController()
	if c == nil {
		return
	}
	c.Configure(audio.PlayMode(g.audioPrefs.CDMode), retailTrackCategory(optionsState.track))
	if g.audioPrefs.CDMode == 3 && optionsState.track > 0 {
		c.Play(optionsState.track)
	}
}

// activateRetailMusicTransport is the four transport buttons. `CDPLAY` plays the
// selection; `CDNEXT`/`CDPREV` step it with wrap over `1..count` and switch
// immediately while playing; `CDSTOP` stops, resets and re-selects track 1
// [03 R-AUD-01 §4].
func (g *gameShell) activateRetailMusicTransport(key string) {
	c := g.retailMusicController()
	count := optionsState.tracks
	switch key {
	case "cdplay":
		if c != nil && optionsState.track > 0 {
			c.Play(optionsState.track)
		}
	case "cdnext":
		if count > 0 {
			optionsState.track++
			if optionsState.track > count {
				optionsState.track = 1
			}
		}
	case "cdprev":
		if count > 0 {
			optionsState.track--
			if optionsState.track < 1 {
				optionsState.track = count
			}
		}
	case "cdstop":
		if c != nil {
			c.Stop()
		}
		optionsState.track = 0
		if count > 0 {
			optionsState.track = 1
		}
	}
	if c != nil && (key == "cdnext" || key == "cdprev") && c.IsPlaying() && optionsState.track > 0 {
		c.Play(optionsState.track)
	}
	g.syncRetailMusicPage()
}

// restoreRetailOptionsDefaults is every page's `RESTORE`. Each page restores
// only the values its own controls write, then reopens itself
// [07 R-FE-01 §6][03 R-AUD-01 §2][03 R-AUD-01 §4][07 R-CAM-01 §7].
func (g *gameShell) restoreRetailOptionsDefaults() {
	switch optionsState.page {
	case "visuals":
		// Bits 1-5 set, gamma 12 and — front end only — 640x480 with
		// `DitheredFog` cleared [07 R-FE-01 §6]. The size pair is the front
		// end's alone: the in-battle page authors no `VIDSLDR`,
		// and resizing the surface out from under a running battle is not
		// something the in-battle arm does.
		g.display.AntiAlias = 1
		g.setRetailShadowBits(true)
		g.display.Shading = 1
		g.display.Gamma = settings.DefaultGamma
		if !optionsState.inBattle {
			g.display.Width = settings.DefaultDisplaymodeWidth
			g.display.Height = settings.DefaultDisplaymodeHeight
			g.display.DitheredFog = settings.DefaultDitheredFog
		}
		g.applyRetailVisualOptions(clPtr)
		applyGammaOption(clPtr, g.display.Gamma)
	case "sound":
		// `fxvol` 27, bits 4-6 set, Sound Mode 1 with the 3-D flag cleared,
		// and the acknowledgement voice level 10 [03 R-AUD-01 §2].
		g.audioPrefs.FXVol = settings.DefaultFXVol
		g.audioPrefs.AckFX = 1
		g.audioPrefs.BuildFX = 1
		g.audioPrefs.SpeechFX = 1
		g.audioPrefs.SoundMode = settings.SoundModeMono
		g.audioPrefs.UnitChat = settings.MaxUnitChat
		g.applyRetailAudioOptions()
	case "music":
		// `musicvol` 32, `cdmode` 4, and music turned on [03 R-AUD-01 §4].
		g.audioPrefs.MusicVol = settings.DefaultMusicVol
		g.audioPrefs.CDMode = settings.DefaultCDMode
		if g.audioPrefs.MusicMode == 0 {
			g.audioPrefs.MusicMode = 1
			g.setRetailMusicEnabled(true)
		}
		g.applyRetailAudioOptions()
		g.applyRetailMusicMode()
	case "speeds":
		// text-scroll 10, lines 10, game speed 10, scroll speed 32,
		// `Interface Type` 0, voice level 10, text level 5 [07 R-CAM-01 §7].
		g.messages.TextScroll = settings.DefaultTextScroll
		g.messages.TextLines = settings.DefaultTextLines
		g.gameSpeed = settings.DefaultGameSpeed
		g.scrollSpeed = settings.DefaultScrollSpeed
		g.interfaceType = settings.DefaultInterfaceType
		g.audioPrefs.UnitChat = settings.MaxUnitChat
		g.messages.UnitChatText = settings.DefaultUnitChatText
	default:
		return
	}
	g.openRetailOptionsPage(optionsState.page)
}

// undoRetailOptionsPage is every page's `UNDO`: the values that page writes are
// taken back from the entry snapshot and the page reopens [07 R-FE-01 §6].
func (g *gameShell) undoRetailOptionsPage() {
	s := optionsState.snapshot
	switch optionsState.page {
	case "visuals":
		// Bits 1-6, gamma and — front end only — the display size
		// [07 R-FE-01 §6].
		width, height := g.display.Width, g.display.Height
		g.display = s.display
		if optionsState.inBattle {
			g.display.Width, g.display.Height = width, height
		}
		g.applyRetailVisualOptions(clPtr)
		applyGammaOption(clPtr, g.display.Gamma)
	case "sound":
		g.audioPrefs.SoundMode = s.audio.SoundMode
		g.audioPrefs.AckFX, g.audioPrefs.BuildFX, g.audioPrefs.SpeechFX = s.audio.AckFX, s.audio.BuildFX, s.audio.SpeechFX
		g.audioPrefs.FXVol = s.audio.FXVol
		g.audioPrefs.UnitChat = s.audio.UnitChat
		g.applyRetailAudioOptions()
	case "music":
		// Volume, list, mode, enable and requested track [03 R-AUD-01 §4].
		g.audioPrefs.MusicVol = s.audio.MusicVol
		g.audioPrefs.MusicMode = s.audio.MusicMode
		g.audioPrefs.CDMode = s.audio.CDMode
		optionsState.categories = s.categories
		g.setRetailMusicEnabled(g.audioPrefs.MusicMode != 0)
		g.applyRetailAudioOptions()
		g.applyRetailMusicMode()
	case "speeds":
		g.messages.TextScroll = s.messages.TextScroll
		g.messages.TextLines = s.messages.TextLines
		g.messages.UnitChatText = s.messages.UnitChatText
		g.scrollSpeed = s.scrollSpeed
		g.gameSpeed = s.gameSpeed
		g.interfaceType = s.interfaceType
	default:
		return
	}
	g.openRetailOptionsPage(optionsState.page)
}

// applyRetailVisualOptions pushes the display-option bits into the live
// presentation. `BSHADOWS` copies bit 4 into bit 3 and bit 3 into bit 2, so one
// control drives `FeatureShadows`, `VehicleShadows` and `Shadows`
// [07 R-FE-01 §6][03 §5.3].
func (g *gameShell) applyRetailVisualOptions(cl *client.Client) {
	if g == nil || cl == nil {
		return
	}
	applyVisualOptions(cl, g.display)
}

// applyVisualOptions is the one place the display-option bits reach a
// client. The windowed shell calls it from the VISUALS page and at start-up;
// the --shot capture calls it with the stored block so a capture composes
// under the same Anti_Alias and Shading bits the window would, which is what
// lets a settings file drive the parity matrix of docs/DESIGN_GPU_RENDERER.md
// §6 [07 R-FE-01 §6][03 §5.3].
func applyVisualOptions(cl *client.Client, d settings.Display) {
	if cl == nil {
		return
	}
	cl.SetAntiAlias(d.AntiAlias != 0)
	cl.SetFeatureShadows(d.FeatureShadows != 0)
	cl.SetShadowOptions(d.Shadows != 0, d.VehicleShadows != 0, d.Shading != 0)
	cl.SetDitheredFog(d.DitheredFogEnabled())
	cl.SetGlow(d.Glow != 0)
}

// setRetailShadowBits is the `BSHADOWS` write: bit 4 takes the stage, bit 3
// takes bit 4 and bit 2 takes bit 3, so all three end at the stage
// [07 R-FE-01 §6].
func (g *gameShell) setRetailShadowBits(on bool) {
	value := boolInt(on)
	g.display.FeatureShadows = value
	g.display.VehicleShadows = g.display.FeatureShadows
	g.display.Shadows = g.display.VehicleShadows
}

// activateRetailOptionsGadget resolves a control on the options root or its
// merged page. It is consulted before the screen underneath, because the
// options root is a child window over that screen [07 R-FE-01 §2].
// retailOptionsCue is the cue column of the whole options family — the root's
// `STARTOPT` / `PREFS` rows [07 R-FE-01 §2] and the merged pages' own controls
// [07 R-FE-01 §6]. `CANCEL` alone plays `Previous`; every other control the
// four page callbacks recognise, `RESTORE` and `UNDO` included, plays
// `Options`.
//
// Two controls are silent. The pages' sliders are driven by their value
// callbacks, which play nothing, so a drag or an arrow step makes no sound; and
// the sound page's `TEST` plays `sounds\explode.wav` through the ordinary gates
// instead of the family cue [03 R-AUD-01 §2].
//
// It is a table of its own rather than an arm of frontendCue because that
// function keys on the shell mode, and the options root has no mode: it is a
// child window over whichever screen opened it, which stays the current mode
// while it is up. The cue still runs where every other one does — in the screen
// handler that consumes the fired result [07 R-WGT-01 §3].
func retailOptionsCue(key string) string {
	switch key {
	case "sound", "music", "speeds", "visuals", "prev",
		"restore", "undo",
		"anti", "shading", "bshadows",
		"mode", "speech",
		"notrak", "trackmode", "tracktype", "cdplay", "cdnext", "cdprev", "cdstop",
		"leftclick", "unitchat":
		return "Options"
	case "cancel":
		return "Previous"
	}
	return ""
}

// retailTestSound is the sample the sound page's `TEST` button plays: mode 1,
// attenuation −585, no pan, under the ordinary play gates [03 R-AUD-01 §2].
const retailTestSound = "sounds/explode.wav"

func (g *gameShell) activateRetailOptionsGadget(name string) bool {
	if !g.retailOptionsActive() {
		return false
	}
	name = gui.CallbackName(name)
	// Every arm below either transitions or writes a preference, so the cue
	// precedes them all, as it does on the screens frontendCue serves.
	g.playMenuCue(retailOptionsCue(retailOptionsCueKey(name)))
	switch name {
	case "SOUND", "MUSIC", "SPEEDS", "VISUALS":
		page, _ := retailOptionsPageKey(name)
		g.openRetailOptionsPage(page)
		return true
	case "PREV":
		// "OK": every preference is written back, then the window closes
		// [07 R-FE-01 §6][07 R-FE-01 §11].
		g.closeRetailOptionsScreen()
		g.commitWindowSize()
		g.saveSettings()
		return true
	case "CANCEL":
		// The entry snapshot is restored and re-applied, then the window
		// closes. Unsaved edits made on any page are discarded, because no
		// screen writes a value directly [07 R-FE-01 §6][07 R-FE-01 §11].
		g.restoreRetailOptionsSnapshot(optionsState.snapshot)
		g.closeRetailOptionsScreen()
		return true
	case "RESTORE":
		g.restoreRetailOptionsDefaults()
		return true
	case "UNDO":
		g.undoRetailOptionsPage()
		return true
	case "ANTI":
		g.display.AntiAlias = g.retailOptionsStage("ANTI", 2, boolInt(g.display.AntiAlias != 0))
		optionsPanel.SetStageAt(optionsPanel.Index("ANTI"), g.display.AntiAlias)
		g.applyRetailVisualOptions(clPtr)
		return true
	case "SHADING":
		g.display.Shading = g.retailOptionsStage("SHADING", 2, boolInt(g.display.Shading != 0))
		optionsPanel.SetStageAt(optionsPanel.Index("SHADING"), g.display.Shading)
		g.applyRetailVisualOptions(clPtr)
		return true
	case "BSHADOWS":
		g.setRetailShadowBits(g.retailOptionsStage("BSHADOWS", 2, boolInt(g.display.FeatureShadows != 0)) != 0)
		optionsPanel.SetStageAt(optionsPanel.Index("BSHADOWS"), g.display.FeatureShadows)
		g.applyRetailVisualOptions(clPtr)
		return true

	// ---- SOUNDS ---------------------------------------------------------
	case "MODE":
		// `MODE` writes the sound-flags byte's low three bits. `Off` stops
		// every voice; `Mono` outside a battle re-issues the front-end `BGM`
		// loop. The device's 3-D flag follows the value 2 [03 R-AUD-01 §2].
		g.audioPrefs.SoundMode = g.retailOptionsStage("MODE", 3, g.audioPrefs.SoundMode)
		g.applyRetailAudioOptions()
		if g.audioPrefs.SoundMode == settings.SoundModeMono && g.battle == nil {
			g.armMenuBGM()
		}
		g.syncRetailSoundPage()
		return true
	case "SPEECH":
		// `SPEECH` writes both halves at once: bit 6 takes `stage != 0` and
		// the acknowledgement voice level takes `stage × 5` [03 R-AUD-01 §2].
		speech := 0
		if g.audioPrefs.SpeechFX != 0 {
			speech = g.audioPrefs.UnitChat / 5
		}
		stage := g.retailOptionsStage("SPEECH", 3, speech)
		g.audioPrefs.SpeechFX = boolInt(stage != 0)
		g.audioPrefs.UnitChat = stage * 5
		g.syncRetailSoundPage()
		return true
	case "TEST":
		g.playRetailSoundTest()
		return true

	// ---- MUSIC ----------------------------------------------------------
	case "NOTRAK":
		g.audioPrefs.MusicMode = boolInt(g.audioPrefs.MusicMode == 0)
		g.setRetailMusicEnabled(g.audioPrefs.MusicMode != 0)
		g.syncRetailMusicPage()
		return true
	case "TRACKMODE":
		// `TRACKMODE`'s stage plus one is `cdmode`. `Repeat` copies the
		// selection into the requested track; `Custom` shows `TRACKTYPE` for
		// the selection [03 R-AUD-01 §4].
		g.audioPrefs.CDMode = g.retailOptionsStage("TRACKMODE", settings.MaxCDMode, g.audioPrefs.CDMode-1) + 1
		g.applyRetailMusicMode()
		g.syncRetailMusicPage()
		return true
	case "TRACKTYPE":
		if optionsState.track >= 1 && optionsState.track <= retailMusicCategoryCount {
			optionsState.categories[optionsState.track-1] = g.retailOptionsStage("TRACKTYPE", 5, retailTrackCategory(optionsState.track))
		}
		g.syncRetailMusicPage()
		return true
	case "CDPLAY", "CDNEXT", "CDPREV", "CDSTOP":
		g.activateRetailMusicTransport(retailOptionsCueKey(name))
		return true

	// ---- SPEEDS (the root captions its button `INTERFACE`) --------------
	case "LEFTCLICK":
		// The two-stage `LEFTCLICK` button writes the `Interface Type` word
		// [07 R-CAM-01 §5].
		g.interfaceType = g.retailOptionsStage("LEFTCLICK", 2, g.interfaceType)
		optionsPanel.SetStageAt(optionsPanel.Index("LEFTCLICK"), g.interfaceType)
		return true
	case "UNITCHAT":
		// `UNITCHAT` is the acknowledgement **text** level, `stage × 5`. Its
		// voice twin is the sound page's `SPEECH` [07 R-CAM-01 §7].
		stage := g.retailOptionsStage("UNITCHAT", 3, g.messages.UnitChatText/5)
		g.messages.UnitChatText = stage * 5
		optionsPanel.SetStageAt(optionsPanel.Index("UNITCHAT"), stage)
		return true
	}
	// Every remaining control on the open page belongs to the options window,
	// so the screen underneath must not see it.
	if optionsPanel != nil && optionsPanel.Window != nil {
		for _, gad := range optionsPanel.Window.Gadgets {
			if gui.CallbackNameEqual(gad.Name, name) {
				return true
			}
		}
	}
	return false
}

// retailOptionsStage takes the stage already selected by a pointer service
// pass, or advances current for a direct/key activation. current also keeps
// callbacks valid in a narrow screen transition after its page is gone. The
// callback runs after the service pass, so it must not cycle the same pointer
// gesture twice [07 R-WGT-01 §1][07 R-WGT-01 §3].
func (g *gameShell) retailOptionsStage(name string, stages, current int) int {
	if optionsPanel == nil {
		return retailCycleStage(current, stages)
	}
	index := optionsPanel.Index(name)
	if optionsState != nil && optionsPanel.Window != nil {
		fired := optionsState.callbackIndex
		if fired > 0 && fired < len(optionsPanel.Window.Gadgets) && gui.CallbackNameEqual(optionsPanel.Window.Gadgets[fired].Name, name) {
			index = fired
		}
	}
	if index < 0 {
		return retailCycleStage(current, stages)
	}
	stage := optionsPanel.StageAt(index)
	if optionsState != nil && optionsState.serviceStageActive && optionsState.serviceStageIndex == index {
		return stage
	}
	// Direct and keyboard callbacks retain the stored preference as their
	// source. The two acknowledgement controls instead use the panel state,
	// which is their screen-local source [07 R-WGT-01 §3].
	switch name {
	case "SPEECH", "UNITCHAT":
		return retailCycleStage(stage, stages)
	default:
		return retailCycleStage(current, stages)
	}
}

// commitRetailSliderValue runs one slider's change callback: the read-out is
// computed from the knob and written to whatever the slider drives
// [07 R-FE-01 §6].
func (g *gameShell) commitRetailSliderValue(index int, s *retailSliderState) {
	if g == nil || s == nil || optionsPanel == nil || optionsPanel.Window == nil || index < 1 || index >= len(optionsPanel.Window.Gadgets) {
		return
	}
	key, ok := retailSliderKey(gui.CallbackName(optionsPanel.Window.Gadgets[index].Name))
	if !ok {
		return
	}
	value := retailSliderValue(s.knob, s.travel, s.max)
	switch key {
	case "vidsldr":
		if len(optionsState.modes) == 0 {
			return
		}
		if value < 0 {
			value = 0
		}
		if value >= len(optionsState.modes) {
			value = len(optionsState.modes) - 1
		}
		mode := optionsState.modes[value]
		g.display.Width, g.display.Height = mode.W, mode.H
		g.syncRetailVideoLabel()
	case "gamma":
		// `GAMMA`'s integer is applied as the palette factor 0.5 + g/24, and
		// the wave and CD volumes are re-pushed whenever it changes
		// [07 R-FE-01 §6][03 R-AUD-01 §2].
		g.display.Gamma = value
		applyGammaOption(clPtr, value)
		g.applyRetailAudioOptions()
	case "fxvol":
		// `fxvol` gates every play and sets the wave device's level; the CD
		// level is re-pushed with it [03 R-AUD-01 §2].
		g.audioPrefs.FXVol = value
		g.applyRetailAudioOptions()
	case "musicvol":
		g.audioPrefs.MusicVol = value
		g.applyRetailAudioOptions()
	case "game":
		// The `GAME` read-out floors at 1 and is applied at once through the
		// speed setter, which clamps 21 down to 20 [07 R-CAM-01 §7]
		// [07 R-CAM-01 §3].
		//
		// In the front end the value only persists: there is no session to
		// apply it to, and its battle consumer is the session's speed state
		// [01 §4.3], which battle entry owns. In battle it goes straight
		// through the session's setter, announcement included.
		if value < settings.MinGameSpeed {
			value = settings.MinGameSpeed
		}
		if value > settings.MaxGameSpeed {
			value = settings.MaxGameSpeed
		}
		g.gameSpeed = value
		g.applyRetailBattleGameSpeed()
	case "screen":
		// The `SCREEN` read-out floors at 1 and stores the scroll-speed byte,
		// which the camera's scroll pass reads [07 R-CAM-01 §7][07 §10].
		if value < settings.MinScrollSpeed {
			value = settings.MinScrollSpeed
		}
		g.scrollSpeed = value
		g.applyRetailBattleScrollSpeed()
	case "txtscrol":
		g.messages.TextScroll = value
		g.syncRetailMaxLinesLabel()
		g.applyRetailBattleMessageLines()
	case "maxlines":
		if value < 0 {
			value = 0
		}
		g.messages.TextLines = value
		g.syncRetailMaxLinesLabel()
		g.applyRetailBattleMessageLines()
	}
}

// moveRetailSlider clamps a knob into 0..travel-1 and runs the change callback
// when it moved [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) moveRetailSliderAt(index int, s *retailSliderState, knob int) {
	if s == nil {
		return
	}
	if knob < 0 {
		knob = 0
	}
	if knob > s.travel-1 {
		knob = s.travel - 1
	}
	if knob == s.knob {
		return
	}
	s.knob = knob
	g.commitRetailSliderValue(index, s)
}

// moveRetailSlider is the explicit named-operation adapter. It retains the
// GUI family's bounded first-match semantics; fired widget callbacks use the
// indexed moveRetailSliderAt path above.
func (g *gameShell) moveRetailSlider(name string, s *retailSliderState, knob int) {
	if optionsPanel == nil {
		return
	}
	g.moveRetailSliderAt(optionsPanel.Index(name), s, knob)
}

// clickRetailSlider takes the capture when the press lands inside the knob
// rectangle. A press on the track beside the knob captures but does not move
// it, and a press on an arrow is handled on release [07 R-WGT-01 §5].
func (g *gameShell) clickRetailSlider(index int, r gui.Rect, x, y int32) bool {
	s := g.retailOptionsSliderAt(index)
	if s == nil {
		return false
	}
	coordinate := int(x)
	_ = y
	// The knob rectangle is (gx+1+knob, gy+1)-(gx+1+knob+knobsize, gy+h-1) over
	// the bar the synthesis shrank and shifted right by one arrow width
	// [07 R-WGT-01 §5 "The knob rectangle"].
	barX := int(r.X) + s.arrowW
	knobStart := barX + 1 + s.knob
	if coordinate < knobStart || coordinate >= knobStart+s.knobSize {
		return true
	}
	optionsState.drag = retailSliderDrag{active: true, index: index, startCoord: coordinate, startKnob: s.knob}
	return true
}

// releaseRetailSlider consumes the release that ended a drag, so the arrow
// step below it does not also fire when the pointer was dragged past the end of
// the track [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) releaseRetailSlider(index int) bool {
	if g.retailOptionsSliderAt(index) == nil {
		return false
	}
	if optionsState.drag.ended || optionsState.drag.active {
		optionsState.drag = retailSliderDrag{}
		return true
	}
	return false
}

// updateRetailSliderDrag maps pointer displacement onto the knob:
// `knob := savedKnob + (pointer - savedPointer)` on the bar's axis
// [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) updateRetailSliderDrag(mouse *input.MouseState) bool {
	if optionsState == nil || !optionsState.drag.active {
		return false
	}
	if mouse == nil || !mouse.Held(input.MouseButtonLeft) {
		optionsState.drag = retailSliderDrag{ended: true}
		return true
	}
	index := optionsState.drag.index
	s := optionsState.sliders[index]
	if s == nil {
		optionsState.drag = retailSliderDrag{ended: true}
		return true
	}
	g.moveRetailSliderAt(index, s, optionsState.drag.startKnob+(int(mouse.X)-optionsState.drag.startCoord))
	return true
}

// adjustRetailSlider is the synthesised arrow buttons' step: the knob moves by
// one while the pointer is before or after the knob rectangle and the button
// is held [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) adjustRetailSlider(index int, delta int) bool {
	s := g.retailOptionsSliderAt(index)
	if s == nil {
		return false
	}
	if optionsState.drag.active {
		return true
	}
	g.moveRetailSliderAt(index, s, s.knob+delta)
	return true
}

func retailOptionsPageKey(name string) (string, bool) {
	switch name {
	case "SOUND":
		return "sound", true
	case "MUSIC":
		return "music", true
	case "SPEEDS":
		return "speeds", true
	case "VISUALS":
		return "visuals", true
	}
	return "", false
}

func retailOptionsCueKey(name string) string {
	switch name {
	case "SOUND":
		return "sound"
	case "MUSIC":
		return "music"
	case "SPEEDS":
		return "speeds"
	case "VISUALS":
		return "visuals"
	case "PREV":
		return "prev"
	case "CANCEL":
		return "cancel"
	case "RESTORE":
		return "restore"
	case "UNDO":
		return "undo"
	case "ANTI":
		return "anti"
	case "SHADING":
		return "shading"
	case "BSHADOWS":
		return "bshadows"
	case "MODE":
		return "mode"
	case "SPEECH":
		return "speech"
	case "NOTRAK":
		return "notrak"
	case "TRACKMODE":
		return "trackmode"
	case "TRACKTYPE":
		return "tracktype"
	case "CDPLAY":
		return "cdplay"
	case "CDNEXT":
		return "cdnext"
	case "CDPREV":
		return "cdprev"
	case "CDSTOP":
		return "cdstop"
	case "LEFTCLICK":
		return "leftclick"
	case "UNITCHAT":
		return "unitchat"
	}
	return ""
}

// retailSliderKey recognizes callback literals only. Page labels such as
// "TEXT" remain labels; they are never aliases for a slider [07 R-WGT-02 §2].
func retailSliderKey(name string) (string, bool) {
	switch name {
	case "VIDSLDR":
		return "vidsldr", true
	case "GAMMA":
		return "gamma", true
	case "FXVOL":
		return "fxvol", true
	case "MUSICVOL":
		return "musicvol", true
	case "GAME":
		return "game", true
	case "SCREEN":
		return "screen", true
	case "TXTSCROL":
		return "txtscrol", true
	case "MAXLINES":
		return "maxlines", true
	}
	return "", false
}
