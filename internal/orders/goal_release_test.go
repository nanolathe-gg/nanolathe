package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestReleaseGoalPayloadReachesTheMoverPort locks the order-side half of
// [04 R-ORD-01 §1]'s payload release: the row hands the record to the mover's
// release port — the helper "runs entirely through the owner's mover" — instead
// of writing record bits by itself, and the release form does NOT clear pending
// `0x20`-`0x200`. That clear belongs to the branch that installs a new object;
// "the release form (no new object) is step (1) alone, which is why it leaves
// `0x80` visible" [04 R-ORD-01 §1][04 R-ORD-01 §0].
//
// `Paralyze` is the row exercised because its arm calls the release
// unconditionally between the slot release and the deadline arm
// [04 R-ORD-01 §2].
func TestReleaseGoalPayloadReachesTheMoverPort(t *testing.T) {
	rng.SeedGlobal(1, 0)
	u := &units.Unit{
		Handle:    1,
		Def:       &content.UnitDef{BMCode: true},
		Alive:     true,
		X:         numeric.Fixed(70 << 16),
		Z:         numeric.Fixed(90 << 16),
		Health:    3000,
		MaxHealth: 3000,
	}
	var released []*Node
	q := &Queue{binding: &QueueBinding{
		SimRNG: rng.Global.Sim,
		Movement: &MovementGoalAdapter{
			Release: func(n *Node) bool { released = append(released, n); return true },
		},
	}}
	BindQueue(u, q)

	q.Push(Lookup("Paralyze"), Node{Owner: u.Handle, Param1: 90})
	n := q.Primary()[0]
	// Movement outcomes the previous payload already produced.
	n.Satisfied |= 0x20 | 0x40 | 0x100 | 0x200
	q.Pump(u, 0)

	if len(released) != 1 || released[0] != n {
		t.Fatalf("mover release port saw %d records, want exactly this one", len(released))
	}
	if n.Satisfied&0x3E0 != 0x3E0-0x80 {
		t.Errorf("pending word = %#x, want 0x20/0x40/0x100/0x200 untouched by the release form", n.Satisfied)
	}
}

// TestReleaseGoalPayloadWithNoMoverPortIsSilent locks the fixture case that the
// four call sites depend on: a queue with no movement service bound leaves the
// record entirely alone rather than synthesising a record-side release. The
// mover-less no-op itself lives at the movement seam, which is where the
// owner's mover can actually be seen [04 R-ORD-01 §1].
func TestReleaseGoalPayloadWithNoMoverPortIsSilent(t *testing.T) {
	rng.SeedGlobal(1, 0)
	u := &units.Unit{Handle: 1, Def: &content.UnitDef{BMCode: true}, Alive: true, Health: 10, MaxHealth: 10}
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	BindQueue(u, q)

	n := &Node{Owner: u.Handle, Satisfied: 0x40}
	releaseGoalPayload(u, n)
	if n.Satisfied != 0x40 {
		t.Errorf("pending word = %#x, want untouched without a movement service", n.Satisfied)
	}
}
