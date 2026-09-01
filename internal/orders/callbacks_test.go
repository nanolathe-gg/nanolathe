package orders

// Cleanup-callback contract tests [R-ORDER-02 §2]: the tombstone gates ONLY
// the TargetCleared weapon-slot walk; the StopBuilding emission runs on every
// removal path regardless of the tombstone and is kept alive through a
// code-9 last-record re-arm; the TargetCleared walk enforces the slot guard
// (control byte bit 1 set, bit 4 clear — bit 4 then set) plus the
// not-already-empty target test and arranges the script event with the
// first argument cell (slotIndex, fillers 0), script-only with no network
// event; the cancel-notification mask 2 is delivered through the record's own
// handler when its dynamic gate still holds bit 1 (value 2) at removal; the
// code-9 completion flag stays write-only.

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// cbProgram builds a synthetic program whose named entry points exist so the
// bridge's StartByName succeeds. The code words never execute: the tests
// inspect the arranged arguments on the started thread stacks.
func cbProgram(names ...string) *cob.Program {
	scripts := make(map[string]int, len(names))
	byID := make([]int, 0, len(names))
	// Every arranged script shares one keep-alive body: push 0, sleep 0. The
	// sleep-0 one-tick minimum leaves the thread sleeping across the test's
	// inspection window, so zero-arity arrangements (StopBuilding) stay
	// observable in the thread table after a pump-driven drain.
	code := []uint32{0, 0x10021001, 0, 0x10013000, 0}
	for _, name := range names {
		scripts[name] = 1
		byID = append(byID, 1)
	}
	return &cob.Program{Code: code, Scripts: scripts, Pieces: []string{"base"}, ScriptsByID: byID}
}

// cbUnit attaches a synthetic production binding (VM + callback bridge) to a
// bare unit, mirroring the combat test fixture pattern.
func cbUnit(prog *cob.Program) (*units.Unit, *cob.VM) {
	u := &units.Unit{Handle: 1}
	vm := cob.NewVM(prog)
	binding := &cob.Binding{VM: vm, Callbacks: cob.NewCallbackBridge(vm)}
	u.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
	return u, vm
}

// startedArgs collects the arranged argument lists of all threads started on
// the VM, in thread (allocation) order. Each entry is the thread's argument
// window Stack[0:SP] — empty for arity-0 arrangements like StopBuilding.
func startedArgs(vm *cob.VM) [][]int32 {
	out := make([][]int32, 0, 8)
	for i := range vm.Threads {
		t := &vm.Threads[i]
		if t.Status == cob.ThreadIdle {
			continue
		}
		args := make([]int32, t.SP)
		copy(args, t.Stack[:t.SP])
		out = append(out, args)
	}
	return out
}

// assignSlot seeds a weapon slot for the cleanup-walk fixtures. The control
// byte's bit 1 — "the slot is enabled" [04 R-ORD-01 §7] — is carried in this
// build by the resolved weapon definition itself, so a slot the walk is
// expected to touch has to have one. `flags` remains the [06 §1.2] flags word.
func assignSlot(u *units.Unit, idx int, flags uint8, target units.Target) {
	if s := u.SlotAt(idx); s != nil {
		s.Weapon = &content.WeaponDef{}
		s.Flags = flags
		s.Target = target
	}
}

func argsEqual(got, want []int32) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestTargetClearedWalkGuardBitsAndArgs(t *testing.T) {
	u, vm := cbUnit(cbProgram("TargetCleared"))
	assignSlot(u, 0, 0x02, units.Target{Kind: units.TargetUnit, Unit: 7})      // assigned, unit target
	assignSlot(u, 1, 0x02, units.Target{Kind: units.TargetGround, X: 1, Z: 2}) // assigned, ground target
	assignSlot(u, 2, 0x02, units.Target{Kind: units.TargetUnit, Unit: 9})      // assigned
	u.SlotAt(2).OrderControl |= slotOrderInhibit                               // bit 4 already set: skipped

	clearWeaponBuildTargets(u)

	args := startedArgs(vm)
	if len(args) != 2 {
		t.Fatalf("TargetCleared arrangements %d, want 2 (slots 0 and 1 only)", len(args))
	}
	want := [][]int32{{0}, {1}}
	for i, w := range want {
		if !argsEqual(args[i], w) {
			t.Fatalf("arrangement %d args %v, want %v", i, args[i], w)
		}
	}
	if s := u.SlotAt(0); s.Target.Kind != units.TargetNone || s.OrderControl&slotOrderInhibit == 0 {
		t.Fatalf("slot 0 not cleared: target %v control %x", s.Target, s.OrderControl)
	}
	if s := u.SlotAt(1); s.Target.Kind != units.TargetNone || s.OrderControl&slotOrderInhibit == 0 {
		t.Fatalf("slot 1 not cleared: target %v control %x", s.Target, s.OrderControl)
	}
	if s := u.SlotAt(2); s.Target.Kind != units.TargetUnit || s.Target.Unit != 9 || s.OrderControl&slotOrderInhibit == 0 {
		t.Fatalf("slot 2 must be skipped (bit 4 already set): target %v control %x", s.Target, s.OrderControl)
	}
}

func TestTargetClearedWalkAlreadyEmptyAndMissingScript(t *testing.T) {
	t.Run("already empty target: latch consumed, no signal", func(t *testing.T) {
		u, vm := cbUnit(cbProgram("TargetCleared"))
		assignSlot(u, 0, 0x02, units.Target{Kind: units.TargetNone})
		clearWeaponBuildTargets(u)
		if got := startedArgs(vm); len(got) != 0 {
			t.Fatalf("empty target signalled TargetCleared %v, want no arrangement", got)
		}
		if s := u.SlotAt(0); s.OrderControl&slotOrderInhibit == 0 {
			t.Fatalf("bit 4 must be set before the empty test, control %x", s.OrderControl)
		}
	})
	t.Run("no TargetCleared script: words still reset, arrange no-op", func(t *testing.T) {
		u, vm := cbUnit(cbProgram("Create")) // no TargetCleared entry
		assignSlot(u, 0, 0x02, units.Target{Kind: units.TargetUnit, Unit: 5})
		clearWeaponBuildTargets(u)
		if got := startedArgs(vm); len(got) != 0 {
			t.Fatalf("missing script started threads %v, want no-op", got)
		}
		s := u.SlotAt(0)
		if s.Target.Kind != units.TargetNone || s.OrderControl&slotOrderInhibit == 0 {
			t.Fatalf("words must reset and latch set even without the script: target %v control %x", s.Target, s.OrderControl)
		}
	})
}

// stopBuildingCount counts arranged calls carrying the seven-zero-argument
// StopBuilding shape, distinguishing them from TargetCleared arrangements
// whose third cell is the literal 1.
func stopBuildingCount(args [][]int32) int {
	n := 0
	for _, a := range args {
		if len(a) == 0 { // arity 0 [R-UNIT-06 §4]
			n++
		}
	}
	return n
}

func targetClearedCount(args [][]int32) int {
	n := 0
	for _, a := range args {
		if len(a) == 1 { // slot index as the first argument [R-UNIT-06 §4]
			n++
		}
	}
	return n
}

// cleanupCase gives one queue bound to one scripted unit. The unit's slot 0
// holds an assigned weapon slot with a live target, so each case proves
// whether the tombstone gated the TargetCleared walk; the binding lookup
// resolves the records' owner to that unit exactly as the session's does.
type cleanupCase struct {
	q  *Queue
	u  *units.Unit
	vm *cob.VM
}

func newCleanupCase(t *testing.T) *cleanupCase {
	t.Helper()
	u, vm := cbUnit(cbProgram("StopBuilding", "TargetCleared"))
	assignSlot(u, 0, 0x02, units.Target{Kind: units.TargetUnit, Unit: 9})
	sim := rng.SimulationFromState(0x2A5F17)
	q := &Queue{binding: &QueueBinding{SimRNG: &sim, Lookup: func(pool.Handle) *units.Unit { return u }}}
	return &cleanupCase{q: q, u: u, vm: vm}
}

func (c *cleanupCase) flagged(id ID) *Node {
	n := &Node{ID: id, Owner: c.u.Handle, Deadline: -1}
	n.Flags |= FlagStopBuildingPending
	return n
}

func TestStopBuildingEmittedOnPrimaryPumpRemovals(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatalf("descriptor lookup failed")
	}
	t.Run("code 5 removal: StopBuilding then TargetCleared, flag cleared", func(t *testing.T) {
		c := newCleanupCase(t)
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 5 })
		defer restore()
		n := c.flagged(moveID)
		c.q.Push(moveID, *n)
		node := c.q.primary[0]
		node.Flags |= FlagStopBuildingPending
		clearGates(c.q)
		c.q.Pump(c.u, 100)
		if len(c.q.primary) != 0 {
			t.Fatalf("record kept, want removed")
		}
		args := startedArgs(c.vm)
		if stopBuildingCount(args) != 1 || targetClearedCount(args) != 1 {
			t.Fatalf("arrangements %v: want one StopBuilding and one TargetCleared", args)
		}
		if !argsEqual(args[0], []int32{}) || !argsEqual(args[1], []int32{0}) {
			t.Fatalf("emission order %v, want StopBuilding before TargetCleared", args)
		}
		if node.Flags&FlagStopBuildingPending != 0 {
			t.Fatalf("pending flag not cleared by cleanup")
		}
		if s := c.u.SlotAt(0); s.Target.Kind != units.TargetNone {
			t.Fatalf("non-tombstoned head must run the weapon-target clear, slot target %v", s.Target)
		}
	})
	t.Run("code 9 last-record re-arm keeps the build alive", func(t *testing.T) {
		c := newCleanupCase(t)
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 9 })
		defer restore()
		c.q.Push(moveID, *c.flagged(moveID))
		node := c.q.primary[0]
		node.Flags |= FlagStopBuildingPending
		clearGates(c.q)
		before := c.q.simForJitter().Draws()
		c.q.Pump(c.u, 100)
		if len(c.q.primary) != 1 {
			t.Fatalf("record removed, want re-armed in place")
		}
		if node.Flags&FlagStopBuildingPending == 0 {
			t.Fatalf("pending flag cleared, want untouched (no cleanup on re-arm)")
		}
		if got := startedArgs(c.vm); len(got) != 0 {
			t.Fatalf("re-arm emitted callbacks %v, want none (StartBuilding keeps running)", got)
		}
		if d := c.q.simForJitter().Draws() - before; d != 1 {
			t.Fatalf("draw delta %d, want exactly the re-arm RNG(30) draw", d)
		}
	})
	t.Run("code 9 non-last removal emits StopBuilding", func(t *testing.T) {
		c := newCleanupCase(t)
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			if n.Param1 == 1 {
				return 9
			}
			n.DynamicGate = 0x400
			return 2
		})
		defer restore()
		c.q.Push(moveID, Node{Param1: 1})
		c.q.Push(moveID, Node{Param1: 2})
		c.q.primary[0].Flags |= FlagStopBuildingPending
		c.q.primary[0].Owner = c.u.Handle
		clearGates(c.q)
		c.q.Pump(c.u, 100)
		if len(c.q.primary) != 1 {
			t.Fatalf("head not removed")
		}
		if stopBuildingCount(startedArgs(c.vm)) != 1 {
			t.Fatalf("non-last code-9 removal must emit StopBuilding")
		}
	})
	t.Run("cancel-all: flagged non-head record emits StopBuilding, tombstone gates the target clear", func(t *testing.T) {
		c := newCleanupCase(t)
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 7 })
		defer restore()
		c.q.Push(moveID, Node{Param1: 1})
		c.q.Push(moveID, Node{Param1: 2})
		tail := c.q.primary[1]
		tail.Owner = c.u.Handle
		tail.Flags |= FlagStopBuildingPending
		clearGates(c.q)
		c.q.Pump(c.u, 100)
		if len(c.q.primary) != 0 || len(c.q.secondary) != 0 {
			t.Fatalf("cancel-all left records behind")
		}
		args := startedArgs(c.vm)
		if stopBuildingCount(args) != 1 {
			t.Fatalf("arrangements %v: StopBuilding must run on the tombstoned removal too", args)
		}
		if targetClearedCount(args) != 0 {
			t.Fatalf("tombstoned record emitted TargetCleared")
		}
		if s := c.u.SlotAt(0); s.Target.Kind != units.TargetUnit {
			t.Fatalf("tombstone must gate the weapon-target clear, slot target %v", s.Target)
		}
	})
}

func TestStopBuildingEmittedOnSecondaryAndCancelRemovals(t *testing.T) {
	buildID := Lookup("BuildWeapon")
	moveID := Lookup("Move_Ground")
	if buildID == 0 || moveID == 0 {
		t.Fatalf("descriptor lookup failed")
	}
	t.Run("secondary removal: rear record always tombstoned, StopBuilding still emitted", func(t *testing.T) {
		c := newCleanupCase(t)
		restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 9 }) // plain removal
		defer restore()
		n := c.flagged(buildID)
		c.q.PushSecondary(buildID, *n)
		node := c.q.secondary[0]
		node.Flags |= FlagStopBuildingPending
		clearGates(c.q)
		c.q.Pump(c.u, 100)
		if len(c.q.secondary) != 0 {
			t.Fatalf("secondary record kept")
		}
		if node.Flags&FlagTombstone == 0 {
			t.Fatalf("rear record must always be tombstoned [R-ORDER-02 §2]")
		}
		args := startedArgs(c.vm)
		if stopBuildingCount(args) != 1 {
			t.Fatalf("arrangements %v: StopBuilding is not tombstone-gated", args)
		}
		if targetClearedCount(args) != 0 {
			t.Fatalf("tombstoned rear record emitted TargetCleared")
		}
	})
	t.Run("cancel-by-negative: flagged tail emits StopBuilding, tombstone gates the clear", func(t *testing.T) {
		c := newCleanupCase(t)
		n1, n2 := Node{Param1: 5}, Node{Param1: 5}
		c.q.Push(moveID, n1)
		c.q.Push(moveID, n2)
		tail := c.q.primary[1]
		tail.Owner = c.u.Handle
		tail.Flags |= FlagStopBuildingPending
		clearGates(c.q)
		ok := c.q.CancelTailMost(func(n Node) bool { return n.Param1 == 5 })
		if !ok || len(c.q.primary) != 1 {
			t.Fatalf("cancel tail-most failed")
		}
		args := startedArgs(c.vm)
		if stopBuildingCount(args) != 1 || targetClearedCount(args) != 0 {
			t.Fatalf("arrangements %v: want StopBuilding without TargetCleared", args)
		}
	})
	t.Run("purge: flagged unprotected record emits StopBuilding", func(t *testing.T) {
		c := newCleanupCase(t)
		c.q.Push(moveID, Node{Param1: 1})
		c.q.Push(moveID, Node{Param1: 2})
		tail := c.q.primary[1]
		tail.Owner = c.u.Handle
		tail.Flags |= FlagStopBuildingPending
		clearGates(c.q)
		c.q.PurgeUnprotected()
		if len(c.q.primary) != 0 {
			t.Fatalf("unprotected records survived the purge")
		}
		if stopBuildingCount(startedArgs(c.vm)) != 1 {
			t.Fatalf("purge removal must emit StopBuilding")
		}
	})
}

func TestEmitStartBuildingSetsPendingFlagAndArgs(t *testing.T) {
	// The first argument is the bearing from the builder to its work target,
	// relative to the builder's own heading [04 R-CB-01 §3]. It was the
	// record's CreationTick & 0xffff while the argument was believed to be an
	// order-record identity; the census of scripts reading it as an angle is
	// what the correction explains.
	t.Run("arranges the relative bearing to the work target and sets the record flag", func(t *testing.T) {
		u, vm := cbUnit(cbProgram("StartBuilding"))
		// Builder at the origin, work target 64 world units toward +X. The
		// bearing whose position step travels +X is 49152 [04 R-MOV-01 §4].
		u.X, u.Z = 0, 0
		u.Move.Heading = 0
		n := &Node{ID: Lookup("MobileBuild"), Owner: u.Handle, Deadline: -1, CreationTick: 0x12345678,
			GoalX: numeric.Fixed(64 << 16)}
		EmitStartBuilding(u, n)
		args := startedArgs(vm)
		if len(args) != 1 || !argsEqual(args[0], []int32{49152}) {
			t.Fatalf("StartBuilding arrange %v, want [49152] [04 R-CB-01 §3]", args)
		}
		if n.Flags&FlagStopBuildingPending == 0 {
			t.Fatalf("emitter must set the record's pending flag")
		}
	})
	t.Run("subtracts the builder's own heading", func(t *testing.T) {
		u, vm := cbUnit(cbProgram("StartBuilding"))
		u.X, u.Z = 0, 0
		u.Move.Heading = 49152 // already facing the target
		n := &Node{ID: Lookup("MobileBuild"), Owner: u.Handle, Deadline: -1,
			GoalX: numeric.Fixed(64 << 16)}
		EmitStartBuilding(u, n)
		args := startedArgs(vm)
		if len(args) != 1 || !argsEqual(args[0], []int32{0}) {
			t.Fatalf("StartBuilding arrange %v, want [0]: a builder already facing its target gets a zero relative bearing [04 R-CB-01 §3]", args)
		}
	})
	t.Run("no script: arrange no-ops, flag still set", func(t *testing.T) {
		u := &units.Unit{Handle: 1} // no production binding
		n := &Node{ID: Lookup("MobileBuild"), Owner: u.Handle, Deadline: -1}
		EmitStartBuilding(u, n)
		if n.Flags&FlagStopBuildingPending == 0 {
			t.Fatalf("flag is written by the emitter regardless of script presence")
		}
	})
}

func TestCancelNotificationMaskDeliveredOnRemoval(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatalf("descriptor lookup failed")
	}
	c := newCleanupCase(t)
	seen := map[uint32][]uint32{} // Param1 -> satisfied values seen
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		seen[n.Param1] = append(seen[n.Param1], s)
		return 2
	})
	defer restore()
	// Head waits on gate 1 (a plain wait): removal must NOT deliver mask 2.
	// Tail waits on gate 2 (the cancel-current bit): removal must deliver it.
	c.q.Push(moveID, Node{Param1: 1})
	c.q.Push(moveID, Node{Param1: 2})
	c.q.primary[0].DynamicGate = 1
	c.q.primary[1].DynamicGate = 2
	c.q.primary[1].Owner = c.u.Handle
	c.q.primary[1].Deadline = -1
	c.q.CancelAll()
	if got := seen[2]; len(got) != 1 || got[0] != 2 {
		t.Fatalf("cancel-current notification deliveries %v, want exactly one mask 2", got)
	}
	if got := seen[1]; len(got) != 0 {
		t.Fatalf("gate without bit 1 delivered mask 2: %v", got)
	}
}

func TestCleanupLeavesCompletionFlagWriteOnly(t *testing.T) {
	// [R-ORDER-02 §2] the completion flag has no reader: cleanup must not
	// clear it, branch on it, or otherwise consume it — removal behavior is
	// identical with and without the flag.
	moveID := Lookup("Move_Ground")
	c := newCleanupCase(t)
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return 5 })
	defer restore()
	n := &Node{ID: moveID, Owner: c.u.Handle, Deadline: -1}
	n.Flags |= FlagRetryMark | FlagTombstone
	c.q.primary = append(c.q.primary, n)
	c.q.cleanupNode(n)
	if n.Flags&FlagRetryMark == 0 {
		t.Fatalf("cleanup consumed the completion flag; it is write-only state")
	}
}
