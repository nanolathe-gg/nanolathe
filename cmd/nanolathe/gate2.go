// Package main — Gate-2 walker slice wiring.
//
// Composition root for PHASES Gate 2 ("6 + 7-stub"): rubber-band select
// follows the [07 §9] truth table, click issues Move_Ground through the
// latch, a spawned armflea interpolates smoothly between 30 Hz sim ticks,
// pausing holds alpha (verifies existing frame.go clamping, no fix needed
// when prev==cur). A* is deliberately stubbed as straight lerp per PHASES
// playable-gate table; WU-07-7 replaces internal/movement with real A*,
// collision, and occupancy.
//
// Registration respects I7's same-tick ordering narrative
//
//	unit update → ... → orders/build → movement integration
//
// via the twelve phases of [01 §4.4]:
//
//	PhaseUnitsScripts (phase 2) for units.Tick,
//	PhaseOrdersPathEconomy (phase 5) for orders pump then movement,
//	PhaseCadenceFlip (phase 12) for snapshot publish (C15).
package main

import (
	"fmt"
	"os"
	"time"

	"kaijuengine.com/bootstrap"
	"kaijuengine.com/platform/hid"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// gate2Session is the Gate-2 composition root. It owns the kernel, clock,
// world, mover, snapshot buffer and camera, and is the single writer of
// snapshot.Frame.Units per WORK_UNITS serialization.
type gate2Session struct {
	catalog    *content.Catalog
	terrain    *world.Terrain
	world      *units.World
	kernel     *kernel.Kernel
	clock      *clock.State
	buffer     *snapshot.Buffer
	mover      *movement.Mover
	cam        *camera.Camera
	latch      input.Latch
	dragActive bool
	dragStartX int32
	dragStartY int32
	dragEndX   int32
	dragEndY   int32
	accum      float64 // viewer delta accumulator for 30 Hz ticks
}

// newGate2Session builds a fresh Gate-2 world, spawns one armflea near the
// map center, and registers the twelve-phase callbacks [01 §4.4] (I7).
func newGate2Session(cat *content.Catalog, terrain *world.Terrain, buf *snapshot.Buffer, cam *camera.Camera) (*gate2Session, error) {
	if cat == nil || terrain == nil || buf == nil || cam == nil {
		return nil, fmt.Errorf("gate2: nil catalog/terrain/buffer/cam")
	}
	w := units.New(600, cat)
	def, ok := cat.Unit("armflea")
	if !ok || def == nil {
		return nil, fmt.Errorf("gate2: armflea def not found")
	}
	// Map center in map pixels [03 §2.1] → world Fixed 16.16.
	cx := int32(terrain.CellW * 8) // (CellW*16)/2
	cz := int32(terrain.CellH * 8)
	x := numeric.Fixed(int64(cx) << 16)
	z := numeric.Fixed(int64(cz) << 16)
	y := terrain.HeightAt(x, z)
	if _, err := w.Create(def, 0, x, y, z); err != nil {
		return nil, fmt.Errorf("gate2: spawn armflea: %w", err)
	}
	k := &kernel.Kernel{}
	clk := &clock.State{Requested: 10, Active: 10}
	mover := &movement.Mover{World: w}
	// [01 §4.4] twelve phases. Registration order within a phase is call order (I1/I7).
	// Phase 2 (units-scripts): unit sweep [01 §4.4] I7 unit update.
	k.Register(kernel.PhaseUnitsScripts, "units-tick", func(tick uint32) {
		w.Tick(tick)
	})
	// Phase 5 (orders-path-economy): orders/build work then movement integration [GAP T15] I7.
	// Pump before mover so a push this tick is visible to the mover this same tick via the
	// same-tick window (deferred before normal drain vs immediate).
	k.Register(kernel.PhaseOrdersPathEconomy, "orders-pump", func(tick uint32) {
		for _, u := range w.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			q := orders.QueueForUnit(u)
			q.Pump(u, tick)
		}
	})
	k.Register(kernel.PhaseOrdersPathEconomy, "movement-straight", func(tick uint32) {
		mover.Tick(tick)
	})
	// Phase 12 (cadence-flip): snapshot publish after all phases [01 §4.4] C15.
	k.Register(kernel.PhaseCadenceFlip, "snapshot-publish", func(tick uint32) {
		views := make([]snapshot.UnitView, 0, w.Used())
		for _, u := range w.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			views = append(views, snapshot.UnitView{
				Slot:           u.Handle,
				Owner:          u.Owner,
				X:              u.X,
				Y:              u.Y,
				Z:              u.Z,
				Heading:        0,
				Pitch:          0,
				Bank:           0,
				Health:         u.Health,
				MaxHealth:      u.MaxHealth,
				BuildRemaining: u.Remaining,
				Flags:          u.Flags,
			})
		}
		buf.Publish(&snapshot.Frame{Tick: tick, Units: views})
	})
	// Initial publish so renderer interpolates from a valid Previous→Current pair.
	views := make([]snapshot.UnitView, 0, w.Used())
	for _, u := range w.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		views = append(views, snapshot.UnitView{
			Slot:           u.Handle,
			Owner:          u.Owner,
			X:              u.X,
			Y:              u.Y,
			Z:              u.Z,
			Health:         u.Health,
			MaxHealth:      u.MaxHealth,
			BuildRemaining: u.Remaining,
			Flags:          u.Flags,
		})
	}
	buf.Publish(&snapshot.Frame{Tick: clk.GlobalTick, Units: views})
	return &gate2Session{
		catalog: cat,
		terrain: terrain,
		world:   w,
		kernel:  k,
		clock:   clk,
		buffer:  buf,
		mover:   mover,
		cam:     cam,
		latch:   input.LatchNormal,
	}, nil
}

// hasSelection reports whether any alive unit is selected [07 §9] 0x10.
func (g *gate2Session) hasSelection() bool {
	if g == nil || g.world == nil {
		return false
	}
	for _, u := range g.world.Iter() {
		if u != nil && u.Alive && u.Flags&client.SelectionFlag != 0 {
			return true
		}
	}
	return false
}

// issueMoveTo resolves Move_Ground via code 2 and pushes it for each selected
// unit [07 §9][GAP T22] C5. It uses orders.Resolve(2, ...) then
// QueueForUnit.Push, and Pump will run in the orders/build window next tick.
func (g *gate2Session) issueMoveTo(wx, wz numeric.Fixed) {
	if g == nil || g.world == nil {
		return
	}
	for _, u := range g.world.Iter() {
		if u == nil || !u.Alive || u.Flags&client.SelectionFlag == 0 {
			continue
		}
		// Ground click pos. Resolve with code 2 (Move) per [GAP T22] LatchMove=2
		// and the [04 §3.4] resolver. For Gate 2 we synthesize a ground pos;
		// features/wrecks are absent so HasFeature stays false.
		pos := &orders.ResolvePos{X: wx, Z: wz, Y: g.terrain.HeightAt(wx, wz)}
		id := orders.Resolve(2, u, nil, pos)
		if id == 0 {
			id = orders.Lookup("Move_Ground")
		}
		if id == 0 {
			continue
		}
		q := orders.QueueForUnit(u)
		// Node carries the goal; other fields are defaults.
		n := orders.Node{GoalX: wx, GoalZ: wz, GoalY: pos.Y}
		q.Push(id, n)
	}
}

// handleInput polls Kaiju HID at the presentation boundary and translates it
// into selection (rubber-band truth table [07 §9] C6) and latch-armed move
// dispatch. It is called once per viewer Step before the tick budget so that
// newly pushed orders are visible to Pump this same tick via the Phase 1
// ingress window [01 §4.4] I7.
func (g *gate2Session) handleInput(host interface {
	// minimal host shape we need; use concrete *engine.Host via type assert in caller
}) {
	// The concrete handling is done in viewerStep where we have *engine.Host.
	// This no-op keeps the method for future headless injection.
	_ = g
	_ = host
}

// viewerStep is the injected client.Options.Step callback [PLAN_04A C9].
// It handles input, advances the 30 Hz tick budget, runs the kernel, and
// handles camera pan. It never mutates presentation state beyond the snapshot
// publish already performed inside the kernel's final phase.
func (g *gate2Session) viewerStep(delta float64, cl *client.Client) {
	if g == nil || cl == nil {
		return
	}
	host := cl.Host()
	// Input handling before ticks so pushes land before Pump.
	if host != nil && host.Window != nil {
		kbd := &host.Window.Keyboard
		mouse := &host.Window.Mouse
		// Latch arming: M arms Move [GAP T22] LatchMove=2. Esc clears.
		if kbd.KeyDown(hid.KeyboardKeyM) {
			g.latch = input.LatchMove
		}
		if kbd.KeyDown(hid.KeyboardKeyEscape) {
			g.latch = input.LatchNormal
		}
		mx := int32(mouse.X)
		my := int32(mouse.Y)
		leftHeld := mouse.Held(hid.MouseButtonLeft)
		additive := kbd.HasShift() // [07 §9] toggle modifier

		if leftHeld && !g.dragActive {
			g.dragActive = true
			g.dragStartX = mx
			g.dragStartY = my
			g.dragEndX = mx
			g.dragEndY = my
		} else if leftHeld && g.dragActive {
			g.dragEndX = mx
			g.dragEndY = my
		} else if !leftHeld && g.dragActive {
			g.dragActive = false
			rect := client.NormalizeRect(g.dragStartX, g.dragStartY, g.dragEndX, g.dragEndY)
			w := rect.MaxX - rect.MinX
			h := rect.MaxY - rect.MinY
			if w < 3 && h < 3 {
				// Click: if latch is Move, issue Move_Ground at click point.
				if g.latch == input.LatchMove {
					wx, wz := g.cam.ScreenToWorld(mx, my)
					g.issueMoveTo(wx, wz)
					g.latch = input.LatchNormal
				} else if mouse.Held(hid.MouseButtonRight) {
					// no-op; right click without latch does nothing for strict latch decode.
				} else {
					// Small click without drag and without latch: no selection change; keep latch.
				}
			} else {
				_, _ = client.ApplyDragSelectionWorld(g.world, g.cam, rect, additive)
			}
		}
		// Right click as Move when latch armed or when selection exists (ergonomic fallback).
		if mouse.Pressed(hid.MouseButtonRight) {
			if g.latch == input.LatchMove || g.hasSelection() {
				wx, wz := g.cam.ScreenToWorld(mx, my)
				g.issueMoveTo(wx, wz)
				g.latch = input.LatchNormal
			}
		}
		// Also handle latch-armed left click via Pressed (in case drag threshold not met and dragActive already cleared)
		if mouse.Pressed(hid.MouseButtonLeft) && g.latch == input.LatchMove && !g.dragActive {
			// This path catches a quick click where dragActive was not yet set (Pressed is the Down edge).
			// Use current mouse pos.
			wx, wz := g.cam.ScreenToWorld(mx, my)
			g.issueMoveTo(wx, wz)
			g.latch = input.LatchNormal
		}
	}
	// Advance 30 Hz tick budget: delta seconds at 30 ticks/sec, clamp 0..5 [01 §4.2].
	g.accum += delta
	ticks := int(g.accum * 30.0)
	if ticks < 0 {
		ticks = 0
	}
	if ticks > 5 {
		ticks = 5
	}
	if ticks > 0 {
		// keep carry as remainder [01 §4.2] float32 carry
		g.accum -= float64(ticks) / 30.0
		for i := 0; i < ticks; i++ {
			g.kernel.SubTick(g.clock)
		}
	}
	// Camera pan: WASD + edge [07 §10] C2/C3. Do per-frame, not per-tick.
	if host != nil && host.Window != nil && g.cam != nil {
		kbd := &host.Window.Keyboard
		mouse := &host.Window.Mouse
		rawDelta := int32(delta * 1000)
		if rawDelta < 0 {
			rawDelta = 0
		}
		if rawDelta == 0 {
			rawDelta = 16
		}
		const scrollSetting = 8
		if kbd.KeyHeld(hid.KeyboardKeyW) || kbd.KeyHeld(hid.KeyboardKeyUp) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
		}
		if kbd.KeyHeld(hid.KeyboardKeyS) || kbd.KeyHeld(hid.KeyboardKeyDown) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
		}
		if kbd.KeyHeld(hid.KeyboardKeyA) || kbd.KeyHeld(hid.KeyboardKeyLeft) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
		}
		if kbd.KeyHeld(hid.KeyboardKeyD) || kbd.KeyHeld(hid.KeyboardKeyRight) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
		}
		const edge = 8
		w := host.Window.Width()
		h := host.Window.Height()
		if w > 0 && h > 0 {
			if mouse.X < edge {
				g.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
			} else if mouse.X > float32(w-edge) {
				g.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
			}
			if mouse.Y < edge {
				g.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
			} else if mouse.Y > float32(h-edge) {
				g.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
			}
		}
	}
}

// runGate2Viewer opens the Kaiju window and draws real terrain with the
// Gate-2 walker slice: one armflea at map center, rubber-band select,
// latch-armed Move_Ground, straight-line mover, and interpolated presentation.
func runGate2Viewer(opts Options, cs *contentSet) error {
	cat, err := content.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("nanolathe: catalog: %w", err)
	}
	terrain, err := world.Load(cs.fs, cat, opts.Map)
	if err != nil {
		return fmt.Errorf("nanolathe: terrain %q: %w", opts.Map, err)
	}
	var pal *palette.Tables
	if p, err := palette.Load(cs.fs); err == nil {
		pal = p
	} else {
		fmt.Fprintf(os.Stderr, "nanolathe: palette: %v (using fallback)\n", err)
	}
	var fnt *formats.FNT
	for _, name := range []string{"fonts/smlfont.fnt", "fonts/armfont.fnt", "fonts/hatt12.fnt"} {
		if data, err := cs.fs.ReadFileLimit(name, 1<<20); err == nil {
			if parsed, err := formats.LoadFNT(data); err == nil {
				fnt = parsed
				break
			}
		}
	}
	const winW, winH = 640, 480
	mapW := int32(terrain.CellW * 16)
	mapH := int32(terrain.CellH * 16)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: int32(winW), ViewH: int32(winH), MapW: mapW, MapH: mapH}
	cam.Pan(0, 0)
	buf := &snapshot.Buffer{}
	sess, err := newGate2Session(cat, terrain, buf, cam)
	if err != nil {
		return err
	}
	// Center camera on the spawned armflea initially.
	if len(sess.world.Iter()) > 0 {
		u := sess.world.Iter()[0]
		// World pixels = Fixed>>16
		px := int32(u.X >> 16)
		pz := int32(u.Z >> 16)
		cam.X = px - int32(winW/2)
		cam.Z = pz - int32(winH/2)
		cam.Pan(0, 0)
	}
	// Keep a pointer so the Step closure can capture the client after creation.
	var clPtr *client.Client
	step := func(delta float64) {
		if clPtr == nil {
			// Before Launch, just tick without input.
			sess.accum += delta
			ticks := int(sess.accum * 30.0)
			if ticks < 0 {
				ticks = 0
			}
			if ticks > 5 {
				ticks = 5
			}
			if ticks > 0 {
				sess.accum -= float64(ticks) / 30.0
				for i := 0; i < ticks; i++ {
					sess.kernel.SubTick(sess.clock)
				}
			}
			return
		}
		sess.viewerStep(delta, clPtr)
	}
	copts := client.Options{
		Buffer:   buf,
		Width:    winW,
		Height:   winH,
		Title:    "Nanolathe — " + opts.Map + " (Gate2)",
		Headless: false,
		Step:     step,
	}
	cl, err := client.New(copts)
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	clPtr = cl
	cl.SetTerrain(terrain)
	cl.SetCamera(cam)
	if pal != nil {
		cl.SetPalette(pal)
	}
	if fnt != nil {
		cl.SetFNT(fnt)
	}
	if _, err := cl.ContentDatabase(); err != nil {
		return fmt.Errorf("nanolathe: viewer: content database %q: %w (run `make kaiju-content`)", "content", err)
	}
	fmt.Fprintf(os.Stderr, "nanolathe: gate2 viewer: opening window %dx%d for map %q (%dx%d cells) armflea@%d,%d M=arm Move Shift=add drag=select right-click=move\n", winW, winH, opts.Map, terrain.CellW, terrain.CellH, cam.X, cam.Z)
	var platformState interface{}
	bootstrap.Main(cl, platformState)
	fmt.Fprintf(os.Stderr, "nanolathe: gate2 viewer: window closed host %v final tick %d rng sim draws %d\n", cl.Host(), sess.clock.GlobalTick, rng.Global.Sim.Draws())
	if cl.Host() == nil {
		return fmt.Errorf("nanolathe: viewer: window never opened (host is nil) — check MoltenVK at runtime (DYLD_LIBRARY_PATH) and that content/ exists")
	}
	return nil
}

// runGate2Headless runs a deterministic headless simulation for --headless
// --ticks N. Same seed ⇒ identical final positions and RNG draw counts [01 §7.1] I4.
// It also synthesizes one Move_Ground order at start so the walker actually walks
// for the determinism check; without it the final position would trivially stay at
// the spawn and the mover would not be exercised.
func runGate2Headless(opts Options, cs *contentSet, out *os.File) error {
	cat, err := content.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("nanolathe: catalog: %w", err)
	}
	terrain, err := world.Load(cs.fs, cat, opts.Map)
	if err != nil {
		return fmt.Errorf("nanolathe: terrain %q: %w", opts.Map, err)
	}
	mapW := int32(terrain.CellW * 16)
	mapH := int32(terrain.CellH * 16)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: mapW, ViewH: mapH, MapW: mapW, MapH: mapH}
	buf := &snapshot.Buffer{}
	sess, err := newGate2Session(cat, terrain, buf, cam)
	if err != nil {
		return err
	}
	// Synthesize selection + Move_Ground for determinism: select the armflea
	// and push a Move_Ground to a deterministic offset so the straight-line
	// mover is exercised headless. Use the truth table for selection and the
	// latch path for order issuance.
	if len(sess.world.Iter()) > 0 {
		u := sess.world.Iter()[0]
		// Select via flags (equivalent to a drag that contains its projection).
		u.Flags |= client.SelectionFlag
		// Deterministic goal: 200 pixels east, 100 south from spawn.
		goalX := u.X + numeric.Fixed(200*65536)
		goalZ := u.Z + numeric.Fixed(100*65536)
		goalY := terrain.HeightAt(goalX, goalZ)
		pos := &orders.ResolvePos{X: goalX, Y: goalY, Z: goalZ}
		id := orders.Resolve(2, u, nil, pos) // code 2 Move [04 §3.4][GAP T22]
		if id == 0 {
			id = orders.Lookup("Move_Ground")
		}
		if id != 0 {
			n := orders.Node{GoalX: goalX, GoalZ: goalZ, GoalY: goalY}
			q := orders.QueueForUnit(u)
			q.Push(id, n)
		}
	}
	ticks := opts.Ticks
	if ticks <= 0 {
		ticks = 60 // default 2 seconds at 30 Hz if no explicit budget
	}
	if ticks > 10000 {
		ticks = 10000
	}
	startSimDraws := uint64(0)
	startCrtDraws := uint64(0)
	if rng.Global.Sim != nil {
		startSimDraws = rng.Global.Sim.Draws()
	}
	if rng.Global.Crt != nil {
		startCrtDraws = rng.Global.Crt.Draws()
	}
	for i := 0; i < ticks; i++ {
		sess.kernel.SubTick(sess.clock)
	}
	endSimDraws := uint64(0)
	endCrtDraws := uint64(0)
	if rng.Global.Sim != nil {
		endSimDraws = rng.Global.Sim.Draws()
	}
	if rng.Global.Crt != nil {
		endCrtDraws = rng.Global.Crt.Draws()
	}
	// Report final positions and draw counts for determinism verification.
	for _, u := range sess.world.Iter() {
		fmt.Fprintf(out, "gate2 unit %d owner %d x=%d z=%d y=%d flags=%x health=%d/%d\n", u.Handle, u.Owner, int64(u.X), int64(u.Y), int64(u.Z), u.Flags, u.Health, u.MaxHealth)
	}
	fmt.Fprintf(out, "gate2 ticks %d globalTick %d simDraws %d->%d crtDraws %d->%d seed sim=%d crt=%d\n",
		ticks, sess.clock.GlobalTick, startSimDraws, endSimDraws, startCrtDraws, endCrtDraws, sess.clock.GlobalTick, sess.clock.GlobalTick)
	// Also dump snapshot for presentation verification.
	if prev, cur, ok := buf.Read(); ok && cur != nil && prev != nil {
		fmt.Fprintf(out, "gate2 snapshot prevTick %d curTick %d units %d\n", prev.Tick, cur.Tick, len(cur.Units))
		if len(cur.Units) > 0 {
			for _, v := range cur.Units {
				fmt.Fprintf(out, "gate2 view slot %d x=%d z=%d flags=%x\n", v.Slot, int64(v.X), int64(v.Z), v.Flags)
			}
		}
	}
	_ = time.Now // keep import until viewer uses it
	return nil
}
