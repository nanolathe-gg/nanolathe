package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestBriefingDisplayDraws is the R2 regression for the FRONT-END briefing
// display helper. Retail's briefing draws are raw inline rand()%n expressions
// [01 §7.3], so a map whose wind bounds are equal must still consume the speed
// draw. [R-CORE-02]: these are front-end display values with no battle-side
// reader — battle entry itself consumes NO wind draws; only the briefing
// screen runs this path. The entry draws exactly twice (speed, countdown),
// arms no deadline and writes no heading (RWU-19-39: the second draw is a
// countdown, not a direction).
func TestBriefingDisplayDraws(t *testing.T) {
	for _, bounds := range [][2]int32{{100, 2000}, {500, 500}, {0, 0}} {
		crt := rng.NewCRT(1)
		w := NewWind(bounds[0], bounds[1])
		w.SeedBriefing(&crt, 0)
		if crt.Draws() != 2 {
			t.Fatalf("bounds %v: briefing entry consumed %d CRT draws, want 2", bounds, crt.Draws())
		}
		if w.Strength < bounds[0] || w.Strength > bounds[1] {
			t.Fatalf("bounds %v: strength %d out of range", bounds, w.Strength)
		}
		if w.NextChange != 0 || w.Heading != 0 {
			t.Fatalf("bounds %v: briefing entry armed a deadline (%d) or wrote a heading (%d) [01 §7.3]", bounds, w.NextChange, w.Heading)
		}
		if w.BriefingCountdown < 0 || w.BriefingCountdown > 63 {
			t.Fatalf("bounds %v: countdown %d outside 0..63 [01 §7.3]", bounds, w.BriefingCountdown)
		}
	}
}

// TestBriefingUpdateJitter locks the per-update display routine of [01 §7.3]:
// no draws while the countdown is at or above one, then on expiry exactly two
// draws, a speed drift of at most two clamped to the bounds, and a re-armed
// countdown in 0..62.
func TestBriefingUpdateJitter(t *testing.T) {
	crt := rng.NewCRT(3)
	w := NewWind(100, 2000)
	w.SeedBriefing(&crt, 0)
	before := crt.Draws()
	w.BriefingCountdown = 2
	if w.BriefingUpdate(&crt, 1) || crt.Draws() != before {
		t.Fatalf("update with countdown 2 redrew or drew (draws %d -> %d)", before, crt.Draws())
	}
	speed := w.Strength
	if !w.BriefingUpdate(&crt, 2) || crt.Draws() != before+2 {
		t.Fatalf("expiring update: redraw=%v draws %d -> %d, want redraw and 2 draws", w.BriefingCountdown < 1, before, crt.Draws())
	}
	if d := w.Strength - speed; d < -2 || d > 2 {
		t.Fatalf("speed drift %d outside -2..2 [01 §7.3]", d)
	}
	if w.BriefingCountdown < 0 || w.BriefingCountdown > 62 {
		t.Fatalf("re-armed countdown %d outside 0..62 [01 §7.3]", w.BriefingCountdown)
	}
	// Clamp: a speed at the floor cannot drift below it.
	w.Strength, w.BriefingCountdown = w.Min, 0
	w.BriefingUpdate(&crt, 3)
	if w.Strength < w.Min || w.Strength > w.Max {
		t.Fatalf("clamped speed %d outside [%d, %d]", w.Strength, w.Min, w.Max)
	}
}

// TestWindChangeDrawCounts locks the per-change stream budget of [01 §7.3]
// [R-CORE-01 §4.4.1]: phase 8's single redraw consumes one CRT draw for the
// interval, then one simulation draw for the strength and one for the heading
// only when the strength is nonzero. The heading is simRand(0x10000); if the
// simulation helper ever regains a chunk-concatenation path it becomes two
// draws and this fails (R1).
func TestWindChangeDrawCounts(t *testing.T) {
	crt := rng.NewCRT(1)
	sim := rng.NewSimulation(7)
	w := NewWind(100, 2000)
	// Battle entry zeroes the deadline; seedBriefing-style pre-draws are not
	// part of battle entry, so start from the zero deadline and arm via the
	// first change instead.
	if w.NextChange != 0 {
		t.Fatalf("fresh wind deadline = %d, want 0 [R-CORE-02]", w.NextChange)
	}

	// Tick 1 is due (deadline zeroed at entry, strict gate): exactly one CRT
	// draw (interval) and two simulation draws (strength, heading).
	if !w.Jitter(1, &crt, &sim) {
		t.Fatal("tick 1 must be due [R-CORE-02]")
	}
	if got := crt.Draws(); got != 1 {
		t.Fatalf("change consumed %d CRT draws, want 1 (interval)", got)
	}
	if sim.Draws() != 2 {
		t.Fatalf("change consumed %d simulation draws, want 2 (strength, heading)", sim.Draws())
	}
	if !w.Changed || w.LastChange != 1 {
		t.Fatalf("change not published at tick 1")
	}

	// A tick before the new deadline draws nothing at all.
	crtAfter, simAfter := crt.Draws(), sim.Draws()
	if w.Jitter(w.NextChange-1, &crt, &sim) {
		t.Fatal("wind changed before its deadline")
	}
	if crt.Draws() != crtAfter || sim.Draws() != simAfter {
		t.Fatalf("undue tick drew: crt %d sim %d", crt.Draws()-crtAfter, sim.Draws()-simAfter)
	}

	// The next due tick again draws exactly one CRT and two simulation values.
	crtAfter, simAfter = crt.Draws(), sim.Draws()
	due := w.NextChange
	if !w.Jitter(due, &crt, &sim) {
		t.Fatal("wind did not change on its deadline")
	}
	if got := crt.Draws() - crtAfter; got != 1 {
		t.Fatalf("change consumed %d CRT draws, want 1 (interval)", got)
	}
	if got := sim.Draws() - simAfter; got != 2 {
		t.Fatalf("change consumed %d simulation draws, want 2 (strength, heading)", got)
	}
}

// TestZeroStrengthSkipsHeadingDraw: the heading draw is taken only when the new
// strength is nonzero [01 §7.3]. A collapsed zero span returns 0 without
// advancing, so a due tick costs one CRT draw and zero simulation draws.
func TestZeroStrengthSkipsHeadingDraw(t *testing.T) {
	crt := rng.NewCRT(1)
	sim := rng.NewSimulation(7)
	w := NewWind(0, 0)
	if !w.Jitter(1, &crt, &sim) {
		t.Fatal("tick 1 must be due")
	}
	if sim.Draws() != 0 {
		t.Fatalf("zero-span change consumed %d simulation draws, want 0", sim.Draws())
	}
	if crt.Draws() != 1 {
		t.Fatalf("zero-span change consumed %d CRT draws, want 1", crt.Draws())
	}
}

// TestWindScalarClampsAtOne locks the published ratio: speed/5000 as float32,
// clamped from above at exactly 1.0 [01 §7.3], [05 "Wind generation"].
func TestWindScalarClampsAtOne(t *testing.T) {
	if got := windScalar(2500); got != 0.5 {
		t.Fatalf("windScalar(2500) = %v, want 0.5", got)
	}
	if got := windScalar(5000); got != 1.0 {
		t.Fatalf("windScalar(5000) = %v, want 1", got)
	}
	if got := windScalar(90000); got != 1.0 {
		t.Fatalf("windScalar(90000) = %v, want 1 (clamped)", got)
	}
}

// TestScheduledRedrawCadence locks the scheduled-redraw cadence with the
// single phase-8 call: one CRT interval draw per change and at most two
// simulation draws per change [01 §7.3][R-CORE-01 §4.4.1].
func TestScheduledRedrawCadence(t *testing.T) {
	crt := rng.NewCRT(1)
	sim := rng.NewSimulation(7)
	w := NewWind(100, 2000)

	changes := 0
	for tick := uint32(1); tick <= 900; tick++ {
		before := w.LastChange
		w.Jitter(tick, &crt, &sim)
		if w.LastChange != before {
			changes++
		}
	}
	if changes < 2 {
		t.Fatalf("only %d wind changes in 900 ticks, want at least 2", changes)
	}
	// Every change is one CRT interval draw and at most two simulation draws.
	if crt.Draws() != uint64(changes) {
		t.Fatalf("crt draws = %d, want %d (1 per change)", crt.Draws(), changes)
	}
	if sim.Draws() != uint64(2*changes) {
		t.Fatalf("sim draws = %d, want %d (2 per change)", sim.Draws(), 2*changes)
	}
}

// TestWindVectorAxes locks the axis assignment closed by [R-WIND-01] (verified
// against two independent consumer families: smoke drift and the fire-spread
// probe): the FIRST word is the X term −2·speed·sin(heading) and the SECOND
// word is the Z term −2·speed·cos(heading). Table semantics [04 §5.1]: entry k
// = 8192·sin(2πk/512), cosine reads the same table a quarter turn ahead, so
// heading 0 reads sin 0 / cos 8192 and a quarter-circle heading (16384 of
// 65536) reads sin 8192 / cos 0. MulRound's add-half form is exact on these
// products, so heading 0 must give X = 0, Z = −2·speed.
func TestWindVectorAxes(t *testing.T) {
	if x, z := windVectors(500, 0); x != 0 || z != -1000 {
		t.Fatalf("heading 0: vectors = (%d, %d), want X = 0 (sin) and Z = -2*speed (cos) [R-WIND-01]", x, z)
	}
	if x, z := windVectors(500, 16384); x != -1000 || z != 0 {
		t.Fatalf("quarter-circle heading: vectors = (%d, %d), want X = -2*speed (sin peak) and Z = 0 (cos zero) [R-WIND-01]", x, z)
	}
	if x, z := windVectors(0, 12345); x != 0 || z != 0 {
		t.Fatalf("zero strength: vectors = (%d, %d), want zeroed", x, z)
	}
}
