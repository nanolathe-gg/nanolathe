package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestPitchSpeedTable locks C21: pitch index = signed height delta arithmetic-shifted
// right by 11 clamped to [-5,+5] indexes table {25,55,70,85,100,100,75,50,25,20,15}
// for indexes -5..+5 asymmetric middle; cap = table[i]*MaxVelocity/100 with signed
// truncation. Below-water halving gate covered. [04 §8.1] C21
func TestPitchSpeedTable(t *testing.T) {
	// Use MaxVelocity that makes cap integer per percent without remainder for table fidelity,
	// plus a second non-divisible case for truncation check.
	const maxVelocity = 100 * 65536                                     // 6553600: cap = table*65536, integer percent [04 §8.1] C21
	expected := [11]int32{25, 55, 70, 85, 100, 100, 75, 50, 25, 20, 15} // [04 §8.1] C21 index -5..+5

	// Every table entry via constructed deltas: delta = i<<11
	for i := -5; i <= 5; i++ {
		delta := int32(i << 11) // arithmetic shift will recover i [04 §8.1] C21
		idx := PitchIndex(delta)
		if idx != i {
			t.Fatalf("PitchIndex delta %d gave idx %d want %d", delta, idx, i)
		}
		cap := PitchCap(delta, maxVelocity)
		want := expected[i+5] * 65536
		if cap != want {
			t.Fatalf("PitchCap i=%d delta %d cap %d want %d (table %d)", i, delta, cap, want, expected[i+5])
		}
		// Also via SteerState.SpeedCap without halving (height above sea)
		s := &SteerState{MaxVelocity: maxVelocity, HeightWord: 100, SeaLevel: 10, DefFlags: 0}
		cap2 := s.SpeedCap(delta)
		if cap2 != want {
			t.Fatalf("SpeedCap no-halve i=%d got %d want %d", i, cap2, want)
		}
	}

	// Clamping beyond range: large positive/negative deltas saturate [04 §8.1] C21
	cases := []struct {
		delta int32
		want  int32
		name  string
	}{
		{6 << 11, 15 * 65536, "+6 clamped to +5 =>15"},                   // 75? no, +5 is 15 [04 §8.1] C21
		{100 << 11, 15 * 65536, "large + clamped"},                       // extreme positive
		{-6 << 11, 25 * 65536, "-6 clamped to -5 =>25"},                  // [04 §8.1] C21
		{-100 << 11, 25 * 65536, "large - clamped"},                      // extreme negative
		{(5 << 11) + 2047, 15 * 65536, "just under next clamp still +5"}, // 10240+2047=12287 >>11 =5
		{(-5 << 11) - 1, 25 * 65536, "just below -5 still -5"},           // -10241 >>11 arithmetic = -6? Check: -10241>>11 = -6 with arithmetic? Actually -10241 decimal binary shift yields -6 (floor) vs trunc? Need arithmetic shift floors. In Go int32 shift floors? For -10241>>11: -10241/2048=-5.000... floors to -6 but retail trunc? Let's verify. Pit: delta=-10241 => -10241>>11 = -6 in Go arithmetic floors, but retail arithmetic shift floors too. That would be -6 clamped to -5 same table 25. So okay.
	}
	for _, tc := range cases {
		got := PitchCap(tc.delta, maxVelocity)
		if got != tc.want {
			t.Fatalf("%s: PitchCap delta %d got %d want %d", tc.name, tc.delta, got, tc.want)
		}
	}

	// Asymmetric middle note: both -1 and 0 give 100 [04 §8.1] C21
	if PitchCap(-1<<11, maxVelocity) != 100*65536 {
		t.Fatal("asymmetric: index -1 should be 100")
	}
	if PitchCap(0, maxVelocity) != 100*65536 {
		t.Fatal("asymmetric: index 0 should be 100")
	}
	// But +1 is 75 not 100, and -2 is 85 not 75 etc — ensure asymmetry
	if PitchCap(1<<11, maxVelocity) == PitchCap(-1<<11, maxVelocity) {
		t.Fatal("asymmetric: +1 (75) must differ from -1 (100)")
	}

	// Signed truncation check [I3][01 §8]: non-divisible MaxVelocity
	const oddMax = 65537 // 1 + 65536, not divisible by 100
	// 25*65537/100 = 16384 trunc toward zero (16384.25), floor would be 16384 as well positive but test negative
	if got := PitchCap(0, oddMax); got != int32(int64(100)*int64(oddMax)/100) {
		t.Fatalf("odd max trunc check got %d", got)
	}
	// Negative MaxVelocity trunc toward zero vs floor: 25*-65537/100 = -16384 trunc, floor -16385
	negMax := int32(-65537)
	gotNeg := PitchCap(-5<<11, negMax)                // table 25 * -65537 /100
	wantNeg := int32(int64(25) * int64(negMax) / 100) // trunc toward zero
	if gotNeg != wantNeg {
		t.Fatalf("signed trunc negative max: got %d want %d (trunc toward zero) [I3]", gotNeg, wantNeg)
	}
	if wantNeg != -16384 { // -16384.25 trunc to -16384
		t.Fatalf("calc sanity: wantNeg %d want -16384", wantNeg)
	}
	// If floor was used it would be -16385, so this locks trunc.

	// Below-water halving gate [04 §8.1] C21: HeightWord < SeaLevel && flags&0x81000==0 => halve
	// Table index 0 => 100% => cap 6553600 => halved 3276800
	halveDelta := int32(0) // index 0 => 100
	full := int32(100 * 65536)
	halved := full / 2
	// Height below sea, no flags => halve
	for _, flags := range []uint32{0} {
		s := &SteerState{MaxVelocity: maxVelocity, HeightWord: 5, SeaLevel: 10, DefFlags: flags} // 5<10
		if got := s.SpeedCap(halveDelta); got != halved {
			t.Fatalf("below-water halve flags 0x%x got %d want %d [04 §8.1] C21", flags, got, halved)
		}
	}
	// Each flag combination set/unset when below water [04 §8.1] C21 0x1000 canhover, 0x80000 floater
	combs := []struct {
		flags uint32
		halve bool
		name  string
	}{
		{0, true, "neither"},
		{0x1000, false, "canhover"},
		{0x80000, false, "floater"},
		{0x1000 | 0x80000, false, "both"},
		{0x81000, false, "mask direct"},
		{0x400, true, "other bit alone still halve"},
	}
	for _, tc := range combs {
		s := &SteerState{MaxVelocity: maxVelocity, HeightWord: 5, SeaLevel: 10, DefFlags: tc.flags}
		got := s.SpeedCap(halveDelta)
		want := full
		if tc.halve {
			want = halved
		}
		if got != want {
			t.Fatalf("halve combo %s flags 0x%x got %d want %d", tc.name, tc.flags, got, want)
		}
	}
	// Above water: never halve even with no flags
	s := &SteerState{MaxVelocity: maxVelocity, HeightWord: 10, SeaLevel: 10, DefFlags: 0} // 10<10 false
	if got := s.SpeedCap(halveDelta); got != full {
		t.Fatalf("above water (equal) should not halve got %d want %d", got, full)
	}
	s = &SteerState{MaxVelocity: maxVelocity, HeightWord: 15, SeaLevel: 10, DefFlags: 0} // 15<10 false
	if got := s.SpeedCap(halveDelta); got != full {
		t.Fatalf("above water (greater) should not halve got %d want %d", got, full)
	}
	// Below water but flags block halve => full preserved even at max pitch index
	s = &SteerState{MaxVelocity: maxVelocity, HeightWord: -10, SeaLevel: 0, DefFlags: 0x1000}
	deltaPos := int32(5 << 11) // table 15 => cap 15*65536=983040
	if got := s.SpeedCap(deltaPos); got != 15*65536 {
		t.Fatalf("below but canhover => not halve got %d want %d", got, 15*65536)
	}
	s = &SteerState{MaxVelocity: maxVelocity, HeightWord: -10, SeaLevel: 0, DefFlags: 0}
	if got := s.SpeedCap(deltaPos); got != (15*65536)/2 {
		t.Fatalf("below no flags => halve got %d want %d", got, (15*65536)/2)
	}
	// Also verify halving truncates toward zero for odd cap (positive caps always even? but check)
	s = &SteerState{MaxVelocity: 65537, HeightWord: -5, SeaLevel: 10, DefFlags: 0}
	capNoHalve := PitchCap(0, 65537)   // 100*65537/100=65537
	if s.SpeedCap(0) != capNoHalve/2 { // 65537/2 trunc 32768
		t.Fatalf("halve trunc: got %d want %d", s.SpeedCap(0), capNoHalve/2)
	}
}

// TestDesiredHeadingWrap checks wrapping across the 0/65535 boundary [04 §8.1] C20.
func TestDesiredHeadingWrap(t *testing.T) {
	// Current near max, desired just past zero => delta +1
	s := &SteerState{Heading: 65535, TurnRate: 100, Dirty: false}
	s.UpdateHeading(0) // desired 0 - current 65535 = 1 as int16 [04 §8.1] C20
	if s.PendingHeading != 0 {
		t.Fatalf("wrap 65535->0 pending %d want 0", s.PendingHeading)
	}
	if !s.Dirty {
		t.Fatal("wrap delta +1 should set Dirty")
	}
	// Opposite wrap: current 0 desired 65535 => delta -1
	s = &SteerState{Heading: 0, TurnRate: 100, Dirty: false}
	s.UpdateHeading(65535)
	if s.PendingHeading != 65535 {
		t.Fatalf("wrap 0->65535 pending %d want 65535", s.PendingHeading)
	}
	if int16(s.PendingHeading-s.Heading) != -1 { // raw delta -1
		t.Fatalf("wrap other delta not -1")
	}
	// Far wrap: current 100 desired 65435 => delta = -201? Check int16(65435-100)= -201?
	s = &SteerState{Heading: 100, TurnRate: 5000, Dirty: false}
	s.UpdateHeading(65435)
	{
		a := uint16(100)
		b := uint16(65435)
		wantDelta := int16(b - a) // wraps: 65335 as unsigned => -201 as int16
		if int16(s.PendingHeading-s.Heading) != wantDelta {
			t.Fatalf("far wrap delta %d want %d", int16(s.PendingHeading-s.Heading), wantDelta)
		}
	}
	// Large delta wraps correctly: 32767 then 32768 etc
	s = &SteerState{Heading: 32768, TurnRate: 40000, Dirty: false}
	s.UpdateHeading(0) // 0-32768 = -32768 as int16
	if s.PendingHeading != 0 {
		t.Fatalf("wrap 32768->0 pending %d want 0", s.PendingHeading)
	}
}

// TestHeadingClamp checks clamping to TurnRate both signs [04 §8.1] C20.
func TestHeadingClamp(t *testing.T) {
	// Positive clamp
	s := &SteerState{Heading: 0, TurnRate: 100}
	s.UpdateHeading(1000) // delta 1000 clamped to 100
	if s.PendingHeading != 100 {
		t.Fatalf("positive clamp pending %d want 100", s.PendingHeading)
	}
	if !s.Dirty {
		t.Fatal("clamped positive should be Dirty")
	}
	// Negative clamp
	s = &SteerState{Heading: 1000, TurnRate: 50}
	s.UpdateHeading(0) // delta -1000 clamped to -50 => pending 950
	if s.PendingHeading != 950 {
		t.Fatalf("negative clamp pending %d want 950", s.PendingHeading)
	}
	// No clamp when delta smaller than TurnRate
	s = &SteerState{Heading: 0, TurnRate: 2000}
	s.UpdateHeading(500)
	if s.PendingHeading != 500 {
		t.Fatalf("no clamp pending %d want 500", s.PendingHeading)
	}
	// Exact TurnRate boundary
	s = &SteerState{Heading: 0, TurnRate: 100}
	s.UpdateHeading(100)
	if s.PendingHeading != 100 {
		t.Fatalf("exact boundary pending %d want 100", s.PendingHeading)
	}
	// Wraps then clamps: current 65530 desired 100 => delta 106? Actually 100-65530=106 as int16
	s = &SteerState{Heading: 65530, TurnRate: 10}
	s.UpdateHeading(100) // delta 106 clamped 10 => pending 4 (65530+10 wraps to 4)
	if s.PendingHeading != 4 {
		t.Fatalf("wrap+clamp pending %d want 4", s.PendingHeading)
	}
	// TurnRate zero => no turn, pending unchanged, Dirty stays false
	s = &SteerState{Heading: 1234, TurnRate: 0, Dirty: false, PendingHeading: 9999}
	s.UpdateHeading(5000)
	if s.PendingHeading != 1234 {
		t.Fatalf("zero TurnRate pending %d want 1234 unchanged", s.PendingHeading)
	}
	if s.Dirty {
		t.Fatal("zero TurnRate with zero delta should not set Dirty")
	}
	// Ensure negative wraps clamp correctly: current 10 desired 65530 => delta -16? 65530-10=65520 as int16=-16, clamp -10 => pending 0?
	s = &SteerState{Heading: 10, TurnRate: 10}
	s.UpdateHeading(65530) // delta -16 clamped -10 => pending 0 (10-10=0)
	if s.PendingHeading != 0 {
		t.Fatalf("neg wrap+clamp pending %d want 0", s.PendingHeading)
	}
}

// TestPendingBeforeIntegration asserts pending heading and dirty flag update BEFORE integration [04 §8.1] C20.
func TestPendingBeforeIntegration(t *testing.T) {
	s := &SteerState{
		X:              0,
		Z:              0,
		Heading:        0,
		TurnRate:       100,
		Speed:          65536, // 1.0
		MaxVelocity:    65536,
		PendingHeading: 0,
		Dirty:          false,
	}
	// Step 1: UpdateHeading sets Pending and Dirty without moving position or changing current Heading
	s.UpdateHeading(100) // delta 100
	if s.PendingHeading != 100 {
		t.Fatalf("pending not set before integration: got %d want 100", s.PendingHeading)
	}
	if !s.Dirty {
		t.Fatal("Dirty not set before integration [04 §8.1] C20")
	}
	if s.Heading != 0 {
		t.Fatalf("current Heading should not change until Integrate, got %d", s.Heading)
	}
	if s.X != 0 || s.Z != 0 {
		t.Fatalf("position should not move until Integrate X/Z %d/%d want 0/0", s.X, s.Z)
	}
	// Step 2: Integrate commits heading and advances position
	s.Integrate()
	if s.Heading != 100 {
		t.Fatalf("after Integrate Heading %d want 100", s.Heading)
	}
	if s.X == 0 && s.Z == 0 {
		t.Fatal("after Integrate position should have advanced along heading 100")
	}
	// Ensure Integrate used pending heading, not stale
	// Check that X/Z correspond to heading 100 via fixed trig. The step is
	// vx = -((sin*speed + 0x1000) >> 13), vz = -((cos*speed + 0x1000) >> 13)
	// [04 R-MOV-01 §4]; the mirror below carried the pre-correction signs.
	sin := numeric.Sin(numeric.Angle(100))
	cos := numeric.Cos(numeric.Angle(100))
	wantX := -int32((int64(sin)*int64(65536) + 0x1000) >> 13)
	wantZ := -int32((int64(cos)*int64(65536) + 0x1000) >> 13)
	if s.X != wantX || s.Z != wantZ {
		t.Fatalf("integration step X/Z %d/%d want %d/%d heading 100", s.X, s.Z, wantX, wantZ)
	}
	// Ensure ordering: calling Integrate without UpdateHeading does not invent a pending
	s2 := &SteerState{X: 0, Z: 0, Heading: 50, Speed: 65536, TurnRate: 10, Dirty: false, PendingHeading: 50}
	s2.Integrate() // no pending change, should keep heading 50 and move along 50
	if s2.Heading != 50 {
		t.Fatalf("no-update Integrate should keep heading 50 got %d", s2.Heading)
	}
}

// TestNoReverse checks that negative target speed never produces negative speed — absence of reverse branch [04 §8.1] C20.
func TestNoReverse(t *testing.T) {
	s := &SteerState{
		MaxVelocity: 65536,
		HeightWord:  20, // above sea
		SeaLevel:    10,
		DefFlags:    0,
		Speed:       65536,
	}
	// Negative target must not appear: UpdateSpeed with negative target clamps to 0 [04 §8.1] C20 no reverse
	s.UpdateSpeed(-50000, 0)
	if s.Speed < 0 {
		t.Fatalf("negative target produced negative speed %d; no reverse branch must be absent [04 §8.1] C20", s.Speed)
	}
	if s.Speed != 0 {
		t.Fatalf("negative target should clamp to 0, got %d", s.Speed)
	}
	// Also via ClampSpeed helper
	if got := ClampSpeed(-100, 0, 65536, 20, 10, 0); got < 0 {
		t.Fatalf("ClampSpeed negative target gave negative %d", got)
	}
	if got := ClampSpeed(-100, 0, 65536, 20, 10, 0); got != 0 {
		t.Fatalf("ClampSpeed negative target want 0 got %d", got)
	}
	// Speed should never go negative even when cap is small and current Speed is high then pitch changes
	s.Speed = 50000
	s.UpdateSpeed(50000, 5<<11) // table 15% => cap ~15% => 9830, target 50000 clamped to cap positive
	if s.Speed < 0 {
		t.Fatalf("capped speed negative %d", s.Speed)
	}
	// Even with MaxVelocity negative? Speed still not negative (no reverse branch)
	s2 := &SteerState{MaxVelocity: -65536, HeightWord: 20, SeaLevel: 10, DefFlags: 0, Speed: 100}
	s2.UpdateSpeed(100, 0) // cap = 100*-65536/100 = -65536, target 100 > cap => clamp to cap negative but Speed must not stay negative due to no reverse rule
	if s2.Speed < 0 {
		t.Fatalf("negative cap should not produce negative speed (no reverse) got %d", s2.Speed)
	}
	// Starting speed negative should be corrected to 0 before any update [04 §8.1] C20
	s3 := &SteerState{MaxVelocity: 65536, HeightWord: 20, SeaLevel: 10, Speed: -100}
	s3.UpdateSpeed(0, 0)
	if s3.Speed < 0 {
		t.Fatalf("initial negative speed not corrected to 0, got %d", s3.Speed)
	}
	// Ensure UpdateHeading never touches speed (no reverse branch touch)
	s4 := &SteerState{Heading: 0, Speed: 32768, TurnRate: 10}
	s4.UpdateHeading(100)
	if s4.Speed != 32768 {
		t.Fatalf("UpdateHeading must not change Speed (no reverse branch) got %d want 32768", s4.Speed)
	}
	if s4.Speed < 0 {
		t.Fatalf("speed negative after heading update %d", s4.Speed)
	}
}
