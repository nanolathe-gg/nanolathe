package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The three contracts [04 R-ORD-01 §12] and [04 R-AIR-01 §16] closed and this
// unit implements: the air entry's two `VTOL_SeekAttack` replacements,
// `SelfRepair` phase 0's split reads, and `HelpBuild`'s target-side footprint.

// seekAtHead reports whether the queue's head is a spawned `VTOL_SeekAttack`
// and returns it, so each case asserts the relationship (a record of that
// identity at the head) rather than a queue census.
func seekAtHead(q *Queue) *Node {
	primary := q.Primary()
	if len(primary) == 0 {
		return nil
	}
	if DescriptorFor(primary[0].ID).Name != "VTOL_SeekAttack" {
		return nil
	}
	return primary[0]
}

// TestAirEntryStep1SeekReplacement locks step 1 of the shared air-attack entry
// as [04 R-AIR-01 §16] names its fields: on an interrupt, the replacement is
// issued only when the record is the LAST on its segment ("no successor
// marker" is the null next-record link) and the unit's fire-stance pair (bits
// 20-21, [04 R-STANCE-01 §2]) is not hold fire. The completion is
// unconditional either way, and the spawned record carries the SAME target and
// cached goal.
func TestAirEntryStep1SeekReplacement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stance    uint32
		successor bool
		wantSeek  bool
	}{
		{name: "last record, fire at will", stance: 2, wantSeek: true},
		{name: "last record, hold fire", stance: 0},
		{name: "has a successor, fire at will", stance: 2, successor: true},
	} {
		q, u := gateFixture()
		u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | tc.stance<<units.StandingFireShift
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return u }})
		q.Push(Lookup("AirToGround"), Node{Owner: u.Handle, Target: 7, GoalX: 11 << 16, GoalY: 12 << 16, GoalZ: 13 << 16})
		if tc.successor {
			q.Push(Lookup("Wait"), Node{Owner: u.Handle})
		}
		n := q.Primary()[0]

		code, done := airEntry(u, n, pendTargetRemoved, pendTargetGone)
		if !done || code != 5 {
			t.Fatalf("%s: (code %d, done %v), want (5, true) — step 1 returns 5 either way [04 R-AIR-01 §16]", tc.name, code, done)
		}
		seek := seekAtHead(q)
		if (seek != nil) != tc.wantSeek {
			t.Fatalf("%s: seek spawned = %v, want %v [04 R-AIR-01 §16]", tc.name, seek != nil, tc.wantSeek)
		}
		if seek == nil {
			continue
		}
		if seek.Target != n.Target {
			t.Fatalf("%s: seek target %d, want the replaced record's %d — the replacement carries the same target [04 R-AIR-01 §16]", tc.name, seek.Target, n.Target)
		}
		if seek.GoalX != n.GoalX || seek.GoalY != n.GoalY || seek.GoalZ != n.GoalZ {
			t.Fatalf("%s: seek goal (%d,%d,%d), want the replaced record's cached goal (%d,%d,%d) [04 R-AIR-01 §16]",
				tc.name, seek.GoalX, seek.GoalY, seek.GoalZ, n.GoalX, n.GoalY, n.GoalZ)
		}
	}
}

// TestAirEntryStep2SeekReplacement locks step 2: with the target reference no
// longer resolving, the seek is issued only for a record that was ISSUED
// AGAINST A TARGET (static-mask bit 9, [04 R-MOV-03 §7]) and is the last on its
// segment; it carries NO target and the unit's own position
// [04 R-AIR-01 §16].
//
// Constructor admission clears bit 9 for an order issued without a target;
// target removal clears the reference while retaining that metadata.
func TestAirEntryStep2SeekReplacement(t *testing.T) {
	for _, tc := range []struct {
		name      string
		target    pool.Handle
		mask      uint32
		successor bool
		wantSeek  bool
	}{
		{name: "target gone, last record", target: 7, mask: staticTargetObserver, wantSeek: true},
		{name: "target gone, has a successor", target: 7, mask: staticTargetObserver, successor: true},
		{name: "never issued against a target", target: 0, mask: 0},
		{name: "descriptor does not observe a target", target: 7, mask: 0},
	} {
		q, u := gateFixture()
		u.Flags |= 2 << units.StandingFireShift
		// The lookup resolves nothing: the target has gone since the record was
		// issued, which is §16's null target reference.
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return nil }})
		q.Push(Lookup("AirToGround"), Node{Owner: u.Handle, Target: tc.target, StaticGate: tc.mask | 1})
		if tc.successor {
			q.Push(Lookup("Wait"), Node{Owner: u.Handle})
		}
		n := q.Primary()[0]

		code, done := airEntry(u, n, 0, pendTargetGone)
		if !done || code != 5 {
			t.Fatalf("%s: (code %d, done %v), want (5, true) — a null target reference ends the order [04 R-AIR-01 §8]", tc.name, code, done)
		}
		seek := seekAtHead(q)
		if (seek != nil) != tc.wantSeek {
			t.Fatalf("%s: seek spawned = %v, want %v [04 R-AIR-01 §16]", tc.name, seek != nil, tc.wantSeek)
		}
		if seek == nil {
			continue
		}
		if seek.Target != 0 {
			t.Fatalf("%s: seek target %d, want 0 — step 2's replacement carries no target [04 R-AIR-01 §16]", tc.name, seek.Target)
		}
		if seek.GoalX != u.X || seek.GoalY != u.Y || seek.GoalZ != u.Z {
			t.Fatalf("%s: seek goal (%d,%d,%d), want the unit's own position (%d,%d,%d) [04 R-AIR-01 §16]",
				tc.name, seek.GoalX, seek.GoalY, seek.GoalZ, u.X, u.Y, u.Z)
		}
	}
}

// TestSelfRepairPhase0ReadsTargetRemainingAndOwnActivation locks the split
// [04 R-ORD-01 §12] corrected into [04 R-ORD-01 §2]'s row: the remaining
// fraction is the TARGET's (the repairer must be complete) and the activation
// bit is THIS UNIT's (the patient). Either failing abandons (8).
func TestSelfRepairPhase0ReadsTargetRemainingAndOwnActivation(t *testing.T) {
	for _, tc := range []struct {
		name              string
		repairerRemaining float32
		patientActivated  bool
		repairerActivated bool
		want              Code
	}{
		{name: "complete repairer, active patient", patientActivated: true, want: 1},
		{name: "incomplete repairer", repairerRemaining: 0.5, patientActivated: true, want: 8},
		{name: "inactive patient", want: 8},
		{name: "inactive patient, active repairer", repairerActivated: true, want: 8},
	} {
		q, u := gateFixture()
		u.Activated = tc.patientActivated
		repairer := &units.Unit{
			Handle:    7,
			Def:       &content.UnitDef{Builder: true},
			Remaining: tc.repairerRemaining,
			Activated: tc.repairerActivated,
		}
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return repairer }})
		n := &Node{ID: Lookup("SelfRepair"), Owner: u.Handle, Target: 7}

		if code := selfRepairHandler(u, n, 0, 40); code != tc.want {
			t.Fatalf("%s: code %d, want %d [04 R-ORD-01 §12][05 R-WORK-01 §3]", tc.name, code, tc.want)
		}
	}
}

// TestAssistApproachHalfIsTheTargetFootprint locks whose footprint the assist
// annulus's inner term reads: [04 R-ORD-01 §12] withdraws §5's "from my own
// footprint" and confirms [05 R-WORK-01 §2] — the pair comes from the TARGET's
// definition, only `builddistance` is the builder's.
//
// The case is asymmetric on purpose, because the radicand squares only the X
// term and adds Z twice: swapping the pair changes the answer, so a caller that
// passes the builder's footprint cannot accidentally agree.
func TestAssistApproachHalfIsTheTargetFootprint(t *testing.T) {
	const targetX, targetZ = int32(5), int32(2) // trunc(16*sqrt(25+2+2))/2
	const builderX, builderZ = int32(2), int32(5)

	got := AssistApproachHalf(targetX, targetZ)
	if want := int32(43); got != want {
		t.Fatalf("target-footprint half = %d, want %d [05 R-WORK-01 §2]", got, want)
	}
	if other := AssistApproachHalf(builderX, builderZ); other == got {
		t.Fatalf("the builder's footprint gives the same half (%d) — the case is not asymmetric enough to lock the contract", other)
	}
}
