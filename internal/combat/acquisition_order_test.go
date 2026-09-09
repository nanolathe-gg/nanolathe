package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

func TestPrimaryPhysicalFailuresDoNotOpenSecondary(t *testing.T) {
	for _, stunned := range []bool{false, true} {
		r := rng.SimulationFromState(42)
		a := acq(0, 0, 100, 0, &r)
		primary := Candidate{Handle: 1, X: fixed(10), Y: fixed(-1), Hostile: true}
		if stunned {
			primary.Y = fixed(1)
			primary.Stunned = true
			a.Paralyzer = true
		}
		a.HasUpgrade = true
		a.Secondary = []Candidate{{Handle: 2, X: fixed(10), Y: fixed(1), Hostile: true}}
		if h, ok := AcquireTarget([]Candidate{primary}, a); ok {
			t.Fatalf("stunned=%t fell back to %d", stunned, h)
		}
		if r.Draws() != 0 {
			t.Fatalf("single rejected pick draws=%d", r.Draws())
		}
		primary.X = fixed(101) // query exclusion, unlike physical failure, permits fallback
		if h, ok := AcquireTarget([]Candidate{primary}, a); !ok || h != 2 {
			t.Fatalf("out-of-query primary prevented fallback: %d/%t", h, ok)
		}
	}
}

// Independently computed Park-Miller vectors for [06 §3.2]: descending handle
// input, every third entry physically rejected, alternating category buckets,
// and distance squared 100. Selection and scoring draws must interleave.
func TestAcquisitionSamplingAndScoringSequence(t *testing.T) {
	for _, tc := range []struct {
		n      int
		winner pool.Handle
		draws  uint64
		state  uint32
	}{
		{49, 23, 80, 748593394}, {50, 24, 82, 569825713}, {51, 43, 84, 1965210926}, {80, 40, 86, 946661368},
	} {
		candidates := make([]Candidate, tc.n)
		for i := range candidates {
			candidates[i] = Candidate{Handle: pool.Handle(tc.n - i), X: fixed(10), Y: fixed(1), Category: uint32(i % 2), Hostile: true}
			if i%3 == 0 {
				candidates[i].Y = fixed(-1)
			}
		}
		r := rng.SimulationFromState(42)
		h, ok := AcquireTarget(candidates, acq(0, 0, 100, 1, &r))
		if !ok || h != tc.winner || r.Draws() != tc.draws || r.State != tc.state {
			t.Fatalf("n=%d winner=%d/%d draws=%d/%d state=%d/%d", tc.n, h, tc.winner, r.Draws(), tc.draws, r.State, tc.state)
		}
		if candidates[0].Handle != pool.Handle(tc.n) || candidates[len(candidates)-1].Handle != 1 {
			t.Fatal("sampling mutated caller registry order")
		}
	}
}

func TestAcquisitionUsesCachedAllianceUntilRebuild(t *testing.T) {
	f := newRegistryFixture(t, false)
	f.onProjectedGrid()
	f.shooter.InstallWeapon(0, &content.WeaponDef{ID: 99, Range: 1000})
	f.shooter.SlotAt(0).Flags |= 0x02
	f.sensorTick(1)
	s := &Service{}
	rebuildEverySlot(s, targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	acquire := func() (pool.Handle, bool) {
		return s.acquireTargetForSlot(f.shooter, f.shooter.SlotAt(0), 0, f.world, f.vis, f.terrain, nil, f.econ)
	}
	if h, ok := acquire(); !ok || h != f.enemy.Handle {
		t.Fatal("fixture did not register visible hostile")
	}
	f.econ.Players[f.shooter.Owner].Allies[f.enemy.Owner] = true
	f.econ.Players[f.enemy.Owner].Allies[f.shooter.Owner] = true
	if h, ok := acquire(); !ok || h != f.enemy.Handle {
		t.Fatal("alliance change refreshed cached hostility before rebuild")
	}
	rebuildEverySlot(s, 2*targetRegistryPeriod, f.world, f.vis, f.terrain, f.econ)
	if h, ok := acquire(); ok {
		t.Fatalf("allied entry survived registry rebuild: %d", h)
	}
}
