package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
)

func TestAttachCOBBindingRequiresCreateAndAttachesStrictState(t *testing.T) {
	vm := cob.NewVM(&cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"Create": 0},
		Pieces:      []string{"base"},
		ScriptsByID: []int{0},
	})
	binding := &cob.Binding{VM: vm, Callbacks: cob.NewCallbackBridge(vm)}
	u := &Unit{}
	if err := u.AttachCOBBinding(binding); err == nil {
		t.Fatal("attachment without mode-I Create unexpectedly succeeded")
	}
	if got := binding.Callbacks.Create(); !got.Started {
		t.Fatalf("Create = %#v", got)
	}
	binding.CreateInvoked = true
	if err := u.AttachCOBBinding(binding); err != nil {
		t.Fatalf("AttachCOBBinding: %v", err)
	}
	if u.GetScript() != vm || u.COBBinding() != binding {
		t.Fatal("strict binding was not retained on unit")
	}
	if err := u.AttachCOBBinding(binding); err == nil {
		t.Fatal("duplicate attachment unexpectedly succeeded")
	}
}

func TestRequiredCOBEntryPointsDoesNotGuessCapabilities(t *testing.T) {
	if got := RequiredCOBEntryPoints(nil); len(got) != 1 || got[0] != "Create" {
		t.Fatalf("nil requirements = %v", got)
	}
	def := &content.UnitDef{Builder: true, BMCode: false, CanMove: true}
	if got := RequiredCOBEntryPoints(def); len(got) != 1 || got[0] != "Create" {
		t.Fatalf("capability requirements = %v", got)
	}
}
