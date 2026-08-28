package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// paritySpineFixture builds a minimal deterministic session with both RNG
// streams seeded, wind bounds retained, and (when withVis) a visibility
// service. Authored after the meteor fixture; no retail bytes.
func paritySpineFixture(t *testing.T, withVis bool) *Session {
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
	uw := units.NewSliced(8, cat)
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
	s.Econ.Players[0].Exists = true
	s.Econ.Players[1].Exists = true
	s.SeedSessionRNG(12345, 67890)
	return s
}

// TestPhaseRegistryOrderAndDrawDeltas locks the DET-02 registry: one tick
// runs exactly the twelve named phases in [01 §4.4] order, and the per-phase
// RNG ledger shows the corrected draw placement — phase 8 consumes the wind
// chain (1 CRT interval + 2 sim at tick 1 for nonzero bounds) and every other
// phase consumes nothing in this fixture (phase 9's meteor scheduler is
// nil-world blocked before any draws) [R-CORE-01 §4.4.1][R-CORE-02].
func TestPhaseRegistryOrderAndDrawDeltas(t *testing.T) {
	s := paritySpineFixture(t, false)
	s.EnablePhaseTrace()
	s.stepAuthoritativePhases(1)

	want := []string{
		"phase1-network", "phase2-units", "phase3-projectiles", "phase4-effects",
		"phase5-orders", "phase6-feature", "phase7-sequences", "phase8-wind",
		"phase9-meteor", "phase10-shake", "phase11-objects", "phase12-cadence",
	}
	got := s.PhaseTrace()
	if len(got) != len(want) {
		t.Fatalf("phase trace = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("phase %d = %q, want %q [01 §4.4]", i+1, got[i], want[i])
		}
	}

	// Deltas are cumulative totals; consecutive differences give per-phase
	// consumption.
	// Tick 1: deadline zeroed at entry, so the full wind chain fires; the
	// meteor scheduler (weapon absent in this fixture) is due at tick 1
	// (next-strike 0, non-strict) and consumes its four scheduling draws even
	// though the storm is disabled [06 §6.5][R-CORE-01 §4.4.1].
	assertPhaseDelta := func(tick uint32, phase string, wantCrt, wantSim uint64) {
		t.Helper()
		deltas := s.PhaseDrawDeltas()
		prevSim, prevCrt := uint64(0), uint64(0)
		for _, d := range deltas {
			simDelta := d.SimDelta - prevSim
			crtDelta := d.CrtDelta - prevCrt
			prevSim, prevCrt = d.SimDelta, d.CrtDelta
			if d.Phase == phase {
				if crtDelta != wantCrt || simDelta != wantSim {
					t.Fatalf("tick %d %s = %d CRT + %d sim, want %d + %d", tick, phase, crtDelta, simDelta, wantCrt, wantSim)
				}
				return
			}
		}
		t.Fatalf("tick %d: phase %s not in trace", tick, phase)
	}
	assertPhaseDelta(1, "phase8-wind", 1, 2)
	assertPhaseDelta(1, "phase9-meteor", 4, 0)

	// Tick 2: wind not due (strict gate); the armed-then-expired meteor
	// window deactivates without draws.
	s.EnablePhaseTrace()
	s.stepAuthoritativePhases(2)
	assertPhaseDelta(2, "phase8-wind", 0, 0)
	assertPhaseDelta(2, "phase9-meteor", 0, 0)
}

// TestRenderCadenceInvariance locks deliverable (i): the committed frame and
// both RNG streams are identical whether the presentation path samples the
// committed frame zero, one, or two times per tick (the headless client never
// samples; a windowed one samples per rendered frame). Sampling consumes no
// RNG [I6][R-CORE-02] — a render cadence change cannot perturb the
// simulation. (The committed frame itself is published exactly once per
// sub-tick; re-publishing the same tick is a frame-buffer contract error.)
func TestRenderCadenceInvariance(t *testing.T) {
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
		s := paritySpineFixture(t, false)
		for tick := uint32(1); tick <= 24; tick++ {
			s.stepAuthoritativePhases(tick)
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

// TestVisibilityStampsInsidePhase5 locks DET-06 [R-CORE-01 §4.4.1]: a unit
// moved before a sub-tick is re-stamped by THAT tick's phase 5 (the dirty
// check detects the changed stamp cell), an unchanged unit's stamp key is
// untouched, and coverage is queryable right after the same committed tick —
// there is no post-phase-12 visibility pass anymore.
func TestVisibilityStampsInsidePhase5(t *testing.T) {
	s := paritySpineFixture(t, true)
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

	// Move the mover (as phase 2 movement would) and step ONE tick. The dirty
	// check must re-stamp it in that same tick's phase 5.
	mover.X = cell(208)
	s.stepAuthoritativePhases(1)

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

	// The step published exactly one committed frame; the registry carries no
	// post-12 visibility call (see TestPhaseRegistryOrderAndDrawDeltas).
	if tick, ok := s.Snapshot.PublishedTick(); !ok || tick != 1 {
		t.Fatalf("published tick = %d (%v), want 1", tick, ok)
	}
}
