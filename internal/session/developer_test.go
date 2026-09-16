package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func developerFixture(t *testing.T) (*Session, pool.Handle) {
	t.Helper()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "mover"}, UnitName: "MOVER", Name: "Mover", BMCode: 1, FootprintX: 1, FootprintZ: 1, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"mover": def}, BuildMenus: map[string]*content.BuildMenuPage{"mover": {Buttons: []string{"second", "first"}}}}
	terrain := &world.Terrain{CellW: 8, CellH: 8, Plot: make([]world.PlotCell, 64)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	s := &Session{Catalog: cat, World: terrain, Units: newSessionFixtureWorld(4, cat), Snapshot: frame.NewBuffer(), Clock: &clock.State{}, Econ: &economy.Service{}, ViewingOwner: 1}
	s.SeedSessionRNG(7, 11)
	s.Vis = visibility.New(terrain, visibility.ModeCurrentEnabled|visibility.ModeHistoryEnabled)
	h, err := s.Units.Create(def, 0, 32<<16, 0, 48<<16)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	u.Flags |= 0x10
	s.Movement = movement.NewSystem(terrain, movement.Template(), movement.NewOccupancyGrid())
	s.Movement.BindWorld(s.Units)
	s.Movement.EnsureUnit(u)
	s.Movement.Routes[h].Publish([]movement.Point{{X: 32, Z: 48}, {X: 64, Z: 80}})
	s.Vis.ByteGrid(0)[0], s.Vis.ByteGrid(1)[0] = 7, 9
	return s, h
}

func TestDeveloperPublicationDetachedAndOptIn(t *testing.T) {
	s, h := developerFixture(t)
	s.publishSnapshot(1)
	if s.Snapshot.Current().Developer != nil {
		t.Fatal("diagnostics enabled by default")
	}
	s.SetDeveloperDiagnostics(true)
	if s.Snapshot.Current().Developer != nil {
		t.Fatal("request changed committed frame")
	}
	s.publishSnapshot(2)
	f := s.Snapshot.Current()
	d := f.Developer
	if d.Tick != 2 || d.MovementSubject != h || d.Coverage[0] != 7 || d.Units[0].InstanceID != f.Units[0].InstanceID {
		t.Fatal("wrong capture identity, local selection or coverage")
	}
	if len(d.Units[0].Builds) != 2 || d.Units[0].Builds[0].Name != "second" || d.Units[0].Builds[0].ScoreAvailable {
		t.Fatal("build order or availability changed")
	}
	if !d.Units[0].MovementAvailable || !d.Units[0].HasWaypoint || len(d.Units[0].Route) != 2 {
		t.Fatal("missing follower")
	}
	anchor, _, _, _ := s.Movement.CommittedFootprint(h)
	if d.Units[0].FootprintX != anchor.X || d.Units[0].FootprintZ != anchor.Z {
		t.Fatal("footprint uses center instead of anchor")
	}
	s.World.Plot[0].SetMetal(93)
	s.World.Plot[0].SetStructureYard(true)
	s.Vis.ByteGrid(0)[0] = 4
	s.Movement.Routes[h].Points[0].X = 77
	s.Catalog.BuildMenus["mover"].Buttons[0] = "changed"
	if d.Cells[0].Metal != 0 || d.Cells[0].Building || d.Coverage[0] != 7 || d.Units[0].Route[0].X != 32 || d.Units[0].Builds[0].Name != "second" {
		t.Fatal("live storage leaked into frame")
	}
	s.publishSnapshot(3)
	if d.Cells[0].Metal != 0 || d.Coverage[0] != 7 {
		t.Fatal("other frame slot rewrote committed diagnostics")
	}
	if !s.Snapshot.Current().Developer.Cells[0].Building {
		t.Fatal("building yard bit not published")
	}
	s.SetDeveloperDiagnostics(false)
	s.publishSnapshot(4)
	if s.Snapshot.Current().Developer != nil {
		t.Fatal("opt-out retained public diagnostics")
	}
	s.SetDeveloperDiagnostics(true)
	s.publishSnapshot(5)
	s.publishSnapshot(6)
	if s.Snapshot.Current().Developer != d {
		t.Fatal("slot did not reuse diagnostic storage")
	}
	next := uint32(7)
	if allocs := testing.AllocsPerRun(20, func() { s.publishSnapshot(next); next++ }); allocs != 0 {
		t.Fatalf("warm publication allocated %v times", allocs)
	}
}

func TestDeveloperObservationLeavesBothGameplayModesUnchanged(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			a, ha := developerFixture(t)
			b, hb := developerFixture(t)
			a.Gameplay, b.Gameplay = mode, mode
			b.SetDeveloperDiagnostics(true)
			a.Wind, b.Wind = world.NewWind(100, 2000), world.NewWind(100, 2000)
			a.RequestShake(5, 20)
			b.RequestShake(5, 20)
			for tick := uint32(1); tick <= 10; tick++ {
				a.stepAuthoritativePhases(tick)
				b.stepAuthoritativePhases(tick)
				a.publishSnapshot(tick)
				b.publishSnapshot(tick)
				ua, ub := a.Units.Unit(ha), b.Units.Unit(hb)
				if ua.X != ub.X || ua.Y != ub.Y || ua.Z != ub.Z || ua.Health != ub.Health || ua.Flags != ub.Flags || ua.Move != ub.Move || ua.Remaining != ub.Remaining || !reflect.DeepEqual(ua.Slots, ub.Slots) {
					t.Fatal("unit state changed")
				}
				if !reflect.DeepEqual(a.World.Plot, b.World.Plot) || !reflect.DeepEqual(a.Movement.Routes, b.Movement.Routes) || !reflect.DeepEqual(a.Movement.Collisions, b.Movement.Collisions) || !reflect.DeepEqual(a.Econ.Players, b.Econ.Players) || !reflect.DeepEqual(a.Vis.WordMask(), b.Vis.WordMask()) || !reflect.DeepEqual(a.Vis.ByteGrid(0), b.Vis.ByteGrid(0)) {
					t.Fatal("terrain, movement, resources or coverage changed")
				}
				if *a.SimRNG() != *b.SimRNG() || *a.CrtRNG() != *b.CrtRNG() {
					t.Fatal("RNG state or draw count changed")
				}
				if !reflect.DeepEqual(a.Wind, b.Wind) || a.shakeRemaining != b.shakeRemaining || a.shakeOffsetX != b.shakeOffsetX || a.shakeOffsetY != b.shakeOffsetY {
					t.Fatal("wind or camera shake changed")
				}
				fa, fb := *a.Snapshot.Current(), *b.Snapshot.Current()
				fb.Developer = nil
				// Frame's private retained storage is intentionally different;
				// compare the ordinary public observations explicitly.
				if !reflect.DeepEqual(fa.Units, fb.Units) || !reflect.DeepEqual(fa.OrderQueues, fb.OrderQueues) || !reflect.DeepEqual(fa.Economy, fb.Economy) || fa.Players != fb.Players {
					t.Fatal("ordinary publication changed")
				}
			}
			if a.SimRNG().Draws() == 0 || a.CrtRNG().Draws() == 0 {
				t.Fatal("fixture did not exercise both random streams")
			}
		})
	}
}
