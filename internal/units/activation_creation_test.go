package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
)

// spinOnActivateProgram is an authored stand-in for the shape every stock
// `activatewhenbuilt` building has: an `Activate` script that starts a piece
// animation which keeps running until `Deactivate` stops it. `armmex` spins its
// `arms`, `armsolar` opens its dishes. Nothing here is copied retail data — the
// opcodes are the documented COB encoding [04 §4.3][04 §4.6] and the body is
// two pushes and a spin.
//
// spin pops the speed from the top of the stack and the acceleration under it,
// and stores each divided by the latched tick denominator, truncated toward
// zero. Acceleration 0 takes the immediate fast path, so the piece advances by
// exactly speed/30 per drained tick [04 §4.6].
func spinOnActivateProgram() *cob.Program {
	const perTickSpeed = 100
	return &cob.Program{
		Code: []uint32{
			// Activate at word 0.
			0x10021001, 0, // push acceleration 0 — immediate, no ramp
			0x10021001, perTickSpeed * 30, // push speed; the handler divides by the denominator
			0x10003000, 1, model.AxisY, // spin piece 1 about Y
			0x10021001, 0, // push the return value
			0x10065000, // return
			// Create at word 9.
			0x10021001, 0,
			0x10065000,
		},
		Scripts:     map[string]int{"Activate": 0, "Create": 9},
		ScriptsByID: []int{0, 9},
		Pieces:      []string{"base", "arms"},
	}
}

func spinFixtureDef() *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader:  content.DefinitionHeader{CanonicalKey: content.CanonicalKey("spinfixture")},
		UnitName:          "spinfixture",
		MaxDamage:         100,
		Limit:             -1,
		ActivateWhenBuilt: true,
		OnOffable:         true,
		Script:            spinOnActivateProgram(),
	}
}

// spunBy drains `ticks` ticks and reports how far the animated piece turned.
func spunBy(t *testing.T, u *Unit, ticks int) uint16 {
	t.Helper()
	vm := u.GetScript()
	if vm == nil {
		t.Fatal("fixture unit has no script VM")
	}
	before := vm.Pieces[1].RotY
	for i := 0; i < ticks; i++ {
		vm.Drain(1)
	}
	return vm.Pieces[1].RotY - before
}

// TestActivateWhenBuiltRunsOnlyForAnAlreadyBuiltCreation locks the creation
// half of [04 R-SPEC-01 §12] site 1: the raise is conditioned on the creation
// service being called with its *already built* argument. A nanoframe is not
// created already-built, so the flag must not raise its edge at creation.
//
// The regression this exists to catch is not a wrong bit — it is a running
// script. Raising the edge and clearing the bit afterwards leaves `Activate`
// started, and the animation it began runs for the whole of the unit's build:
// a mex spun and a solar opened while still a nanoframe. Only the absence of
// motion proves the script never started, so this asserts on the piece.
func TestActivateWhenBuiltRunsOnlyForAnAlreadyBuiltCreation(t *testing.T) {
	def := spinFixtureDef()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)

	// Already-built creation: the flag raises the edge and the script runs.
	built, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("already-built create: %v", err)
	}
	prebuilt := w.Unit(built)
	if !prebuilt.Activated {
		t.Fatal("an already-built activatewhenbuilt unit is created active")
	}
	if got := spunBy(t, prebuilt, 10); got != 1000 {
		t.Fatalf("already-built unit turned %d over ten ticks, want 1000", got)
	}

	// Nanoframe creation: no raise, and — the part that regressed — no motion.
	frame, err := w.CreateNanoframe(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("nanoframe create: %v", err)
	}
	unbuilt := w.Unit(frame)
	if unbuilt.Activated {
		t.Fatal("a nanoframe is created inactive")
	}
	if got := spunBy(t, unbuilt, 10); got != 0 {
		t.Fatalf("nanoframe turned %d over ten ticks, want 0: its Activate script ran while it was still a frame", got)
	}

	// Site 2, build completion: the raise on that same frame is a true rising
	// edge, so `Activate` starts exactly once and only now does it animate.
	unbuilt.SetActivationEdge(true)
	if !unbuilt.Activated {
		t.Fatal("completion did not raise the activation edge")
	}
	if got := spunBy(t, unbuilt, 10); got != 1000 {
		t.Fatalf("completed unit turned %d over ten ticks, want 1000", got)
	}
	// A second raise is not an edge and must not start a second animation
	// thread [04 R-UNIT-06 §2].
	unbuilt.SetActivationEdge(true)
	if got := spunBy(t, unbuilt, 10); got != 1000 {
		t.Fatalf("repeated raise changed the rate: turned %d over ten ticks, want 1000", got)
	}
}
