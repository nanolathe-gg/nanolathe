package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// battleSession is the composition root for the windowed battle view. It owns
// the integrated session (all twelve kernel phases) and the interaction state:
// selection, order latch, build panel and placement.
type battleSession struct {
	sess *session.Session
	cat  *content.Catalog
	cam  *camera.Camera
	hud  *retailBattleHUD

	latch      input.Latch
	dragActive bool
	dragStartX int32
	dragStartY int32
	dragEndX   int32
	dragEndY   int32

	// Build placement: non-empty while an armed product awaits a click.
	buildDef   string
	buildFootX int32
	buildFootZ int32
	buildOK    bool
	buildMX    int32
	buildMY    int32

	msAccum    float64 // renderer delta → scaled-now for Session.Step
	lastScaled int64   // previous scaled-now; delta = sim frames ran

	panelButtons []panelButton

	// Retail HUD data-driven assets [02 §6][07 §6][P0-I14].
	// Anchors are the 30 mandatory side anchors stored verbatim [02 §6] C8; panel
	// is the sliding rail with 15 ms throttle [07 §6] C13. GUI window is the
	// authored BATTLE.GUI panel when available [07 §4]. Remaining full GUI wiring
	// (anchors-driven button placement, SHD lookup, fog composer) is TODO(P1).
	anchors   hud.Anchors
	anchorsOK bool
	panel     *hud.Panel
	guiWin    *gui.Window
	guiOK     bool
}

type panelButton struct {
	Name string
	X, Y int32
	Kind string // "build" | "cancel" | "onoff" | "stockpile"
}

const (
	panelButtonW = 96
	panelButtonH = 20
)

var clPtr *client.Client

// runBattleView launches the windowed battle view over the real session.
func runBattleView(opts Options, cs *contentSet) error {
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		return err
	}
	terrain := sess.World
	pal := loadPalette(cs)
	fnt := loadFNT(cs)

	const winW, winH = 640, 480
	mapW := int32(terrain.CellW * 16)
	mapH := int32(terrain.CellH * 16)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: mapW, MapH: mapH}
	cam.Pan(0, 0)
	centerOnCommander(sess.Units, cam, winW, winH)

	b := &battleSession{sess: sess, cat: cat, cam: cam, latch: input.LatchNormal}
	// Load retail HUD data-driven assets: side anchors [02 §6] C8, panel [07 §6] C13, and authored GUI [07 §4][P0-I14].
	// TODO(P1): full GUI wiring uses gui.Window dispatch, side-anchored button placement, palette/SHD lookup,
	// fog composer and ten-layer draw order [07 §6][GAP T22]. Minimal HUD below preserves authored BuildMenus
	// order and pagination, uses anchors/bars/panel/minimap when available, and issues canonical commands.
	if cat != nil && len(cat.Sides) > 0 {
		// Use local player's side for anchors; fallback to first side [02 §6].
		var side *content.SideDef
		if sess.LocalOwner < uint8(len(cat.Sides)) && cat.Sides[sess.LocalOwner] != nil {
			side = cat.Sides[sess.LocalOwner]
		} else {
			for _, s := range cat.Sides {
				if s != nil {
					side = s
					break
				}
			}
		}
		if side != nil {
			if a, err := hud.AnchorsFromSide(side); err == nil {
				b.anchors = a
				b.anchorsOK = true
			}
		}
	}
	// Panel starts visible when session mode has bit 0x04 [07 §6] C13. Use 0x04 for battle entry.
	b.panel = hud.NewPanel(0x04, winW, winH, nil)
	// Attempt to load authored battle GUI for data-driven proof [07 §4][P0-I14]. Not fatal if missing.
	for _, name := range []string{"gui/BATTLE.GUI", "gui/battle.gui", "gui/BATTLE2.GUI"} {
		if w, gerr := gui.Load(cs.fs, name); gerr == nil && w != nil {
			b.guiWin = w
			b.guiOK = true
			break
		}
	}
	cl, err := client.New(client.Options{
		Buffer:   sess.Snapshot,
		Width:    winW,
		Height:   winH,
		Title:    "Nanolathe — " + opts.Map,
		Headless: false,
		Step: func(delta float64) {
			b.viewerStep(delta, clPtr)
		},
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	clPtr = cl
	cl.SetModelFS(cs.fs)
	cl.SetTerrain(terrain)
	cl.SetCamera(cam)
	if pal != nil {
		cl.SetPalette(pal)
	}
	if fnt != nil {
		cl.SetFNT(fnt)
	}
	// Software cursor [07 §8]. A missing cursor GAF is not fatal: the battle
	// view falls back to the window system's own pointer.
	if cursors, cerr := client.LoadCursors(cs.fs); cerr == nil {
		cl.SetCursors(cursors)
	} else {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", cerr)
	}
	cl.Overlay = func(c *client.Client) { b.drawOverlay(c, fnt) }
	fmt.Fprintln(os.Stderr, "nanolathe: battle view — drag=select right-click=context M=move A=attack P=patrol R=repair E=reclaim C=capture G=guard D=blast B=build X=cancel O=on/off N=stockpile Esc=cancel 1..9=buildpage Shift=queue")
	return client.RunGame(cl)
}

// newBattleSession builds the integrated skirmish session for the window.
func newBattleSession(opts Options, cs *contentSet) (*session.Session, *content.Catalog, error) {
	cfg := session.SkirmishConfig{MapName: opts.Map}
	cfg.ApplyDefaults()
	if cfg.NumPlayers < 2 {
		cfg.NumPlayers = 2
	}
	cfg.Players[0].Controller = 0 // human
	if cfg.NumPlayers > 1 {
		cfg.Players[1].Controller = 1 // computer
	}
	return newBattleSessionWithConfig(opts, cs, cfg)
}

// newBattleSessionWithConfig is the windowed composition path used by the
// skirmish lobby. The menu's per-slot and round settings must reach the same
// session constructor as the headless path [08 "Skirmish configuration"].
func newBattleSessionWithConfig(opts Options, cs *contentSet, cfg session.SkirmishConfig) (*session.Session, *content.Catalog, error) {
	if cfg.MapName == "" {
		cfg.MapName = opts.Map
	}
	cfg.ApplyDefaults()
	sess, err := session.NewSkirmishWithFS(cs.fs, nil, cfg)
	if err != nil {
		return nil, nil, err
	}
	return sess, sess.Catalog, nil
}

// centerOnCommander pans the camera to player 0's commander if present. The
// SIDEDATA commander name ends in "com" ([02 §6] side anchors table).
func centerOnCommander(w *units.World, cam *camera.Camera, winW, winH int32) {
	for _, u := range w.Iter() {
		if u != nil && u.Alive && u.Owner == 0 && u.Def != nil &&
			strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			cam.X = int32(u.X>>16) - winW/2
			cam.Z = int32(u.Z>>16) - winH/2
			cam.Pan(0, 0)
			return
		}
	}
}

// viewerStep runs one rendered frame: input → session ticks → camera pan.
func (b *battleSession) viewerStep(delta float64, cl *client.Client) {
	if b == nil || cl == nil {
		return
	}
	// Panel slide target from Space polarity [07 §6] C14: Space held slides toward -31 unless text-editor focused.
	if b.panel != nil {
		spaceHeld := cl.Input().Kbd.KeyHeld(input.KeySpace)
		b.panel.SetTarget(spaceHeld, false)
		b.panel.Step(uint32(time.Now().UnixMilli() & 0xffffffff))
	}
	b.handleInput(cl.Input(), cl)
	// Authoritative budget lives in Session.Step [01 §4.2][01 §4.3]; the
	// accumulator converts renderer seconds into scaled milliseconds.
	b.msAccum += delta * 1000
	scaled := int64(b.msAccum * 30 / 1000)
	if scaled > 1<<30 {
		scaled = 1 << 30
	}
	b.sess.Step(int32(scaled))
	// Animated model textures tick with the simulation frame count
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if ran := scaled - b.lastScaled; ran > 0 {
		cl.TickTextureAnimators(int(ran))
		cl.Cursors().Step(int(ran))
		b.lastScaled = scaled
	}
	b.updateCursor(cl)
	// Camera pan identical to Gate-1/Gate-2 caps [07 §10].
	if b.cam != nil {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := int32(delta * 1000)
		if rawDelta <= 0 {
			rawDelta = 16
		}
		const scrollSetting = 8
		if kbd.KeyHeld(input.KeyUp) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
		}
		if kbd.KeyHeld(input.KeyDown) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
		}
		if kbd.KeyHeld(input.KeyLeft) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
		}
		if kbd.KeyHeld(input.KeyRight) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
		}
		// WASD edge reserved for camera only when latch is Normal to avoid
		// conflict with A=attack, D=blast, S=stockpile hotkeys [07 §10][P0-I14].
		// When a command latch is armed we keep camera on arrow keys + mouse edge only.
		if b.latch == input.LatchNormal {
			if kbd.KeyHeld(input.KeyW) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
			}
			if kbd.KeyHeld(input.KeyS) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
			}
			if kbd.KeyHeld(input.KeyA) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
			}
			if kbd.KeyHeld(input.KeyD) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
			}
		}
		const edge = 8
		w, h := cl.Size()
		if w > 0 && h > 0 {
			if mouse.X < float32(edge) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
			} else if mouse.X > float32(w-edge) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
			}
			if mouse.Y < float32(edge) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
			} else if mouse.Y > float32(h-edge) {
				b.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
			}
		}
	}
}

// handleInput processes selection, orders, and build placement.
// It converts input into complete canonical commands with target/position and
// queue modifiers (shift-queued) via one picking routine that respects fog,
// unit/feature overlap, and command validity [07 §9][03 §3.2] C8 [P0-I14].
func (b *battleSession) handleInput(in *client.InputState, cl *client.Client) {
	kbd := in.Kbd
	mouse := in.Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)

	// Latch arming via hotkeys — retail latch byte IS dispatcher switch key [GAP T22][07 §9] C11.
	// Preserve authored button order and pagination for build menu [02 "Build-menu catalog keys"].
	if kbd.KeyDown(input.KeyM) {
		b.latch = input.LatchMove
	}
	if kbd.KeyDown(input.KeyA) {
		b.latch = input.LatchAttack
	}
	if kbd.KeyDown(input.KeyP) {
		b.latch = input.LatchPatrol
	}
	if kbd.KeyDown(input.KeyR) {
		b.latch = input.LatchRepair
	}
	if kbd.KeyDown(input.KeyE) {
		b.latch = input.LatchReclaim
	}
	if kbd.KeyDown(input.KeyC) {
		b.latch = input.LatchCapture
	}
	if kbd.KeyDown(input.KeyG) {
		b.latch = input.LatchFollow
	}
	if kbd.KeyDown(input.KeyD) {
		b.latch = input.LatchBlast
	}
	if kbd.KeyDown(input.KeyB) {
		b.armBuildPanel()
	}
	if kbd.KeyDown(input.KeyX) {
		b.cancelSelectedProduction()
	}
	if kbd.KeyDown(input.KeyO) {
		b.toggleOnOffSelected(kbd.HasShift())
	}
	if kbd.KeyDown(input.KeyN) {
		b.stockpileSelected(kbd.HasShift())
	}
	// Build page switching via digits 1..9 — routes to build page when builder selected [07 §9] C10.
	// Use hud.RoutesToPage gate approximation: when builder selected we treat digits as pages.
	for d := 1; d <= 9; d++ {
		var key input.Key
		switch d {
		case 1:
			key = input.Key1
		case 2:
			key = input.Key2
		case 3:
			key = input.Key3
		case 4:
			key = input.Key4
		case 5:
			key = input.Key5
		case 6:
			key = input.Key6
		case 7:
			key = input.Key7
		case 8:
			key = input.Key8
		case 9:
			key = input.Key9
		}
		if kbd.KeyDown(key) {
			if b.selectedBuilder() != nil {
				b.switchBuildPage(d)
			}
		}
	}
	if kbd.KeyDown(input.KeyEscape) {
		b.latch = input.LatchNormal
		b.buildDef = ""
	}

	// Armed build panel captures clicks before selection/drag handling.
	// Build buttons are data-driven from SIDEDATA authored order via cat.BuildMenus [02 §6].
	if len(b.panelButtons) > 0 && mouse.Pressed(input.MouseButtonLeft) && !b.dragActive {
		if my := int32(mouse.Y); my >= 480-panelButtonH-32 {
			if b.panelClick(mx, my) {
				return
			}
		}
	}

	// Build placement mode captures clicks before selection handling.
	if b.buildDef != "" {
		b.updatePlacement(mx, my)
		if mouse.Pressed(input.MouseButtonLeft) && b.buildOK {
			queued := kbd.HasShift()
			b.commitBuild(queued)
			// Keep latch for queued multi-build via shift: if shift held keep buildDef for next placement [P0-I14].
			if !queued {
				b.buildDef = ""
			}
		}
		if mouse.Pressed(input.MouseButtonRight) {
			b.buildDef = ""
		}
		return
	}

	leftHeld := mouse.Held(input.MouseButtonLeft)
	additive := kbd.HasShift()
	if leftHeld && !b.dragActive {
		b.dragActive = true
		b.dragStartX, b.dragStartY = mx, my
		b.dragEndX, b.dragEndY = mx, my
	} else if leftHeld && b.dragActive {
		b.dragEndX, b.dragEndY = mx, my
	} else if !leftHeld && b.dragActive {
		b.dragActive = false
		rect := client.NormalizeRect(b.dragStartX, b.dragStartY, b.dragEndX, b.dragEndY)
		w, h := rect.MaxX-rect.MinX, rect.MaxY-rect.MinY
		if w < 3 && h < 3 {
			// Small click: dispatch via armed latch if any, else no order (selection handled via drag only).
			// Use one picking routine that respects fog, unit/feature overlap, command validity [07 §9][03 §3.2][P0-I14].
			if b.latch != input.LatchNormal {
				code := hud.LatchToCode(b.latch)
				if code != 0 {
					b.orderSelected(code, mx, my, additive)
				}
				// Return latch to Normal after dispatch unless shift-queuing keeps it [07 §9][P0-I14].
				if !additive {
					b.latch = input.LatchNormal
				}
			}
		} else {
			client.ApplyDragSelectionWorld(b.sess.Units, b.cam, rect, additive)
			b.filterSelectionToPlayer(0)
		}
	}
	if mouse.Pressed(input.MouseButtonRight) && b.hasSelection() {
		queued := kbd.HasShift()           // queue modifier Replace/Append [04 §3.3][P0-I03]
		b.orderSelected(1, mx, my, queued) // contextual [04 §3.4]
		b.latch = input.LatchNormal
	}
}

// filterSelectionToPlayer clears selection on foreign units.
func (b *battleSession) filterSelectionToPlayer(owner uint8) {
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Owner != owner {
			u.Flags &^= client.SelectionFlag
		}
	}
}

func (b *battleSession) hasSelection() bool {
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Flags&client.SelectionFlag != 0 {
			return true
		}
	}
	return false
}

// selectedUnits returns all selected player 0 units in stable ascending order [I1][07 §9].
func (b *battleSession) selectedUnits() []*units.Unit {
	var out []*units.Unit
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == 0 && u.Flags&client.SelectionFlag != 0 {
			out = append(out, u)
		}
	}
	return out
}

// selectedBuilder returns the first player builder unit under selection.
func (b *battleSession) selectedBuilder() *units.Unit {
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == 0 && u.Flags&client.SelectionFlag != 0 &&
			u.Def != nil && u.Def.Builder {
			return u
		}
	}
	return nil
}

// switchBuildPage handles digit 1..9 build page switching [07 §9] C10.
// Page number lives in flag bits 23-25 with bit 22 paged indicator [07 §9].
func (b *battleSession) switchBuildPage(digit int) {
	u := b.selectedBuilder()
	if u == nil || b.cat == nil {
		return
	}
	page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]
	if !ok || page == nil || len(page.Buttons) == 0 {
		return
	}
	// Derive page count from authored buttons split into pages of buttonsPerPage [07 §9] C10.
	// Per-page slot count is not closed; use 8 as minimal presentation pagination that preserves order [P0-I14].
	const buttonsPerPage = 8
	count := (len(page.Buttons) + buttonsPerPage - 1) / buttonsPerPage
	if count <= 1 {
		return
	}
	// Digit 1..9 maps to page digit-1 [07 §9] C10.
	target := hud.DigitToPage(digit)
	target = hud.ClampPage(target, count)
	var dirty uint32
	// BuildPage switching validates builder identity and page count [07 §9] C10.
	su := &hud.SelectUnit{Flags: u.Flags, DefID: 1} // DefID nonzero validates [07 §9] C10 placeholder
	if hud.SetBuildPage(su, target, count, &dirty) {
		u.Flags = su.Flags
		b.armBuildPanel()
	}
}

// armBuildPanel resolves the selected builder's CANBUILD page into buttons
// [02 "Build-menu catalog keys"]. Authored page.Buttons order is preserved
// verbatim — do NOT sort alphabetically [P0-I03][02 "Build-menu catalog keys"].
// Pagination is applied via flag bits 23-25 with bit 22 paged [07 §9] C10.
func (b *battleSession) armBuildPanel() {
	b.panelButtons = b.panelButtons[:0]
	u := b.selectedBuilder()
	if u == nil || b.cat == nil {
		return
	}
	page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]
	if !ok || page == nil {
		return
	}
	// Preserve authored order [02 "Build-menu catalog keys"] — Buttons already authored.
	// Pagination: split Buttons into pages of 8, page from Flags bits [07 §9] C10.
	const buttonsPerPage = 8
	count := (len(page.Buttons) + buttonsPerPage - 1) / buttonsPerPage
	if count == 0 {
		count = 1
	}
	curPage := 0
	if hud.IsPaged(u.Flags) {
		curPage = hud.DecodePage(u.Flags)
	}
	curPage = hud.ClampPage(curPage, count)
	// Clamp and re-encode if needed to keep flags coherent.
	// Build panel slice for current page.
	start := curPage * buttonsPerPage
	end := start + buttonsPerPage
	if end > len(page.Buttons) {
		end = len(page.Buttons)
	}
	// Main build buttons for this page [02 "Build-menu catalog keys"] C8 order preserved.
	x := int32(8)
	y := int32(480 - panelButtonH - 28)
	for _, name := range page.Buttons[start:end] {
		if _, found := b.cat.Unit(name); !found {
			continue
		}
		b.panelButtons = append(b.panelButtons, panelButton{Name: name, X: x, Y: y, Kind: "build"})
		x += panelButtonW + 4
		if x > 640-panelButtonW {
			break
		}
	}
	// Pagination affordance: if more than one page, show page indicator as non-clickable
	// but keep authored order; the digit keys and page switching already covered.
	// Append command controls after build buttons for minimal HUD that issues same orders [P0-I14].
	// These are data-driven in the sense they map to canonical orders via orders.NewNodeForOrder;
	// retail assets would provide same via side anchors [02 §6].
	y2 := int32(480 - panelButtonH - 8)
	x2 := int32(8)
	// Command controls are minimal fallback for stockpile/on-off/cancel [P0-I14].
	// They are presented as text buttons; clicking them issues the correct order.
	controls := []struct {
		label string
		kind  string
	}{
		{"Cancel", "cancel"},
		{"On/Off", "onoff"},
		{"Stock+1", "stockpile"},
	}
	for _, c := range controls {
		b.panelButtons = append(b.panelButtons, panelButton{Name: c.label, X: x2, Y: y2, Kind: c.kind})
		x2 += panelButtonW + 4
		if x2 > 640-panelButtonW {
			break
		}
	}
}

// panelClick handles a click against the armed build panel; returns true when
// a button was hit and placement armed or command issued.
func (b *battleSession) panelClick(mx, my int32) bool {
	for _, btn := range b.panelButtons {
		if mx >= btn.X && mx < btn.X+panelButtonW && my >= btn.Y && my < btn.Y+panelButtonH {
			switch btn.Kind {
			case "cancel":
				b.cancelSelectedProduction()
				return true
			case "onoff":
				b.toggleOnOffSelected(false)
				return true
			case "stockpile":
				b.stockpileSelected(false)
				return true
			case "build":
				fallthrough
			default:
				def, found := b.cat.Unit(btn.Name)
				if !found || def == nil {
					return true // consumed; nothing placeable
				}
				b.buildDef = def.CanonicalKey
				b.buildFootX = int32(def.FootprintX)
				b.buildFootZ = int32(def.FootprintZ)
				if b.buildFootX <= 0 {
					b.buildFootX = 1
				}
				if b.buildFootZ <= 0 {
					b.buildFootZ = 1
				}
				b.buildOK = false
				return true
			}
		}
	}
	return false
}

// cancelSelectedProduction cancels the tail-most matching factory/mobile build for selected units [04 §3.3][P1-14].
// It walks each selected factory/builder's primary queue tail-most and decrements or frees via
// construction.CancelTailMost / CancelMobileTailMost. Tombstone bit ensures weapon-target-clear skip [04 §3.3].
func (b *battleSession) cancelSelectedProduction() {
	for _, u := range b.selectedUnits() {
		if u == nil || u.Def == nil {
			continue
		}
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			continue
		}
		prim := q.Primary()
		if len(prim) == 0 {
			continue
		}
		tail := prim[len(prim)-1]
		if tail == nil || tail.BuildDefKey == "" {
			// No build product at tail — try generic tail cancel for any build-like tail
			// Use string match on BuildDefKey; for queue without BuildDefKey but with MobileBuild/BuildingBuild id, use def from Param1?
			continue
		}
		// Prefer mobile cancel when descriptor is MobileBuild, else factory.
		if orders.IsMobileBuild(tail.ID) {
			_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
		} else if orders.IsFactoryBuild(tail.ID) {
			_ = construction.CancelTailMost(u, tail.BuildDefKey)
		} else {
			// Generic fallback: try factory path
			if err := construction.CancelTailMost(u, tail.BuildDefKey); err != nil {
				_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
			}
		}
	}
}

// toggleOnOffSelected issues Activate/Deactivate for OnOffable units [02 "Unit record"].
// OnOffable is data-driven; the command is Activate/Deactivate via orders.NewNodeForOrder [P0-I14].
func (b *battleSession) toggleOnOffSelected(queued bool) {
	tick := uint32(0)
	if b.sess != nil && b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	for _, u := range b.selectedUnits() {
		if u == nil || u.Def == nil || !u.Def.OnOffable {
			continue
		}
		// Simple toggle: check LSB of Flags as active proxy [TODO(question)].
		// Retail's exact activation bit is not fully placed, but Activate/Deactivate are symmetric.
		// Use queued modifier as Append/Shift-queue [04 §3.3].
		var id orders.ID
		if u.Flags&0x1000 != 0 {
			id = orders.Lookup("Deactivate")
		} else {
			id = orders.Lookup("Activate")
		}
		if id == 0 {
			continue
		}
		node := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, queued)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		if queued {
			q.Push(id, node)
		} else {
			q.PurgeUnprotected()
			q.DropLeadingAutoOps()
			q.Push(id, node)
		}
		// Flip proxy bit for next toggle visualization.
		u.Flags ^= 0x1000
	}
}

// stockpileSelected queues one BuildWeapon round for stockpile weapons [06 §11.1].
// Stockpile launch requires BuildWeapon descriptor (rear segment 0x40000) with count.
func (b *battleSession) stockpileSelected(queued bool) {
	tick := uint32(0)
	if b.sess != nil && b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	buildWeaponID := orders.Lookup("BuildWeapon")
	if buildWeaponID == 0 {
		return
	}
	for _, u := range b.selectedUnits() {
		if u == nil || u.Def == nil {
			continue
		}
		hasStockpile := false
		if u.Def.Weapon1Def != nil && u.Def.Weapon1Def.Stockpile {
			hasStockpile = true
		}
		if u.Def.Weapon2Def != nil && u.Def.Weapon2Def.Stockpile {
			hasStockpile = true
		}
		if u.Def.Weapon3Def != nil && u.Def.Weapon3Def.Stockpile {
			hasStockpile = true
		}
		// Fallback via slot state: if any populated slot has stockpile weapon.
		for i := 0; i < units.NumSlots; i++ {
			if s := u.SlotAt(i); s != nil && s.Weapon != nil && s.Weapon.Stockpile {
				hasStockpile = true
				break
			}
		}
		if !hasStockpile {
			continue
		}
		// BuildWeapon is secondary [04 §3.1] 0x40000 — use PushSecondary via queue coalesce.
		// Use queued flag as purge survivor? For secondary, queued semantics differ: head insert but flag preserved [04 §3.3].
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		slotIdx := -1
		for sIdx := 0; sIdx < units.NumSlots; sIdx++ {
			if s := u.SlotAt(sIdx); s != nil && s.Weapon != nil && s.Weapon.Stockpile {
				slotIdx = sIdx
				break
			}
		}
		if slotIdx < 0 {
			continue
		}
		node := orders.NewNodeForOrder(buildWeaponID, 0, 0, 0, 0, tick, u.Handle, queued)
		node.Param1 = uint32(slotIdx) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
		node.Param2 = 1               // count 1 [06 §11.1]
		node.Param3 = 0               // progress 0 start [06 §11.1]
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		// Tail-only coalesce for counted BuildWeapon [04 §3.3]; mirror construction path.
		q.CoalesceTail(buildWeaponID, node)
	}
}

// updatePlacement tracks the ghost under the cursor and validates it against
// the world [04 §6.2][PLAN_08 C17].
func (b *battleSession) updatePlacement(mx, my int32) {
	b.buildMX, b.buildMY = mx, my
	wx, wz := b.cam.ScreenToWorld(mx, my)
	cx, cz := world.WorldToCell(wx), world.WorldToCell(wz)
	halfX, halfZ := b.buildFootX/2, b.buildFootZ/2
	cx -= halfX
	cz -= halfZ
	self := uint16(0)
	if u := b.selectedBuilder(); u != nil {
		self = uint16(u.Handle)
	}
	yard, yerr := world.ParseYardMap(b.yardMapFor(), int(b.buildFootX), int(b.buildFootZ))
	if yerr != nil {
		yard = nil
	}
	b.buildOK = b.sess.World.ValidatePlacement(cx, cz, yard, int(b.buildFootX), int(b.buildFootZ), self) == nil
}

// yardMapFor returns the placed definition's yard text when known.
func (b *battleSession) yardMapFor() string {
	def, found := b.cat.Unit(b.buildDef)
	if !found || def == nil {
		return ""
	}
	return def.YardMap
}

// commitBuild queues a mobile-build order through the ordinary construction
// path [PLAN_08 C12/C23][P0-I05]; the session's construction pump drives the lifecycle.
// Site coordinates are passed at queue time and preserved on the queued node as GoalX/Z [P0-I05]:
// the validated ghost anchor (buildMX/buildMY) carries the selected site via QueueMobileBuild.
// It uses catalog indices (not FNV hash) [P0-I05] and respects queue modifier (shift=queued) [04 §3.3][P0-I14].
// Every producer goes through one canonical payload constructor [P0-I03]: orders.NewMobileBuildNode / QueueMobileBuild.
func (b *battleSession) commitBuild(queued bool) {
	builder := b.selectedBuilder()
	if builder == nil {
		return
	}
	wx, wz := b.cam.ScreenToWorld(b.buildMX, b.buildMY)
	wy := numeric.Fixed(0)
	if b.sess.World != nil {
		wy = b.sess.World.HeightAt(wx, wz)
		if wy == numeric.Fixed(-1) {
			wy = 0
		}
	}
	tick := uint32(0)
	if b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	// Build canonical payload via construction helper that uses catalog index and GoalX/Z site [P0-I05].
	// Queue modifier: shift=queued appends behind active with FlagPurgeSurvivor; else replace [04 §3.3][P0-I14].
	// Accomplish by purging before helper when not queued, and ensuring queued flag on node after.
	if !queued {
		if q := orders.QueueForUnit(builder); q != nil {
			q.PurgeUnprotected()
			q.DropLeadingAutoOps()
		}
	}
	// Mobile build uses distinct handler with site anchor [P0-I05]; factory uses BuildingBuild.
	if err := construction.QueueMobileBuild(builder, b.buildDef, wx, wz, 1, b.cat); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: build %s: %v\n", b.buildDef, err)
		return
	}
	q := orders.QueueForUnit(builder)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	prim := q.Primary()
	tail := prim[len(prim)-1]
	if tail == nil {
		return
	}
	// Ensure canonical fields populated for determinism [04 §3.2][P0-I05][P0-I03].
	if tail.GoalY == 0 {
		tail.GoalY = wy
	}
	if tail.Owner == 0 {
		tail.Owner = builder.Handle
	}
	if tail.CreationTick == 0 {
		tail.CreationTick = tick
	}
	if queued {
		tail.Flags |= orders.FlagPurgeSurvivor // queued builds survive future Replace purge [04 §3.3][P0-I14]
	} else {
		tail.Flags &^= orders.FlagPurgeSurvivor
	}
	// Site is authoritative and catalog-indexed via BuildDefKey+Param1 [P0-I05]; not FNV hash.
	_ = wx
	_ = wy
	_ = wz
}

// drawOverlay renders the build panel and placement ghost after units.
// It exposes retail GUI assets: anchors, build pages, command buttons, panel slide offset,
// resource bars, minimap contacts, messages, queue counts [07 §6][02 §6][P0-I14].
// Full ten-layer composer and SHD fog remain TODO(P1) [PLAN_13].
func (b *battleSession) drawOverlay(c *client.Client, fnt *formats.FNT) {
	const W = 640
	// Panel slide offset [07 §6] C13: moving strip blitted at y+offset when nonzero [P0-I14].
	stripY := 0
	if b.panel != nil && b.panel.ShouldBlitStrip() {
		stripY = int(b.panel.Offset)
	}
	_ = stripY // TODO(P1): apply to strip blit when strip art wired; for now background tracks panel.

	if len(b.panelButtons) > 0 {
		// Background for panel area: two rows (build + controls) [07 §6]. Offset follows panel slide for fidelity [P0-I14].
		baseY := 480 - panelButtonH - 32 + stripY
		if baseY < 0 {
			baseY = 0
		}
		if baseY > 480-panelButtonH*2-8 {
			baseY = 480 - panelButtonH*2 - 8
		}
		c.UIFillRect(0, baseY, W, panelButtonH*2+8, 0)
		for _, btn := range b.panelButtons {
			x, y := int(btn.X), int(btn.Y+int32(stripY))
			// Clamp y into visible after slide.
			if y < 0 {
				y = 0
			}
			// Different shade for control kinds for visual distinction [P0-I14].
			bg := byte(12)
			if btn.Kind == "cancel" {
				bg = 8
			} else if btn.Kind == "onoff" {
				bg = 10
			} else if btn.Kind == "stockpile" {
				bg = 14
			}
			c.UIFillRect(x, y, panelButtonW, panelButtonH, bg)
			c.UIFrameRect(x, y, panelButtonW, panelButtonH, 250)
			c.UIText(fnt, btn.Name, x+4, y+6, 250)
		}
		// Latch indicator and page hint [07 §9][P0-I14].
		latchText := "Latch: " + b.latch.String()
		c.UIText(fnt, latchText, 500, baseY+4, 250)
		if u := b.selectedBuilder(); u != nil && b.cat != nil {
			if page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]; ok && page != nil {
				const bpp = 8
				cnt := (len(page.Buttons) + bpp - 1) / bpp
				if cnt > 1 {
					cur := 0
					if hud.IsPaged(u.Flags) {
						cur = hud.DecodePage(u.Flags)
					}
					cur = hud.ClampPage(cur, cnt)
					pg := fmt.Sprintf("Page %d/%d (1..9)", cur+1, cnt)
					c.UIText(fnt, pg, 500, baseY+20, 250)
				}
			}
		}
	}
	if b.buildDef != "" {
		wx, wz := b.cam.ScreenToWorld(b.buildMX, b.buildMY)
		sx, sy := c.WorldToScreenPx(wx, wz, numeric.Fixed(0))
		wpx := int(b.buildFootX) * 16
		hpx := int(b.buildFootZ) * 16
		col := byte(200)
		if b.buildOK {
			col = 250
		}
		c.UIFrameRect(int(sx)-wpx/2, int(sy)-hpx/2, wpx, hpx, col)
		label := b.buildDef + " — BLOCKED"
		if b.buildOK {
			label = b.buildDef + " — click to place"
		}
		c.UIText(fnt, label, int(sx)-wpx/2, int(sy)+hpx/2+2, col)
	}
	// Resource bars via anchors [02 §6][07 §6][P0-I14]: ENERGYBAR/METALBAR filled left-to-right [01 §8].
	// TODO(P1): full HUD uses all 30 anchors with SHD lookup, fog composer and ten-layer draw [07 §6][GAP T22].
	if b.anchorsOK && b.sess != nil && b.sess.Econ != nil {
		// Local player stocks are float32 metal/energy [05 "Player slot"] I2 allowlist.
		p := b.sess.Econ.Players[0]
		// Fractions against max storage; when storage zero show empty [02 §6].
		maxMetal := p.Capacity[economy.Metal]
		if maxMetal <= 0 {
			maxMetal = 1000
		}
		maxEnergy := p.Capacity[economy.Energy]
		if maxEnergy <= 0 {
			maxEnergy = 1000
		}
		mFrac := hud.ResourceFraction(p.Stock[economy.Metal], maxMetal)
		eFrac := hud.ResourceFraction(p.Stock[economy.Energy], maxEnergy)
		metalAnchor, _ := b.anchors.ByName("METALBAR")
		energyAnchor, _ := b.anchors.ByName("ENERGYBAR")
		mf := hud.MetalBarFill(metalAnchor, mFrac)
		ef := hud.EnergyBarFill(energyAnchor, eFrac)
		// Draw filled portions as thin rects in HUD area; if anchors are degenerate fallback to top bar.
		if !mf.IsEmpty() {
			l, t, r, btm := mf.Ordered()
			c.UIFillRect(int(l), int(t), int(r-l), int(btm-t), 210) // metal tint placeholder
			c.UIFrameRect(int(l), int(t), int(r-l), int(btm-t), 250)
		} else if !metalAnchor.IsEmpty() {
			l, t, r, btm := metalAnchor.Ordered()
			c.UIFrameRect(int(l), int(t), int(r-l), int(btm-t), 120)
		}
		if !ef.IsEmpty() {
			l, t, r, btm := ef.Ordered()
			c.UIFillRect(int(l), int(t), int(r-l), int(btm-t), 220) // energy tint
			c.UIFrameRect(int(l), int(t), int(r-l), int(btm-t), 250)
		} else if !energyAnchor.IsEmpty() {
			l, t, r, btm := energyAnchor.Ordered()
			c.UIFrameRect(int(l), int(t), int(r-l), int(btm-t), 120)
		}
		// Numeric labels at anchor positions when available.
		if r, ok := b.anchors.ByName("METALNUM"); ok && !r.IsEmpty() {
			l, t, _, _ := r.Ordered()
			c.UIText(fnt, fmt.Sprintf("M:%d/%d", int(p.Stock[economy.Metal]), int(maxMetal)), int(l), int(t), 250)
		}
		if r, ok := b.anchors.ByName("ENERGYNUM"); ok && !r.IsEmpty() {
			l, t, _, _ := r.Ordered()
			c.UIText(fnt, fmt.Sprintf("E:%d/%d", int(p.Stock[economy.Energy]), int(maxEnergy)), int(l), int(t), 250)
		}
	} else if b.sess != nil && b.sess.Econ != nil {
		// Fallback top bar when anchors not yet loaded [P0-I14].
		p := b.sess.Econ.Players[0]
		c.UIText(fnt, fmt.Sprintf("M:%d E:%d", int(p.Stock[economy.Metal]), int(p.Stock[economy.Energy])), 4, 14, 250)
	}
	// Minimap contacts [07 §6][03 §3.2]: small overview with unit dots; presentation-only separate from LOS [I6][P0-I14].
	// TODO(P1): full minimap uses fog cache, radar contacts via visibility.Service sensor surfaces and panel anchors.
	{
		const mmX, mmY, mmW, mmH = 540, 360, 90, 90
		c.UIFillRect(mmX, mmY, mmW, mmH, 0)
		c.UIFrameRect(mmX, mmY, mmW, mmH, 250)
		c.UIText(fnt, "MINIMAP", mmX+2, mmY-8, 250)
		if b.sess != nil && b.sess.World != nil && b.sess.Units != nil {
			cw := int(b.sess.World.CellW)
			ch := int(b.sess.World.CellH)
			if cw > 0 && ch > 0 {
				for _, u := range b.sess.Units.Iter() {
					if u == nil || !u.Alive {
						continue
					}
					// Fog: only draw contacts visible to local player [03 §3.2] C8 [P0-I14].
					if b.sess.Vis != nil {
						t := visibility.Target{Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z, Status: u.Flags}
						if !b.sess.Vis.IsVisible(visibility.PlayerID(0), t) && u.Owner != 0 {
							continue
						}
					}
					cx := world.WorldToCell(u.X)
					cz := world.WorldToCell(u.Z)
					px := mmX + int(cx)*mmW/cw
					py := mmY + int(cz)*mmH/ch
					col := byte(100)
					if u.Owner == 0 {
						col = 250
					} else if b.sess.Vis != nil && b.sess.Vis.IsVisible(visibility.PlayerID(0), visibility.Target{Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z}) {
						col = 180
					} else {
						col = 80
					}
					if u.Flags&client.SelectionFlag != 0 {
						col = 255
					}
					c.UIFillRect(px, py, 2, 2, col)
				}
			}
		}
	}
	// Queue counts for selected factory/builder [04 §3.2][P0-I14].
	if sel := b.selectedUnits(); len(sel) > 0 {
		for i, u := range sel {
			if i >= 3 {
				break // show first 3 to avoid clutter
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			n := q.LenPrimary()
			// Stockpile UI: ammo vs queued per [06 §11.1] C29 – Ammo byte vs
			// reload vs stockpile count distinction [06 §11.1] (Retail §11).
			ammo, queued := orders.StockpileCounts(u)
			hasStockpile := false
			for sIdx := 0; sIdx < units.NumSlots; sIdx++ {
				if s := u.SlotAt(sIdx); s != nil && s.Weapon != nil && s.Weapon.Stockpile {
					hasStockpile = true
					break
				}
			}
			if hasStockpile {
				// Show per-slot ammo/queued even when primary queue empty.
				for sIdx := 0; sIdx < units.NumSlots; sIdx++ {
					if s := u.SlotAt(sIdx); s != nil && s.Weapon != nil && s.Weapon.Stockpile {
						c.UIText(fnt, fmt.Sprintf("%s stockpile slot%d: %d ready +%d queued", u.Def.UnitName, sIdx, ammo[sIdx], queued[sIdx]), 4, 26+12*i+12*sIdx, 250)
					}
				}
				continue
			}
			if n == 0 {
				continue
			}
			name := u.Def.UnitName
			if len(name) > 10 {
				name = name[:10]
			}
			label := fmt.Sprintf("%s Q:%d", name, n)
			if tail := q.Primary(); len(tail) > 0 && tail[len(tail)-1] != nil && tail[len(tail)-1].BuildDefKey != "" {
				label += " -> " + tail[len(tail)-1].BuildDefKey
			}
			c.UIText(fnt, label, 4, 26+12*i, 250)
		}
	}
	// Messages / queue counts area [P0-I14]: show last build error or latch help.
	if b.buildDef != "" {
		// already shown placement label above
	} else {
		c.UIText(fnt, "M:move A:attack D:blast P:patrol R:repair E:reclaim C:capture G:guard B:build X:cancel O:on/off N:stockpile Space:panel Shift=queue 1..9:page", 4, 2, 250)
		if b.guiOK {
			c.UIText(fnt, "GUI: BATTLE.GUI loaded ✓", 4, 60, 200)
		}
	}
	// TODO(P1): messages queue, chat, cloak/jammer indicators, SHD fog strip, ten-layer composer, audio cues [07 §6][GAP T22][03 §3.3].
}

// loadPalette loads the retail palette tables, nil on failure.
func loadPalette(cs *contentSet) *palette.Tables {
	if p, err := palette.Load(cs.fs); err == nil {
		return p
	}
	return nil
}

// loadFNT loads the first available UI font, nil on failure.
func loadFNT(cs *contentSet) *formats.FNT {
	for _, name := range []string{"fonts/smlfont.fnt", "fonts/armfont.fnt", "fonts/hatt12.fnt"} {
		if data, err := cs.fs.ReadFileLimit(name, 1<<20); err == nil {
			if parsed, ferr := formats.LoadFNT(data); ferr == nil {
				return parsed
			}
		}
	}
	return nil
}

// pickTarget returns the unit handle under the cursor if any, else ground pos.
// It is the ONE picking routine that respects fog (local-player word), overlap
// (nearest squared distance wins with strict < tie-break so lower slot wins on
// equal), unit/feature overlap (units win when both within radius, else feature
// under cell wins), and command validity via orders.Resolve gate [04 §3.5][07 §9][03 §3.2] C8 [P0-I03][P0-I14].
// Feature picking sets ResolvePos.HasFeature when a feature footprint covers the
// clicked cell and is visible; unit picking is tried first [07 §8][07 §9][P0-I14].
// It delegates unit picking to client.PickUnit so there is one canonical routine [P0-I14].
func (b *battleSession) pickTarget(sx, sy int32) (pool.Handle, *units.Unit, *orders.ResolvePos) {
	wx, wz := b.cam.ScreenToWorld(sx, sy)
	pos := &orders.ResolvePos{X: wx, Z: wz}
	if b.sess == nil || b.sess.Units == nil || b.cam == nil {
		return 0, nil, pos
	}
	if b.sess.World != nil {
		y := b.sess.World.HeightAt(wx, wz)
		if y != -1 {
			pos.Y = y
		}
	}
	// ONE picking routine via client.PickUnit [P0-I14][07 §9][03 §3.2] C8.
	if bh, bu := client.PickUnit(sx, sy, b.cam, b.sess.Units, b.sess.Vis, visibility.PlayerID(0)); bh != 0 && bu != nil {
		return bh, bu, pos
	}
	// Feature picking: when no unit hit, test feature footprint at clicked cell [07 §8][P0-I14].
	// Respect fog via IsVisible for feature extents; preserve unit>feature overlap priority (unit already won).
	if b.sess.World != nil {
		cx := world.WorldToCell(wx)
		cz := world.WorldToCell(wz)
		if featIdx, ok := world.ResolveFeature(b.sess.World.Plot, int(b.sess.World.CellW), int(b.sess.World.CellH), int(cx), int(cz)); ok {
			if def, ok2 := b.sess.World.FeatureDefAt(featIdx); ok2 && def != nil {
				visible := true
				if b.sess.Vis != nil {
					// Use VisiblePoint for feature centre; feature visibility is terrain-independent [03 §3.2].
					// When mode disables current, VisiblePoint reads wordMask at local bit; still gate as fog.
					visible = b.sess.Vis.VisiblePoint(visibility.PlayerID(0), wx, pos.Y, wz)
					// For footprint features, also test extents box [03 §3.2] VisibleExtents.
					if !visible {
						footX := def.FootprintX
						footZ := def.FootprintZ
						if footX <= 0 {
							footX = 1
						}
						if footZ <= 0 {
							footZ = 1
						}
						minX := wx
						minZ := wz
						maxX := wx + numeric.Fixed(int64(footX)*65536)
						maxZ := wz + numeric.Fixed(int64(footZ)*65536)
						bx := visibility.Box{MinX: minX, MinZ: minZ, MaxX: maxX, MaxZ: maxZ, Y: pos.Y}
						visible = b.sess.Vis.VisibleExtents(visibility.PlayerID(0), bx)
					}
				}
				if visible {
					pos.HasFeature = true
					// Reclaimable check is data-driven; wreck test via reclaimable + non-empty successor or naming heuristic.
					// For P0-I14 minimal HUD we mark IsWreck when reclaimable (many wrecks are reclaimable) [05 "Feature reclaim"].
					// Resurrectable when wreck and definition is resurrectable and actor can resurrect; gate handled in orders.Resolve.
					pos.IsWreck = def.Reclaimable // TODO(question) exact wreck vs debris discrimination not closed; reclaimable is close proxy [05]
					// Approximate resurrectable when reclaimable and has featuredead (corpse chain) [06 §12.1].
					pos.FeatureResurrectable = pos.IsWreck && def.Reclaimable && (def.FeatureDead != "" || def.FeatureDeadDef != nil)
					// Also consider metal/energy reclaimable; indestructible not reclaimable but still has feature.
					// For minimal HUD, HasFeature true suffices; order resolver will gate via canReclaim/canResurrect.
				}
			}
		} else if b.sess.Features != nil {
			// Fallback via live instance map for sparse features not in FeatureDefs (synthetic terrain).
			if inst := b.sess.Features.InstanceAt(int(cx), int(cz)); inst != nil && inst.Def != nil {
				visible := true
				if b.sess.Vis != nil {
					visible = b.sess.Vis.VisiblePoint(visibility.PlayerID(0), wx, pos.Y, wz)
				}
				if visible {
					pos.HasFeature = true
					pos.IsWreck = inst.Def.Reclaimable
					pos.FeatureResurrectable = pos.IsWreck && inst.Def.Reclaimable
				}
			}
		}
	}
	return 0, nil, pos
}

// orderSelected resolves code at the clicked world position for every selected
// player unit and pushes the resulting canonical order payload [04 §3.4][04 §3.3][P0-I03][P0-I14].
// It uses the ONE picking routine pickTarget that respects fog, overlap, feature priority,
// and command validity [07 §9][03 §3.2] C8. It constructs a canonical Node via
// orders.NewNodeForOrder that writes GoalX/Y/Z, target handle, queue modifier,
// creation tick and owner, and pushes via q.Push(id, node) — never empty [P0-I03][P0-I14].
// Attack ground where permitted is handled: when latch Attack with no unit hit but ground pos,
// it falls back to a ground attack descriptor (Attack_Chase/NoMove or AttackSpecial) [P1-14][04 §3.4].
func (b *battleSession) orderSelected(code int, sx, sy int32, queued bool) {
	targetHandle, targetUnit, pos := b.pickTarget(sx, sy)
	tick := uint32(0)
	if b.sess != nil && b.sess.Clock != nil {
		tick = uint32(b.sess.Clock.GlobalTick)
	}
	for _, u := range b.sess.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != 0 || u.Flags&client.SelectionFlag == 0 {
			continue
		}
		id := orders.Resolve(code, u, targetUnit, pos)
		// Attack ground fallback: code 3 (ATTACK) with no unit but ground pos should still produce a ground attack
		// where permitted (BLAST always ground, ATTACK unit-or-ground) [P1-14][04 §3.4].
		// Resolve currently requires target !=nil for code 3, so fallback to a ground descriptor when canAttack.
		if id == 0 && code == 3 && targetHandle == 0 && pos != nil && u.Def != nil && u.Def.CanAttack {
			// Prefer AttackSpecial if unit canDGun (special attack ground), else Attack_Chase/NoMove ground.
			// Use AttackSpecial as blast-equivalent ground attack where permitted.
			if u.Def.CanDGun {
				if alt := orders.Lookup("AttackSpecial"); alt != 0 {
					id = alt
				}
			}
			if id == 0 {
				if alt := orders.Lookup("Attack_Chase"); alt != 0 {
					id = alt
				} else if alt2 := orders.Lookup("Attack_NoMove"); alt2 != 0 {
					id = alt2
				} else if alt3 := orders.Lookup("Suppress"); alt3 != 0 {
					id = alt3
				}
			}
			// Ensure pos is used as ground payload for fallback.
			if id != 0 {
				targetHandle = 0
				targetUnit = nil
			}
		}
		// Patrol ground fallback: code 9 with no target should still produce QPatrol/Patrol ground [04 §3.4].
		// Resolve already handles it (returns QPatrol), but ensure fallback if nil.
		if id == 0 && code == 9 && pos != nil && u.Def != nil && u.Def.CanPatrol {
			if alt := orders.Lookup("QPatrol"); alt != 0 {
				id = alt
			} else if alt2 := orders.Lookup("Patrol"); alt2 != 0 {
				id = alt2
			}
			targetHandle = 0
			targetUnit = nil
		}
		if id == 0 {
			continue
		}
		var gx, gy, gz numeric.Fixed
		if targetHandle != 0 && targetUnit != nil {
			gx = targetUnit.X
			gy = targetUnit.Y
			gz = targetUnit.Z
		} else {
			gx = pos.X
			gy = pos.Y
			gz = pos.Z
		}
		node := orders.NewNodeForOrder(id, targetHandle, gx, gy, gz, tick, u.Handle, queued)
		q := orders.QueueForUnit(u)
		if q == nil {
			continue
		}
		if queued {
			q.Push(id, node)
		} else {
			q.PurgeUnprotected()
			q.DropLeadingAutoOps()
			q.Push(id, node)
		}
	}
}

// updateCursor resolves the software-cursor shape for this frame [07 §8].
// The pointer region, the armed latch, the selection, and the world pick under
// the pointer are the whole input; the chooser owns the truth table.
func (b *battleSession) updateCursor(cl *client.Client) {
	cursors := cl.Cursors()
	if cursors == nil || b.sess == nil {
		return
	}
	mouse := cl.Input().Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)
	hover := hud.CursorHover{
		OverWorld:      b.overWorld(mx, my),
		Placing:        b.buildDef != "",
		PlacementValid: b.buildOK,
	}
	if hover.OverWorld && !hover.Placing {
		_, hover.Target, _ = b.pickTarget(mx, my)
		hover.Feature = b.hoverFeature(mx, my)
	}
	sel := hud.CursorSelection{Viewer: 0, Hostile: b.hostile}
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == 0 && u.Flags&client.SelectionFlag != 0 {
			sel.Units = append(sel.Units, u)
		}
	}
	if b.sess.Econ != nil {
		sel.Metal = b.sess.Econ.Players[0].Stock[economy.Metal]
		sel.Energy = b.sess.Econ.Players[0].Stock[economy.Energy]
	}
	cursors.SetIndex(hud.ChooseCursor(b.latch, sel, hover))
}

// overWorld reports whether a pointer position lies in the world viewport
// rather than on the HUD chrome; chrome forces the idle cursor shape [07 §8].
// It uses the same band the click path treats as panel, so the shape and the
// click destination cannot disagree.
//
// TODO(question): retail's region summary is viewport **or minimap**, and the
// minimap sets the same bit so world shapes appear over it [07 §8]. This
// overlay's minimap does not consume clicks either, so it reads as world here.
func (b *battleSession) overWorld(x, y int32) bool {
	if b.hud != nil {
		return b.hud.overWorld(x, y)
	}
	if len(b.panelButtons) > 0 && y >= 480-panelButtonH-32 {
		return false
	}
	return true
}

// hoverFeature returns the definition of the feature occupying the cell under
// the pointer, or nil [07 §8][05 "Feature instance and terrain cell"].
func (b *battleSession) hoverFeature(sx, sy int32) *content.FeatureDef {
	if b.sess.Features == nil || b.cam == nil {
		return nil
	}
	wx, wz := b.cam.ScreenToWorld(sx, sy)
	inst := b.sess.Features.InstanceAt(int(world.WorldToCell(wx)), int(world.WorldToCell(wz)))
	if inst == nil {
		return nil
	}
	return inst.Def
}

// hostile routes the cursor's side test through the acting unit's own
// diplomacy predicate, the one the order resolver consults [04 §3.4].
func (b *battleSession) hostile(actor, target *units.Unit) bool {
	if actor == nil || target == nil {
		return false
	}
	if q := orders.QueueForUnit(actor); q != nil && q.Hostility != nil {
		return q.Hostility(actor, target)
	}
	return actor.Owner != target.Owner
}
