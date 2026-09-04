package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestSelfDestructParam2WordIsExact locks the two halves of [04 R-ORD-01 §14]
// that are easy to regress into a "behaviourally equivalent" shape: the seed
// stores the WHOLE high nibble as the marker, and the step replaces the word
// with `(count-1) | marker` rather than merging into a narrow field.
//
// A stand-in marker of one nibble bit passes every functional assertion in this
// package — the only readers of p2 are the marker test and the count — which is
// exactly why the exact word needs a test of its own.
func TestSelfDestructParam2WordIsExact(t *testing.T) {
	id := Lookup("SelfDestructFG")
	if id == 0 {
		t.Fatal("SelfDestructFG is not in the descriptor table")
	}
	_, u := standingFixture(nil) // no authored countdown: the definition field defaults to 5
	n := &Node{Owner: u.Handle}

	if code := selfDestructHandler(u, n, 0, 10); code != 1 {
		t.Fatalf("first visit returned %d, want advance (1) [04 R-ORD-01 §2]", code)
	}
	// Seed 5, one step taken: the whole word is 4 with all four marker bits.
	if n.Param2 != 0xf0000004 {
		t.Fatalf("p2 = %#x after the first step, want 0xf0000004 — the full-nibble marker over the count [04 R-ORD-01 §14]", n.Param2)
	}
	if code := selfDestructHandler(u, n, 0, 40); code != 1 {
		t.Fatalf("second visit returned %d, want advance (1)", code)
	}
	if n.Param2 != 0xf0000003 {
		t.Fatalf("p2 = %#x after the second step, want 0xf0000003", n.Param2)
	}

	// The count field is twenty-eight bits wide, not three: a count of 8 steps
	// to 7 rather than wrapping to 0 and latching p1 [04 R-ORD-01 §14].
	wide := &Node{Owner: u.Handle, Param2: 0xf0000008}
	if code := selfDestructHandler(u, wide, 0, 70); code != 1 {
		t.Fatalf("wide-count visit returned %d, want advance (1)", code)
	}
	if wide.Param2 != 0xf0000007 {
		t.Fatalf("p2 = %#x, want 0xf0000007 — the count is read from 28 bits [04 R-ORD-01 §14]", wide.Param2)
	}
	if wide.Param1 != 0 {
		t.Fatalf("p1 = %d, want 0 — only a count of zero latches the terminal step", wide.Param1)
	}
}

// TestSelfDestructSpawnLandsOnTheRearSegment locks the head insert's routing:
// "the front of the segment the record's rear-segment flag selects"
// [04 R-ORD-01 §1], where the flag is the descriptor's static bit 18
// [04 §3.1][04 R-ORD-01 §13]. `SelfDestruct` carries it and `SelfDestructFG`
// does not, so the two descriptors of one handler body land on different
// segments — which is what keeps a kamikaze's spawned countdown off the primary
// queue it would otherwise block.
func TestSelfDestructSpawnLandsOnTheRearSegment(t *testing.T) {
	rear := Lookup("SelfDestruct")
	front := Lookup("SelfDestructFG")
	if rear == 0 || front == 0 {
		t.Fatal("the self-destruct descriptors are not in the table")
	}
	if !isSecondary(rear) {
		t.Fatalf("SelfDestruct static mask %#x lacks the rear-segment bit [04 §3.1]", DescriptorFor(rear).StaticGate)
	}
	if isSecondary(front) {
		t.Fatalf("SelfDestructFG static mask %#x carries the rear-segment bit; it is the front-segment twin [04 R-ORD-01 §2]", DescriptorFor(front).StaticGate)
	}

	q, u := standingFixture(nil)
	spawnAtSegmentHead(q, rear, Node{Owner: u.Handle, Param1: 1})
	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the spawned rear record to stay off the primary segment", q.LenPrimary())
	}
	if got := len(q.Secondary()); got != 1 {
		t.Fatalf("secondary length = %d, want 1", got)
	}

	// A front-segment descriptor still head-inserts into the primary segment.
	spawnAtSegmentHead(q, front, Node{Owner: u.Handle})
	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the front-segment spawn at the primary head", q.LenPrimary())
	}
}

// TestStopClearsTargetsWithoutTouchingTheControlByte locks the unconditional
// entry of [R-ORDER-02 §3]: it reads and writes no slot control byte, so a
// slot's enabled bit and its autonomy survive a `Stop`, and a slot whose
// enabled bit is clear has its stale target pair cleared all the same. Picking
// either verb's guard here instead would silence `TargetCleared` for one half
// of the slots and flip autonomy for the other.
func TestStopClearsTargetsWithoutTouchingTheControlByte(t *testing.T) {
	def := &content.UnitDef{UnitName: "stopper"}
	_, u := standingFixture(def)

	// Slot 0: enabled and autonomous. Slot 1: neither. Slot 2: already empty.
	for i := 0; i < units.NumSlots; i++ {
		s := u.SlotAt(i)
		if s == nil {
			t.Fatalf("slot %d is absent from the unit record", i)
		}
	}
	u.SlotAt(0).Flags = units.SlotFlagEnabled | units.SlotFlagAutonomous
	u.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: pool.Handle(9)}
	u.SlotAt(1).Flags = 0
	u.SlotAt(1).Target = units.Target{Kind: units.TargetUnit, Unit: pool.Handle(11)}
	u.SlotAt(2).Flags = units.SlotFlagEnabled
	u.SlotAt(2).Target = units.Target{Kind: units.TargetNone}

	clearWeaponTargetsUnconditional(u)

	for i, want := range []uint8{units.SlotFlagEnabled | units.SlotFlagAutonomous, 0, units.SlotFlagEnabled} {
		if got := u.SlotAt(i).Flags; got != want {
			t.Fatalf("slot %d control byte = %#x after an unconditional clear, want %#x unchanged [R-ORDER-02 §3]", i, got, want)
		}
	}
	for i := 0; i < units.NumSlots; i++ {
		if k := u.SlotAt(i).Target.Kind; k != units.TargetNone {
			t.Fatalf("slot %d target kind = %v, want the empty pair — the clear has no enabled-bit guard [R-ORDER-02 §3]", i, k)
		}
	}
}

// TestAirWorkPreambleDropCommitsAirborneMode locks the preamble's carried arm:
// the drop passes third value 2 to the attach commit, and that value IS the
// cargo's committed mover-mode pair [R-COB-03 §5][04 R-ORD-01 §7]. A carried
// air worker therefore comes out of the drop AIRBORNE, which is also why the
// takeoff arm below it — "only when the mover is grounded (mode 1)" — does not
// fire for it.
func TestAirWorkPreambleDropCommitsAirborneMode(t *testing.T) {
	def := &content.UnitDef{UnitName: "airworker", CanFly: true, BMCode: true, CruiseAlt: 100}
	q, u := standingFixture(def)
	carrier := &units.Unit{Handle: 2, Def: &content.UnitDef{UnitName: "carrier"}, Alive: true}
	BindQueue(carrier, q)
	q.binding.Lookup = func(h pool.Handle) *units.Unit {
		switch h {
		case u.Handle:
			return u
		case carrier.Handle:
			return carrier
		}
		return nil
	}
	u.Attachment.Carrier = carrier.Handle
	u.Attachment.AttachPiece = 3
	carrier.Attachment.Cargo = []pool.Handle{u.Handle}
	u.Move.Mode = 0 // attached/parked [04 R-MOV-01 §8]

	n := &Node{Owner: u.Handle}
	if code := airWorkPreamble(u, n, "Building"); code != 1 {
		t.Fatalf("preamble returned %d, want advance (1) [04 R-ORD-01 §7]", code)
	}
	if got := u.Move.Mode & 0x3; got != 2 {
		t.Fatalf("committed mover mode = %d after the drop, want 2 (airborne) — the attach commit's third value [R-COB-03 §5]", got)
	}
	if u.Attachment.Carrier != 0 || len(carrier.Attachment.Cargo) != 0 {
		t.Fatalf("carrier link = %d, cargo = %v, want both severed", u.Attachment.Carrier, carrier.Attachment.Cargo)
	}
	if n.DynamicGate&gateMoveOutcomes != 0 {
		t.Fatalf("gate = %#x, want no takeoff marker: the drop already committed mode 2, so the grounded arm does not run", n.DynamicGate)
	}
}
