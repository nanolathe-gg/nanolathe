package clock

import "testing"

// TestBudgetClampsAndDropsExcess locks C1: raw is truncated toward zero, the
// remainder is kept as carry, and the runnable count clamps to 0..5 with the
// excess dropped rather than queued [01 §4.2].
func TestBudgetClampsAndDropsExcess(t *testing.T) {
	s := State{Requested: 10, Active: 10} // nominal speed: activeSpeed * 0.1 == 1.0
	if got := s.AdvanceSP(6); got != 5 {
		t.Fatalf("delta 6 at nominal speed = %d ticks, want 5 (clamped)", got)
	}
	if s.Carry != 0 {
		t.Fatalf("carry = %v, want 0", s.Carry)
	}
	// Excess is dropped, not queued: the next sample starts from the new anchor.
	if got := s.AdvanceSP(7); got != 1 {
		t.Fatalf("next delta 1 = %d ticks, want 1 (backlog must not accumulate)", got)
	}
}

// TestBudgetCarrySurvivesBelowOneTick locks the fractional carry: sub-tick
// remainders accumulate across samples instead of being discarded [01 §4.2].
func TestBudgetCarrySurvivesBelowOneTick(t *testing.T) {
	s := State{Requested: 5, Active: 5} // 0.5 ticks per scaled unit; exact in binary
	got := []int{}
	for i := 1; i <= 4; i++ {
		got = append(got, s.AdvanceSP(int32(i)))
	}
	want := []int{0, 1, 0, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("half-speed tick sequence = %v, want %v", got, want)
		}
	}
}

// TestWrappedTickCountClampsToZero locks C2: a wrapped GetTickCount produces a
// negative delta, which clamps to zero rather than being repaired as elapsed
// time [01 §4.2].
func TestWrappedTickCountClampsToZero(t *testing.T) {
	s := State{Requested: 10, Active: 10, ScaledAnchor: 1000}
	if got := s.AdvanceSP(0); got != 0 {
		t.Fatalf("delta -1000 = %d ticks, want 0", got)
	}
	if s.ScaledAnchor != 0 {
		t.Fatalf("anchor = %d, want 0 (the wrapped sample still re-anchors)", s.ScaledAnchor)
	}
}

// TestPauseAsymmetry locks C4 and [GAP T12]. Single player short-circuits the
// budget, so the anchor stalls and unpause yields one capped burst. Multiplayer
// evaluates the budget while paused, discards the integer and keeps only the
// fractional remainder, so there is no burst.
func TestPauseAsymmetry(t *testing.T) {
	sp := State{Requested: 10, Active: 10, Paused: true}
	if got := sp.AdvanceSP(3000); got != 0 {
		t.Fatalf("paused SP ran %d ticks, want 0", got)
	}
	if sp.ScaledAnchor != 0 {
		t.Fatal("SP pause must stall the scaled-time anchor")
	}
	sp.Paused = false
	if got := sp.AdvanceSP(3000); got != 5 {
		t.Fatalf("SP unpause = %d ticks, want one capped burst of 5", got)
	}

	mp := State{Requested: 10, Active: 10, Paused: true}
	if got := mp.AdvanceMP(3000); got != 0 {
		t.Fatalf("paused MP ran %d ticks, want 0", got)
	}
	if mp.ScaledAnchor != 3000 {
		t.Fatalf("MP anchor = %d, want 3000 (budget runs while paused)", mp.ScaledAnchor)
	}
	mp.Paused = false
	if got := mp.AdvanceMP(3001); got != 1 {
		t.Fatalf("MP unpause = %d ticks, want 1 (no burst)", got)
	}
}

// TestSaveBoxRoundTrip locks C14: the scheduler block is exactly 28 bytes and
// restores every field it carries [08 "Scheduler and random state in saves"].
func TestSaveBoxRoundTrip(t *testing.T) {
	s := State{Requested: 7, Active: 4, GlobalTick: 4242}
	s.AdvanceSP(9)
	box := s.SaveBox()
	if len(box) != 28 {
		t.Fatalf("save box is %d bytes, want 28", len(box))
	}
	var restored State
	restored.LoadBox(box)
	if restored.GlobalTick != s.GlobalTick ||
		restored.ScaledAnchor != s.ScaledAnchor ||
		restored.Delta != s.Delta ||
		restored.Carry != s.Carry ||
		restored.Requested != s.Requested ||
		restored.Active != s.Active {
		t.Fatalf("round trip lost state:\n got %+v\nwant %+v", restored, s)
	}
}

// TestScaledNow locks the timebase conversion [01 §4.1].
func TestScaledNow(t *testing.T) {
	if got := ScaledNow(1000); got != 30 {
		t.Fatalf("ScaledNow(1000ms) = %d, want 30", got)
	}
	if got := ScaledNow(33); got != 0 {
		t.Fatalf("ScaledNow(33ms) = %d, want 0 (floor)", got)
	}
}
