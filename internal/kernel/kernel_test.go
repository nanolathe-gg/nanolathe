package kernel

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
)

// TestPhaseOrderStable locks C7: phases run in numeric order every sub-tick,
// and within a phase callbacks run in registration call order [01 §4.4].
func TestPhaseOrderStable(t *testing.T) {
	var k Kernel
	var got []string
	k.Register(PhaseWindField, "b", func(uint32) { got = append(got, "wind-field") })
	k.Register(PhaseUnitsScripts, "a", func(uint32) { got = append(got, "units-1") })
	k.Register(PhaseUnitsScripts, "a2", func(uint32) { got = append(got, "units-2") })
	k.Register(PhaseCadenceFlip, "c", func(uint32) { got = append(got, "cadence") })

	var s clock.State
	k.SubTick(&s)

	want := []string{"units-1", "units-2", "wind-field", "cadence"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("phase order = %v, want %v", got, want)
	}
}

// TestTickIncrementsBeforePhaseOne locks C6: the global tick increments before
// any phase runs, so the first sub-tick of a fresh session is tick 1, not 0
// [01 §4.4].
func TestTickIncrementsBeforePhaseOne(t *testing.T) {
	var k Kernel
	var seen []uint32
	k.Register(PhaseNetwork, "probe", func(tick uint32) { seen = append(seen, tick) })

	var s clock.State
	k.Run(&s, 3)

	if fmt.Sprint(seen) != fmt.Sprint([]uint32{1, 2, 3}) {
		t.Fatalf("observed ticks %v, want [1 2 3]", seen)
	}
	if s.GlobalTick != 3 {
		t.Fatalf("clock GlobalTick = %d, want 3", s.GlobalTick)
	}
}

// TestSingleTickSurvivesSaveRoundTrip is the R4 regression. The tick the phases
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [01 §4.4], [08 "Scheduler and random state in saves"]. Two counters means a
// resumed game silently restarts its tick-modulo cadences.
func TestSingleTickSurvivesSaveRoundTrip(t *testing.T) {
	var k Kernel
	var last uint32
	k.Register(PhaseUnitsScripts, "probe", func(tick uint32) { last = tick })

	var s clock.State
	k.Run(&s, 5)
	k.Run(&s, 5)
	if last != 10 {
		t.Fatalf("phases observed tick %d, want 10", last)
	}

	box := s.SaveBox()

	var resumed clock.State
	resumed.LoadBox(box)
	if resumed.GlobalTick != 10 {
		t.Fatalf("restored GlobalTick = %d, want 10", resumed.GlobalTick)
	}
	k.SubTick(&resumed)
	if last != 11 {
		t.Fatalf("first tick after resume = %d, want 11", last)
	}
}

// TestRunClampsToBudget: the clock budget is already clamped 0..5 [01 §4.2];
// the kernel clamps defensively and drops excess rather than queueing it (C1).
func TestRunClampsToBudget(t *testing.T) {
	var k Kernel
	n := 0
	k.Register(PhaseNetwork, "count", func(uint32) { n++ })

	var s clock.State
	k.Run(&s, 9)
	k.Run(&s, -3)
	if n != 5 {
		t.Fatalf("ran %d sub-ticks, want 5", n)
	}
	if s.GlobalTick != 5 {
		t.Fatalf("GlobalTick = %d, want 5", s.GlobalTick)
	}
}
