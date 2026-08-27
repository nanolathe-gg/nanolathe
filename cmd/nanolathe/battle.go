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
	"github.com/nanolathe/nanolathe/internal/snapshot"
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

	latch      input.Latch
	dragActive bool
	dragStartX int32
	dragStartY int32
	dragEndX   int32
	dragEndY   int32

	// Input-capture latch [F-P1-008][07 §3]: a press that begins on HUD chrome
	// never starts/completes world drag selection even if released over world.
	hudCaptured bool
	hudPressX   int32
	hudPressY   int32
	prevMouseX  float32
	prevMouseY  float32

	// Build placement: non-empty while an armed product awaits a click.
	buildDef   string
	buildFootX int32
	buildFootZ int32
	buildOK    bool
	buildMX    int32
	buildMY    int32
	// Site resolved by the last updatePlacement: the north-west footprint cell
	// and the ground height retail draws the ghost and stores the order at
	// [07 §9]. buildSticky is retail's placement-valid-pending bit, set by a
	// shift-click so the mode survives until shift is released.
	buildCellX  int32
	buildCellZ  int32
	buildSiteH  int32
	buildSticky bool
	// placeCaptured is the placement half of the input-capture latch above: a
	// left press the placement path consumed owns that button until it is
	// released, so the rest of a held click cannot also run a world path
	// [07 §9 "Mouse-button assignment is closed"].
	placeCaptured bool
	// shiftHeld mirrors the last polled Shift state so the overlay can gate the
	// order-queue markers on it the way retail's battle draw does [07 §9].
	// pointerX/Y mirror the last polled pointer so the overlay can gate on the
	// live region rather than on the site the ghost was last moved to.
	shiftHeld        bool
	shiftLatchSticky bool
	pointerX         int32
	pointerY         int32

	msAccum float64 // renderer delta → scaled-now for Session.Step

	// Status message for game-speed and pause feedback [07 §11][07 §2]; presentation-only transient overlay (I6).
	statusMessage string
	statusUntil   uint32 // GlobalTick expiry

	menu             battleMenuState
	menuPressed      int
	menuPressedState battleMenuState
	returnToMenu     func(*client.Client)
	returnToSkirmish func(*client.Client)
	retryFunc        func(*client.Client)
	continueFunc     func(*client.Client)
	ended            bool
	resultDismissed  bool
	resultButtons    []panelButton // buttons for result overlay [RS-05]

	anchors   hud.Anchors
	anchorsOK bool
	guiWin    *gui.Window
	guiOK     bool

	// commandDispatchFn is the typed battle/application boundary. All
	// world-mutating input is represented as a battleCommand before application.
	commandDispatchFn func(battleCommand) error
	// battleMode is the established runtime mode flag consumed by the digit
	// gate [07 §9]. The production composition currently has no separate
	// publisher for this byte, so the composition value remains zero until
	// that producer is wired (TODO(question): identify the mode-byte writer).
	battleMode byte
	controller *BattleController
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

	const winW, winH = 640, 480
	mapW := int32(terrain.CellW * 16)
	mapH := int32(terrain.CellH * 16)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: mapW, MapH: mapH}
	cam.Pan(0, 0)
	centerOnCommanderForSession(sess, cam, winW, winH)

	b := &battleSession{sess: sess, cat: cat, cam: cam, fs: cs.fs, latch: input.LatchNormal}
	// Production composition owns the typed command queue.
	b.commandDispatchFn = func(cmd battleCommand) error {
		hc, ok := b.sessionHumanCommand(cmd)
		if !ok {
			return fmt.Errorf("battle: unsupported command kind %d", cmd.Kind)
		}
		return sess.EnqueueHumanCommand(hc)
	}
	b.retryFunc = func(cl *client.Client) { b.doRetry(cl) }
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
	cl.Overlay = func(c *client.Client) { b.hud.draw(c, b) }
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
	// RS-05: result overlay takes precedence over menu and world input [08][P1-01] and
	// must not leave hidden ticks running [RS-P0-012].
	if b.isResultVisible() {
		b.handleResultInput(in, cl)
		// Do not advance simulation while result overlay is visible; Step would early-return
		// due to StatePostBattle anyway, but we skip it entirely to keep the countdown frozen
		// and to prevent the automatic 7→2 transition until the user chooses an action.
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	if in != nil && in.Kbd != nil && in.Kbd.KeyDown(input.KeyTab) {
		switch b.menu {
		case battleMenuClosed:
			b.openBattleMenu()
		case battleMenuOptions:
			b.closeBattleMenu()
		}
	}
	// ESC-menu token path [07 §2]: ESC reuses Tab menu machinery; also disarms latch as today [07 §9].
	if in != nil && in.Kbd != nil && in.Kbd.KeyDown(input.KeyEscape) && b.menu == battleMenuClosed {
		if b.latch == input.LatchNormal && b.buildDef == "" {
			b.openBattleMenu()
		} else {
			b.disarmPlacement()
			b.latch = input.LatchNormal
			b.hudCaptured = false
			b.dragActive = false
		}
	}
	if b.menu != battleMenuClosed {
		b.handleBattleMenuInput(in, cl)
	} else {
		if b.controller == nil {
			b.controller = NewBattleController(b)
		}
		b.controller.Step(BattleInputFrameFromClient(in, delta), cl)
	}
	if b.ended {
		return
	}
	if b.menu != battleMenuClosed {
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
	if b.cam != nil && b.menu == battleMenuClosed {
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
		modalActive := b.menu != battleMenuClosed
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
			dx := int32(mouse.X - b.prevMouseX)
			dy := int32(mouse.Y - b.prevMouseY)
			if dx != 0 || dy != 0 {
				b.cam.Drag(dx, dy)
			}
		}
		// Wheel zoom presentation-only, centered where practical (cursor) [F-P1-008].
		if mouse.Scrolled() && mouse.ScrollY != 0 {
			b.cam.AddZoom(mouse.ScrollY, int32(mouse.X), int32(mouse.Y))
		}
		b.prevMouseX = mouse.X
		b.prevMouseY = mouse.Y
	}
}

// isOverMinimap identifies the existing radar interaction region [07 §10].
func (b *battleSession) isOverMinimap(x, y int32) bool {
	const mmX, mmY, mmW, mmH = 540, 360, 90, 90
	return x >= mmX && x < mmX+mmW && y >= mmY && y < mmY+mmH
}

// handleInput processes selection, orders, and build placement.
// It converts input into complete canonical commands with target/position and
// queue modifiers (shift-queued) via one picking routine that respects fog,
// unit/feature overlap, and command validity [07 §9][03 §3.2] C8 [P0-I14].
// Order buttons are bound via injected dispatch [R-P0-03]; build products are
// data-driven from cat.BuildMenus; input-capture latch prevents HUD presses
// from leaking into world drag [F-P0-003][F-P1-008].
func (b *battleSession) handleInput(in *client.InputState, cl *client.Client) {
	kbd := in.Kbd
	mouse := in.Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)
	b.shiftHeld = kbd.HasShift()
	b.pointerX, b.pointerY = mx, my
	// Any latch held by Shift retires on the live Shift-up, regardless of
	// order family [R-P0-11].
	if !b.shiftHeld && b.shiftLatchSticky {
		b.latch = input.LatchNormal
		b.shiftLatchSticky = false
	}

	// Minimap click-to-jump [C-6][07 §10] using camera.Minimap math (ToCamera/ToWorld).
	// This is presentation-only and never writes sim [I6].
	if b.isOverMinimap(mx, my) {
		if mouse.Pressed(input.MouseButtonLeft) && b.cam != nil && b.sess != nil && b.sess.World != nil {
			playW := b.sess.World.PlayRight
			playH := b.sess.World.PlayBottom
			if playW <= 0 || playH <= 0 {
				playW = b.sess.World.CellW*16 - 32
				playH = b.sess.World.CellH*16 - 128
				if playW <= 0 {
					playW = b.sess.World.CellW * 16
				}
				if playH <= 0 {
					playH = b.sess.World.CellH * 16
				}
			}
			m := camera.LayoutMinimap(playW, playH) // [07 §10]
			const mmX, mmY, mmW, mmH = 540, 360, 90, 90
			// Scale HUD 90x90 to 126 canvas for letterbox math [07 §10][03 §3.6].
			canvasX := (mx - mmX) * 126 / mmW
			canvasY := (my - mmY) * 126 / mmH
			if canvasX < 0 {
				canvasX = 0
			} else if canvasX >= 126 {
				canvasX = 125
			}
			if canvasY < 0 {
				canvasY = 0
			} else if canvasY >= 126 {
				canvasY = 125
			}
			eW, eH := b.cam.EffectiveView()
			if eW <= 0 {
				eW = b.cam.ViewW
			}
			if eH <= 0 {
				eH = b.cam.ViewH
			}
			cx, cz := m.ToCamera(canvasX, canvasY, playW, playH, eW, eH) // [07 §10] C4
			b.cam.X = cx
			b.cam.Z = cz
			b.cam.Pan(0, 0)
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
		b.latch = input.LatchNormal
		b.shiftLatchSticky = false
		b.hudCaptured = false
		b.dragActive = false
		return
	}
	// Right button is deselect/cancel only: every world order fires on left [07 §9][04 §3.4].
	if mouse.Pressed(input.MouseButtonRight) {
		// A factory product button is the one right-click exception: it
		// subtracts one/five from the matching tail node [R-P0-11].
		if b.hud != nil && b.hud.hitTestFor(b, mx, my) && b.hud.consumeRightClick(b, mx, my) {
			return
		}
		if b.buildDef != "" {
			// Cancel armed placement before affecting selection [R-P0-03][F-P0-003][07 §9].
			b.disarmPlacement()
			b.hudCaptured = false
			return
		}
		if b.latch != input.LatchNormal {
			// Cancel armed order latch to idle [07 §9][07 §8][07 §9].
			b.latch = input.LatchNormal
			b.shiftLatchSticky = false
			return
		}
		if b.hasSelection() {
			// Deselect is a typed command; UI never mutates live flags [I6].
			_ = b.submitBattleCommand(battleCommand{Kind: battleCommandSelectionClear})
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
			b.hudCaptured = true
			b.hudPressX = mx
			b.hudPressY = my
			b.dragActive = false
		}
	}
	if b.hudCaptured && mouse.Released(input.MouseButtonLeft) {
		// Retail buttons arm while held and activate once on release-inside.
		// Requiring the same authored gadget at both endpoints prevents a drag
		// across the rail from activating a different control [07 §3][07 §4].
		b.hudCaptured = false
		b.dragActive = false
		if b.hud != nil && b.hud.sameButton(b, b.hudPressX, b.hudPressY, mx, my) && b.hud.consumeClick(b, mx, my) {
			return
		}
		return
	}
	if b.hudCaptured {
		return
	}

	// A left press the placement path already consumed owns that button until
	// it is released. Retail routes one press/release pair through exactly one
	// world path [07 §9]; because placement commits on the press edge and then
	// disarms, the remaining held frames of an ordinary human click used to
	// fall through to drag selection, and its release issued a contextual Move
	// that purged the build order the same click had just queued.
	if b.placeCaptured {
		if !mouse.Held(input.MouseButtonLeft) {
			b.placeCaptured = false
		}
		if b.buildDef != "" {
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
	if b.buildSticky && !kbd.HasShift() {
		b.disarmPlacement()
	}
	if b.buildDef != "" {
		b.updatePlacement(mx, my)
		if mouse.Pressed(input.MouseButtonLeft) {
			// The press belongs to placement whatever it decides below —
			// placed, refused, or rejected by the command boundary.
			b.placeCaptured = true
			if !b.buildOK {
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
				b.buildSticky = true
			} else {
				b.disarmPlacement()
			}
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
			// Small click precedence [07 §8][07 §9][RS-P0-003]: armed latch dispatches on left-click; idle latch left-click selects or issues contextual order; right-click never issues an order [04 §3.4][07 §9].
			// Uses ONE canonical picker client.PickUnit so fog, 16px radius, strict < tie (lower slot wins),
			// unit>feature priority and viewer are identical for selection and targeting [07 §9][03 §3.2][P0-I14].
			if b.latch != input.LatchNormal {
				code := hud.LatchToCode(b.latch)
				if code != 0 {
					b.orderSelected(code, mx, my, additive)
				}
				// Return latch to Normal after dispatch unless shift-queuing keeps it [07 §9][P0-I14].
				if additive {
					b.shiftLatchSticky = true
				} else {
					b.latch = input.LatchNormal
					b.shiftLatchSticky = false
				}
			} else {
				// Idle latch left-click: every world command is left-click; right-click is deselect/cancel only [07 §9][04 §3.4].
				// When clicking an own visible unit we change selection; otherwise with a selection we issue the contextual order (code 1) which delegates to move/attack/repair/etc. based on the target [04 §3.4].
				viewer := visibility.PlayerID(0)
				if b.sess != nil {
					viewer = visibility.PlayerID(b.sess.LocalOwner)
				}
				frame, ok := b.currentSnapshot()
				if !ok {
					return
				}
				var bh pool.Handle
				var bu snapshot.UnitView
				var hit bool
				// The framebuffer composer already rebases the projected world point
				// from the beam origin before drawing it. Mouse coordinates are in that
				// same logical framebuffer, so do not subtract the HUD viewport origin
				// a second time [03 §2.5][07 §8].
				shellX, shellY := mx, my
				bh, bu, hit = client.PickSnapshotUnit(frame, shellX, shellY, b.cam, uint8(viewer))
				hitOwn := hit && bh != 0 && bu.Owner == b.sess.LocalOwner
				if hitOwn {
					if additive {
						_ = b.submitBattleCommand(battleCommand{Kind: battleCommandSelectionToggle, Selection: battleSelectionCommand{Handles: []pool.Handle{bh}}})
					} else {
						_ = b.submitBattleCommand(battleCommand{Kind: battleCommandSelectionReplace, Selection: battleSelectionCommand{Handles: []pool.Handle{bh}}})
					}
				} else {
					if b.hasSelection() {
						// Left-click contextual order when a selection exists and the click is not on own unit [04 §3.4][07 §9].
						b.orderSelected(1, mx, my, additive)
					} else {
						// No selection and click not on own unit: clear if not additive, else preserve [07 §9] C6.
						if !additive {
							_ = b.submitBattleCommand(battleCommand{Kind: battleCommandSelectionClear})
						}
					}
				}
			}
		} else {
			// The drag rectangle is already in the framebuffer coordinate space
			// used by the rendered world [03 §2.5][07 §8].
			shellRect := rect
			frame, ok := b.currentSnapshot()
			if !ok {
				return
			}
			handles := client.SnapshotUnitHandlesInRect(frame, b.cam, shellRect, b.sess.LocalOwner)
			kind := battleCommandSelectionReplace
			if additive {
				kind = battleCommandSelectionToggle
			}
			_ = b.submitBattleCommand(battleCommand{Kind: kind, Selection: battleSelectionCommand{Handles: handles}})
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

func (b *battleSession) currentSnapshot() (*snapshot.Frame, bool) {
	if b == nil || b.sess == nil || b.sess.Snapshot == nil {
		return nil, false
	}
	_, cur, ok := b.sess.Snapshot.Read()
	return cur, ok && cur != nil
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
		if u != nil && u.Alive && u.Owner == owner && u.Flags&client.SelectionFlag != 0 {
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
		if u != nil && u.Alive && u.Owner == owner && u.Flags&client.SelectionFlag != 0 &&
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

// handleHudOrderButton binds named order buttons to the existing command path
// via injected dispatch [R-P0-03][07 §9]. It is used by retail HUD consumeClick.
func (b *battleSession) handleHudOrderButton(name string) {
	latch := hud.ParseButtonLatch(name, 1)
	// STOP is a distinct immediate command. It must never dispatch contextual
	// code 1 at the map origin before the Stop descriptor [04 §3.4][07 §9].
	if latch == input.LatchNormal && containsStop(name) {
		_ = b.dispatchStopCommand()
		b.latch = input.LatchNormal
		return
	}
	if latch.IsValid() {
		b.latch = latch
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
		_ = b.DispatchActivation(battleActivationCommand{
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
	b.buildDef = def.CanonicalKey
	b.buildFootX, b.buildFootZ = footprintCellsForCatalog(b.cat, def)
	b.buildOK = false
	b.buildSticky = false
	b.latch = input.LatchMobileBuild
}

// disarmPlacement returns the battle screen to the idle latch after a placement
// ends, whether it ended in a click, a cancel, or shift being released [07 §9].
func (b *battleSession) disarmPlacement() {
	if b == nil {
		return
	}
	b.buildDef = ""
	b.buildFootX, b.buildFootZ = 0, 0
	b.buildOK = false
	b.buildMX, b.buildMY = 0, 0
	b.buildCellX, b.buildCellZ = 0, 0
	b.buildSiteH = 0
	b.buildSticky = false
	if b.latch == input.LatchMobileBuild {
		b.latch = input.LatchNormal
	}
}

// playUICue plays a non-positional interface sound by its authored alias
// [07 §9][03 §8.3]. Retail's placement path plays `oktobuild` on a placed site
// and `notoktobuild` on a refused one; both are ordinary sound aliases, not a
// separate UI audio path.
func (b *battleSession) playUICue(cl *client.Client, alias string) {
	if cl == nil {
		return
	}
	cl.PlayUICue(alias)
}

// updatePlacement tracks the ghost under the cursor and validates it against
// the world [04 §6.2][PLAN_08 C17].
func (b *battleSession) updatePlacement(mx, my int32) {
	b.buildMX, b.buildMY = mx, my
	wx, _, wz := b.cursorWorld(mx, my)
	b.buildCellX, b.buildCellZ = world.PlacementAnchor(wx, wz, b.buildFootX, b.buildFootZ)
	self := uint16(0)
	if frame, ok := b.currentSnapshot(); ok {
		self = uint16(frame.CommandPage.Builder)
	}
	footX, footZ := b.buildFootX, b.buildFootZ
	var def *content.UnitDef
	if b.cat != nil {
		def, _ = b.cat.Unit(b.buildDef)
	}
	result, err := b.checkProductPlacement(b.buildCellX, b.buildCellZ, def, footX, footZ, self)
	b.buildOK = err == nil
	waterline := int32(0)
	if def != nil {
		waterline = def.Waterline
	}
	if err == nil {
		b.buildSiteH = result.SiteHeight
	} else {
		// Keep an informative ghost height while illegal; legality itself is
		// decided only by the canonical query above.
		yard, _ := world.ParseYardMap(b.yardMapFor(), int(footX), int(footZ))
		b.buildSiteH = b.sess.World.SiteHeight(b.buildCellX, b.buildCellZ, yard, int(footX), int(footZ), waterline)
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
	return b.sess.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: self, Mobile: def.BMCode})
}

// placementRect returns the armed site's footprint as a screen rectangle
// [07 §9]. Retail projects the two cell-aligned corners with the ordinary
// half-height shear, using the site height for both, so the ghost lies flat on
// the ground the building will stand on rather than following the cursor.
func (b *battleSession) placementRect() (left, top, right, bottom int32) {
	l := b.buildCellX * 16
	t := b.buildCellZ * 16
	r := l + b.buildFootX*16
	btm := t + b.buildFootZ*16
	return b.siteRectToScreen(l, t, r, btm, b.buildSiteH)
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
	if b.cat != nil && !hud.ValidateBuildProduct(b.cat, v.DefName, b.buildDef) {
		return false // GUI may not invent products absent from authored list [R-P0-03]
	}
	if !b.buildOK {
		return false // illegal placement queues nothing [R-P0-03]
	}
	// The order carries the footprint's center and the site height, not the raw
	// cursor point: retail recomputes the same cell-aligned anchor the ghost was
	// drawn on and stores `((foot + 2*cell) << 19)` per axis with the validator's
	// site height as Y [07 §9]. Sending the cursor point instead would put the
	// building half a footprint off the box the player aimed with.
	wx, wz := world.PlacementCenter(b.buildCellX, b.buildCellZ, b.buildFootX, b.buildFootZ)
	// Queue the typed command; the session applies it at the authoritative input
	// phase [01 §4.4][07 §9].
	if err := b.DispatchMobileBuild(b.buildDef, wx, wz, queued); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: build %s: %v\n", b.buildDef, err)
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
	if b.buildDef == "" || b.cam == nil {
		return
	}
	if !b.overWorld(b.pointerX, b.pointerY) {
		return
	}
	l, t, r, btm := b.placementRect()
	col := c.GUIColor(hud.GhostColorIllegal)
	if b.buildOK {
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
	latch, ok := latchForOrderCode(code)
	if !ok {
		return
	}
	_ = b.DispatchOrderCommand(battleOrderCommand{
		Latch: latch, Target: targetHandle,
		Position: *pos, Queued: queued,
	})
}

func latchForOrderCode(code int) (input.Latch, bool) {
	switch code {
	case 1:
		return input.LatchNormal, true
	case 2:
		return input.LatchMove, true
	case 3:
		return input.LatchAttack, true
	case 4:
		return input.LatchBlast, true
	case 5:
		return input.LatchUnload, true
	case 6:
		return input.LatchPickup, true
	case 7:
		return input.LatchFollow, true
	case 8:
		return input.LatchRepair, true
	case 9:
		return input.LatchPatrol, true
	case 11:
		return input.LatchTeleport, true
	case 12:
		return input.LatchReclaim, true
	case 13:
		return input.LatchCapture, true
	case 14:
		return input.LatchMobileBuild, true
	default:
		return input.LatchNormal, false
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
	sel := hud.CursorSelection{Viewer: b.sess.LocalOwner, Hostile: b.hostile}
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == b.sess.LocalOwner && u.Flags&client.SelectionFlag != 0 {
			sel.Units = append(sel.Units, u)
		}
	}
	if b.sess.Econ != nil {
		local := b.sess.LocalOwner
		sel.Metal = b.sess.Econ.Players[local].Stock[economy.Metal]
		sel.Energy = b.sess.Econ.Players[local].Stock[economy.Energy]
	}
	cursors.SetIndex(hud.ChooseCursor(b.latch, sel, hover))
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
	if b == nil || b.sess == nil || b.resultDismissed {
		return false
	}
	if b.sess.GetResult().Ended {
		return true
	}
	if b.sess.Snapshot != nil {
		if view := b.sess.Snapshot.GetResultView(); view.Ended {
			return true
		}
		if _, cur, ok := b.sess.Snapshot.Read(); ok && cur != nil && cur.Result.Ended {
			return true
		}
	}
	if b.sess.State == session.StatePostBattle {
		return true
	}
	return false
}

// resultView returns the current authoritative result view for overlay [RS-05].
func (b *battleSession) resultView() snapshot.ResultView {
	if b == nil || b.sess == nil {
		return snapshot.ResultView{}
	}
	if b.sess.Snapshot != nil {
		if view := b.sess.Snapshot.GetResultView(); view.Ended {
			return view
		}
		if _, cur, ok := b.sess.Snapshot.Read(); ok && cur != nil && cur.Result.Ended {
			return cur.Result
		}
	}
	r := b.sess.GetResult()
	if r.Ended {
		view := snapshot.ResultView{
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
			view.Scores = append([]snapshot.ResultScore(nil), r.Scores...)
		}
		return view
	}
	return snapshot.ResultView{}
}

// ensureResultButtons builds the result overlay button set [RS-05][07 §8].
func (b *battleSession) ensureResultButtons() {
	if b == nil {
		return
	}
	if len(b.resultButtons) != 0 {
		return
	}
	// Determine campaign vs skirmish via Mission type
	isCampaign := b.sess != nil && b.sess.Mission != nil && b.sess.Mission.Type == 1 // TypeCampaign
	// Centered overlay: 640x480, box 400x200 at (120,140), buttons at y=300
	y := int32(300)
	if isCampaign {
		b.resultButtons = []panelButton{
			{Name: "Retry", X: 140, Y: y, Kind: "result_retry"},
			{Name: "Continue", X: 270, Y: y, Kind: "result_continue"},
			{Name: "Main Menu", X: 400, Y: y, Kind: "result_main"},
		}
	} else {
		b.resultButtons = []panelButton{
			{Name: "Retry", X: 140, Y: y, Kind: "result_retry"},
			{Name: "Skirmish Setup", X: 270, Y: y, Kind: "result_skirmish"},
			{Name: "Main Menu", X: 400, Y: y, Kind: "result_main"},
		}
	}
}

// handleResultInput owns all input while the result overlay is visible [RS-05][07 §3].
// Buttons activate once on release-inside the same authored gadget.
func (b *battleSession) handleResultInput(in *client.InputState, cl *client.Client) {
	if b == nil || in == nil {
		return
	}
	b.ensureResultButtons()
	mx, my := int32(0), int32(0)
	if in.Mouse != nil {
		mx, my = int32(in.Mouse.X), int32(in.Mouse.Y)
	}
	// Keyboard shortcuts: R retry, S skirmish, M main, C continue, Esc main
	if in.Kbd != nil {
		if in.Kbd.KeyDown(input.KeyR) {
			b.doResultAction("result_retry", cl)
			return
		}
		if in.Kbd.KeyDown(input.KeyM) || in.Kbd.KeyDown(input.KeyEscape) {
			b.doResultAction("result_main", cl)
			return
		}
		if in.Kbd.KeyDown(input.KeyC) {
			b.doResultAction("result_continue", cl)
			return
		}
		// S for skirmish (not conflicting with other)
		if in.Kbd.KeyDown(input.KeyS) {
			b.doResultAction("result_skirmish", cl)
			return
		}
	}
	if in.Mouse != nil && in.Mouse.Released(input.MouseButtonLeft) {
		for _, btn := range b.resultButtons {
			if mx >= btn.X && mx < btn.X+panelButtonW && my >= btn.Y && my < btn.Y+panelButtonH {
				b.doResultAction(btn.Kind, cl)
				return
			}
		}
	}
}

// doResultAction executes the result overlay button action through the state graph [RS-05][08 "Session states"].
func (b *battleSession) doResultAction(kind string, cl *client.Client) {
	if b == nil {
		return
	}
	switch kind {
	case "result_retry":
		if b.retryFunc != nil {
			b.retryFunc(cl)
			b.resultDismissed = false
			b.resultButtons = nil
			return
		}
		// Recreate the session directly when no retry callback is installed.
		b.doRetry(cl)
	case "result_skirmish":
		if b.returnToSkirmish != nil {
			b.returnToSkirmish(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuSkirmish)
		} else if b.returnToMenu != nil {
			b.returnToMenu(cl)
		}
		b.resultDismissed = true
	case "result_main":
		if b.returnToMenu != nil {
			b.returnToMenu(cl)
		} else if b.shell != nil {
			b.shell.openMenu(modeMenuMain)
		}
		b.resultDismissed = true
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
							b.resultDismissed = true
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
					// TODO(question): losing Continue vs Retry distinction not established; current behavior returns to main, Retry handles same-mission reload [P1-01 §7.5].
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
		b.resultDismissed = true
	}
}

// doRetry recreates a clean session for retry without duplicate callbacks [RS-05] RS-P0-012.
func (b *battleSession) doRetry(cl *client.Client) {
	if b == nil || b.sess == nil || b.fs == nil {
		// Fallback: reset result state in place
		if b.sess != nil {
			b.sess.ResetResultForRetry()
			_ = b.sess.Retry()
		}
		b.resultDismissed = false
		b.resultButtons = nil
		return
	}
	cfg := b.sess.Skirmish
	cat := b.cat
	if cat == nil {
		cat = b.sess.Catalog
	}
	newSess, err := session.NewSkirmishWithFS(b.fs, cat, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: retry failed: %v\n", err)
		return
	}
	// Preserve callback? The new session should have no callback yet; shell will reinstall if needed.
	b.sess = newSess
	b.cat = newSess.Catalog
	b.resultDismissed = false
	b.resultButtons = nil
	centerOnCommanderForSession(newSess, b.cam, 640, 480)
	if b.shell != nil {
		b.shell.battle = b
		if cl != nil {
			cl.SetSnapshot(newSess.Snapshot)
			cl.SetTerrain(newSess.World)
		}
	}
	// Ensure battle state
	newSess.State = session.StateBattle
}

// setStatusMessage stores a transient on-screen message [07 §11][07 §2] presentation-only (I6).
func (b *battleSession) setStatusMessage(msg string) {
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	b.statusMessage = msg
	// Display for 90 ticks (~3 seconds at 30 Hz) [07 §11] animation cadence; TODO(question): exact duration not established
	b.statusUntil = b.sess.Clock.GlobalTick + 90
}

// statusVisible reports whether the transient message should be drawn [07 §11].
func (b *battleSession) statusVisible() bool {
	if b == nil || b.sess == nil || b.sess.Clock == nil || b.statusMessage == "" {
		return false
	}
	// Show until expiry; if clock hasn't ticked yet, still show
	return b.sess.Clock.GlobalTick <= b.statusUntil
}

// adjustGameSpeed clamps Clock.Requested to 1..20 and emits the retail status
// message [07 §11][07 §2].
func (b *battleSession) adjustGameSpeed(delta int) {
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	old := b.sess.Clock.Requested
	newReq := old + int32(delta)
	if newReq < 1 {
		newReq = 1
	}
	if newReq > 20 {
		newReq = 20
	}
	if newReq == old {
		return
	}
	b.sess.Clock.Requested = newReq
	// Retail updates Active immediately as well as Requested [07 §11].
	b.sess.Clock.Active = newReq
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
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	b.sess.Clock.Paused = !b.sess.Clock.Paused
	var msg string
	if b.sess.Clock.Paused {
		msg = "Game Paused" // TODO(question): retail pause localized message not established beyond igpaused GAF [07 §11]; verify with decompile
	} else {
		msg = "Game Resumed"
	}
	b.setStatusMessage(msg)
}
