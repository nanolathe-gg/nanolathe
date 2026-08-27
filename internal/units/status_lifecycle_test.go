package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestCOBInBuildStancePortIsInstanceState(t *testing.T) {
	u := &Unit{}
	// The retail compiler emits the identifier first and the value second,
	// so the value sits on top of the stack: `push 5; push 1; set` is
	// INBUILDSTANCE <- 1 [R-P0-10] (asset census: armlab, armcom).
	vm := cob.NewVM(&cob.Program{Code: []uint32{
		0x10021001, 5, // push INBUILDSTANCE id
		0x10021001, 1, // push value
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

func TestCOBInstancePorts(t *testing.T) {
	u := &Unit{Activated: true}
	vm := cob.NewVM(&cob.Program{})
	ports := unitPortHandlers(vm, u)
	for _, tc := range []struct {
		port cob.Port
		set  func()
		get  func() bool
	}{
		{cob.Port(5), func() { ports[cob.Port(5)]([]int32{5, 1}) }, func() bool { return u.InBuildStance }},
		{cob.Port(6), func() { ports[cob.Port(6)]([]int32{6, 1}) }, func() bool { return u.Busy }},
		{cob.Port(18), func() { ports[cob.Port(18)]([]int32{18, 1}) }, func() bool { return u.YardOpen }},
		{cob.Port(19), func() { ports[cob.Port(19)]([]int32{19, 1}) }, func() bool { return u.BuggerOff }},
		{cob.Port(20), func() { ports[cob.Port(20)]([]int32{20, 1}) }, func() bool { return u.Armored }},
	} {
		tc.set()
		if !tc.get() || ports[tc.port]([]int32{int32(tc.port)}) != 1 {
			t.Fatalf("port %d did not round-trip", tc.port)
		}
	}
	ports[cob.Port(1)]([]int32{1, 0})
	if u.Activated {
		t.Fatal("activation port did not clear activation")
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
