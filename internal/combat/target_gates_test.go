package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// Acquisition-time physical admission is separate from retention and firing
// [06 §3.1]. Every gate below runs BEFORE the sampling draw, so admitting a
// candidate retail rejects does not merely pick a different target — it
// advances the shared simulation stream differently.

func base(r *rng.Simulation) Acquisition {
	return Acquisition{
		ShooterX: 0, ShooterZ: 0, ShooterY: fixed(10), SeaLevel: fixed(5), Range: 1000, RNG: r,
		Visible: func(Candidate) bool { return true },
	}
}

func hostileAt(h pool.Handle, x, y int) Candidate {
	return Candidate{Handle: h, X: fixed(x), Z: 0, Y: fixed(y), Hostile: true}
}

func TestNonWaterWeaponRequiresBothHeightsAboveSeaLevel(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)

	// Candidate below sea level is not admitted [06 §3.1].
	if _, ok := AcquireTarget([]Candidate{hostileAt(1, 10, 2)}, a); ok {
		t.Fatalf("a candidate below sea level was acquired by a non-water weapon")
	}
	// Above it, the same candidate is.
	if _, ok := AcquireTarget([]Candidate{hostileAt(1, 10, 9)}, a); !ok {
		t.Fatalf("a candidate above sea level was not acquired")
	}
	// A shooter below sea level acquires nothing.
	sunk := a
	sunk.ShooterY = fixed(1)
	if _, ok := AcquireTarget([]Candidate{hostileAt(1, 10, 9)}, sunk); ok {
		t.Fatalf("a shooter below sea level acquired a target")
	}
}

func TestToAirClassIsEnforcedOnlyWhenRequested(t *testing.T) {
	r := rng.NewSimulation(1)
	ground := hostileAt(1, 10, 9)
	air := hostileAt(2, 10, 9)
	air.AirTarget = true

	a := base(&r)
	if _, ok := AcquireTarget([]Candidate{ground}, a); !ok {
		t.Fatalf("a ground candidate should be acquired when to-air is not requested")
	}
	a.ToAir = true
	if _, ok := AcquireTarget([]Candidate{ground}, a); ok {
		t.Fatalf("a to-air slot acquired a non-air candidate")
	}
	if h, ok := AcquireTarget([]Candidate{ground, air}, a); !ok || h != 2 {
		t.Fatalf("a to-air slot picked %d ok=%v, want the air candidate 2", h, ok)
	}
}

func TestBallisticRequirementIsARequirementNotADefaultPass(t *testing.T) {
	r := rng.NewSimulation(1)
	c := hostileAt(1, 10, 9)

	a := base(&r)
	a.Ballistic = true
	// No solver: a required solution that cannot be produced admits nothing.
	if _, ok := AcquireTarget([]Candidate{c}, a); ok {
		t.Fatalf("a ballistic slot with no solver acquired a target")
	}
	a.BallisticFeasible = func(Candidate) bool { return false }
	if _, ok := AcquireTarget([]Candidate{c}, a); ok {
		t.Fatalf("a candidate with no ballistic solution was acquired")
	}
	a.BallisticFeasible = func(Candidate) bool { return true }
	if _, ok := AcquireTarget([]Candidate{c}, a); !ok {
		t.Fatalf("a solvable candidate was not acquired")
	}
}

func TestDirectVisibilityPredicate(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)
	a.Visible = func(Candidate) bool { return false } // nothing is in the visibility state

	own := hostileAt(1, 10, 9)
	own.OwnSide = true
	// Own-side units are accepted outright, before the sampling [06 §3.1].
	if _, ok := AcquireTarget([]Candidate{own}, a); !ok {
		t.Fatalf("an own-side candidate was rejected by the visibility state")
	}
	// Everything else needs the sampled predicate.
	if _, ok := AcquireTarget([]Candidate{hostileAt(2, 10, 9)}, a); ok {
		t.Fatalf("an unseen candidate was acquired")
	}

	a.Visible = func(Candidate) bool { return true }
	cloaked := hostileAt(3, 10, 9)
	cloaked.Cloaked = true
	if _, ok := AcquireTarget([]Candidate{cloaked}, a); ok {
		t.Fatalf("a cloaked candidate was acquired")
	}

	sub := hostileAt(4, 10, 9)
	sub.Underwater = true
	if _, ok := AcquireTarget([]Candidate{sub}, a); ok {
		t.Fatalf("an underwater candidate without its status bit was acquired")
	}
	sub.UnderwaterSeen = true
	if _, ok := AcquireTarget([]Candidate{sub}, a); !ok {
		t.Fatalf("an underwater candidate carrying its status bit was rejected")
	}
}

func TestMissingVisibilityRejectsHostileCandidate(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)
	a.Visible = nil
	if _, ok := AcquireTarget([]Candidate{hostileAt(1, 10, 9)}, a); ok {
		t.Fatalf("hostile candidate acquired without a visibility predicate")
	}
}

func TestWaterWeaponSkipsTheSeaLevelHeightGate(t *testing.T) {
	r := rng.NewSimulation(1)
	sub := hostileAt(1, 10, 1) // below sea level
	a := base(&r)
	a.WaterWeapon = true
	if _, ok := AcquireTarget([]Candidate{sub}, a); !ok {
		t.Fatalf("a water weapon could not acquire a submerged candidate")
	}
	a.WaterAdmit = func(Candidate) bool { return false }
	if _, ok := AcquireTarget([]Candidate{sub}, a); ok {
		t.Fatalf("the water depth/type predicate did not reject")
	}
}

// The gates run before the sampling draw, so a rejected candidate must not
// consume RNG. Two runs from the same seed, one with an inadmissible candidate
// added, must leave the stream in the same place.
func TestRejectedCandidatesDoNotAdvanceTheStream(t *testing.T) {
	good := hostileAt(1, 300, 9)
	sunk := hostileAt(2, 300, 1) // below sea level: rejected before scoring

	r1 := rng.NewSimulation(7)
	AcquireTarget([]Candidate{good}, base(&r1))

	r2 := rng.NewSimulation(7)
	AcquireTarget([]Candidate{good, sunk}, base(&r2))

	if r1.Draws() != r2.Draws() {
		t.Fatalf("an inadmissible candidate advanced the shared stream: %d vs %d draws",
			r1.Draws(), r2.Draws())
	}
}
