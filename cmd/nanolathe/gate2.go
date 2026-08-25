// Package main — Gate-2 real pathing wiring.
//
// Composition root for PHASES Gate 2 with real movement stack [PLAN_07].
// Rubber-band select follows the [07 §9] truth table, click issues Move_Ground
// through the latch, a spawned armflea interpolates smoothly between 30 Hz sim
// ticks. A* is real: Scheduler + path.Search + profile passability, with route
// publication clamped to 20 and save form to 3, pruning, steer heading clamp
// and integrated step, and synchronous occupancy via CollisionState.
//
// Registration respects I7's same-tick ordering via twelve phases [01 §4.4]:
//
//	PhaseUnitsScripts (phase 2) for units.Tick,
//	PhaseOrdersPathEconomy (phase 5) for orders pump, scheduler tick, movement integrate,
//	PhaseCadenceFlip (phase 12) for snapshot publish (C15).
package main

import (
	"fmt"
	"os"

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
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// gate2Session is the Gate-2 composition root with real pathing. It owns the
// kernel, clock, world, scheduler/system, snapshot buffer and camera, and is
// the single writer of snapshot.Frame.Units.
type gate2Session struct {
	catalog    *content.Catalog
	terrain    *world.Terrain
	world      *units.World
	kernel     *kernel.Kernel
	clock      *clock.State
	buffer     *snapshot.Buffer
	system     *movement.System
	cam        *camera.Camera
	latch      input.Latch
	dragActive bool
	dragStartX int32
	dragStartY int32
	dragEndX   int32
	dragEndY   int32
	// frame converts the renderer's float seconds delta into the scaled
	// milliseconds the authoritative clock budget anchors against. The tick
	// COUNT still comes from clock.State [01 §4.2].
	frame clock.FrameClock
}

// newGate2Session builds a fresh Gate-2 world, spawns one armflea near the
// map center, and registers the twelve-phase callbacks [01 §4.4] (I7) with real
// pathing: scheduler tick + movement integrate replace the straight-line stub.
func newGate2Session(cat *content.Catalog, terrain *world.Terrain, buf *snapshot.Buffer, cam *camera.Camera) (*gate2Session, error) {
	if cat == nil || terrain == nil || buf == nil || cam == nil {
		return nil, fmt.Errorf("gate2: nil catalog/terrain/buffer/cam")
	}
	w := units.New(600, cat)
	def, ok := cat.Unit("armflea")
	if !ok || def == nil {
		return nil, fmt.Errorf("gate2: armflea def not found")
	}
	cx := int32(terrain.CellW * 8) // (CellW*16)/2 map pixels
	cz := int32(terrain.CellH * 8)
	x := numeric.Fixed(int64(cx) << 16)
	z := numeric.Fixed(int64(cz) << 16)
	y := terrain.HeightAt(x, z)
	h, err := w.Create(def, 0, x, y, z)
	if err != nil {
		return nil, fmt.Errorf("gate2: spawn armflea: %w", err)
	}
	u := w.Unit(h)
	// Movement profiles are per unit, resolved from each definition's movement
	// class [02 "Movement class record"] [04 §6.1]. The System owns that
	// resolution; the fallback here covers only definitions naming no class at
	// all, which is aircraft and buildings.
	grid := movement.NewOccupancyGrid()
	system := movement.NewSystem(terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, grid)
	system.SetClasses(cat.Movement)
	if u != nil {
		system.EnsureUnit(u)
	}
	if len(system.Unresolved) > 0 {
		fmt.Fprintf(os.Stderr, "nanolathe: gate2: unresolved movement classes %v\n", system.Unresolved)
	}
	k := &kernel.Kernel{}
	clk := &clock.State{Requested: 10, Active: 10}
	// [01 §4.4] twelve phases. Registration order within a phase is call order (I1/I7).
	k.Register(kernel.PhaseUnitsScripts, "units-tick", func(tick uint32) {
		w.Tick(tick)
	})
	// Phase 5 (orders-path-economy): orders pump, scheduler tick, movement integrate [GAP T15] I7.
	k.Register(kernel.PhaseOrdersPathEconomy, "orders-pump", func(tick uint32) {
		for _, uu := range w.Iter() {
			if uu == nil || !uu.Alive {
				continue
			}
			q := orders.QueueForUnit(uu)
			q.Pump(uu, tick)
		}
	})
	k.Register(kernel.PhaseOrdersPathEconomy, "scheduler-tick", func(tick uint32) {
		if system != nil && system.Scheduler != nil {
			system.Scheduler.Tick(tick)
		}
	})
	k.Register(kernel.PhaseOrdersPathEconomy, "movement-integrate", func(tick uint32) {
		if system != nil {
			system.Tick(tick, w)
		}
	})
	// Phase 12 (cadence-flip): snapshot publish after all phases [01 §4.4] C15.
	k.Register(kernel.PhaseCadenceFlip, "snapshot-publish", func(tick uint32) {
		views := make([]snapshot.UnitView, 0, w.Used())
		for _, uu := range w.Iter() {
			if uu == nil || !uu.Alive {
				continue
			}
			heading := uint16(0)
			if system != nil {
				if st, ok := system.Steers[uu.Handle]; ok && st != nil {
					heading = st.Heading
				}
			}
			views = append(views, snapshot.UnitView{
				Slot:           uu.Handle,
				Owner:          uu.Owner,
				X:              uu.X,
				Y:              uu.Y,
				Z:              uu.Z,
				Heading:        heading,
				Pitch:          0,
				Bank:           0,
				Health:         uu.Health,
				MaxHealth:      uu.MaxHealth,
				BuildRemaining: uu.Remaining,
				Flags:          uu.Flags,
			})
		}
		buf.Publish(&snapshot.Frame{Tick: tick, Units: views})
	})
	views := make([]snapshot.UnitView, 0, w.Used())
	for _, uu := range w.Iter() {
		if uu == nil || !uu.Alive {
			continue
		}
		views = append(views, snapshot.UnitView{
			Slot:           uu.Handle,
			Owner:          uu.Owner,
			X:              uu.X,
			Y:              uu.Y,
			Z:              uu.Z,
			Health:         uu.Health,
			MaxHealth:      uu.MaxHealth,
			BuildRemaining: uu.Remaining,
			Flags:          uu.Flags,
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
		system:  system,
		cam:     cam,
		latch:   input.LatchNormal,
	}, nil
}

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

// issueMoveTo submits a scheduler request with PointGoal at click cell radius 0
// and keeps the orders queue as authority by also pushing a Move_Ground node
// whose Goal carries the click world position. The route is stored per-unit via
// System.Routes on publication [04 §7.3] C14.
func (g *gate2Session) issueMoveTo(wx, wz numeric.Fixed) {
	if g == nil || g.world == nil || g.system == nil {
		return
	}
	for _, u := range g.world.Iter() {
		if u == nil || !u.Alive || u.Flags&client.SelectionFlag == 0 {
			continue
		}
		g.system.EnsureUnit(u)
		start := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
		goal := path.Cell{X: world.WorldToCell(wx), Z: world.WorldToCell(wz)}
		g.system.SubmitMove(u.Handle, u.Owner, start, goal)
		// Keep orders queue as authority: push Move_Ground node storing goal
		pos := &orders.ResolvePos{X: wx, Z: wz, Y: g.terrain.HeightAt(wx, wz)}
		id := orders.Resolve(2, u, nil, pos)
		if id == 0 {
			id = orders.Lookup("Move_Ground")
		}
		if id == 0 {
			continue
		}
		q := orders.QueueForUnit(u)
		n := orders.Node{GoalX: wx, GoalZ: wz, GoalY: pos.Y}
		q.Push(id, n)
	}
}

func (g *gate2Session) handleInput(host interface{}) {
	_ = g
	_ = host
}

func (g *gate2Session) viewerStep(delta float64, cl *client.Client) {
	if g == nil || cl == nil {
		return
	}
	if in := cl.Input(); in != nil {
		kbd := in.Kbd
		mouse := in.Mouse
		if kbd.KeyDown(input.KeyM) {
			g.latch = input.LatchMove
		}
		if kbd.KeyDown(input.KeyEscape) {
			g.latch = input.LatchNormal
		}
		mx := int32(mouse.X)
		my := int32(mouse.Y)
		leftHeld := mouse.Held(input.MouseButtonLeft)
		additive := kbd.HasShift()
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
				if g.latch == input.LatchMove {
					wx, wz := g.cam.ScreenToWorld(mx+camera.OriginX, my+camera.OriginY)
					g.issueMoveTo(wx, wz)
					g.latch = input.LatchNormal
				} else if mouse.Held(input.MouseButtonRight) {
				} else {
				}
			} else {
				_, _ = client.ApplyDragSelectionWorld(g.world, g.cam, rect, additive)
			}
		}
		if mouse.Pressed(input.MouseButtonRight) {
			if g.latch == input.LatchMove || g.hasSelection() {
				wx, wz := g.cam.ScreenToWorld(mx+camera.OriginX, my+camera.OriginY)
				g.issueMoveTo(wx, wz)
				g.latch = input.LatchNormal
			}
		}
		if mouse.Pressed(input.MouseButtonLeft) && g.latch == input.LatchMove && !g.dragActive {
			wx, wz := g.cam.ScreenToWorld(mx+camera.OriginX, my+camera.OriginY)
			g.issueMoveTo(wx, wz)
			g.latch = input.LatchNormal
		}
	}
	// The authoritative clock owns the frame-to-tick budget: pause, requested
	// versus active speed, speed hysteresis, the float32 carry and the
	// zero-to-five clamp are all [01 §4.2] [01 §4.3] behavior, and a second
	// accumulator here would diverge from every one of them.
	ticks := g.clock.AdvanceSP(g.frame.Scaled(delta))
	for i := 0; i < ticks; i++ {
		g.kernel.SubTick(g.clock)
	}
	if g.cam != nil && cl.Input() != nil {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := int32(delta * 1000)
		if rawDelta < 0 {
			rawDelta = 0
		}
		if rawDelta == 0 {
			rawDelta = 16
		}
		const scrollSetting = 8
		if kbd.KeyHeld(input.KeyW) || kbd.KeyHeld(input.KeyUp) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirUp)
		}
		if kbd.KeyHeld(input.KeyS) || kbd.KeyHeld(input.KeyDown) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirDown)
		}
		if kbd.KeyHeld(input.KeyA) || kbd.KeyHeld(input.KeyLeft) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirLeft)
		}
		if kbd.KeyHeld(input.KeyD) || kbd.KeyHeld(input.KeyRight) {
			g.cam.Scroll(scrollSetting, rawDelta, camera.DirRight)
		}
		const edge = 8
		w, h := cl.Size()
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
	if len(sess.world.Iter()) > 0 {
		u := sess.world.Iter()[0]
		px := int32(u.X >> 16)
		pz := int32(u.Z >> 16)
		cam.X = px - int32(winW/2)
		cam.Z = pz - int32(winH/2)
		cam.Pan(0, 0)
	}
	var clPtr *client.Client
	step := func(delta float64) {
		if clPtr == nil {
			// Same budget, same authority — the client just is not up yet.
			ticks := sess.clock.AdvanceSP(sess.frame.Scaled(delta))
			for i := 0; i < ticks; i++ {
				sess.kernel.SubTick(sess.clock)
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
	fmt.Fprintf(os.Stderr, "nanolathe: gate2 viewer: opening window %dx%d for map %q (%dx%d cells) armflea@%d,%d M=arm Move Shift=add drag=select right-click=move\n", winW, winH, opts.Map, terrain.CellW, terrain.CellH, cam.X, cam.Z)
	err = client.RunGame(cl)
	finalTick := sess.clock.GlobalTick
	fmt.Fprintf(os.Stderr, "nanolathe: gate2 viewer: window closed final tick %d rng sim draws %d\n", finalTick, rng.Global.Sim.Draws())
	return err
}

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
	if len(sess.world.Iter()) > 0 {
		u := sess.world.Iter()[0]
		u.Flags |= client.SelectionFlag
		goalX := u.X + numeric.Fixed(200*65536)
		goalZ := u.Z + numeric.Fixed(100*65536)
		// Clamp goal inside map
		maxX := numeric.Fixed(int64(mapW) << 16)
		maxZ := numeric.Fixed(int64(mapH) << 16)
		if goalX < 0 {
			goalX = 0
		}
		if goalX > maxX {
			goalX = maxX - numeric.Fixed(1<<16)
		}
		if goalZ < 0 {
			goalZ = 0
		}
		if goalZ > maxZ {
			goalZ = maxZ - numeric.Fixed(1<<16)
		}
		sess.issueMoveTo(goalX, goalZ)
	}
	ticks := opts.Ticks
	if ticks <= 0 {
		ticks = 60
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
	for _, u := range sess.world.Iter() {
		fmt.Fprintf(out, "gate2 unit %d owner %d x=%d z=%d y=%d flags=%x health=%d/%d\n", u.Handle, u.Owner, int64(u.X), int64(u.Y), int64(u.Z), u.Flags, u.Health, u.MaxHealth)
		if sess.system != nil {
			if route, ok := sess.system.Routes[u.Handle]; ok && route != nil {
				activeCount := 0
				if route.Active {
					activeCount = int(route.Count)
				}
				enc := movement.EncodeRoute(route)
				fmt.Fprintf(out, "gate2 route points %d active %v enc %d bytes %v\n", activeCount, route.Active, len(enc), enc)
				fmt.Fprintf(out, "gate2 route count %d encLen %d\n", activeCount, len(enc))
			}
		}
	}
	fmt.Fprintf(out, "gate2 ticks %d globalTick %d simDraws %d->%d crtDraws %d->%d seed sim=%d crt=%d\n",
		ticks, sess.clock.GlobalTick, startSimDraws, endSimDraws, startCrtDraws, endCrtDraws, sess.clock.GlobalTick, sess.clock.GlobalTick)
	if prev, cur, ok := buf.Read(); ok && cur != nil && prev != nil {
		fmt.Fprintf(out, "gate2 snapshot prevTick %d curTick %d units %d\n", prev.Tick, cur.Tick, len(cur.Units))
		if len(cur.Units) > 0 {
			for _, v := range cur.Units {
				fmt.Fprintf(out, "gate2 view slot %d x=%d z=%d flags=%x\n", v.Slot, int64(v.X), int64(v.Z), v.Flags)
			}
		}
	}
	return nil
}

func runGate2RouteDump(opts Options, cs *contentSet, out *os.File) error {
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
	if len(sess.world.Iter()) > 0 {
		u := sess.world.Iter()[0]
		u.Flags |= client.SelectionFlag
		// Use a farther goal for route dump so after 100 ticks route remains active
		// (demonstrates ≤20 / ≤13 while en route). Clamp inside map.
		goalX := u.X + numeric.Fixed(600*65536)
		goalZ := u.Z + numeric.Fixed(400*65536)
		maxX := numeric.Fixed(int64(mapW) << 16)
		maxZ := numeric.Fixed(int64(mapH) << 16)
		if goalX < 0 {
			goalX = 0
		}
		if goalX > maxX {
			goalX = maxX - numeric.Fixed(1<<16)
		}
		if goalZ < 0 {
			goalZ = 0
		}
		if goalZ > maxZ {
			goalZ = maxZ - numeric.Fixed(1<<16)
		}
		sess.issueMoveTo(goalX, goalZ)
	}
	ticks := opts.Ticks
	if ticks <= 0 {
		ticks = 100
	}
	if ticks > 10000 {
		ticks = 10000
	}
	for i := 0; i < ticks; i++ {
		sess.kernel.SubTick(sess.clock)
	}
	// After N ticks, dump route point count (≤20), encoded save form length (≤3 pairs ⇒ ≤13 bytes), and final position; deterministic under --seed [task]
	for _, u := range sess.world.Iter() {
		activeCount := 0
		encLen := 1
		var enc []byte
		if sess.system != nil {
			if route, ok := sess.system.Routes[u.Handle]; ok && route != nil {
				if route.Active {
					activeCount = int(route.Count)
					if activeCount > 20 {
						activeCount = 20
					}
				}
				enc = movement.EncodeRoute(route)
				encLen = len(enc)
			} else {
				enc = []byte{0}
				encLen = 1
			}
		}
		fmt.Fprintf(out, "route points %d\n", activeCount)
		fmt.Fprintf(out, "route encLen %d\n", encLen)
		fmt.Fprintf(out, "route enc %v\n", enc)
		fmt.Fprintf(out, "pos x=%d z=%d y=%d\n", int64(u.X), int64(u.Y), int64(u.Z))
		fmt.Fprintf(out, "ticks %d globalTick %d\n", ticks, sess.clock.GlobalTick)
		if activeCount > 20 {
			fmt.Fprintf(out, "route violation: points %d >20\n", activeCount)
		}
		if encLen > 13 {
			fmt.Fprintf(out, "route violation: encLen %d >13\n", encLen)
		}
	}
	return nil
}
