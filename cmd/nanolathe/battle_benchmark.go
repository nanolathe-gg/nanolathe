package main

import (
	"encoding/json"
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"os"
	"path/filepath"
)

func runBattleBenchmark(opts Options, b *battleSession, c *client.Client) error {
	if err := os.MkdirAll(filepath.Dir(opts.BattleBenchmark), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(opts.BattleBenchmark, 0755); err != nil {
		return fmt.Errorf("nanolathe: create fresh benchmark directory: %w", err)
	}
	s := b.sess
	diagnostics := newBattleBenchmarkDiagnostics(opts.BattleBenchmark, s)
	var factories []*units.Unit
	// Scene composition and spacing are fixture choices, not retail rules.
	n := 160
	cx, cz, terrainRelief, err := benchmarkBattleCentre(s.World)
	if err != nil {
		return err
	}
	fmt.Printf("map=%dx%d center=%d,%d terrain_relief=%d\n", b.cam.MapW, b.cam.MapH, cx, cz, terrainRelief)
	names := [][]string{{"armflash", "armstump", "armpw", "armrock", "armham", "armfav", "armzeus", "armfig", "armthund", "armwar"}, {"corraid", "corlevlr", "corak", "corstorm", "corthud", "corfav", "corpyro", "corveng", "corshad", "correap"}}
	buildings := [][]string{{"armsolar", "armlab", "armllt", "armrad"}, {"corsolar", "corlab", "corllt", "corrad"}}
	// Reserve a full largest authored building width plus a cell on either side.
	// The earlier fixed 80-pixel pitch overlapped ARM solar and lab yards.
	buildingPitch := int32(80)
	for _, side := range buildings {
		for _, name := range side {
			if def, ok := s.Catalog.Unit(name); ok {
				buildingPitch = max(buildingPitch, int32(def.FootprintX)*16+32)
			}
		}
	}
	for side := 0; side < 2; side++ {
		for i := 0; i < n+16; i++ {
			name := names[side][i%len(names[side])]
			if i >= n {
				name = buildings[side][(i-n)%4]
			}
			def, ok := s.Catalog.Unit(name)
			if !ok {
				return fmt.Errorf("nanolathe: benchmark unit missing: logical path %s, providers searched [catalog], expected an authored unit definition", name)
			}
			x := cx + int32((side*2-1)*300) + int32(i%8)*48 - 168
			z := cz + int32(i/8)*48 - 456
			if i >= n {
				x = cx + int32((side*2-1)*620) + int32((i-n)%2)*buildingPitch - (buildingPitch - 80)
				z = cz + int32((i-n)/2)*80 - 250
			}
			fx, fz := numeric.Fixed(x<<16), numeric.Fixed(z<<16)
			fy := s.World.HeightAt(fx, fz)
			h, e := s.Units.Create(def, uint8(side), fx, fy, fz)
			if e != nil {
				return fmt.Errorf("create %s: %w", name, e)
			}
			u := s.Units.Unit(h)
			if opts.BenchmarkFactories && (name == "armlab" || name == "corlab") {
				factories = append(factories, u)
				product := "armpw"
				if side == 1 {
					product = "corak"
				}
				if err := construction.QueueFactoryBuild(u, product, 10, s.Catalog); err != nil {
					return fmt.Errorf("nanolathe: benchmark factory queue: %w", err)
				}
			}
			if s.Movement != nil {
				s.Movement.EnsureUnit(u)
			}
			if i < n {
				q, ok := u.Orders.(*orders.Queue)
				if ok {
					id := orders.Lookup("Move_Ground")
					goalX := numeric.Fixed(cx+int32((1-side*2)*250)) << 16
					goalY := s.World.HeightAt(goalX, fz)
					q.Push(id, orders.NewNodeForOrder(id, 0, goalX, goalY, fz, s.Clock.GlobalTick, h, false))
				}
			}
		}
	}
	millis := &shotMillisSource{}
	b.millisSource = millis
	step := func() {
		diagnostics.Step(func() { millis.step++; b.viewerStep(1.0/30, c) })
	}
	b.cam.JumpToBattleViewCenter(cx, cz)
	for i := 0; i < opts.BenchmarkPreTicks; i++ {
		step()
		// Drain each committed tick so the first displayed frame does not replay
		// the entire lead-in's retained sound and status queue.
		c.TickPresentationAudio()
	}
	b.cam.JumpToBattleViewCenter(cx, cz)
	// The benchmark scene at the detail view is the same battle drawn from
	// twice the pixels (DESIGN_GPU_RENDERER §14.1). The scale is applied after
	// the scene's own camera jump and about the viewport centre, so the army
	// stays framed, and it is recorded in the scene metadata below: two runs
	// are comparable only at the same scale.
	// The benchmark scene is native unless `--zoom` asks otherwise: the
	// window's resolution default (§14.6) would silently change the scene two
	// runs are compared on, and the scale is part of the scene metadata.
	if opts.Zoom != 0 && opts.Zoom != camera.ZoomUnit {
		mx, my := battleViewCentre(b.cam)
		jumpBattleZoom(b, mx, my, opts.Zoom, modernRenderer(opts))
	}
	census := func() any {
		f := s.Snapshot.Current()
		nanoframes, nano, damaged := 0, 0, 0
		moving := benchmarkMovingUnits(f, s.Snapshot.Previous())
		// In-view counts use projected anchors inside the battle viewport;
		// they do not claim pixel visibility after fog or sprite occlusion.
		sprites, burning, visibleSprites, visibleBurning := 0, 0, 0, 0
		for _, feature := range f.Features {
			if feature.Filename != "" {
				sprites++
				sx, sy := b.cam.WorldToScreen(feature.X, feature.Y, feature.Z)
				visible := sx >= camera.OriginX && sx < 1920 && sy >= camera.OriginY && sy < 1048
				if visible {
					visibleSprites++
				}
				if feature.IsBurning {
					burning++
					if visible {
						visibleBurning++
					}
				}
			}
		}
		var production [8]benchmarkFactory
		for i, u := range factories {
			if u == nil || !u.Alive || u.Def == nil {
				continue
			}
			row := benchmarkFactory{Unit: u.Def.UnitName, Owner: u.Owner, Stance: u.InBuildStance, YardOpen: u.YardOpen}
			if q := orders.QueueOfUnit(u); q != nil {
				for _, n := range q.Primary() {
					if n.BuildDefKey != "" {
						row.Phase = n.Phase
						row.Product = n.BuildDefKey
						row.Deadline = n.Deadline
						row.Target = uint32(n.Target)
						break
					}
				}
			}
			production[i] = row
		}
		for _, u := range f.Units {
			if u.Health < u.MaxHealth && u.BuildRemaining == 0 {
				damaged++
			}
			if u.BuildRemaining > 0 {
				nanoframes++
			}
		}
		for _, e := range f.Events {
			if e.Kind == frame.EventKindNanolathe {
				nano++
			}
		}
		return map[string]any{"features": len(f.Features), "sprite_features": sprites, "burning_features": burning, "in_view_sprite_features": visibleSprites, "in_view_burning_features": visibleBurning, "damaged_units": damaged, "moving_units": moving, "tick": s.Clock.GlobalTick, "units": len(f.Units), "projectiles": len(f.Projectiles), "effects": len(f.Effects), "fragments": len(f.Fragments), "state": s.State.String(), "nanoframes": nanoframes, "nanolathe_events": nano, "factory_production": production, "builds": len(f.Builds), "shake": f.ShakeActive, "camera_x": b.cam.X, "camera_z": b.cam.Z}
	}
	err = ebitenapp.BattleBenchmark(c, step, census, ebitenapp.BenchmarkOptions{Directory: opts.BattleBenchmark, Renderer: opts.Renderer, Frames: opts.BenchmarkFrames, TPS: opts.BenchmarkTPS, BeforeMeasure: diagnostics.Begin, AfterMeasure: diagnostics.End, Metadata: map[string]any{"scene_version": 4, "phase_timing": true, "tps": opts.BenchmarkTPS, "map": opts.Map, "seed": opts.Seed, "factories": opts.BenchmarkFactories, "viewport": []int{1920, 1080}, "zoom": viewZoomOf(b).Float(), "auto_remaster": opts.AutoRemaster, "pre_window_ticks": opts.BenchmarkPreTicks, "mobiles_per_side": n, "mobile_roster": names, "battle_center": []int32{cx, cz}, "terrain_relief": terrainRelief, "display": loadedSettings().Display, "root": opts.Root, "roots": opts.Roots}})
	if err != nil {
		return err
	}
	var blocked []map[string]any
	for _, u := range factories {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		rect, ok := s.Build.PlacementForProduct(u.Handle)
		if !ok {
			continue
		}
		yard, e := world.ParseYardMap(u.Def.YardMap, int(rect.Width()), int(rect.Depth()))
		if e != nil {
			return e
		}
		for z := rect.MinZ(); z < rect.MaxZ(); z++ {
			for x := rect.MinX(); x < rect.MaxX(); x++ {
				if yard[int((z-rect.MinZ())*rect.Width()+x-rect.MinX())]&(0x02|0x08) == 0 {
					continue
				}
				h, held := s.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z})
				if held && h != int(u.Handle) {
					blocked = append(blocked, map[string]any{"factory": u.Def.UnitName, "handle": u.Handle, "cell": []int32{x, z}, "occupant": h})
				}
			}
		}
	}
	f, e := os.Create(filepath.Join(opts.BattleBenchmark, "factory-blockers.json"))
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(blocked)
}

type benchmarkFactory struct {
	Unit     string
	Owner    uint8
	YardOpen bool
	Stance   bool
	Phase    uint8
	Product  string
	Deadline int32
	Target   uint32
}

// benchmarkBattleCentre minimizes terrain relief across the whole fixture:
// both formations, the crossing lanes, and the rear buildings. These are
// benchmark placement choices, not retail movement thresholds. A plateau is
// fine; a hillside or a cliff through the middle of a formation is not.
// Stable row-major ties keep scene selection independent of either RNG.
func benchmarkBattleCentre(t *world.Terrain) (int32, int32, int32, error) {
	const halfX, halfZ = int32(800), int32(520)
	bestX, bestZ := t.PlayRight/2, t.PlayBottom/2
	best := int32(1 << 30)
	for z := halfZ + 32; z <= t.PlayBottom-halfZ-32; z += 32 {
		for x := halfX + 32; x <= t.PlayRight-halfX-32; x += 32 {
			low, high := int32(255), int32(0)
			valid := true
			for dz := -halfZ; dz <= halfZ && valid; dz += 16 {
				for dx := -halfX; dx <= halfX; dx += 16 {
					h := int32(t.HeightAt(numeric.Fixed(x+dx)<<16, numeric.Fixed(z+dz)<<16).Int())
					if h <= int32(t.SeaLevel) {
						valid = false
						break
					}
					low, high = min(low, h), max(high, h)
					if high-low >= best {
						valid = false
						break
					}
				}
			}
			if valid {
				bestX, bestZ, best = x, z, high-low
			}
		}
	}
	if best == 1<<30 {
		return 0, 0, 0, fmt.Errorf("nanolathe: benchmark placement failed: logical path <battle terrain>, providers searched [map], expected a dry area large enough for the formations and buildings")
	}
	return bestX, bestZ, best, nil
}

// Published units are in ascending pool-slot order [I1]. Compare only the same
// instance in adjacent committed frames: a blocked mover can retain nonzero
// speed, and a newly created unit is not evidence of displacement.
func benchmarkMovingUnits(current, previous *frame.Frame) int {
	if current == nil || previous == nil {
		return 0
	}
	moving, before := 0, 0
	for _, u := range current.Units {
		for before < len(previous.Units) && previous.Units[before].Slot < u.Slot {
			before++
		}
		if before == len(previous.Units) {
			break
		}
		p := previous.Units[before]
		if p.Slot == u.Slot && p.InstanceID == u.InstanceID && (p.X != u.X || p.Y != u.Y || p.Z != u.Z) {
			moving++
		}
	}
	return moving
}
