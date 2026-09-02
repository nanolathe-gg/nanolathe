package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestStrategicConstructorDrawLedger(t *testing.T) {
	r := rng.NewSimulation(123)
	s := &Strategic{}
	if !s.InitializeRandomState(&r) {
		t.Fatal("strategic random state did not initialize")
	}
	probe := rng.NewSimulation(123)
	w0 := probe.Uint32n(10)
	h0 := probe.Uint32n(3)
	negW, negH := 11+w0, 11+h0
	w1 := probe.Uint32n(negW)
	h1 := probe.Uint32n(negH)
	w2 := probe.Uint32n(20)
	h2 := probe.Uint32n(3)
	posW, posH := 14+w2, 14+h2
	w3 := probe.Uint32n(posW)
	h3 := probe.Uint32n(posH)
	want := [8]uint32{w0, h0, w1, h1, w2, h2, w3, h3}
	if s.setupDraws != want {
		t.Fatalf("constructor draws=%v, want %v", s.setupDraws, want)
	}
	if s.negRegionW != negW || s.negRegionH != negH || s.posRegionW != posW || s.posRegionH != posH {
		t.Fatalf("derived region bounds=%d,%d,%d,%d, want %d,%d,%d,%d", s.negRegionW, s.negRegionH, s.posRegionW, s.posRegionH, negW, negH, posW, posH)
	}
	if got := r.Draws(); got != 8 {
		t.Fatalf("constructor draw count=%d, want 8", got)
	}
	if s.InitializeRandomState(&r) {
		t.Fatal("constructor randomization ran twice")
	}
	if got := r.Draws(); got != 8 {
		t.Fatalf("second initialization changed draw count to %d", got)
	}
}

func TestUnitLossThrottleDrawAndNilFailClosed(t *testing.T) {
	r := rng.NewSimulation(321)
	m := &Manager{RNG: &r}
	m.RecordUnitLoss(100)
	if got := r.Draws(); got != 1 {
		t.Fatalf("unit-loss draw count=%d, want 1", got)
	}
	if got := m.UnitLossDeadline(); got < 130 || got > 429 {
		t.Fatalf("unit-loss deadline=%d, want [130,429]", got)
	}

	noRNG := &Manager{unitLossDeadline: 77}
	noRNG.RecordUnitLoss(100)
	if got := noRNG.UnitLossDeadline(); got != 77 {
		t.Fatalf("nil RNG changed unit-loss deadline to %d", got)
	}
}

func TestStrategicRefreshNilRNGDoesNotMutate(t *testing.T) {
	s := &Strategic{LastRefreshTick: 0}
	if s.MaybeRefresh(30, nil, 0, nil) {
		t.Fatal("nil RNG refresh should fail closed")
	}
	if s.LastRefreshTick != 0 {
		t.Fatalf("nil RNG changed refresh tick to %d", s.LastRefreshTick)
	}
}

func TestExploreAndRallyBodyDraws(t *testing.T) {
	terrain := &world.Terrain{CellW: 32, CellH: 24}
	w := units.NewSliced(1, nil)

	// The small-group branch runs two or three legs around the strategic
	// centre. Width and height are full world extents divided by eight
	// [08 R-AI-01 §6].
	probe := rng.NewSimulation(1)
	trials := probe.Uint32n(2)
	wantSmall := uint64(1 + (trials+2)*2)
	sim := rng.NewSimulation(1)
	m := &Manager{RNG: &sim, Terrain: terrain, GroupExplore: []pool.Handle{1}}
	m.Strategic.CenterX, m.Strategic.CenterZ = numeric.FixedFromInt(100), numeric.FixedFromInt(100)
	m.doExplore(0, w, nil)
	if got := sim.Draws(); got != wantSmall {
		t.Fatalf("small explore body draws=%d, want %d", got, wantSmall)
	}

	sim = rng.NewSimulation(1)
	m = &Manager{RNG: &sim, Terrain: terrain, GroupExplore: []pool.Handle{1, 2, 3, 4, 5}}
	m.doExplore(0, w, nil)
	if got := sim.Draws(); got != 3 {
		t.Fatalf("large explore body draws=%d, want 3", got)
	}

	// Find a seed whose first body draw enters rally's drift branch. That arm
	// draws one 16-bit angle, then a validating probe draws incumbent and
	// challenger scores in that order [08 R-AI-01 §7].
	seed := uint32(1)
	for {
		probe = rng.NewSimulation(seed)
		if probe.Uint32n(10) == 0 {
			break
		}
		seed++
	}
	probe = rng.NewSimulation(seed)
	probe.Uint32n(10)
	angle := numeric.Angle(probe.Uint32n(65536))
	centreX := numeric.FixedFromInt(int64(terrain.CellW * 8))
	centreZ := numeric.FixedFromInt(int64(terrain.CellH * 8))
	probeX := centreX - numeric.Fixed(numeric.MulRound(numeric.Sin(angle), int32(numeric.FixedFromInt(320))))
	probeZ := centreZ - numeric.Fixed(numeric.MulRound(numeric.Cos(angle), int32(numeric.FixedFromInt(320))))
	attackerDef := &content.UnitDef{UnitName: "attacker", CanAttack: true, CanMove: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"attacker": attackerDef}}
	w = newAIFixtureWorld(4, cat)
	attacker, err := w.Create(attackerDef, 0, probeX, 0, probeZ)
	if err != nil {
		t.Fatal(err)
	}
	w.Unit(attacker).Group = 9
	w.Unit(attacker).Flags |= units.ArmedStatus
	sim = rng.NewSimulation(seed)
	m = &Manager{
		RNG: &sim, GroupRally: []pool.Handle{attacker},
	}
	if !m.InitializeBattleState(terrain, RallyBattleBindings{
		ProbeKnown:     func(uint8, numeric.Fixed, numeric.Fixed, numeric.Fixed) bool { return true },
		ShotTimeAdmits: func(*units.Unit, numeric.Fixed, numeric.Fixed, numeric.Fixed) bool { return true },
	}) {
		t.Fatal("explicit rally battle initialization failed")
	}
	m.rallyBestX = centreX
	m.rallyBestZ = centreZ
	m.rallyProbeX = centreX
	m.rallyProbeZ = centreZ
	m.rallyBestScore = 17
	m.rallyTargets = []pool.Handle{attacker}
	m.Strategic.SingleVectors = map[string]int8{"attacker": 23}
	m.doRally(0, w, nil)
	probe = rng.NewSimulation(seed)
	probe.Uint32n(10)
	probe.Uint32n(65536)
	incumbentDraw := probe.Uint32n(17)
	challengerDraw := probe.Uint32n(23)
	if got := sim.Draws(); got != 4 || sim.State != probe.State {
		t.Fatalf("rally body ledger draws=%d state=%d, want 4 state=%d", got, sim.State, probe.State)
	}
	adopted := incumbentDraw < challengerDraw
	if got := m.rallyBestScore == 23 && m.rallyBestX == probeX && m.rallyBestZ == probeZ; got != adopted {
		t.Fatalf("rally strict adoption=%v, want %v for incumbent draw %d challenger draw %d", got, adopted, incumbentDraw, challengerDraw)
	}
}
