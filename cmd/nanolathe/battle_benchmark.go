package main

import (
	"encoding/json"
	"fmt"
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
	var factories []*units.Unit
	n := 80
	cx, cz := b.cam.MapW/2, b.cam.MapH/2
	fmt.Printf("map=%dx%d center=%d,%d\n", b.cam.MapW, b.cam.MapH, cx, cz)
	names := [][]string{{"armflash", "armstump", "armpw", "armrock", "armham", "armflea", "armpeep", "armfig"}, {"corraid", "corlevlr", "corak", "corstorm", "corthud", "corfav", "corfink", "corveng"}}
	buildings := [][]string{{"armsolar", "armlab", "armllt", "armrad"}, {"corsolar", "corlab", "corllt", "corrad"}}
	// Reserve a full largest authored building width plus a cell on either side.
	// The earlier fixed 80-pixel pitch overlapped ARM solar and lab yards.
	buildingPitch := int32(80)
	if opts.BenchmarkFactories {
		for _, side := range buildings {
			for _, name := range side {
				if def, ok := s.Catalog.Unit(name); ok {
					buildingPitch = max(buildingPitch, int32(def.FootprintX)*16+32)
				}
			}
		}
	}
	for side := 0; side < 2; side++ {
		for i := 0; i < n+16; i++ {
			name := names[side][i%8]
			if i >= n {
				name = buildings[side][(i-n)%4]
			}
			def, ok := s.Catalog.Unit(name)
			if !ok {
				return fmt.Errorf("missing %s", name)
			}
			x := cx + int32((side*2-1)*260) + int32(i%10)*40 - 180
			z := cz + int32(i/10)*44 - 200
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
					q.Push(id, orders.NewNodeForOrder(id, 0, numeric.Fixed((cx+int32((1-side*2)*250))<<16), fy, fz, s.Clock.GlobalTick, h, false))
				}
			}
		}
	}
	millis := &shotMillisSource{}
	b.millisSource = millis
	step := func() { millis.step++; b.viewerStep(1.0/30, c) }
	for i := 0; i < 30; i++ {
		step()
	}
	b.cam.JumpToBattleViewCenter(cx, cz)
	// The benchmark scene at the detail view is the same battle drawn from
	// twice the pixels (DESIGN_GPU_RENDERER §14.1). The scale is applied after
	// the scene's own camera jump and about the viewport centre, so the army
	// stays framed, and it is recorded in the scene metadata below: two runs
	// are comparable only at the same scale.
	applyEntryZoom(opts, b)
	census := func() any {
		f := s.Snapshot.Current()
		nanoframes, nano := 0, 0
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
			if u.BuildRemaining > 0 {
				nanoframes++
			}
		}
		for _, e := range f.Events {
			if e.Kind == frame.EventKindNanolathe {
				nano++
			}
		}
		return map[string]any{"tick": s.Clock.GlobalTick, "units": len(f.Units), "projectiles": len(f.Projectiles), "effects": len(f.Effects), "fragments": len(f.Fragments), "state": s.State.String(), "nanoframes": nanoframes, "nanolathe_events": nano, "factory_production": production, "builds": len(f.Builds), "shake": f.ShakeActive, "camera_x": b.cam.X, "camera_z": b.cam.Z}
	}
	err := ebitenapp.BattleBenchmark(c, step, census, ebitenapp.BenchmarkOptions{Directory: opts.BattleBenchmark, Renderer: opts.Renderer, Frames: opts.BenchmarkFrames, TPS: opts.BenchmarkTPS, Metadata: map[string]any{"scene_version": 3, "tps": opts.BenchmarkTPS, "map": opts.Map, "seed": opts.Seed, "factories": opts.BenchmarkFactories, "viewport": []int{1920, 1080}, "zoom": viewScaleOf(b), "auto_remaster": opts.AutoRemaster, "warmup_draws": 60, "pre_window_ticks": 30, "display": loadedSettings().Display, "root": opts.Root}})
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
