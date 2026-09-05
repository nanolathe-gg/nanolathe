package main

// The options family [07 R-FE-01 §6].
//
// `SINGLE`'s `Options` opens the options root as a child window over `SINGLE`;
// `PREV`/`CANCEL` pop it again [07 R-FE-01 §2]. The root is `STARTOPT.GUI`
// over the `options4x` background. Its four page buttons open their own `.GUI`
// with the merge flag: the page's gadgets are appended to the open window
// [07 R-FE-01 §6]. Only `VISUALS` is built here; the other three pages are
// visible but do nothing, see openRetailOptionsPage.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/ui"
)

const (
	retailOptionsGUI      = "guis/startopt.gui"
	retailOptionsBackdrop = "bitmaps/options4x.pcx"
	retailVisualsGUI      = "guis/visuals.gui"
	// The per-page background. Established: the page opener itself hands it to
	// the shared bitmap cache in the statement after the merge-flag window
	// open, so it is the page's step and not an argument of the opener and not
	// the root's repaint. Each of the four pages names its own — `optsound4x`,
	// `optmusic4x`, `optinterface4x`, `optvisual4x` — and the in-battle
	// variants of the same pages hand none, keeping the battle behind them
	// [07 R-FE-01 §6].
	retailVisualsBackdrop = "bitmaps/optvisual4x.pcx"
)

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

// retailOptionsState is the live options session: which page is merged, the
// entry snapshot `CANCEL` and `UNDO` restore, and the knob position of every
// kind-4 slider on the open page [07 R-FE-01 §6].
type retailOptionsState struct {
	page string // "" for the root with no page merged
	// snapshot is the preference block captured when the root opened. `CANCEL`
	// restores it and leaves; `UNDO` restores it and reopens the page.
	snapshot settings.Display
	sliders  map[string]*retailSliderState
	drag     retailSliderDrag
	modes    []retailDisplayMode
}

// retailDisplayMode is one row of the table `VIDSLDR` indexes.
type retailDisplayMode struct{ W, H int }

// retailDisplayModes builds that table.
//
// Retail's windowed (GDI) presentation offers the fixed list 640x480, 800x600,
// 1024x768, then 1280x1024 only when the desktop is at least 1280x1024 and
// 1600x1200 only when the desktop is at least 1600x1200, both axes inclusive;
// its DirectDraw presentation substitutes the driver's enumeration of 8-bit
// modes [07 R-FE-02 §9]. Nanolathe presents a software framebuffer through a
// windowed surface, so the windowed arm is the applicable one and this is it
// verbatim.
//
// Nothing here stands in for the DirectDraw arm, and nothing needs to: this
// build has no 8-bit display driver to enumerate, so there is no mode list to
// read. If a full-screen indexed presentation ever lands, its enumeration
// replaces the fixed list above rather than adding to it.
//
// The options page then sorts the table ascending by width and then height and
// drops every mode below 640x480 [07 R-FE-01 §6]. The GDI list is already in
// that order, but the sort and the filter are applied all the same: they are
// the page's step, not the source's.
func retailDisplayModes(desktopW, desktopH int) []retailDisplayMode {
	modes := []retailDisplayMode{{640, 480}, {800, 600}, {1024, 768}}
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
	name       string
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
	if optionsState == nil || optionsPanel == nil || g == nil || g.activePanel() != optionsPanel {
		return nil
	}
	return optionsState.sliders[menuKey(name)]
}

// openRetailOptionsScreen builds `STARTOPT.GUI` and pushes it over the current
// surface, snapshotting the preference block the way entering the options root
// does [07 R-FE-01 §6].
func (g *gameShell) openRetailOptionsScreen() error {
	if g == nil || g.cs == nil || g.cs.fs == nil {
		return fmt.Errorf("nanolathe: options screen: no mounted content: logical path %s, providers searched [], expected the authored options root", retailOptionsGUI)
	}
	window, err := gui.Load(g.cs.fs, retailOptionsGUI)
	if err != nil {
		return retailFrontendAssetError(g.cs, "retail options GUI unavailable", retailOptionsGUI, "the authored options root", err)
	}
	background, err := formats.LoadPCXFile(g.cs.fs, retailOptionsBackdrop)
	if err != nil {
		return retailFrontendAssetError(g.cs, "retail options bitmap", retailOptionsBackdrop, "the authored options background", err)
	}
	desktopW, desktopH := ebitenapp.DesktopSize()
	optionsAssets = &retailPanelAssets{window: window, background: background}
	optionsState = &retailOptionsState{
		snapshot: g.display,
		sliders:  map[string]*retailSliderState{},
		modes:    retailDisplayModes(desktopW, desktopH),
	}
	optionsPanel = ui.NewPanel(window)
	if optionsPanel == nil {
		return retailFrontendAssetError(g.cs, "retail options GUI unavailable", retailOptionsGUI, "the authored options root", nil)
	}
	g.frontend.Panels.Push(optionsPanel)
	return nil
}

func (g *gameShell) openRetailOptionsScreenReporting() {
	if err := g.openRetailOptionsScreen(); err != nil {
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
// page's gadgets are appended to the root's; the root authors no `PANEL`
// gadget, so there is nothing to centre inside and the authored rectangles
// stand [07 R-FE-01 §6].
//
// Only `VISUALS` is implemented. `SOUND`, `MUSIC` and `SPEEDS` are refused
// here rather than merged: their pages' controls — the two mixer gauges, the
// six interface sliders and stage buttons — have no owner in this build, and a
// page of controls that move but change nothing reads as a defect rather than
// as an honest gap.
// The merge bakes the page's own window origin into every appended rectangle
// [07 R-FE-01 §6]: when the open window authors no gadget named `PANEL` — and
// `STARTOPT.GUI` authors none — the opener adds the page header's x and y to
// each of the page's controls before appending them, and the root's own origin
// is then added at draw time as it is for any gadget. `SOUNDS.GUI` is authored
// at (0,1) and moves its whole page down a pixel; the other three pages are at
// (0,0). The `PANEL` arm centres the page inside that gadget instead and is
// unreachable from this root.
func (g *gameShell) openRetailOptionsPage(page string) {
	if !g.retailOptionsActive() || optionsAssets == nil || optionsAssets.window == nil {
		return
	}
	page = menuKey(page)
	if page != "visuals" {
		return
	}
	pageWindow, err := gui.Load(g.cs.fs, retailVisualsGUI)
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(
			retailFrontendAssetError(g.cs, "retail options page GUI unavailable", retailVisualsGUI, "the authored VISUALS page", err).Error()))
		return
	}
	background, err := formats.LoadPCXFile(g.cs.fs, retailVisualsBackdrop)
	if err != nil {
		reportRetailMessageError(g.showRetailMessage(
			retailFrontendAssetError(g.cs, "retail options page bitmap", retailVisualsBackdrop, "the authored VISUALS background", err).Error()))
		return
	}

	root := optionsAssets.window
	kept := make([]gui.Gadget, 0, len(root.Gadgets)+len(pageWindow.Gadgets))
	for _, gad := range root.Gadgets {
		if retailOptionsPageGadget(gad) {
			continue
		}
		kept = append(kept, gad)
	}
	// The page header (index 0) is the page's own window record, not a control.
	// Its origin is folded into every control the page contributes, because the
	// merged list belongs to the root window from here on and only the root's
	// origin is applied when the panel is drawn [07 R-FE-01 §6].
	for _, gad := range pageWindow.Gadgets[1:] {
		gad.SourceName = retailOptionsPageSource + gad.SourceName
		gad.Rect.X += pageWindow.OriginX
		gad.Rect.Y += pageWindow.OriginY
		kept = append(kept, gad)
	}
	root.Gadgets = kept
	optionsAssets.background = background
	optionsState.page = page
	optionsState.sliders = map[string]*retailSliderState{}

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
	g.refreshRetailOptionsPage()
}

// refreshRetailOptionsPage installs every page control's state from the live
// preference block and runs each slider's value callback once, which is what
// the page open does [07 R-FE-01 §6].
func (g *gameShell) refreshRetailOptionsPage() {
	if !g.retailOptionsActive() || optionsState.page != "visuals" {
		return
	}
	p := optionsPanel
	for _, gad := range p.Window.Gadgets {
		if !retailOptionsPageGadget(gad) || gad.Kind != gui.KindScrollBar {
			continue
		}
		travel, knobSize, arrowW, ok := g.retailSliderMetrics(gad)
		if !ok {
			continue
		}
		state := &retailSliderState{travel: travel, knobSize: knobSize, arrowW: arrowW}
		switch menuKey(gad.Name) {
		case "vidsldr":
			// `VIDSLDR` indexes the display-mode table, and the page opener
			// writes its maximum straight from that table's count minus one
			// rather than from a stored constant — Established, unlike
			// `GAMMA`'s literal 20 beside it [07 R-FE-01 §6].
			state.max = len(optionsState.modes) - 1
			state.knob = retailSliderKnob(retailDisplayModeIndex(optionsState.modes, g.display.Width, g.display.Height), travel, state.max)
		case "gamma":
			// `GAMMA`'s maximum is 20 and its integer is applied as the
			// palette factor 0.5 + g/24 [07 R-FE-01 §6]. The knob and the
			// stored value are bound; the palette factor is not, because
			// nothing in this build owns a display-palette gamma ramp. The
			// value persists and is re-shown, so wiring the ramp later needs
			// no change here.
			state.max = settings.MaxGamma
			state.knob = retailSliderKnob(g.display.Gamma, travel, state.max)
		default:
			continue
		}
		optionsState.sliders[menuKey(gad.Name)] = state
	}
	// Two-stage buttons: stage 1 is the "On" label of the authored `Off|On`
	// pair. `ANTI`, `BSHADOWS` and `SHADING` are bits 1, 4 and 5 of the display
	// option word, and `BSHADOWS` drives all three shadow values together
	// [07 R-FE-01 §6].
	p.SetStatus("ANTI", boolInt(g.display.AntiAlias != 0))
	p.SetStatus("SHADING", boolInt(g.display.Shading != 0))
	p.SetStatus("BSHADOWS", boolInt(g.display.FeatureShadows != 0))
	g.syncRetailVideoLabel()
}

// syncRetailVideoLabel writes the `VIDVAL` read-out. Retail formats it as
// `%d X %d` [07 R-FE-01 §6].
func (g *gameShell) syncRetailVideoLabel() {
	if optionsPanel == nil {
		return
	}
	optionsPanel.SetText("VIDVAL", fmt.Sprintf("%d X %d", g.display.Width, g.display.Height))
}

// applyRetailVisualOptions pushes the three display-option bits into the live
// presentation. `BSHADOWS` copies bit 4 into bit 3 and bit 3 into bit 2, so one
// control drives `FeatureShadows`, `VehicleShadows` and `Shadows`
// [07 R-FE-01 §6][03 §5.3].
func (g *gameShell) applyRetailVisualOptions(cl *client.Client) {
	if g == nil || cl == nil {
		return
	}
	cl.SetAntiAlias(g.display.AntiAlias != 0)
	cl.SetShadowOptions(g.display.Shadows != 0, g.display.VehicleShadows != 0, g.display.Shading != 0)
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
// `Options`. The pages' sliders are the one exception and are absent here on
// purpose: a slider is driven by its value callback, which plays nothing, so a
// drag or an arrow step is silent.
//
// It is a table of its own rather than an arm of frontendCue because that
// function keys on the shell mode, and the options root has no mode: it is a
// child window over whichever screen opened it, which stays the current mode
// while it is up. The cue still runs where every other one does — in the screen
// handler that consumes the fired result [07 R-WGT-01 §3].
func retailOptionsCue(key string) string {
	switch key {
	case "sound", "music", "speeds", "visuals", "prev",
		"restore", "undo", "anti", "shading", "bshadows":
		return "Options"
	case "cancel":
		return "Previous"
	}
	return ""
}

func (g *gameShell) activateRetailOptionsGadget(name string) bool {
	if !g.retailOptionsActive() {
		return false
	}
	// Every arm below either transitions or writes a preference, so the cue
	// precedes them all, as it does on the screens frontendCue serves.
	g.playMenuCue(retailOptionsCue(menuKey(name)))
	switch menuKey(name) {
	case "sound", "music", "speeds", "visuals":
		g.openRetailOptionsPage(name)
		return true
	case "prev":
		// "OK": every preference is written back, then the window closes
		// [07 R-FE-01 §6][07 R-FE-01 §11].
		g.closeRetailOptionsScreen()
		g.saveSettings()
		return true
	case "cancel":
		// The entry snapshot is restored and re-applied, then the window
		// closes. Unsaved edits made on any page are discarded, because no
		// screen writes a value directly [07 R-FE-01 §6][07 R-FE-01 §11].
		g.display = optionsState.snapshot
		g.applyRetailVisualOptions(clPtr)
		g.closeRetailOptionsScreen()
		return true
	case "restore":
		// `VISUALS` `RESTORE`: bits 1-5 set, gamma 12 and — front end only —
		// 640x480 with `DitheredFog` cleared; the page reopens
		// [07 R-FE-01 §6]. `DitheredFog` has no owner here, so nothing clears
		// it; it is not persisted either.
		if optionsState.page != "visuals" {
			return true
		}
		g.display.AntiAlias = 1
		g.setRetailShadowBits(true)
		g.display.Shading = 1
		g.display.Gamma = settings.DefaultGamma
		g.display.Width = settings.DefaultDisplaymodeWidth
		g.display.Height = settings.DefaultDisplaymodeHeight
		g.applyRetailVisualOptions(clPtr)
		g.openRetailOptionsPage(optionsState.page)
		return true
	case "undo":
		// `UNDO` restores bits 1-6, gamma and — front end only — the display
		// size from the entry snapshot, and reopens the page [07 R-FE-01 §6].
		if optionsState.page != "visuals" {
			return true
		}
		g.display = optionsState.snapshot
		g.applyRetailVisualOptions(clPtr)
		g.openRetailOptionsPage(optionsState.page)
		return true
	case "anti":
		g.display.AntiAlias = boolInt(g.display.AntiAlias == 0)
		optionsPanel.SetStatus("ANTI", g.display.AntiAlias)
		g.applyRetailVisualOptions(clPtr)
		return true
	case "shading":
		g.display.Shading = boolInt(g.display.Shading == 0)
		optionsPanel.SetStatus("SHADING", g.display.Shading)
		g.applyRetailVisualOptions(clPtr)
		return true
	case "bshadows":
		g.setRetailShadowBits(g.display.FeatureShadows == 0)
		optionsPanel.SetStatus("BSHADOWS", g.display.FeatureShadows)
		g.applyRetailVisualOptions(clPtr)
		return true
	}
	// Every remaining control on the open page belongs to the options window,
	// so the screen underneath must not see it.
	if optionsPanel != nil && optionsPanel.Window != nil {
		for _, gad := range optionsPanel.Window.Gadgets {
			if strings.EqualFold(gad.Name, name) {
				return true
			}
		}
	}
	return false
}

// commitRetailSliderValue runs one slider's change callback: the read-out is
// computed from the knob and written to whatever the slider drives
// [07 R-FE-01 §6].
func (g *gameShell) commitRetailSliderValue(name string, s *retailSliderState) {
	if g == nil || s == nil {
		return
	}
	value := retailSliderValue(s.knob, s.travel, s.max)
	switch menuKey(name) {
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
		g.display.Gamma = value
	}
}

// moveRetailSlider clamps a knob into 0..travel-1 and runs the change callback
// when it moved [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) moveRetailSlider(name string, s *retailSliderState, knob int) {
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
	g.commitRetailSliderValue(name, s)
}

// clickRetailSlider takes the capture when the press lands inside the knob
// rectangle. A press on the track beside the knob captures but does not move
// it, and a press on an arrow is handled on release [07 R-WGT-01 §5].
func (g *gameShell) clickRetailSlider(gad gui.Gadget, r gui.Rect, x, y int32) bool {
	s := g.retailOptionsSlider(gad.Name)
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
	optionsState.drag = retailSliderDrag{active: true, name: menuKey(gad.Name), startCoord: coordinate, startKnob: s.knob}
	return true
}

// releaseRetailSlider consumes the release that ended a drag, so the arrow
// step below it does not also fire when the pointer was dragged past the end of
// the track [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) releaseRetailSlider(gad gui.Gadget) bool {
	if g.retailOptionsSlider(gad.Name) == nil {
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
	name := optionsState.drag.name
	s := optionsState.sliders[name]
	if s == nil {
		optionsState.drag = retailSliderDrag{ended: true}
		return true
	}
	g.moveRetailSlider(name, s, optionsState.drag.startKnob+(int(mouse.X)-optionsState.drag.startCoord))
	return true
}

// adjustRetailSlider is the synthesised arrow buttons' step: the knob moves by
// one while the pointer is before or after the knob rectangle and the button
// is held [07 R-WGT-01 §5 "Pointer"].
func (g *gameShell) adjustRetailSlider(gad gui.Gadget, delta int) bool {
	s := g.retailOptionsSlider(gad.Name)
	if s == nil {
		return false
	}
	if optionsState.drag.active {
		return true
	}
	g.moveRetailSlider(menuKey(gad.Name), s, s.knob+delta)
	return true
}
