package clock

import (
	"encoding/binary"
	"math"
	"testing"
)

// TestBudgetClampsAndDropsExcess locks C1: raw is floored before narrowing, the
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

func TestBudgetFloorsBeforeNarrowing(t *testing.T) {
	tests := []struct {
		name      string
		anchor    int32
		now       int32
		carry     float32
		wantTicks int
		wantCarry float32
	}{
		{name: "positive fraction", anchor: 0, now: 1, wantTicks: 0, wantCarry: 0.1},
		{name: "negative fraction", anchor: 1, now: 0, wantTicks: 0, wantCarry: 0.9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := State{ScaledAnchor: test.anchor, Carry: test.carry, Requested: 1, Active: 1}
			if got := s.AdvanceSP(test.now); got != test.wantTicks {
				t.Fatalf("budget = %d, want %d", got, test.wantTicks)
			}
			if s.Carry != test.wantCarry {
				t.Fatalf("carry = %v, want %v", s.Carry, test.wantCarry)
			}
		})
	}
}

func TestBudgetCarryUsesFloatingFloorBeforeWordWrap(t *testing.T) {
	// At speed 20, this valid signed delta produces 4294967294.25. The
	// retained signed word is -2, but carry remains .25 [01 §4.2].
	s := State{Requested: 20, Active: 20, Carry: 0.25}
	if got := s.AdvanceSP(math.MaxInt32); got != 0 || s.Carry != 0.25 {
		t.Fatalf("wrapped budget = %d, carry = %v; want 0, .25", got, s.Carry)
	}
}

func TestBudgetRoundedCarrySurvivesSave(t *testing.T) {
	// The stored speed multiplier at three is slightly above .3. A delta
	// of -10 therefore floors to -4 and leaves a remainder just below
	// one, which rounds to one at the float32 store [01 §4.2].
	s := State{ScaledAnchor: 10, Requested: 3, Active: 3}
	if got := s.AdvanceSP(0); got != 0 || s.Carry != 1 {
		t.Fatalf("negative budget = %d, carry = %v; want 0, 1", got, s.Carry)
	}
	var restored State
	if err := restored.LoadBoxChecked(s.SaveBox()); err != nil {
		t.Fatalf("rejecting a budget-produced scheduler image: %v", err)
	}
	if got, want := restored.AdvanceSP(1), s.AdvanceSP(1); got != want || restored.SaveBox() != s.SaveBox() {
		t.Fatalf("restored next budget = %d, want %d, or scheduler image differs", got, want)
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

func TestHysteresisThresholdEdges(t *testing.T) {
	tests := []struct {
		name       string
		capped     bool
		slew       int16
		requested  int32
		active     int32
		wantSlew   int16
		wantActive int32
	}{
		{name: "capped below", capped: true, slew: 9, requested: 10, active: 10, wantSlew: 10, wantActive: 10},
		{name: "capped at", capped: true, slew: 10, requested: 10, active: 10, wantSlew: 0, wantActive: 9},
		{name: "capped above", capped: true, slew: 11, requested: 10, active: 10, wantSlew: 0, wantActive: 9},
		{name: "normal below", slew: -99, requested: 10, active: 9, wantSlew: -100, wantActive: 9},
		{name: "normal at", slew: -100, requested: 10, active: 9, wantSlew: 0, wantActive: 10},
		{name: "normal above", slew: -101, requested: 10, active: 9, wantSlew: 0, wantActive: 10},
		{name: "normal does not lower", slew: -100, requested: 9, active: 10, wantSlew: 0, wantActive: 10},
		{name: "capped lower bound", capped: true, slew: 10, requested: 10, active: 1, wantSlew: 0, wantActive: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := State{Requested: test.requested, Active: test.active, slew: test.slew}
			var trunc int32
			if test.capped {
				trunc = 6
			}
			s.applyHysteresis(trunc)
			if s.slew != test.wantSlew || s.Active != test.wantActive {
				t.Fatalf("after hysteresis slew=%d active=%d, want slew=%d active=%d", s.slew, s.Active, test.wantSlew, test.wantActive)
			}
		})
	}
}

func TestSpeedBoundsAreInclusive(t *testing.T) {
	low := State{Requested: 0, Active: 0}
	low.AdvanceSP(0)
	if low.Requested != 1 || low.Active != 1 {
		t.Fatalf("low speed bounds requested=%d active=%d, want 1/1", low.Requested, low.Active)
	}
	high := State{Requested: 21, Active: 21}
	high.AdvanceSP(0)
	if high.Requested != 20 || high.Active != 20 {
		t.Fatalf("high speed bounds requested=%d active=%d, want 20/20", high.Requested, high.Active)
	}
}

func TestLoadBoxPreservesValidPrefixBits(t *testing.T) {
	var want [28]byte
	binary.LittleEndian.PutUint32(want[0:4], 0xffffcfc7) // int32(-12345)
	binary.LittleEndian.PutUint32(want[4:8], 5)
	binary.LittleEndian.PutUint32(want[8:12], 0xffffffef) // int32(-17)
	binary.LittleEndian.PutUint32(want[12:16], math.Float32bits(-0.5))
	binary.LittleEndian.PutUint32(want[16:20], 0xFEDCBA98)
	binary.LittleEndian.PutUint16(want[20:22], 7)
	binary.LittleEndian.PutUint16(want[22:24], 4)
	binary.LittleEndian.PutUint16(want[24:26], 0xff85) // int16(-123)
	// Preserve the lag bit and opaque upper bits; bit 2 agrees with 7 != 4.
	binary.LittleEndian.PutUint16(want[26:28], 0xA006)

	var got State
	if err := got.LoadBoxChecked(want); err != nil {
		t.Fatalf("LoadBoxChecked rejected valid image: %v", err)
	}
	if box := got.SaveBox(); box != want {
		t.Fatalf("valid scheduler image changed on round trip:\n got %x\nwant %x", box, want)
	}

	var prefixed State
	data := append(append([]byte(nil), want[:]...), 9, 9, 9, 9)
	if err := prefixed.LoadBoxBytes(data); err != nil {
		t.Fatalf("LoadBoxBytes rejected oversized valid image: %v", err)
	}
	if box := prefixed.SaveBox(); box != want {
		t.Fatalf("oversized scheduler image changed its prefix:\n got %x\nwant %x", box, want)
	}
}

func TestLoadBoxRejectsMalformedWithoutMutation(t *testing.T) {
	original := State{ScaledAnchor: 19, Delta: -3, Carry: 0.25, GlobalTick: 77, Requested: 7, Active: 4, Paused: true, pending: 2, slew: -9, flags: 0xA007}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "short", data: make([]byte, 27)},
		{name: "pending out of range", data: malformedBox(func(b *[28]byte) { binary.LittleEndian.PutUint32(b[4:8], 6) })},
		{name: "requested out of range", data: malformedBox(func(b *[28]byte) { binary.LittleEndian.PutUint16(b[20:22], 0) })},
		{name: "active out of range", data: malformedBox(func(b *[28]byte) { binary.LittleEndian.PutUint16(b[22:24], 21) })},
		{name: "non-finite carry", data: malformedBox(func(b *[28]byte) { binary.LittleEndian.PutUint32(b[12:16], math.Float32bits(float32(math.NaN()))) })},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := original
			if err := got.LoadBoxBytes(test.data); err == nil {
				t.Fatal("malformed scheduler image was accepted")
			}
			if got != original {
				t.Fatalf("malformed image partially mutated state:\n got %+v\nwant %+v", got, original)
			}
		})
	}
}

func malformedBox(edit func(*[28]byte)) []byte {
	var box [28]byte
	binary.LittleEndian.PutUint32(box[4:8], 2)
	binary.LittleEndian.PutUint32(box[12:16], math.Float32bits(0.25))
	binary.LittleEndian.PutUint16(box[20:22], 7)
	binary.LittleEndian.PutUint16(box[22:24], 4)
	binary.LittleEndian.PutUint16(box[26:28], 4)
	edit(&box)
	return box[:]
}
