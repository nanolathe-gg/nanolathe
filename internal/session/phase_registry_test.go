package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// visibilityFixture builds a minimal deterministic session with both RNG
// streams seeded, wind bounds retained, and (when withVis) a visibility
// service. Authored after the meteor fixture; no retail bytes.
func visibilityFixture(t *testing.T, withVis bool) *Session {
	t.Helper()
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	cat := &content.Catalog{
		Weapons: map[string]*content.WeaponDef{},
		Units:   map[string]*content.UnitDef{},
		Maps:    map[string]*content.MapHeader{},
		Sides:   []*content.SideDef{{Commander: "armcom"}},
		Sounds:  map[string]*content.SoundCategory{},
	}
	cat.Units[content.CanonicalKey("armcom")] = &content.UnitDef{UnitName: "armcom", MaxDamage: 100, SightDistance: 128}
	uw := newSessionFixtureWorld(8, cat)
	s := &Session{
		Catalog:  cat,
		World:    terrain,
		Units:    uw,
		Econ:     &economy.Service{},
		Wind:     world.NewWind(100, 2000),
		Snapshot: frame.NewBuffer(),
	}
	if withVis {
		s.Vis = visibility.New(terrain, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
		// Author a minimal sight-shape table (ours, not retail bytes): shape
		// index 0 serves radius 128 via the floor(radius/32)-5 quantization.
		// Without a table the service publishes nothing by design.
		shapes := &content.SightShapes{}
		shapes.Shapes = []content.SightShape{{W: 5, H: 5, AnchorX: 2, AnchorY: 2, Opaque: make([]bool, 25)}}
		for i := range shapes.Shapes[0].Opaque {
			shapes.Shapes[0].Opaque[i] = true
		}
		s.Vis.SetShapes(shapes)
	}
	// Production shape: every existing slot carries a controller state. Slot 1
	// is the human/local viewer, slot 0 a computer opponent. Without these the
	// session finds no local player at all and phase 5's sensor seam never
	// runs, which is not a property of any fixture this file means to model
	// [08 "Skirmish configuration"][R-SENSOR-01].
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 2
	s.Econ.Players[1].Exists = true
	s.Econ.Players[1].ControllerState = 1
	s.LocalOwner = 1
	s.ViewingOwner = 1
	s.SeedSessionRNG(12345, 67890)
	return s
}

// TestRetailTickSequenceAndRandomDrawPlacement locks the established retail
// tick order and the locations of the wind and meteor random draws
// [01 §4.4][R-CORE-01 §4.4.1][R-CORE-02]. The labels are the observable
// sequence recorded by the session's optional diagnostic ledger; this test
// does not mirror or drive the sequence a second time.
func TestRetailTickSequenceAndRandomDrawPlacement(t *testing.T) {
	s := visibilityFixture(t, false)
	s.EnablePhaseTrace()
	s.stepAuthoritativePhases(1)

	want := []string{
		"phase1-network", "phase2-units", "phase3-projectiles", "phase4-effects",
		"phase5-orders", "phase6-feature", "phase7-sequences", "phase8-wind",
		"phase9-meteor", "phase10-shake", "phase11-objects", "phase12-cadence",
	}
	got := s.PhaseTrace()
	if len(got) != len(want) {
		t.Fatalf("retail tick sequence = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tick step %d = %q, want %q [01 §4.4]", i+1, got[i], want[i])
		}
	}

	// Draw deltas are cumulative totals; consecutive differences identify the
	// draws consumed by each named step. At tick 1 the zero deadline runs the
	// full wind chain. The meteor scheduler is due at tick 1 and consumes its
	// four scheduling draws even though this fixture has no storm weapon
	// [06 §6.5][R-CORE-01 §4.4.1].
	assertDrawDelta := func(tick uint32, step string, wantCRT, wantSim uint64) {
		t.Helper()
		deltas := s.PhaseDrawDeltas()
		prevSim, prevCRT := uint64(0), uint64(0)
		for _, d := range deltas {
			simDelta := d.SimDelta - prevSim
			crtDelta := d.CrtDelta - prevCRT
			prevSim, prevCRT = d.SimDelta, d.CrtDelta
			if d.Phase == step {
				if crtDelta != wantCRT || simDelta != wantSim {
					t.Fatalf("tick %d %s = %d CRT + %d sim, want %d + %d", tick, step, crtDelta, simDelta, wantCRT, wantSim)
				}
				return
			}
		}
		t.Fatalf("tick %d: %s missing from recorded sequence", tick, step)
	}
	assertDrawDelta(1, "phase8-wind", 1, 2)
	assertDrawDelta(1, "phase9-meteor", 4, 0)

	// Wind is not due on the next tick (strict deadline). The disabled shower
	// has zero interval in this fixture, so its next deadline is due again and
	// it consumes the scheduling block before clearing Active [06 §6.5].
	s.EnablePhaseTrace()
	s.stepAuthoritativePhases(2)
	assertDrawDelta(2, "phase8-wind", 0, 0)
	assertDrawDelta(2, "phase9-meteor", 4, 0)
}

func TestIdleUnitKeepsOrdersNilAcrossTicks(t *testing.T) {
	s := visibilityFixture(t, false)
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	h, err := s.Units.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create idle unit: %v", err)
	}
	u := s.Units.Unit(h)
	if u == nil {
		t.Fatal("created idle unit is missing")
	}
	for tick := uint32(1); tick <= 5; tick++ {
		s.stepAuthoritativePhases(tick)
		if u.Orders != nil {
			t.Fatalf("idle unit acquired an order queue on tick %d", tick)
		}
	}
}

// TestCommittedFrameSamplingDoesNotAffectSimulation locks the retail
// presentation boundary: the committed frame and
// both RNG streams are identical whether the presentation path samples the
// committed frame zero, one, or two times per tick (the headless client never
// samples; a windowed one samples per rendered frame). Sampling consumes no
// RNG [I6][R-CORE-02] — a render cadence change cannot perturb the
// simulation. (The committed frame itself is published exactly once per
// sub-tick; re-publishing the same tick is a frame-buffer contract error.)
func TestCommittedFrameSamplingDoesNotAffectSimulation(t *testing.T) {
	type result struct {
		simState  uint32
		simDraws  uint64
		crtState  uint32
		crtDraws  uint64
		frameTick uint32
		shakes    [7]int32 // offX, offY, duration, remaining, ampX, ampY, active-as-int
		wind      [4]int32
	}
	run := func(t *testing.T, samplesPerTick int) result {
		t.Helper()
		s := visibilityFixture(t, false)
		for tick := uint32(1); tick <= 24; tick++ {
			s.stepAuthoritativePhases(tick)
			s.publishSnapshot(tick)
			for i := 0; i < samplesPerTick; i++ {
				// Presentation sampling the committed frame: read-only.
				f := s.Snapshot.Current()
				if f == nil || f.Tick != tick {
					t.Fatalf("sample %d of tick %d: frame missing or stale", i, tick)
				}
			}
		}
		f := s.Snapshot.Current()
		if f == nil {
			t.Fatal("no committed frame")
		}
		active := int32(0)
		if f.ShakeActive {
			active = 1
		}
		return result{
			simState:  s.SimRNG().State,
			simDraws:  s.SimRNG().Draws(),
			crtState:  s.CrtRNG().State,
			crtDraws:  s.CrtRNG().Draws(),
			frameTick: f.Tick,
			shakes:    [7]int32{f.ShakeOffsetX, f.ShakeOffsetY, f.ShakeDuration, f.ShakeRemaining, f.ShakeAmpX, f.ShakeAmpY, active},
			wind:      [4]int32{s.Wind.Strength, int32(s.Wind.Heading), int32(s.Wind.NextChange), int32(s.Wind.LastChange)},
		}
	}
	base := run(t, 0)
	for _, samples := range []int{1, 2} {
		other := run(t, samples)
		if other != base {
			t.Fatalf("render cadence %d samples/tick changed authoritative state: %+v vs %+v", samples, other, base)
		}
	}
}

// TestMovedUnitRefreshesVisibilitySameTick locks [R-CORE-01 §4.4.1]: a unit
// moved before a sub-tick is re-stamped by that tick's visibility publication
// (the dirty check detects the changed stamp cell), an unchanged unit's stamp key is
// untouched, and coverage is queryable right after the same committed tick —
// and there is no second visibility publication after the tick's regular work.
func TestMovedUnitRefreshesVisibilitySameTick(t *testing.T) {
	s := visibilityFixture(t, true)
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	h0, err := s.Units.Create(def, 0, cell(96), 0, cell(96))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	h1, err := s.Units.Create(def, 1, cell(224), 0, cell(224))
	if err != nil {
		t.Fatalf("create stationary: %v", err)
	}
	mover := s.Units.Unit(h0)
	stationary := s.Units.Unit(h1)
	if mover == nil || stationary == nil {
		t.Fatal("fixture units missing")
	}

	// Battle-entry-style bulk publish at the units' current cells.
	publishVisibilityForAll(s)
	stamp0 := s.visStamps[int(h0)]
	stamp1 := s.visStamps[int(h1)]
	if stamp0 == (visStamp{}) {
		t.Fatalf("mover stamp not published at entry: %+v", stamp0)
	}

	// Move the mover before one tick. The dirty check must re-stamp it during
	// that same tick's visibility publication.
	mover.X = cell(208)
	s.stepAuthoritativePhases(1)
	s.publishSnapshot(1)

	newStamp, seen := s.visStamps[int(h0)]
	if !seen {
		t.Fatal("mover lost its stamp after the step")
	}
	if newStamp == stamp0 {
		t.Fatal("mover stamp not refreshed after same-tick movement [R-CORE-01 §4.4.1]")
	}
	wantCx, wantCz := observerTile(mover, heightByteAt(mover, seaLevelFor(s)))
	if newStamp.cx != wantCx || newStamp.cz != wantCz {
		t.Fatalf("mover stamp cell = (%d,%d), want (%d,%d)", newStamp.cx, newStamp.cz, wantCx, wantCz)
	}
	// The unchanged unit's stored stamp key is identical (writes nothing).
	if s.visStamps[int(h1)] != stamp1 {
		t.Fatalf("stationary unit stamp changed: %+v -> %+v", stamp1, s.visStamps[int(h1)])
	}

	// Coverage is queryable immediately after the same committed tick.
	if !s.Vis.VisiblePoint(visibility.PlayerID(0), mover.X, mover.Y, mover.Z) {
		t.Fatal("mover's new cell not covered in the same tick's publication")
	}

	// The step publishes exactly one committed frame.
	if tick, ok := s.Snapshot.PublishedTick(); !ok || tick != 1 {
		t.Fatalf("published tick = %d (%v), want 1", tick, ok)
	}
}

// TestSensorPassUsesFreshLocalCoverage locks the [R-SENSOR-01] seam: the
// sensor/deadline pass runs for the local viewing player after that player's
// fresh coverage is stamped. The local viewer here is player 1 — not the
// lowest index — so the moved observer's coverage can admit a seen marker for
// an enemy cell in the same tick.
func TestSensorPassUsesFreshLocalCoverage(t *testing.T) {
	s := visibilityFixture(t, true)
	s.LocalOwner = 1
	s.ViewingOwner = 1
	s.Vis.SetLocal(visibility.PlayerID(1)) // mirrors composition's local-viewer binding
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	cell := func(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

	// Local viewer's observer (player 1) and an enemy unit (player 0) far
	// outside its initial coverage. Pixels stay inside the fixture's 32-unit
	// coverage grid (64 cells → 32 grid columns).
	observer, err := s.Units.Create(def, 1, cell(96), 0, cell(96))
	if err != nil {
		t.Fatalf("create local observer: %v", err)
	}
	enemy, err := s.Units.Create(def, 0, cell(896), 0, cell(384))
	if err != nil {
		t.Fatalf("create enemy: %v", err)
	}

	publishVisibilityForAll(s)
	if s.Vis.VisiblePoint(visibility.PlayerID(1), cell(896), 0, cell(384)) {
		t.Fatal("precondition: enemy cell covered before the observer moves")
	}

	// Move the local observer next to the enemy before the tick:
	// its stamp cell becomes (26,12), so the enemy's grid point (28,12) falls
	// inside the 5x5 sight shape — but only after player 1's stamp sweep
	// re-stamps it.
	s.Units.Unit(observer).X = cell(832)
	s.Units.Unit(observer).Z = cell(384)
	s.stepAuthoritativePhases(1)

	if got := s.visStatus[int(enemy)]; got&visibility.SeenBit == 0 {
		t.Fatalf("enemy SeenBit = %#x after the step, want set: the sensor pass must run after the local player's stamp sweep within the same tick [R-SENSOR-01]", got)
	}
}
