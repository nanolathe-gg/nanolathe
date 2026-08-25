package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
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

	msAccum float64 // renderer delta → scaled-now for Session.Step

	panelButtons []panelButton
}

type panelButton struct {
	Name string
	X, Y int32
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

	b := &battleSession{sess: sess, cat: cat, cam: cam}
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
	cl.Overlay = func(c *client.Client) { b.drawOverlay(c, fnt) }
	fmt.Fprintln(os.Stderr, "nanolathe: battle view — drag=select right-click=order M=move B=build Esc=cancel")
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
	b.handleInput(cl.Input(), cl)
	// Authoritative budget lives in Session.Step [01 §4.2][01 §4.3]; the
	// accumulator converts renderer seconds into scaled milliseconds.
	b.msAccum += delta * 1000
	scaled := int64(b.msAccum * 30 / 1000)
	if scaled > 1<<30 {
		scaled = 1 << 30
	}
	b.sess.Step(int32(scaled))
	// Camera pan identical to Gate-1/Gate-2 caps [07 §10].
	if b.cam != nil {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := int32(delta * 1000)
		if rawDelta <= 0 {
			rawDelta = 16
		}
		const scrollSetting = 8
		if kbd.KeyHeld(input.KeyW) || kbd.KeyHeld(input.KeyUp) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
		}
		if kbd.KeyHeld(input.KeyS) || kbd.KeyHeld(input.KeyDown) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
		}
		if kbd.KeyHeld(input.KeyA) || kbd.KeyHeld(input.KeyLeft) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
		}
		if kbd.KeyHeld(input.KeyD) || kbd.KeyHeld(input.KeyRight) {
			b.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
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
func (b *battleSession) handleInput(in *client.InputState, cl *client.Client) {
	kbd := in.Kbd
	mouse := in.Mouse
	mx, my := int32(mouse.X), int32(mouse.Y)

	if kbd.KeyDown(input.KeyM) {
		b.latch = input.LatchMove
	}
	if kbd.KeyDown(input.KeyB) {
		b.armBuildPanel()
	}
	if kbd.KeyDown(input.KeyEscape) {
		b.latch = input.LatchNormal
		b.buildDef = ""
	}

	// Armed panel captures clicks before selection/drag handling.
	if len(b.panelButtons) > 0 && mouse.Pressed(input.MouseButtonLeft) && !b.dragActive {
		if my := int32(mouse.Y); my >= 480-panelButtonH-12 {
			if b.panelClick(mx, my) {
				return
			}
		}
	}

	// Build placement mode captures clicks before selection handling.
	if b.buildDef != "" {
		b.updatePlacement(mx, my)
		if mouse.Pressed(input.MouseButtonLeft) && b.buildOK {
			b.commitBuild()
			b.buildDef = ""
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
			if b.latch == input.LatchMove {
				b.orderSelected(2, mx, my, additive) // move [04 §3.4] queued via Shift [P0-I03]
				b.latch = input.LatchNormal
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

// armBuildPanel resolves the selected builder's CANBUILD page into buttons
// [02 "Build-menu catalog keys"]. Authored page.Buttons order is preserved
// verbatim — do NOT sort alphabetically [P0-I03][02 "Build-menu catalog keys"].
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
	x := int32(8)
	y := int32(480 - panelButtonH - 8)
	for _, name := range page.Buttons {
		if _, found := b.cat.Unit(name); !found {
			continue
		}
		b.panelButtons = append(b.panelButtons, panelButton{Name: name, X: x, Y: y})
		x += panelButtonW + 4
		if x > 640-panelButtonW {
			break
		}
	}
}

// panelClick handles a click against the armed build panel; returns true when
// a button was hit and placement armed.
func (b *battleSession) panelClick(mx, my int32) bool {
	for _, btn := range b.panelButtons {
		if mx >= btn.X && mx < btn.X+panelButtonW && my >= btn.Y && my < btn.Y+panelButtonH {
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
	return false
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
func (b *battleSession) commitBuild() {
	builder := b.selectedBuilder()
	if builder == nil {
		return
	}
	wx, wz := b.cam.ScreenToWorld(b.buildMX, b.buildMY)
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
	// Ensure tick/owner and GoalY are populated for determinism [04 §3.2][P0-I05].
	// QueueMobileBuild already wrote GoalX/Z and BuildDefKey; fill remaining canonical fields.
	if b.sess.World != nil && tail.GoalY == 0 {
		tail.GoalY = b.sess.World.HeightAt(wx, wz)
		if tail.GoalY == numeric.Fixed(-1) {
			tail.GoalY = 0
		}
	}
	if tail.Owner == 0 {
		tail.Owner = builder.Handle
	}
	if tail.CreationTick == 0 && b.sess.Clock != nil {
		tail.CreationTick = uint32(b.sess.Clock.GlobalTick)
	}
	// Verify site is authoritative [P0-I05]
	_ = wx
	_ = wz
}

// drawOverlay renders the build panel and placement ghost after units.
func (b *battleSession) drawOverlay(c *client.Client, fnt *formats.FNT) {
	const W = 640
	if len(b.panelButtons) > 0 {
		c.UIFillRect(0, 480-panelButtonH-16, W, panelButtonH+16, 0)
		for i, btn := range b.panelButtons {
			x, y := int(btn.X), int(btn.Y)
			c.UIFillRect(x, y, panelButtonW, panelButtonH, 12)
			c.UIFrameRect(x, y, panelButtonW, panelButtonH, 250)
			c.UIText(fnt, btn.Name, x+4, y+6, 250)
			_ = i
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
// It respects fog (local-player word), overlap (nearest squared distance wins
// with strict < tie-break so lower slot wins on equal), and validity (alive)
// [04 §3.5][07 §9][03 §3.2] C8 [P0-I03]. Feature picking is stubbed: ground pos
// is returned when no unit hit; future feature picking will use the same routine.
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
	bestHandle := pool.Handle(0)
	var bestUnit *units.Unit
	bestDist2 := int64(1 << 30)
	const pickRadiusSq = 16 * 16 // 16 pixel radius [07 §9] overlap tolerance
	for _, u := range b.sess.Units.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		if b.sess.Vis != nil {
			t := visibility.Target{
				Owner:  visibility.PlayerID(u.Owner),
				X:      u.X,
				Y:      u.Y,
				Z:      u.Z,
				Hidden: u.Flags&0x4 != 0, // cloaked bit placeholder TODO(question) [03 §3.2]
				Status: u.Flags,
			}
			if !b.sess.Vis.IsVisible(visibility.PlayerID(0), t) {
				continue // fogged: treated as absent for picking [03 §3.2] C8
			}
		}
		sxU, syU := b.cam.WorldToScreen(u.X, u.Y, u.Z)
		dx := int64(sxU) - int64(sx)
		dy := int64(syU) - int64(sy)
		dist2 := dx*dx + dy*dy
		if dist2 <= pickRadiusSq && dist2 < bestDist2 { // strict < so lower slot wins on tie [07 §9]
			bestDist2 = dist2
			bestHandle = u.Handle
			bestUnit = u
		}
	}
	if bestHandle != 0 && bestUnit != nil {
		return bestHandle, bestUnit, pos
	}
	// TODO(P0-I05): feature picking — when a feature footprint covers the clicked
	// cell and is visible, set pos.HasFeature etc. For now HasFeature false.
	return 0, nil, pos
}

// orderSelected resolves code at the clicked world position for every selected
// player unit and pushes the resulting canonical order payload [04 §3.4][04 §3.3][P0-I03].
// It computes wx/wz, picks a target handle respecting fog/overlap/validity via
// pickTarget, resolves via orders.Resolve, constructs a canonical Node via
// orders.NewNodeForOrder that writes GoalX/Y/Z, target handle, queue modifier,
// creation tick and owner, and pushes via q.Push(id, node) — never empty [P0-I03].
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
