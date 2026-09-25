package main

import (
	"io"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// This is an authored benchmark terrain, not a retail-behavior fixture.
// The tempting central area is a hillside; a broad northern plateau is flat.
func TestFilmDryCentreAvoidsHillsideAndWater(t *testing.T) {
	terrain := &world.Terrain{CellW: 160, CellH: 256, PlayRight: 2528, PlayBottom: 3968, Plot: make([]world.PlotCell, 160*256)}
	for z := int32(0); z < terrain.CellH; z++ {
		for x := int32(0); x < terrain.CellW; x++ {
			h := uint8(50)
			if z > 105 {
				h += uint8((z - 105) / 2)
			}
			terrain.PlotAt(x, z).SetHeight(h)
		}
	}
	_, z, relief, err := filmDryBattleCentre(terrain)
	if err != nil || z+520 >= 105*16 || relief != 0 {
		t.Fatalf("centre z=%d relief=%d err=%v; wanted the flat northern plateau", z, relief, err)
	}
	terrain.SeaLevel = 255
	if _, _, _, err := filmDryBattleCentre(terrain); err == nil {
		t.Fatal("accepted an entirely submerged formation")
	}
}

func TestBattleBenchmarkLeadInFlags(t *testing.T) {
	opts, err := parseFlags([]string{"--battle-benchmark=/tmp/unused-battle"}, io.Discard)
	if err != nil || opts.Map != "expanded confluence" || opts.BenchmarkPreTicks != 300 {
		t.Fatalf("defaults: map=%q pre=%d err=%v", opts.Map, opts.BenchmarkPreTicks, err)
	}
	opts, err = parseFlags([]string{"--battle-benchmark=/tmp/unused-battle", "--benchmark-pre-ticks=0"}, io.Discard)
	if err != nil || opts.BenchmarkPreTicks != 0 {
		t.Fatalf("opening capture: pre=%d err=%v", opts.BenchmarkPreTicks, err)
	}
	for _, value := range []string{"-1", "18001"} {
		if _, err := parseFlags([]string{"--battle-benchmark=/tmp/unused-battle", "--benchmark-pre-ticks=" + value}, io.Discard); err == nil {
			t.Fatalf("accepted pre-ticks=%s", value)
		}
	}
}

func TestCaptureBenchmarkFlags(t *testing.T) {
	base := []string{"--battle-benchmark=/tmp/unused-battle", "--benchmark-capture=/tmp/unused-capture", "--survival", "--map=Moon Quartet", "--shot-size=1280x827"}
	opts, err := parseFlags(base, io.Discard)
	if err != nil || opts.BenchmarkCapture != "/tmp/unused-capture" || opts.ShotSize != "1280x827" {
		t.Fatalf("capture benchmark flags: opts=%+v err=%v", opts, err)
	}
	for _, args := range [][]string{
		{"--benchmark-capture=/tmp/unused-capture"},
		{"--battle-benchmark=/tmp/unused-battle", "--benchmark-capture=/tmp/unused-capture", "--map=Moon Quartet"},
		{"--battle-benchmark=/tmp/unused-battle", "--benchmark-capture=/tmp/unused-capture", "--survival"},
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Fatalf("accepted incomplete capture benchmark flags: %v", args)
		}
	}
}

func TestCoastalBenchmarkStagesEveryMedium(t *testing.T) {
	root := probeRetail(t)
	opts, err := parseFlags([]string{"--battle-benchmark=/tmp/unused-coastal", "--root=" + root}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	sess, _, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	scene, err := stageCoastalBenchmark(opts, sess)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.Vis.Mode().HistoryEnabled() || !sess.Vis.Mode().CurrentEnabled() {
		t.Fatal("benchmark disabled normal fog")
	}
	for _, u := range scene.Factories {
		found := false
		for _, n := range orders.QueueOfUnit(u).Primary() {
			if n.ID == orders.Lookup("QPatrol") {
				found = true
			}
		}
		if !found {
			t.Fatal("factory products have no patrol rally")
		}
	}
	if len(scene.Factories) != 8 {
		t.Fatalf("factory queues: %d", len(scene.Factories))
	}
	var air, naval, waterBuildings int
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		if u.Def.CanFly {
			air++
			want := movement.CruiseAltitudeForOffset(sess.World, u.X, u.Z, u.Def.CruiseAlt)
			if u.Move.Mode != 2 || u.Y != want {
				t.Fatalf("%s not airborne at authored cruise altitude", u.Def.UnitName)
			}
			q := orders.QueueOfUnit(u)
			if q == nil || len(q.Primary()) == 0 || q.Primary()[0].ID != orders.Lookup("VTOL_Patrol") {
				t.Fatalf("%s missing air patrol", u.Def.UnitName)
			}
		} else if u.Def.MinWaterDepth > 0 {
			if u.Def.BMCode == 0 {
				waterBuildings++
			} else {
				naval++
			}
			if sess.World.HeightAt(u.X, u.Z) >= sess.World.SeaLevelWorld() {
				t.Fatalf("%s staged on dry land", u.Def.UnitName)
			}
		}
	}
	if air == 0 || naval == 0 || waterBuildings == 0 {
		t.Fatalf("missing medium: air=%d navy=%d water buildings=%d", air, naval, waterBuildings)
	}
}
