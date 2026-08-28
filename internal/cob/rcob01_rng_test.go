package cob

// Contract tests for [R-COB-01 §2] — the VM random-draw census: exactly two
// dispatched opcodes consume draws, both from the simulation stream —
// `random` (0 or 1 draws; a bound below 2 consumes nothing and yields the low
// value unchanged) and `explode` (0 or 6 draws bounded 3000, 3000, 3000, 40,
// 10, 40 with the bound-10 draw dead; zero draws under bitmap-only). No other
// opcode may consume either stream, and the CRT stream is never touched from
// opcode execution.
//
// Fixtures are authored byte-for-byte by makeCOB; no retail bytes are used.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// newCensusVM authors a one-script VM with the given code and a fresh bound
// simulation stream. The test observes consumption through Simulation.Draws.
func newCensusVM(t *testing.T, statics int, code []uint32) (*VM, *rng.Simulation) {
	t.Helper()
	prog, err := Load(makeCOBWithStatics(uint32(statics), code, []string{"S"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load census fixture: %v", err)
	}
	vm := NewVM(prog)
	vm.SetSFXVisible(func(int, int32) bool { return true })
	sim := rng.NewSimulation(1)
	vm.SetSimulationRNG(&sim)
	if !vm.Start(0, nil) {
		t.Fatal("census script start failed")
	}
	return vm, &sim
}

func TestRandomDrawCensus(t *testing.T) {
	// random(x, x): bound below 2 consumes NO draw and the result is the low
	// value unchanged — the stream's bounded sampler returns zero without
	// advancing whenever the bound is below two [R-COB-01 §2][01 §7.1] I4.
	vm, sim := newCensusVM(t, 1, []uint32{
		0x10021001, 5, // push low [04 §4.3]
		0x10021001, 5, // push high
		0x10041000, // random [04 §4.3][R-COB-01 §2]
		0x10023004, 0,
		0x10065000,
	})
	vm.Drain(1)
	if got := vm.getStatic(0); got != 5 {
		t.Fatalf("random(5,5) = %d want 5 unchanged [R-COB-01 §2]", got)
	}
	if got := sim.Draws(); got != 0 {
		t.Fatalf("random(5,5) consumed %d draws want 0 [R-COB-01 §2]", got)
	}

	// A bound of high−low+1 ≥ 2 consumes exactly one draw.
	vm2, sim2 := newCensusVM(t, 1, []uint32{
		0x10021001, 100, // push low
		0x10021001, 159, // push high — bound 60
		0x10041000,
		0x10023004, 0,
		0x10065000,
	})
	vm2.Drain(1)
	if got := sim2.Draws(); got != 1 {
		t.Fatalf("random(100,159) consumed %d draws want 1 [R-COB-01 §2]", got)
	}
	if got := vm2.getStatic(0); got < 100 || got > 159 {
		t.Fatalf("random(100,159) = %d outside the inclusive range [04 §4.3]", got)
	}
}

func TestExplodeDrawCensus(t *testing.T) {
	// Physical branch: six draws in fixed order bounded 3000, 3000, 3000, 40,
	// 10, 40 — the fifth (bound 10) is dead, its stored result overwritten by
	// the sixth, and the dead draw is still made [04 §4.5][R-COB-01 §2].
	// Authored explode flags per [fmt cob] "Explosion type flags":
	// FALL|SMOKE|FIRE|EXPLODE_ON_HIT without BITMAPONLY.
	const physicalFlags = 0x1E
	vm, sim := newCensusVM(t, 0, []uint32{
		0x10021001, physicalFlags, // push flags [04 §4.3]
		0x10071000, 0, // explode piece 0 [04 §4.3] B
		0x10065000,
	})
	vm.Drain(1)
	if got := sim.Draws(); got != 6 {
		t.Fatalf("physical explode consumed %d draws want 6 [R-COB-01 §2]", got)
	}

	// Bitmap-only: zero draws from either stream [R-COB-01 §2]. BITMAPONLY is
	// flag 0x20 [fmt cob]; bitmap art selection bits change nothing.
	for _, flags := range []uint32{0x20, 0x20 | 0x100, 0x120} {
		vmB, simB := newCensusVM(t, 0, []uint32{
			0x10021001, flags,
			0x10071000, 0,
			0x10065000,
		})
		vmB.Drain(1)
		if got := simB.Draws(); got != 0 {
			t.Fatalf("bitmap-only explode flags %#x consumed %d draws want 0 [R-COB-01 §2]", flags, got)
		}
	}
}

func TestNoOtherOpcodeConsumesStreams(t *testing.T) {
	// A bounded sweep over every other dispatched family must consume zero
	// simulation draws; the CRT stream is never reachable from opcode
	// execution at all — the VM holds no CRT reference and no opcode path
	// touches one [R-COB-01 §2].
	sweep := []uint32{
		// Piece operations [04 §4.3].
		0x10021001, 640, 0x10021001, 1280, // speed, target
		0x10001000, 0, 0, // move piece 0 axis 0
		0x10021001, 65536, 0x10021001, 16384, // speed, angle
		0x10002000, 0, 0, // turn
		0x10021001, 5, 0x10021001, 300, // speed, acceleration
		0x10003000, 0, 0, // spin
		0x10021001, 5, // deceleration
		0x10004000, 0, 0, // stop-spin
		0x10005000, 0, // show
		0x10006000, 0, // hide
		0x10007000, 0, // cache
		0x10008000, 0, // dont-cache
		0x10021001, 1, 0x10021001, 2, // two values for the legacy effect
		0x10009000, 0, // legacy two-arg effect (empty unit stub)
		0x1000a000, 0, // dont-shadow (empty adapter)
		0x10021001, 44, // target
		0x1000b000, 0, 0, // move-now
		0x10021001, 8192, // angle
		0x1000c000, 0, 0, // turn-now
		0x1000d000, 0, // shade
		0x1000e000, 0, // dont-shade
		0x10021001, 0x101, // white smoke point type
		0x1000f000, 0, // emit-sfx (presentation sink wired)
		0x10011000, 0, 0, // wait-for-turn on an idle axis — wakes at once
		0x10012000, 0, 0, // wait-for-move — wakes at once
		0x10021001, 0, // duration 0
		0x10013000, // sleep — yields, resumes next drain
		// Stack, locals, statics [04 §4.3].
		0x10022000,                   // alloc-local
		0x10021001, 9, 0x10023002, 0, // push 9; pop local 0
		0x10021002, 0, 0x10023004, 0, // push local; pop static
		0x10021003, 123, // push unknown mode — uninitialized scratch
		0x10023003, 0, // pop unknown mode — pops nothing
		0x10024000, // discard
		// Arithmetic, logic, comparisons [04 §4.3].
		0x10021001, 3, 0x10021001, 4,
		0x10031000,                // add
		0x10021001, 3, 0x10032000, // subtract
		0x10021001, 5, 0x10033000, // multiply
		0x10021001, 2, 0x10034000, // divide 10/2 — the guarded fault path is P2-03's
		0x10021001, 0x0f, 0x10035000, // and
		0x10021001, 0x30, 0x10036000, // or
		0x10021001, 0x55, 0x10037000, // xor
		0x10038000, // not
		0x10051000, 0x10052000, 0x10053000, 0x10054000,
		0x10055000, 0x10056000, 0x10057000, 0x10058000, 0x10059000,
		0x1005a000, // logical not
		// Engine reads and writes [04 §4.4] — no arm consumes draws.
		0x10021001, 4, 0x10042000, // one-argument read (health)
		0x10021001, 14, 0x10021001, 3, 0x10021001, 4, 0x10021001, 0, 0x10021001, 0,
		0x10043000,                 // five-argument read
		0x10021001, 16, 0x10044000, // single-argument-port read
		0x10045000,                                // no-argument read
		0x10021001, 19, 0x10021001, 1, 0x10082000, // engine write (bugger off)
		0x10021001, 1, 0x10021001, 0, 0x10083000, // attach
		0x10021001, 1, 0x10084000, // detach
		// Control flow, signals, script starts [04 §4.3]. Ids 1/2 are the
		// start/call children authored below.
		0x10061000, 1, 0, // start-script id 1 argc 0
		0x10062000, 2, 0, // call-script id 2 argc 0 — child runs and unblocks
		0x10021001, 2, 0x10021001, 3, 0x10063000, 0, 2, // reserved pop-N (id, count)
		0x10064000, 0, // jump — target patched below
		0x10021001, 0, 0x10066000, 0, // jump-if-false with a patched target
		0x10021001, 0, 0x10068000, // set-signal-mask 0 — self survives the signal
		0x10021001, 0x80, 0x10067000, // signal — matches no live thread
		0x10021001, 1, 0x10023004, 0, // marker
		0x10065000, // return
	}
	// The two unconditional jumps must not loop: retarget both to the
	// set-signal-mask word just before the epilogue (the drain has no
	// iteration cap — a tight loop wedges, C14).
	setmaskIdx := -1
	for i, w := range sweep {
		if w == 0x10068000 {
			setmaskIdx = i
			break
		}
	}
	if setmaskIdx < 0 {
		t.Fatal("sweep missing set-signal-mask")
	}
	patchTarget := func(op uint32) {
		for i, w := range sweep {
			if w == op {
				sweep[i+1] = uint32(setmaskIdx)
				return
			}
		}
		t.Fatalf("sweep missing opcode %#x", op)
	}
	patchTarget(0x10064000) // jump
	patchTarget(0x10066000) // jump-if-false
	// Children: id 1 (start-script) and id 2 (call-script) each return at
	// once; the call child's return unblocks the caller on the next scan.
	children := []uint32{0x10065000, 0x10065000}
	prog, err := Load(makeCOBWithStatics(1, append(append([]uint32{}, sweep...), children...),
		[]string{"S", "StartChild", "CallChild"},
		[]uint32{0, uint32(len(sweep)), uint32(len(sweep) + 1)}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load sweep fixture: %v", err)
	}
	vm := NewVM(prog)
	vm.SetSFXVisible(func(int, int32) bool { return true })
	sim := rng.NewSimulation(1)
	vm.SetSimulationRNG(&sim)
	if !vm.Start(0, nil) {
		t.Fatal("census script start failed")
	}
	// Each wait-for-turn/wait-for-move/sleep yields its drain visit even when
	// the polled word already reads zero, and a blocked caller resumes on the
	// scan after its callee returns — one-tick latency per yield
	// [04 §4.2][04 §4.6]. Four yields need five drains.
	for i := 0; i < 5 && vm.Threads[0].Status != ThreadIdle; i++ {
		vm.Drain(1)
	}
	if vm.Threads[0].Status != ThreadIdle {
		t.Fatalf("sweep script did not complete: status %d", vm.Threads[0].Status)
	}
	if got := sim.Draws(); got != 0 {
		t.Fatalf("non-random opcode sweep consumed %d simulation draws want 0 [R-COB-01 §2]", got)
	}
	if vm.getStatic(0) != 1 {
		t.Fatalf("sweep did not reach its marker: static %d", vm.getStatic(0))
	}
}
