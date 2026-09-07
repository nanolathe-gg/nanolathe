package cob

// Contract tests for [R-COB-01 §1] — VM initialization and engine-call
// frames: construction clears only thread status words and latches the tick
// denominator; the bind zero-fills piece state but not retail statics (the
// zeroing here is the recorded I11 determinism divergence); thread allocation
// leaves window words stale; the argument-carrying starter writes four
// physical cells with producer filler zeros beyond the arity while zero-arg
// starts leave the window stale; the blocked synchronous-query lifecycle
// forces the receiver to none, copies back immediately, and leaves the
// yielded thread alive; the disable-shadow opcode is an empty adapter.
//
// Fixtures are authored byte-for-byte by makeCOB/makeCOBWithStatics; no
// retail bytes are used.

import "testing"

const (
	polluterBase = 11 // first window marker; window word i holds polluterBase+i
	argMarker    = 0x777
)

// loadFrameProg authors a three-script COB: a polluter that fills its first
// ten window words with markers and exits, an arity-1 probe that copies window
// words 0,1,2,4 into statics 3,0,1,2 and returns window word 0, and a
// zero-arg probe that copies window word 0 into static 4 and returns.
func loadFrameProg(t *testing.T) *Program {
	t.Helper()
	code := []uint32{}
	idx := []uint32{}
	// Polluter: ten constant pushes then return. Window words 0..9 hold
	// polluterBase..polluterBase+9 after it runs.
	idx = append(idx, uint32(len(code)))
	for i := 0; i < 10; i++ {
		code = append(code, 0x10021001, uint32(polluterBase+i)) // push constant [04 §4.3] F
	}
	code = append(code, 0x10065000) // return [04 §4.3]
	// ProbeA (arity 1): window 1,2,4 are filler/stale probes; window 0 is the
	// argument. Ends with the argument still on the stack top for return.
	idx = append(idx, uint32(len(code)))
	code = append(code,
		0x10021002, 1, 0x10023004, 0, // push local 1; pop static 0 [04 §4.3] F
		0x10021002, 2, 0x10023004, 1, // push local 2; pop static 1
		0x10021002, 4, 0x10023004, 2, // push local 4; pop static 2
		0x10021002, 0, 0x10023004, 3, // push local 0; pop static 3
		0x10021002, 0, // push local 0 — remains on top for the return value
		0x10065000, // return [04 §4.3]
	)
	// ProbeB (zero-arg): the whole window must be stale.
	idx = append(idx, uint32(len(code)))
	code = append(code,
		0x10021002, 0, 0x10023004, 4, // push local 0; pop static 4
		0x10021002, 1, 0x10023004, 5, // push local 1; pop static 5
		0x10065000,
	)
	prog, err := Load(makeCOBWithStatics(6, code, []string{"Polluter", "ProbeA", "ProbeB"}, idx, []string{"base"}))
	if err != nil {
		t.Fatalf("Load frame fixture: %v", err)
	}
	return prog
}

func TestVMConstructionClearsOnlyStatusWords(t *testing.T) {
	// Construction clears each thread's status word and the active-thread
	// count and latches the tick denominator; PC, depth, timer, mask, wait
	// words and the 32 physical window words keep their prior content [R-COB-01 §1].
	prog := loadFrameProg(t)
	vm := NewVM(prog)
	if vm.tickDenom != 30 {
		t.Fatalf("tick denominator %d want 30 latched at construction [04 §4.6][R-COB-01 §1]", vm.tickDenom)
	}
	// Polluter fills slot 0's window with markers and exits.
	if !vm.StartByName("Polluter", nil) {
		t.Fatal("polluter start failed")
	}
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadIdle {
		t.Fatalf("polluter should have completed, status %d", vm.Threads[0].Status)
	}
	if got := vm.Threads[0].Stack[3]; got != polluterBase+3 {
		t.Fatalf("polluter window word 3 = %d want %d", got, polluterBase+3)
	}
	// Re-bind the same program: a construction over a used instance.
	vm.SetProgram(prog)
	for i := range vm.Threads {
		if vm.Threads[i].Status != ThreadIdle {
			t.Fatalf("construction must clear thread %d status [R-COB-01 §1]", i)
		}
	}
	if got := vm.Threads[0].Stack[3]; got != polluterBase+3 {
		t.Fatalf("construction cleared window word 3: %d want %d [R-COB-01 §1]", got, polluterBase+3)
	}
	if got := vm.Threads[0].Stack[9]; got != polluterBase+9 {
		t.Fatalf("construction cleared window word 9: %d want %d [R-COB-01 §1]", got, polluterBase+9)
	}
}

func TestThreadAllocationLeavesWindowStale(t *testing.T) {
	// A new allocation takes the lowest idle slot and seeds status/PC/top/
	// mask but does NOT clear the 32 physical window words — the next tenant of a
	// slot reads the previous tenant's bytes [R-COB-01 §1].
	prog := loadFrameProg(t)
	vm := NewVM(prog)
	if !vm.StartByName("Polluter", nil) {
		t.Fatal("polluter start failed")
	}
	vm.Drain(1) // polluter completes; its window stays in slot 0

	// ProbeA with arity 1 reuses slot 0 (lowest free).
	if !vm.StartByName("ProbeA", []int32{argMarker}) {
		t.Fatal("probe A start failed")
	}
	if vm.Threads[0].Status != ThreadRunning {
		t.Fatal("probe A must run on the lowest free slot 0 [01 §6.1] C13")
	}
	if vm.Threads[0].SP != 1 {
		t.Fatalf("logical top must be arity−1: SP %d want 1 [R-COB-01 §1]", vm.Threads[0].SP)
	}
	vm.Drain(1)
	// Filler cells beyond the arity are the traced producers' explicit zeros.
	if got := vm.getStatic(0); got != 0 {
		t.Fatalf("window word 1 (filler) = %d want 0 [R-COB-01 §1]", got)
	}
	if got := vm.getStatic(1); got != 0 {
		t.Fatalf("window word 2 (filler) = %d want 0 [R-COB-01 §1]", got)
	}
	// Window words above word 3 are untouched stale slot memory.
	if got := vm.getStatic(2); got != polluterBase+4 {
		t.Fatalf("window word 4 (stale) = %d want %d [R-COB-01 §1]", got, polluterBase+4)
	}
	if got := vm.getStatic(3); got != argMarker {
		t.Fatalf("window word 0 (argument) = %d want %#x [R-COB-01 §1]", got, argMarker)
	}
	if val, ok := vm.ConsumeReturn(0); !ok || val != argMarker {
		t.Fatalf("explicit return = (%d,%v) want (%#x,true)", val, ok, argMarker)
	}

	// Zero-argument start: ALL window words stay stale.
	if !vm.StartByName("ProbeB", nil) {
		t.Fatal("probe B start failed")
	}
	if vm.Threads[0].SP != 0 {
		t.Fatalf("zero-arg start logical top %d want −1 (SP 0) [R-COB-01 §1]", vm.Threads[0].SP)
	}
	vm.Drain(1)
	if got := vm.getStatic(4); got != argMarker {
		t.Fatalf("zero-arg start window word 0 = %d want stale %#x [R-COB-01 §1]", got, argMarker)
	}
	if got := vm.getStatic(5); got != argMarker {
		// Window word 1 holds the previous tenant's last written byte (the
		// arity-1 probe's final expression-stack write): a zero-arg start
		// must clear nothing [R-COB-01 §1].
		t.Fatalf("zero-arg start window word 1 = %d want stale %#x [R-COB-01 §1]", got, argMarker)
	}
}

func TestEngineStartWritesFourPhysicalCells(t *testing.T) {
	// The argument-carrying starter always writes window words 0..3 and only
	// then sets the logical top; arity 2 must still physically write fillers
	// into words 2..3 [R-COB-01 §1].
	prog := loadFrameProg(t)
	vm := NewVM(prog)
	if !vm.StartByName("Polluter", nil) {
		t.Fatal("polluter start failed")
	}
	vm.Drain(1)
	if !vm.StartByName("ProbeA", []int32{argMarker, 0x1234}) {
		t.Fatal("probe A start failed")
	}
	// Direct physical inspection before the run: four cells written, arity
	// sets only the logical top.
	stack := &vm.Threads[0].Stack
	if stack[0] != argMarker || stack[1] != 0x1234 {
		t.Fatalf("cells 0..1 = %d,%d want arg values [R-COB-01 §1]", stack[0], stack[1])
	}
	if stack[2] != 0 || stack[3] != 0 {
		t.Fatalf("cells 2..3 = %d,%d want producer filler zeros [R-COB-01 §1]", stack[2], stack[3])
	}
	if stack[4] != polluterBase+4 {
		t.Fatalf("cell 4 = %d want stale %d — starter wrote past the four-cell area [R-COB-01 §1]", stack[4], polluterBase+4)
	}
	vm.Drain(1)
}

func TestBindZeroFillsPieceState(t *testing.T) {
	// The bind zero-fills the per-piece animation array — every piece starts
	// with all animation words zero [R-COB-01 §1].
	prog := loadFrameProg(t)
	vm := NewVM(prog)
	for i := range vm.Pieces {
		for axis := 0; axis < 3; axis++ {
			if vm.Pieces[i].Trans[axis].Raw() != 0 {
				t.Fatalf("piece %d trans[%d] = %d want 0 [R-COB-01 §1]", i, axis, vm.Pieces[i].Trans[axis].Raw())
			}
		}
	}
	for i := range vm.anims {
		for axis := 0; axis < 3; axis++ {
			a := vm.anims[i].axes[axis]
			if a.moveTarget != 0 || a.moveSpeed != 0 || a.turnSpeed != 0 || a.spinSpeed != 0 || a.spinAccel != 0 {
				t.Fatalf("piece %d axis %d animation words not zero-filled [R-COB-01 §1]", i, axis)
			}
		}
	}
	// Script statics: retail's bind performs NO zeroing pass [R-COB-01 §1];
	// the allocator-provided initial content is Unknown and Nanolathe zeroes
	// as a recorded I11 determinism divergence (see SetProgram). This asserts
	// the divergence, not retail behavior.
	for i, v := range vm.statics {
		if v != 0 {
			t.Fatalf("statics[%d] = %d want 0 (Nanolathe I11 divergence, not retail) [R-COB-01 §1]", i, v)
		}
	}
}

func TestStartScriptChildEmptyDepth(t *testing.T) {
	// Script-started threads receive exactly the popped argument count with
	// the last popped landing highest, and start at an EMPTY logical depth —
	// the arguments sit physically in window words 0..argc−1 while SP is 0
	// [04 §4.3][R-COB-01 §1].
	caller := []uint32{
		0x10021001, 100, // push 100 — first pushed [04 §4.3]
		0x10021001, 200, // push 200 — caller top
		0x10061000, 1, 2, // start-script id 1 argc 2 [04 §4.3] E
		0x10065000, // caller returns
	}
	child := []uint32{
		0x10021002, 0, 0x10023004, 0, // child local 0 -> static 0
		0x10021002, 1, 0x10023004, 1, // child local 1 -> static 1
		0x10065000,
	}
	code := append(append([]uint32{}, caller...), child...)
	prog, err := Load(makeCOBWithStatics(2, code, []string{"Caller", "Child"}, []uint32{0, uint32(len(caller))}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vm := NewVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("caller start failed")
	}
	vm.Drain(1)
	// The child copies arguments into ascending window words, preserving the
	// caller's original push order despite popping from the caller's top.
	if got := vm.getStatic(0); got != 100 {
		t.Fatalf("child local 0 = %d want 100 (first pushed stays word 0) [04 §4.3]", got)
	}
	if got := vm.getStatic(1); got != 200 {
		t.Fatalf("child local 1 = %d want 200 (caller top stays word 1) [04 §4.3]", got)
	}
	if vm.Threads[1].Status != ThreadIdle {
		t.Fatalf("child should have completed")
	}
}

func TestTickDenominatorSleepArithmetic(t *testing.T) {
	// Sleep stores trunc(denom*ms/1000): 34ms → 1 tick, 33ms → 0 ticks with
	// the one-guard-decrement minimum [04 §4.6].
	mk := func(ms int32) *VM {
		code := []uint32{
			0x10021001, uint32(ms), // push duration [04 §4.3]
			0x10013000, // sleep [04 §4.6]
			0x10021001, 7,
			0x10023004, 0, // pop static 0 marker
			0x10065000,
		}
		prog, err := Load(makeCOBWithStatics(1, code, []string{"S"}, []uint32{0}, []string{"base"}))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return NewVM(prog)
	}
	vm34 := mk(34)
	if !vm34.Start(0, nil) {
		t.Fatal("start failed")
	}
	vm34.Drain(1)
	if got := vm34.Threads[0].Sleep; got != 1 {
		t.Fatalf("sleep 34ms stored %d want 1 tick [04 §4.6]", got)
	}
	vm33 := mk(33)
	if !vm33.Start(0, nil) {
		t.Fatal("start failed")
	}
	vm33.Drain(1)
	if got := vm33.Threads[0].Sleep; got != 0 {
		t.Fatalf("sleep 33ms stored %d want 0 ticks [04 §4.6]", got)
	}
	if vm33.getStatic(0) != 0 {
		t.Fatalf("sleep 33 must not complete in its own tick [04 §4.6]")
	}
	vm33.Drain(1)
	if vm33.getStatic(0) != 7 {
		t.Fatalf("sleep 33 must wake on the next drain's guard [04 §4.6]")
	}
}

func TestBlockedQueryLifecycle(t *testing.T) {
	// The synchronous four-cell query forces the slot's completion receiver
	// to none, writes the non-null input cells (null cells seeded literal
	// zero), forces the logical top to 3, runs ONE slot inline with delta 0,
	// and copies window words 0..3 back immediately. A thread that yields
	// stays allocated and may resume in a later normal drain, but it has no
	// receiver that can revise the values the host already copied [R-COB-01
	// §1][04 §4.2].
	//
	// Code layout: Caller (stale-receiver setup), QueryProbe (writes window
	// word 1, then sleeps), Sleeper (long sleep used to plant a stale pending
	// receiver before the query reuses its slot).
	queryProbe := []uint32{
		0x10021001, 55, 0x10023002, 1, // push 55; pop local 1 — script output cell [04 §4.3]
		0x10021001, 0, 0x10013000, // push 0; sleep — yield mid-query [04 §4.6]
		0x10021001, 99, 0x10023002, 0, // push 99; pop local 0 — post-resume write
		0x10065000, // return — must deliver to no receiver
	}
	sleeper := []uint32{
		0x10021001, 30000, 0x10013000, // sleep 900 ticks [04 §4.6]
		0x10065000,
	}
	code := append(append([]uint32{}, queryProbe...), sleeper...)
	prog, err := Load(makeCOBWithStatics(0, code, []string{"QueryProbe", "Sleeper"}, []uint32{0, uint32(len(queryProbe))}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bridge := NewCallbackBridge(NewVM(prog))
	vm := bridge.VM

	var received []CallbackReturn
	if r := bridge.Deferred("Sleeper", nil, func(cr CallbackReturn) { received = append(received, cr) }); !r.Started {
		t.Fatal("sleeper start failed")
	}
	// Engine-side kill without a drain. The direct kill stands in for the two
	// located producers of a thread termination that skips the receiver — the
	// signal opcode's mask scan and the invalid-opcode abnormal termination
	// [04 §4.3], [04 §5.3] — because both need a running interpreter to reach.
	// The pending receiver for slot 0 is now stale: neither producer invokes it
	// [04 §5.3], and collectReturns has not run to clear it.
	vm.killThread(0)
	if len(received) != 0 {
		t.Fatalf("killed sleeper must not invoke the receiver, got %+v [04 §5.3]", received)
	}

	res := bridge.Query("QueryProbe", [4]int32{7, 0, 0, 0})
	if !res.Started || res.Completed {
		t.Fatalf("blocked query = started %v completed %v, want started, not completed", res.Started, res.Completed)
	}
	slot := vm.lastQueryThread
	if slot < 0 || !vm.IsThreadAlive(slot) {
		t.Fatalf("yielded query thread %d must stay allocated [R-COB-01 §1]", slot)
	}
	if vm.Threads[slot].Status != ThreadSleeping || vm.Threads[slot].SP != 4 {
		t.Fatalf("query thread status %d SP %d want sleeping with top forced to 3 [R-COB-01 §1]", vm.Threads[slot].Status, vm.Threads[slot].SP)
	}
	// Immediate copy-back of the current window words.
	if res.Values[0] != 7 || res.Values[1] != 55 || res.Values[2] != 0 || res.Values[3] != 0 {
		t.Fatalf("copy-back = %v want [7 55 0 0] [R-COB-01 §1]", res.Values)
	}
	if vm.DrainCalls != 0 {
		t.Fatalf("a query runs no piece pass and no drain: DrainCalls %d [04 §4.2]", vm.DrainCalls)
	}

	// The resumed thread writes window word 0 and returns; the host's copied
	// values are never revised and no receiver fires.
	vm.Drain(1)
	if vm.IsThreadAlive(slot) {
		t.Fatal("resumed query thread should have completed")
	}
	if res.Values[0] != 7 || res.Values[1] != 55 {
		t.Fatalf("host values revised after copy-back: %v [R-COB-01 §1]", res.Values)
	}
	if len(received) != 0 {
		t.Fatalf("query receiver must be forced to none: %+v [R-COB-01 §1]", received)
	}
}

func TestDisableShadowEmptyAdapter(t *testing.T) {
	// The disable-shadow opcode binds an empty adapter on units — no effect,
	// no script-visible per-piece shadow state [R-COB-01 §1].
	code := []uint32{
		0x1000a000, 0, // dont-shadow piece 0 [04 §4.3] B
		0x10005000, 0, // show piece 0 — the pair still runs [04 §4.3]
		0x10065000,
	}
	prog, err := Load(makeCOB(code, []string{"Create"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vm := NewVM(prog)
	before := vm.SnapshotFlags()
	if !vm.StartByName("Create", nil) {
		t.Fatal("start failed")
	}
	vm.Drain(1)
	after := vm.SnapshotFlags()
	if len(after) != len(before) {
		t.Fatalf("flags length changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if after[i] != before[i]|0x01 { // show may set the draw bit; nothing else may move
			t.Fatalf("piece %d flags %d -> %d: dont-shadow must be a no-op [R-COB-01 §1]", i, before[i], after[i])
		}
	}
	if after[0]&0x01 == 0 {
		t.Fatalf("show did not set the draw bit [04 §4.3]")
	}
}
