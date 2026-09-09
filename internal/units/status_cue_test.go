package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// TestEdgeMachineRaisesTheFourStatusCueCodes locks the codes the unit edge
// machine raises and the edge test that gates them [03 R-AUD-01 §7]. The
// integer is the slot index of the static table of [03 §8.3]: 3 `activate`,
// 4 `deactivate`, 14 `cloak`, 15 `uncloak`. Only an actual change is an edge,
// and the sink is called with the unit that changed.
func TestEdgeMachineRaisesTheFourStatusCueCodes(t *testing.T) {
	u := &Unit{Handle: 1}
	var raised []uint8
	u.SetStatusCueSink(func(got *Unit, code uint8) {
		if got != u {
			t.Fatalf("the sink was handed unit %p, want %p", got, u)
		}
		raised = append(raised, code)
	})

	u.SetActivationEdge(true)
	u.SetActivationEdge(true) // not an edge [04 R-UNIT-06 §2]
	u.SetActivationEdge(false)
	u.SetCloakedInstance(true)
	u.SetCloakedInstance(true) // not an edge [05 R-ECO-01 §8]
	u.SetCloakedInstance(false)

	want := []uint8{
		StatusCueActivate, StatusCueDeactivate,
		StatusCueCloak, StatusCueUncloak,
	}
	if len(raised) != len(want) {
		t.Fatalf("raised %v, want %v [03 R-AUD-01 §7]", raised, want)
	}
	for i := range want {
		if raised[i] != want[i] {
			t.Fatalf("raised %v, want %v [03 R-AUD-01 §7]", raised, want)
		}
	}
}

// TestActivationCallbackRunsBeforeTheCue locks the ordering [05 R-ECO-01 §8]
// [03 R-AUD-01 §7]: on the activation bit's edges the COB `Activate` /
// `Deactivate` callback is raised BEFORE the cue. The observable is the started
// script thread — if the cue came first the count would still be zero when the
// sink ran.
func TestActivationCallbackRunsBeforeTheCue(t *testing.T) {
	def := spinFixtureDef()
	def.ActivateWhenBuilt = false // raise the edge explicitly below, not at creation
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	vm := u.GetScript()
	if vm == nil {
		t.Fatal("fixture unit has no script VM")
	}
	if u.Activated {
		t.Fatal("fixture unit was created active")
	}

	threadsAtCue := -1
	u.SetStatusCueSink(func(_ *Unit, code uint8) {
		if code != StatusCueActivate {
			t.Fatalf("code = %d, want %d", code, StatusCueActivate)
		}
		threadsAtCue = int(vm.ActiveThreadCount())
	})
	u.SetActivationEdge(true)

	if threadsAtCue < 1 {
		t.Fatalf("the cue ran with %d active script threads: the COB callback did not run first [05 R-ECO-01 §8]", threadsAtCue)
	}
}
