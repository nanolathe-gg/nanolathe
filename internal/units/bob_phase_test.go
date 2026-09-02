package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestBobPhaseIsTheAllocatorFullDomainDraw locks [04 R-MOV-01 §5c]: the unit
// initializer's full-domain simulation draw — the one [R-P28-ANG-01R §2] places
// immediately after the `buildangle` draw — is stored as the unit's bob phase
// word, as the low 16 bits read signed. Storing it consumes no additional draw,
// so the stream's state and draw count after allocation are exactly those of
// the two invocations the allocator already made.
func TestBobPhaseIsTheAllocatorFullDomainDraw(t *testing.T) {
	sim := rng.NewSimulation(7)
	expected := rng.NewSimulation(7)
	w := newFixtureWorld(4, nil)
	w.SetSimulationRNG(&sim)
	def := p28AngleDef("bob", 4096)

	for i := 0; i < 3; i++ {
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		_ = expected.Uint32n(4096) // the buildangle-bounded heading draw
		wantPhase := int16(uint16(expected.Uint32n(0x10000)))
		if got := w.Unit(h).BobPhase; got != wantPhase {
			t.Fatalf("unit %d BobPhase = %d, want the full-domain draw %d", i, got, wantPhase)
		}
		// No draw is added or reordered by storing the value.
		if sim.State != expected.State || sim.Draws() != expected.Draws() {
			t.Fatalf("unit %d stream = (%d,%d), want (%d,%d) — the phase store must consume no draw",
				i, sim.State, sim.Draws(), expected.State, expected.Draws())
		}
	}
	if got := sim.Draws(); got != 6 {
		t.Fatalf("draw count = %d, want 6 (heading then full-domain per allocation)", got)
	}
}

// TestBobPhaseIsWrittenForEveryUnit locks the "every unit receives one, whether
// or not it can hover" half of [04 R-MOV-01 §5c]. A definition whose buildangle
// draw is skipped entirely (bound below two returns 0 without advancing,
// [01 §7.1]) still takes the full-domain draw and still gets a phase.
func TestBobPhaseIsWrittenForEveryUnit(t *testing.T) {
	sim := rng.NewSimulation(31)
	expected := rng.NewSimulation(31)
	w := newFixtureWorld(2, nil)
	w.SetSimulationRNG(&sim)
	h, err := w.Create(p28AngleDef("nobuildangle", 0), 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantPhase := int16(uint16(expected.Uint32n(0x10000)))
	if got := w.Unit(h).BobPhase; got != wantPhase {
		t.Fatalf("BobPhase = %d, want %d", got, wantPhase)
	}
	if sim.Draws() != 1 {
		t.Fatalf("draw count = %d, want 1", sim.Draws())
	}
}
