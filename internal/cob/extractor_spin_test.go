package cob

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestExtractorSetSpeedSizesStockSpin locks the one relationship that decides
// whether a completed metal extractor visibly spins, and that regresses
// silently because every part of it still "works" when it is broken.
//
// The chain is Established on both ends:
//
//   - The creator, immediately after storing the sampled extraction rate and
//     only when the unit has a script VM, starts a deferred `SetSpeed` whose
//     single argument is the footprint metal accumulator — Σ(metal byte + 1)
//     over the stamped footprint — sign-extended from sixteen bits. "That is
//     what stock extractor scripts use to size their animation rate"
//     [05 R-PROD-01 §6][04 R-COB-04 §9].
//   - `spin` stores the target speed as trunc(speed/30) against the latched
//     tick denominator, so a raw speed below 30 yields a per-tick step of zero
//     and no motion at all [04 §4.6 "spin accel and stop-spin",
//     "zero-speed and zero-decel"].
//
// The stock `armmex` script joins the two: `Create` zeroes its speed variable,
// `SetSpeed` stores argument×45 into it, and `Go` (reached from `Activate`
// through the state-change script) spins the `arms` piece at that speed with a
// fixed acceleration. Skip the callback and every one of those steps still
// runs — the unit activates, the thread starts, the spin is marked live — and
// the piece stands perfectly still, because the speed the handler truncates is
// zero. That is exactly the failure this test exists to catch.
//
// The asserted angle is arithmetic from the two contracts above, not a
// recorded observation: with accumulator 404 the raw speed is 404×45 = 18180,
// the per-tick target is 18180/30 = 606, the script's acceleration constant is
// 91 so the per-tick acceleration is 91/30 = 3, and the ramp has not reached
// the target after thirty ticks. The angle advanced is therefore
// Σ(3k, k=1..30) = 1395 in the 65536-per-circle domain.
func TestExtractorSetSpeedSizesStockSpin(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()

	prog, ok, err := LoadFromFS(fs, "armmex")
	if err != nil || !ok || prog == nil {
		t.Skipf("armmex script unavailable: %v", err)
	}
	arms := -1
	for i, name := range prog.Pieces {
		if name == "arms" {
			arms = i
		}
	}
	if arms < 0 {
		t.Fatalf("armmex pieces %v carry no arms piece", prog.Pieces)
	}

	// Both runs are identical except for the creation-time callback.
	run := func(sum int32, notify bool) uint16 {
		vm := NewVM(prog)
		bridge := NewCallbackBridge(vm)
		bridge.Create()
		if notify {
			bridge.SetSpeedFootprint(sum)
			bridge.Drain(0)
		}
		bridge.Activate()
		bridge.Drain(0)
		before := vm.Pieces[arms].RotY
		for i := 0; i < 30; i++ {
			bridge.Drain(1)
		}
		return vm.Pieces[arms].RotY - before
	}

	// Without the callback the script's speed variable keeps the zero Create
	// wrote, the spin handler truncates it to a zero per-tick step, and nothing
	// moves [04 §4.6 "zero-speed and zero-decel"].
	if got := run(404, false); got != 0 {
		t.Fatalf("no SetSpeed: arms advanced %d, want 0", got)
	}
	// With it, the accumulator sizes the spin.
	if got := run(404, true); got != 1395 {
		t.Fatalf("SetSpeed(404): arms advanced %d, want 1395", got)
	}
}
