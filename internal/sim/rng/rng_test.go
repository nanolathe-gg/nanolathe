package rng

import "testing"

// TestSimulationSeedTransform locks the seed transform retail applies before
// its first draw [01 §7.1].
func TestSimulationSeedTransform(t *testing.T) {
	if got := NewSimulation(0); got.State != 0x66e29573 {
		t.Fatalf("seed 0 -> state %#x, want 0x66e29573", got.State)
	}
	if got := NewSimulation(1); got.State&1 != 1 {
		t.Fatal("seed transform must force an odd state")
	}
}

// TestBoundBelowTwoDoesNotAdvance is determinism-critical: retail returns zero
// for bounds below two *without consuming a draw* [01 §7.1]. A stream that
// advances here desynchronizes from every later consumer.
func TestBoundBelowTwoDoesNotAdvance(t *testing.T) {
	stream := NewSimulation(12345)
	before := stream.State
	for _, bound := range []uint32{0, 1} {
		if got := stream.Uint32n(bound); got != 0 {
			t.Fatalf("Uint32n(%d) = %d, want 0", bound, got)
		}
	}
	if stream.State != before || stream.Draws() != 0 {
		t.Fatalf("stream advanced: state %#x -> %#x, draws %d", before, stream.State, stream.Draws())
	}
	if stream.Uint32n(2); stream.Draws() != 1 {
		t.Fatalf("draws = %d after one bounded call, want 1", stream.Draws())
	}
}

// TestSimulationWideBoundIsOneDraw locks the R1 contract: the simulation
// helper has no chunk-concatenation path. [01 §7.1] describes one Lehmer
// update and a modulo for every bound; the chunk loop belongs to the CRT
// stream [01 §7.2], which yields 15 bits per draw where Park-Miller yields 31.
//
// simRand(0x10000) is the wind heading of [01 §7.3] and [05 "Wind generation"].
// If this ever costs two draws again, every simulation consumer downstream of
// the first wind change is on a shifted stream.
func TestSimulationWideBoundIsOneDraw(t *testing.T) {
	for _, bound := range []uint32{0x8000, 0x10000, 0x7FFFFFFF} {
		stream := NewSimulation(12345)
		got := stream.Uint32n(bound)
		if stream.Draws() != 1 {
			t.Fatalf("Uint32n(%#x) consumed %d draws, want 1", bound, stream.Draws())
		}
		if want := stream.State % bound; got != want {
			t.Fatalf("Uint32n(%#x) = %d, want %d (state %% bound)", bound, got, want)
		}
	}
}

// TestParkMillerSchrageStaysInRange checks the generator holds its invariant
// over a long run: Schrage's method must never produce zero or a negative
// signed state.
func TestParkMillerSchrageStaysInRange(t *testing.T) {
	stream := NewSimulation(1)
	for i := 0; i < 100000; i++ {
		stream.Uint32n(1000)
		if stream.State == 0 || int32(stream.State) < 1 {
			t.Fatalf("state left range at draw %d: %#x", i, stream.State)
		}
	}
}

// TestCRTSequence locks the MSVCRT generator [01 §7.2].
func TestCRTSequence(t *testing.T) {
	stream := NewCRT(1)
	want := []int32{41, 18467, 6334, 26500, 19169}
	for i, expected := range want {
		if got := stream.Rand(); got != expected {
			t.Fatalf("draw %d = %d, want %d", i, got, expected)
		}
	}
	if stream.Draws() != uint64(len(want)) {
		t.Fatalf("draws = %d, want %d", stream.Draws(), len(want))
	}
}

// TestCRTBoundOneStillDraws locks the R2 contract. Retail's CRT call sites are
// raw inline `rand() % n` expressions — the briefing wind speed of [01 §7.3] is
// written that way — so the simulation helper's zero-bound guard must NOT be
// transplanted here. A map with minwindspeed == maxwindspeed would otherwise
// skip a draw and shift every later CRT consumer by one.
func TestCRTBoundOneStillDraws(t *testing.T) {
	stream := NewCRT(1)
	if got := stream.Uint32n(1); got != 0 {
		t.Fatalf("Uint32n(1) = %d, want 0", got)
	}
	if stream.Draws() != 1 {
		t.Fatalf("draws = %d after Uint32n(1), want 1", stream.Draws())
	}
	// The draw consumed must be the first of the sequence, so the next raw
	// Rand() is the second value of the known vector.
	if got := stream.Rand(); got != 18467 {
		t.Fatalf("next Rand() = %d, want 18467 (Uint32n(1) must consume draw 1)", got)
	}
}

// TestCRTWideBoundConcatenates locks the chunk-concatenation helper of
// [01 §7.2], followed literally: mask and result both start at 0x7FFF, so a
// bound of 0x10000 consumes exactly ONE draw and the sample's top bits come
// from that constant (see the TODO(question) on CRT.Uint32n for why this
// reading was chosen over seeding result from a fresh draw).
func TestCRTWideBoundConcatenates(t *testing.T) {
	stream := NewCRT(1)
	got := stream.Uint32n(0x10000)
	if stream.Draws() != 1 {
		t.Fatalf("Uint32n(0x10000) consumed %d draws, want 1", stream.Draws())
	}
	// Seed 1: first draw is 41 [01 §7.2]. Literal loop:
	// result = (0x7FFF << 15 | 41) % 0x10000.
	const firstDraw = uint32(41)
	if want := ((uint32(0x7FFF) << 15) | firstDraw) % 0x10000; got != want {
		t.Fatalf("Uint32n(0x10000) = %d, want %d", got, want)
	}
	// A wider bound needs a second iteration and a second draw.
	stream = NewCRT(1)
	_ = stream.Uint32n(0x40000000)
	if stream.Draws() != 2 {
		t.Fatalf("Uint32n(0x40000000) consumed %d draws, want 2", stream.Draws())
	}
}

// TestSeedGlobalIsReproducible locks PLAN_00 C4 / PLAN_03 C10: a fixed seed
// pair reproduces a run. RNG state is not saved and is reseeded on load
// [08 "Scheduler and random state in saves"], so this is the only handle a
// repro has.
func TestSeedGlobalIsReproducible(t *testing.T) {
	drawSome := func() (uint32, uint32) {
		SeedGlobal(7, 7)
		var a, b uint32
		for i := 0; i < 50; i++ {
			a = Global.Sim.Uint32n(1000)
			b = Global.Crt.Uint32n(1000)
		}
		return a, b
	}
	a1, b1 := drawSome()
	a2, b2 := drawSome()
	if a1 != a2 || b1 != b2 {
		t.Fatalf("same seed diverged: sim %d/%d crt %d/%d", a1, a2, b1, b2)
	}
	if Global.Sim.Draws() != 50 || Global.Crt.Draws() != 50 {
		t.Fatalf("draw counts not reset by reseed: sim %d crt %d",
			Global.Sim.Draws(), Global.Crt.Draws())
	}
	SeedGlobal(7, 7)
	if Global.Sim.State != NewSimulation(7).State || Global.Crt.State != 7 {
		t.Fatal("SeedGlobal must apply the battle-entry transform to the simulation seed and seed the CRT directly")
	}
	SeedGlobal(8, 8)
	a3 := Global.Sim.Uint32n(1000)
	SeedGlobal(7, 7)
	if a4 := Global.Sim.Uint32n(1000); a3 == a4 {
		t.Fatal("different seeds produced the same first draw")
	}
}
