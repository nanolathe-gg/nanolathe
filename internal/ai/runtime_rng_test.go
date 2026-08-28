package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
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

	// The small-group branch runs one baseline attempt plus its binary choice,
	// sampling width/8 and height/8 for each attempt.
	probe := rng.NewSimulation(1)
	trials := probe.Uint32n(2)
	wantSmall := uint64(1 + (trials+2)*2)
	sim := rng.NewSimulation(1)
	m := &Manager{RNG: &sim, Terrain: terrain, GroupExplore: []pool.Handle{1}}
	m.doExplore(0, w, nil)
	if got := sim.Draws(); got != wantSmall {
		t.Fatalf("small explore body draws=%d, want %d", got, wantSmall)
	}

	sim = rng.NewSimulation(1)
	m = &Manager{RNG: &sim, Terrain: terrain, GroupExplore: []pool.Handle{1, 2, 3, 4, 5}}
	m.doExplore(0, w, nil)
	if got := sim.Draws(); got != 5 {
		t.Fatalf("large explore body draws=%d, want 5", got)
	}

	// Find a seed whose first bounded draw enters rally's drift seed branch:
	// gate 10 followed by two independent 16-bit draws.
	seed := uint32(1)
	for {
		probe = rng.NewSimulation(seed)
		if probe.Uint32n(10) == 0 {
			break
		}
		seed++
	}
	sim = rng.NewSimulation(seed)
	m = &Manager{RNG: &sim, GroupRally: []pool.Handle{1}, rallyScore: 17, rallyNextScore: 23}
	m.doRally(0, w, nil)
	probe = rng.NewSimulation(seed)
	probe.Uint32n(10)
	probe.Uint32n(65536)
	probe.Uint32n(65536)
	probe.Uint32n(17)
	probe.Uint32n(23)
	if got := sim.Draws(); got != 5 || sim.State != probe.State {
		t.Fatalf("rally body ledger draws=%d state=%d, want 5 state=%d", got, sim.State, probe.State)
	}
}
