package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/construction"
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
// selection, order latch, build panel and placement.
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
	panelButtons     []panelButton

	// Legacy fixture-only fallback state. Production battles always install
	// retailBattleHUD below; these fields remain for the small synthetic input
	// fixtures which have no mounted retail asset set.
	anchors   hud.Anchors
	anchorsOK bool
	panel     *hud.Panel
	guiWin    *gui.Window
	guiOK     bool

	// Injection points ON-09/session must bind [R-P0-03]. When nil the
	// battleSession fallback paths use construction/orders directly.
	mobileBuildFn       func(product string, wx, wz numeric.Fixed, queued bool) error
	factoryBuildFn      func(product string, queued bool) error
	factoryBuildDeltaFn func(product string, count int) error
	orderDispatchFn     func(latch input.Latch, x, y int32, queued bool)
	// commandDispatchFn is the typed battle/application boundary. All
	// world-mutating input is represented as a battleCommand before application;
	// composition does not currently bind this callback to a separate session
	// command queue, so the direct application fallback remains authoritative.
	commandDispatchFn func(battleCommand) error
	// battleMode is the established runtime mode flag consumed by the digit
	// gate [07 §9]. The production composition currently has no separate
	// publisher for this byte, so the composition value remains zero until
	// that producer is wired (TODO(question): identify the mode-byte writer).
	battleMode byte
	// requireCommandDispatch is true only for the real production composition;
	// fixture sessions may explicitly exercise legacy fallbacks.
	requireCommandDispatch bool
	controller             *BattleController
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
	b.requireCommandDispatch = true
	// Production composition owns the typed command queue. Synthetic fixtures
	// intentionally leave this nil and use their explicit fallback helpers.
	b.commandDispatchFn = func(cmd battleCommand) error {
		hc, ok := b.sessionHumanCommand(cmd)
		if !ok {
			return fmt.Errorf("battle: unsupported command kind %d", cmd.Kind)
		}
		return sess.EnqueueHumanCommand(hc)
	}
	b.retryFunc = func(cl *client.Client) { b.doRetry(cl) }
	b.returnToMenu = func(cl *client.Client) {
		// Headless battle view has no shell; just mark ended
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

// centerOnCommander pans the camera to LocalOwner's commander if present. The
// SIDEDATA commander name ends in "com" ([02 §6] side anchors table).
// Deprecated: use centerOnCommanderForSession with Session.LocalOwner [08 "Skirmish configuration"].
func centerOnCommander(w *units.World, cam *camera.Camera, winW, winH int32) {
	for _, u := range w.Iter() {
		if u != nil && u.Alive && u.Def != nil &&
			strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			cam.X = int32(u.X>>16) - winW/2
			cam.Z = int32(u.Z>>16) - winH/2
			cam.Pan(0, 0)
			return
		}
	}
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
	// Panel slide target from Space polarity [07 §6] C14: Space held slides toward -31 unless text-editor focused.
	if b.panel != nil {
		spaceHeld := cl.Input().Kbd.KeyHeld(input.KeySpace)
		b.panel.SetTarget(spaceHeld, false)
		b.panel.Step(uint32(time.Now().UnixMilli() & 0xffffffff))
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// Also handle comma/period as next/prev for headless tests (period maps to unknown but we use Insert/Delete as proxies)
	if kbd.KeyDown(input.KeyInsert) {
		b.nextBuildPage()
	}
	if kbd.KeyDown(input.KeyDelete) {
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
		if len(b.panelButtons) > 0 && b.isOverPanel(mx, my) && b.panelClickDelta(mx, my, true) {
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
		if len(b.panelButtons) > 0 && b.isOverPanel(mx, my) {
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
		if len(b.panelButtons) > 0 && b.sameFallbackButton(b.hudPressX, b.hudPressY, mx, my) {
			if b.panelClick(mx, my) {
				return
			}
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
				var bh pool.Handle
				var bu snapshot.UnitView
				var hit bool
				// The framebuffer composer already rebases the projected world point
				// from the beam origin before drawing it. Mouse coordinates are in that
				// same logical framebuffer, so do not subtract the HUD viewport origin
				// a second time [03 §2.5][07 §8].
				shellX, shellY := mx, my
				if frame, ok := b.currentSnapshot(); ok {
					bh, bu, hit = client.PickSnapshotUnit(frame, shellX, shellY, b.cam, uint8(viewer))
				} else {
					// Snapshot not yet available (e.g., initial loading frames). Fall back to live picker
					// so a click is not silently dropped. For production this still routes through the
					// typed human-command queue; for fixtures it would have taken this path anyway.
					lh, lu := client.PickUnit(shellX, shellY, b.cam, b.sess.Units, b.sess.Vis, viewer)
					bh = lh
					if lu != nil {
						bu = snapshot.UnitView{Slot: lu.Handle, Owner: lu.Owner, Flags: lu.Flags}
						hit = true
					}
				}
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
			if frame, ok := b.currentSnapshot(); ok {
				handles := client.SnapshotUnitHandlesInRect(frame, b.cam, shellRect, b.sess.LocalOwner)
				kind := battleCommandSelectionReplace
				if additive {
					kind = battleCommandSelectionToggle
				}
				_ = b.submitBattleCommand(battleCommand{Kind: kind, Selection: battleSelectionCommand{Handles: handles}})
			} else {
				// Snapshot not yet available – use live world rect as fallback. For fixtures this
				// is the established path; for production we collect handles and still dispatch
				// through the typed queue so the input boundary remains consistent.
				if b.requireCommandDispatch {
					// Collect visible handles in live world using the same shell rect and visibility.
					var liveHandles []pool.Handle
					if b.sess != nil && b.sess.Units != nil && b.cam != nil {
						for _, u := range b.sess.Units.Iter() {
							if u == nil || !u.Alive {
								continue
							}
							// Visibility check via live service (owner bypass, fog, etc.)
							if b.sess.Vis != nil {
								t := visibility.Target{Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z, Hidden: u.Flags&0x4 != 0, Status: u.Flags}
								if !b.sess.Vis.IsVisible(visibility.PlayerID(b.sess.LocalOwner), t) {
									continue
								}
							}
							sx0, sy0 := b.cam.WorldToScreen(u.X, u.Y, u.Z)
							sx, sy := sx0-camera.OriginX, sy0-camera.OriginY
							if shellRect.Contains(sx, sy) && u.Owner == b.sess.LocalOwner {
								liveHandles = append(liveHandles, u.Handle)
							}
						}
					}
					kind := battleCommandSelectionReplace
					if additive {
						kind = battleCommandSelectionToggle
					}
					_ = b.submitBattleCommand(battleCommand{Kind: kind, Selection: battleSelectionCommand{Handles: liveHandles}})
				} else {
					client.ApplyDragSelectionWorld(b.sess.Units, b.cam, shellRect, additive)
					b.filterSelectionToPlayer(b.sess.LocalOwner)
				}
			}
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

// filterSelectionToPlayer clears selection on foreign units [08 "Skirmish configuration"].
func (b *battleSession) filterSelectionToPlayer(owner uint8) {
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Owner != owner {
			u.Flags &^= client.SelectionFlag
		}
	}
}

func (b *battleSession) currentSnapshot() (*snapshot.Frame, bool) {
	if b == nil || b.sess == nil || b.sess.Snapshot == nil {
		return nil, false
	}
	_, cur, ok := b.sess.Snapshot.Read()
	return cur, ok && cur != nil
}

// selectedHandles reads only the immutable current frame in production. The
// live-pool fallback is retained for asset-free synthetic fixtures.
func (b *battleSession) selectedHandles() []pool.Handle {
	if f, ok := b.currentSnapshot(); ok {
		return append([]pool.Handle(nil), f.Selection.Handles...)
	}
	if b == nil || b.requireCommandDispatch || b.sess == nil || b.sess.Units == nil {
		return nil
	}
	out := make([]pool.Handle, 0)
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == b.sess.LocalOwner && u.Flags&client.SelectionFlag != 0 {
			out = append(out, u.Handle)
		}
	}
	return out
}

func (b *battleSession) hasSelection() bool {
	if f, ok := b.currentSnapshot(); ok {
		return len(f.Selection.Handles) != 0
	}
	if b.sess == nil || b.sess.Units == nil {
		return false
	}
	owner := b.sess.LocalOwner
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner && u.Flags&client.SelectionFlag != 0 {
			return true
		}
	}
	return false
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

// selectedFactory returns the first selected immobile builder (factory) [R-P0-03][07 §9].
func (b *battleSession) selectedFactory() *units.Unit {
	if b.sess == nil || b.sess.Units == nil {
		return nil
	}
	owner := b.sess.LocalOwner
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == owner && u.Flags&client.SelectionFlag != 0 &&
			u.Def != nil && hud.IsFactoryBuilder(u.Def) {
			return u
		}
	}
	return nil
}

// isOverPanel reports whether a screen point is over the minimal build panel
// area used by this overlay [F-P0-003]. The retail HUD uses overWorld/hitTest.
func (b *battleSession) isOverPanel(mx, my int32) bool {
	if len(b.panelButtons) == 0 {
		return false
	}
	if my < 480-panelButtonH-32 {
		return false
	}
	// Any y in the panel band is considered HUD chrome for capture.
	return true
}

func (b *battleSession) fallbackButtonAt(x, y int32) int {
	for i, btn := range b.panelButtons {
		if x >= btn.X && x < btn.X+panelButtonW && y >= btn.Y && y < btn.Y+panelButtonH {
			return i
		}
	}
	return -1
}

func (b *battleSession) sameFallbackButton(x0, y0, x1, y1 int32) bool {
	pressed := b.fallbackButtonAt(x0, y0)
	return pressed >= 0 && pressed == b.fallbackButtonAt(x1, y1)
}

// buildPageCount follows the retail DOWNLOADMENU mapping when mounted HUD
// pages are available. Synthetic fixtures fall back to the six-product page
// grouping documented by SIDEDATA's CANBUILD contract [fmt tdf] and confirmed
// by the stock <unit>N.GUI pages [07 §9].
func (b *battleSession) buildPageCount(u *units.Unit, page *content.BuildMenuPage) int {
	if u == nil || u.Def == nil || page == nil {
		return 0
	}
	if b.hud != nil {
		return b.hud.buildPageCount(u.Def)
	}
	return hud.PageCountFromButtons(len(page.Buttons), hud.RetailBuildButtonsPerPage)
}

// switchBuildPage handles digit 1..9 build page switching [07 §9] C10.
// Page number lives in flag bits 23-25 with bit 22 paged indicator [07 §9].
func (b *battleSession) switchBuildPage(digit int) {
	if frame, productionFrame := b.currentSnapshot(); productionFrame {
		if frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount == 0 || digit < 1 || digit > 9 {
			return
		}
		target := hud.ClampPage(hud.DigitToPage(digit), int(frame.CommandPage.PageCount))
		_ = b.DispatchBuildPage(target)
		return
	}
	u := b.selectedBuilder()
	if u == nil || b.cat == nil {
		return
	}
	page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]
	if !ok || page == nil || len(page.Buttons) == 0 {
		return
	}
	count := b.buildPageCount(u, page)
	if count <= 1 {
		return
	}
	// Digit 1..9 maps to page digit-1 [07 §9] C10.
	target := hud.DigitToPage(digit)
	target = hud.ClampPage(target, count)
	var dirty uint32
	// BuildPage switching validates builder identity and page count [07 §9] C10.
	su := &hud.SelectUnit{Flags: u.Flags, DefID: b.catalogDefID(u)}
	if hud.SetBuildPage(su, target, count, &dirty) {
		u.Flags = su.Flags
		b.armBuildPanel()
	}
}

// nextBuildPage advances one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) nextBuildPage() {
	if frame, productionFrame := b.currentSnapshot(); productionFrame {
		if frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
			return
		}
		target := hud.ClampPage(int(frame.CommandPage.Page)+1, int(frame.CommandPage.PageCount))
		_ = b.DispatchBuildPage(target)
		return
	}
	u := b.selectedBuilder()
	if u == nil || b.cat == nil {
		return
	}
	page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]
	if !ok || page == nil || len(page.Buttons) == 0 {
		return
	}
	count := b.buildPageCount(u, page)
	if count <= 1 {
		return
	}
	var dirty uint32
	su := &hud.SelectUnit{Flags: u.Flags, DefID: b.catalogDefID(u)}
	cur := 0
	if hud.IsPaged(u.Flags) {
		cur = hud.DecodePage(u.Flags)
	}
	cur = hud.ClampPage(cur, count)
	next := hud.ClampPage(cur+1, count)
	if next == cur {
		return
	}
	if hud.SetBuildPage(su, next, count, &dirty) {
		u.Flags = su.Flags
		b.armBuildPanel()
	}
}

// prevBuildPage goes back one page data-driven with guard [R-P0-03][07 §9] C10.
func (b *battleSession) prevBuildPage() {
	if frame, productionFrame := b.currentSnapshot(); productionFrame {
		if frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount <= 1 {
			return
		}
		target := hud.ClampPage(int(frame.CommandPage.Page)-1, int(frame.CommandPage.PageCount))
		_ = b.DispatchBuildPage(target)
		return
	}
	u := b.selectedBuilder()
	if u == nil || b.cat == nil {
		return
	}
	page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]
	if !ok || page == nil || len(page.Buttons) == 0 {
		return
	}
	count := b.buildPageCount(u, page)
	if count <= 1 {
		return
	}
	var dirty uint32
	su := &hud.SelectUnit{Flags: u.Flags, DefID: b.catalogDefID(u)}
	cur := 0
	if hud.IsPaged(u.Flags) {
		cur = hud.DecodePage(u.Flags)
	}
	cur = hud.ClampPage(cur, count)
	prev := hud.ClampPage(cur-1, count)
	if prev == cur {
		return
	}
	if hud.SetBuildPage(su, prev, count, &dirty) {
		u.Flags = su.Flags
		b.armBuildPanel()
	}
}

// dispatchMobileBuildFallback is the ON-09 fallback for mobile builds [R-P0-03][PLAN_08 C12].
func (b *battleSession) dispatchMobileBuildFallback(product string, wx, wz numeric.Fixed, queued bool) error {
	builder := b.selectedBuilder()
	if builder == nil || b.cat == nil {
		return nil
	}
	if !hud.ValidateBuildProduct(b.cat, builder.Def.CanonicalKey, product) {
		return nil
	}
	// Validate placement via ghost: if illegal, queue nothing [R-P0-03].
	// Note: callers that already validated via updatePlacement can still call; we re-validate.
	def, ok := b.cat.Unit(product)
	if !ok || def == nil {
		return nil
	}
	footX, footZ := footprintCellsForCatalog(b.cat, def)
	cx, cz := world.WorldToCell(wx), world.WorldToCell(wz)
	cx -= footX / 2
	cz -= footZ / 2
	if b.sess != nil && b.sess.World != nil {
		if _, err := b.checkProductPlacement(cx, cz, def, footX, footZ, uint16(builder.Handle)); err != nil {
			return nil // illegal -> queue nothing [R-P0-03]
		}
	}
	if !queued {
		if q := orders.QueueForUnit(builder); q != nil {
			q.PurgeUnprotected()
			q.DropLeadingAutoOps()
		}
	}
	if err := construction.QueueMobileBuild(builder, product, wx, wz, 1, b.cat); err != nil {
		return err
	}
	// Mark queued flag per [04 §3.3][P0-I14].
	if q := orders.QueueForUnit(builder); q != nil && q.LenPrimary() > 0 {
		prim := q.Primary()
		if tail := prim[len(prim)-1]; tail != nil {
			if queued {
				tail.Flags |= orders.FlagPurgeSurvivor
			} else {
				tail.Flags &^= orders.FlagPurgeSurvivor
			}
		}
	}
	return nil
}

// dispatchFactoryBuildFallback is the ON-09 fallback for factory builds [R-P0-03].
func (b *battleSession) dispatchFactoryBuildFallback(product string, queued bool) error {
	// Compatibility entry point for asset-free fixtures. The retail product
	// path below is count-based; the legacy bool has no replacement semantics.
	return b.dispatchFactoryBuildDeltaFallback(product, 1)
}

func (b *battleSession) dispatchFactoryBuildDeltaFallback(product string, count int) error {
	fac := b.selectedFactory()
	if fac == nil {
		// Fallback: any immobile builder works for synthetic tests where Builder+!CanMove encodes factory.
		fac = b.selectedBuilder()
		if fac == nil || fac.Def == nil || fac.Def.CanMove {
			return nil
		}
	}
	if b.cat != nil && fac.Def != nil && !hud.ValidateBuildProduct(b.cat, fac.Def.CanonicalKey, product) {
		return nil
	}
	return b.queueFactoryDirectDelta(fac, product, count)
}

// queueFactoryDirect queues a factory product via construction path [R-P0-03][05].
func (b *battleSession) queueFactoryDirect(fac *units.Unit, product string, queued bool) error {
	return b.queueFactoryDirectDelta(fac, product, 1)
}

// queueFactoryDirectDelta applies the signed factory button delta. Product
// clicks never purge the existing queue; positive deltas tail-coalesce and
// negative deltas cancel the tail-most matching product [R-P0-11].
func (b *battleSession) queueFactoryDirectDelta(fac *units.Unit, product string, count int) error {
	if fac == nil || b.cat == nil {
		return nil
	}
	if count > 0 {
		return construction.QueueFactoryBuild(fac, product, count, b.cat)
	}
	if count < 0 {
		return construction.CancelProductCount(fac, product, -count)
	}
	return nil
}

// armBuildPanel resolves the selected builder's CANBUILD page into buttons
// [02 "Build-menu catalog keys"]. Authored page.Buttons order is preserved
// verbatim — do NOT sort alphabetically [P0-I03][02 "Build-menu catalog keys"].
// Pagination is applied via flag bits 23-25 with bit 22 paged [07 §9] C10.
func (b *battleSession) armBuildPanel() {
	b.panelButtons = b.panelButtons[:0]
	// The production fallback rail is presentation state. Its builder, page,
	// and product slice must come from the immutable frame published by the
	// authoritative input phase; reading selectedBuilder here would cross the
	// presentation boundary and can observe a stale/mutated live flag [I6].
	if b.requireCommandDispatch {
		if b.hud != nil {
			// The mounted retail HUD owns the authored GUI rail. There is no
			// fallback geometry to refresh in this path.
			return
		}
		frame, ok := b.currentSnapshot()
		if !ok || b.cat == nil || frame.CommandPage.Builder == 0 || frame.CommandPage.PageCount == 0 {
			return
		}
		builderView, found := snapshotUnitByHandle(frame, frame.CommandPage.Builder)
		if !found || b.sess == nil || builderView.Owner != b.sess.LocalOwner || !b.snapshotBuilder(builderView) {
			return
		}
		if int(frame.CommandPage.Page) >= int(frame.CommandPage.PageCount) {
			return
		}
		builderDef, found := b.cat.Unit(builderView.DefName)
		if !found || builderDef == nil {
			return
		}
		b.appendFallbackBuildPanel(frame.CommandPage.ProductKeys)
		return
	}
	u := b.selectedBuilder()
	if u == nil || b.cat == nil {
		return
	}
	// Production uses the authored <unit>N.GUI buttons. The custom rectangle
	// fallback exists only for asset-free fixtures and must never overlap or
	// intercept a real retail rail.
	if b.hud != nil {
		return
	}
	page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]
	if !ok || page == nil {
		return
	}
	// Preserve authored order [02 "Build-menu catalog keys"] — Buttons already authored.
	// Pagination: split Buttons into pages via data-driven helper [R-P0-03][07 §9] C10.
	const buttonsPerPage = hud.RetailBuildButtonsPerPage
	count := hud.PageCountFromButtons(len(page.Buttons), buttonsPerPage)
	if count == 0 {
		count = 1
	}
	curPage := 0
	if hud.IsPaged(u.Flags) {
		curPage = hud.DecodePage(u.Flags)
	}
	curPage = hud.ClampPage(curPage, count)
	pageButtons := hud.ProductsForPage(page.Buttons, curPage, buttonsPerPage)
	b.appendFallbackBuildPanel(pageButtons)
}

// appendFallbackBuildPanel lays out the small synthetic/headless rail from an
// already-resolved product slice. Production callers pass CommandPage keys;
// fixture callers pass the authored catalog page. The slice is never sorted or
// rewritten, preserving the producer's authored order [02 "Build-menu catalog
// keys"] and making page refreshes deterministic [I1][I6].
func (b *battleSession) appendFallbackBuildPanel(productKeys []string) {
	if b == nil || b.cat == nil {
		return
	}
	// Main build buttons for this page [02 "Build-menu catalog keys"] C8 order preserved.
	x := int32(8)
	y := int32(480 - panelButtonH - 28)
	for _, name := range productKeys {
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
// a button was hit and placement armed or command issued [R-P0-03].
// Build products are data-driven from cat.BuildMenus; no hardcoded unit names
// [R-P0-03]. Mobile builders arm placement (definition retained); factories
// queue immediately via injected callback with progress/count shown thereafter
// [R-P0-03][F-P1-008]. Illegal products are rejected (queues nothing).
func (b *battleSession) panelClick(mx, my int32) bool {
	return b.panelClickDelta(mx, my, false)
}

func (b *battleSession) panelClickDelta(mx, my int32, rightClick bool) bool {
	if b == nil {
		return false
	}
	for _, btn := range b.panelButtons {
		if mx >= btn.X && mx < btn.X+panelButtonW && my >= btn.Y && my < btn.Y+panelButtonH {
			switch btn.Kind {
			case "cancel":
				if rightClick {
					return true
				}
				b.cancelSelectedProduction()
				return true
			case "onoff":
				if rightClick {
					return true
				}
				b.toggleOnOffSelected(false)
				return true
			case "stockpile":
				if rightClick {
					return true
				}
				b.stockpileSelected(false)
				return true
			case "build":
				fallthrough
			default:
				if b.cat == nil {
					return true // consume an authored button while the catalog is unavailable
				}
				def, found := b.cat.Unit(btn.Name)
				if !found || def == nil {
					return true // consumed; nothing placeable
				}
				// Data-driven guard: product must be in the selected builder's
				// authored list [R-P0-03]. Production uses the immutable command
				// page and unit view; fixture-only sessions retain the live lookup.
				if b.requireCommandDispatch {
					frame, ok := b.currentSnapshot()
					if !ok || b.cat == nil || frame.CommandPage.Builder == 0 {
						return true
					}
					builderView, found := snapshotUnitByHandle(frame, frame.CommandPage.Builder)
					if !found || b.sess == nil || builderView.Owner != b.sess.LocalOwner {
						return true
					}
					builderDef, found := b.cat.Unit(builderView.DefName)
					if !found || builderDef == nil || !hud.ValidateBuildProduct(b.cat, builderDef.CanonicalKey, def.CanonicalKey) {
						return true // consumed but not placeable (illegal product)
					}
					// The visible button may have been armed from an older frame.
					// Require the clicked product to remain in the current immutable
					// page before dispatching it; the catalog menu alone is not a
					// sufficient page identity [07 §9][I6].
					pageProduct := false
					for _, key := range frame.CommandPage.ProductKeys {
						if content.CanonicalKey(key) == content.CanonicalKey(def.CanonicalKey) {
							pageProduct = true
							break
						}
					}
					if !pageProduct {
						return true
					}
				} else if builder := b.selectedBuilder(); builder != nil && builder.Def != nil && b.cat != nil {
					if !hud.ValidateBuildProduct(b.cat, builder.Def.CanonicalKey, def.CanonicalKey) {
						return true // consumed but not placeable (illegal product)
					}
				}
				// Retail branches on the product's authored BMcode, not on the
				// builder's mobility [07 §9]: a building arms placement, and
				// anything else queues immediately.
				if !hud.ProductArmsPlacement(def) {
					_ = b.DispatchFactoryBuildDelta(def.CanonicalKey, factoryBuildDelta(b.shiftHeld, rightClick))
					return true
				}
				if rightClick {
					return true
				}
				b.armPlacement(def)
				return true
			}
		}
	}
	return false
}

// isOverPanel helper defined earlier; panelClick uses panelButtons

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
		if b.orderDispatchFn != nil {
			// For button-originated latch, route through injected dispatcher for world-click path as well
			// (tests can observe latch arming via b.latch)
		}
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
	for _, u := range b.selectedUnits() {
		if u != nil {
			_ = b.DispatchCancelProduction(u.Handle)
		}
	}
}

// toggleOnOffSelected issues Activate/Deactivate for OnOffable units [02 "Unit record"].
// OnOffable is data-driven; the command is Activate/Deactivate via orders.NewNodeForOrder [P0-I14].
func (b *battleSession) toggleOnOffSelected(queued bool) {
	for _, u := range b.selectedUnits() {
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
	for _, u := range b.selectedUnits() {
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

// isOverMinimap reports whether a screen point is over the minimap HUD interaction region [07 §10].
func (b *battleSession) isOverMinimap(x, y int32) bool { // [07 §10]
	const mmX, mmY, mmW, mmH = 540, 360, 90, 90 // HUD minimap stub region [C-6] drawn in drawOverlay
	return x >= mmX && x < mmX+mmW && y >= mmY && y < mmY+mmH
}

// drawMinimap renders terrain-minimap + fog + contacts using camera.Minimap math [07 §10] C4 [C-6].
// It uses LayoutMinimap/ToCamera/ToWorld via camera.Minimap [C-6] and is presentation-only [I6].
func (b *battleSession) drawMinimap(c *client.Client, fnt *formats.FNT) { // [07 §10][C-6]
	const mmX, mmY, mmW, mmH = 540, 360, 90, 90
	c.UIFillRect(mmX, mmY, mmW, mmH, 0)
	c.UIFrameRect(mmX, mmY, mmW, mmH, 250)
	c.UIText(fnt, "MINIMAP", mmX+2, mmY-8, 250)
	if b.sess == nil || b.sess.World == nil {
		return
	}
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
	m := camera.LayoutMinimap(playW, playH) // [07 §10] 126 letterbox
	// Build radar picture from terrain tiles [03 §3.7] 2x supersampled; baked minimap not yet wired, generate.
	var pal *palette.Tables
	// c.Palette is not directly exposed; pal stays nil fallback to nearest without ALP [03 §3.7].
	picture := render.BuildRadarPicture(b.sess.World, playW, playH, m, nil, 0, 0, pal)
	if picture == nil || picture.Bits == nil || picture.W <= 0 || picture.H <= 0 {
		// Fallback to dots only
	} else {
		// Fog: apply snapshot FogView Ch0==15 as black for unexplored [03 §3.3][03 §3.3][C-6].
		mapped := picture
		if b.sess.Snapshot != nil {
			if _, cur, ok := b.sess.Snapshot.Read(); ok && cur != nil && cur.Fog.Valid && cur.Fog.W > 0 && cur.Fog.H > 0 && len(cur.Fog.Ch0) == int(cur.Fog.W*cur.Fog.H) {
				w, h := picture.W, picture.H
				bits := make([]byte, w*h)
				copy(bits, picture.Bits)
				fw, fh := int(cur.Fog.W), int(cur.Fog.H)
				for y := 0; y < h; y++ {
					vy := y * fh / h
					if vy < 0 {
						vy = 0
					} else if vy >= fh {
						vy = fh - 1
					}
					for x := 0; x < w; x++ {
						vx := x * fw / w
						if vx < 0 {
							vx = 0
						} else if vx >= fw {
							vx = fw - 1
						}
						idxFog := vy*fw + vx
						if idxFog >= 0 && idxFog < len(cur.Fog.Ch0) && cur.Fog.Ch0[idxFog] == 15 {
							bits[y*w+x] = 0
						}
					}
				}
				mapped = &render.RadarSurface{W: w, H: h, Pitch: (w + 3) &^ 3, Bits: bits}
			}
		}
		// Draw mapped picture scaled to HUD rect via nearest [03 §3.7][C-6].
		for y := 0; y < mmH; y++ {
			srcY := y * mapped.H / mmH
			if srcY < 0 {
				srcY = 0
			} else if srcY >= mapped.H {
				srcY = mapped.H - 1
			}
			for x := 0; x < mmW; x++ {
				srcX := x * mapped.W / mmW
				if srcX < 0 {
					srcX = 0
				} else if srcX >= mapped.W {
					srcX = mapped.W - 1
				}
				idx := srcY*mapped.W + srcX
				if idx < 0 || idx >= len(mapped.Bits) {
					continue
				}
				pix := mapped.Bits[idx]
				c.UIFillRect(int(mmX+x), int(mmY+y), 1, 1, pix)
			}
		}
		// Viewport rect lens 1-pixel [07 §10][03 §3.9] clipped to HUD.
		if b.cam != nil {
			eW, eH := b.cam.EffectiveView()
			if eW <= 0 {
				eW = b.cam.ViewW
			}
			if eH <= 0 {
				eH = b.cam.ViewH
			}
			if eW < 1 {
				eW = 1
			}
			if eH < 1 {
				eH = 1
			}
			rx0, ry0 := m.WorldToRadar(b.cam.X, b.cam.Z, playW, playH)
			rx1, ry1 := m.WorldToRadar(b.cam.X+eW-1, b.cam.Z+eH-1, playW, playH)
			if rx0 > rx1 {
				rx0, rx1 = rx1, rx0
			}
			if ry0 > ry1 {
				ry0, ry1 = ry1, ry0
			}
			if rx0 < 0 {
				rx0 = 0
			}
			if ry0 < 0 {
				ry0 = 0
			}
			if rx1 >= int32(m.W) {
				rx1 = int32(m.W - 1)
			}
			if ry1 >= int32(m.H) {
				ry1 = int32(m.H - 1)
			}
			hx0 := mmX + int(rx0)*mmW/int(m.W)
			hy0 := mmY + int(ry0)*mmH/int(m.H)
			hx1 := mmX + int(rx1)*mmW/int(m.W)
			hy1 := mmY + int(ry1)*mmH/int(m.H)
			if hx0 < mmX {
				hx0 = mmX
			}
			if hy0 < mmY {
				hy0 = mmY
			}
			if hx1 >= mmX+mmW {
				hx1 = mmX + mmW - 1
			}
			if hy1 >= mmY+mmH {
				hy1 = mmY + mmH - 1
			}
			col := byte(250)
			for x := hx0; x <= hx1; x++ {
				c.UIFillRect(x, hy0, 1, 1, col)
				c.UIFillRect(x, hy1, 1, 1, col)
			}
			for y := hy0; y <= hy1; y++ {
				c.UIFillRect(hx0, y, 1, 1, col)
				c.UIFillRect(hx1, y, 1, 1, col)
			}
		}
	}
	// Contacts via RadarProjection [03 §3.9][07 §10] using same minimap math [C-6].
	if b.sess.Units != nil && playW > 0 && playH > 0 {
		m2 := camera.LayoutMinimap(playW, playH)
		for _, u := range b.sess.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			viewer := visibility.PlayerID(b.sess.LocalOwner)
			if b.sess.Vis != nil {
				t := visibility.Target{Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z, Status: u.Flags}
				if !b.sess.Vis.IsVisible(viewer, t) && u.Owner != b.sess.LocalOwner {
					continue
				}
			}
			wx := int32(u.X >> 16)
			wz := int32(u.Z >> 16)
			wy := int32(u.Y >> 16)
			rx, ry := m2.WorldToRadarWithY(wx, wy, wz, playW, playH)
			px := mmX + int(rx)*mmW/int(m2.W)
			py := mmY + int(ry)*mmH/int(m2.H)
			if px < mmX || px >= mmX+mmW || py < mmY || py >= mmY+mmH {
				continue
			}
			col := byte(100)
			if u.Owner == b.sess.LocalOwner {
				col = 250
			} else if b.sess.Vis != nil && b.sess.Vis.IsVisible(viewer, visibility.Target{Owner: visibility.PlayerID(u.Owner), X: u.X, Y: u.Y, Z: u.Z}) {
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

// cursorWorld is the one cursor-to-ground conversion the battle screen uses
// [07 §8]. The camera's inverse alone assumes height zero, but every world
// object is drawn with the half-height shear, so on raised ground that inverse
// lands north of the pixel the player clicked — an order given at the foot of a
// hill puts the unit partway up it. Terrain.CursorToWorld runs retail's search
// along Z to find the ground whose sheared projection is the clicked row, and
// returns the height there as Y.
// It clamps the pointer into the battle viewport before ground resolution per
// [07 §8] step 1. The framebuffer world pass is rebased from the beam origin,
// so the inverse adds that origin back before resolving terrain [03 §2.5].
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
	if b.requireCommandDispatch {
		if frame, ok := b.currentSnapshot(); ok {
			self = uint16(frame.CommandPage.Builder)
		}
	} else if u := b.selectedBuilder(); u != nil {
		self = uint16(u.Handle)
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
	var builder *units.Unit
	builderKey := ""
	if b.requireCommandDispatch {
		frame, ok := b.currentSnapshot()
		if !ok || frame.CommandPage.Builder == 0 {
			return false
		}
		v, found := snapshotUnitByHandle(frame, frame.CommandPage.Builder)
		if !found || v.Owner != b.sess.LocalOwner || !b.snapshotBuilder(v) {
			return false
		}
		builderKey = v.DefName
	} else {
		builder = b.selectedBuilder()
		if builder == nil {
			return false
		}
		builderKey = builder.Def.CanonicalKey
	}
	if b.cat != nil && !hud.ValidateBuildProduct(b.cat, builderKey, b.buildDef) {
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
	wy := numeric.Fixed(int64(b.buildSiteH) << 16)
	// Use injected dispatch with fallback [R-P0-03][ON-09]
	if err := b.DispatchMobileBuild(b.buildDef, wx, wz, queued); err != nil {
		fmt.Fprintf(os.Stderr, "nanolathe: build %s: %v\n", b.buildDef, err)
		return false
	}
	// Ensure canonical fields populated for determinism [04 §3.2][P0-I05][P0-I03].
	// The queue helper builds its node from the site alone, so the height, the
	// owning builder and the creation tick are stamped here — for every builder
	// the click ordered, not just the first. The creation tick is what the site
	// marker measures its sweep from [07 §9]; an unstamped order simply renders
	// at rest.
	// Synthetic fixtures may stamp their live queue for legacy assertions. The
	// production frame path must leave queue mutation to the authoritative input
	// phase [I6].
	if _, productionFrame := b.currentSnapshot(); !productionFrame && b.sess != nil && b.sess.Units != nil {
		for _, u := range b.selectedUnits() {
			q := orders.QueueForUnit(u)
			if q == nil || q.LenPrimary() == 0 {
				continue
			}
			prim := q.Primary()
			tail := prim[len(prim)-1]
			if tail == nil || tail.BuildDefKey != content.CanonicalKey(b.buildDef) {
				continue
			}
			if tail.GoalY == 0 {
				tail.GoalY = wy
			}
			if tail.Owner == 0 {
				tail.Owner = u.Handle
			}
			if tail.CreationTick == 0 && b.sess.Clock != nil {
				tail.CreationTick = b.sess.Clock.GlobalTick
			}
		}
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
			label := btn.Name
			if frame, ok := b.currentSnapshot(); ok {
				if count := hud.QueueCountLabel(frame.OrderQueues, btn.Name); count != "" {
					label += " " + count
				}
			}
			c.UIText(fnt, label, x+4, y+6, 250)
		}
		// Latch indicator and page hint [07 §9][P0-I14].
		latchText := "Latch: " + b.latch.String()
		c.UIText(fnt, latchText, 500, baseY+4, 250)
		if b.requireCommandDispatch {
			if frame, ok := b.currentSnapshot(); ok && frame.CommandPage.Builder != 0 && frame.CommandPage.PageCount > 1 {
				cur := hud.ClampPage(int(frame.CommandPage.Page), int(frame.CommandPage.PageCount))
				pg := fmt.Sprintf("Page %d/%d (1..9)", cur+1, frame.CommandPage.PageCount)
				c.UIText(fnt, pg, 500, baseY+20, 250)
			}
		} else if u := b.selectedBuilder(); u != nil && b.cat != nil {
			if page, ok := b.cat.BuildMenus[u.Def.CanonicalKey]; ok && page != nil {
				const bpp = hud.RetailBuildButtonsPerPage
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
	b.drawBuildGhost(c)
	// Resource bars via anchors [02 §6][07 §6][P0-I14]: ENERGYBAR/METALBAR filled left-to-right [01 §8].
	// TODO(P1): full HUD uses all 30 anchors with SHD lookup, fog composer and ten-layer draw [07 §6][GAP T22].
	if b.anchorsOK && b.sess != nil && b.sess.Econ != nil {
		// Local player stocks are float32 metal/energy [05 "Player slot"] I2 allowlist.
		p := b.sess.Econ.Players[b.sess.LocalOwner]
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
		p := b.sess.Econ.Players[b.sess.LocalOwner]
		c.UIText(fnt, fmt.Sprintf("M:%d E:%d", int(p.Stock[economy.Metal]), int(p.Stock[economy.Energy])), 4, 14, 250)
	}
	// Minimap: terrain-minimap + fog + contacts using camera.Minimap math [07 §10] C4 [C-6]; click-to-jump wired in handleInput [C-6].
	// TODO(P1): panel anchors for minimap HUD rect; this uses a fixed 90x90 stub region [C-6] with 126 letterbox scaling [07 §10].
	b.drawMinimap(c, fnt)
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
	// Transient game-speed and pause messages [07 §11][07 §2] presentation-only (I6)
	if b.statusVisible() && fnt != nil {
		w := client.MeasureText(fnt, b.statusMessage)
		x := (640 - w) / 2
		c.UIText(fnt, b.statusMessage, x, 30, 15)
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
			// Fallback via live instance map for sparse features not in FeatureDefs (synthetic terrain).
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
// It uses the same band the click path treats as panel, so the shape and the
// click destination cannot disagree. Unified on drawn-chrome layout [C-3][07 §6][07 §8].
func (b *battleSession) overWorld(x, y int32) bool {
	if b.hud != nil {
		return b.hud.overWorld(x, y)
	}
	if len(b.panelButtons) > 0 && y >= 480-panelButtonH-32 {
		return false
	}
	// Fallback uses drawn-chrome viewport [C-3][07 §8] via ViewportTransform's viewport rect.
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
		// Fallback: direct session retry via clean recreation
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
			// This is the fallback when no continueFunc was installed (e.g. synthetic battleSession); production path uses gameShell.continueFunc.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	b.sess.Clock.Active = newReq
	var msg string
	if newReq == 10 {
		msg = "Game Speed Normal" // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	} else {
		d := int(newReq - 10)
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
