package units

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

func p28AngleDef(name string, angle int32) *content.UnitDef {
	return &content.UnitDef{UnitName: name, BuildAngle: angle, MaxDamage: 100, Limit: -1}
}

func p28CreateHeading(t *testing.T, w *World, def *content.UnitDef) uint16 {
	t.Helper()
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return w.Unit(h).Move.Heading
}

func TestP28BuildAngleKnownSequenceAndSingleStream(t *testing.T) {
	sim := rng.NewSimulation(7)
	w := NewSliced(4, nil)
	w.SetSimulationRNG(&sim)
	def := p28AngleDef("angle", 4096)

	// Seed 7 produces bounded results 2328, 3877, and 3598 when the
	// full-domain initializer draw remains between heading calls.
	want := []uint16{33048, 34597, 34318}
	for i, expected := range want {
		if got := p28CreateHeading(t, w, def); got != expected {
			t.Fatalf("heading %d = %d, want %d", i, got, expected)
		}
	}
	if got := sim.Draws(); got != 6 {
		t.Fatalf("draw count = %d, want 6 (heading then full-domain per allocation)", got)
	}
}

func TestP28BuildAngleSignedSixteenBitConversion(t *testing.T) {
	sim := rng.NewSimulation(7)
	w := NewSliced(1, nil)
	w.SetSimulationRNG(&sim)
	// The first sample is 36570. Interpreting it as unsigned would produce a
	// different intermediate; the established signed-16 conversion wraps the
	// final circular heading to 36571.
	if got := p28CreateHeading(t, w, p28AngleDef("signed", 65535)); got != 36571 {
		t.Fatalf("signed-conversion heading = %d, want 36571", got)
	}
	if sim.Draws() != 2 {
		t.Fatalf("draw count = %d, want 2", sim.Draws())
	}
}

func TestP28FixtureWorldWithoutBoundStreamUsesZeroSpanHeading(t *testing.T) {
	w := NewSliced(1, nil)
	if got := p28CreateHeading(t, w, p28AngleDef("fixture", 4096)); got != 30720 {
		t.Fatalf("nil-stream fixture heading = %d, want deterministic zero sample 30720", got)
	}
}

func TestP28BuildAngleSameSeedSameHeadingsAndState(t *testing.T) {
	makeRun := func(seed uint32) ([]uint16, uint32, uint64) {
		sim := rng.NewSimulation(seed)
		w := NewSliced(4, nil)
		w.SetSimulationRNG(&sim)
		def := p28AngleDef("same", 4096)
		got := make([]uint16, 3)
		for i := range got {
			got[i] = p28CreateHeading(t, w, def)
		}
		return got, sim.State, sim.Draws()
	}
	a, stateA, drawsA := makeRun(23)
	b, stateB, drawsB := makeRun(23)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("same seed heading %d differs: %d != %d", i, a[i], b[i])
		}
	}
	if stateA != stateB || drawsA != drawsB {
		t.Fatalf("same seed stream differs: (%d,%d) != (%d,%d)", stateA, drawsA, stateB, drawsB)
	}
	c, stateC, _ := makeRun(24)
	if a[0] == c[0] && a[1] == c[1] && a[2] == c[2] && stateA == stateC {
		t.Fatal("different seed produced identical heading sequence and stream state")
	}
}

func TestP28BuildAngleZeroAndOneSkipOnlyBoundedDraw(t *testing.T) {
	for _, bound := range []int32{0, 1} {
		t.Run(string(rune('0'+bound)), func(t *testing.T) {
			sim := rng.NewSimulation(31)
			expected := rng.NewSimulation(31)
			w := NewSliced(3, nil)
			w.SetSimulationRNG(&sim)
			def := p28AngleDef("small", bound)
			for i := 0; i < 2; i++ {
				if got := p28CreateHeading(t, w, def); got != 32768 {
					t.Fatalf("heading = %d, want 32768", got)
				}
				_ = expected.Uint32n(0x10000)
			}
			if sim.State != expected.State || sim.Draws() != expected.Draws() {
				t.Fatalf("bound %d stream = (%d,%d), want only full-domain calls (%d,%d)", bound, sim.State, sim.Draws(), expected.State, expected.Draws())
			}
		})
	}
}

func TestP28BuildAngleUsesLowSixteenBitBound(t *testing.T) {
	sim := rng.NewSimulation(39)
	expected := rng.NewSimulation(39)
	w := NewSliced(1, nil)
	w.SetSimulationRNG(&sim)
	// 65537 narrows to bound one, so heading selection returns zero without
	// advancing and only the following full-domain invocation consumes a draw.
	if got := p28CreateHeading(t, w, p28AngleDef("narrow", 65537)); got != 32768 {
		t.Fatalf("narrowed bound heading = %d, want 32768", got)
	}
	_ = expected.Uint32n(0x10000)
	if sim.State != expected.State || sim.Draws() != 1 {
		t.Fatalf("narrowed bound stream = (%d,%d), want (%d,1)", sim.State, sim.Draws(), expected.State)
	}
}

func TestP28ForcedSlotSuccessRunsCommonInitializer(t *testing.T) {
	sim := rng.NewSimulation(41)
	expected := rng.NewSimulation(41)
	w := NewSliced(2, nil)
	w.SetSimulationRNG(&sim)
	def := p28AngleDef("forced-success", 4096)
	draw := expected.Uint32n(4096)
	wantHeading := uint16(int32(int16(uint16(draw))) - int32(uint16(4096)>>1) + 32768)
	_ = expected.Uint32n(0x10000)
	h, err := w.CreateWithForcedSlot(def, 0, 0, 0, 0, pool.Handle(2))
	if err != nil {
		t.Fatalf("CreateWithForcedSlot: %v", err)
	}
	if got := w.Unit(h).Move.Heading; got != wantHeading {
		t.Fatalf("forced-slot heading = %d, want %d", got, wantHeading)
	}
	if sim.State != expected.State || sim.Draws() != 2 {
		t.Fatalf("forced-slot stream = (%d,%d), want (%d,2)", sim.State, sim.Draws(), expected.State)
	}
}

func TestP28BuildAngleFailuresBeforeInitializerConsumeNoDraws(t *testing.T) {
	t.Run("definition limit", func(t *testing.T) {
		sim := rng.NewSimulation(41)
		w := NewSliced(2, nil)
		w.SetSimulationRNG(&sim)
		def := p28AngleDef("limited", 4096)
		def.LimitEnabled = true
		def.Limit = 1
		_ = p28CreateHeading(t, w, def)
		beforeState, beforeDraws := sim.State, sim.Draws()
		if _, err := w.Create(def, 0, 0, 0, 0); err == nil {
			t.Fatal("over-limit allocation unexpectedly succeeded")
		}
		if sim.State != beforeState || sim.Draws() != beforeDraws {
			t.Fatalf("limit failure advanced stream: (%d,%d) -> (%d,%d)", beforeState, beforeDraws, sim.State, sim.Draws())
		}
	})
	t.Run("slice full", func(t *testing.T) {
		sim := rng.NewSimulation(43)
		w := NewSliced(1, nil)
		w.SetSimulationRNG(&sim)
		def := p28AngleDef("full", 4096)
		_ = p28CreateHeading(t, w, def)
		beforeState, beforeDraws := sim.State, sim.Draws()
		if _, err := w.Create(def, 0, 0, 0, 0); err == nil {
			t.Fatal("slice-full allocation unexpectedly succeeded")
		}
		if sim.State != beforeState || sim.Draws() != beforeDraws {
			t.Fatalf("pool failure advanced stream: (%d,%d) -> (%d,%d)", beforeState, beforeDraws, sim.State, sim.Draws())
		}
	})
	t.Run("forced validation", func(t *testing.T) {
		sim := rng.NewSimulation(47)
		w := NewSliced(1, nil)
		w.SetSimulationRNG(&sim)
		beforeState, beforeDraws := sim.State, sim.Draws()
		if _, err := w.CreateWithForcedSlot(p28AngleDef("forced", 4096), 0, 0, 0, 0, pool.Handle(2)); err == nil {
			t.Fatal("out-of-slice forced allocation unexpectedly succeeded")
		}
		if sim.State != beforeState || sim.Draws() != beforeDraws {
			t.Fatalf("forced validation failure advanced stream: (%d,%d) -> (%d,%d)", beforeState, beforeDraws, sim.State, sim.Draws())
		}
	})
}

func TestP28StrictBindingFailureRollsBackAllocation(t *testing.T) {
	sim := rng.NewSimulation(53)
	w := NewSliced(1, nil)
	w.SetSimulationRNG(&sim)
	w.SetCOBBinder(func(*Unit) error { return errors.New("fixture binding failure") })
	if _, err := w.Create(p28AngleDef("binding", 4096), 0, 0, 0, 0); err == nil {
		t.Fatal("strict binding failure unexpectedly succeeded")
	}
	if got := w.Unit(1); got != nil {
		t.Fatalf("failed strict binding retained unit: %+v", got)
	}
	// The failed allocation must release its slot. RNG behavior is deliberately
	// not asserted because that Nanolathe-only failure boundary is unresolved.
	w.SetCOBBinder(nil)
	h, err := w.Create(p28AngleDef("replacement", 4096), 0, 0, 0, 0)
	if err != nil || h != 1 {
		t.Fatalf("released slot was not reusable: handle=%d err=%v", h, err)
	}
}

func TestP28RetailBuildAngles(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail assets: %v", err)
	}
	defs, err := content.CompileUnits(fs)
	if err != nil {
		t.Fatalf("compile retail units: %v", err)
	}
	for _, key := range []string{"armsolar", "armlab"} {
		def := defs[key]
		if def == nil {
			t.Fatalf("missing stock definition %s", key)
		}
		if got := uint16(def.BuildAngle); got != 4096 {
			t.Fatalf("%s BuildAngle = %d, want 4096", key, got)
		}
	}
}
