package headless

import (
	"testing"
	"time"
)

// TestSimBenchOptionDefaults locks the scene's declared window and roster
// arithmetic without touching retail assets. The roster totals matter: the
// scene claims 250 units per team, and a table edit that quietly breaks that
// changes the workload two runs are compared on.
func TestSimBenchOptionDefaults(t *testing.T) {
	var opts SimBenchOptions
	opts.applyDefaults()
	if opts.Map != SimBenchDefaultMap {
		t.Fatalf("map = %q, want %q", opts.Map, SimBenchDefaultMap)
	}
	if opts.WarmupTicks != SimBenchDefaultWarmupTicks || opts.MeasureTicks != SimBenchDefaultMeasureTicks {
		t.Fatalf("window = %d + %d ticks, want %d + %d", opts.WarmupTicks, opts.MeasureTicks, SimBenchDefaultWarmupTicks, SimBenchDefaultMeasureTicks)
	}
	if opts.UnitLimit < simBenchUnitsPerTeam {
		t.Fatalf("unit limit %d is below the %d units the scene places per team", opts.UnitLimit, simBenchUnitsPerTeam)
	}
	if total := rosterTotal(simBenchBuildings) + rosterTotal(simBenchMobiles); total != simBenchUnitsPerTeam {
		t.Fatalf("roster totals %d units per team, want %d", total, simBenchUnitsPerTeam)
	}
	for _, row := range append(append([]simBenchRosterEntry(nil), simBenchBuildings...), simBenchMobiles...) {
		if row.Arm == "" || row.Core == "" || row.Count <= 0 {
			t.Fatalf("roster row %+v is incomplete", row)
		}
	}
}

// TestSimBenchConfigIsThreeHostileComputerArmies locks the setup record the
// scene composes: three computer slots in three distinct ally groups, plus the
// human slot retail's lobby rule requires, in a fourth group of its own.
func TestSimBenchConfigIsThreeHostileComputerArmies(t *testing.T) {
	cfg := simBenchConfig("fixture", 400)
	if cfg.NumPlayers != simBenchTeams+1 {
		t.Fatalf("NumPlayers = %d, want %d", cfg.NumPlayers, simBenchTeams+1)
	}
	if !cfg.Players[simBenchHumanSlot].IsHuman() {
		t.Fatalf("slot %d controller = %d, want the human the lobby rule requires", simBenchHumanSlot, cfg.Players[simBenchHumanSlot].Controller)
	}
	groups := map[int]int{cfg.Players[simBenchHumanSlot].AllyGroup: simBenchHumanSlot}
	for team := 0; team < simBenchTeams; team++ {
		slot := simBenchFirstAISlot + team
		if !cfg.Players[slot].IsComputer() {
			t.Fatalf("slot %d controller = %d, want a computer army", slot, cfg.Players[slot].Controller)
		}
		if other, clash := groups[cfg.Players[slot].AllyGroup]; clash {
			t.Fatalf("slot %d shares ally group %d with slot %d; the armies must be mutually hostile", slot, cfg.Players[slot].AllyGroup, other)
		}
		groups[cfg.Players[slot].AllyGroup] = slot
		if cfg.Players[slot].Metal <= 0 || cfg.Players[slot].Energy <= 0 {
			t.Fatalf("slot %d starts with metal %d energy %d; construction would stall", slot, cfg.Players[slot].Metal, cfg.Players[slot].Energy)
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

// TestSimBenchPhaseTimerAttributesBoundaries locks the host timer's shape: the
// twelve phase slots plus one tail slot, filled in arrival order, with the
// whole tick accounted for. It uses a synthetic boundary sequence, not a
// session, so it needs no assets.
func TestSimBenchPhaseTimerAttributesBoundaries(t *testing.T) {
	timer := newSimBenchPhaseTimer()
	names := []string{
		"phase1-network", "phase2-units", "phase3-projectiles", "phase4-effects",
		"phase5-orders", "phase6-feature", "phase7-sequences", "phase8-wind",
		"phase9-meteor", "phase10-shake", "phase11-objects", "phase12-cadence",
	}
	for tick := 0; tick < 3; tick++ {
		timer.beginTick()
		start := time.Now()
		for _, name := range names {
			timer.observe(name, uint32(tick))
		}
		timer.endTick(start, time.Since(start))
	}
	summary := timer.summary(3)
	if len(summary) != len(names)+1 {
		t.Fatalf("summary has %d rows, want %d phases plus one tail", len(summary), len(names))
	}
	for i, name := range names {
		if summary[i].Phase != name {
			t.Fatalf("row %d = %q, want %q", i, summary[i].Phase, name)
		}
	}
	var share float64
	for _, row := range summary {
		share += row.SharePercent
	}
	if share < 99.9 || share > 100.1 {
		t.Fatalf("phase shares sum to %.3f%%, want 100%%", share)
	}
}

// TestSimBenchCensusTicksAreOrderedAndInside locks the interior census
// schedule: the open and close samples are taken by the runner, so the
// schedule holds only the interior ticks and every one must land inside the
// window.
func TestSimBenchCensusTicksAreOrderedAndInside(t *testing.T) {
	ticks := simBenchCensusTicks(3000, 7)
	if len(ticks) != 5 {
		t.Fatalf("interior samples = %d, want 5", len(ticks))
	}
	previous := uint32(0)
	for _, tick := range ticks {
		if tick <= previous || tick >= 3000 {
			t.Fatalf("census schedule %v is not strictly increasing inside the window", ticks)
		}
		previous = tick
	}
	if got := simBenchCensusTicks(3000, 2); got != nil {
		t.Fatalf("two samples need no interior schedule, got %v", got)
	}
}

// TestSimBenchRestampCorrelation locks the reporting contract of the per-tick
// class-layer restamp series: a rebuild is attributed to the tick it ran in,
// the slow/fast split uses the declared threshold, and the two means separate
// the populations. Synthetic series, no session.
func TestSimBenchRestampCorrelation(t *testing.T) {
	millis := []float64{5, 20, 6, 4, 30, 8}
	restamps := []uint32{0, 1, 0, 0, 2, 1}
	got := summarizeSimBenchRestamps(millis, restamps)
	if got.Total != 4 || got.TicksWithRestamp != 3 {
		t.Fatalf("total %d over %d ticks, want 4 over 3", got.Total, got.TicksWithRestamp)
	}
	if got.SlowMillis != simBenchSlowTickMillis || got.SlowTicks != 2 {
		t.Fatalf("slow = %d ticks over %.1f ms, want 2 over %.1f", got.SlowTicks, got.SlowMillis, simBenchSlowTickMillis)
	}
	if got.SlowWithRestamp != 2 || got.FastWithRestamp != 1 {
		t.Fatalf("restamps split slow %d / fast %d, want 2 / 1", got.SlowWithRestamp, got.FastWithRestamp)
	}
	// (20+30+8)/3 against (5+6+4)/3.
	if got.MeanMillisWith <= got.MeanMillisWithout {
		t.Fatalf("mean with restamp %.3f is not above mean without %.3f", got.MeanMillisWith, got.MeanMillisWithout)
	}
}
