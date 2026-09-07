package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
)

// TestCreateWithMoverModeInstallsModeAfterCOBCreate keeps the creator wrapper
// order while retaining the capture mode for the subsequent movement bootstrap.
func TestCreateWithMoverModeInstallsModeAfterCOBCreate(t *testing.T) {
	def := spinFixtureDef()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)
	var modes []uint8
	w.SetCOBBinder(func(u *Unit) error {
		modes = append(modes, u.Move.Mode)
		return nil
	})

	if _, err := w.CreateWithMoverMode(def, 0, 0, 0, 0, 2); err != nil {
		t.Fatalf("capture creator: %v", err)
	}
	if _, err := w.Create(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("ordinary creator: %v", err)
	}
	if _, err := w.CreateNanoframe(def, 0, 0, 0, 0); err != nil {
		t.Fatalf("nanoframe creator: %v", err)
	}
	if len(modes) != 3 || modes[0] != CreatedMoverMode || modes[1] != CreatedMoverMode || modes[2] != CreatedMoverMode {
		t.Fatalf("modes at COB Create = %v, want [%d %d %d]",
			modes, CreatedMoverMode, CreatedMoverMode, CreatedMoverMode)
	}
	if got := w.Unit(1).Move.Mode; got != 2 {
		t.Fatalf("capture creator mode after COB Create = %d, want 2 [05 R-WORK-01 §15]", got)
	}
}

// TestReplayOperationalStateUsesSetThenClearPasses locks the transfer writer's
// two calls. Each call writes the whole modeled byte before it sends its
// callbacks and cues, so the set pass's cloak notification precedes the clear
// pass's deactivate notification [05 R-ECO-01 §8][05 R-ECO-01 §9].
func TestReplayOperationalStateUsesSetThenClearPasses(t *testing.T) {
	vm := cob.NewVM(&cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"Deactivate": 0, "StartBuilding": 0},
		ScriptsByID: []int{0, 0},
	})
	bridge := cob.NewCallbackBridge(vm)
	u := &Unit{
		Activated: true,
		Armored:   true,
		ScriptState: &ScriptState{VM: vm, Binding: &cob.Binding{
			VM: vm, Callbacks: bridge,
		}},
	}
	var events []string
	checkState := func(at string, want uint8) {
		t.Helper()
		if got := u.OperationalState(); got != want {
			t.Errorf("%s observed operational byte %#x, want %#x", at, got, want)
		}
	}
	bridge.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Phase != "start" {
			return
		}
		switch e.Name {
		case "StartBuilding":
			checkState("StartBuilding", operationalMask)
			events = append(events, "StartBuilding")
		case "Deactivate":
			checkState("Deactivate", operationalCloaked|operationalBuilding)
			events = append(events, "Deactivate")
		}
	})
	u.SetStatusCueSink(func(_ *Unit, code uint8) {
		switch code {
		case StatusCueCloak:
			checkState("cloak cue", operationalMask)
			events = append(events, "Cloak")
		case StatusCueDeactivate:
			checkState("deactivate cue", operationalCloaked|operationalBuilding)
			events = append(events, "DeactivateCue")
		}
	})

	u.ReplayOperationalState(operationalCloaked | operationalBuilding)
	if got := u.OperationalState(); got != operationalCloaked|operationalBuilding {
		t.Fatalf("replayed operational byte %#x, want cloak and building only", got)
	}
	want := []string{"StartBuilding", "Cloak", "Deactivate", "DeactivateCue"}
	if len(events) != len(want) {
		t.Fatalf("operational replay events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("operational replay events = %v, want %v", events, want)
		}
	}
}
