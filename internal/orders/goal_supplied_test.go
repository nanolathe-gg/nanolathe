package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The record constructor's second presence clear, [04 §3.1]'s "0x400 cleared
// when constructed without a goal position" as closed by [04 R-MOV-03 §7], and
// its one reader: the guard's leg-4 copy arm [04 R-ORD-01 §13].
//
// `QMove`'s authored mask is bit 10 alone, so it isolates the bit.

// TestConstructorClearsGoalBitOnlyWhenNoGoalWasSupplied locks both directions
// of the clear, including the case a zero-coordinate heuristic gets WRONG: a
// record whose goal really is the fixed-point origin keeps bit 10, because the
// origin is a legal map position and presence is stated by the producer rather
// than inferred from the triple.
func TestConstructorClearsGoalBitOnlyWhenNoGoalWasSupplied(t *testing.T) {
	id := Lookup("QMove")
	if id == 0 {
		t.Fatal("QMove is not in the descriptor table")
	}
	if DescriptorFor(id).StaticGate != staticGoalObserver {
		t.Fatalf("QMove's static mask %#x is not bit 10 alone; the fixture proves nothing", DescriptorFor(id).StaticGate)
	}

	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle}) // no position payload
	if got := q.Primary()[0].StaticGate; got&staticGoalObserver != 0 {
		t.Fatalf("goal-less record static mask = %#x, want bit 10 clear [04 §3.1][04 R-MOV-03 §7]", got)
	}

	// The heuristic case: a goal WAS supplied and it is (0,0,0).
	q2, u2 := gateFixture()
	q2.Push(id, Node{Owner: u2.Handle, GoalX: 0, GoalY: 0, GoalZ: 0, GoalSupplied: true})
	if got := q2.Primary()[0].StaticGate; got&staticGoalObserver == 0 {
		t.Fatalf("origin-goal record static mask = %#x, want bit 10 kept: the origin is a goal [04 §3.1][04 R-MOV-03 §7]", got)
	}

	// An ordinary goal keeps it too, and the presence argument is consumed —
	// it is an insertion-time input, not stored record state [04 §3.2].
	q3, u3 := gateFixture()
	q3.Push(id, Node{Owner: u3.Handle, GoalX: numeric.Fixed(120 << 16), GoalZ: numeric.Fixed(40 << 16), GoalSupplied: true})
	stored := q3.Primary()[0]
	if stored.StaticGate&staticGoalObserver == 0 {
		t.Fatalf("goal record static mask = %#x, want bit 10 kept", stored.StaticGate)
	}
	if stored.GoalSupplied {
		t.Fatal("the goal-presence argument survived onto the stored record; it is an insertion-time input [04 §3.2]")
	}
}

// TestGuardCopyArmReadsTheConstructorsGoalBit locks the one reader of bit 10
// against the constructor's clear [04 R-ORD-01 §13]: the guard's leg-4 arm B
// copies the ward's front record only when that record has something to copy —
// a bound target, or a goal. A ward record built with NO goal has bit 10 clear
// and is not copied.
//
// `VTOL_MobileBuild` is the row this test actually decides: of the rows
// carrying the build-site class (bit 20) that reach leg 4, it and `MobileBuild`
// are the only two that also carry bit 10, and `MobileBuild` is taken by arm A.
// Both halves below go through the ordinary producer insertion, so the ward's
// bit is the constructor's own output for a record of that shape.
func TestGuardCopyArmReadsTheConstructorsGoalBit(t *testing.T) {
	id := rowVTOLMobileBuild
	if id == 0 {
		t.Fatal("VTOL_MobileBuild is not in the descriptor table")
	}
	if DescriptorFor(id).StaticGate&staticGoalObserver == 0 {
		t.Fatalf("VTOL_MobileBuild's static mask %#x carries no bit 10; the fixture proves nothing", DescriptorFor(id).StaticGate)
	}

	for _, tc := range []struct {
		name     string
		wardHead Node
		wantCopy bool
	}{
		{
			name:     "goal supplied: the ward's site is copied",
			wardHead: Node{GoalX: numeric.Fixed(500 << 16), GoalZ: numeric.Fixed(700 << 16), GoalSupplied: true},
			wantCopy: true,
		},
		{
			name:     "no goal supplied: the constructor clears bit 10 and nothing is copied",
			wardHead: Node{},
			wantCopy: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGuardFixture(t, 1, 1)
			f.guard.Def.Builder = true
			f.ward.Def.Builder = true

			head := tc.wardHead
			head.Owner = f.ward.Handle
			wq := QueueForUnit(f.ward)
			wq.Push(id, head)
			stored := wq.Primary()[0]
			if got := stored.StaticGate&staticGoalObserver != 0; got != tc.wantCopy {
				t.Fatalf("ward head static mask = %#x, bit 10 present = %v, want %v", stored.StaticGate, got, tc.wantCopy)
			}

			n := guardNode(f)
			n.Phase = 1
			code := guardHandler(f.guard, n, 0, 10)

			gq := QueueForUnit(f.guard)
			copied := gq.LenPrimary() > 0 && gq.Primary()[0].ID == id
			if copied != tc.wantCopy {
				t.Fatalf("guard copied the ward's record = %v, want %v (code %d) [04 R-ORD-01 §13]", copied, tc.wantCopy, code)
			}
			if tc.wantCopy && code != Code(3) {
				t.Fatalf("copy arm returned %d, want the wait code 3 [04 §3.3]", code)
			}
			if tc.wantCopy && gq.Primary()[0].GoalX != tc.wardHead.GoalX {
				t.Fatalf("copy goal = %v, want the ward's %v", gq.Primary()[0].GoalX, tc.wardHead.GoalX)
			}
		})
	}
}
