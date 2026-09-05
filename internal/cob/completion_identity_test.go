package cob

import "testing"

// The two tests below lock the completion-ownership rule of [04 §4.2],
// [04 §4.3] and [04 §5.3]: a thread's completion receiver belongs to ONE
// allocation of one slot. Signal termination never invokes it, a new child
// never inherits it, and an explicit return delivers to it exactly once —
// before the slot is released and therefore before anything can reuse the slot.
//
// Both fixtures reach the same hazard the engine reaches through its ordinary
// scheduler: eight slots, lowest-free allocation, immediate reuse, and a drain
// that scans slots 0..7 once. A slot freed at slot 0 and refilled from slot 1
// is refilled inside that same scan, so the freed callback and its replacement
// coexist within one Drain — which is exactly when a slot-indexed receiver
// answers the wrong script.

// buildReuseProg authors a three-script COB whose second script frees and
// refills slot 0 within one drain. aimBody is the AimPrimary body.
func buildReuseProg(t *testing.T, aimBody []uint32, signalFirst bool) *Program {
	t.Helper()
	code := append([]uint32(nil), aimBody...)
	replaceAt := uint32(len(code))
	if signalFirst {
		// Move Replace off signal mask 1 so it survives its own signal, then
		// signal mask 1 — the mask every engine-started root thread carries
		// [04 §4.2] — which releases the sleeping AimPrimary in slot 0.
		code = append(code, 0x10021001, 2, 0x10068000) // push 2; set-signal-mask
		code = append(code, 0x10021001, 1, 0x10067000) // push 1; signal
	}
	code = append(code, 0x10061000, 2, 0)          // start-script Child (id 2), no arguments
	code = append(code, 0x10021001, 0, 0x10065000) // push 0; return
	childAt := uint32(len(code))
	code = append(code, 0x10021001, 7, 0x10065000) // push 7; return — the replacement's own value
	prog, err := Load(makeCOB(code,
		[]string{"AimPrimary", "Replace", "Child"},
		[]uint32{0, replaceAt, childAt},
		[]string{"base"}))
	if err != nil {
		t.Fatal(err)
	}
	return prog
}

func TestSignalledAimReceivesNothingFromTheSlotsNextOccupant(t *testing.T) {
	// AimPrimary sleeps, so it is still parked in slot 0 when Replace signals
	// it dead and starts Child into the slot it just freed. Signal termination
	// has no completion receiver [04 §4.3], and a new child begins with none
	// [04 §4.2], so Child's explicit return 7 belongs to nobody: the Aim
	// receiver must never be called.
	b := NewCallbackBridge(NewVM(buildReuseProg(t,
		[]uint32{0x10021001, 1000, 0x10013000, 0x10021001, 0, 0x10065000}, true)))
	var got []CallbackReturn
	if res := b.Aim(WeaponPrimary, 0, 0, func(r CallbackReturn) { got = append(got, r) }); !res.Started || res.Thread != 0 {
		t.Fatalf("Aim start = %+v, want slot 0", res)
	}
	if res := b.Deferred("Replace", nil, nil); !res.Started || res.Thread != 1 {
		t.Fatalf("Replace start = %+v, want slot 1", res)
	}
	b.Drain(1) // slot 0 signalled dead, slot 0 reallocated to Child
	if !b.VM.IsThreadAlive(0) {
		t.Fatal("fixture did not refill slot 0 within the drain")
	}
	b.Drain(1) // Child explicitly returns 7 from the reused slot
	if len(got) != 0 {
		t.Fatalf("signalled Aim was answered by the slot's next occupant: %+v [04 §4.3][04 §5.3]", got)
	}
}

func TestCompletedAimDeliversItsOwnResultExactlyOnceBeforeReuse(t *testing.T) {
	// AimPrimary returns 1 explicitly in slot 0. Replace then refills slot 0
	// with a Child returning 7, in the same drain. The explicit return is
	// delivered by the return opcode itself — before the slot is released —
	// so the receiver sees 1, once, and never sees Child's 7 [04 §4.2].
	b := NewCallbackBridge(NewVM(buildReuseProg(t,
		[]uint32{0x10021001, 1, 0x10065000}, false)))
	var got []CallbackReturn
	if res := b.Aim(WeaponPrimary, 0, 0, func(r CallbackReturn) { got = append(got, r) }); !res.Started || res.Thread != 0 {
		t.Fatalf("Aim start = %+v, want slot 0", res)
	}
	if res := b.Deferred("Replace", nil, nil); !res.Started || res.Thread != 1 {
		t.Fatalf("Replace start = %+v, want slot 1", res)
	}
	b.Drain(1)
	if len(got) != 1 || !got[0].Explicit || got[0].Value != 1 || got[0].Name != "AimPrimary" || got[0].Thread != 0 {
		t.Fatalf("Aim receiver = %+v, want one explicit AimPrimary 1 on slot 0 [04 §4.2]", got)
	}
	if !b.VM.IsThreadAlive(0) {
		t.Fatal("fixture did not refill slot 0 within the drain")
	}
	b.Drain(1) // Child explicitly returns 7 from the reused slot
	if len(got) != 1 {
		t.Fatalf("Aim receiver called again after its slot was reused: %+v [04 §5.3]", got)
	}
}

func TestThreadIdentityIsNotReissuedAcrossReuse(t *testing.T) {
	// The identity is what makes the two tests above decidable: it names one
	// allocation, so a holder of a stale one is provably not the current
	// occupant even when the slot index matches [04 §4.2].
	prog, err := Load(makeCOB([]uint32{0x10021001, 1000, 0x10013000, 0x10021001, 0, 0x10065000},
		[]string{"AimPrimary"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatal(err)
	}
	vm := NewVM(prog)
	if got := vm.ThreadIdentity(0); got != 0 {
		t.Fatalf("unallocated slot identity = %d, want the 0 sentinel", got)
	}
	if !vm.StartByName("AimPrimary", nil) {
		t.Fatal("first start failed")
	}
	first := vm.ThreadIdentity(0)
	if first == 0 || !vm.ThreadAliveAs(0, first) {
		t.Fatalf("first allocation identity %d is not live", first)
	}
	vm.Signal(1)
	if vm.ThreadAliveAs(0, first) {
		t.Fatal("signalled allocation still reads live [04 §4.3]")
	}
	if !vm.StartByName("AimPrimary", nil) {
		t.Fatal("second start failed")
	}
	second := vm.ThreadIdentity(0)
	if second == first {
		t.Fatalf("reused slot 0 reissued identity %d [04 §4.2]", second)
	}
	if vm.ThreadAliveAs(0, first) || !vm.ThreadAliveAs(0, second) {
		t.Fatalf("identities confused after reuse: first=%d second=%d", first, second)
	}
	// A receiver cannot be attached to an allocation that is already over.
	if vm.SetThreadCompletion(0, first, func(int32) {}) {
		t.Fatal("a retired allocation accepted a completion receiver [04 §4.2]")
	}
}
