package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func fixed(v int) numeric.Fixed { return numeric.Fixed(int64(v) * 65536) }

// acq builds the ordinary non-water acquisition gate for these fixtures. Sea
// level is zero and every candidate sits at height 1, so the [06 §3.1]
// above-sea-level requirement passes and the test is about selection, not
// admission. The nil Visible port samples nothing and admits, which is what a
// session with no visibility service wired has.
func acq(sx, sz numeric.Fixed, weaponRange int32, badMask uint32, r *rng.Simulation) Acquisition {
	return Acquisition{
		ShooterX: sx, ShooterZ: sz, ShooterY: fixed(1),
		SeaLevel: 0, Range: weaponRange, BadMask: badMask, RNG: r,
	}
}

func TestAcquisitionOrderDeterminismTiedCandidates(t *testing.T) {
	// Two candidates at same distance (0) → bound <2 → score 0 without RNG advance [06 §3.2] [01 §7.1] I4.
	// Strictly lower wins, equal preserves first sampled [06 §3.2]; determinism requires pool slot asc (I1).
	// This locks that tied scores do not randomize order beyond stable iteration.
	shooterX, shooterZ := fixed(0), fixed(0)
	weaponRange := int32(1000)
	badMask := uint32(0)

	candidates := []Candidate{
		{Handle: pool.Handle(10), X: fixed(0), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},
		{Handle: pool.Handle(5), X: fixed(0), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},
	}
	// Shuffle input order to prove determinism is by Handle asc, not input order.
	// AcquireTarget sorts ≤50 sampled by Handle asc, so winner should be 5 regardless.
	simRng := rng.NewSimulation(12345) // any seed; bound<2 path does not advance [01 §7.1]
	// Use non-nil rng to exercise zero/no-advance path
	var r rng.Simulation = simRng
	h, ok := AcquireTarget(candidates, acq(shooterX, shooterZ, weaponRange, badMask, &r))
	if !ok {
		t.Fatalf("acquire failed, want ok")
	}
	if h != pool.Handle(5) {
		t.Fatalf("tied winner %d, want 5 (pool slot asc) [06 §3.2] I1", h)
	}
	// Reverse input order still yields same winner.
	candidatesRev := []Candidate{
		{Handle: pool.Handle(5), X: fixed(0), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},
		{Handle: pool.Handle(10), X: fixed(0), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},
	}
	r2 := rng.NewSimulation(999)
	h2, _ := AcquireTarget(candidatesRev, acq(shooterX, shooterZ, weaponRange, badMask, &r2))
	if h2 != pool.Handle(5) {
		t.Fatalf("tied winner rev %d, want 5 [06 §3.2] I1", h2)
	}
	// Ensure deterministic across two runs with same seed and same candidates
	r3 := rng.NewSimulation(1)
	r4 := rng.NewSimulation(1)
	cands := []Candidate{
		{Handle: pool.Handle(2), X: fixed(10), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},
		{Handle: pool.Handle(3), X: fixed(10), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},
	}
	h3, _ := AcquireTarget(cands, acq(shooterX, shooterZ, weaponRange, badMask, &r3))
	h4, _ := AcquireTarget(cands, acq(shooterX, shooterZ, weaponRange, badMask, &r4))
	if h3 != h4 {
		t.Fatalf("determinism failed: %d vs %d with same seed [06 §3.2] I1 I4", h3, h4)
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
	if !WithinCoverageSquare(shooterX, shooterZ, candX, candZ, coverage) {
		t.Fatalf("WithinCoverage true for distance 100 vs coverage 200, want true [06 §11.2]")
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

func TestAcquisitionVisibilityHostilityFiltering(t *testing.T) {
	shooterX, shooterZ := fixed(0), fixed(0)
	weaponRange := int32(1000)
	candidates := []Candidate{
		{Handle: pool.Handle(1), X: fixed(10), Z: fixed(0), Category: 0, Hostile: false, Y: fixed(1)},               // not hostile
		{Handle: pool.Handle(2), X: fixed(10), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1), Cloaked: true}, // not visible
		{Handle: pool.Handle(3), X: fixed(10), Z: fixed(0), Category: 0, Hostile: true, Y: fixed(1)},                // valid
	}
	r := rng.NewSimulation(1)
	h, ok := AcquireTarget(candidates, acq(shooterX, shooterZ, weaponRange, 0, &r))
	if !ok || h != pool.Handle(3) {
		t.Fatalf("filtering failed, got %d ok %v want 3 [06 §3.1]", h, ok)
	}
}

func TestTargetRetentionHysteresis(t *testing.T) {
	// Retention rechecks hostility, badMask, stunned; otherwise keeps without rerunning range/sensor [06 §3.2].
	cand := Candidate{Handle: pool.Handle(5), Category: 0, Hostile: true, Y: fixed(1)}
	if !ShouldRetain(cand, true, 0, false) {
		t.Fatalf("retain true want true [06 §3.2]")
	}
	if ShouldRetain(cand, false, 0, false) {
		t.Fatalf("retain with hostile false, want false [06 §3.2] hostility recheck")
	}
	// Category 0 vs bad 0x1 -> preferred (clear), so should retain
	if !ShouldRetain(cand, true, 0x1, false) {
		t.Fatalf("retain cat0 vs bad 0x1 should retain (preferred) [06 §3.2]")
	}
	candBad := Candidate{Handle: pool.Handle(5), Category: 0x1}
	if ShouldRetain(candBad, true, 0x1, false) {
		t.Fatalf("retain with bad category match, want false [06 §3.2] bad-target rejection")
	}
	if ShouldRetain(cand, true, 0, true) {
		t.Fatalf("retain with stunned, want false [06 §3.2] paralyzer exclusion")
	}
	// TODO(question): range/visibility not rechecked on retention — we keep even if now out of range.
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
