package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestCOBInBuildStancePortIsInstanceState(t *testing.T) {
	u := &Unit{}
	vm := cob.NewVM(&cob.Program{Code: []uint32{
		0x10021001, 1, // push value
		0x10021001, 5, // push INBUILDSTANCE id
		0x10082000, // set
	}})
	bindUnitPortHandlers(vm, u)
	vm.Threads[0].Status = cob.ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	if !u.InBuildStance {
		t.Fatal("COB set port 5 did not raise INBUILDSTANCE")
	}
	if vm.Threads[0].Status != cob.ThreadIdle {
		t.Fatalf("port-write thread status=%d, want idle", vm.Threads[0].Status)
	}
}

func TestClassifierEligibilityStatusLifecycle(t *testing.T) {
	world := New(4, nil)
	def := &content.UnitDef{UnitName: "status-lifecycle", MaxDamage: 100, Limit: -1}

	var observedAtCreate uint32
	world.OnCreate = func(_ pool.Handle, u *Unit) {
		observedAtCreate = u.Flags
	}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := world.Unit(h)
	if u == nil {
		t.Fatal("created unit is not live")
	}
	if u.Flags&ClassifierEligibleStatus == 0 {
		t.Fatalf("created unit status=%08x, want allocator eligibility bit", u.Flags)
	}
	if observedAtCreate&ClassifierEligibleStatus == 0 {
		t.Fatalf("OnCreate observed status=%08x, want allocator eligibility bit", observedAtCreate)
	}
	if u.InBuildStance {
		t.Fatal("allocator must not synthesize COB INBUILDSTANCE")
	}

	// Activation and construction progress are separate instance state; neither
	// is a writer for the classifier bit.
	u.SetActivated(false)
	u.Remaining = 1
	if u.Flags&ClassifierEligibleStatus == 0 {
		t.Fatalf("activation/construction state changed classifier status=%08x", u.Flags)
	}

	// The death notification observes the still-live record. The bit is cleared
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	var observedAtDeath uint32
	world.OnDeath = func(_ pool.Handle, _ DeathCause, dead *Unit) {
		observedAtDeath = dead.Flags
	}
	world.Destroy(h, DeathKilled)
	if observedAtDeath&ClassifierEligibleStatus == 0 {
		t.Fatalf("OnDeath observed status=%08x, want bit before finalization", observedAtDeath)
	}
	if u.Flags&ClassifierEligibleStatus == 0 {
		t.Fatalf("death mark cleared classifier bit before finalization: %08x", u.Flags)
	}
	world.Cleanup()
	if u.Flags&ClassifierEligibleStatus != 0 {
		t.Fatalf("cleanup left classifier bit set: %08x", u.Flags)
	}

	// Save/forced-slot reconstruction shares the allocator initializer.
	forced, err := world.CreateWithForcedSlot(def, 0, 0, 0, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	forcedUnit := world.Unit(forced)
	if forcedUnit == nil {
		t.Fatal("forced-slot unit is not live")
	}
	if got := forcedUnit.Flags; got&ClassifierEligibleStatus == 0 {
		t.Fatalf("forced-slot unit status=%08x, want allocator eligibility bit", got)
	}
	world.FreeImmediate(forced)
	if got := forcedUnit.Flags; got&ClassifierEligibleStatus != 0 {
		t.Fatalf("immediate free left classifier bit set: %08x", got)
	}
}
