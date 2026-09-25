package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func runBattleBenchmark(opts Options, cs *contentSet, b *battleSession, c *client.Client) error {
	if err := os.MkdirAll(filepath.Dir(opts.BattleBenchmark), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(opts.BattleBenchmark, 0755); err != nil {
		return fmt.Errorf("nanolathe: create fresh benchmark directory: %w", err)
	}
	s := b.sess
	diagnostics := newBattleBenchmarkDiagnostics(opts.BattleBenchmark, s)
	scene, err := stageCoastalBenchmark(opts, s)
	if err != nil {
		return err
	}
	factories := scene.Factories
	cx, cz := scene.X, scene.Z
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
	// The benchmark defaults to native, as do the window and capture routes.
	// An explicit --zoom is part of the scene metadata (§14.6).
	if opts.Zoom != 0 && opts.Zoom != camera.ZoomUnit {
		mx, my := battleViewCentre(b.cam)
		jumpBattleZoom(b, mx, my, opts.Zoom, modernRenderer(opts))
	}
	census := func() any {
		f := s.Snapshot.Current()
		nanoframes, nano, damaged := 0, 0, 0
		visibleUnits, visibleProjectiles, visibleEffects := 0, 0, 0
		visible := func(x, y, z numeric.Fixed) bool {
			sx, sy := b.cam.WorldToScreen(x, y, z)
			return sx >= camera.OriginX && sx < 1920 && sy >= camera.OriginY && sy < 1048
		}
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
			if visible(u.X, u.Y, u.Z) {
				visibleUnits++
			}
			if u.Health < u.MaxHealth && u.BuildRemaining == 0 {
				damaged++
			}
			if u.BuildRemaining > 0 {
				nanoframes++
			}
		}
		for _, p := range f.Projectiles {
			if visible(p.X, p.Y, p.Z) {
				visibleProjectiles++
			}
		}
		for _, e := range f.Effects {
			if visible(e.X, e.Y, e.Z) {
				visibleEffects++
			}
		}
		for _, e := range f.Events {
			if e.Kind == frame.EventKindNanolathe {
				nano++
			}
		}
		sample := map[string]any{"in_view_units": visibleUnits, "in_view_projectiles": visibleProjectiles, "in_view_effects": visibleEffects, "features": len(f.Features), "sprite_features": sprites, "burning_features": burning, "in_view_sprite_features": visibleSprites, "in_view_burning_features": visibleBurning, "damaged_units": damaged, "moving_units": moving, "tick": s.Clock.GlobalTick, "units": len(f.Units), "projectiles": len(f.Projectiles), "effects": len(f.Effects), "fragments": len(f.Fragments), "state": s.State.String(), "nanoframes": nanoframes, "nanolathe_events": nano, "factory_production": production, "builds": len(f.Builds), "shake": f.ShakeActive, "camera_x": b.cam.X, "camera_z": b.cam.Z}
		addCoastalBenchmarkCensus(sample, f, s, b.cam)
		return sample
	}
	metadata := map[string]any{"scene_version": 5, "gameplay": s.Gameplay.Normalize(), "rules": s.Rules.Name, "gameplay_features": s.Community, "entry_gameplay_features": s.EntryCommunity, "gameplay_features_digest": s.Community.Digest(), "content_profile": cs.contentProfileName(), "mod": cs.modSelector(), "mutators": s.Mutators.String(), "phase_timing": true, "tps": opts.BenchmarkTPS, "map": opts.Map, "seed": opts.Seed, "factories": opts.BenchmarkFactories, "viewport": []int{1920, 1080}, "zoom": viewZoomOf(b).Float(), "auto_remaster": opts.AutoRemaster, "pre_window_ticks": opts.BenchmarkPreTicks, "mobiles_per_side": 160, "mobile_roster": coastalBenchmarkMobiles(), "battle_center": []int32{cx, cz}, "display": loadedSettings().Display, "root": opts.Root, "roots": opts.Roots}
	metadata["coastal_scene"] = scene
	metadata["visibility"] = "normal"
	err = ebitenapp.BattleBenchmark(c, step, census, ebitenapp.BenchmarkOptions{Directory: opts.BattleBenchmark, Renderer: opts.Renderer, Frames: opts.BenchmarkFrames, TPS: opts.BenchmarkTPS, BeforeMeasure: diagnostics.Begin, AfterMeasure: diagnostics.End, Metadata: metadata})
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
