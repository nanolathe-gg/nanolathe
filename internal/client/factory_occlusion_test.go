package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The host factory-occlusion policy preserves the established height test
// [03 R-REN-03A §4] across retail's completion detach [04 R-FAC-02 §3].
// The real script and mover supply every captured pose; resources are topped
// up only to finish the build without waiting for an economy bootstrap.
func TestCompletedFactoryProductKeepsHeightComposition(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		t.Skipf("palette unavailable: %v", err)
	}
	rng.SeedGlobal(1, 1)
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioDirectOTA, Map: "Great Divide",
		LocalOwner: -1, SimulationSeed: 1, CRTSeed: 1, FS: fs,
	})
	if err != nil {
		t.Skipf("battle composition unavailable: %v", err)
	}
	sess := composed.Session
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}
	factoryDef, ok := sess.Catalog.Unit("armvp")
	if !ok || factoryDef == nil {
		t.Skip("retail catalog has no armvp")
	}
	var comX, comZ numeric.Fixed
	for _, u := range sess.Units.Iter() {
		if u != nil && u.Alive && u.Owner == sess.LocalOwner && u.Def != nil &&
			strings.HasSuffix(strings.ToLower(u.Def.UnitName), "com") {
			comX, comZ = u.X, u.Z
			break
		}
	}
	if comX == 0 && comZ == 0 {
		t.Skip("no local commander to place the factory beside")
	}
	// Ten cells clear of the commander: a factory cannot
	// open its yard while a foreign unit holds a cell the open state selects
	// [04 R-FAC-02 §5].
	const factoryOffsetCells = 10
	factoryHandle, err := sess.Units.Create(factoryDef, sess.LocalOwner,
		comX+numeric.Fixed(int64(factoryOffsetCells)<<20), sess.World.HeightAt(comX+numeric.Fixed(int64(factoryOffsetCells)<<20), comZ), comZ)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := sess.Units.Unit(factoryHandle)
	if factory == nil {
		t.Fatal("factory not in pool")
	}
	if err := sess.Build.RegisterBuildingPlacement(factory); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if err := construction.QueueFactoryBuild(factory, "armflash", 1, sess.Catalog); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}

	c := newTestClient(t)
	c.SetPalette(pal)
	c.SetModelFS(fs)

	var cur *frame.Frame
	var product *frame.UnitView
	for step := int32(31); step <= 600 && product == nil; step++ {
		sess.Step(step)
		cur = sess.Snapshot.Current()
		if cur == nil {
			continue
		}
		for i := range cur.Units {
			u := &cur.Units[i]
			if u.Carrier == factoryHandle && u.BuildRemaining > 0 {
				product = u
			}
		}
	}
	if product == nil {
		t.Fatal("the factory never carried a nanoframe; the factory never allocated its product")
	}
	factoryView := (*frame.UnitView)(nil)
	for i := range cur.Units {
		if cur.Units[i].Slot == factoryHandle {
			factoryView = &cur.Units[i]
		}
	}
	if factoryView == nil {
		t.Fatal("the factory is not in the committed frame")
	}

	if dir := os.Getenv("NANOLATHE_SHOT_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	c.cam = &camera.Camera{X: int32(factoryView.X>>16) - 320, Z: int32(factoryView.Z>>16) - 240, ViewW: 640, ViewH: 480, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	ph := product.Slot
	for n := 0; n < 6000 && sess.Units.Unit(ph).Remaining > 0; n++ {
		for i := range sess.Econ.Players {
			sess.Econ.Players[i].Stock[0] = 1e8
			sess.Econ.Players[i].Stock[1] = 1e8
			sess.Econ.Players[i].Capacity[0] = 1e8
			sess.Econ.Players[i].Capacity[1] = 1e8
		}
		sess.Step(int32(cur.Tick) + 1)
		cur = sess.Snapshot.Current()
	}
	if sess.Units.Unit(ph).Remaining > 0 {
		t.Fatal("product never completed")
	}
	sawOverlap, sawExit := false, false
	for n := 0; n < 300; n++ {
		sess.Step(int32(cur.Tick) + 1)
		cur = sess.Snapshot.Current()
		var fac, prod frame.UnitView
		for _, u := range cur.Units {
			if u.Slot == factoryHandle {
				fac = u
			}
			if u.Slot == ph {
				prod = u
			}
		}
		if n != 0 && n != 1 && n != 9 && n != 29 && n != 59 && n != 99 && n != 199 && n != 299 {
			continue
		}
		if !fac.IsFactory || prod.Carrier != 0 || prod.BuildRemaining != 0 {
			t.Fatal("fixture must publish a factory and its completed detached product")
		}
		for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
			c.cam.Scale = scale
			c.cam.X = int32(fac.X>>16) - scale.Inverse(320)
			c.cam.Z = int32(fac.Z>>16) - int32(fac.Y>>17) - scale.Inverse(240)
			disabled := fac
			disabled.IsFactory = false
			before := c.composeUnits(t, cur, []frame.UnitView{disabled, prod})
			after := c.composeUnits(t, cur, []frame.UnitView{fac, prod})
			if factoryFootprintsOverlap(fac, prod) {
				sawOverlap = true
				linked := prod
				linked.Carrier = fac.Slot
				linked.CarriedPiece = 0
				linkedFactory := fac
				linkedFactory.Cargo = []pool.Handle{prod.Slot}
				want := c.composeUnits(t, cur, []frame.UnitView{linkedFactory, linked})
				if !bytes.Equal(after, want) {
					t.Fatalf("exit frame %d differs from the established per-pixel factory composite", n)
				}
				if n == 0 && bytes.Equal(before, after) {
					t.Fatal("fixture no longer exposes the plate occluding the completed product")
				}
			} else {
				sawExit = true
				if !bytes.Equal(before, after) {
					t.Fatal("cleared yard retains factory grouping")
				}
			}

			if dir := os.Getenv("NANOLATHE_SHOT_DIR"); dir != "" {
				writeIndexedPNG(t, c, before, fmt.Sprintf("%s/exit-%03d-x%d-before.png", dir, n, scale/2))
				writeIndexedPNG(t, c, after, fmt.Sprintf("%s/exit-%03d-x%d-staged.png", dir, n, scale/2))
				// The optional capture payload replays the production modern
				// recorder externally, without introducing a device into this test.
				fac.Cargo = nil
				c.geometryOnlyModels = true
				c.enhanced = true
				for _, capture := range []struct {
					name    string
					factory frame.UnitView
				}{{"before", disabled}, {"staged", fac}} {
					c.composeUnits(t, cur, []frame.UnitView{capture.factory, prod})
					type modelCapture struct {
						Geometry   *drawlist.ModelGeometry
						ShadowOnly bool
					}
					commands := c.list.ModelCommands()
					payload := make([]modelCapture, len(commands))
					for i, command := range commands {
						payload[i] = modelCapture{Geometry: command.Geometry, ShadowOnly: command.ShadowOnly}
					}
					data, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(fmt.Sprintf("%s/exit-%03d-x%d-%s.json", dir, n, scale/2, capture.name), data, 0644); err != nil {
						t.Fatal(err)
					}
				}
				c.geometryOnlyModels = false
				c.enhanced = false
			}
		}

	}
	if !sawOverlap || !sawExit {
		t.Fatal("capture sequence must cover overlapping yard and cleared yard")
	}
}

// The same presentation-only group must reach modern's geometry recorder once,
// and must disappear when the yard clears. A cold frame needs no prior cargo
// history (including a newly restored battle).
func TestFactoryOccupantGeometryAndRelease(t *testing.T) {
	c := newPieceFixtureClient(t)
	c.geometryOnlyModels = true
	c.models = map[string]*unitModel{"yard": teamLogoTestModel()}
	views := []frame.UnitView{
		{Slot: 1, InstanceID: 1, Model: "yard", IsFactory: true, ZBuffer: true, FootX: 4, FootZ: 4, MoverMode: moverModeGrounded, X: 100 << 16, Z: 100 << 16},
		{Slot: 2, InstanceID: 2, Model: "yard", BMCode: true, ZBuffer: true, FootX: 2, FootZ: 2, MoverMode: moverModeGrounded, X: 100 << 16, Y: 3 << 16, Z: 90 << 16},
	}
	for _, pose := range []struct {
		offset int64
		cloak  bool
	}{{0, false}, {0, true}, {0, false}, {48, false}, {48, true}} {
		views[1].X = numeric.Fixed((100 + pose.offset) << 16)
		views[1].Cloaked = pose.cloak
		f := frame.Frame{Units: views}
		c.resetListForTest()
		c.drawWorldPass(&f, true)
		c.drawWorldPassB(&f, true)
		commands := c.list.ModelCommands()
		if len(commands) != 2 {
			t.Fatalf("pose %+v: %d model commands, want exactly two", pose, len(commands))
		}
		if pose.offset == 0 && !pose.cloak {
			if !commands[0].ShadowOnly || len(commands[1].Geometry.Children) != 1 {
				t.Fatal("overlapping completed product must have one shadow and one factory-group body")
			}
			if commands[1].Geometry.Children[0].KeyDelta != 3 {
				t.Fatal("factory grouping changed the published world-height delta")
			}
		} else {
			cloakedBodies := 0
			for _, cmd := range commands {
				if cmd.ShadowOnly || len(cmd.Geometry.Children) != 0 {
					t.Fatal("cleared yard or different cloak states must retain independent bodies")
				}
				if cmd.Geometry.Cloaked {
					cloakedBodies++
				}
			}
			wantCloaked := 0
			if pose.cloak {
				wantCloaked = 1
			}
			if cloakedBodies != wantCloaked {
				t.Fatalf("pose %+v: %d cloaked bodies, want only the occupant's current cloak", pose, cloakedBodies)
			}
		}
		if views[1].Carrier != 0 || len(views[0].Cargo) != 0 {
			t.Fatal("presentation changed committed attachment state")
		}
	}
}

// An independent yard occupant keeps its own image-blit cloak state
// [03 R-RAST-01 §7], even when its footprint overlaps a keyed factory image.
func TestFactoryOccupantKeepsIndependentCloak(t *testing.T) {
	c, factory := cachedLiveRegressionSubject(t)
	c.pal = &palette.Tables{}
	for i := range c.pal.Alpha {
		c.pal.Alpha[i] = 42
	}
	factory.IsFactory, factory.FootX, factory.FootZ, factory.MoverMode = true, 4, 4, moverModeGrounded
	occupant := factory
	occupant.Slot, occupant.InstanceID, occupant.IsFactory, occupant.BMCode = 2, 72, false, true
	occupant.X += 24 << 16
	occupant.FootX, occupant.FootZ = 2, 2
	base := &frame.Frame{}
	opaque := c.composeUnits(t, base, []frame.UnitView{factory, occupant})
	for _, cloak := range []bool{true, false, true} {
		occupant.Cloaked = cloak
		got := c.composeUnits(t, base, []frame.UnitView{factory, occupant})
		if cloak {
			independent := factory
			independent.IsFactory = false
			want := c.composeUnits(t, base, []frame.UnitView{independent, occupant})
			if bytes.Equal(want, opaque) {
				t.Fatal("fixture must expose the occupant's cloak blend")
			}
			if !bytes.Equal(got, want) {
				t.Fatal("factory grouping changed the independent occupant's cloak blend")
			}
		} else if !bytes.Equal(got, opaque) {
			t.Fatal("decloaking did not restore the opaque factory composition")
		}
	}
}

func TestFactoryOccupantAdmission(t *testing.T) {
	base := []frame.UnitView{
		{Slot: 1, IsFactory: true, FootX: 4, FootZ: 4, MoverMode: moverModeGrounded},
		{Slot: 2, BMCode: true, FootX: 2, FootZ: 2, MoverMode: moverModeGrounded},
	}
	cases := []struct {
		name   string
		change func([]frame.UnitView)
		omit   int
		want   bool
	}{
		{name: "completed occupant", want: true},
		{name: "unfinished occupant", change: func(v []frame.UnitView) { v[1].BuildRemaining = .5 }},
		{name: "airborne occupant", change: func(v []frame.UnitView) { v[1].MoverMode = 2 }},
		{name: "ordinary structure", change: func(v []frame.UnitView) { v[0].IsFactory = false }},
		{name: "unfinished factory", change: func(v []frame.UnitView) { v[0].BuildRemaining = .5 }},
		{name: "unadmitted occupant", omit: 2},
		{name: "unadmitted factory", omit: 1},
		{name: "touching edge", change: func(v []frame.UnitView) { v[1].X = 48 << 16 }},
		{name: "last subpixel inside", change: func(v []frame.UnitView) { v[1].X = (48 << 16) - 1 }, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := append([]frame.UnitView(nil), base...)
			if tc.change != nil {
				tc.change(v)
			}
			var b worldBuckets
			b.indexChildren(v)
			for i := range v {
				if int(v[i].Slot) != tc.omit {
					b.add(worldDrawable{unit: &v[i], index: int32(i)})
				}
			}
			b.indexFactoryOccupants(worldWindow{rowCount: 1})
			if got := b.isFactoryOccupant(1); got != tc.want {
				t.Fatalf("grouped=%v, want %v", got, tc.want)
			}
		})
	}
}
