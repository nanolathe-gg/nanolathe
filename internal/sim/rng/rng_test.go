package rng

import "testing"

// TestRetailSimulationSeedTransform locks the seed transform retail applies
// before its first draw [01 §7.1].
func TestRetailSimulationSeedTransform(t *testing.T) {
	if got := NewRetailSimulation(0); got.State != 0x66e29573 {
		t.Fatalf("seed 0 -> state %#x, want 0x66e29573", got.State)
	}
	if got := NewRetailSimulation(1); got.State&1 != 1 {
		t.Fatal("seed transform must force an odd state")
	}
}

// TestBoundBelowTwoDoesNotAdvance is determinism-critical: retail returns zero
// for bounds below two *without consuming a draw* [01 §7.1]. A stream that
// advances here desynchronizes from every later consumer.
func TestBoundBelowTwoDoesNotAdvance(t *testing.T) {
	stream := NewRetailSimulation(12345)
	before := stream.State
	for _, bound := range []uint32{0, 1} {
		if got := stream.Uint32n(bound); got != 0 {
			t.Fatalf("Uint32n(%d) = %d, want 0", bound, got)
		}
	}
	if stream.State != before || stream.Draws != 0 {
		t.Fatalf("stream advanced: state %#x -> %#x, draws %d", before, stream.State, stream.Draws)
	}
	if stream.Uint32n(2); stream.Draws != 1 {
		t.Fatalf("draws = %d after one bounded call, want 1", stream.Draws)
	}
}

// TestParkMillerSchrageStaysInRange checks the generator holds its invariant
// over a long run: Schrage's method must never produce zero or a negative
// signed state.
func TestParkMillerSchrageStaysInRange(t *testing.T) {
	stream := NewRetailSimulation(1)
	for i := 0; i < 100000; i++ {
		stream.Uint32n(1000)
		if stream.State == 0 || int32(stream.State) < 1 {
			t.Fatalf("state left range at draw %d: %#x", i, stream.State)
		}
	}
}

// TestRetailCRTSequence locks the MSVCRT generator [01 §7.2].
func TestRetailCRTSequence(t *testing.T) {
	stream := NewRetailCRT(1)
	want := []int32{41, 18467, 6334, 26500, 19169}
	for i, expected := range want {
		if got := stream.Rand(); got != expected {
			t.Fatalf("draw %d = %d, want %d", i, got, expected)
		}
	}
	if stream.Draws != uint64(len(want)) {
		t.Fatalf("draws = %d, want %d", stream.Draws, len(want))
	}
}
