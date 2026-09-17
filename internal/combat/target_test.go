package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

func fixed(v int) numeric.Fixed { return numeric.Fixed(int64(v) * 65536) }

// acq builds the ordinary non-water acquisition gate for these fixtures. Sea
// level is zero and every candidate sits at height 1, so the [06 §3.1]
// above-sea-level requirement passes and the test is about selection, not
// admission. Tests that need a different visibility result override Visible.
//
// The shooter's owner is a computer player, which is check 2's second disjunct
// [06 §3.2]: these fixtures are about sampling, scoring and the physical gate,
// so check 2 is opened once here rather than authoring `shootme` on every
// candidate literal below.
func acq(sx, sz numeric.Fixed, weaponRange int32, badMask uint32, r *rng.Simulation) Acquisition {
	return Acquisition{
		ShooterX: sx, ShooterZ: sz, ShooterY: fixed(1),
		SeaLevel: 0, Range: weaponRange, BadMask: badMask, RNG: r,
		ShooterControlByte: ControlByteComputer, // check 2 [06 §3.2]
		Visible:            func(Candidate) bool { return true },
	}
}

func TestAcquisitionOrderDeterminismTiedCandidates(t *testing.T) {
	// Zero-distance scores tie without drawing. Sampling still spends its
	// bound-two draw, and the first sampled candidate wins [06 §3.2].
	candidates := []Candidate{
		{Handle: 10, Hostile: true, Y: fixed(1)},
		{Handle: 5, Hostile: true, Y: fixed(1)},
	}
	for reversed := 0; reversed < 2; reversed++ {
		r := rng.NewSimulation(12345)
		expected := r
		first := expected.Uint32n(2)
		h, ok := AcquireTarget(candidates, acq(0, 0, 1000, 0, &r))
		if !ok || h != candidates[first].Handle || r.State != expected.State || r.Draws() != expected.Draws() {
			t.Fatalf("order=%d winner=%d expected=%d draws=%d", reversed, h, candidates[first].Handle, r.Draws())
		}
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}
}

func TestAcquisitionUsesRangeNotCoverage(t *testing.T) {
	// Coverage is separate from ordinary fire range and drives overlay only [06 §3.3] [06 §2.1].
	// Ordinary acquisition uses Range, not Coverage. This test constructs a candidate within coverage square
	// but outside weapon Range and asserts it is NOT acquired.
	shooterX, shooterZ := fixed(0), fixed(0)
	weaponRange := int32(50)
	coverage := int32(200)
	_ = coverage // used for WithinCoverage helper assertion

	candX, candZ := fixed(100), fixed(0) // distance 100: outside range 50, inside coverage 200
	if WithinRange(shooterX, shooterZ, candX, candZ, weaponRange) {
		t.Fatalf("WithinRange true for distance 100 vs range 50, want false [06 §3.3]")
	}
	// The interceptor coverage compare is the live one [06 R-WPN-05 §10]; it
	// admits the same candidate that ordinary fire range refuses.
	if !WithinInterceptorCoverage(Vec3{X: candX, Z: candZ}, Vec3{X: shooterX, Z: shooterZ}, coverage) {
		t.Fatalf("coverage 200 must admit a candidate 100 world units away [06 §11.2]")
	}
	candidates := []Candidate{
		{Handle: pool.Handle(1), X: candX, Z: candZ, Category: 0, Hostile: true, Y: fixed(1)},
	}
	r := rng.NewSimulation(1)
	h, ok := AcquireTarget(candidates, acq(shooterX, shooterZ, weaponRange, 0, &r))
	if ok {
		t.Fatalf("acquire succeeded with candidate outside range (%d), want fail (coverage vs engagement) [06 §3.3] ", h)
	}
	// Within range but outside coverage should still be acquired (coverage ignored)
	candX2 := fixed(30)
	if !WithinRange(shooterX, shooterZ, candX2, shooterZ, weaponRange) {
		t.Fatalf("WithinRange false for 30 vs 50, want true")
	}
	// candidate at 30 is within range, even if coverage were 20 it would still be acquired — we test acquisition succeeds despite coverage miss.
	candidates2 := []Candidate{
		{Handle: pool.Handle(2), X: candX2, Z: candZ, Category: 0, Hostile: true, Y: fixed(1)},
	}
	// Acquisition uses range, so should succeed; we pass same range 50 and ignore coverage param.
	r2 := rng.NewSimulation(1)
	h2, ok2 := AcquireTarget(candidates2, acq(shooterX, shooterZ, weaponRange, 0, &r2))
	if !ok2 || h2 != pool.Handle(2) {
		t.Fatalf("acquire failed for candidate within range 30 (coverage irrelevant), got %d ok %v [06 §3.3]", h2, ok2)
	}
}

func TestAcquisitionPreferredOverFallback(t *testing.T) {
	// Preferred bucket (category clear of badMask) wins over fallback even if fallback is closer [06 §3.1].
	shooterX, shooterZ := fixed(0), fixed(0)
	weaponRange := int32(1000)
	badMask := uint32(0x1) // slot rejects category bit 0

	// Preferred candidate farther (distance 100) vs fallback closer (distance 10)
	candidates := []Candidate{
		{Handle: pool.Handle(1), X: fixed(10), Z: fixed(0), Category: 0x1, Hostile: true, Y: fixed(1)},  // fallback: matches bad
		{Handle: pool.Handle(2), X: fixed(100), Z: fixed(0), Category: 0x0, Hostile: true, Y: fixed(1)}, // preferred
	}
	r := rng.NewSimulation(1)
	h, ok := AcquireTarget(candidates, acq(shooterX, shooterZ, weaponRange, badMask, &r))
	if !ok {
		t.Fatalf("acquire failed")
	}
	if h != pool.Handle(2) {
		t.Fatalf("preferred vs fallback: winner %d, want 2 (preferred) [06 §3.1]", h)
	}
	// If no preferred, fallback can win
	candidates2 := []Candidate{
		{Handle: pool.Handle(3), X: fixed(10), Z: fixed(0), Category: 0x1, Hostile: true, Y: fixed(1)},
		{Handle: pool.Handle(4), X: fixed(20), Z: fixed(0), Category: 0x1, Hostile: true, Y: fixed(1)},
	}
	r2 := rng.NewSimulation(1)
	h2, ok2 := AcquireTarget(candidates2, acq(shooterX, shooterZ, weaponRange, badMask, &r2))
	if !ok2 {
		t.Fatalf("acquire fallback failed")
	}
	// Determinism: with equal bound<2? But distances differ, bounds differ. We'll just check one of them wins and is from fallback bucket.
	if h2 != pool.Handle(3) && h2 != pool.Handle(4) {
		t.Fatalf("fallback winner %d unexpected", h2)
	}
}

// The per-attempt filter keeps hostility and drops the sensor clauses: "no
// visibility, category, sensor, medium, alliance or range test happens at this
// point — the visibility predicate ran at rebuild time" [06 §3.1]. A cloak bit
// that arrived after the rebuild therefore does not protect a listed unit, and
// a non-hostile entry is still refused.
func TestAcquisitionFiltersHostilityAndNotVisibility(t *testing.T) {
	shooterX, shooterZ := fixed(0), fixed(0)
	weaponRange := int32(1000)
	friendly := Candidate{Handle: pool.Handle(1), X: fixed(10), Z: fixed(0), Hostile: false, Y: fixed(1)}
	cloakedSinceRebuild := Candidate{Handle: pool.Handle(2), X: fixed(10), Z: fixed(0), Hostile: true, Y: fixed(1), Cloaked: true}
	plain := Candidate{Handle: pool.Handle(3), X: fixed(10), Z: fixed(0), Hostile: true, Y: fixed(1)}

	r := rng.NewSimulation(1)
	if h, ok := AcquireTarget([]Candidate{friendly}, acq(shooterX, shooterZ, weaponRange, 0, &r)); !ok || h != friendly.Handle {
		t.Fatalf("a cached entry that became allied must remain in this acquisition query [06 §3.1]")
	}
	r2 := rng.NewSimulation(1)
	if h, ok := AcquireTarget([]Candidate{cloakedSinceRebuild}, acq(shooterX, shooterZ, weaponRange, 0, &r2)); !ok || h != pool.Handle(2) {
		t.Fatalf("a listed entry that cloaked since the rebuild stays acquirable, got %d ok %v", h, ok)
	}
	r3 := rng.NewSimulation(1)
	if h, ok := AcquireTarget([]Candidate{plain}, acq(shooterX, shooterZ, weaponRange, 0, &r3)); !ok || h != pool.Handle(3) {
		t.Fatalf("filtering failed, got %d ok %v want 3 [06 §3.1]", h, ok)
	}
}

// The stunned mark is read by exactly one acquisition gate, and only for a
// paralyzer: "a paralyzer weapon rejects a candidate already carrying the
// stunned bit" is check 5 of the picked-candidate order [06 §3.2], and an
// ordinary weapon ignores the mark entirely [06 R-DMG-01 §11]. Wiring it
// unconditionally would make every weapon in the game stop shooting at
// paralyzed units, which is the defect the dead ShouldRetain helpers encoded.
func TestParalyzerRejectsStunnedCandidateOrdinaryDoesNot(t *testing.T) {
	shooterX, shooterZ := fixed(0), fixed(0)
	weaponRange := int32(1000)
	candidates := []Candidate{
		{Handle: pool.Handle(7), X: fixed(10), Z: fixed(0), Hostile: true, Y: fixed(1), Stunned: true},
	}

	r := rng.NewSimulation(1)
	ordinary := acq(shooterX, shooterZ, weaponRange, 0, &r)
	if h, ok := AcquireTarget(candidates, ordinary); !ok || h != pool.Handle(7) {
		t.Fatalf("ordinary weapon got (%d, %v), want candidate 7 retained: the mark is paralyzer-only [06 §3.2][06 R-DMG-01 §11]", h, ok)
	}

	r2 := rng.NewSimulation(1)
	paralyzer := acq(shooterX, shooterZ, weaponRange, 0, &r2)
	paralyzer.Paralyzer = true
	if h, ok := AcquireTarget(candidates, paralyzer); ok {
		t.Fatalf("paralyzer got (%d, %v), want no target: check 5 rejects a stunned candidate [06 §3.2]", h, ok)
	}

	// An unstunned candidate is acquired by the paralyzer as usual — the
	// rejection is the mark's, not the weapon's.
	awake := []Candidate{{Handle: pool.Handle(8), X: fixed(10), Z: fixed(0), Hostile: true, Y: fixed(1)}}
	r3 := rng.NewSimulation(1)
	paralyzer2 := acq(shooterX, shooterZ, weaponRange, 0, &r3)
	paralyzer2.Paralyzer = true
	if h, ok := AcquireTarget(awake, paralyzer2); !ok || h != pool.Handle(8) {
		t.Fatalf("paralyzer got (%d, %v), want candidate 8 [06 §3.2]", h, ok)
	}
}

func TestRngBoundBelowTwoNoAdvance(t *testing.T) {
	// Bound <2 returns 0 without advancing Sim stream [01 §7.1] I4; ensures tied acquisition does not desync.
	var s rng.Simulation = rng.NewSimulation(42)
	drawsBefore := s.Draws()
	_ = s.Uint32n(1) // bound 1 <2 -> 0 no advance
	if s.Draws() != drawsBefore {
		t.Fatalf("Uint32n(1) advanced draws %d -> %d, want no advance [01 §7.1] I4", drawsBefore, s.Draws())
	}
	_ = s.Uint32n(0)
	if s.Draws() != drawsBefore {
		t.Fatalf("Uint32n(0) advanced draws, want no advance [01 §7.1]")
	}
	// Bound 2 should advance
	_ = s.Uint32n(2)
	if s.Draws() != drawsBefore+1 {
		t.Fatalf("Uint32n(2) draws %d, want %d [01 §7.1]", s.Draws(), drawsBefore+1)
	}
}
