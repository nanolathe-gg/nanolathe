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
	binding := &cob.Binding{Program: vm.Program(), VM: vm, Callbacks: cob.NewCallbackBridge(vm)}
	u := &Unit{}
	if err := u.AttachCOBBinding(binding); err == nil {
		t.Fatal("attachment without D+wake Create unexpectedly succeeded")
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

func TestAttachCOBBindingRejectsNilVM(t *testing.T) {
	u := &Unit{}
	if err := u.AttachCOBBinding(&cob.Binding{}); err == nil {
		t.Fatal("nil-VM binding was accepted")
	}
}

func TestAttachCOBBindingRejectsNilBinding(t *testing.T) {
	u := &Unit{}
	if err := u.AttachCOBBinding(nil); err == nil {
		t.Fatal("nil binding was accepted")
	}
}

type creationCallbackTrace struct {
	steps []string
	query []int32
	aim   []int32
}

func (c *creationCallbackTrace) QueryWeapon(slot cob.WeaponSlot) cob.CallbackResult {
	names := [...]string{"Primary", "Secondary", "Tertiary"}
	c.steps = append(c.steps, "query:"+names[slot])
	value := int32(slot) + 10
	if len(c.query) == int(slot) {
		c.query = append(c.query, value)
	} else {
		value++
	}
	return cob.CallbackResult{Values: [4]int32{value}}
}

func (c *creationCallbackTrace) AimFromWeapon(slot cob.WeaponSlot) cob.CallbackResult {
	names := [...]string{"Primary", "Secondary", "Tertiary"}
	c.steps = append(c.steps, "aim:"+names[slot])
	if slot == cob.WeaponPrimary {
		return cob.CallbackResult{Values: [4]int32{-1}}
	}
	value := int32(slot) + 20
	c.aim = append(c.aim, value)
	return cob.CallbackResult{Values: [4]int32{value}}
}

func (c *creationCallbackTrace) SetMaxReloadTime(value int32) cob.CallbackResult {
	c.steps = append(c.steps, "max")
	return cob.CallbackResult{}
}

func TestCreationCallbackOrdering(t *testing.T) {
	trace := &creationCallbackTrace{}
	u := &Unit{}
	initializeCreationCallbacks(u, trace, 99)
	wantSteps := []string{
		"query:Primary", "aim:Primary", "query:Primary",
		"query:Secondary", "aim:Secondary",
		"query:Tertiary", "aim:Tertiary", "max",
	}
	if len(trace.steps) != len(wantSteps) {
		t.Fatalf("creation steps %v want %v", trace.steps, wantSteps)
	}
	for i := range wantSteps {
		if trace.steps[i] != wantSteps[i] {
			t.Fatalf("creation step %d=%q want %q", i, trace.steps[i], wantSteps[i])
		}
	}
	for i, want := range []int32{10, 11, 12} {
		if u.Slots[i].MuzzlePiece != want {
			t.Fatalf("slot %d muzzle=%d want %d", i, u.Slots[i].MuzzlePiece, want)
		}
	}
	for i, want := range []int32{11, 21, 22} {
		if u.Slots[i].AimOriginPiece != want {
			t.Fatalf("slot %d aim origin=%d want %d", i, u.Slots[i].AimOriginPiece, want)
		}
	}
}

func TestRequiredCOBEntryPointsLeavesCreateOptional(t *testing.T) {
	if got := RequiredCOBEntryPoints(nil); len(got) != 0 {
		t.Fatalf("nil requirements = %v", got)
	}
	def := &content.UnitDef{Builder: true, BMCode: false, CanMove: true}
	if got := RequiredCOBEntryPoints(def); len(got) != 0 {
		t.Fatalf("capability requirements = %v", got)
	}
}
