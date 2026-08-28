package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// battleSession is the composition root for the windowed battle view. It owns
// the integrated session (all twelve kernel phases) and the interaction state:
// selection, order latch, and build placement.
type battleSession struct {
	sess  *session.Session
	cat   *content.Catalog
	cam   *camera.Camera
	hud   *retailBattleHUD
	fs    vfs.FSOps
	shell *gameShell

	msAccum float64 // renderer delta → scaled-now for Session.Step

	battleUI         *ui.BattleState
	returnToMenu     func(*client.Client)
	returnToSkirmish func(*client.Client)
	continueFunc     func(*client.Client)
	ended            bool

	anchors   hud.Anchors
	anchorsOK bool
	guiWin    *gui.Window
	guiOK     bool

	// battleMode is the established runtime mode flag consumed by the digit
	// gate [07 §9]. The production composition currently has no separate
	// publisher for this byte, so the composition value remains zero until
	// that producer is wired (TODO(question): identify the mode-byte writer).
	battleMode byte
	controller *BattleController

	// AppliedShake records only the last committed camera offset consumed by
	// this battle owner. The client renderer remains a pure frame reader; the
	// battle camera applies the authoritative phase-10 random walk once after
	// Session.Step, before the following draw [03 §5.6][I6].
	appliedShakeX int32
	appliedShakeY int32
}

// factoryBuildDelta applies the retail signed button count: left click adds
// one, Shift-left adds five; right-click variants pass negative values.
func factoryBuildDelta(shiftHeld, rightClick bool) int {
	count := 1
	if shiftHeld {
		count = 5
	}
	if rightClick {
		count = -count
	}
	return count
}

var clPtr *client.Client

// runBattleView launches the windowed battle view over the real session.
func runBattleView(opts Options, cs *contentSet) error {
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		return err
	}
	terrain := sess.World
	pal, err := loadPaletteStrict(cs)
	if err != nil {
		return err
	}

	const winW, winH = 640, 480
	terrainW := int32(terrain.CellW * 16)
	terrainH := int32(terrain.CellH * 16)
	// Camera clamp uses the same playable insets consumed by minimap input and
	// marker projection; raw terrain extents include the void margins [07 §10].
	cam := camera.NewFromTerrain(terrainW, terrainH, terrain.PlayRight, terrain.PlayBottom, winW, winH)
	cam.Pan(0, 0)
	centerOnCommanderForSession(sess, cam, winW, winH)

	b := &battleSession{sess: sess, cat: cat, cam: cam, fs: cs.fs, battleUI: ui.NewBattleState()}
	b.returnToMenu = func(cl *client.Client) {
		// The battle view has no menu shell callback; mark it ended and exit.
		b.ended = true
		if cl != nil {
			cl.RequestExit()
		}
	}
	// The battle HUD is mandatory retail content: side-selected PANELTOP,
	// PANELSIDE, PANELBOT, the 30 SIDEDATA anchors, side fonts, and the authored
	// <prefix>main/<prefix>gen/<unit>N GUI pages [07 §6][07 §9]. A production
	// battle must never silently fall back to Nanolathe-owned rectangles.
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal)
	if err != nil {
		return err
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
	cl.SetFNT(b.hud.console)
	// Software cursor [07 §8]. The cursor GAF is mandatory for a windowed
	// battle, and installation happens before entering Ebitengine's loop.
	cursors, cerr := client.LoadCursors(cs.fs)
	if cerr != nil {
		return cerr
	}
	cl.SetCursors(cursors)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	// Join the session's audio queue/cache/music to the client's device and
	// per-frame drain [03 §8.2][03 §8.3][03 §8.4]. Without this the client
	// drains a queue it was never given and no cue reaches playback.
	attachBattleAudio(cl, sess, cs.fs)
	defer detachBattleAudio(cl, sess)
	fmt.Fprintln(os.Stderr, "nanolathe: battle view — drag=select left-click=action right-click=deselect/cancel M=move A=attack P=patrol R=repair E=reclaim C=capture G=guard D=blast B=build X=cancel O=on/off N=stockpile Esc=cancel 1..9=buildpage Shift=queue")
	return client.RunGame(cl)
}

// newBattleSession builds the integrated skirmish session for the window.
// It uses the canonical DirectSkirmishConfig normalization [08 "Skirmish configuration"] [GAP T14].
func newBattleSession(opts Options, cs *contentSet) (*session.Session, *content.Catalog, error) {
	cfg := session.DirectSkirmishConfig(opts.Map)
	return newBattleSessionWithConfig(opts, cs, cfg)
}

// newBattleSessionWithConfig is the windowed composition path used by the
// skirmish lobby. The menu's per-slot and round settings reach the canonical
// session constructor [08 "Skirmish configuration"].
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

// centerOnCommanderForSession pans to the Session.LocalOwner commander [08 "Skirmish configuration"].
func centerOnCommanderForSession(sess *session.Session, cam *camera.Camera, winW, winH int32) {
	if sess == nil {
		return
	}
	owner := sess.LocalOwner
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner && u.Def != nil &&
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
	in := cl.Input()
	// End-mission presentation takes ownership of the frame once the
	// authoritative result is latched. The authored result panel owns any
	// release-inside gesture; no battle hotkey or world command leaks through
	// [07 §3][07 §11].
	if b.isResultVisible() {
		if b.hud != nil {
			if action := b.hud.handleResultInput(in); action != "" {
				b.doResultAction(action, cl)
			}
		}
		// StatePostBattle already suppresses simulation, but skip the controller
		// entirely so a result frame cannot advance or submit a world command.
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	state := b.battleState()
	// Modal ownership is decided at frame entry. Closing ARMOPT with Tab or
	// Escape must not hand the same frame's remaining mouse/key edges to the
	// battle controller [07 §2][07 §3].
	modalAtFrameStart := state.Modal() != ui.BattleModalClosed
	if in != nil && in.Kbd != nil && in.Kbd.KeyDown(input.KeyTab) {
		switch state.Modal() {
		case ui.BattleModalClosed:
			b.openBattleMenu()
		case ui.BattleModalOptions:
			b.closeBattleMenu()
		}
	}
	// ESC-menu token path [07 §2]: ESC reuses Tab menu machinery; also disarms latch as today [07 §9].
	if in != nil && in.Kbd != nil && in.Kbd.KeyDown(input.KeyEscape) && state.Modal() == ui.BattleModalClosed {
		if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
			b.openBattleMenu()
		} else {
			b.disarmPlacement()
			b.battleState().Input.Latch = input.LatchNormal
			b.battleState().Input.HUDCaptured = false
			b.battleState().Input.DragActive = false
		}
	}
	if modalAtFrameStart || state.Modal() != ui.BattleModalClosed {
		b.handleBattleMenuInput(in, cl)
	} else {
		if b.controller == nil {
			b.controller = NewBattleController(b)
		}
		b.controller.Step(BattleInputFrameFromClient(in, delta), cl)
		b.applyCommittedShake()
	}
	if b.ended {
		return
	}
	if state.Modal() != ui.BattleModalClosed {
		cl.Cursors().SetIndex(render.CursorNormal)
	} else {
		b.updateCursor(cl)
	}
	// Camera pan: exact predicates per [07 §10] C2/C3; presentation-only [I6].
	// - delta = scrollSettingByte * rawTimeDelta capped at 128 [07 §10] (C2)
	// - direction predicates: exact-edge bands plus less-than-100px beyond-edge forced strip with focus [07 §10]
	// - held-arrow gated on TALK.GUI suppression, edge never suppressed by TALK [07 §10]
	// - minimap interaction region suppresses edge [07 §10]
	// - modal GUI suppresses edge [07 §10]
	// - focus gating before edge [07 §10]
	// - scroll setting from persisted settings byte [02 "Settings"] default 32 [C-5]
	// W/A/S/D remain unbound per ON-05 (do not pan) [F-P1-008].
	if b.cam != nil && state.Modal() == ui.BattleModalClosed {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := int32(delta * 1000)
		if rawDelta <= 0 {
			rawDelta = 16
		}
		scrollSetting := b.scrollSetting()
		focused := ebiten.IsFocused() || (cl != nil && cl.IsHeadless())
		w, h := cl.Size()
		wi, hi := int32(w), int32(h)
		mx, my := int32(mouse.X), int32(mouse.Y)
		// Beyond-edge forced strip: pointer outside right/bottom <100px beyond with focus is forced to edge [07 §10].
		effX, effY := mx, my
		if focused {
			if mx >= wi && mx < wi+100 {
				effX = wi - 1
			}
			if my >= hi && my < hi+100 {
				effY = hi - 1
			}
		}
		talkActive := b.isTalkGUIActive()
		overMinimap := b.isOverMinimap(effX, effY)
		modalActive := state.Modal() != ui.BattleModalClosed
		// Held-arrow branches gated on TALK absence [07 §10]; edge branches gated on focus, modal, and minimap.
		// Left: (Left held && !talk) OR (x==0 && y<H) [07 §10]
		if kbd.KeyHeld(input.KeyLeft) && !talkActive {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
		} else if focused && !modalActive && !overMinimap && effX == 0 && effY < hi {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
		}
		// Right: (Right held && !talk) OR x==W-1 [07 §10]
		if kbd.KeyHeld(input.KeyRight) && !talkActive {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
		} else if focused && !modalActive && !overMinimap && effX == wi-1 {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
		}
		// Up: (Up held && !talk) OR (y==0 && x<W) [07 §10]
		if kbd.KeyHeld(input.KeyUp) && !talkActive {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
		} else if focused && !modalActive && !overMinimap && effY == 0 && effX < wi {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
		}
		// Down: (Down held && !talk) OR y==H-1 [07 §10]
		if kbd.KeyHeld(input.KeyDown) && !talkActive {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
		} else if focused && !modalActive && !overMinimap && effY == hi-1 {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
		}
		// Middle-drag camera pan [F-P1-008]: presentation-only, uses mouse delta / scale.
		if mouse.Held(input.MouseButtonMiddle) && mouse.Moved() {
			dx := int32(mouse.X - b.battleState().Input.PrevMouseX)
			dy := int32(mouse.Y - b.battleState().Input.PrevMouseY)
			if dx != 0 || dy != 0 {
				b.cam.Drag(dx, dy)
			}
		}
		// Wheel belongs to the active GUI list under the pointer. It is not a
		// battle-camera control [07 §2][07 §10]. The UI boundary consumes it
		// before this camera pass.
		b.battleState().Input.PrevMouseX = mouse.X
		b.battleState().Input.PrevMouseY = mouse.Y
	}
}

// applyCommittedShake transfers the cumulative phase-10 displacement from the
// current committed frame to the one camera used by input and rendering. It
// lives with battle camera ownership rather than Client so repainting the same
// frame cannot mutate camera state or consume another random value [03 §5.6].
func (b *battleSession) applyCommittedShake() {
	if b == nil || b.cam == nil || b.sess == nil || b.sess.Snapshot == nil {
		return
	}
	cur := b.sess.Snapshot.Current()
	if cur == nil {
		return
	}
	dx := cur.ShakeOffsetX - b.appliedShakeX
	dy := cur.ShakeOffsetY - b.appliedShakeY
	if dx != 0 || dy != 0 {
		b.cam.Pan(dx, dy)
	}
	b.appliedShakeX = cur.ShakeOffsetX
	b.appliedShakeY = cur.ShakeOffsetY
}

// minimapLayout is the sole production adapter for radar geometry. PlayRight
// and PlayBottom are authored by map loading; the rail destination is the
// battle composer's fixed 126-pixel canvas origin [07 §6][07 §10].
func (b *battleSession) minimapLayout() (camera.Minimap, hud.Rect, bool) {
	if b == nil || b.sess == nil || b.sess.World == nil || b.hud == nil {
		return camera.Minimap{}, hud.Rect{}, false
	}
	playW, playH := b.sess.World.PlayRight, b.sess.World.PlayBottom
	if playW <= 0 || playH <= 0 {
		return camera.Minimap{}, hud.Rect{}, false
	}
	dst, ok := b.hud.minimapRect()
	if !ok {
		return camera.Minimap{}, hud.Rect{}, false
	}
	return camera.LayoutMinimap(playW, playH), dst, true
}

// isOverMinimap identifies the authored radar interaction region [07 §10].
func (b *battleSession) isOverMinimap(x, y int32) bool {
	_, dst, ok := b.minimapLayout()
	return ok && dst.Contains(x, y)
}

// isOnRadar distinguishes the fitted radar rectangle from its 126-pixel
// canvas letterbox. The canvas remains the interaction capture region, while
// only the fitted rectangle selects the direct lens branch [07 §10].
func (b *battleSession) isOnRadar(x, y int32) bool {
	m, dst, ok := b.minimapLayout()
	if !ok {
		return false
	}
	dl, dt, dr, db := dst.Ordered()
	if x < dl || x > dr || y < dt || y > db {
		return false
	}
	cx, cy, _ := m.DisplayToCanvas(x, y, dl, dt, dr-dl+1, db-dt+1)
	return m.HitTest(cx, cy)
}

// handleInput processes selection, orders, and build placement.
// It converts input into complete canonical commands with target/position and
// queue modifiers (shift-queued) via one picking routine that respects fog,
// unit/feature overlap, and command validity [07 §9][03 §3.2] C8 [P0-I14].
// Order buttons enqueue session-owned commands directly [R-P0-03]; build products are
// data-driven from cat.BuildMenus; input-capture latch prevents HUD presses
// from leaking into world drag [F-P0-003][F-P1-008].
func (b *battleSession) handleInput(in *client.InputState, cl *client.Client) {
	kbd := in.Kbd
	mouse := in.Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)
	b.battleState().Input.ShiftHeld = kbd.HasShift()
	b.battleState().Input.PointerX, b.battleState().Input.PointerY = mx, my
	// Any latch held by Shift retires on the live Shift-up, regardless of
	// order family [R-P0-11].
	if !b.battleState().Input.ShiftHeld && b.battleState().Input.ShiftLatchSticky {
		b.battleState().Input.Latch = input.LatchNormal
		b.battleState().Input.ShiftLatchSticky = false
	}

	// Minimap click-to-jump [C-6][07 §10] uses the same layout adapter as draw.
	// This is presentation-only and never writes sim [I6].
	if b.isOverMinimap(mx, my) {
		if mouse.Pressed(input.MouseButtonLeft) && b.cam != nil {
			m, dst, ok := b.minimapLayout()
			if !ok {
				return
			}
			client.HandleMinimapInput(b.cam, m, dst, b.sess.World.PlayRight, b.sess.World.PlayBottom, mx, my, b.isOnRadar(mx, my), nil)
			return
		}
		// While over minimap, suppress world drag/selection [07 §10] minimap interaction region.
		if mouse.Pressed(input.MouseButtonLeft) || mouse.Held(input.MouseButtonLeft) {
			return
		}
	}

	// Latch arming via hotkeys — retail latch byte IS dispatcher switch key [GAP T22][07 §9] C11.
	// Preserve authored button order and pagination for build menu [02 "Build-menu catalog keys"].
	if kbd.KeyDown(input.KeyM) {
		b.battleState().Input.Latch = input.LatchMove
	}
	if kbd.KeyDown(input.KeyA) {
		b.battleState().Input.Latch = input.LatchAttack
	}
	if kbd.KeyDown(input.KeyP) {
		b.battleState().Input.Latch = input.LatchPatrol
	}
	if kbd.KeyDown(input.KeyR) {
		b.battleState().Input.Latch = input.LatchRepair
	}
	if kbd.KeyDown(input.KeyE) {
		b.battleState().Input.Latch = input.LatchReclaim
	}
	if kbd.KeyDown(input.KeyC) {
		b.battleState().Input.Latch = input.LatchCapture
	}
	if kbd.KeyDown(input.KeyG) {
		b.battleState().Input.Latch = input.LatchFollow
	}
	if kbd.KeyDown(input.KeyD) {
		b.battleState().Input.Latch = input.LatchBlast
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
	// Game-speed and pause keys [07 §2][07 §11]: +/- clamp Requested 1..20
	// with localized messages; Pause toggles pause with the retail message.
	if kbd.KeyDown(input.KeyPause) {
		b.togglePause()
	}
	if kbd.KeyDown(input.KeyEqual) || kbd.KeyDown(input.KeyNumpadAdd) {
		b.adjustGameSpeed(1)
	}
	if kbd.KeyDown(input.KeyMinus) || kbd.KeyDown(input.KeyNumpadSubtract) {
		b.adjustGameSpeed(-1)
	}
	// Digit routing uses the established battle-mode/Alt gate. Ctrl+digit is
	// assignment; the non-page branch is group recall with Shift as preserve /
	// toggle [07 §9] C9-C10.
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
			if kbd.KeyHeld(input.KeyCtrl) {
				_ = b.DispatchGroupAssign(d)
			} else {
				b.routeDigit(d, kbd.KeyHeld(input.KeyAlt), kbd.HasShift())
			}
		}
	}
	// Page next/prev data-driven with guard [R-P0-03][07 §9] C10: no hardcoding.
	if kbd.KeyDown(input.KeyPrior) || kbd.KeyDown(input.KeyRight) && kbd.HasShift() {
		b.nextBuildPage()
	}
	if kbd.KeyDown(input.KeyNext) || kbd.KeyDown(input.KeyLeft) && kbd.HasShift() {
		b.prevBuildPage()
	}
	if kbd.KeyDown(input.KeyEscape) {
		b.disarmPlacement()
		b.battleState().Input.Latch = input.LatchNormal
		b.battleState().Input.ShiftLatchSticky = false
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		return
	}
	// Right button is deselect/cancel only: every world order fires on left [07 §9][04 §3.4].
	if mouse.Pressed(input.MouseButtonRight) {
		// A factory product button is the one right-click exception: it
		// subtracts one/five from the matching tail node [R-P0-11].
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) && b.hud.consumeRightClick(b, mx, my) {
			return
		}
		if b.battleState().Input.BuildDef != "" {
			// Cancel armed placement before affecting selection [R-P0-03][F-P0-003][07 §9].
			b.disarmPlacement()
			b.battleState().Input.HUDCaptured = false
			return
		}
		if b.battleState().Input.Latch != input.LatchNormal {
			// Cancel armed order latch to idle [07 §9][07 §8][07 §9].
			b.battleState().Input.Latch = input.LatchNormal
			b.battleState().Input.ShiftLatchSticky = false
			return
		}
		if b.hasSelection() {
			// Deselect is a typed command; UI never mutates live flags [I6].
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
			return
		}
		return
	}

	// Input-capture latch [F-P0-003][F-P1-008]: press that begins on HUD chrome
	// never starts/completes world drag selection even if released over world.
	// Detect HUD origin on the pressed edge.
	if mouse.Pressed(input.MouseButtonLeft) {
		overHUD := false
		if !b.overWorld(mx, my) {
			overHUD = true
		}
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) {
			overHUD = true
		}
		if overHUD {
			b.battleState().Input.HUDCaptured = true
			b.battleState().Input.HUDPressX = mx
			b.battleState().Input.HUDPressY = my
			b.battleState().Input.DragActive = false
		}
	}
	if b.battleState().Input.HUDCaptured && mouse.Released(input.MouseButtonLeft) {
		// Retail buttons arm while held and activate once on release-inside.
		// Requiring the same authored gadget at both endpoints prevents a drag
		// across the rail from activating a different control [07 §3][07 §4].
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		if b.hud != nil && b.hud.sameButton(b, b.battleState().Input.HUDPressX, b.battleState().Input.HUDPressY, mx, my) && b.hud.consumeClick(b, mx, my) {
			return
		}
		return
	}
	if b.battleState().Input.HUDCaptured {
		return
	}

	// A left press the placement path already consumed owns that button until
	// it is released. Retail routes one press/release pair through exactly one
	// world path [07 §9]; because placement commits on the press edge and then
	// disarms, the remaining held frames of an ordinary human click used to
	// fall through to drag selection, and its release issued a contextual Move
	// that purged the build order the same click had just queued.
	if b.battleState().Input.PlaceCaptured {
		if !mouse.Held(input.MouseButtonLeft) {
			b.battleState().Input.PlaceCaptured = false
		}
		if b.battleState().Input.BuildDef != "" {
			b.updatePlacement(mx, my)
		}
		return
	}

	// Build placement mode captures left-clicks before selection handling [R-P0-03].
	// Right-click cancellation is handled at the top of handleInput with the
	// latch/selection precedence of [07 §9].
	//
	// A shift-click leaves the mode armed so the next click places another copy;
	// retail records that on a placement-valid-pending bit and drops back to the
	// idle latch as soon as shift is released, whether or not another click
	// arrives. Arming from a build button does not set the bit, so a first
	// placement without shift still gets its click.
	if b.battleState().Input.BuildSticky && !kbd.HasShift() {
		b.disarmPlacement()
	}
	if b.battleState().Input.BuildDef != "" {
		b.updatePlacement(mx, my)
		if mouse.Pressed(input.MouseButtonLeft) {
			// The press belongs to placement whatever it decides below —
			// placed, refused, or rejected by the command boundary.
			b.battleState().Input.PlaceCaptured = true
			if !b.battleState().Input.BuildOK {
				// An illegal site queues nothing and stays armed; the player
				// hears the refusal and can move the ghost [07 §9].
				b.playUICue(cl, "notoktobuild")
				return
			}
			queued := kbd.HasShift()
			if !b.commitBuild(queued) {
				// A command rejection is a failed commit, not an armed
				// placement state. All cancellation exits share disarmPlacement.
				b.disarmPlacement()
				return
			}
			b.playUICue(cl, "oktobuild")
			if queued {
				b.battleState().Input.BuildSticky = true
			} else {
				b.disarmPlacement()
			}
		}
		return
	}

	leftHeld := mouse.Held(input.MouseButtonLeft)
	additive := kbd.HasShift()
	if leftHeld && !b.battleState().Input.DragActive {
		b.battleState().Input.DragActive = true
		b.battleState().Input.DragStartX, b.battleState().Input.DragStartY = mx, my
		b.battleState().Input.DragEndX, b.battleState().Input.DragEndY = mx, my
	} else if leftHeld && b.battleState().Input.DragActive {
		b.battleState().Input.DragEndX, b.battleState().Input.DragEndY = mx, my
	} else if !leftHeld && b.battleState().Input.DragActive {
		b.battleState().Input.DragActive = false
		rect := client.NormalizeRect(b.battleState().Input.DragStartX, b.battleState().Input.DragStartY, b.battleState().Input.DragEndX, b.battleState().Input.DragEndY)
		w, h := rect.MaxX-rect.MinX, rect.MaxY-rect.MinY
		if w < 3 && h < 3 {
			// Small click precedence [07 §8][07 §9][RS-P0-003]: armed latch dispatches on left-click; idle latch left-click selects or issues contextual order; right-click never issues an order [04 §3.4][07 §9].
			// Uses ONE canonical picker client.PickUnit so fog, 16px radius, strict < tie (lower slot wins),
			// unit>feature priority and viewer are identical for selection and targeting [07 §9][03 §3.2][P0-I14].
			if b.battleState().Input.Latch != input.LatchNormal {
				code := hud.LatchToCode(b.battleState().Input.Latch)
				if code != 0 {
					b.orderSelected(code, mx, my, additive)
				}
				// Return latch to Normal after dispatch unless shift-queuing keeps it [07 §9][P0-I14].
				if additive {
					b.battleState().Input.ShiftLatchSticky = true
				} else {
					b.battleState().Input.Latch = input.LatchNormal
					b.battleState().Input.ShiftLatchSticky = false
				}
			} else {
				// Idle latch left-click: every world command is left-click; right-click is deselect/cancel only [07 §9][04 §3.4].
				// When clicking an own visible unit we change selection; otherwise with a selection we issue the contextual order (code 1) which delegates to move/attack/repair/etc. based on the target [04 §3.4].
				viewer := visibility.PlayerID(0)
				if b.sess != nil {
					viewer = visibility.PlayerID(b.sess.LocalOwner)
				}
				f, ok := b.currentSnapshot()
				if !ok {
					return
				}
				var bh pool.Handle
				var bu frame.UnitView
				var hit bool
				// The framebuffer composer already rebases the projected world point
				// from the beam origin before drawing it. Mouse coordinates are in that
				// same logical framebuffer, so do not subtract the HUD viewport origin
				// a second time [03 §2.5][07 §8].
				shellX, shellY := mx, my
				bh, bu, hit = client.PickSnapshotUnit(f, shellX, shellY, b.cam, uint8(viewer))
				hitOwn := hit && bh != 0 && bu.Owner == b.sess.LocalOwner
				if hitOwn {
					if additive {
						_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionToggle, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{bh}}})
					} else {
						_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{bh}}})
					}
				} else {
					if b.hasSelection() {
						// Left-click contextual order when a selection exists and the click is not on own unit [04 §3.4][07 §9].
						b.orderSelected(1, mx, my, additive)
					} else {
						// No selection and click not on own unit: clear if not additive, else preserve [07 §9] C6.
						if !additive {
							_ = b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
						}
					}
				}
			}
		} else {
			// The drag rectangle is already in the framebuffer coordinate space
			// used by the rendered world [03 §2.5][07 §8].
			shellRect := rect
			f, ok := b.currentSnapshot()
			if !ok {
				return
			}
			handles := client.SnapshotUnitHandlesInRect(f, b.cam, shellRect, b.sess.LocalOwner)
			kind := session.HumanSelectionReplace
			if additive {
				kind = session.HumanSelectionToggle
			}
			_ = b.enqueueHumanCommand(session.HumanCommand{Kind: kind, Selection: session.HumanSelectionCommand{Handles: handles}})
		}
	}
	// No right-button order path: right-click is deselect/cancel only, handled at the top [07 §9][04 §3.4].
}

func (b *battleSession) routeDigit(digit int, altHeld, shiftHeld bool) {
	if hud.RoutesToPage(b.battleMode, altHeld) {
		b.switchBuildPage(digit)
		return
	}
	_ = b.DispatchGroupRecall(digit, shiftHeld)
}

func (b *battleSession) currentSnapshot() (*frame.Frame, bool) {
	if b == nil || b.sess == nil || b.sess.Snapshot == nil {
		return nil, false
	}
	cur := b.sess.Snapshot.Current()
	return cur, cur != nil
}

func (b *battleSession) hasSelection() bool {
	if f, ok := b.currentSnapshot(); ok {
		return len(f.Selection.Handles) != 0
	}
	return false
}

// selectedCommandUnits resolves the committed selection for typed commands.
// Live selection flags are presentation state; command application owns all
// authoritative selection and queue mutation [01 §4.4][07 §9].
func (b *battleSession) selectedCommandUnits() []*units.Unit {
	if b == nil || b.sess == nil || b.sess.Units == nil {
		return nil
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	out := make([]*units.Unit, 0, len(f.Selection.Handles))
	for _, h := range f.Selection.Handles {
		u := b.sess.Units.Unit(h)
		if u != nil && u.Alive && u.Owner == b.sess.LocalOwner {
			out = append(out, u)
		}
	}
	return out
}

// selectedUnits returns all selected LocalOwner units in stable ascending order [I1][07 §9].
func (b *battleSession) selectedUnits() []*units.Unit {
	owner := uint8(0)
	if b.sess != nil {
		owner = b.sess.LocalOwner
	}
	var out []*units.Unit
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner && u.Flags&hud.SelectionFlag != 0 {
			out = append(out, u)
		}
	}
	return out
}

// selectedBuilder returns the first player builder unit under selection.
func (b *battleSession) selectedBuilder() *units.Unit {
	if b.sess == nil || b.sess.Units == nil {
		return nil
	}
	owner := b.sess.LocalOwner
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner && u.Flags&hud.SelectionFlag != 0 &&
			u.Def != nil && u.Def.Builder {
			return u
		}
	}
	return nil
}

// catalogDefID returns the stable compiled catalog identity used by the HUD
// page guard. It is deliberately not a literal placeholder and does not use
// the allocation order of the live unit pool [02 §5][07 §9] C10.
func (b *battleSession) catalogDefID(u *units.Unit) uint16 {
	if b == nil || b.cat == nil || u == nil || u.Def == nil {
		return 0
	}
	id, ok := b.cat.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || id == 0 || id > 0xffff {
		return 0
	}
	return uint16(id)
}

// switchBuildPage handles digit 1..9 build page switching [07 §9] C10.
// Page number lives in flag bits 23-25 with bit 22 paged indicator [07 §9].
func (b *battleSession) switchBuildPage(digit int) {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount == 0 || digit < 1 || digit > 9 {
		return
	}
	target := hud.ClampPage(hud.DigitToPage(digit), int(frame.CommandPage.PageCount))
	_ = b.DispatchBuildPage(target)
}

// nextBuildPage advances one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) nextBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		return
	}
	target := hud.ClampPage(int(frame.CommandPage.Page)+1, int(frame.CommandPage.PageCount))
	_ = b.DispatchBuildPage(target)
}

// prevBuildPage goes back one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) prevBuildPage() {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
		return
	}
	target := hud.ClampPage(int(frame.CommandPage.Page)-1, int(frame.CommandPage.PageCount))
	_ = b.DispatchBuildPage(target)
}

// handleHudOrderButton binds named order buttons to the session command path
// [R-P0-03][07 §9]. It is used by retail HUD consumeClick.
func (b *battleSession) handleHudOrderButton(name string) {
	latch := hud.ParseButtonLatch(name, 1)
	// STOP is a distinct immediate command. It must never dispatch contextual
	// code 1 at the map origin before the Stop descriptor [04 §3.4][07 §9].
	if latch == input.LatchNormal && containsStop(name) {
		_ = b.dispatchStopCommand()
		b.battleState().Input.Latch = input.LatchNormal
		return
	}
	if latch.IsValid() {
		b.battleState().Input.Latch = latch
	}
}

func containsStop(s string) bool {
	upper := strings.ToUpper(s)
	return strings.Contains(upper, "STOP")
}

// cancelSelectedProduction cancels the tail-most matching factory/mobile build for selected units [04 §3.3][P1-14].
// It walks each selected factory/builder's primary queue tail-most and decrements or frees via
// construction.CancelTailMost / CancelMobileTailMost. Tombstone bit ensures weapon-target-clear skip [04 §3.3].
func (b *battleSession) cancelSelectedProduction() {
	for _, u := range b.selectedCommandUnits() {
		if u != nil {
			_ = b.DispatchCancelProduction(u.Handle)
		}
	}
}

// toggleOnOffSelected issues Activate/Deactivate for OnOffable units [02 "Unit record"].
// OnOffable is data-driven; the command is Activate/Deactivate via orders.NewNodeForOrder [P0-I14].
func (b *battleSession) toggleOnOffSelected(queued bool) {
	for _, u := range b.selectedCommandUnits() {
		if u == nil || u.Def == nil || !u.Def.OnOffable {
			continue
		}
		// Activation state is typed unit state, not the unrelated order flag
		// word. The queued command is consumed by the ordinary order/COB edge
		// machinery [05 "Unit instance economy state"].
		_ = b.DispatchActivation(session.HumanActivationCommand{
			Unit: u.Handle, Activate: !u.Activated, Queued: queued,
		})
	}
}

// stockpileSelected queues one BuildWeapon round for stockpile weapons [06 §11.1].
// Stockpile launch requires BuildWeapon descriptor (rear segment 0x40000) with count.
func (b *battleSession) stockpileSelected(queued bool) {
	for _, u := range b.selectedCommandUnits() {
		if u != nil {
			_ = b.DispatchStockpile(u.Handle, queued)
		}
	}
}

// scrollSetting returns the persisted scroll speed byte [02 "Settings"] [07 §10] C2.
// It is presentation-only and never touches sim [I6].
func (b *battleSession) scrollSetting() byte { // [07 §10] [02 "Settings"]
	s, _ := settings.Load()
	ss := s.ScrollSpeed
	if ss <= 0 || ss > 255 {
		ss = settings.DefaultScrollSpeed
	}
	return byte(ss)
}

// isTalkGUIActive reports whether TALK.GUI suppresses held-arrow movement [07 §10].
// Retail suppresses only held-arrow, not edge. In Nanolathe chat is not yet fully
// wired, so we treat any active modal that would correspond to chat as active.
// For now, no dedicated TALK.GUI state exists, so held-arrow is never suppressed
// except when a modal menu is active which already suppresses edge separately.
// This preserves retail's distinction: TALK suppresses arrow but not edge.
func (b *battleSession) isTalkGUIActive() bool { // [07 §10]
	// TODO(question): wire real TALK.GUI detection when chat is implemented; for now return false
	return false
}

func (b *battleSession) cursorWorld(sx, sy int32) (wx, wy, wz numeric.Fixed) {
	if b.cam == nil {
		return 0, 0, 0
	}
	// Clamp pointer into the battle viewport before ground resolution [07 §8] step 1 [C-2].
	clampedX := sx
	clampedY := sy
	if clampedX < camera.OriginX+1 {
		clampedX = camera.OriginX + 1
	} else if clampedX > 639 {
		clampedX = 639
	}
	if clampedY < camera.OriginY {
		clampedY = camera.OriginY
	} else if clampedY > 447 {
		clampedY = 447
	}
	// The renderer stores world points at beam position minus OriginX/Y. Restore
	// those fixed offsets for the camera inverse [03 §2.5].
	fx, fz := b.cam.ScreenToWorld(clampedX+camera.OriginX, clampedY+camera.OriginY)
	if b.sess == nil || b.sess.World == nil {
		return fx, 0, fz
	}
	return b.sess.World.CursorToWorld(int32(fx>>16), int32(fz>>16))
}

// armPlacement enters build-placement mode for a product [07 §9]. Retail arms
// the MOBILEBUILD latch, stores the product's definition id in a pending-build
// word and plays the `addbuild` cue; nothing is queued until the world click.
func (b *battleSession) armPlacement(def *content.UnitDef) {
	if def == nil {
		return
	}
	footX, footZ := footprintCellsForCatalog(b.cat, def)
	b.battleState().ArmPlacement(def.CanonicalKey, footX, footZ)
	b.battleState().Input.Latch = input.LatchMobileBuild
}

// disarmPlacement returns the battle screen to the idle latch after a placement
// ends, whether it ended in a click, a cancel, or shift being released [07 §9].
func (b *battleSession) disarmPlacement() {
	if b == nil {
		return
	}
	b.battleState().ClearPlacement()
}

// playUICue plays a non-positional interface sound by its authored alias
// [07 §9][03 §8.3]. Retail's placement path plays `oktobuild` on a placed site
// and `notoktobuild` on a refused one; both are ordinary sound aliases, not a
// separate UI audio path.
func (b *battleSession) playUICue(cl *client.Client, alias string) {
	if b == nil || b.sess == nil || b.sess.Audio == nil {
		return
	}
	_ = b.sess.Audio.PlayUICue(alias)
}

// updatePlacement tracks the ghost under the cursor and validates it against
// the world [04 §6.2][PLAN_08 C17].
func (b *battleSession) updatePlacement(mx, my int32) {
	b.battleState().Input.BuildMX, b.battleState().Input.BuildMY = mx, my
	wx, _, wz := b.cursorWorld(mx, my)
	b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ = world.PlacementAnchor(wx, wz, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ)
	self := uint16(0)
	if frame, ok := b.currentSnapshot(); ok {
		self = uint16(frame.CommandPage.Builder)
	}
	footX, footZ := b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ
	var def *content.UnitDef
	if b.cat != nil {
		def, _ = b.cat.Unit(b.battleState().Input.BuildDef)
	}
	result, err := b.checkProductPlacement(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, def, footX, footZ, self)
	b.battleState().Input.BuildOK = err == nil
	waterline := int32(0)
	if def != nil {
		waterline = def.Waterline
	}
	if err == nil {
		b.battleState().Input.BuildSiteH = result.SiteHeight
	} else {
		// Keep an informative ghost height while illegal; legality itself is
		// decided only by the canonical query above.
		yard, _ := world.ParseYardMap(b.yardMapFor(), int(footX), int(footZ))
		b.battleState().Input.BuildSiteH = b.sess.World.SiteHeight(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, yard, int(footX), int(footZ), waterline)
	}
}

// checkProductPlacement is the battle-side adapter to the canonical
// preview/commit predicate. It resolves movement/FBI terrain rules and uses
// the product's compiled footprint, matching construction exactly [R-P0-08]
// [07 §9].
func (b *battleSession) checkProductPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self uint16) (world.PlacementResult, error) {
	if b == nil || b.sess == nil || b.sess.World == nil {
		return world.PlacementResult{}, fmt.Errorf("battle: placement world unavailable")
	}
	if def == nil {
		return world.PlacementResult{}, fmt.Errorf("battle: placement definition unavailable")
	}
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		return world.PlacementResult{}, err
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		return world.PlacementResult{}, err
	}
	rules, err := world.PlacementRulesForUnit(b.cat, def)
	if err != nil {
		return world.PlacementResult{}, err
	}
	var yard []world.YardCell
	if !def.BMCode {
		yard, err = world.ParseYardMap(def.YardMap, int(footX), int(footZ))
		if err != nil {
			return world.PlacementResult{}, err
		}
	}
	// Completed buildings live in the construction service's structures
	// registry once their frame occupancy stamps are released; the ghost must
	// reject overlap with them exactly like the sim validator [04 §6.2][05].
	selfHandle := pool.Handle(self)
	if _, blocked := b.sess.Build.StructureBlocks(selfHandle, rect); blocked {
		return world.PlacementResult{}, fmt.Errorf("battle: footprint overlaps a completed structure")
	}
	return b.sess.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: self, Mobile: def.BMCode})
}

// placementRect returns the armed site's footprint as a screen rectangle
// [07 §9]. Retail projects the two cell-aligned corners with the ordinary
// half-height shear, using the site height for both, so the ghost lies flat on
// the ground the building will stand on rather than following the cursor.
func (b *battleSession) placementRect() (left, top, right, bottom int32) {
	l := b.battleState().Input.BuildCellX * 16
	t := b.battleState().Input.BuildCellZ * 16
	r := l + b.battleState().Input.BuildFootX*16
	btm := t + b.battleState().Input.BuildFootZ*16
	return b.siteRectToScreen(l, t, r, btm, b.battleState().Input.BuildSiteH)
}

// siteRectToScreen projects a map-pixel footprint rectangle standing at height
// h (in map-pixel height units) into viewport-relative screen pixels [03 §2.5].
// Both corners take the same height, which is what flattens the marker onto the
// ground plane instead of tilting it.
func (b *battleSession) siteRectToScreen(l, t, r, btm, h int32) (left, top, right, bottom int32) {
	if b.cam == nil {
		return l, t, r, btm
	}
	px := func(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }
	x0, y0 := b.cam.WorldToScreen(px(l), px(h), px(t))
	x1, y1 := b.cam.WorldToScreen(px(r), px(h), px(btm))
	return x0 - camera.OriginX, y0 - camera.OriginY, x1 - camera.OriginX, y1 - camera.OriginY
}

// yardMapFor returns the placed definition's yard text when known.
func (b *battleSession) yardMapFor() string {
	def, found := b.cat.Unit(b.battleState().Input.BuildDef)
	if !found || def == nil {
		return ""
	}
	return def.YardMap
}

// commitBuild queues a mobile-build order through the ordinary construction
// path [PLAN_08 C12/C23][P0-I05]; the session's construction pump drives the lifecycle.
// Site coordinates are passed at queue time and preserved on the queued node as GoalX/Y/Z [P0-I05]:
// the validated footprint center and site height carry the selected site via QueueMobileBuild.
// It uses catalog indices (not FNV hash) [P0-I05] and respects queue modifier (shift=queued) [04 §3.3][P0-I14].
// Every producer goes through one canonical payload constructor [P0-I03]: orders.NewMobileBuildNode / QueueMobileBuild.
// It is data-driven: product must be in builder's BuildMenus list; illegal placement queues nothing [R-P0-03].
func (b *battleSession) commitBuild(queued bool) bool {
	frame, ok := b.currentSnapshot()
	if !ok || frame.CommandPage.Builder == 0 {
		return false
	}
	v, found := snapshotUnitByHandle(frame, frame.CommandPage.Builder)
	if !found || b.sess == nil || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
		return false
	}
	if b.cat != nil && !hud.ValidateBuildProduct(b.cat, v.DefName, b.battleState().Input.BuildDef) {
		return false // GUI may not invent products absent from authored list [R-P0-03]
	}
	if !b.battleState().Input.BuildOK {
		return false // illegal placement queues nothing [R-P0-03]
	}
	// The order carries the footprint's center and the site height, not the raw
	// cursor point: retail recomputes the same cell-aligned anchor the ghost was
	// drawn on and stores `((foot + 2*cell) << 19)` per axis with the validator's
	// site height as Y [07 §9]. Sending the cursor point instead would put the
	// building half a footprint off the box the player aimed with.
	wx, wz := world.PlacementCenter(b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ)
	// Queue the typed command; the session applies it at the authoritative input
	// phase [01 §4.4][07 §9].
	wy := numeric.Fixed(int64(b.battleState().Input.BuildSiteH) << 16)
	if err := b.DispatchMobileBuild(b.battleState().Input.BuildDef, wx, wy, wz, queued); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: build %s: %v\n", b.battleState().Input.BuildDef, err)
		return false
	}
	return true
}

// drawBuildGhost draws the armed build site the way retail does [07 §9].
//
// The ghost is two nested one-pixel outlines on the footprint's own cell-aligned
// rectangle — not a box centered on the cursor — and both are drawn in a single
// color chosen by site validity: logical palette entry 10 when the site is legal
// and 4 when it is not. Retail draws no text next to it; the product name and
// its cost live on the build button, and the cursor (`cursorfindsite` versus
// `cursortoofar`) carries the validity as well.
//
// Retail suppresses the ghost whenever the pointer leaves the world viewport,
// so it never appears over the side panel or the minimap.
func (b *battleSession) drawBuildGhost(c *client.Client) {
	if b.battleState().Input.BuildDef == "" || b.cam == nil {
		return
	}
	if !b.overWorld(b.battleState().Input.PointerX, b.battleState().Input.PointerY) {
		return
	}
	l, t, r, btm := b.placementRect()
	col := c.GUIColor(hud.GhostColorIllegal)
	if b.battleState().Input.BuildOK {
		col = c.GUIColor(hud.GhostColorLegal)
	}
	c.UIFrameRect(int(l), int(t), int(r-l), int(btm-t), col)
	c.UIFrameRect(int(l)+1, int(t)+1, int(r-l)-2, int(btm-t)-2, col)
}

// footprintCells returns a definition's footprint, clamped to at least one cell
// on each axis so a definition that authors neither still occupies a square.
func footprintCells(def *content.UnitDef) (footX, footZ int32) {
	footX, footZ = def.FootprintX, def.FootprintZ
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	return footX, footZ
}

func footprintCellsForCatalog(cat *content.Catalog, def *content.UnitDef) (footX, footZ int32) {
	// HUD preview must resolve the same compiled movement/FBI footprint as the
	// sim [R-P0-08][07 §9] C-7: prefer the world helper so the two cannot
	// diverge.
	return world.FootprintForUnit(cat, def)
}

// loadPalette loads the retail palette tables for compatibility with focused
// presentation tests. Production construction uses loadPaletteStrict so a
// missing or malformed shared palette cannot become an unannounced nil.
func loadPalette(cs *contentSet) *palette.Tables {
	p, _ := loadPaletteStrict(cs)
	return p
}

func loadPaletteStrict(cs *contentSet) (*palette.Tables, error) {
	if cs == nil || cs.fs == nil {
		return nil, retailFrontendAssetError(cs, "retail palette", "palettes/PALETTE.PAL", "the shared retail palette tables", fmt.Errorf("missing VFS"))
	}
	p, err := palette.Load(cs.fs)
	if err != nil {
		return nil, retailFrontendAssetError(cs, "retail palette", "palettes/PALETTE.PAL", "the shared retail palette tables", err)
	}
	return p, nil
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
	wx, wy, wz := b.cursorWorld(sx, sy)
	pos := &orders.ResolvePos{X: wx, Y: wy, Z: wz}
	if b.sess == nil || b.sess.Units == nil || b.cam == nil {
		return 0, nil, pos
	}
	// ONE picking routine via client.PickUnit [P0-I14][07 §9][03 §3.2] C8.
	// PickUnit uses the framebuffer coordinates produced by the world renderer.
	// The battle input pointer already uses that coordinate space [03 §2.5].
	shellX, shellY := sx, sy
	viewer := visibility.PlayerID(b.sess.LocalOwner)
	if bh, bu := client.PickUnit(shellX, shellY, b.cam, b.sess.Units, b.sess.Vis, viewer); bh != 0 && bu != nil {
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
					viewer := visibility.PlayerID(b.sess.LocalOwner)
					visible = b.sess.Vis.VisiblePoint(viewer, wx, pos.Y, wz)
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
						visible = b.sess.Vis.VisibleExtents(viewer, bx)
					}
				}
				if visible {
					pos.HasFeature = true
					// Reclaimable is not a corpse identity: trees, rocks and deposits
					// may all be reclaimable. Resolve the feature's compiled identity
					// against authored unit Corpse links [05 "Feature reclaim"].
					pos.IsWreck = b.isCorpseFeature(def)
					pos.FeatureResurrectable = pos.IsWreck && def.Reclaimable
				}
			}
		} else if b.sess.Features != nil {
			// Consult the feature service for instances not represented in the
			// terrain's compact feature table.
			if inst := b.sess.Features.InstanceAt(int(cx), int(cz)); inst != nil && inst.Def != nil {
				visible := true
				if b.sess.Vis != nil {
					viewer := visibility.PlayerID(b.sess.LocalOwner)
					visible = b.sess.Vis.VisiblePoint(viewer, wx, pos.Y, wz)
				}
				if visible {
					pos.HasFeature = true
					pos.IsWreck = b.isCorpseFeature(inst.Def)
					pos.FeatureResurrectable = pos.IsWreck && inst.Def.Reclaimable
				}
			}
		}
	}
	return 0, nil, pos
}

// isCorpseFeature applies the recovered resurrection identity rule: truncate
// the feature name at its first underscore, then resolve that unit key in the
// compiled catalog. Reclaimable alone is not a wreck identity [05 "Resurrection"].
func (b *battleSession) isCorpseFeature(def *content.FeatureDef) bool {
	if b == nil || b.cat == nil || def == nil {
		return false
	}
	key := content.CanonicalKey(def.CanonicalKey)
	if i := strings.IndexByte(key, '_'); i >= 0 {
		key = key[:i]
	}
	if key == "" {
		return false
	}
	_, ok := b.cat.Unit(key)
	return ok
}

// orderSelected resolves code at the clicked world position and submits one
// typed order command. Descriptor selection remains solely in orders.Resolve;
// this integration layer does not guess an attack descriptor for ground clicks
// [04 §3.4][07 §9].
func (b *battleSession) orderSelected(code int, sx, sy int32, queued bool) {
	targetHandle, _, pos := b.pickTarget(sx, sy)
	if pos == nil {
		return
	}
	// The HUD latch table is the single semantic mapping between an armed
	// order and the session order code. Validate the caller's code by running
	// it through that table; do not maintain a second switch here [07 §9].
	latch := input.Latch(code)
	if hud.LatchToCode(latch) != code {
		return
	}
	_ = b.DispatchOrderCommand(session.HumanOrderCommand{
		Code: code, Target: targetHandle,
		Position: *pos, Queued: queued,
	})
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
		Placing:        b.battleState().Input.BuildDef != "",
		PlacementValid: b.battleState().Input.BuildOK,
	}
	if hover.OverWorld && !hover.Placing {
		_, hover.Target, _ = b.pickTarget(mx, my)
		hover.Feature = b.hoverFeature(mx, my)
	}
	sel := hud.CursorSelection{Viewer: b.sess.LocalOwner, Hostile: b.hostile}
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == b.sess.LocalOwner && u.Flags&hud.SelectionFlag != 0 {
			sel.Units = append(sel.Units, u)
		}
	}
	if b.sess.Econ != nil {
		local := b.sess.LocalOwner
		sel.Metal = b.sess.Econ.Players[local].Stock[economy.Metal]
		sel.Energy = b.sess.Econ.Players[local].Stock[economy.Energy]
	}
	cursors.SetIndex(hud.ChooseCursor(b.battleState().Input.Latch, sel, hover))
}

// overWorld reports whether a pointer position lies in the world viewport
// rather than on the HUD chrome; chrome forces the idle cursor shape [07 §8].
// It uses the same drawn-chrome layout as the click path, so the shape and
// click destination cannot disagree [C-3][07 §6][07 §8].
func (b *battleSession) overWorld(x, y int32) bool {
	if b.hud != nil {
		return b.hud.overWorld(x, y)
	}
	// When no authored HUD layout is available, use the viewport transform's
	// drawn-chrome rectangle [C-3][07 §8].
	vt := client.NewViewportTransform(b.cam, nil, 640, 480)
	return vt.Viewport.Contains(x, y)
}

// hoverFeature returns the definition of the feature occupying the cell under
// the pointer, or nil [07 §8][05 "Feature instance and terrain cell"].
func (b *battleSession) hoverFeature(sx, sy int32) *content.FeatureDef {
	if b.sess.Features == nil || b.cam == nil {
		return nil
	}
	wx, _, wz := b.cursorWorld(sx, sy)
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

// isResultVisible reports whether the authoritative result overlay should be shown [RS-05][08][P1-01].
// It is presentation-only and reads the snapshot view plus the session's latch state (I6).
// The overlay is visible when the terminal result is latched (Ended) and has not been dismissed.
func (b *battleSession) isResultVisible() bool {
	if b == nil || b.sess == nil || b.battleState().Input.ResultDismissed {
		return false
	}
	if b.sess.GetResult().Ended {
		return true
	}
	if b.sess.Snapshot != nil {
		if cur := b.sess.Snapshot.Current(); cur != nil && cur.Result.Ended {
			return true
		}
	}
	if b.sess.State == session.StatePostBattle {
		return true
	}
	return false
}

// resultView returns the current authoritative result view for overlay [RS-05].
func (b *battleSession) resultView() frame.ResultView {
	if b == nil || b.sess == nil {
		return frame.ResultView{}
	}
	if b.sess.Snapshot != nil {
		if cur := b.sess.Snapshot.Current(); cur != nil && cur.Result.Ended {
			return cur.Result
		}
	}
	r := b.sess.GetResult()
	if r.Ended {
		view := frame.ResultView{
			Ended:      r.Ended,
			Kind:       r.Kind,
			WinnerTeam: r.WinnerTeam,
			Reason:     r.Reason,
			Tick:       r.Tick,
			ArmedTick:  r.ArmedTick,
			Countdown:  r.Countdown,
			Draw:       r.Draw,
		}
		if len(r.Winners) > 0 {
			view.Winners = append([]int(nil), r.Winners...)
		}
		if len(r.Losers) > 0 {
			view.Losers = append([]int(nil), r.Losers...)
		}
		if len(r.Scores) > 0 {
			view.Scores = append([]frame.ResultScore(nil), r.Scores...)
		}
		return view
	}
	return frame.ResultView{}
}

// doResultAction executes the result overlay button action through the state graph [RS-05][08 "Session states"].
func (b *battleSession) doResultAction(kind string, cl *client.Client) {
	if b == nil {
		return
	}
	switch kind {
	case "result_skirmish":
		if b.returnToSkirmish != nil {
			b.returnToSkirmish(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuSkirmish)
		} else if b.returnToMenu != nil {
			b.returnToMenu(cl)
		}
		b.battleState().Input.ResultDismissed = true
	case "result_main":
		if b.returnToMenu != nil {
			b.returnToMenu(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuMain)
		}
		b.battleState().Input.ResultDismissed = true
	case "result_continue":
		if b.continueFunc != nil {
			b.continueFunc(cl)
		} else if b.shell != nil && b.sess != nil {
			// Campaign Continue: on victory load next MISSION slot+1 [07 §11][08 "Progression"]; on defeat stay at menu [P1-01 §7.5].
			// Continue the campaign directly when no continue callback is installed.
			if b.sess.Mission != nil && b.sess.Mission.Type == mission.TypeCampaign {
				isWin := false
				if b.sess.CampaignSlot >= 0 && b.sess.CampaignSlot < len(b.sess.Progress.WL) && b.sess.Progress.WL[b.sess.CampaignSlot] == 'W' {
					isWin = true
				} else if b.sess.Latch.IsWin() {
					isWin = true
				} else if b.sess.VictoryDone && !b.sess.DefeatDone {
					isWin = true
				} else if r := b.sess.GetResult(); r.Ended && r.Kind == "victory" {
					isWin = true
				}
				if !isWin && b.sess.Mission.CampaignIndex >= 0 && b.sess.Mission.CampaignIndex < len(b.sess.Progress.WL) && b.sess.Progress.WL[b.sess.Mission.CampaignIndex] == 'W' {
					isWin = true
				}
				if isWin {
					campaignPath := b.sess.Mission.CampaignPath
					curIdx := b.sess.Mission.CampaignIndex
					if curIdx < 0 {
						curIdx = b.sess.CampaignSlot
					}
					difficulty := b.sess.Mission.Difficulty
					if difficulty < 0 && b.shell != nil {
						difficulty = b.shell.missionDifficulty()
					}
					// Ensure WL written before advancing [P1-01 §2.3].
					_ = b.sess.ContinueCampaign()
					if campaignPath != "" && curIdx >= 0 {
						if nextIdx, hasNext, err := mission.NextCampaignMission(b.fs, campaignPath, curIdx); err == nil && hasNext {
							nextPath := fmt.Sprintf("%s:MISSION%d", campaignPath, nextIdx)
							prevProgress := b.sess.Progress
							prevSlot := curIdx
							b.shell.beginLoad("", modeMenuMission, func(state *loadingState) (*session.Session, error) {
								sess2, err := session.NewMissionWithProgress(b.fs, nil, nextPath, difficulty, state.report)
								if err != nil {
									return nil, err
								}
								sess2.Progress = prevProgress
								if prevSlot >= 0 && prevSlot < len(sess2.Progress.WL) && sess2.Progress.WL[prevSlot] == 0 {
									sess2.Progress.WL[prevSlot] = 'W'
								}
								sess2.CampaignSlot = nextIdx
								return sess2, nil
							})
							b.battleState().Input.ResultDismissed = true
							return
						}
					}
					// Campaign complete or provenance missing: return to main.
					// TODO(question): retail end-of-campaign briefing/report/credits sequence not established [07 §11]; treat as menu return.
					if b.sess.State == session.StatePostBattle {
						_ = b.sess.ContinueCampaign()
					}
					b.shell.openMenu(modeMenuMain)
				} else {
					// TODO(question): losing Continue behavior beyond the authored route is not established; return to main [07 §11].
					_ = b.sess.ContinueCampaign()
					b.shell.openMenu(modeMenuMain)
				}
			} else {
				if b.sess.ContinueCampaign() {
					b.shell.openMenu(modeMenuMain)
				} else {
					b.shell.openMenu(modeMenuMain)
				}
			}
		} else if b.returnToMenu != nil {
			b.returnToMenu(cl)
		}
		b.battleState().Input.ResultDismissed = true
	}
}

// setStatusMessage stores a transient on-screen message [07 §11][07 §2] presentation-only (I6).
func (b *battleSession) setStatusMessage(msg string) {
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	b.battleState().Input.StatusMessage = msg
	// Display for 90 ticks (~3 seconds at 30 Hz) [07 §11] animation cadence; TODO(question): exact duration not established
	b.battleState().Input.StatusUntil = b.sess.Clock.GlobalTick + 90
}

// statusVisible reports whether the transient message should be drawn [07 §11].
func (b *battleSession) statusVisible() bool {
	if b == nil || b.sess == nil || b.sess.Clock == nil || b.battleState().Input.StatusMessage == "" {
		return false
	}
	// Show until expiry; if clock hasn't ticked yet, still show
	return b.sess.Clock.GlobalTick <= b.battleState().Input.StatusUntil
}

// adjustGameSpeed emits a concrete UI scheduling intent; Session performs the
// clamp and applies it at the scheduling boundary [07 §11][07 §2].
func (b *battleSession) adjustGameSpeed(delta int) {
	if b == nil || b.sess == nil {
		return
	}
	b.applyBattleSchedule(ui.SpeedIntent(delta))
}

func (b *battleSession) setGameSpeed(delta int) {
	if b == nil || b.sess == nil {
		return
	}
	newReq, changed := b.sess.AdjustSpeed(delta)
	if !changed {
		return
	}
	var msg string
	if newReq == 10 {
		msg = "Game Speed Normal" // [07 §2]
	} else {
		d := int(newReq - 10)
		// Retail formats the localized speed label with two spaces and a signed delta [07 §2].
		msg = fmt.Sprintf("Game Speed  %+d", d)
	}
	b.setStatusMessage(msg)
}

// togglePause flips the pause bit and emits retail message [07 §11] igpaused overlay is the established indicator; TODO(question): exact on-screen pause string not recovered, using "Game Paused"/"Game Resumed" as placeholder behind TODO.
func (b *battleSession) togglePause() {
	if b == nil || b.sess == nil {
		return
	}
	paused := true
	if b.sess.Clock != nil {
		paused = !b.sess.Clock.Paused
	}
	b.applyBattleSchedule(ui.PauseIntent(paused))
	var msg string
	if paused {
		msg = "Game Paused" // TODO(question): retail pause localized message not established beyond igpaused GAF [07 §11]; verify with decompile
	} else {
		msg = "Game Resumed"
	}
	b.setStatusMessage(msg)
}
