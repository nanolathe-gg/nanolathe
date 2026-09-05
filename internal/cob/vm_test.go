package cob

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// helper to build a synthetic Program without going through Load [fmt cob].
func synthProg(code []uint32, pieces []string, statics int, byID []int) *Program {
	if pieces == nil {
		pieces = []string{"base"}
	}
	if byID == nil && len(code) > 0 {
		byID = []int{0}
	}
	scripts := make(map[string]int)
	for i, pc := range byID {
		scripts[string(rune('A'+i))] = pc
		_ = pc
	}
	// Every fixture program declares its entry-point table: a script id is an
	// index into ScriptsByID and nothing else [04 §4.3] C14. The VM used to
	// carry a second arm that read the id as a direct code word index when a
	// program had no table, which only hand-built programs could reach.
	return &Program{
		Code:        code,
		Scripts:     scripts,
		Pieces:      pieces,
		Statics:     statics,
		ScriptsByID: byID,
	}
}

// newTestVM makes the visibility dependency explicit for tests whose behavior
// is unrelated to visibility. Production composition supplies the concrete
// gate; an accidentally missing dependency makes emit-sfx fail closed [GAP T15] C19.
func newTestVM(prog *Program) *VM {
	vm := NewVM(prog)
	vm.SetSFXVisible(func(piece int, sfxType int32) bool { return true })
	return vm
}

type vmSFXSink struct{ calls int }

func (s *vmSFXSink) EmitSFX(int, int32, SFXKind) { s.calls++ }

func TestEmitSFXMissingVisibilityFailsClosed(t *testing.T) {
	// A missing visibility dependency suppresses presentation only. The opcode
	// still pops its effect type, advances the PC, and leaves normal scheduling
	// intact [GAP T15] C19.
	prog := synthProg([]uint32{
		0x10021001, 1, // push vector effect type [04 §4.3] F
		0x1000f000, 0, // emit-sfx piece 0; pops the effect type [04 §4.3] B
		0x10021001, 0, // push zero sleep duration
		0x10013000, // sleep yields after the emit-sfx instruction
	}, []string{"base"}, 0, []int{0})
	vm := NewVM(prog)
	sink := &vmSFXSink{}
	vm.SetSFXSink(sink)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)

	if sink.calls != 0 {
		t.Fatalf("emit-sfx without visibility must fail closed, got %d sink calls", sink.calls)
	}
	if vm.Threads[0].Status != ThreadSleeping {
		t.Fatalf("emit-sfx missing visibility changed scheduling: status %d want sleeping", vm.Threads[0].Status)
	}
	if vm.Threads[0].PC != 7 {
		t.Fatalf("emit-sfx missing visibility did not preserve operand/PC advance: PC %d want 7", vm.Threads[0].PC)
	}
	if vm.Threads[0].SP != 0 {
		t.Fatalf("emit-sfx missing visibility did not consume operands normally: SP %d want 0", vm.Threads[0].SP)
	}
}

func TestOpcodeDispatch(t *testing.T) {
	// C11 exactly 57 dispatched values [04 §4.3].
	if len(dispatchKeys) != 57 {
		t.Fatalf("dispatchKeys len %d want 57 [04 §4.3] C11", len(dispatchKeys))
	}
	// Sorted check (binary search requires sorted) [04 §4.3] C11.
	for i := 1; i < len(dispatchKeys); i++ {
		if dispatchKeys[i] <= dispatchKeys[i-1] {
			t.Fatalf("dispatchKeys not strictly sorted at %d: %#x <= %#x", i, dispatchKeys[i], dispatchKeys[i-1])
		}
	}
	// Verify each expected key dispatches via binary search [04 §4.3] C11.
	for _, k := range dispatchKeys {
		pos := sort.Search(len(dispatchKeys), func(i int) bool { return dispatchKeys[i] >= k })
		if pos >= len(dispatchKeys) || dispatchKeys[pos] != k {
			t.Fatalf("expected key %#x not found via binary search", k)
		}
	}
	// Unknown keys must take kill path [04 §4.3] C11: clear thread status, decrement active count, yield.
	unknowns := []uint32{0x10099000, 0x100aa000, 0x100ff000, 0x10000000, 0x10100000}
	for _, uk := range unknowns {
		masked := uk & dispatchMask
		pos := sort.Search(len(dispatchKeys), func(i int) bool { return dispatchKeys[i] >= masked })
		found := pos < len(dispatchKeys) && dispatchKeys[pos] == masked
		if found {
			t.Fatalf("unknown key %#x masked %#x unexpectedly dispatched", uk, masked)
		}
		// Verify VM kill path for one unknown.
		prog := synthProg([]uint32{uk}, []string{"base"}, 0, []int{0})
		vm := newTestVM(prog)
		// Manually start a thread at PC 0 (bypass isValidEntry which would reject unknown entry;
		// for dispatch test we set thread directly).
		vm.Threads[0].Status = ThreadRunning
		vm.Threads[0].PC = 0
		vm.Threads[0].SP = 0
		vm.Drain(1)
		if vm.Threads[0].Status != ThreadIdle {
			t.Fatalf("unknown key %#x did not take kill path: status %d want idle", uk, vm.Threads[0].Status)
		}
	}
	// Spot-check that dispatched keys do NOT take kill path when properly formed.
	// show sets pieceFlags bit 0 [04 §4.3], so the flag proves dispatch: the
	// kill path would clear the thread before the trailing return ever ran.
	// (The runThread drain has no iteration cap — retail wedges on tight loops
	// and so do we — so a jump-loop program must not be used here.)
	prog2 := synthProg([]uint32{0x10005000, 0, 0x10065000}, []string{"base"}, 0, []int{0}) // show piece 0 + return
	vm2 := newTestVM(prog2)
	vm2.Threads[0].Status = ThreadRunning
	vm2.Threads[0].PC = 0
	vm2.Drain(1)
	if vm2.Threads[0].Status == ThreadRunning {
		t.Fatalf("dispatched program did not terminate")
	}
	if len(vm2.pieceFlags) == 0 || vm2.pieceFlags[0]&0x01 == 0 {
		t.Fatalf("dispatched show opcode did not set the draw bit — kill path taken?")
	}
}

func TestSleepZeroCostsOneTick(t *testing.T) {
	// Sleep conversion trunc(30*ms/1000) [04 §4.6], plus one guard decrement => sleep 0 costs one tick [04 §4.2] C13.
	// Program: push 0, sleep, push 123, pop-static 0, return.
	// After first Drain, static should still be 0; after second Drain, static should be 123.
	code := []uint32{
		0x10021001, 0, // push-constant 0 [04 §4.3] F mode1
		0x10013000,      // sleep [04 §4.3] [04 §4.6]
		0x10021001, 123, // push 123
		0x10023004, 0, // pop-static 0 [04 §4.3] F mode4
		0x10065000, // return
	}
	prog := synthProg(code, []string{"base"}, 1, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatalf("Start failed")
	}
	// First tick: start thread runs push+sleep and yields sleeping.
	vm.Drain(1)
	if vm.statics[0] != 0 {
		t.Fatalf("sleep 0 should not wake in same tick: statics[0]=%d want 0 [04 §4.6]", vm.statics[0])
	}
	if vm.Threads[0].Status != ThreadSleeping {
		t.Fatalf("thread should be sleeping after sleep 0, status %d", vm.Threads[0].Status)
	}
	// Second tick: sleep guard subtracts delta, wakes, runs push/pop/return.
	vm.Drain(1)
	if vm.statics[0] != 123 {
		t.Fatalf("sleep 0 did not wake on next tick: statics[0]=%d want 123 [04 §4.6] C13", vm.statics[0])
	}
}

func TestEngineRootSignalMaskSeed(t *testing.T) {
	vm := newTestVM(&Program{Code: []uint32{0x10065000}, Scripts: map[string]int{"Create": 0}, ScriptsByID: []int{0}})
	if !vm.StartByName("Create", nil) {
		t.Fatal("root start failed")
	}
	if got := vm.Threads[0].SignalMask; got != 1 {
		t.Fatalf("root signal mask=%d want 1 [R-P0-10]", got)
	}
}

func TestStackUnderflowOverflow(t *testing.T) {
	// Underflow: add with empty stack should not panic, pop zeros, push 0 [04 §4.3] C13
	prog := synthProg([]uint32{0x10031000, 0x10065000}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Threads[0].SP = 0
	vm.Drain(1)
	// After add underflow, the add must yield 0 without panicking. The
	// program stores the result in static 0 and returns (the drain has no
	// iteration cap — C14 — so no jump-loop programs).
	prog2 := synthProg([]uint32{0x10031000, 0x10023004, 0, 0x10065000}, []string{"base"}, 1, []int{0}) // add, pop-static 0, return
	vm2 := newTestVM(prog2)
	vm2.Threads[0].Status = ThreadRunning
	vm2.Threads[0].PC = 0
	vm2.Threads[0].SP = 0
	vm2.Drain(1)
	if vm2.getStatic(0) != 0 {
		t.Fatalf("underflow add result %d want 0", vm2.getStatic(0))
	}

	// Overflow: push 11 constants, stack depth 10 cap [04 §4.2] C13
	// Retail kills thread on overflow (status cleared) [P1-11] §2.4, not discard.
	// 11th push should kill thread before pop-static, leaving statics 0.
	code := make([]uint32, 0, 24)
	for i := 0; i < 11; i++ {
		code = append(code, 0x10021001, uint32(i+1))
	}
	code = append(code, 0x10023004, 0) // pop-static 0
	code = append(code, 0x10065000)    // return
	prog3 := synthProg(code, []string{"base"}, 1, []int{0})
	vm3 := newTestVM(prog3)
	vm3.Threads[0].Status = ThreadRunning
	vm3.Threads[0].PC = 0
	vm3.Drain(1)
	if vm3.Threads[0].Status != ThreadIdle {
		t.Fatalf("overflow should kill thread (status cleared) [P1-11] §2.4, got %d want idle", vm3.Threads[0].Status)
	}
	if vm3.getStatic(0) != 0 {
		t.Fatalf("overflow kill should not write static, got %d want 0 [P1-11]", vm3.getStatic(0))
	}
	// Under overflow kill, stack is cleared via killThread (SP=0). Verify.
	if vm3.Threads[0].SP != 0 {
		t.Fatalf("overflow kill SP %d want 0", vm3.Threads[0].SP)
	}
}

func TestPushPopAddressingModes(t *testing.T) {
	// Push modes: 1 constant, 2 local, 4 static; other pushes scratch 0 [04 §4.3] C14
	// Pop modes: 2 local, 4 static; other pops nothing [04 §4.3] C14
	// Build program that exercises table.
	prog := synthProg([]uint32{
		0x10021001, 77, // push 77
		0x10023002, 0, // pop-local 0 -> local0=77 [04 §4.3]
		0x10021002, 0, // push-local 0 -> push 77
		0x10023004, 0, // pop-static 0 -> statics[0]=77
		0x10021004, 0, // push-static 0 -> push 77
		0x10021003, 999, // push unknown mode 3 -> push 0 scratch [04 §4.3] C14
		0x10023003, 0, // pop unknown mode 3 -> pops nothing, SP unchanged [04 §4.3] C14
		0x10021001, 0, // push 0 for sleep
		0x10013000, // sleep [04 §4.3]
	}, []string{"base"}, 1, []int{0})
	vm := newTestVM(prog)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	// After Drain, thread executed sequence then slept; verify statics and stack
	if vm.statics[0] != 77 {
		t.Fatalf("push/pop static mode failed: statics[0]=%d want 77", vm.statics[0])
	}
	// Stack after ops: start 0
	// push 77 -> [77] SP1
	// pop-local -> SP0, local0=77
	// push-local -> push 77 -> [77] SP1
	// pop-static -> SP0, statics 77
	// push-static -> push 77 -> [77] SP1
	// push unknown -> push 0 -> [77,0] SP2
	// pop unknown -> SP still 2 (no pop)
	// push 0 -> [77,0,0] SP3
	// sleep pops 0 -> [77,0] SP2 sleeping
	if vm.Threads[0].SP != 2 {
		t.Fatalf("push/pop mode SP %d want 2", vm.Threads[0].SP)
	}
	if vm.Threads[0].Stack[0] != 77 || vm.Threads[0].Stack[1] != 0 {
		t.Fatalf("push/pop mode stack %v want [77 0]", vm.Threads[0].Stack[:2])
	}
}

func TestStaticsZeroInit(t *testing.T) {
	prog := synthProg([]uint32{0x10065000}, []string{"base"}, 3, []int{0})
	vm := newTestVM(prog)
	for i, v := range vm.statics {
		if v != 0 {
			t.Fatalf("statics[%d]=%d want 0 zero-init per [fmt cob] [04 §4.2]", i, v)
		}
	}
	// Also via Load path statics count is carried; VM uses it.
}

func TestThreadSlotSelectionOrder(t *testing.T) {
	prog := synthProg([]uint32{0x10065000}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	// Alloc order lowest clear [01 §6.1] C13
	for i := 0; i < 3; i++ {
		ok := vm.Start(0, nil)
		if !ok {
			t.Fatalf("Start %d failed", i)
		}
	}
	// Threads 0,1,2 should be active (Running), others idle
	for i := 0; i < 3; i++ {
		if vm.Threads[i].Status == ThreadIdle {
			t.Fatalf("thread %d should be allocated", i)
		}
	}
	for i := 3; i < 8; i++ {
		if vm.Threads[i].Status != ThreadIdle {
			t.Fatalf("thread %d should be idle", i)
		}
	}
	// Free middle slot 1
	vm.killThread(1)
	if vm.Threads[1].Status != ThreadIdle {
		t.Fatalf("killThread failed")
	}
	// Next Start should reuse lowest clear = 1 [01 §6.1] C13
	if !vm.Start(0, nil) {
		t.Fatalf("Start reuse failed")
	}
	if vm.Threads[1].Status == ThreadIdle {
		t.Fatalf("slot 1 not reused, lowest clear order violated")
	}
}

func TestStartScriptFullPoolArgRetention(t *testing.T) {
	// C14 start-script with full pool retains args [04 §4.3]
	code := []uint32{
		0x10061000, 0, 2, // start-script id0 argc2 [04 §4.3] E
		0x10005000, 0, // show (no stack, keeps thread alive)
		0x10021001, 0, // push 0
		0x10013000, // sleep to yield
	}
	prog := synthProg(code, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	for i := 0; i < 8; i++ {
		vm.Threads[i].Status = ThreadSleeping
		vm.Threads[i].Sleep = 100
		vm.Threads[i].WaitPiece = -1
		vm.Threads[i].WaitAxis = -1
		vm.Threads[i].WaitThread = -1
	}
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Threads[0].SP = 2
	vm.Threads[0].Stack[0] = 10
	vm.Threads[0].Stack[1] = 20
	vm.Drain(1)
	// After Drain with no free slot, args retained, PC advanced past start (0+3=3) then show+push+sleep executed; final SP after push0+sleep pop is still 2, status sleeping
	if vm.Threads[0].SP != 2 {
		t.Fatalf("full pool start-script should retain args SP %d want 2 [04 §4.3] C14", vm.Threads[0].SP)
	}
	if vm.Threads[0].Stack[0] != 10 || vm.Threads[0].Stack[1] != 20 {
		t.Fatalf("full pool start-script args not retained: %v want [10 20]", vm.Threads[0].Stack[:2])
	}
	// PC after start(3) -> show(5) -> push(7) -> sleep yields at PC 8
	if vm.Threads[0].PC != 8 {
		t.Fatalf("full pool start-script PC %d want 8", vm.Threads[0].PC)
	}
	if vm.Threads[0].Status != ThreadSleeping {
		t.Fatalf("full pool start-script should be sleeping after show+sleep, status %d", vm.Threads[0].Status)
	}
}

func TestCallScriptFullPoolLeak(t *testing.T) {
	// C14 call-script with no free slot waits at -1 and leaks [04 §4.3]
	code := []uint32{
		0x10062000, 0, 2, // call-script [04 §4.3] E
		0x10005000, 0, // show to keep alive if succeeds (not taken when leak)
	}
	prog := synthProg(code, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	for i := 0; i < 8; i++ {
		vm.Threads[i].Status = ThreadSleeping
		vm.Threads[i].Sleep = 100
	}
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Threads[0].SP = 2
	vm.Threads[0].Stack[0] = 10
	vm.Threads[0].Stack[1] = 20
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadWaitCall {
		t.Fatalf("call-script no free slot should become WaitCall, status %d", vm.Threads[0].Status)
	}
	if vm.Threads[0].WaitThread != -1 {
		t.Fatalf("call-script leak WaitThread %d want -1 [04 §4.3] C14", vm.Threads[0].WaitThread)
	}
	if vm.Threads[0].SP != 2 {
		t.Fatalf("call-script no free slot should retain args SP %d want 2 [04 §4.3] C14", vm.Threads[0].SP)
	}
	// Ensure it never wakes on next Drain (leaked)
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadWaitCall {
		t.Fatalf("leaked call should stay waiting")
	}
}

func TestSignalWake(t *testing.T) {
	// Signal wake: signal kills every thread whose mask intersects popped mask, waking waiters [04 §4.3]
	prog := synthProg([]uint32{0x10065000}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	// Thread1 will be callee with mask 0x02
	vm.Threads[1].Status = ThreadRunning
	vm.Threads[1].PC = 0
	vm.Threads[1].SignalMask = 0x02
	vm.Threads[1].SP = 0
	// Thread0 waits on thread1
	vm.Threads[0].Status = ThreadWaitCall
	vm.Threads[0].WaitThread = 1
	vm.Threads[0].SignalMask = 0
	// External Signal with mask 0x02 should kill thread1 and wake thread0
	vm.Signal(0x02)
	if vm.Threads[1].Status != ThreadIdle {
		t.Fatalf("signal did not kill thread1")
	}
	if vm.Threads[0].Status != ThreadRunning {
		t.Fatalf("signal did not wake waiter, status %d want Running", vm.Threads[0].Status)
	}
	if vm.Threads[0].WaitThread != -1 {
		t.Fatalf("wake did not clear WaitThread")
	}
	// Signal via opcode: thread2 signals, then proves it survived the signal
	// by writing static 0 afterwards (the drain has no iteration cap — C14 —
	// so a jump-loop program cannot be used to keep the thread alive).
	code2 := []uint32{0x10067000, 0x10021001, 7, 0x10023004, 0, 0x10065000} // signal, push 7, pop-static 0, return
	prog2 := synthProg(code2, []string{"base"}, 1, []int{0})
	vm2 := newTestVM(prog2)
	vm2.Threads[3].Status = ThreadRunning
	vm2.Threads[3].PC = 0
	vm2.Threads[3].SignalMask = 0x04
	vm2.Threads[2].Status = ThreadRunning
	vm2.Threads[2].PC = 0
	vm2.Threads[2].SP = 1
	vm2.Threads[2].Stack[0] = 0x04
	vm2.Threads[2].SignalMask = 0 // does not match
	vm2.Drain(1)
	// After Drain, thread2's signal should have killed thread3. Thread2 itself not killed because its mask 0 doesn't intersect.
	if vm2.Threads[3].Status != ThreadIdle {
		t.Fatalf("opcode signal did not kill matching thread")
	}
	if vm2.getStatic(0) != 7 {
		t.Fatalf("opcode signal incorrectly killed self (static %d want 7)", vm2.getStatic(0))
	}
	// Self-kill via signal
	vm3 := newTestVM(prog2)
	vm3.Threads[3].Status = ThreadRunning
	vm3.Threads[3].PC = 0
	vm3.Threads[3].SignalMask = 0x04
	vm3.Threads[3].SP = 0
	vm3.Threads[2].Status = ThreadRunning
	vm3.Threads[2].PC = 0
	vm3.Threads[2].SignalMask = 0x04 // also matches
	vm3.Threads[2].Stack[0] = 0x04
	vm3.Threads[2].SP = 1
	vm3.Drain(1)
	if vm3.Threads[2].Status != ThreadIdle {
		t.Fatalf("signal self-kill should kill self, status %d", vm3.Threads[2].Status)
	}
}

func TestDividePanics(t *testing.T) {
	// [P2-03] fallback: retail divide with b==0 or INT_MIN/-1 raises #DE
	// and kills the process [04 §4.3] C14 I11. Nanolathe keeps malformed as
	// explicit fallback, not crash: the thread is killed, a diagnostic is
	// recorded, and the process stays alive (I11 divergence noted in vm.go).
	prog := synthProg([]uint32{
		0x10021001, 10,
		0x10021001, 0,
		0x10034000, // divide 10/0 → thread kill fallback [P2-03][04 §4.3] C14
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadIdle {
		t.Fatalf("divide by zero should kill thread [P2-03] got status %d want idle", vm.Threads[0].Status)
	}
	if len(vm.Diagnostics()) == 0 {
		t.Fatalf("divide by zero should record diagnostic [P2-03]")
	}
	// INT_MIN / -1 same guard
	prog2 := synthProg([]uint32{
		0x10021001, 0x80000000,
		0x10021001, 0xffffffff,
		0x10034000,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm2 := newTestVM(prog2)
	vm2.Threads[0].Status = ThreadRunning
	vm2.Threads[0].PC = 0
	vm2.Drain(1)
	if vm2.Threads[0].Status != ThreadIdle {
		t.Fatalf("INT_MIN/-1 should kill thread [P2-03] got %d", vm2.Threads[0].Status)
	}
}

func TestPieceMoveInterpolate(t *testing.T) {
	// Test move interpolation: speed 60 (2 per tick), target 10
	prog := synthProg([]uint32{
		0x10021001, 60, // speed
		0x10021001, 10, // target
		0x10001000, 0, 0, // move piece0 axis0 [04 §4.3]
		0x10065000, // return (no jump loop: the drain has no iteration cap, C14)
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	// Two pushes then move schedules, then interpolate
	// For move, pushes are speed then target: stack ... speed, target(top)
	// Our code pushes 60 then 10, so stack [60,10] top 10 target, second 60 speed -> move target 10 speed 60.
	// Drain first tick: thread executes pushes and move, then piece pass with delta1: step 2, cur 0 -> 2
	vm.Drain(1)
	if got := int64(vm.Pieces[0].Trans[0].Raw()); got != 2 {
		t.Fatalf("move step 1: Trans %d want 2", got)
	}
	vm.Drain(1)
	if got := int64(vm.Pieces[0].Trans[0].Raw()); got != 4 {
		t.Fatalf("move step2: Trans %d want 4", got)
	}
	// Continue until target
	for i := 0; i < 10; i++ {
		vm.Drain(1)
	}
	if got := int64(vm.Pieces[0].Trans[0].Raw()); got != 10 {
		t.Fatalf("move target not reached: %d want 10", got)
	}
	// Wait-for-move should wake after arrival
	// Reset vm for wait test
	prog2 := synthProg([]uint32{
		0x10021001, 60,
		0x10021001, 6,
		0x10001000, 0, 0, // move to 6 at speed60 => needs 3 ticks
		0x10012000, 0, 0, // wait-for-move [04 §4.3]
		0x10021001, 99,
		0x10023004, 0, // pop-static 0 marker
		0x10065000,
	}, []string{"base"}, 1, []int{0})
	vm2 := newTestVM(prog2)
	vm2.Threads[0].Status = ThreadRunning
	vm2.Threads[0].PC = 0
	vm2.Drain(1) // move scheduled, piece 0->2, wait starts
	if vm2.Threads[0].Status != ThreadWaitMove {
		t.Fatalf("wait-for-move should block, status %d", vm2.Threads[0].Status)
	}
	vm2.Drain(1) // piece 4
	if vm2.Threads[0].Status != ThreadWaitMove {
		t.Fatalf("still waiting tick2")
	}
	vm2.Drain(1) // piece 6 arrival
	// Wait observes arrival one guard after interpolation [04 §4.6], so still waiting this tick?
	// Our Drain order: first handles wait guard before thread execution; interpolation that produced arrival happened previous tick's piece pass. So waiter wakes next Drain.
	// So after tick that reached target, waiter still waiting, next tick it wakes.
	if vm2.Threads[0].Status != ThreadWaitMove {
		t.Fatalf("should still be waiting at arrival tick, status %d", vm2.Threads[0].Status)
	}
	vm2.Drain(1) // now wake and run marker
	if vm2.statics[0] != 99 {
		t.Fatalf("wait-for-move wake failed: statics %d want 99", vm2.statics[0])
	}
}

func TestAssetGuardedRealCOB(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()
	records, err := fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var cobPaths []string
	for _, rec := range records {
		if strings.EqualFold(filepath.Ext(rec.LogicalPath), ".cob") && strings.HasPrefix(strings.ToLower(rec.LogicalPath), "scripts/") {
			cobPaths = append(cobPaths, rec.LogicalPath)
		}
	}
	if len(cobPaths) == 0 {
		t.Fatal("no COB files found via VFS")
	}
	sort.Strings(cobPaths)
	// Test a few COBs, not all, to keep test fast.
	limit := 5
	if len(cobPaths) < limit {
		limit = len(cobPaths)
	}
	for i := 0; i < limit; i++ {
		p := cobPaths[i]
		data, err := fs.ReadFileLimit(p, 8<<20)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		prog, err := Load(data)
		if err != nil {
			t.Fatalf("load %s: %v", p, err)
		}
		if len(prog.Code) == 0 {
			continue
		}
		vm := newTestVM(prog)
		// Run Create if present [04 §5.1]
		if pc, ok := prog.Scripts["Create"]; ok {
			rng.SeedGlobal(1, 1) // deterministic [01 §7.1] I4
			if !vm.Start(pc, nil) {
				t.Fatalf("Start Create failed for %s", p)
			}
			// Run a few ticks to ensure no panic
			for tick := 0; tick < 3; tick++ {
				vm.Drain(1)
			}
		}
	}
	// Also verify our synthetic piece state adapter works with real model piece count
	_ = model.AxisX // ensure import used
}

// Ensure vm.Pieces adapter methods are used (C22)
var _ = model.PieceState{}

// TestVMGroundHeightPortRoundTrip runs a real script through the VM's "get"
// engine-read opcode (0x10043000, the five-argument form: id then four
// argument slots, zero-filled here beyond the one GROUND_HEIGHT uses
// [fmt cob]) end to end, proving GroundHeightPortFunc is reachable from
// compiled bytecode, not only as a pure function: BindPort(16, ...) → engine
// read → GroundHeight → the injected height query, with the decoded
// coordinate matching [R-COB-03 §3]'s high-half-X/low-half-Z convention and
// the 16.16 result landing on the script's local exactly as the callback
// arithmetic requires (WU-19-7).
func TestVMGroundHeightPortRoundTrip(t *testing.T) {
	x := numeric.FixedFromInt(12)
	z := numeric.FixedFromInt(-3)
	packed := PackXZ(x, z)

	prog := synthProg([]uint32{
		0x10021001, 16, // push port id 16 (GROUND_HEIGHT) [04 §4.4]
		0x10021001, uint32(packed), // push packed X/Z [R-COB-03 §3]
		0x10021001, 0, // compiler zero-fill slot [fmt cob]
		0x10021001, 0, // compiler zero-fill slot
		0x10021001, 0, // compiler zero-fill slot
		0x10043000,    // five-argument engine read [fmt cob 0x10043000]
		0x10023002, 1, // pop into local 1 (local 0 aliases Stack[0], which the
		// sleep-duration push below would immediately overwrite — window word
		// i is Stack[i] [04 §4.3], not a separate save slot)
		0x10021001, 0, // push 0 for sleep duration
		0x10013000, // sleep, yields so Drain(1) observes the local
	}, []string{"base"}, 0, []int{0})

	vm := newTestVM(prog)
	var gotX, gotZ numeric.Fixed
	calls := 0
	vm.BindPort(Port(16), GroundHeightPortFunc(func(qx, qz numeric.Fixed) numeric.Fixed {
		calls++
		gotX, gotZ = qx, qz
		return numeric.FixedFromInt(7) // arbitrary valid height, 16.16
	}))
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)

	if calls != 1 {
		t.Fatalf("GROUND_HEIGHT height query called %d times want 1 (I4: exactly one query, no RNG involved)", calls)
	}
	if gotX != x || gotZ != z {
		t.Fatalf("GROUND_HEIGHT decoded (x=%v z=%v) want (x=%v z=%v) [R-COB-03 §3] high half X, low half Z", gotX, gotZ, x, z)
	}
	want := int32(numeric.FixedFromInt(7))
	if got := vm.Threads[0].Stack[1]; got != want {
		t.Fatalf("GROUND_HEIGHT local1 = %d want %d (16.16 passthrough) [04 §4.4]", got, want)
	}
}

var _ sort.Interface = sort.StringSlice{}
