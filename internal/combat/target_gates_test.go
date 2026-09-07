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

// The `toairweapon` clause reads the target's COMMITTED MOVER MODE and admits
// only the value 2 [06 R-WPN-05 §1] clause 3 — not the definition's `canfly`,
// which a landed aircraft still carries. [02 R-KEYS-01 §2] recorded that
// operand as the one inference in the flag's reader census.
func TestToAirClassIsEnforcedOnlyWhenRequested(t *testing.T) {
	r := rng.NewSimulation(1)
	ground := hostileAt(1, 10, 9)
	ground.MoverMode = 1 // grounded/surface
	air := hostileAt(2, 10, 9)
	air.MoverMode = airborneMoverMode

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

// The direct-visibility predicate is the REBUILD's, not the acquisition's
// [06 §3.1]: it decides which hostile units the registry files on the primary
// list, and an acquisition attempt filters that list without re-testing it.
// IsValidAcquisitionCandidate is the predicate's one remaining caller — the
// reaction offer's single-candidate question [06 R-WPN-04 §2 part 3] — so the
// clauses are locked through it. directlyVisibleAtRebuild is the same three
// clauses over a live unit, locked in target_registry_test.go.
func TestDirectVisibilityPredicate(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)
	a.Visible = func(Candidate) bool { return false } // nothing is in the visibility state

	own := hostileAt(1, 10, 9)
	own.OwnSide = true
	// Own-side units are accepted outright, before the sampling [06 §3.1].
	if !IsValidAcquisitionCandidate(own, a) {
		t.Fatalf("an own-side candidate was rejected by the visibility state")
	}
	// Everything else needs the sampled predicate.
	if IsValidAcquisitionCandidate(hostileAt(2, 10, 9), a) {
		t.Fatalf("an unseen candidate passed the predicate")
	}

	a.Visible = func(Candidate) bool { return true }
	cloaked := hostileAt(3, 10, 9)
	cloaked.Cloaked = true
	if IsValidAcquisitionCandidate(cloaked, a) {
		t.Fatalf("a cloaked candidate passed the predicate")
	}

	sub := hostileAt(4, 10, 9)
	sub.Underwater = true
	if IsValidAcquisitionCandidate(sub, a) {
		t.Fatalf("an underwater candidate without its status bit passed the predicate")
	}
	sub.UnderwaterSeen = true
	if !IsValidAcquisitionCandidate(sub, a) {
		t.Fatalf("an underwater candidate carrying its status bit was rejected")
	}

	// The attempt itself applies none of this: a cloaked, unseen entry that is
	// already on the primary list is acquired, because the predicate ran when
	// the registry filed it [06 §3.1].
	a.Visible = func(Candidate) bool { return false }
	if _, ok := AcquireTarget([]Candidate{cloaked}, a); !ok {
		t.Fatalf("an entry already on the primary list must not be re-tested for visibility")
	}
}

// A missing visibility predicate fails the REBUILD closed, not the attempt: an
// unbound service files nothing on the primary list, so nothing is acquirable
// [06 §3.1].
func TestMissingVisibilityRejectsHostileCandidate(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)
	a.Visible = nil
	if IsValidAcquisitionCandidate(hostileAt(1, 10, 9), a) {
		t.Fatalf("hostile candidate admitted without a visibility predicate")
	}
}

// The water branch tests the TARGET alone: no shooter height, no air class, no
// ballistic solution [06 §3.1][06 R-WPN-05 §1] clause 1. Its two clauses are
// the definition's `floater` and `canhover`, and they are part of the gate
// proper — they used to reach it only through an optional closure nothing
// installed, so every water weapon admitted every candidate at any height.
func TestWaterWeaponTestsFloaterAndCanHover(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)
	a.WaterWeapon = true
	a.ShooterY = fixed(1) // the water branch never looks at the shooter

	sub := hostileAt(1, 10, 1) // hull under the waterline
	if _, ok := AcquireTarget([]Candidate{sub}, a); !ok {
		t.Fatalf("a water weapon could not acquire a submerged candidate")
	}

	// A candidate above sea level is refused unless it is a `floater`.
	surface := hostileAt(2, 10, 9)
	if _, ok := AcquireTarget([]Candidate{surface}, a); ok {
		t.Fatalf("a water weapon acquired a candidate out of the water")
	}
	floater := surface
	floater.Floater = true
	if _, ok := AcquireTarget([]Candidate{floater}, a); !ok {
		t.Fatalf("a water weapon refused a floater riding the surface")
	}

	// `canhover` adds the second clause: Y plus HALF the model top must not pass
	// sea level either. A hovercraft at the waterline whose hull is tall enough
	// rides clear of it.
	hover := hostileAt(3, 10, 5)
	hover.Floater = true
	hover.CanHover = true
	hover.ModelTop = 2 // 5 + 1 > 5
	if _, ok := AcquireTarget([]Candidate{hover}, a); ok {
		t.Fatalf("a water weapon acquired a hovercraft riding above the surface")
	}
	hover.ModelTop = 0 // 5 + 0 is not above 5
	if _, ok := AcquireTarget([]Candidate{hover}, a); !ok {
		t.Fatalf("a water weapon refused a hovercraft sitting at the waterline")
	}
}

// The height clauses are whole-unit words PLUS the definition's model top word,
// compared against the sea-level byte [06 §3.1][06 R-WPN-05 §1] clause 2 — not
// the bare 16.16 position compare that stood here. A hull at or under the
// waterline whose model reaches above it is admitted.
func TestHeightClausesAddTheModelTopWord(t *testing.T) {
	r := rng.NewSimulation(1)
	a := base(&r)

	awash := hostileAt(1, 10, 5) // Y equals sea level: the bare compare refused it
	awash.ModelTop = 4
	if _, ok := AcquireTarget([]Candidate{awash}, a); !ok {
		t.Fatalf("a candidate whose model top clears sea level was refused")
	}
	sunk := awash
	sunk.ModelTop = 0
	if _, ok := AcquireTarget([]Candidate{sunk}, a); ok {
		t.Fatalf("a candidate whose top sits at sea level was admitted (the compare is strict)")
	}

	// The shooter half carries the same addend.
	low := a
	low.ShooterY = fixed(3)
	low.ShooterModelTop = 0
	if _, ok := AcquireTarget([]Candidate{awash}, low); ok {
		t.Fatalf("a submerged shooter acquired a target")
	}
	low.ShooterModelTop = 9
	if _, ok := AcquireTarget([]Candidate{awash}, low); !ok {
		t.Fatalf("a shooter whose model top clears sea level was refused")
	}
}

// Range is the gate's LAST clause [06 §3.1][06 R-WPN-05 §1]. Two observations
// lock the order: the pre-range body rejects an out-of-range candidate for its
// HEIGHT, and the ballistic solver — the clause immediately before range — is
// still consulted for a candidate range would refuse.
func TestRangeIsTheLastClause(t *testing.T) {
	// Height reason: the candidate is both far out of range and under water.
	shooter := unitGateEnd{Y: 10}
	sunk := unitGateEnd{Y: 2}
	if unitToUnitAdmitsBeforeRange(shooter, sunk, 5, false, false) {
		t.Fatalf("the height clause did not reject a submerged candidate")
	}
	if !unitToUnitAdmitsBeforeRange(shooter, unitGateEnd{Y: 9}, 5, false, false) {
		t.Fatalf("the pre-range body refused an admissible pair")
	}

	r := rng.NewSimulation(1)
	a := base(&r)
	a.Range = 1 // far inside the candidate's distance
	a.Ballistic = true
	consulted := 0
	a.BallisticFeasible = func(Candidate) bool { consulted++; return true }
	if a.admits(hostileAt(1, 900, 9)) {
		t.Fatalf("physical gate admitted an out-of-range candidate")
	}
	if consulted != 1 {
		t.Fatalf("the ballistic clause ran %d times for an out-of-range candidate, want 1 (range is last)", consulted)
	}
	consulted = 0
	AcquireTarget([]Candidate{hostileAt(1, 900, 9)}, a)
	if consulted != 0 {
		t.Fatal("the preliminary query should reject before invoking the physical gate")
	}
}

// Physical rejection happens after the sampling draw but before scoring
// [06 §3.2]. Adding one rejected entry spends one more sampling draw.
func TestRejectedCandidatesSpendSamplingButNotScoringDraws(t *testing.T) {
	good := hostileAt(1, 300, 9)
	sunk := hostileAt(2, 300, 1) // below sea level: rejected before scoring

	r1 := rng.NewSimulation(7)
	AcquireTarget([]Candidate{good}, base(&r1))

	r2 := rng.NewSimulation(7)
	AcquireTarget([]Candidate{good, sunk}, base(&r2))

	if r1.Draws() != 1 || r2.Draws() != 2 {
		t.Fatalf("sampling/scoring draws=%d/%d, want 1/2",
			r1.Draws(), r2.Draws())
	}
}
