package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// dueManager is the smallest fixture in which the retail step is observable
// without a world: both dispatch gates open, every task slot due at tick, and
// a stream to draw from. The step then advances the six fixed cadences and
// takes the explore and rally draws, so "the step ran" and "the step ran with
// the retail draw count" are both assertable
// [08 "Dispatch gates and order sinks"].
func dueManager(tick uint32, seed uint32) (*Manager, *economy.Service, *rng.Simulation) {
	r := rng.NewSimulation(seed)
	m := &Manager{Player: 1, RNG: &r}
	for k := TaskKind(0); k < TaskKindCount; k++ {
		m.Deadlines[k] = tick
	}
	return m, runtimeEconomy(1, 2), &r
}

// The six deadlines the step advances by a fixed cadence when every slot is
// due. Explore and rally advance by a drawn amount and are checked through the
// draw count instead.
var fixedCadences = map[TaskKind]uint32{
	TaskResource:     30,
	TaskWaveA:        300,
	TaskRegroupA:     150,
	TaskConstruction: 90,
	TaskWaveB:        300,
	TaskRegroupB:     150,
}

func assertRetailStepRan(t *testing.T, m *Manager, r *rng.Simulation, tick uint32) {
	t.Helper()
	for slot, cadence := range fixedCadences {
		if m.Deadlines[slot] != tick+cadence {
			t.Fatalf("slot %d deadline=%d, want %d: the retail step did not run", slot, m.Deadlines[slot], tick+cadence)
		}
	}
	if draws := r.Draws(); draws != 2 {
		t.Fatalf("the step took %d draws, want the retail explore and rally pair", draws)
	}
}

// A manager with no planner runs the retail step, and binding RetailPlanner
// explicitly is the same thing: the zero value is the retail answer so a
// fixture, a manager a restore rebuilt and the Strict 3.1 baseline all behave
// as the executable does.
func TestANilPlannerRunsTheRetailStep(t *testing.T) {
	const tick = uint32(100)
	unbound, econ, r := dueManager(tick, 7)
	unbound.Tick(tick, nil, econ)
	assertRetailStepRan(t, unbound, r, tick)

	bound, econ, boundRNG := dueManager(tick, 7)
	bound.Planner = RetailPlanner{}
	bound.Tick(tick, nil, econ)
	assertRetailStepRan(t, bound, boundRNG, tick)
	if bound.Deadlines != unbound.Deadlines || boundRNG.State != r.State {
		t.Fatalf("an explicitly bound retail planner answered differently: %v vs %v", bound.Deadlines, unbound.Deadlines)
	}
}

// recordingPlanner answers the step without doing anything, and records what
// the dispatch handed it. It is zero size, so the counters are package-level:
// an implementation that carried them would allocate when it was bound
// (docs/DESIGN_GAMEPLAY_RULES.md "Allocation rules").
type recordingPlanner struct{}

var (
	recordedManager *Manager
	recordedTick    uint32
	recordedWorld   *units.World
	recordedEconomy *economy.Service
	recordedSteps   int
)

func (recordingPlanner) Step(m *Manager, tick uint32, w *units.World, econ *economy.Service) {
	recordedManager, recordedTick, recordedWorld, recordedEconomy = m, tick, w, econ
	recordedSteps++
}

// A bound planner answers the whole step: the retail body does not also run,
// and the manager it receives is the one the session dispatched, with its own
// state intact for the replacement to read. Nothing draws, because the
// replacement took no draw — the stream's call order belongs to whoever
// answers the step.
func TestABoundPlannerAnswersTheStepInPlaceOfRetail(t *testing.T) {
	const tick = uint32(100)
	m, econ, r := dueManager(tick, 7)
	w := newAIFixtureWorld(1, nil)
	recordedSteps = 0
	m.Planner = recordingPlanner{}
	m.Tick(tick, w, econ)
	if recordedSteps != 1 {
		t.Fatalf("the bound planner was stepped %d times, want once per dispatched tick", recordedSteps)
	}
	if recordedManager != m || recordedTick != tick || recordedWorld != w || recordedEconomy != econ {
		t.Fatal("the step did not receive the dispatched manager, tick, world and economy")
	}
	for slot := TaskKind(0); slot < TaskKindCount; slot++ {
		if m.Deadlines[slot] != tick {
			t.Fatalf("slot %d advanced to %d; the retail step ran as well as the replacement", slot, m.Deadlines[slot])
		}
	}
	if draws := r.Draws(); draws != 0 {
		t.Fatalf("a replacement that drew nothing moved the stream %d times", draws)
	}
}

// A nil manager is still dispatchable, because the session's per-player walk
// holds nil holes and the retail gates used to be the thing that absorbed
// them.
func TestDispatchingANilManagerIsASkip(t *testing.T) {
	var m *Manager
	m.Tick(10, nil, runtimeEconomy(1, 2))
}

// The seam costs one indirect call per player per tick and nothing else:
// binding a zero-size implementation allocates nothing, and neither does
// asking it. The step measured here is the retail one on a fixture manager
// whose own body allocates nothing either — the dispatch entry and the body
// called directly are compared on the same fixture, so a difference would be
// the seam's own cost rather than the step's work
// (docs/DESIGN_GAMEPLAY_RULES.md "Allocation rules").
func TestPlannerDispatchDoesNotAllocate(t *testing.T) {
	const tick = uint32(100)
	for _, tc := range []struct {
		what    string
		planner Planner
	}{
		{"an unbound manager", nil},
		{"the retail planner", RetailPlanner{}},
		{"a replacement planner", recordingPlanner{}},
	} {
		m, econ, _ := dueManager(tick, 7)
		m.Planner = tc.planner
		if allocs := testing.AllocsPerRun(200, func() { m.Tick(tick, nil, econ) }); allocs != 0 {
			t.Fatalf("dispatching to %s allocated %v per tick", tc.what, allocs)
		}
	}
	direct, econ, _ := dueManager(tick, 7)
	body := testing.AllocsPerRun(200, func() { direct.retailStep(tick, nil, econ) })
	seam, econ, _ := dueManager(tick, 7)
	through := testing.AllocsPerRun(200, func() { seam.Tick(tick, nil, econ) })
	if through != body {
		t.Fatalf("the retail step allocated %v through the seam and %v called directly", through, body)
	}
}
