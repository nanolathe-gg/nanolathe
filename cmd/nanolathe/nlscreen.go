package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/platform/screenkit"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The Nanolathe screen: the main menu's NANOLATHE button opens a full-window
// setup screen over a live battle, one page per area and one card per setting
// (docs/DESIGN_INTERFACE_HUD_INPUT.md §3.17). It edits a draft; the live
// background always shows the draft, and Apply writes it through the same
// shell paths the options pages and the Mods screen use.

// nlScreenInst is the window's screen; nil in displayless runs, where the
// NANOLATHE button falls back to the Mods & Mutators window.
var nlScreenInst *nlScreen

type nlScreen struct {
	host func() *gameShell

	open          bool
	closing       bool // closed, waiting for the closing gesture to be released
	ready         bool
	fonts         screenkit.Fonts
	art           *nlArt
	pointer       *client.Cursors
	pointerCS     *contentSet
	pointerPal    *palette.Tables
	pointerImages map[*formats.GAFFrame]*ebiten.Image
	pointerClock  time.Time
	pointerTick   int64
	pointerUnder  *ebiten.Image
	pointerRect   image.Rectangle
	preview       *nlPreview
	hits          screenkit.Hits

	page    int
	focus   [5]int
	draft   nlDraft
	touched map[string]bool
	mods    []modlibrary.Mod

	compare       bool
	wipe          float64
	wipeTouched   bool
	paired        bool           // the background is a twin compare in two columns
	partSel       map[string]int // the part each grouped card compares
	src           nlSource       // the running content's layers (nlscreen_source.go)
	pendingAction func()         // a locked change waiting for the override
	pendingWhat   string         // what the waiting change is, for the dialog
	// The shell the screen is bound to, a content switch in progress, the
	// catalogue popup, the install count last seen, and a mod awaiting the
	// remove confirmation.
	bound        *gameShell
	reload       *nlReload
	catalog      *nlCatalog
	installsSeen int
	removing     modlibrary.Mod
	warmed       *gameShell // the shell warm last prepared for
	// The presets panel: the selected row, the parts to apply, the name
	// being typed, and presets applied to the draft for Apply to write whole.
	presetSel      int
	presetTop      int
	presetList     screenkit.Rect
	presetScopes   [3]bool
	presetName     string
	pendingPresets []json.RawMessage
	// The Controls page: the tab, the selected row, the first row shown, a
	// key cap waiting for a press, and where the table was drawn.
	ctlGroup          int
	ctlRow            int
	ctlScroll         int
	capture           nlCapture
	ctlTable          screenkit.Rect
	contentTop        int            // first row the content list shows
	contentList       screenkit.Rect // list and scrollbar wheel target
	contentDragging   bool
	contentGrab       float64 // pointer offset within the scrollbar thumb
	dialog            string
	pendingV          int
	resolutionModes   []retailDisplayMode
	resolutionNative  retailDisplayMode
	resolutionText    [2]string
	resolutionField   int
	resolutionReplace bool
	resolutionError   string

	toast     string
	toastLeft float64
	lastFrame time.Time
	lastInput time.Time
	heroT     float64
	heroKey   string
	pageT     float64
	scroll    float64
	lift      map[string]float64
	anim      map[string]float64
	dt        float64
	wheel     float64
	openT     float64
	demoKey   string
	demoT     float64
	clock     float64
	baseFS    vfs.FSOps
	// canvasScale replaces the display's device scale when set, for a
	// capture that has no display.
	canvasScale float64
	// deviceScale is the display's device scale as last read, and the
	// screen clock and canvas size it was read at (cachedDeviceScale).
	deviceScale                float64
	deviceScaleRead            bool
	deviceScaleAt              float64
	deviceScaleW, deviceScaleH float64
	stats                      nlStats
	statsCS                    *contentSet
	catalogue                  nlCatalogue // the built pages (pages)
	// saved is the shell's live state as a draft and changed the count of
	// cards and keys the draft differs from it by, read once a frame for the
	// Apply count and the cards' changed lamps.
	saved   nlDraft
	changed int
	ui      nlUICache // memoized strings and wrapped text (nlscreen_ui_cache.go)
	// restrictSum is the Unit restrictions card's last summary and what it
	// was read from (restrictionSummary).
	restrictSum    nlRestrictionSummary
	restrictSumKey nlRestrictionSummaryKey
}

func newNLScreen(host func() *gameShell) *nlScreen {
	return &nlScreen{host: host, touched: map[string]bool{}, lift: map[string]float64{}, anim: map[string]float64{}, partSel: map[string]int{}, wipe: 0.64}
}

func (s *nlScreen) shell() *gameShell { return s.host() }

// Active implements ebitenapp.FullScreen.
func (s *nlScreen) Active() bool { return s != nil && s.open }

// openNLScreen opens the screen from the main menu.
func (g *gameShell) openNLScreen() bool {
	s := nlScreenInst
	if s == nil || g.cs == nil {
		return false
	}
	s.show(g)
	return true
}

func (s *nlScreen) show(g *gameShell) {
	s.bindShell(g)
	s.readResolutionModes()
	s.capture, s.ctlGroup, s.ctlRow, s.ctlScroll = nlCapture{}, 0, 0, 0
	s.compare, s.dialog, s.toast, s.closing = false, "", "", false
	s.page, s.pageT, s.heroT, s.openT = 0, 0, 0, 0
	s.focus = [5]int{}
	s.open = true
	s.lastFrame = time.Time{}
	s.pointerClock, s.pointerTick = time.Time{}, 0
	s.pointerRect = image.Rectangle{}
	if s.pointer != nil {
		s.pointer.SetIndex(render.CursorNormal)
	}
	g.playMenuCue("BigButton")
}

// warm prepares the screen while the main menu is idle, so opening it is
// quick: the fonts and art, the installed mods, the first card's scene
// staged in the background and the unit pictures. It prepares once per
// shell, and a content switch releases it first (releasePreview); later idle
// steps only keep the picture loader going (tendMenuPictures).
func (s *nlScreen) warm(g *gameShell) {
	if s == nil || s.open || s.reload != nil || g == nil || g.cs == nil {
		return
	}
	if s.warmed == g {
		s.tendMenuPictures(g)
		return
	}
	if s.baseFS == nil {
		base := vfs.New()
		if err := base.MountGameDirectories(g.cs.baseRoots); err == nil {
			s.baseFS = base
		} else {
			base.Close()
			s.baseFS = g.cs.fs
		}
	}
	if !s.ready {
		s.fonts = screenkit.LoadFonts()
		s.art = loadNLArt(s.baseFS)
		s.ready = true
	}
	// The canvas the screen will open at: the window, or the display in
	// fullscreen, in device pixels.
	scale := ebiten.Monitor().DeviceScaleFactor()
	w, h := ebiten.WindowSize()
	if ebiten.IsFullscreen() {
		w, h = ebiten.Monitor().Size()
	}
	if w <= 0 || h <= 0 {
		// No window size yet (a hidden or hosted window): the display's.
		w, h = ebiten.Monitor().Size()
	}
	if w <= 0 || h <= 0 {
		return
	}
	nlScreenW, nlScreenH = float64(w)*scale, float64(h)*scale
	s.noteDeviceScale(scale)
	s.reloadMods(g)
	s.draft = s.freshDraft(g)
	s.releasePreview(false)
	s.preview = newNLPreview(g.opts, g.cs)
	card := s.pages()[0].cards[0]
	key, _, _ := s.plan(card, card.get(&s.draft))
	s.preview.request(key)
	s.warmed = g
	s.tendMenuPictures(g)
}

// releasePreview retires the preview and pauses the picture loader. With
// wait, a scene still being staged is waited for and closed here, so nothing
// reads the content afterwards — what a content switch needs before it closes
// the old content.
func (s *nlScreen) releasePreview(wait bool) {
	s.art.pauseLoader()
	p := s.preview
	if p == nil {
		return
	}
	if wait && p.loading {
		if res := <-p.results; res.inst != nil {
			res.inst.close()
		}
		p.loading = false
	}
	p.Close()
	s.preview = nil
	s.warmed = nil
}

// bindShell points the screen at a shell: its mods, its draft and a preview
// on its content. A content switch binds the new shell without closing the
// screen.
func (s *nlScreen) bindShell(g *gameShell) {
	s.bound = g
	s.bindPointer(g)
	s.reloadMods(g)
	s.bindStats(g)
	s.draft = s.freshDraft(g)
	s.revealContent(s.draft.mod)
	s.touched = map[string]bool{}
	s.pendingPresets = nil
	s.installsSeen = modDownload.view().installs
	s.bindSource(g)
	if s.preview == nil {
		s.preview = newNLPreview(g.opts, g.cs)
	}
	// Unit pictures come from the running content, which a mod switch
	// replaces; the base-install art stays.
	s.tendPictures(g)
	if s.baseFS == nil {
		base := vfs.New()
		if err := base.MountGameDirectories(g.cs.baseRoots); err == nil {
			s.baseFS = base
		} else {
			base.Close()
			s.baseFS = g.cs.fs
		}
	}
}

// reloadMods reads the installed mods, as the mount will see them.
func (s *nlScreen) reloadMods(g *gameShell) {
	s.mods = nil
	if lib, err := openModLibrary(); err == nil {
		if installed, err := lib.Installed(); err == nil {
			for _, m := range installed {
				s.mods = append(s.mods, modlibrary.ResolveProfileDefaults(g.cs.baseRoots, m))
			}
		}
	}
}

// hide closes the screen. The window keeps it up, drawing nothing new,
// until the key or click that closed it is released, so Enter or Esc never
// reaches the menu underneath as a fresh press.
func (s *nlScreen) hide() {
	s.closing = true
	s.dialog = ""
	s.warmed = nil // the menu warms the screen again for its next opening
	// The menu underneath may switch content once it has the input back; its
	// idle steps resume the pictures (warm).
	s.art.pauseLoader()
	// The previews go now: a content switch requested with the close may
	// release the content set they read before the gesture ends. The window
	// keeps its last frame meanwhile, since the screen is not cleared.
	if s.preview != nil {
		s.preview.Close()
		s.preview = nil
	}
}

// finishHide releases the previews once the closing gesture has ended.
func (s *nlScreen) finishHide() {
	s.open, s.closing = false, false
	s.pointerRect = image.Rectangle{}
	if s.preview != nil {
		s.preview.Close()
		s.preview = nil
	}
	g := s.shell()
	if g != nil && g.cs != nil && clPtr != nil {
		// The preview clients registered their own model source; the window
		// client takes it back.
		clPtr.SetModelFS(g.cs.unmappedMount, g.cs.presentation.TeamLogos)
		if p := g.activePanel(); p != nil && g.frontend.Mode == modeMenuMain {
			g.refreshMainMenuModStatus(p)
		}
	}
}

// snapshot reads the shell's live preferences into a draft.
func (s *nlScreen) snapshot(g *gameShell) nlDraft {
	d := nlDraft{
		gameplay:     g.gameplay.Normalize(),
		pres:         g.presentation,
		glow:         g.display.Glow,
		glowStrength: g.display.GlowStrength,
		shadows:      g.display.Shadows, vehicleShadows: g.display.VehicleShadows,
		mutators:      g.opts.Mutators,
		unitLimit:     g.savedUnitLimit,
		fullscreen:    g.fullscreen,
		resolution:    retailDisplayMode{g.display.Width, g.display.Height},
		switchAlt:     g.switchAlt,
		override:      g.lockOverridden(g.cs.mod),
		interfaceType: g.interfaceType,
		restrictions:  nlLiveRestrictions(g).String(),
	}
	if d.glowStrength < 0 {
		d.glowStrength = settings.DefaultGlowStrength
	}
	for i, m := range s.mods {
		if sameMod(&m, g.cs.mod) {
			d.mod = i + 1
		}
	}
	return d
}

// freshDraft is the shell's live state as a draft, with its own copy of the
// key map for the Controls page to edit. snapshot alone leaves keys nil, so
// the per-frame comparisons never copy the map.
func (s *nlScreen) freshDraft(g *gameShell) nlDraft {
	d := s.snapshot(g)
	live := g.liveKeyMap()
	d.keys = input.NewKeyMap(live.Profile(), live.Overrides())
	return d
}

func (s *nlScreen) modAt(i int) *modlibrary.Mod {
	if i <= 0 || i > len(s.mods) {
		return nil
	}
	return &s.mods[i-1]
}

// dirty counts the cards (and keys) whose draft value differs from the
// shell's, keeping the shell's state in s.saved and the count in s.changed.
// Draw calls it once a frame: the header's Apply count and the carousel's
// changed lamps read what it kept, and nothing changes the draft or the
// shell while a frame draws.
func (s *nlScreen) dirty() int {
	s.saved, s.changed = nlDraft{}, 0
	g := s.shell()
	if g == nil {
		return 0
	}
	s.saved = s.snapshot(g)
	n := s.keysDiffer()
	for _, page := range s.pages() {
		for i := range page.cards {
			if c := &page.cards[i]; !nlCardEqual(*c, &s.draft, &s.saved) {
				n++
			}
		}
	}
	s.changed = n
	return n
}

func (s *nlScreen) setCard(c nlCard, v int) {
	if s.cardUnavailable(c) != "" || configurationValueUnavailable(c.key, v, s.draft.gameplay, s.draft.pres) != "" {
		return
	}
	if c.kind != nlGroup && (v < 0 || v >= len(c.steps)) {
		return
	}
	next := s.draft
	c.set(&next, v)
	s.setCardDraft(c, next)
}

// Selector indices cannot distinguish inherited defaults or legacy counts
// from explicit choices or arbitrary dimensions. Apply/restore copy those values.
func nlCardEqual(c nlCard, a, b *nlDraft) bool {
	if c.key == "resolution" {
		return a.resolution == b.resolution
	}
	if c.key == "sidebar" {
		return a.pres.ExpandedSidebar == b.pres.ExpandedSidebar && a.pres.BuildMenuPageSize == b.pres.BuildMenuPageSize &&
			a.pres.SidebarOrders == b.pres.SidebarOrders && a.pres.UIScale == b.pres.UIScale
	}
	if c.copy != nil {
		left, right := nlDraft{gameplay: a.gameplay}, nlDraft{gameplay: a.gameplay}
		c.copy(&left, a)
		c.copy(&right, b)
		return left == right
	}
	return c.get(a) == c.get(b)
}

func nlCopyCard(c nlCard, to, from *nlDraft) {
	if c.key == "resolution" {
		to.resolution = from.resolution
		return
	}
	if c.key == "sidebar" {
		to.pres.ExpandedSidebar, to.pres.BuildMenuPageSize = from.pres.ExpandedSidebar, from.pres.BuildMenuPageSize
		to.pres.SidebarOrders = from.pres.SidebarOrders
		to.pres.UIScale = from.pres.UIScale
		return
	}
	if c.copy != nil {
		c.copy(to, from)
		return
	}
	c.set(to, c.get(from))
}

func (s *nlScreen) setCardDraft(c nlCard, next nlDraft) {
	if s.cardUnavailable(c) != "" {
		return
	}
	if nlCardEqual(c, &s.draft, &next) {
		return
	}
	if c.key == "profile" {
		s.chooseProfile(next.controls)
		return
	}
	if s.cardLocked(c) {
		s.guardLocked(c, func() { s.setCardDraft(c, next) })
		return
	}
	overview := s.draft.pres.Overview
	nlCopyCard(c, &s.draft, &next)
	if c.key == "zoomstyle" && s.draft.pres.Overview != overview {
		// Carry the explicit Tab edit even if the player changes rules before
		// Apply; card copying then follows the destination's new mode.
		s.touched["tab"] = true
	}
	if c.key == "content" {
		s.revealContent(s.draft.mod)
		if g := s.shell(); g != nil {
			s.bindSource(g)
		}
	}
	s.touched[c.key] = true
	if g := s.shell(); g != nil {
		g.playMenuCue("SmallButton")
	}
}

// apply writes the draft through the shell. A content switch becomes a
// reload request; everything else takes effect now and is saved.
func (s *nlScreen) apply() {
	g := s.shell()
	if g == nil {
		return
	}
	target := s.modAt(s.draft.mod)
	if s.guardApplyLocks() {
		return
	}
	if !sameMod(target, g.cs.mod) && !g.cs.manualRoots {
		// A different content switches first, and the rest of the draft is
		// applied to the new content's settings once it is bound
		// (updateReload), so a change made in the same Apply is kept for the
		// content it was made for.
		request := contentReloadRequest{selector: "none", mutators: s.draft.mutators}
		name := "Total Annihilation"
		if target != nil {
			request.selector = modSelectorOf(target.ID, target.Version)
			request.mod = settings.ModSelection{ID: target.ID, Version: target.Version}
			name = target.Name
		}
		s.reload = &nlReload{request: request, name: name, draft: s.draft, touched: s.touched, presets: s.pendingPresets}
		s.releasePreview(true)
		g.playMenuCue("BigButton")
		return
	}
	s.applyDraft(g, s.draft, s.touched, s.pendingPresets)
	s.draft = s.freshDraft(g)
	s.toast, s.toastLeft = "Settings saved", 2.2
	g.playMenuCue("BigButton")
}

// applyDraft writes a draft to the running content's settings: a chosen
// controls profile and applied presets first, then only the cards the
// player touched, over the live state, so a profile keeps every row nobody
// changed afterwards. It saves the settings file.
func (s *nlScreen) applyDraft(g *gameShell, draft nlDraft, touched map[string]bool, presets []json.RawMessage) {
	configuredUnitLimit, savedUnitLimit := g.setup.UnitLimit, g.savedUnitLimit
	if draft.override != g.lockOverridden(g.cs.mod) {
		// Settle the settings under the old lock state, then reload them
		// under the new one, so an override brings back the player's own
		// values on locked paths (modsettings.go).
		file := g.captureSettings()
		g.setLockOverride(g.cs.mod, draft.override)
		file.ModLockOverrides = g.lockOverrides
		g.applySettings(file)
	}
	preset := nlControlsPresets[draft.controls].preset
	if preset != "" {
		g.applyControlsPreset(preset)
	}
	// Presets applied on the screen reach every setting they name, the ones
	// no card shows included; the touched cards below then write over them.
	if len(presets) > 0 {
		if eff, err := settings.Layer(g.liveSettings(), presets...); err == nil {
			g.applySettings(g.fileSettings(eff))
		}
	}
	s.pendingPresets = nil
	next := s.snapshot(g)
	for _, page := range s.pages() {
		for _, c := range page.cards {
			if touched[c.key] && c.key != "content" {
				nlCopyCard(c, &next, &draft)
			}
		}
	}
	next.override = draft.override
	p := next.pres
	p.Normalize()
	g.setPresentation(p)
	if g.display.Glow != next.glow || g.display.GlowStrength != next.glowStrength {
		g.display.Glow, g.display.GlowStrength = next.glow, next.glowStrength
		g.applyRetailVisualOptions(clPtr)
	}
	g.switchAlt = next.switchAlt
	g.interfaceType = next.interfaceType
	// The keyboard: the draft map as edited, over whatever profile the
	// preset above selected, since the profile bar already showed it.
	if draft.keys != nil && (touched["keys"] || draft.controls != 0) {
		g.keyMap = input.NewKeyMap(draft.keys.Profile(), draft.keys.Overrides())
	}
	g.savedUnitLimit = next.unitLimit
	// A restore carries its configured word independently of the preference
	// [08 R-SESS-01 §9]. Re-layering an unrelated preset or lock override
	// above must not replace it with the startup default.
	g.setup.UnitLimit = configuredUnitLimit
	if touched["unitlimit"] || next.unitLimit != savedUnitLimit {
		g.setup.UnitLimit = settings.Settings{UnitLimit: next.unitLimit}.ConfiguredUnitLimit()
		if g.opts.UnitLimit != 0 {
			g.setup.UnitLimit = g.opts.UnitLimit
		}
	}
	g.opts.Mutators, g.mutatorSetting = next.mutators, next.mutators.Map()
	// An edited restriction set becomes the running content's setting and
	// is written to its layer (DESIGN_MODS_MUTATORS §15.9).
	if touched["restrictions"] {
		g.selectRestrictions(nlRestrictionsOf(draft.restrictions))
	}
	if next.gameplay != g.gameplay.Normalize() {
		g.setGameplay(next.gameplay)
	}
	g.enforceModGameplayMinimum()
	if next.fullscreen != g.fullscreen {
		g.fullscreen = next.fullscreen
		ebiten.SetFullscreen(next.fullscreen)
	}
	g.display.Width, g.display.Height = next.resolution.W, next.resolution.H
	g.commitWindowSize()
	g.saveSettings()
	s.touched = map[string]bool{}
	s.bindSource(g)
}

// Update implements ebitenapp.FullScreen.
func (s *nlScreen) Update() {
	now := time.Now()
	dt := 1.0 / 60
	if !s.lastInput.IsZero() {
		dt = min(0.1, now.Sub(s.lastInput).Seconds())
	}
	s.lastInput = now
	s.updateInput(screenkit.ReadInput(), dt)
}

func (s *nlScreen) updateInput(in screenkit.Input, dt float64) {
	if in.Pressed || !in.Down {
		s.contentDragging = false
	}
	if s.closing {
		if !in.Held {
			s.finishHide()
		}
		return
	}
	s.hits.Update(in, dt)
	if !s.ready {
		return
	}
	pages := s.pages()
	page := pages[s.page]
	idx := min(s.focus[s.page], len(page.cards)-1)
	card := page.cards[idx]
	v := card.get(&s.draft)
	if s.reload != nil {
		s.updateReload()
		return
	}
	s.pollInstalls()
	switch s.dialog {
	case "resolution":
		s.updateResolution(in)
		return
	case "presets":
		s.updatePresets(in)
		return
	case "catalog", "remove":
		if in.KeyPressed(ebiten.KeyEscape) {
			if s.dialog == "catalog" {
				s.closeCatalog()
			} else {
				s.dialog = ""
			}
		}
		return
	}
	if s.dialog != "" {
		if in.KeyPressed(ebiten.KeyEscape) {
			s.dialog = ""
		}
		if in.KeyPressed(ebiten.KeyEnter) && !ebiten.IsKeyPressed(ebiten.KeyAlt) {
			s.confirmOverride()
		}
		return
	}
	if page.key == "controls" && s.updateControls(in) {
		return
	}
	if page.key == "controls" && in.WheelY != 0 && s.ctlTable.Contains(in.X, in.Y) {
		s.wheel += in.WheelY
		if math.Abs(s.wheel) >= 1 {
			s.ctlScroll -= int(math.Copysign(1, s.wheel))
			s.wheel = 0
		}
		return
	}
	switch {
	case in.KeyPressed(ebiten.KeyEscape):
		s.hide()
	case in.KeyPressed(ebiten.KeyEnter) && !ebiten.IsKeyPressed(ebiten.KeyAlt):
		// Alt+Enter is the window's fullscreen toggle, not Apply.
		s.apply()
	case in.KeyPressed(ebiten.KeyArrowRight):
		s.focusCard(min(idx+1, len(page.cards)-1))
	case in.KeyPressed(ebiten.KeyArrowLeft):
		s.focusCard(max(idx-1, 0))
	case in.KeyPressed(ebiten.KeyArrowUp) && card.kind == nlGroup:
		s.partSel[card.key] = max(0, s.selectedPart(&card)-1)
	case in.KeyPressed(ebiten.KeyArrowDown) && card.kind == nlGroup:
		s.partSel[card.key] = min(len(card.parts)-1, s.selectedPart(&card)+1)
	case in.KeyPressed(ebiten.KeyArrowUp) && card.kind == nlContent:
		s.step(card, v, -1)
	case in.KeyPressed(ebiten.KeyArrowDown) && card.kind == nlContent:
		s.step(card, v, 1)
	case in.KeyPressed(ebiten.KeyArrowUp):
		s.step(card, v, 1)
	case in.KeyPressed(ebiten.KeyArrowDown):
		s.step(card, v, -1)
	case in.KeyPressed(ebiten.KeyTab):
		d := 1
		if ebiten.IsKeyPressed(ebiten.KeyShift) {
			d = len(pages) - 1
		}
		s.selectPage((s.page + d) % len(pages))
	case in.KeyPressed(ebiten.KeySpace):
		if s.compareAvailable(card) {
			s.compare = !s.compare
		}
	}
	// The wheel steps whole notches, so a trackpad's fine deltas add up
	// first: over the cards it moves the focus, over the hero the value.
	s.wheel += in.WheelY
	if card.kind == nlContent && s.contentList.Contains(in.X, in.Y) {
		// Keep fractional trackpad travel and consume every whole row in a
		// larger wheel batch (DESIGN_INTERFACE_HUD_INPUT §3.17).
		d := int(s.wheel)
		s.wheel -= float64(d)
		s.scrollContent(s.contentTop - d)
		return
	}
	if math.Abs(s.wheel) >= 1 {
		d := int(math.Copysign(1, s.wheel))
		s.wheel = 0
		u := s.u()
		switch {
		case in.Y > float64(s.carouselTop()):
			s.focusCard(max(0, min(len(page.cards)-1, idx-d)))
		case in.X < 800*u && in.Y > 140*u && card.kind != nlContent:
			s.step(card, v, d)
		}
	}
}

func (s *nlScreen) step(c nlCard, v, d int) {
	if s.cardUnavailable(c) != "" || c.kind == nlRestrictions {
		return
	}
	if c.kind == nlResolution {
		s.stepResolution(d)
		return
	}
	if c.kind == nlGroup {
		if len(c.parts) == 0 {
			return
		}
		i := s.selectedPart(&c)
		part := c.parts[i]
		s.setPart(c, i, max(0, min(len(part.steps)-1, part.get(&s.draft)+d)))
		return
	}
	next := v + d
	if c.kind == nlLayers && next >= 0 {
		if locked, _ := s.stageLocked(next); locked {
			// Stepping into a locked layer asks, as clicking it does.
			s.pendingV, s.pendingAction, s.pendingWhat, s.dialog = next, nil, "", "override"
			return
		}
	}
	s.setCard(c, max(0, min(len(c.steps)-1, next)))
	if c.kind == nlContent {
		s.revealContent(c.get(&s.draft))
	}
}

func (s *nlScreen) focusCard(i int) {
	if s.focus[s.page] == i {
		return
	}
	s.focus[s.page] = i
	s.heroT = 0
	s.compare = false
}

func (s *nlScreen) selectPage(i int) {
	if s.page == i {
		return
	}
	s.page, s.pageT, s.heroT = i, 0, 0
	s.compare = false
	if g := s.shell(); g != nil {
		g.playMenuCue("SmallButton")
	}
}

// stageLocked reports whether a rules stage sits below the draft content's
// minimum and has not been overridden.
func (s *nlScreen) stageLocked(stage int) (bool, *modlibrary.Mod) {
	m := s.modAt(s.draft.mod)
	minimum, ok := modMinimumGameplay(m)
	if !ok || s.draft.override {
		return false, m
	}
	return stage < gameplayOptionStage(minimum), m
}

func (s *nlScreen) confirmOverride() {
	s.draft.override = true
	s.dialog = ""
	if action := s.pendingAction; action != nil {
		s.pendingAction = nil
		action()
	} else {
		s.touched["rules"] = true
		s.draft.gameplay = nlRuleModes[s.pendingV]
	}
	if g := s.shell(); g != nil {
		g.playMenuCue("BigButton")
	}
}

func (s *nlScreen) carouselTop() int { return int(s.h() - 232*s.u()) }

var nlScreenW, nlScreenH float64

// u is the layout unit: the design is drawn on a 900-unit-tall canvas at
// least 1560 units wide, so a narrow window scales down to fit its width and
// keeps the spare height between the hero and the cards.
func (s *nlScreen) u() float64 { return min(nlScreenH/900, nlScreenW/1560) }
func (s *nlScreen) h() float64 { return nlScreenH }
func (s *nlScreen) w() float64 { return nlScreenW }

// ---------------------------------------------------------------- drawing

var (
	nlGoldTop    = color.RGBA{255, 246, 216, 255}
	nlGoldBottom = color.RGBA{168, 143, 70, 255}
	nlKicker     = color.RGBA{184, 166, 103, 255}
	nlGreen      = color.RGBA{61, 255, 92, 255}
	nlGreenText  = color.RGBA{182, 242, 184, 255}
	nlAmber      = color.RGBA{255, 217, 122, 255}
	nlBody       = color.RGBA{226, 219, 190, 255}
	nlDim        = color.RGBA{159, 151, 122, 255}
	nlCream      = color.RGBA{255, 240, 184, 255}
	nlRed        = color.RGBA{255, 74, 51, 255}
)

func alphaC(c color.RGBA, a float64) color.RGBA {
	a = clamp(a, 0, 1)
	return color.RGBA{c.R, c.G, c.B, uint8(float64(c.A) * a)}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Draw implements ebitenapp.FullScreen.
func (s *nlScreen) Draw(screen *ebiten.Image) {
	s.restorePointer(screen)
	if s.closing || (s.preview == nil && s.reload == nil) {
		s.drawPointer(screen, time.Now())
		return
	}
	now := time.Now()
	dt := 1.0 / 60
	if !s.lastFrame.IsZero() {
		dt = min(0.1, now.Sub(s.lastFrame).Seconds())
	}
	s.lastFrame = now
	defer s.drawPointer(screen, now)
	s.clock += dt
	s.dt = dt
	s.heroT = min(1, s.heroT+dt/0.28)
	s.openT = min(1, s.openT+dt/0.35)
	s.pageT = min(1, s.pageT+dt/0.45)
	s.toastLeft = max(0, s.toastLeft-dt)
	g := s.shell()
	if !s.ready {
		s.fonts = screenkit.LoadFonts()
		s.art = loadNLArt(s.baseFS)
		s.ready = true
	}
	b := screen.Bounds()
	nlScreenW, nlScreenH = float64(b.Dx()), float64(b.Dy())
	u := s.u()
	s.hits.Begin()
	defer s.hits.End()
	if s.reload != nil && s.preview == nil {
		// During a content switch the last frame stays and the plate goes
		// over it; nothing reads the old content's preview.
		s.drawReload(screen)
		return
	}

	pages := s.pages()
	page := pages[s.page]
	idx := min(s.focus[s.page], len(page.cards)-1)
	card := page.cards[idx]
	v := card.get(&s.draft)
	s.dirty()
	s.tendPictures(g)

	s.drawBackground(screen, card, v, dt)
	s.drawChrome(screen, card)
	if page.key == "controls" {
		// A mapping view, not a deck: no hero, no cards, no demonstration.
		s.drawControls(screen)
		s.drawHeader(screen, pages)
	} else {
		if s.demoKey != page.key+card.key {
			s.demoKey, s.demoT = page.key+card.key, 0
		}
		s.demoT += dt
		s.drawDemo(screen, card, v)
		s.drawHeader(screen, pages)
		s.drawHero(screen, page, &page.cards[idx], idx, v)
		s.drawCarousel(screen, page, idx, dt)
	}
	s.drawLoadout(screen, pages)
	s.drawHints(screen)
	if page.key != "controls" {
		s.drawPreviewLimit(screen)
	}
	if s.preview.Loading() && s.preview.Ready() {
		s.drawStaging(screen)
	}
	switch s.dialog {
	case "":
	case "resolution":
		s.drawResolutionDialog(screen)
	case "presets":
		s.drawPresets(screen)
	case "catalog":
		s.drawCatalog(screen)
	case "remove":
		s.drawRemoveConfirm(screen)
	default:
		s.drawDialog(screen)
	}
	if s.toastLeft > 0 {
		a := min(1, s.toastLeft/0.4)
		f := s.fonts.Display
		st := screenkit.Style{Size: 14 * u, Tracking: 0.12, Top: alphaC(nlGreenText, a), Upper: true, Align: 1}
		tw := f.Measure(s.toast, st) + 40*u
		r := screenkit.Rect{X: s.w()/2 - tw/2, Y: 100 * u, W: tw, H: 40 * u}
		screenkit.Fill(screen, r, color.RGBA{6, 14, 6, uint8(220 * a)})
		screenkit.Outline(screen, r, 1.5*u, alphaC(color.RGBA{60, 120, 60, 255}, a))
		f.Draw(screen, s.toast, s.w()/2, r.Y+27*u, st)
	}
	if s.reload != nil {
		s.drawReload(screen)
	}
	// The screen rises out of black as it opens.
	if s.openT < 1 {
		screenkit.Fill(screen, screenkit.Rect{W: s.w(), H: s.h()}, color.RGBA{0, 0, 0, uint8(255 * (1 - s.openT))})
	}
}

// drawLoadout is the one line that says what a battle started now would
// run: content, rules, renderer, mutators and unit restrictions. Each part
// jumps to its card.
func (s *nlScreen) drawLoadout(screen *ebiten.Image, pages []nlPage) {
	u := s.u()
	d := &s.draft
	content := "Total Annihilation"
	if m := s.modAt(d.mod); m != nil {
		content = s.ui.text(nlTextKey{kind: "mod", a: m.Name, b: m.Version}, func() string { return m.Name + " " + m.Version })
	}
	renderer := "Enhanced"
	if d.pres.Renderer == "classic" {
		renderer = "Classic"
	}
	parts := [...]struct{ text, page, card, id string }{
		{content, "game", "content", "loadout-gamecontent"},
		{s.ui.text(nlTextKey{kind: "rules", a: gameplayLabel(d.gameplay)}, func() string { return gameplayLabel(d.gameplay) + " rules" }), "game", "rules", "loadout-gamerules"},
		{renderer, "graphics", "renderer", "loadout-graphicsrenderer"},
		{s.mutatorsLabel(d.mutators), "mutators", "", "loadout-mutators"},
		{s.restrictionsLabel(), "mutators", "restrictions", "loadout-restrictions"},
	}
	x, y := 60*u, 96*u
	bf := s.fonts.Body
	for i, part := range parts {
		if i > 0 {
			screenkit.Disc(screen, x+8*u, y-4*u, 2*u, alphaC(nlKicker, 0.8))
			x += 18 * u
		}
		id := part.id
		hover := s.hits.HoverAmount(id)
		st := screenkit.Style{Size: 11.5 * u, Top: lerpRGBA(color.RGBA{190, 182, 150, 255}, color.RGBA{255, 244, 210, 255}, hover), Shadow: 0.12}
		tw := bf.Draw(screen, part.text, x, y, st)
		if hover > 0 {
			screenkit.Fill(screen, screenkit.Rect{X: x, Y: y + 4*u, W: tw, H: 1 * u}, alphaC(nlGoldTop, hover))
		}
		s.hits.Add(screenkit.Region{ID: id, Rect: screenkit.Rect{X: x - 4*u, Y: y - 14*u, W: tw + 8*u, H: 20 * u}, Click: func() {
			for pi, p := range pages {
				if p.key != part.page {
					continue
				}
				s.selectPage(pi)
				for ci, c := range p.cards {
					if c.key == part.card {
						s.focusCard(ci)
					}
				}
			}
		}})
		x += tw + 8*u
	}
}

// mutatorsLabel names the mutators in the loadout line: none, the one, or
// how many.
func (s *nlScreen) mutatorsLabel(m content.Mutators) string {
	return s.ui.text(nlTextKey{kind: "mutators", a: s.ui.mutatorKey(m)}, func() string {
		switch desc := m.Describe(); len(desc) {
		case 0:
			return "No mutators"
		case 1:
			return desc[0]
		default:
			return fmt.Sprintf("%d mutators", len(desc))
		}
	})
}

// drawHints names the keyboard, bottom right under the cards.
func (s *nlScreen) drawHints(screen *ebiten.Image) {
	u := s.u()
	// "lr" and "ud" are drawn as chevrons: the typefaces carry no arrows.
	hints := [][2]string{{"lr", "Choose"}, {"ud", "Change"}, {"Tab", "Page"}, {"Space", "Compare"}, {"Enter", "Apply"}, {"Esc", "Back"}}
	if s.pages()[s.page].key == "controls" {
		hints = [][2]string{{"ud", "Action"}, {"Space", "Rebind"}, {"Tab", "Page"}, {"Enter", "Apply"}, {"Esc", "Back"}}
		if s.capture.id != "" {
			return // the table's own line says what a press does
		}
	}
	x := s.w() - 56*u
	y := s.h() - 14*u
	df, bf := s.fonts.Display, s.fonts.Body
	for i := len(hints) - 1; i >= 0; i-- {
		h := hints[i]
		ls := screenkit.Style{Size: 10 * u, Top: color.RGBA{150, 142, 116, 255}, Align: 2}
		x -= bf.Draw(screen, h[1], x, y, ls) + 6*u
		ks := screenkit.Style{Size: 10 * u, Tracking: 0.08, Top: color.RGBA{220, 210, 170, 255}, Align: 2}
		kw := df.Measure(h[0], ks)
		if h[0] == "lr" || h[0] == "ud" {
			kw = 22 * u
		}
		screenkit.Outline(screen, screenkit.Rect{X: x - kw - 6*u, Y: y - 12*u, W: kw + 12*u, H: 16 * u}, 1*u, color.RGBA{90, 86, 70, 255})
		cx, cy, c := x-kw/2, y-4*u, color.RGBA{220, 210, 170, 255}
		switch h[0] {
		case "lr":
			s.chevron(screen, cx-6*u, cy, -1, 0, 3.5*u, c)
			s.chevron(screen, cx+6*u, cy, 1, 0, 3.5*u, c)
		case "ud":
			s.chevron(screen, cx-6*u, cy, 0, -1, 3.5*u, c)
			s.chevron(screen, cx+6*u, cy, 0, 1, 3.5*u, c)
		default:
			df.Draw(screen, h[0], x, y, ks)
		}
		x -= kw + 26*u
	}
}

// chevron draws a small arrowhead pointing along (dx, dy).
func (s *nlScreen) chevron(screen *ebiten.Image, cx, cy, dx, dy, r float64, c color.RGBA) {
	w := max(1, r*0.4)
	tipX, tipY := cx+dx*r*0.5, cy+dy*r*0.5
	backX, backY := cx-dx*r*0.5, cy-dy*r*0.5
	screenkit.Line(screen, tipX, tipY, backX-dy*r, backY-dx*r, w, c)
	screenkit.Line(screen, tipX, tipY, backX+dy*r, backY+dx*r, w, c)
}

// drawStaging is the small lamp that says a new scene is being staged
// while the previous picture holds.
func (s *nlScreen) drawStaging(screen *ebiten.Image) {
	u := s.u()
	x, y := s.w()-60*u, 104*u
	for i := 0; i < 3; i++ {
		lit := int(s.clock*6)%3 == i
		s.lamp(screen, x-float64(2-i)*14*u, y, 4*u, nlGreen, lit)
	}
	s.fonts.Display.Draw(screen, "Staging", x-44*u, y+4*u, screenkit.Style{Size: 11 * u, Tracking: 0.2, Top: nlKicker, Upper: true, Align: 2})
}

// A capability note belongs to the scene actually requested, including its
// rules and mutators; a previous scene may keep playing while that one stages.
func (s *nlScreen) drawPreviewLimit(screen *ebiten.Image) {
	p := s.preview
	if p == nil || p.cur == nil || p.cur.key != p.want || p.cur.previewLimit == "" {
		return
	}
	u := s.u()
	st := screenkit.Style{Size: 11.5 * u, Top: nlAmber, Shadow: 0.12}
	const leading = 1.75
	width := 360 * u
	lines := s.ui.wrap(s.fonts.Body, p.cur.previewLimit, st, width)
	height := float64(len(lines))*st.Size*leading + 32*u
	x, y := s.w()-420*u, float64(s.carouselTop())-height-14*u
	screenkit.Fill(screen, screenkit.Rect{X: x - 12*u, Y: y - 12*u, W: width + 24*u, H: height}, color.RGBA{10, 14, 8, 224})
	screenkit.Outline(screen, screenkit.Rect{X: x - 12*u, Y: y - 12*u, W: width + 24*u, H: height}, u, color.RGBA{120, 103, 54, 210})
	s.fonts.Display.Draw(screen, "Preview", x, y+10*u, screenkit.Style{Size: 10 * u, Tracking: 0.2, Top: nlKicker, Upper: true})
	s.drawWrapped(screen, s.fonts.Body, p.cur.previewLimit, x, y+20*u, width, leading, st)
}

func (s *nlScreen) previewSize() (int, int) {
	scale := s.canvasScale
	if scale == 0 {
		scale = s.cachedDeviceScale()
	}
	if scale < 1 {
		scale = 1
	}
	w, h := int(s.w()/scale), int(s.h()/scale)
	if w > 1920 {
		h = h * 1920 / w
		w = 1920
	}
	// Sizes snap to 64-pixel steps, so dragging the window's edge restages
	// the scene a few times rather than at every pixel; the cover crop
	// absorbs the difference.
	w, h = max(320, (w+63)/64*64), max(240, (h+63)/64*64)
	return w, h
}

// cachedDeviceScale is the display's device scale, read again four times a
// second of the screen's clock and whenever the canvas changes size, never
// every frame: in Ebitengine's multi-threaded mode each Monitor query waits
// for the OS main thread. A move to a display of another scale also resizes
// the canvas, so it is seen at once.
func (s *nlScreen) cachedDeviceScale() float64 {
	if !s.deviceScaleRead || s.clock-s.deviceScaleAt >= 0.25 || s.w() != s.deviceScaleW || s.h() != s.deviceScaleH {
		s.noteDeviceScale(ebiten.Monitor().DeviceScaleFactor())
	}
	return s.deviceScale
}

func (s *nlScreen) noteDeviceScale(scale float64) {
	s.deviceScale, s.deviceScaleRead = scale, true
	s.deviceScaleAt, s.deviceScaleW, s.deviceScaleH = s.clock, s.w(), s.h()
}

// plan picks the scene and frame parameters for the focused card.
func (s *nlScreen) plan(card nlCard, v int) (nlSceneKey, nlRender, *nlRender) {
	d := &s.draft
	r := nlRender{
		classic: d.pres.Renderer == "classic",
		// The background repaints at the rate a battle would present at
		// (nlCadence); the Frame rate card shows its own value.
		fps: d.pres.FPS,
	}
	applyNLDraftEffects(d, &r)
	if card.render != nil {
		card.render(d, v, &r)
	}
	w, h := s.previewSize()
	preset := card.scene(d, v)
	if f := nlPresets[preset].surface; f > 0 {
		w, h = int(float64(w)*f)&^1, int(float64(h)*f)&^1
	}
	key := nlSceneKey{preset: preset, gameplay: d.gameplay, w: w, h: h}
	if card.usesMutators {
		key.mutators = s.ui.mutatorKey(d.mutators)
	}
	var alt *nlRender
	if s.compare && s.compareAvailable(card) {
		if bv, ok := card.compare(d, v); ok {
			if card.usesMutators {
				// A mutator changes the simulation, so its compare is a twin
				// scene under the other factor rather than a second render.
				tw := nlDraft{mutators: d.mutators}
				card.set(&tw, bv)
				key.paired, key.twinMutators = true, s.ui.mutatorKey(tw.mutators)
				a := r
				return key, r, &a
			}
			a := r
			a.classic = d.pres.Renderer == "classic"
			if card.render != nil {
				card.render(d, bv, &a)
			}
			alt = &a
		}
	}
	return key, r, alt
}

func applyNLDraftEffects(d *nlDraft, r *nlRender) {
	r.effects = presentationEffects(d.pres)
	r.glow, r.glowStrength = d.glow != 0, d.glowStrength
	r.trailStrength = d.pres.TrailStrength
	r.groundLightStrength, r.blastRingStrength = d.pres.GroundLightStrength, d.pres.BlastRingStrength
}

// nlBlinkPhase is how long a blink compare holds each value, seconds: long
// enough to take in the frame, short enough that the eye still compares the
// two from memory.
const nlBlinkPhase = 0.8

// blinkShowsAlt reports whether a blink compare is showing the compared
// value rather than the chosen one.
func (s *nlScreen) blinkShowsAlt() bool {
	return int(s.clock/nlBlinkPhase)%2 == 0
}

func (s *nlScreen) drawBackground(screen *ebiten.Image, card nlCard, v int, dt float64) {
	key, primary, alt := s.plan(card, v)
	s.preview.request(key)
	s.preview.Frame(dt, primary, alt)
	full := screenkit.Rect{W: s.w(), H: s.h()}
	p := s.preview
	s.paired = key.paired && p.cur != nil && p.cur.twin != nil && p.alt != nil
	if p.frame == nil {
		s.drawIdleBackground(screen)
	} else if s.paired {
		s.drawPaired(screen, card, v)
	} else if alt != nil && p.alt != nil && card.blink {
		img := p.frame
		if s.blinkShowsAlt() {
			img = p.alt
		}
		screenkit.Cover(screen, img, full, 1)
	} else if alt != nil && p.alt != nil {
		screenkit.Cover(screen, p.alt, full, 1)
		x := clamp(s.wipe, 0.02, 0.98) * s.w()
		fb := p.frame.Bounds()
		scale := s.w() / float64(fb.Dx())
		sub := p.frame.SubImage(image.Rect(int(x/scale), 0, fb.Dx(), fb.Dy())).(*ebiten.Image)
		screenkit.Image(screen, sub, screenkit.Rect{X: x, Y: 0, W: s.w() - x, H: s.h()}, 1, screenkit.Crisp(scale))
	} else {
		screenkit.Cover(screen, p.frame, full, 1)
	}
	if p.fade != nil && p.fadeLeft > 0 {
		screenkit.Cover(screen, p.fade, full, p.fadeLeft)
	}
	if !s.paired && p.frame != nil && p.cur != nil && key.preset == "blast" && p.fadeLeft == 0 {
		fb := p.frame.Bounds()
		scale := max(s.w()/float64(fb.Dx()), s.h()/float64(fb.Dy()))
		ox := (float64(fb.Dx()) - s.w()/scale) / 2
		oy := (float64(fb.Dy()) - s.h()/scale) / 2
		s.drawHealthBars(screen, p.cur, full, func(px, py float64) (float64, float64) {
			return (px - ox) * scale, (py - oy) * scale
		})
	}
}

// drawHealthBars puts a health bar over every hurt unit of a scene, and over
// every enemy ground unit in the blast scene, so who a shell hits and by how
// much reads at a glance. project maps a surface pixel to the screen.
func (s *nlScreen) drawHealthBars(screen *ebiten.Image, inst *nlPreviewInstance, clip screenkit.Rect, project func(px, py float64) (float64, float64)) {
	if inst == nil {
		return
	}
	u := s.u()
	view := inst.b.cam.PresentationView()
	all := inst.key.preset == "blast"
	for _, m := range inst.marks {
		if m.maxHealth <= 0 || m.building > 0 || (!all && m.health >= m.maxHealth) || (all && m.own) {
			continue
		}
		px := (m.x-view.X)*view.Factor - float64(camera.OriginX)
		py := (m.z-m.y/2-view.Z)*view.Factor - float64(camera.OriginY)
		sx, sy := project(px, py)
		if !clip.Contains(sx, sy) {
			continue
		}
		w, h := 44*u, 6*u
		bar := screenkit.Rect{X: sx - w/2, Y: sy - 30*u, W: w, H: h}
		frac := clamp(float64(m.health)/float64(m.maxHealth), 0, 1)
		c := color.RGBA{61, 220, 92, 255}
		switch {
		case frac < 0.34:
			c = nlRed
		case frac < 0.67:
			c = nlAmber
		}
		screenkit.Fill(screen, bar.Inset(-1*u), color.RGBA{0, 0, 0, 220})
		screenkit.Fill(screen, bar, color.RGBA{40, 16, 12, 230})
		screenkit.Fill(screen, screenkit.Rect{X: bar.X, Y: bar.Y, W: bar.W * frac, H: bar.H}, c)
	}
}

// pairedColumns is where a twin compare draws its two scenes: side by side
// in the part of the screen the hero leaves clear.
func (s *nlScreen) pairedColumns() (left, right screenkit.Rect) {
	u := s.u()
	x0 := min(0.46*s.w(), 800*u)
	top, bottom := 110*u, float64(s.carouselTop())-24*u
	cw := (s.w() - x0 - 40*u - 12*u) / 2
	left = screenkit.Rect{X: x0, Y: top, W: cw, H: bottom - top}
	right = screenkit.Rect{X: x0 + cw + 12*u, Y: top, W: cw, H: bottom - top}
	return left, right
}

// drawPaired draws a mutator compare: the ×1 twin and the chosen factor,
// each cropped on the same ground around the fight both cameras follow.
func (s *nlScreen) drawPaired(screen *ebiten.Image, card nlCard, v int) {
	p := s.preview
	full := screenkit.Rect{W: s.w(), H: s.h()}
	screenkit.Cover(screen, p.frame, full, 1)
	screenkit.Fill(screen, full, color.RGBA{0, 0, 0, 150})
	fb := p.frame.Bounds()
	scale := max(s.w()/float64(fb.Dx()), s.h()/float64(fb.Dy()))
	// The camera frames the fight right of centre and a little high
	// (nlPreviewInstance.applyCamera); both crops centre on that point.
	fx, fy := 0.66*float64(fb.Dx()), 0.42*float64(fb.Dy())
	if inst := p.cur; inst.focusSeen {
		// A camera clamped at the map's edge leaves the fight short of that
		// point, so the crops project the point it aimed at instead.
		dx, dz, _ := inst.preset.camera(inst.seconds())
		view := inst.b.cam.PresentationView()
		fx = (inst.focusX+dx-view.X)*view.Factor - float64(camera.OriginX)
		fy = (inst.focusZ+dz-view.Z)*view.Factor - float64(camera.OriginY)
	}
	u := s.u()
	left, right := s.pairedColumns()
	crop := func(img *ebiten.Image, r screenkit.Rect, inst *nlPreviewInstance) {
		cw, ch := r.W/scale, r.H/scale
		x0 := clamp(fx-cw/2, 0, float64(fb.Dx())-cw)
		y0 := clamp(fy-ch/2, 0, float64(fb.Dy())-ch)
		sub := img.SubImage(image.Rect(int(x0), int(y0), int(x0+cw), int(y0+ch))).(*ebiten.Image)
		screenkit.Fill(screen, r.Inset(-3*u), color.RGBA{0, 0, 0, 255})
		screenkit.Image(screen, sub, r, 1, screenkit.Crisp(scale))
		// Structures still rising carry their progress, read from that
		// scene's own simulation, so a build-rate difference reads at once.
		view := inst.b.cam.PresentationView()
		for _, m := range inst.marks {
			if m.building <= 0 || !m.own {
				continue
			}
			px := (m.x-view.X)*view.Factor - float64(camera.OriginX)
			py := (m.z-m.y/2-view.Z)*view.Factor - float64(camera.OriginY)
			sx, sy := r.X+(px-x0)*scale, r.Y+(py-y0)*scale
			if !r.Contains(sx, sy) {
				continue
			}
			pct := int((1 - m.building) * 100)
			label := s.ui.text(nlTextKey{kind: "percent", i: pct}, func() string { return fmt.Sprintf("%d%%", pct) })
			st := screenkit.Style{Size: 13 * u, Tracking: 0.04, Top: nlGreenText, Align: 1, Shadow: 0.1}
			tw := s.fonts.Display.Measure(label, st) + 12*u
			tag := screenkit.Rect{X: sx - tw/2, Y: sy - 11*u, W: tw, H: 22 * u}
			screenkit.Fill(screen, tag, color.RGBA{6, 12, 6, 200})
			s.fonts.Display.Draw(screen, label, sx, tag.Y+16*u, st)
		}
		s.drawHealthBars(screen, inst, r, func(px, py float64) (float64, float64) {
			return r.X + (px-x0)*scale, r.Y + (py-y0)*scale
		})
		// The viewer's metal, from that scene's own economy, for the income
		// and salvage mutators.
		if key := card.key; key == "mut-salvage" || key == "mut-income" {
			text := fmt.Sprintf("Metal %d", int(inst.economy.Metal))
			st := screenkit.Style{Size: 16 * u, Tracking: 0.06, Top: nlCream, Align: 1, Shadow: 0.08}
			tw := s.fonts.Display.Measure(text, st) + 24*u
			tag := screenkit.Rect{X: r.X + r.W/2 - tw/2, Y: r.Y + r.H - 44*u, W: tw, H: 32 * u}
			screenkit.Fill(screen, tag, color.RGBA{6, 12, 6, 220})
			screenkit.Outline(screen, tag, 1*u, color.RGBA{60, 90, 60, 255})
			s.fonts.Display.Draw(screen, text, tag.X+tw/2, tag.Y+22*u, st)
		}
	}
	crop(p.alt, left, p.cur.twin)
	crop(p.frame, right, p.cur)
	bv, _ := card.compare(&s.draft, v)
	for i, r := range [2]screenkit.Rect{left, right} {
		label := card.steps[bv]
		if i == 1 {
			label = nlCardValueText(card, &s.draft)
		}
		edge := color.RGBA{90, 90, 80, 255}
		if i == 1 {
			edge = color.RGBA{255, 227, 138, 255}
		}
		screenkit.Outline(screen, r.Inset(-3*u), 2*u, edge)
		st := screenkit.Style{Size: 22 * u, Tracking: 0.06, Top: nlCream, Align: 1, Shadow: 0.08}
		tw := s.fonts.Display.Measure(label, st) + 28*u
		tag := screenkit.Rect{X: r.X + r.W/2 - tw/2, Y: r.Y + 10*u, W: tw, H: 38 * u}
		screenkit.Fill(screen, tag, color.RGBA{6, 12, 6, 220})
		screenkit.Outline(screen, tag, 1*u, color.RGBA{60, 90, 60, 255})
		s.fonts.Display.Draw(screen, label, r.X+r.W/2, tag.Y+28*u, st)
	}
}

func (s *nlScreen) drawIdleBackground(screen *ebiten.Image) {
	screen.Fill(color.RGBA{14, 13, 10, 255})
	if s.art.texture != nil {
		tb := s.art.texture.Bounds()
		k := math.Max(2, math.Round(s.u()*2))
		for y := 0.0; y < s.h(); y += float64(tb.Dy()) * k {
			for x := 0.0; x < s.w(); x += float64(tb.Dx()) * k {
				screenkit.Image(screen, s.art.texture, screenkit.Rect{X: x, Y: y, W: float64(tb.Dx()) * k, H: float64(tb.Dy()) * k}, 0.55, true)
			}
		}
	}
	u := s.u()
	cx, cy := s.w()*0.72, s.h()*0.42
	for i := 0; i < 8; i++ {
		a := s.clock*3 + float64(i)*math.Pi/4
		fade := 0.25 + 0.75*math.Mod(float64(i)+s.clock*8, 8)/8
		screenkit.Disc(screen, cx+math.Cos(a)*22*u, cy+math.Sin(a)*22*u, 4*u, alphaC(nlGreen, fade))
	}
	s.fonts.Display.Draw(screen, "Staging a live battle", cx, cy+60*u, screenkit.Style{Size: 13 * u, Tracking: 0.2, Top: nlKicker, Upper: true, Align: 1})
}

func (s *nlScreen) drawChrome(screen *ebiten.Image, card nlCard) {
	w, h := s.w(), s.h()
	black := func(a float64) color.RGBA { return color.RGBA{0, 0, 0, uint8(a * 255)} }
	screenkit.HGradient(screen, screenkit.Rect{X: 0, Y: 0, W: w * 0.4, H: h}, black(0.9), black(0.62))
	screenkit.HGradient(screen, screenkit.Rect{X: w * 0.4, Y: 0, W: w * 0.28, H: h}, black(0.62), black(0))
	screenkit.VGradient(screen, screenkit.Rect{X: 0, Y: h * 0.62, W: w, H: h * 0.38}, black(0), black(0.88))
	screenkit.VGradient(screen, screenkit.Rect{X: 0, Y: 0, W: w, H: h * 0.16}, black(0.75), black(0))
	if s.compare && s.preview.alt != nil && !s.paired && !card.blink {
		u := s.u()
		x := clamp(s.wipe, 0.02, 0.98) * w
		screenkit.Glow(screen, screenkit.Rect{X: x - 18*u, Y: 0, W: 36 * u, H: h}, color.RGBA{255, 214, 92, 70})
		screenkit.Fill(screen, screenkit.Rect{X: x - 1.5*u, Y: 0, W: 3 * u, H: h}, color.RGBA{255, 214, 92, 255})
		knob := screenkit.Rect{X: x - 16*u, Y: h*0.5 - 16*u, W: 32 * u, H: 32 * u}
		screenkit.Disc(screen, x, h*0.5, 16*u, color.RGBA{30, 26, 12, 230})
		screenkit.Ring(screen, x, h*0.5, 16*u, 2*u, color.RGBA{255, 214, 92, 255})
		screenkit.Line(screen, x-7*u, h*0.5, x-3*u, h*0.5-4*u, 2*u, nlAmber)
		screenkit.Line(screen, x-7*u, h*0.5, x-3*u, h*0.5+4*u, 2*u, nlAmber)
		screenkit.Line(screen, x+7*u, h*0.5, x+3*u, h*0.5-4*u, 2*u, nlAmber)
		screenkit.Line(screen, x+7*u, h*0.5, x+3*u, h*0.5+4*u, 2*u, nlAmber)
		s.hits.Add(screenkit.Region{ID: "wipe", Rect: screenkit.Rect{X: x - 18*u, Y: 110 * u, W: 36 * u, H: h - 360*u}, Drag: func(px, _ float64) {
			s.wipe, s.wipeTouched = clamp(px/w, 0.02, 0.98), true
		}})
		_ = knob
	}
}

// ---------------------------------------------------------------- header

func (s *nlScreen) drawHeader(screen *ebiten.Image, pages []nlPage) {
	u := s.u()
	x := 56 * u
	if wm := s.art.wordmark; wm != nil {
		b := wm.Bounds()
		k := 44 * u / float64(b.Dy())
		screenkit.Image(screen, wm, screenkit.Rect{X: x, Y: 22 * u, W: float64(b.Dx()) * k, H: 44 * u}, 1, true)
		x += float64(b.Dx())*k + 64*u
	} else {
		x += s.fonts.Display.Draw(screen, "NANOLATHE", x, 60*u, screenkit.Style{Size: 34 * u, Tracking: 0.08, Top: nlGoldTop, Bottom: nlGoldBottom, Shadow: 0.06}) + 64*u
	}
	f := s.fonts.Display
	for i, p := range pages {
		st := screenkit.Style{Size: 16 * u, Tracking: 0.14, Upper: true, Shadow: 0.1}
		id := s.ui.id("tab-", p.key, -1, -1)
		hover := s.hits.HoverAmount(id)
		switch {
		case i == s.page:
			st.Top = nlCream
		default:
			st.Top = lerpRGBA(color.RGBA{183, 174, 140, 255}, color.RGBA{255, 255, 255, 255}, hover)
		}
		tw := f.Draw(screen, p.title, x, 58*u, st)
		if i == s.page {
			screenkit.Glow(screen, screenkit.Rect{X: x - 10*u, Y: 60 * u, W: tw + 20*u, H: 16 * u}, color.RGBA{61, 255, 92, 60})
			screenkit.VGradient(screen, screenkit.Rect{X: x, Y: 67 * u, W: tw, H: 3 * u}, color.RGBA{184, 255, 194, 255}, color.RGBA{21, 168, 43, 255})
		}
		s.hits.Add(screenkit.Region{ID: id, Rect: screenkit.Rect{X: x - 10*u, Y: 30 * u, W: tw + 20*u, H: 46 * u}, Click: func() { s.selectPage(i) }})
		x += tw + 34*u
	}
	// Apply and Back on the right.
	dirty := s.changed
	aw, bw, bh := 150*u, 120*u, 44*u
	ax := s.w() - 56*u - aw
	label := "Apply"
	if dirty > 0 {
		label = s.ui.text(nlTextKey{kind: "apply", i: dirty}, func() string { return fmt.Sprintf("Apply  %d", dirty) })
	}
	s.button(screen, "apply", screenkit.Rect{X: ax, Y: 24 * u, W: aw, H: bh}, label, true, dirty > 0, func() { s.apply() })
	s.button(screen, "back", screenkit.Rect{X: ax - 14*u - bw, Y: 24 * u, W: bw, H: bh}, "Back", false, false, func() { s.hide() })
	pw := 140 * u
	s.button(screen, "presets", screenkit.Rect{X: ax - 28*u - bw - pw, Y: 24 * u, W: pw, H: bh}, "Presets", false, false, func() {
		s.dialog, s.presetScopes, s.presetName = "presets", [3]bool{true, true, true}, ""
	})
}

func lerpRGBA(a, b color.RGBA, t float64) color.RGBA {
	t = clamp(t, 0, 1)
	return color.RGBA{uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t), uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t), uint8(float64(a.A) + (float64(b.A)-float64(a.A))*t)}
}

// buttonAuto is a plain button sized to its label; it returns its width.
func (s *nlScreen) buttonAuto(screen *ebiten.Image, id string, x, y float64, label string, click func()) float64 {
	u := s.u()
	w := s.fonts.Display.Measure(label, screenkit.Style{Size: 14 * u, Tracking: 0.12, Upper: true}) + 36*u
	s.button(screen, id, screenkit.Rect{X: x, Y: y, W: w, H: 36 * u}, label, false, false, click)
	return w
}

// button is the mounted stock metal plate; gold marks the committing action.
func (s *nlScreen) button(screen *ebiten.Image, id string, r screenkit.Rect, label string, gold, pulse bool, click func()) {
	u := s.u()
	if pulse {
		a := 0.35 + 0.25*math.Sin(s.clock*4)
		screenkit.Glow(screen, screenkit.Rect{X: r.X - 20*u, Y: r.Y - 16*u, W: r.W + 40*u, H: r.H + 32*u}, alphaC(color.RGBA{255, 205, 80, 255}, a))
	}
	disabled := click == nil
	s.buttonPlate(screen, id, r, gold, disabled)
	text := color.RGBA{244, 236, 206, 255}
	if gold {
		text = nlCream
	}
	if disabled {
		text = nlDim
	}
	size := max(8, 14*u)
	st := screenkit.Style{Size: size, Tracking: 0.06, Top: text, Upper: true, Align: 1, Shadow: 0.1}
	for st.Size > 6 && s.fonts.Display.Measure(label, st) > r.W-12*u {
		st.Size -= 0.5
	}
	// The caption stays still while the authored plate changes [07 R-WGT-01 §3].
	s.buttonCaption(screen, label, r.X+r.W/2, r.Y+r.H/2+st.Size/2, st)
	s.hits.Add(screenkit.Region{ID: id, Rect: r, Disable: disabled, Click: click})
}

func (s *nlScreen) settingButton(screen *ebiten.Image, id string, r screenkit.Rect, label string, disabled bool, click func()) {
	if disabled {
		click = nil
	}
	s.button(screen, id, r, label, false, false, click)
}

// buttonCaption retains readable stock-style pale lettering with a dark
// outline over the textured face, including compact key caps.
func (s *nlScreen) buttonCaption(screen *ebiten.Image, label string, x, y float64, st screenkit.Style) {
	outline := st
	outline.Top, outline.Bottom, outline.Shadow = color.RGBA{10, 12, 10, 235}, color.RGBA{}, 0
	d := max(0.65, st.Size/14)
	for _, off := range [4][2]float64{{-d, 0}, {d, 0}, {0, -d}, {0, d}} {
		s.fonts.Display.Draw(screen, label, x+off[0], y+off[1], outline)
	}
	st.Shadow = 0
	s.fonts.Display.Draw(screen, label, x, y, st)
}

// buttonPlate is shared by actions, profile choices and keyboard caps.
func (s *nlScreen) buttonPlate(screen *ebiten.Image, id string, r screenkit.Rect, selected, disabled bool) {
	u := s.u()
	hover := s.hits.HoverAmount(id)
	pressed := s.hits.Active() == id && s.hits.Hot() == id
	if !s.art.drawButton(screen, r, pressed, disabled) {
		screenkit.Fill(screen, r, color.RGBA{70, 70, 63, 255})
		screenkit.Bevel(screen, r, max(1, 2*u), color.RGBA{201, 201, 193, 255}, color.RGBA{30, 30, 27, 255}, pressed)
	}
	if hover > 0 && !disabled {
		screenkit.Fill(screen, r.Inset(max(1, 3*u)), color.RGBA{255, 255, 255, uint8(8 * hover)})
	}
	if selected {
		screenkit.Outline(screen, r, max(1, u), color.RGBA{230, 206, 131, 255})
	}
}

// bindPointer gives the screen its own playback from the currently mounted
// cursor bank, never the underlying menu's or preview's mutable cursor.
func (s *nlScreen) bindPointer(g *gameShell) {
	if g == nil || g.cs == s.pointerCS {
		return
	}
	s.pointerCS, s.pointer, s.pointerPal = g.cs, nil, nil
	s.pointerImages = map[*formats.GAFFrame]*ebiten.Image{}
	s.pointerClock, s.pointerTick = time.Time{}, 0
	if g.cs == nil || g.cs.fs == nil {
		return
	}
	s.pointer, _ = client.LoadCursors(g.cs.fs)
	if g.assets != nil {
		s.pointerPal = g.assets.pal
	}
}

// OwnsPointer implements the optional FullScreen pointer contract. Missing art
// leaves the native pointer visible, including after a content reload fails.
func (s *nlScreen) OwnsPointer() bool {
	f := s.pointer.Frame()
	return s.Active() && s.pointerPal != nil && f != nil && f.Width > 0 && f.Height > 0
}

func (s *nlScreen) pointerShape() int {
	if !s.closing && (s.reload != nil || s.preview.Loading()) {
		return render.CursorHourglass
	}
	return render.CursorNormal
}

func (s *nlScreen) stepPointer(now time.Time) {
	if s.pointer == nil {
		return
	}
	s.pointer.SetIndex(s.pointerShape())
	if s.pointerClock.IsZero() {
		s.pointerClock = now
	}
	ticks := int64(now.Sub(s.pointerClock)) * 30 / int64(time.Second)
	if ticks > s.pointerTick {
		s.pointer.Step(int(ticks - s.pointerTick))
		s.pointerTick = ticks
	}
}

// nlPointerRect scales the authored hotspot along with the frame. The pointer
// sample and destination are device pixels [03 R-FX-01 §5][fmt gaf].
func nlPointerRect(f *formats.GAFFrame, x, y int, scale float64) screenkit.Rect {
	return screenkit.Rect{X: float64(x) - float64(f.XOffset)*scale, Y: float64(y) - float64(f.YOffset)*scale, W: float64(f.Width) * scale, H: float64(f.Height) * scale}
}

func (s *nlScreen) restorePointer(screen *ebiten.Image) {
	if s.pointerUnder == nil || s.pointerRect.Empty() {
		return
	}
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(s.pointerRect.Min.X), float64(s.pointerRect.Min.Y))
	screen.DrawImage(s.pointerUnder, op)
	s.pointerRect = image.Rectangle{}
}

// drawPointer is deferred until every overlay has drawn. A small saved region
// also lets it keep moving while the closing gesture holds the last frame.
func (s *nlScreen) drawPointer(screen *ebiten.Image, now time.Time) {
	s.stepPointer(now)
	if !s.OwnsPointer() {
		return
	}
	f := s.pointer.Frame()
	img := s.pointerImages[f]
	if img == nil {
		rgba := nlGAFImage(f, s.pointerPal)
		if rgba == nil {
			return
		}
		img = ebiten.NewImageFromImage(rgba)
		s.pointerImages[f] = img
	}
	x, y := ebiten.CursorPosition()
	k := max(1, math.Round(2*s.u()))
	r := nlPointerRect(f, x, y, k)
	clip := image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H)).Intersect(screen.Bounds())
	if clip.Empty() {
		return
	}
	if s.pointerUnder == nil || s.pointerUnder.Bounds().Dx() != clip.Dx() || s.pointerUnder.Bounds().Dy() != clip.Dy() {
		if s.pointerUnder != nil {
			s.pointerUnder.Deallocate()
		}
		s.pointerUnder = ebiten.NewImage(clip.Dx(), clip.Dy())
	}
	s.pointerUnder.Clear()
	// Reuse the canvas identity; the small destination clips this translated
	// copy without a moving source view (DESIGN_GPU_RENDERER §34).
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(-clip.Min.X), float64(-clip.Min.Y))
	s.pointerUnder.DrawImage(screen, op)
	s.pointerRect = clip
	screenkit.Image(screen, img, r, 1, true)
}

// ---------------------------------------------------------------- hero

func (s *nlScreen) drawHero(screen *ebiten.Image, page nlPage, card *nlCard, idx, v int) {
	u := s.u()
	if s.heroKey != page.key+card.key {
		s.heroKey, s.heroT = page.key+card.key, 0
	}
	ease := 1 - math.Pow(1-s.heroT, 3)
	a := ease
	x := 72*u + (1-ease)*-24*u
	y := 144 * u
	df, bf := s.fonts.Display, s.fonts.Body
	ks := screenkit.Style{Size: 12 * u, Tracking: 0.32, Top: alphaC(nlKicker, a), Upper: true, Shadow: 0.1}
	kx := x + df.Draw(screen, page.title, x, y, ks) + 12*u
	screenkit.Disc(screen, kx, y-6*u, 2.5*u, alphaC(nlGreen, a))
	count := s.ui.text(nlTextKey{kind: "count", i: idx + 1, j: len(page.cards)}, func() string { return fmt.Sprintf("%d / %d", idx+1, len(page.cards)) })
	kx += 16*u + df.Draw(screen, count, kx+16*u, y, ks)
	s.drawSource(screen, card, kx+24*u, y-17*u, a)
	title := s.ui.upperCase(card.label)
	size := 76 * u
	for size > 30*u && df.Measure(title, screenkit.Style{Size: size, Tracking: 0.02}) > 720*u {
		size -= 2 * u
	}
	y += 22*u + size
	df.Draw(screen, title, x, y, screenkit.Style{Size: size, Tracking: 0.02, Top: alphaC(nlGoldTop, a), Bottom: alphaC(nlGoldBottom, a), Shadow: 0.05})
	y += 26 * u
	controlTop := y
	switch card.kind {
	case nlResolution:
		y = s.heroResolution(screen, x, y, a)
	case nlMeter:
		y = s.heroMeter(screen, card, v, x, y, a)
	case nlSwitch:
		y = s.heroSwitch(screen, card, v, x, y, a)
	case nlGroup:
		y = s.heroGroup(screen, card, x, y, a)
	case nlStepper:
		y = s.heroStepper(screen, card, v, x, y, a)
	case nlLayers:
		y = s.heroLayers(screen, card, v, x, y, a)
	case nlHalves:
		y = s.heroHalves(screen, card, v, x, y, a)
	case nlContent:
		y = s.heroContent(screen, card, v, x, y, a)
	case nlRestrictions:
		y = s.heroRestrictions(screen, x, y, a)
	}
	if card.kind != nlGroup && s.cardUnavailable(*card) != "" {
		screenkit.Fill(screen, screenkit.Rect{X: x - 4*u, Y: controlTop - 4*u, W: 648 * u, H: y - controlTop + 8*u}, color.RGBA{8, 12, 8, 145})
	}
	y += 22 * u
	{
		desc := card.desc(&s.draft, v)
		st := screenkit.Style{Size: 13 * u, Top: alphaC(nlBody, a), Shadow: 0.12}
		y += s.drawWrapped(screen, bf, desc, x, y, 620*u, 2.15, st)
	}
	if card.details != nil {
		if lines := card.details(&s.draft, v); len(lines) > 0 {
			y += 4 * u
			heading := "What it changes"
			if v > 0 && card.kind == nlLayers {
				heading = "What it adds to " + card.steps[v-1]
			}
			s.fonts.Display.Draw(screen, heading, x, y+12*u, screenkit.Style{Size: 11 * u, Tracking: 0.24, Top: alphaC(nlKicker, a), Upper: true})
			y += 20 * u
			rows := (len(lines) + 1) / 2
			for i, line := range lines {
				col, row := i/rows, i%rows
				lx, ly := x+float64(col)*320*u, y+float64(row)*19*u
				screenkit.Disc(screen, lx+4*u, ly+8*u, 2.5*u, alphaC(nlGreen, a))
				bf.Draw(screen, line, lx+14*u, ly+12*u, screenkit.Style{Size: 11 * u, Top: alphaC(nlBody, a), Shadow: 0.1})
			}
			y += float64(rows)*19*u + 2*u
		}
	}
	if card.key == "profile" {
		y = s.heroProfileChanges(screen, v, x, y+8*u, a)
	}
	if card.usesMutators {
		y = s.heroMutatorStats(screen, strings.TrimPrefix(card.key, "mut-"), x, y+8*u, a)
	}
	// Notes: locks, Enhanced-only, stand-in.
	y += 6 * u
	if note := s.cardNote(*card, v); note != "" {
		s.padlock(screen, x, y+2*u, 13*u, nlAmber, false)
		bf.Draw(screen, note, x+22*u, y+13*u, screenkit.Style{Size: 11.5 * u, Top: alphaC(nlAmber, a), Shadow: 0.12})
		y += 30 * u
	}
	// Chips and compare.
	cx := x
	if card.kind == nlRestrictions {
		s.heroRestrictionChips(screen, x, y, a)
	}
	for _, c := range card.chips {
		cx += s.chip(screen, c, cx, y, a) + 8*u
	}
	if card.usesMutators && !s.draft.mutators.IsZero() {
		cx += s.buttonAuto(screen, "mut-reset", cx, y-4*u, "Reset all", func() {
			s.draft.mutators = content.Mutators{}
			for _, c := range s.mutatorCards() {
				s.touched[c.key] = true
			}
		}) + 8*u
	}
	if card.kind == nlContent {
		s.button(screen, "get-mods", screenkit.Rect{X: cx, Y: y - 4*u, W: 200 * u, H: 36 * u}, "Get more mods", false, false, s.openCatalog)
	}
	if card.compare != nil {
		if _, ok := card.compare(&s.draft, v); ok {
			label := "Compare"
			if card.blink {
				label = "Blink"
			}
			if s.compare {
				label = "Hide compare"
			}
			s.settingButton(screen, "compare", screenkit.Rect{X: cx + 6*u, Y: y - 4*u, W: 150 * u, H: 36 * u}, label, !s.compareAvailable(*card), func() { s.compare = !s.compare })
		}
	}
	if s.compare && s.compareAvailable(*card) && s.preview.alt != nil && !s.paired {
		bv, _ := card.compare(&s.draft, v)
		xw := clamp(s.wipe, 0.02, 0.98) * s.w()
		tag := func(text string, tx float64, align int) {
			st := screenkit.Style{Size: 12 * u, Tracking: 0.18, Top: nlGreenText, Upper: true, Align: align}
			tw := df.Measure(text, st) + 20*u
			rx := tx
			if align == 2 {
				rx = tx - tw
			}
			screenkit.Fill(screen, screenkit.Rect{X: rx, Y: 108 * u, W: tw, H: 30 * u}, color.RGBA{6, 12, 6, 210})
			screenkit.Outline(screen, screenkit.Rect{X: rx, Y: 108 * u, W: tw, H: 30 * u}, 1*u, color.RGBA{60, 90, 60, 255})
			df.Draw(screen, text, rx+10*u, 128*u, screenkit.Style{Size: 12 * u, Tracking: 0.18, Top: nlGreenText, Upper: true})
		}
		if card.blink {
			// One tag, the value on screen, with a lamp for each phase.
			var off, on string
			if card.kind == nlGroup {
				p := card.parts[s.selectedPart(card)]
				off, on = p.label+" "+p.steps[0], p.label+" "+nlPartValueText(p, &s.draft)
			} else {
				off, on = card.steps[bv], card.steps[v]
			}
			text := on
			if s.blinkShowsAlt() {
				text = off
			}
			cx := s.w() * 0.7
			st := screenkit.Style{Size: 16 * u, Tracking: 0.18, Top: nlCream, Upper: true}
			tw := max(df.Measure(off, st), df.Measure(on, st)) + 64*u
			r := screenkit.Rect{X: cx - tw/2, Y: 104 * u, W: tw, H: 40 * u}
			screenkit.Fill(screen, r, color.RGBA{6, 12, 6, 220})
			screenkit.Outline(screen, r, 1*u, color.RGBA{255, 214, 92, 200})
			df.Draw(screen, text, cx+12*u, r.Y+27*u, screenkit.Style{Size: 16 * u, Tracking: 0.18, Top: nlCream, Upper: true, Align: 1})
			lamp := nlGreen
			if s.blinkShowsAlt() {
				lamp = color.RGBA{70, 70, 60, 255}
			}
			screenkit.Disc(screen, r.X+22*u, r.Y+20*u, 7*u, lamp)
		} else if card.kind == nlGroup {
			p := card.parts[s.selectedPart(card)]
			tag(p.label+" "+p.steps[0], xw-14*u, 2)
			tag(p.label+" "+nlPartValueText(p, &s.draft), xw+14*u, 0)
		} else {
			tag(card.steps[bv], xw-14*u, 2)
			tag(card.steps[v], xw+14*u, 0)
		}
	}
}

func (s *nlScreen) cardNote(card nlCard, v int) string {
	if reason := s.cardUnavailable(card); reason != "" {
		return reason
	}
	if card.kind == nlGroup {
		if reason := s.partUnavailable(card, card.parts[s.selectedPart(&card)]); reason != "" {
			return reason
		}
	}
	switch card.kind {
	case nlLayers:
		if locked, m := s.stageLocked(0); locked && m != nil {
			return fmt.Sprintf("%s locks the rules below %s. Click a locked layer to override.", m.Name, gameplayLabel(nlRuleModes[firstUnlocked(s)]))
		}
		if s.draft.override {
			if m := s.modAt(s.draft.mod); m != nil {
				if _, ok := modMinimumGameplay(m); ok {
					return fmt.Sprintf("Overridden: %s's rule lock is off. Future network games may refuse this.", m.Name)
				}
			}
		}
	case nlContent:
		if m := s.modAt(v); m != nil {
			if min, ok := modMinimumGameplay(m); ok {
				return fmt.Sprintf("Locks the rules to %s or newer.", gameplayLabel(min))
			}
		}
	case nlRestrictions:
		if g := s.shell(); g != nil && g.opts.RestrictionsSet {
			return "The command line set this run's restrictions; an edit applied here replaces them."
		}
	}
	return ""
}

func firstUnlocked(s *nlScreen) int {
	for i := range nlRuleModes {
		if locked, _ := s.stageLocked(i); !locked {
			return i
		}
	}
	return 0
}

func (s *nlScreen) chip(screen *ebiten.Image, text string, x, y, a float64) float64 {
	return s.chipTone(screen, text, x, y, a, color.RGBA{198, 235, 192, 255}, color.RGBA{47, 74, 48, 255}, nlGreen)
}

// chipTone is a chip in the given ink, edge and lamp colours.
func (s *nlScreen) chipTone(screen *ebiten.Image, text string, x, y, a float64, ink, edge, lamp color.RGBA) float64 {
	u := s.u()
	st := screenkit.Style{Size: 10.5 * u, Top: alphaC(ink, a)}
	tw := s.fonts.Body.Measure(text, st) + 24*u
	r := screenkit.Rect{X: x, Y: y, W: tw, H: 30 * u}
	screenkit.Fill(screen, r, color.RGBA{10, 18, 10, uint8(205 * a)})
	screenkit.Outline(screen, r, 1*u, alphaC(edge, a))
	screenkit.Disc(screen, x+10*u, y+15*u, 3*u, alphaC(lamp, a))
	s.fonts.Body.Draw(screen, text, x+17*u, y+20*u, st)
	return tw
}

// heroRestrictions is the Unit restrictions card's control: a summary of
// the draft — the numbers removed and capped and up to four entries with
// their pictures and states, then how many more — with Edit... and Clear in
// its heading row, so the card keeps the height of the others
// (DESIGN_MODS_MUTATORS §15.9).
func (s *nlScreen) heroRestrictions(screen *ebiten.Image, x, y, a float64) float64 {
	u := s.u()
	df, bf := s.fonts.Display, s.fonts.Body
	sum := s.restrictionSummary(s.draft.restrictions)
	rows := (len(sum.rows) + 1) / 2
	h := 86 * u
	if rows > 0 {
		h = 62*u + float64(rows)*44*u
		if sum.more > 0 {
			h += 22 * u
		}
	}
	box := screenkit.Rect{X: x, Y: y, W: 600 * u, H: h}
	s.well(screen, box, a)
	// The actions, right-aligned in the heading row.
	cw := df.Measure("Clear", screenkit.Style{Size: 14 * u, Tracking: 0.12, Upper: true}) + 36*u
	clear := screenkit.Rect{X: box.X + box.W - 16*u - cw, Y: box.Y + 12*u, W: cw, H: 36 * u}
	s.settingButton(screen, "restrict-clear", clear, "Clear", s.draft.restrictions == "", func() {
		s.setRestrictionDraft(content.Restrictions{})
	})
	ew := df.Measure("Edit...", screenkit.Style{Size: 14 * u, Tracking: 0.12, Upper: true}) + 36*u
	s.button(screen, "restrict-edit", screenkit.Rect{X: clear.X - 10*u - ew, Y: clear.Y, W: ew, H: 36 * u}, "Edit...", false, false, s.editRestrictions)
	df.Draw(screen, s.ui.upperCase(sum.text), box.X+24*u, box.Y+40*u, screenkit.Style{Size: 24 * u, Tracking: 0.04, Top: alphaC(nlCream, a), Shadow: 0.06})
	if rows == 0 {
		bf.Draw(screen, "Every unit is open to every player.", box.X+24*u, box.Y+66*u, screenkit.Style{Size: 11.5 * u, Top: alphaC(nlDim, a)})
	}
	for i, r := range sum.rows {
		col, row := i%2, i/2
		rr := screenkit.Rect{X: box.X + 16*u + float64(col)*288*u, Y: box.Y + 58*u + float64(row)*44*u, W: 280 * u, H: 40 * u}
		screenkit.Fill(screen, rr, color.RGBA{8, 12, 8, uint8(170 * a)})
		pic := screenkit.Rect{X: rr.X + 3*u, Y: rr.Y + 3*u, W: 34 * u, H: 34 * u}
		alpha := a
		if r.removed {
			alpha = 0.4 * a
		}
		if img := s.art.pic(r.key); img != nil {
			screenkit.Image(screen, img, pic, alpha, false)
		}
		stateStyle := screenkit.Style{Size: 10.5 * u, Tracking: 0.1, Top: alphaC(nlGreenText, a), Upper: true, Align: 2}
		if r.removed {
			stateStyle.Top = alphaC(color.RGBA{226, 150, 128, 255}, a)
		}
		stateW := df.Measure(r.state, stateStyle)
		df.Draw(screen, r.state, rr.X+rr.W-10*u, rr.Y+25*u, stateStyle)
		// A long name shrinks a little before it is cut.
		st := screenkit.Style{Size: 12 * u, Top: alphaC(nlBody, a), Shadow: 0.1}
		room := rr.W - 46*u - stateW - 14*u
		for st.Size > 9.5*u && bf.Measure(r.name, st) > room {
			st.Size -= 0.5 * u
		}
		name := r.name
		if bf.Measure(name, st) > room {
			runes := []rune(name)
			for len(runes) > 1 && bf.Measure(string(runes)+"…", st) > room {
				runes = runes[:len(runes)-1]
			}
			name = strings.TrimSpace(string(runes)) + "…"
		}
		bf.Draw(screen, name, rr.X+46*u, rr.Y+25*u, st)
	}
	if sum.more > 0 {
		more := s.ui.text(nlTextKey{kind: "restrictions more", i: sum.more}, func() string { return fmt.Sprintf("and %d more", sum.more) })
		bf.Draw(screen, more, box.X+24*u, box.Y+box.H-16*u, screenkit.Style{Size: 11 * u, Top: alphaC(nlDim, a)})
	}
	return box.Y + box.H
}

// heroRestrictionChips names the entries the running content leaves out of
// its battles (DESIGN_MODS_MUTATORS §15.3), in amber, over at most two lines.
func (s *nlScreen) heroRestrictionChips(screen *ebiten.Image, x, y, a float64) {
	u := s.u()
	chips := s.restrictionSummary(s.draft.restrictions).leftOut
	ink, edge := color.RGBA{255, 222, 150, 255}, color.RGBA{120, 96, 40, 255}
	cx, line := x, 0
	for i, text := range chips {
		st := screenkit.Style{Size: 10.5 * u}
		tw := s.fonts.Body.Measure(text, st) + 24*u
		if cx > x && cx+tw > x+640*u {
			cx, line = x, line+1
		}
		rest := len(chips) - i
		if line == 1 && rest > 1 {
			// The last line ends with the count of the rest when they would
			// not all fit.
			total := cx
			for _, t := range chips[i:] {
				total += s.fonts.Body.Measure(t, st) + 32*u
			}
			if total > x+640*u {
				more := fmt.Sprintf("and %d more left out", rest)
				s.chipTone(screen, more, cx, y+float64(line)*38*u, a, ink, edge, nlAmber)
				return
			}
		}
		if line > 1 {
			return
		}
		cx += s.chipTone(screen, text, cx, y+float64(line)*38*u, a, ink, edge, nlAmber) + 8*u
	}
}

// heroProfileChanges lists what applying the chosen profile would change:
// each row the profile assigns that differs from its live value, old then
// new, in two columns.
func (s *nlScreen) heroProfileChanges(screen *ebiten.Image, v int, x, y, a float64) float64 {
	preset := nlControlsPresets[v].preset
	g := s.shell()
	if preset == "" || g == nil {
		return y
	}
	u := s.u()
	type change struct{ label, from, to string }
	var changes []change
	for _, row := range controlsPresetRows {
		next := row.presetValue(preset)
		if next == presetUnchanged {
			continue
		}
		if cur := row.get(g); cur != next {
			changes = append(changes, change{row.label, row.valueText(cur), row.valueText(next)})
		}
	}
	bf := s.fonts.Body
	if len(changes) == 0 {
		bf.Draw(screen, "Every row already matches this profile.", x, y+14*u, screenkit.Style{Size: 12 * u, Top: alphaC(nlGreenText, a), Shadow: 0.1})
		return y + 26*u
	}
	heading := fmt.Sprintf("Apply changes %d settings", len(changes))
	if len(changes) == 1 {
		heading = "Apply changes 1 setting"
	}
	s.fonts.Display.Draw(screen, heading, x, y+12*u, screenkit.Style{Size: 11 * u, Tracking: 0.24, Top: alphaC(nlKicker, a), Upper: true})
	y += 24 * u
	const cols, maxRows = 2, 7
	rows := min((len(changes)+cols-1)/cols, maxRows)
	colW := 372 * u
	for i, c := range changes {
		col, row := i/rows, i%rows
		if col >= cols {
			bf.Draw(screen, fmt.Sprintf("and %d more", len(changes)-i), x, y+float64(rows)*21*u+12*u, screenkit.Style{Size: 11 * u, Top: alphaC(nlDim, a)})
			rows++
			break
		}
		cx, cy := x+float64(col)*colW, y+float64(row)*21*u
		st := screenkit.Style{Size: 11 * u, Top: alphaC(nlBody, a), Shadow: 0.1}
		bf.Draw(screen, c.label, cx, cy+12*u, st)
		vx := cx + 196*u
		st.Top = alphaC(nlDim, a)
		vx += bf.Draw(screen, c.from, vx, cy+12*u, st) + 8*u
		s.chevron(screen, vx+2*u, cy+8*u, 1, 0, 3*u, alphaC(nlGreen, a))
		st.Top = alphaC(nlGreenText, a)
		bf.Draw(screen, c.to, vx+12*u, cy+12*u, st)
	}
	return y + float64(rows)*21*u
}

// lamp is a round TA indicator lamp.
func (s *nlScreen) lamp(screen *ebiten.Image, cx, cy, r float64, c color.RGBA, lit bool) {
	if lit {
		screenkit.Glow(screen, screenkit.Rect{X: cx - r*3, Y: cy - r*3, W: r * 6, H: r * 6}, alphaC(c, 0.5))
		screenkit.Disc(screen, cx, cy, r, lerpRGBA(c, color.RGBA{0, 0, 0, 255}, 0.35))
		screenkit.Disc(screen, cx, cy, r*0.72, c)
		screenkit.Disc(screen, cx-r*0.25, cy-r*0.25, r*0.25, color.RGBA{255, 255, 255, 200})
	} else {
		screenkit.Disc(screen, cx, cy, r, color.RGBA{34, 66, 40, 255})
		screenkit.Disc(screen, cx-r*0.2, cy-r*0.2, r*0.35, color.RGBA{70, 104, 74, 255})
	}
	screenkit.Ring(screen, cx, cy, r, max(1, r*0.14), color.RGBA{0, 0, 0, 255})
}

// segment is one lamp of a meter: a tall rounded LED.
func (s *nlScreen) segment(screen *ebiten.Image, r screenkit.Rect, state int, hover float64) {
	u := s.u()
	screenkit.Fill(screen, r.Inset(-2*u), color.RGBA{10, 10, 8, 255})
	switch state {
	case 1:
		screenkit.Glow(screen, screenkit.Rect{X: r.X - r.W, Y: r.Y - r.H*0.4, W: r.W * 3, H: r.H * 1.8}, color.RGBA{61, 255, 92, 70})
		screenkit.VGradient(screen, screenkit.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H * 0.4}, color.RGBA{212, 255, 219, 255}, color.RGBA{61, 255, 92, 255})
		screenkit.VGradient(screen, screenkit.Rect{X: r.X, Y: r.Y + r.H*0.4, W: r.W, H: r.H * 0.6}, color.RGBA{61, 255, 92, 255}, color.RGBA{17, 165, 42, 255})
	case 2:
		screenkit.Glow(screen, screenkit.Rect{X: r.X - r.W, Y: r.Y - r.H*0.4, W: r.W * 3, H: r.H * 1.8}, color.RGBA{255, 74, 51, 70})
		screenkit.VGradient(screen, screenkit.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H * 0.4}, color.RGBA{255, 214, 207, 255}, nlRed)
		screenkit.VGradient(screen, screenkit.Rect{X: r.X, Y: r.Y + r.H*0.4, W: r.W, H: r.H * 0.6}, nlRed, color.RGBA{168, 28, 15, 255})
	default:
		screenkit.VGradient(screen, r, lerpRGBA(color.RGBA{18, 36, 26, 255}, color.RGBA{40, 70, 45, 255}, hover), color.RGBA{12, 24, 16, 255})
	}
}

// heroSwitch is a two-way throw switch: a metal knob that slides over a
// riveted track, the lit side green for on and a red lamp for off.
func (s *nlScreen) heroSwitch(screen *ebiten.Image, card *nlCard, v int, x, y, a float64) float64 {
	u := s.u()
	r := screenkit.Rect{X: x, Y: y, W: 250 * u, H: 66 * u}
	id := s.ui.id("switch-", card.key, -1, -1)
	pos := s.ease(id, float64(v), 16)
	s.well(screen, r, a)
	in := r.Inset(9 * u)
	half := in.W / 2
	// The knob covers the side not chosen, so the open window reads the
	// setting: a lit green ON on the right, a dark red OFF on the left.
	if pos > 0.01 {
		screenkit.Glow(screen, screenkit.Rect{X: in.X + half - 30*u, Y: in.Y - 24*u, W: half + 60*u, H: in.H + 48*u}, alphaC(color.RGBA{61, 255, 92, 255}, 0.4*pos*a))
		screenkit.VGradient(screen, screenkit.Rect{X: in.X + half, Y: in.Y, W: half, H: in.H}, alphaC(color.RGBA{90, 240, 110, 255}, pos*a), alphaC(color.RGBA{16, 120, 36, 255}, pos*a))
	}
	if pos < 0.99 {
		screenkit.VGradient(screen, screenkit.Rect{X: in.X, Y: in.Y, W: half, H: in.H}, alphaC(color.RGBA{96, 30, 22, 255}, (1-pos)*a), alphaC(color.RGBA{40, 12, 8, 255}, (1-pos)*a))
	}
	df := s.fonts.Display
	ls := screenkit.Style{Size: 19 * u, Tracking: 0.16, Top: alphaC(color.RGBA{255, 170, 150, 255}, a), Align: 1, Shadow: 0.08}
	df.Draw(screen, "OFF", in.X+half/2, in.Y+in.H/2+9*u, ls)
	ls.Top = alphaC(color.RGBA{236, 255, 238, 255}, a)
	df.Draw(screen, "ON", in.X+half+half/2, in.Y+in.H/2+9*u, ls)
	knob := screenkit.Rect{X: in.X + (1-pos)*half, Y: in.Y, W: half, H: in.H}
	screenkit.Shade(screen, screenkit.Rect{X: knob.X - 8*u, Y: knob.Y - 4*u, W: knob.W + 16*u, H: knob.H + 14*u}, 0.6*a)
	screenkit.VGradient(screen, screenkit.Rect{X: knob.X, Y: knob.Y, W: knob.W, H: knob.H * 0.5}, color.RGBA{122, 122, 114, 255}, color.RGBA{78, 78, 70, 255})
	screenkit.VGradient(screen, screenkit.Rect{X: knob.X, Y: knob.Y + knob.H*0.5, W: knob.W, H: knob.H * 0.5}, color.RGBA{78, 78, 70, 255}, color.RGBA{50, 50, 44, 255})
	screenkit.Bevel(screen, knob, 2*u, color.RGBA{214, 214, 206, 255}, color.RGBA{24, 24, 22, 255}, false)
	// Grip ridges and the knob's own lamp.
	for i := -1; i <= 1; i++ {
		gx := knob.X + knob.W/2 + float64(i)*7*u + 16*u
		screenkit.Fill(screen, screenkit.Rect{X: gx, Y: knob.Y + 12*u, W: 2 * u, H: knob.H - 24*u}, color.RGBA{36, 36, 32, 255})
		screenkit.Fill(screen, screenkit.Rect{X: gx + 2*u, Y: knob.Y + 12*u, W: 1 * u, H: knob.H - 24*u}, color.RGBA{150, 150, 142, 255})
	}
	lampC := nlRed
	if v == 1 {
		lampC = nlGreen
	}
	s.lamp(screen, knob.X+22*u, knob.Y+knob.H/2, 8*u, lampC, true)
	s.hits.Add(screenkit.Region{ID: id, Rect: r, Disable: s.cardUnavailable(*card) != "", Click: func() { s.setCard(*card, 1-v) }})
	label := s.ui.upperCase(card.steps[v])
	vc := nlGreenText
	if v == 0 {
		vc = color.RGBA{214, 140, 120, 255}
	}
	df.Draw(screen, label, r.X+r.W+20*u, r.Y+r.H/2+16*u, screenkit.Style{Size: 32 * u, Tracking: 0.04, Top: alphaC(vc, a), Shadow: 0.06})
	return r.Y + r.H
}

func (s *nlScreen) selectedPart(card *nlCard) int {
	return max(0, min(s.partSel[card.key], len(card.parts)-1))
}

// heroGroup lists a grouped card's parts, one row each: its name and what it
// does, and its own switch or strength lamps. Clicking a row picks the part
// Compare shows; the switch or lamps change it.
func (s *nlScreen) heroGroup(screen *ebiten.Image, card *nlCard, x, y, a float64) float64 {
	u := s.u()
	df, bf := s.fonts.Display, s.fonts.Body
	sel := s.selectedPart(card)
	// Leave room for the description, renderer/lock note and Compare above
	// the carousel, including the five-row Glow and Heat cards at 16:9.
	rowH := min(58*u, max(44*u, (float64(s.carouselTop())-y-150*u)/float64(len(card.parts))-6*u))
	for i, p := range card.parts {
		reason := s.partUnavailable(*card, p)
		disabled := reason != ""
		r := screenkit.Rect{X: x, Y: y + float64(i)*(rowH+6*u), W: 640 * u, H: rowH}
		id := s.ui.id("part-", card.key, i, -1)
		v := p.get(&s.draft)
		screenkit.Fill(screen, r, color.RGBA{12, 16, 12, uint8(215 * a)})
		if i == sel {
			screenkit.HGradient(screen, r, color.RGBA{50, 90, 44, uint8(170 * a)}, color.RGBA{14, 20, 14, 0})
			screenkit.Outline(screen, r, 2*u, alphaC(color.RGBA{255, 227, 138, 255}, a))
		} else {
			screenkit.Outline(screen, r, 1*u, alphaC(lerpRGBA(color.RGBA{58, 58, 51, 255}, color.RGBA{150, 150, 130, 255}, s.hits.HoverAmount(id)), a))
		}
		df.Draw(screen, s.ui.upperCase(p.label), r.X+18*u, r.Y+min(25*u, rowH-25*u), screenkit.Style{Size: 16 * u, Tracking: 0.08, Top: alphaC(nlCream, a)})
		sub := p.sub
		if disabled {
			sub = reason
		}
		bf.Draw(screen, sub, r.X+18*u, r.Y+rowH-13*u, screenkit.Style{Size: 11 * u, Top: alphaC(color.RGBA{169, 162, 131, 255}, a)})
		controlWidth := 230 * u
		var choiceWidths []float64
		if p.choices {
			controlWidth = 16 * u
			for _, label := range p.steps {
				w := max(76*u, df.Measure(label, screenkit.Style{Size: 11 * u})+20*u)
				choiceWidths = append(choiceWidths, w)
				controlWidth += w + 4*u
			}
		}
		s.hits.Add(screenkit.Region{ID: id, Rect: screenkit.Rect{X: r.X, Y: r.Y, W: r.W - controlWidth, H: r.H}, Click: func() { s.partSel[card.key] = i }})
		// The control, right-aligned in the row.
		cx := r.X + r.W - 16*u
		if p.choices {
			lx := r.X + r.W - controlWidth
			for k, w := range choiceWidths {
				cr := screenkit.Rect{X: lx, Y: r.Y + 10*u, W: w, H: r.H - 20*u}
				lid := s.ui.id("part-choice-", card.key, i, k)
				screenkit.Fill(screen, cr, color.RGBA{6, 8, 6, 240})
				ink := nlDim
				if k == v {
					screenkit.Fill(screen, cr, color.RGBA{42, 67, 33, uint8(240 * a)})
					screenkit.Outline(screen, cr, 1.5*u, alphaC(nlKicker, a))
					ink = nlCream
				}
				df.Draw(screen, p.steps[k], cr.X+cr.W/2, cr.Y+cr.H/2+4*u, screenkit.Style{Size: 11 * u, Top: alphaC(ink, a), Align: 1})
				s.hits.Add(screenkit.Region{ID: lid, Rect: cr, Disable: disabled, Click: func() { s.setPart(*card, i, k) }})
				lx += w + 4*u
			}
		} else if !p.meter {
			w := 110 * u
			sr := screenkit.Rect{X: cx - w, Y: r.Y + 12*u, W: w, H: r.H - 24*u}
			sid := s.ui.id("part-sw-", card.key, i, -1)
			pos := s.ease(sid, float64(v), 16)
			screenkit.Fill(screen, sr, color.RGBA{6, 8, 6, 240})
			half := sr.W / 2
			if pos > 0.01 {
				screenkit.VGradient(screen, screenkit.Rect{X: sr.X + half, Y: sr.Y, W: half, H: sr.H}, alphaC(color.RGBA{90, 240, 110, 255}, pos*a), alphaC(color.RGBA{16, 120, 36, 255}, pos*a))
			}
			if pos < 0.99 {
				screenkit.VGradient(screen, screenkit.Rect{X: sr.X, Y: sr.Y, W: half, H: sr.H}, alphaC(color.RGBA{96, 30, 22, 255}, (1-pos)*a), alphaC(color.RGBA{40, 12, 8, 255}, (1-pos)*a))
			}
			ls := screenkit.Style{Size: 12 * u, Tracking: 0.14, Top: alphaC(color.RGBA{255, 170, 150, 255}, a), Align: 1}
			df.Draw(screen, "OFF", sr.X+half/2, sr.Y+sr.H/2+5*u, ls)
			ls.Top = alphaC(color.RGBA{236, 255, 238, 255}, a)
			df.Draw(screen, "ON", sr.X+half+half/2, sr.Y+sr.H/2+5*u, ls)
			knob := screenkit.Rect{X: sr.X + (1-pos)*half, Y: sr.Y, W: half, H: sr.H}
			screenkit.VGradient(screen, knob, color.RGBA{122, 122, 114, 255}, color.RGBA{56, 56, 50, 255})
			screenkit.Bevel(screen, knob, 1.5*u, color.RGBA{214, 214, 206, 255}, color.RGBA{24, 24, 22, 255}, false)
			screenkit.Outline(screen, sr, 1*u, color.RGBA{0, 0, 0, 255})
			s.hits.Add(screenkit.Region{ID: sid, Rect: sr, Disable: disabled, Click: func() { s.setPart(*card, i, 1-v) }})
		} else {
			n := len(p.steps)
			sw, gap := 30*u, 6*u
			lx := cx - float64(n)*(sw+gap) + gap
			for k := 0; k < n; k++ {
				lr := screenkit.Rect{X: lx + float64(k)*(sw+gap), Y: r.Y + 14*u, W: sw, H: r.H - 28*u}
				state := 0
				switch {
				case p.steps[0] == "Off" && k == 0 && v == 0:
					state = 2
				case k <= v:
					state = 1
				}
				lid := s.ui.id("part-seg-", card.key, i, k)
				s.segment(screen, lr, state, s.hits.HoverAmount(lid))
				s.hits.Add(screenkit.Region{ID: lid, Rect: lr.Inset(-3 * u), Disable: disabled, Click: func() { s.setPart(*card, i, k) }})
			}
			df.Draw(screen, nlPartValueText(p, &s.draft), lx-12*u, r.Y+r.H/2+6*u, screenkit.Style{Size: 14 * u, Tracking: 0.04, Top: alphaC(nlGreenText, a), Align: 2})
		}
		if disabled {
			screenkit.Fill(screen, screenkit.Rect{X: r.X + r.W - controlWidth, Y: r.Y, W: controlWidth, H: r.H}, color.RGBA{8, 12, 8, 145})
		}
	}
	return y + float64(len(card.parts))*(rowH+6*u)
}

// ease moves a named animation value toward target, frame by frame.
func (s *nlScreen) ease(id string, target, rate float64) float64 {
	cur, ok := s.anim[id]
	if !ok {
		cur = target
	}
	cur += (target - cur) * min(1, s.dt*rate)
	if math.Abs(target-cur) < 0.002 {
		cur = target
	}
	s.anim[id] = cur
	return cur
}

func (s *nlScreen) heroMeter(screen *ebiten.Image, card *nlCard, v int, x, y, a float64) float64 {
	u := s.u()
	sw, sh, gap := 34*u, 54*u, 9*u
	for i := range card.steps {
		r := screenkit.Rect{X: x + float64(i)*(sw+gap), Y: y, W: sw, H: sh}
		state := 0
		if i == 0 && v == 0 {
			state = 2
		} else if i <= v {
			state = 1
		}
		id := s.ui.id("seg-", card.key, i, -1)
		s.segment(screen, r, state, s.hits.HoverAmount(id))
		s.hits.Add(screenkit.Region{ID: id, Rect: r.Inset(-4 * u), Disable: s.cardUnavailable(*card) != "", Click: func() {
			if i == 0 && v == 0 {
				s.setCard(*card, 1)
			} else {
				s.setCard(*card, i)
			}
		}})
	}
	tx := x + float64(len(card.steps))*(sw+gap) + 16*u
	s.fonts.Display.Draw(screen, s.ui.upperCase(card.steps[v]), tx, y+sh/2+16*u, screenkit.Style{Size: 32 * u, Tracking: 0.04, Top: alphaC(nlGreenText, a), Shadow: 0.06})
	return y + sh
}

// arrow is a TA green arrow button.
func (s *nlScreen) arrow(screen *ebiten.Image, id string, r screenkit.Rect, left bool, enabled bool, click func()) {
	hover := s.hits.HoverAmount(id)
	s.buttonPlate(screen, id, r, false, !enabled)
	c := lerpRGBA(color.RGBA{40, 190, 60, 255}, color.RGBA{120, 255, 140, 255}, hover)
	if !enabled {
		c = color.RGBA{40, 70, 45, 255}
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	d := r.W * 0.22
	if left {
		d = -d
	}
	for i := 0.0; i < r.W*0.26; i += 1 {
		t := i / (r.W * 0.26)
		hh := (1 - t) * r.H * 0.26
		xx := cx - d + (d*2)*t
		screenkit.Fill(screen, screenkit.Rect{X: xx, Y: cy - hh, W: 1.2, H: hh * 2}, c)
	}
	if enabled {
		s.hits.Add(screenkit.Region{ID: id, Rect: r, Click: click})
	}
}

func (s *nlScreen) well(screen *ebiten.Image, r screenkit.Rect, alpha float64) {
	u := s.u()
	screenkit.Fill(screen, r, color.RGBA{6, 11, 6, uint8(225 * alpha)})
	if s.art.frame != nil {
		k := math.Max(1, math.Round(u*1.25))
		screenkit.NineSlice(screen, s.art.frame, r, nlFrameEdge, k, false)
	} else {
		screenkit.Bevel(screen, r, 2*u, color.RGBA{35, 35, 31, 255}, color.RGBA{156, 156, 148, 255}, false)
	}
}

func (s *nlScreen) heroStepper(screen *ebiten.Image, card *nlCard, v int, x, y, a float64) float64 {
	u := s.u()
	bh := 66 * u
	enabled := s.cardUnavailable(*card) == ""
	s.arrow(screen, s.ui.id("prev-", card.key, -1, -1), screenkit.Rect{X: x, Y: y + 8*u, W: 50 * u, H: 50 * u}, true, enabled && v > 0, func() { s.setCard(*card, v-1) })
	box := screenkit.Rect{X: x + 62*u, Y: y, W: 330 * u, H: bh}
	s.well(screen, box, a)
	s.fonts.Display.Draw(screen, nlCardValueText(*card, &s.draft), box.X+box.W/2, box.Y+bh/2+14*u, screenkit.Style{Size: 29 * u, Tracking: 0.04, Top: alphaC(nlCream, a), Align: 1})
	s.arrow(screen, s.ui.id("next-", card.key, -1, -1), screenkit.Rect{X: box.X + box.W + 12*u, Y: y + 8*u, W: 50 * u, H: 50 * u}, false, enabled && v < len(card.steps)-1, func() { s.setCard(*card, v+1) })
	// A notch per value under the readout; click one to jump there.
	n := len(card.steps)
	span := box.W - 40*u
	for i := 0; i < n; i++ {
		cx := box.X + 20*u
		if n > 1 {
			cx += span * float64(i) / float64(n-1)
		}
		cy := y + bh + 18*u
		id := s.ui.id("notch-", card.key, i, -1)
		lit := i == v
		s.lamp(screen, cx, cy, 5*u, nlGreen, lit)
		if !lit && s.hits.HoverAmount(id) > 0 {
			screenkit.Ring(screen, cx, cy, 8*u, 1.5*u, alphaC(nlGreen, s.hits.HoverAmount(id)))
		}
		s.hits.Add(screenkit.Region{ID: id, Rect: screenkit.Rect{X: cx - 12*u, Y: cy - 12*u, W: 24 * u, H: 24 * u}, Disable: !enabled, Click: func() { s.setCard(*card, i) }})
	}
	return y + bh + 30*u
}

func (s *nlScreen) padlock(screen *ebiten.Image, x, y, size float64, c color.RGBA, open bool) {
	w := size * 0.9
	body := screenkit.Rect{X: x, Y: y + size*0.42, W: w, H: size * 0.58}
	sx := x + w/2
	sy := y + size*0.42
	if open {
		sx += w * 0.25
		sy -= size * 0.12
	}
	screenkit.Ring(screen, sx, sy, w*0.3, size*0.12, c)
	screenkit.Fill(screen, screenkit.Rect{X: sx - w*0.3 - size*0.06, Y: sy, W: w*0.6 + size*0.12, H: size * 0.3}, color.RGBA{0, 0, 0, 0})
	screenkit.Fill(screen, body, c)
	screenkit.Fill(screen, screenkit.Rect{X: x + w/2 - size*0.06, Y: body.Y + body.H*0.3, W: size * 0.12, H: body.H * 0.4}, color.RGBA{30, 24, 8, 255})
}

func (s *nlScreen) heroLayers(screen *ebiten.Image, card *nlCard, v int, x, y, a float64) float64 {
	u := s.u()
	bw, bh, gap := 600*u, 60*u, 8*u
	df, bf := s.fonts.Display, s.fonts.Body
	for row := 0; row < 3; row++ {
		i := 2 - row
		r := screenkit.Rect{X: x, Y: y + float64(row)*(bh+gap), W: bw, H: bh}
		id := s.ui.id("layer", "", i, -1)
		hover := s.hits.HoverAmount(id)
		included, selected := i <= v, i == v
		locked, _ := s.stageLocked(i)
		screenkit.Fill(screen, r, color.RGBA{14, 16, 12, uint8(215 * a)})
		if included {
			screenkit.HGradient(screen, r, color.RGBA{40, 90, 40, uint8(150 * a)}, color.RGBA{14, 16, 12, 0})
		}
		border := color.RGBA{58, 58, 51, 255}
		if included {
			border = color.RGBA{63, 107, 60, 255}
		}
		if selected {
			border = color.RGBA{255, 227, 138, 255}
			screenkit.Glow(screen, screenkit.Rect{X: r.X - 30*u, Y: r.Y - 20*u, W: r.W + 60*u, H: r.H + 40*u}, color.RGBA{255, 210, 90, uint8(50 * a)})
		}
		border = lerpRGBA(border, color.RGBA{255, 255, 255, 255}, hover*0.2)
		screenkit.Outline(screen, r, 2*u, alphaC(border, a))
		lampC := nlGreen
		if locked {
			lampC = nlRed
		}
		s.lamp(screen, r.X+28*u, r.Y+bh/2, 8*u, lampC, included || locked)
		tc := color.RGBA{143, 136, 109, 255}
		if included {
			tc = color.RGBA{223, 245, 218, 255}
		}
		if selected {
			tc = nlCream
		}
		tw := df.Draw(screen, s.ui.upperCase(card.steps[i]), r.X+52*u, r.Y+bh/2+10*u, screenkit.Style{Size: 20 * u, Tracking: 0.08, Top: alphaC(tc, a)})
		if locked {
			s.padlock(screen, r.X+62*u+tw, r.Y+bh/2-10*u, 18*u, nlAmber, false)
		}
		bf.Draw(screen, card.subs[i], r.X+r.W-20*u, r.Y+bh/2+6*u, screenkit.Style{Size: 11 * u, Top: alphaC(color.RGBA{169, 162, 131, 255}, a), Align: 2})

		s.hits.Add(screenkit.Region{ID: id, Rect: r, Click: func() {
			if locked {
				s.pendingV, s.pendingAction, s.pendingWhat, s.dialog = i, nil, "", "override"
				return
			}
			s.setCard(*card, i)
		}})
	}
	return y + 3*bh + 2*gap
}

func (s *nlScreen) heroHalves(screen *ebiten.Image, card *nlCard, v int, x, y, a float64) float64 {
	u := s.u()
	n := len(card.steps)
	total := 600 * u
	gap := 10 * u
	w := (total - float64(n-1)*gap) / float64(n)
	h := 112 * u
	df, bf := s.fonts.Display, s.fonts.Body
	for i := 0; i < n; i++ {
		r := screenkit.Rect{X: x + float64(i)*(w+gap), Y: y, W: w, H: h}
		id := s.ui.id("half-", card.key, i, -1)
		hover := s.hits.HoverAmount(id)
		on := i == v
		screenkit.Fill(screen, r, color.RGBA{14, 16, 12, uint8(215 * a)})
		if on {
			screenkit.Glow(screen, screenkit.Rect{X: r.X - 30*u, Y: r.Y - 20*u, W: r.W + 60*u, H: r.H + 40*u}, color.RGBA{255, 210, 90, uint8(45 * a)})
			screenkit.VGradient(screen, r, color.RGBA{40, 90, 40, uint8(140 * a)}, color.RGBA{14, 16, 12, 0})
		}
		border := color.RGBA{58, 58, 51, 255}
		if on {
			border = color.RGBA{255, 227, 138, 255}
		}
		screenkit.Outline(screen, r, 2*u, alphaC(lerpRGBA(border, color.RGBA{255, 255, 255, 255}, hover*0.2), a))
		s.lamp(screen, r.X+r.W/2, r.Y+22*u, 8*u, nlGreen, on)
		tc := color.RGBA{143, 136, 109, 255}
		if on {
			tc = nlCream
		}
		size := 22 * u
		label := s.ui.upperCase(card.steps[i])
		for size > 12*u && df.Measure(label, screenkit.Style{Size: size, Tracking: 0.06}) > r.W-20*u {
			size -= u
		}
		df.Draw(screen, label, r.X+r.W/2, r.Y+64*u, screenkit.Style{Size: size, Tracking: 0.06, Top: alphaC(tc, a), Align: 1})
		if len(card.subs) > i {
			bf.Draw(screen, card.subs[i], r.X+r.W/2, r.Y+92*u, screenkit.Style{Size: 10 * u, Top: alphaC(color.RGBA{169, 162, 131, 255}, a), Align: 1})
		}
		s.hits.Add(screenkit.Region{ID: id, Rect: r, Disable: s.cardUnavailable(*card) != "", Click: func() { s.setCard(*card, i) }})
	}
	return y + h
}

// heroContent is the installed content as a list: one row each, the chosen
// one lit, with its version, its rule lock and whether it brings its own
// controls. More rows than fit scroll with the wheel or the arrows beside
// the list.
func (s *nlScreen) heroContent(screen *ebiten.Image, card *nlCard, v int, x, y, a float64) float64 {
	u := s.u()
	rowH := 46 * u
	n := len(card.steps)
	shown := min(n, nlContentVisible)
	list := screenkit.Rect{X: x, Y: y, W: 560 * u, H: float64(shown)*rowH + 12*u}
	s.well(screen, list, a)
	// Scrolling can leave the selected row offscreen. Only a selection
	// change reveals it again (DESIGN_INTERFACE_HUD_INPUT §3.17).
	s.scrollContent(s.contentTop)
	df, bf := s.fonts.Display, s.fonts.Body
	for row := 0; row < shown; row++ {
		i := s.contentTop + row
		r := screenkit.Rect{X: list.X + 8*u, Y: list.Y + 6*u + float64(row)*rowH, W: list.W - 16*u, H: rowH - 4*u}
		id := s.ui.id("content", "", i, -1)
		hover := s.hits.HoverAmount(id)
		on := i == v
		if on {
			screenkit.HGradient(screen, r, color.RGBA{40, 100, 44, uint8(210 * a)}, color.RGBA{16, 30, 16, uint8(120 * a)})
			screenkit.Outline(screen, r, 1.5*u, alphaC(color.RGBA{255, 227, 138, 255}, a))
		} else if hover > 0 {
			screenkit.Fill(screen, r, color.RGBA{60, 70, 50, uint8(90 * hover * a)})
		}
		s.lamp(screen, r.X+18*u, r.Y+r.H/2, 6*u, nlGreen, on)
		m := s.modAt(i)
		name, version := "Total Annihilation", "The original game"
		if m != nil {
			name, version = m.Name, m.Version
		}
		tc := color.RGBA{200, 192, 160, 255}
		if on {
			tc = nlCream
		}
		tw := df.Draw(screen, name, r.X+36*u, r.Y+r.H/2+7*u, screenkit.Style{Size: 18 * u, Tracking: 0.03, Top: alphaC(tc, a)})
		bf.Draw(screen, version, r.X+44*u+tw, r.Y+r.H/2+6*u, screenkit.Style{Size: 11 * u, Top: alphaC(nlDim, a)})
		// Badges at the right: the rule lock, then recommended controls.
		bx := r.X + r.W - 10*u
		badge := func(text string, c color.RGBA) {
			st := screenkit.Style{Size: 10 * u, Tracking: 0.1, Top: alphaC(c, a), Upper: true}
			w := df.Measure(text, st) + 14*u
			bx -= w
			br := screenkit.Rect{X: bx, Y: r.Y + r.H/2 - 10*u, W: w, H: 20 * u}
			screenkit.Outline(screen, br, 1*u, alphaC(c, 0.6*a))
			df.Draw(screen, text, br.X+7*u, br.Y+14*u, st)
			bx -= 6 * u
		}
		if m != nil {
			if m.Controls != "" {
				badge("Own controls", nlGreenText)
			}
			if minimum, ok := modMinimumGameplay(m); ok {
				badge(gameplayLabel(minimum)+"+", nlAmber)
			}
		}
		s.hits.Add(screenkit.Region{ID: id, Rect: screenkit.Rect{X: r.X, Y: r.Y, W: r.W - 40*u, H: r.H}, Disable: s.cardUnavailable(*card) != "", Click: func() { s.setCard(*card, i) }})
		// Badges can move the remove cap inside the row's selection region.
		// Register it last so the visible cap receives the click
		// (DESIGN_MODS_MUTATORS §8.2).
		if m != nil && !sameMod(m, s.shell().cs.mod) {
			rid := s.ui.id("content-remove", "", i, -1)
			xr := screenkit.Rect{X: bx - 30*u, Y: r.Y + r.H/2 - 12*u, W: 24 * u, H: 24 * u}
			c := lerpRGBA(color.RGBA{120, 110, 90, 255}, nlRed, s.hits.HoverAmount(rid))
			screenkit.Line(screen, xr.X+6*u, xr.Y+6*u, xr.X+xr.W-6*u, xr.Y+xr.H-6*u, 2*u, c)
			screenkit.Line(screen, xr.X+xr.W-6*u, xr.Y+6*u, xr.X+6*u, xr.Y+xr.H-6*u, 2*u, c)
			mod := *m
			s.hits.Add(screenkit.Region{ID: rid, Rect: xr, Click: func() { s.removing, s.dialog = mod, "remove" }})
		}
	}
	// Scroll arrows when the list is longer than its window.
	if n > shown {
		ax := list.X + list.W + 10*u
		s.arrowV(screen, "content-up", screenkit.Rect{X: ax, Y: list.Y, W: 40 * u, H: 40 * u}, true, s.contentTop > 0, func() { s.scrollContent(s.contentTop - 1) })
		s.arrowV(screen, "content-down", screenkit.Rect{X: ax, Y: list.Y + list.H - 40*u, W: 40 * u, H: 40 * u}, false, s.contentTop+shown < n, func() { s.scrollContent(s.contentTop + 1) })
		track := screenkit.Rect{X: ax + 17*u, Y: list.Y + 46*u, W: 6 * u, H: list.H - 92*u}
		screenkit.Fill(screen, track, color.RGBA{20, 24, 20, 200})
		th := track.H * float64(shown) / float64(n)
		ty := track.Y + (track.H-th)*float64(s.contentTop)/float64(n-shown)
		screenkit.Fill(screen, screenkit.Rect{X: track.X, Y: ty, W: track.W, H: th}, color.RGBA{120, 200, 120, 220})
		// The hit target spans the arrow column. A track press centres the
		// thumb; grabbing the thumb preserves the pointer's offset on redraw.
		s.hits.Add(screenkit.Region{ID: "content-track", Rect: screenkit.Rect{X: ax, Y: track.Y, W: 40 * u, H: track.H}, Drag: func(_, py float64) {
			if !s.contentDragging {
				s.contentDragging = true
				s.contentGrab = th / 2
				if py >= ty && py < ty+th {
					s.contentGrab = py - ty
				}
			}
			frac := clamp((py-track.Y-s.contentGrab)/(track.H-th), 0, 1)
			s.scrollContent(int(math.Round(frac * float64(n-shown))))
		}})
	}
	s.contentList = list
	if n > shown {
		s.contentList.W += 50 * u
	}
	return list.Y + list.H
}

// arrowV is the arrow button pointing up or down.
func (s *nlScreen) arrowV(screen *ebiten.Image, id string, r screenkit.Rect, up, enabled bool, click func()) {
	s.buttonPlate(screen, id, r, false, !enabled)
	c := lerpRGBA(color.RGBA{40, 190, 60, 255}, color.RGBA{120, 255, 140, 255}, s.hits.HoverAmount(id))
	if !enabled {
		c = color.RGBA{40, 70, 45, 255}
	}
	dy := -1.0
	if !up {
		dy = 1
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	screenkit.Poly(screen, []float64{cx, cy + dy*r.H*0.22, cx - r.W*0.24, cy - dy*r.H*0.16, cx + r.W*0.24, cy - dy*r.H*0.16}, c)
	if enabled {
		s.hits.Add(screenkit.Region{ID: id, Rect: r, Click: click})
	}
}

// ---------------------------------------------------------------- carousel

func (s *nlScreen) drawCarousel(screen *ebiten.Image, page nlPage, focus int, dt float64) {
	u := s.u()
	cw, ch, gap := 226*u, 196*u, 14*u
	top := s.h() - 232*u
	left := 56 * u
	right := s.w() - 56*u
	total := float64(len(page.cards))*(cw+gap) - gap
	// Keep the focused card in view.
	fx := float64(focus) * (cw + gap)
	visible := right - left
	goal := s.scroll
	if fx-goal < 0 {
		goal = fx
	}
	if fx+cw-goal > visible {
		goal = fx + cw - visible
	}
	goal = clamp(goal, 0, math.Max(0, total-visible))
	s.scroll += (goal - s.scroll) * min(1, dt*10)
	s.scroll = clamp(s.scroll, 0, math.Max(0, total-visible))
	for i := range page.cards {
		c := &page.cards[i]
		// Cards arrive with a short stagger when a page opens.
		appear := clamp(s.pageT*1.6-float64(i)*0.08, 0, 1)
		appear = 1 - math.Pow(1-appear, 3)
		x := left + float64(i)*(cw+gap) - s.scroll
		if x+cw < 0 || x > s.w() {
			continue
		}
		id := s.ui.id("card-", c.key, -1, -1)
		target := 0.0
		if i == focus {
			target = 14 * u
		} else if s.hits.Hot() == id {
			target = 6 * u
		}
		s.lift[id] += (target - s.lift[id]) * min(1, dt*12)
		y := top - s.lift[id] + (1-appear)*40*u
		r := screenkit.Rect{X: x, Y: y, W: cw, H: ch}
		cv := c.get(&s.draft)
		alpha := appear
		if s.cardUnavailable(*c) != "" {
			// Unavailable cards remain inspectable, with their choices dimmed.
			alpha = appear * 0.45
		}
		s.drawCard(screen, c, r, cv, i == focus, alpha, !nlCardEqual(*c, &s.saved, &s.draft))
		s.hits.Add(screenkit.Region{ID: id, Rect: r, Click: func() { s.focusCard(i) }, Right: func() {
			s.focusCard(i)
		}})
	}
	if total > visible {
		if s.scroll > 1 {
			s.arrow(screen, "car-left", screenkit.Rect{X: 8 * u, Y: top + ch/2 - 22*u, W: 40 * u, H: 44 * u}, true, true, func() { s.focusCard(max(0, focus-1)) })
		}
		if s.scroll < total-visible-1 {
			s.arrow(screen, "car-right", screenkit.Rect{X: s.w() - 48*u, Y: top + ch/2 - 22*u, W: 40 * u, H: 44 * u}, false, true, func() {
				s.focusCard(min(len(page.cards)-1, focus+1))
			})
		}
	}
}

func (s *nlScreen) drawCard(screen *ebiten.Image, c *nlCard, r screenkit.Rect, v int, focused bool, a float64, changed bool) {
	u := s.u()
	if focused {
		screenkit.Glow(screen, screenkit.Rect{X: r.X - 40*u, Y: r.Y - 30*u, W: r.W + 80*u, H: r.H + 60*u}, color.RGBA{255, 210, 90, uint8(70 * a)})
	}
	screenkit.Shade(screen, screenkit.Rect{X: r.X - 20*u, Y: r.Y - 10*u, W: r.W + 40*u, H: r.H + 40*u}, 0.5*a)
	screenkit.Fill(screen, r, color.RGBA{8, 12, 8, uint8(225 * a)})
	img := screenkit.Rect{X: r.X + 10*u, Y: r.Y + 10*u, W: r.W - 20*u, H: 112 * u}
	if pic := s.art.pic(s.previewCardPics(c)...); pic != nil {
		b := pic.Bounds()
		scale := math.Max(img.W/float64(b.Dx()), img.H/float64(b.Dy()))
		cw, ch := img.W/scale, img.H/scale
		ox, oy := (float64(b.Dx())-cw)/2, (float64(b.Dy())-ch)/2
		sub := pic.SubImage(image.Rect(int(ox), int(oy), int(ox+cw), int(oy+ch))).(*ebiten.Image)
		screenkit.Image(screen, sub, img, a, false)
	} else {
		screenkit.VGradient(screen, img, color.RGBA{30, 40, 30, uint8(255 * a)}, color.RGBA{10, 14, 10, uint8(255 * a)})
	}
	screenkit.VGradient(screen, screenkit.Rect{X: img.X, Y: img.Y + img.H*0.55, W: img.W, H: img.H * 0.45}, color.RGBA{0, 0, 0, 0}, color.RGBA{0, 0, 0, uint8(150 * a)})
	if s.art.frame != nil {
		k := math.Max(1, math.Round(u))
		screenkit.NineSlice(screen, s.art.frame, r, nlFrameEdge, k, false)
	}
	if focused {
		screenkit.Outline(screen, r.Inset(-2*u), 2*u, alphaC(color.RGBA{255, 227, 138, 255}, a))
	}
	df := s.fonts.Display
	name := s.ui.upperCase(c.label)
	size := 16 * u
	for size > 9*u && df.Measure(name, screenkit.Style{Size: size, Tracking: 0.08}) > r.W-28*u {
		size -= 0.5 * u
	}
	df.Draw(screen, name, r.X+14*u, r.Y+152*u, screenkit.Style{Size: size, Tracking: 0.08, Top: alphaC(nlCream, a), Shadow: 0.1})
	// Mini lamps on the value row: the meter's segments, the layers up to
	// the chosen one, or one lamp per option.
	n := len(c.steps)
	if c.kind == nlGroup {
		// One lamp per part: lit when the part is on or above its lowest.
		lw, lh, lg := 9*u, 13*u, 3*u
		lx := r.X + r.W - 14*u - float64(len(c.parts))*(lw+lg) + lg
		on := 0
		for i, p := range c.parts {
			pv := p.get(&s.draft)
			lit := pv > 0 || p.meter && p.steps[0] != "Off"
			if c.key == "sidebar" {
				lit = i == 0 || pv == 0
			}
			if lit {
				on++
			}
			state := 2
			if lit {
				state = 1
			}
			s.segment(screen, screenkit.Rect{X: lx + float64(i)*(lw+lg), Y: r.Y + 165*u, W: lw, H: lh}, state, 0)
		}
		parts := s.ui.text(nlTextKey{kind: "parts on", i: on, j: len(c.parts)}, func() string { return fmt.Sprintf("%d of %d on", on, len(c.parts)) })
		if c.key == "sidebar" {
			orders := "auto"
			if s.draft.pres.SidebarOrders == 0 {
				orders = "never"
			}
			parts = c.parts[0].steps[c.parts[0].get(&s.draft)] + " · orders " + orders
		}
		s.fonts.Body.Draw(screen, parts, r.X+14*u, r.Y+177*u, screenkit.Style{Size: 10.5 * u, Top: alphaC(nlGreenText, a)})
		s.drawCardSource(screen, c, r, a)
		if changed {
			s.lamp(screen, r.X+r.W-16*u, r.Y+22*u, 5*u, nlAmber, true)
		}
		return
	}
	if c.kind != nlContent && n <= 8 {
		lw, lh, lg := 7*u, 13*u, 3*u
		if c.kind == nlSwitch {
			lw = 22 * u
		}
		lx := r.X + r.W - 14*u - float64(n)*(lw+lg) + lg
		if c.kind == nlSwitch {
			lx = r.X + r.W - 14*u - lw
		}
		for i := 0; i < n; i++ {
			if c.kind == nlSwitch && i != 0 {
				break
			}
			lr := screenkit.Rect{X: lx + float64(i)*(lw+lg), Y: r.Y + 165*u, W: lw, H: lh}
			state := 0
			switch {
			case c.kind == nlSwitch:
				state = 2 - v
			case c.kind == nlMeter && i == 0 && v == 0:
				state = 2
			case (c.kind == nlMeter || c.kind == nlLayers) && i <= v:
				state = 1
			case c.kind != nlMeter && i == v:
				state = 1
			}
			s.segment(screen, lr, state, 0)
		}
	}
	value := nlCardValueText(*c, &s.draft)
	if c.kind == nlResolution {
		value = resolutionLabel(s.draft.resolution)
	}
	s.fonts.Body.Draw(screen, value, r.X+14*u, r.Y+177*u, screenkit.Style{Size: 10.5 * u, Top: alphaC(nlGreenText, a)})
	s.drawCardSource(screen, c, r, a)
	if changed {
		s.lamp(screen, r.X+r.W-16*u, r.Y+22*u, 5*u, nlAmber, true)
	}
}

// ---------------------------------------------------------------- dialog

func (s *nlScreen) drawDialog(screen *ebiten.Image) {
	u := s.u()
	screenkit.Fill(screen, screenkit.Rect{W: s.w(), H: s.h()}, color.RGBA{0, 0, 0, 150})
	w, h := 620*u, 250*u
	r := screenkit.Rect{X: s.w()/2 - w/2, Y: s.h()/2 - h/2, W: w, H: h}
	s.hits.Add(screenkit.Region{ID: "dialog-block", Rect: screenkit.Rect{W: s.w(), H: s.h()}})
	s.well(screen, r, 1)
	m := s.modAt(s.draft.mod)
	name := "This content"
	if m != nil {
		name = m.Name + " " + m.Version
	}
	s.padlock(screen, r.X+32*u, r.Y+34*u, 26*u, nlAmber, true)
	s.fonts.Display.Draw(screen, "Override "+name+"?", r.X+72*u, r.Y+58*u, screenkit.Style{Size: 24 * u, Tracking: 0.04, Top: nlAmber, Upper: true})
	minimum := gameplay.Community39
	if mm, ok := modMinimumGameplay(m); ok {
		minimum = mm
	}
	body := fmt.Sprintf("%s is built for %s rules or newer. %s may change how its units play, and future network games may refuse the change.",
		name, gameplayLabel(minimum), gameplayLabel(nlRuleModes[s.pendingV]))
	if s.pendingAction != nil {
		body = fmt.Sprintf("%s sets %s and asks that it stay as it is. Changing it may change how the mod plays, and future network games may refuse the change. Overriding unlocks all of %s's settings.",
			name, strings.ToLower(s.pendingWhat), name)
	}
	s.drawWrapped(screen, s.fonts.Body, body, r.X+32*u, r.Y+84*u, w-64*u, 2.1, screenkit.Style{Size: 12.5 * u, Top: nlBody})
	bw := 170 * u
	s.button(screen, "dlg-override", screenkit.Rect{X: r.X + w - 32*u - bw, Y: r.Y + h - 70*u, W: bw, H: 44 * u}, "Override", true, false, s.confirmOverride)
	s.button(screen, "dlg-keep", screenkit.Rect{X: r.X + w - 46*u - 2*bw, Y: r.Y + h - 70*u, W: bw, H: 44 * u}, "Keep lock", false, false, func() { s.dialog = "" })
}

// nlReload is a content switch in progress: the request, the content's name
// for the loading plate, and how far it has gone.
type nlReload struct {
	request contentReloadRequest
	name    string
	// The draft, its touched cards and applied presets, written to the new
	// content's settings once it is bound.
	draft     nlDraft
	touched   map[string]bool
	presets   []json.RawMessage
	shown     bool // the loading plate has been drawn
	requested bool // the window loop has the request
}

// updateReload drives a content switch: once the plate is on screen it
// hands the request to the window loop, and once the loop has run it binds
// the new shell — or, when the switch failed and the old shell is still
// running, says why and restores the preview.
func (s *nlScreen) updateReload() {
	r := s.reload
	switch {
	case !r.shown:
	case !r.requested:
		request := r.request
		pendingContentReload = &request
		r.requested = true
	case pendingContentReload == nil:
		s.reload = nil
		g := s.shell()
		if g != s.bound {
			s.bindShell(g)
			if len(r.touched) > 0 || len(r.presets) > 0 || r.draft.controls != 0 {
				s.applyDraft(g, r.draft, r.touched, r.presets)
				s.draft = s.freshDraft(g)
			}
			s.toast, s.toastLeft = "Now playing "+r.name, 2.5
			return
		}
		s.bindShell(g)
		notice := "The switch failed"
		if g.cs != nil && g.cs.modNotice != "" {
			notice = g.cs.modNotice
		}
		s.toast, s.toastLeft = notice, 4
	}
}

// drawReload is the loading plate over the screen during a switch.
func (s *nlScreen) drawReload(screen *ebiten.Image) {
	u := s.u()
	screenkit.Fill(screen, screenkit.Rect{W: s.w(), H: s.h()}, color.RGBA{0, 0, 0, 190})
	w, h := 560*u, 150*u
	r := screenkit.Rect{X: s.w()/2 - w/2, Y: s.h()/2 - h/2, W: w, H: h}
	s.well(screen, r, 1)
	s.fonts.Display.Draw(screen, "Loading "+s.reload.name, r.X+w/2, r.Y+64*u, screenkit.Style{Size: 26 * u, Tracking: 0.04, Top: nlCream, Align: 1})
	s.fonts.Body.Draw(screen, "Mounting its archives and compiling its units", r.X+w/2, r.Y+100*u, screenkit.Style{Size: 12 * u, Top: nlKicker, Align: 1})
	s.reload.shown = true
}
