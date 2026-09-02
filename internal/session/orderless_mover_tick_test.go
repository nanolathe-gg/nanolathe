package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// unitTransformDirtyBit is bit 16 of the unit's flags word, the transform-dirty
// bit of [04 R-MOV-01 §1]. The post-move correction's gate reads it, so the
// test arms it the way a commit would [04 R-MOV-01 §5].
const unitTransformDirtyBit uint32 = 1 << 16

// TestOrderlessMoversAreSweptAndCorrected is the composed-session half of
// WU-19-29. It proves two things about the phase-2 unit sweep against real
// content, end to end:
//
//   - the sweep visits units in ascending pool order within ascending player
//     slots and in no other order [04 R-MOV-03 §1] "The player gate" (I1);
//   - the mover tick and the post-move correction run for EVERY live unit that
//     has a mover, whether or not it holds an order [04 R-MOV-03 §1] step 9.
//     The idle human's units are given no command for the whole battle, so
//     every one of them is orderless; each has its Y displaced and its
//     transform marked dirty, and one authoritative tick later the four-branch
//     correction of [04 R-MOV-01 §5] has written the displacement away.
//
// Before WU-19-29 the movement step returned at its empty-queue guard, so an
// orderless unit's Y was never corrected and its commit never ran.
func TestOrderlessMoversAreSweptAndCorrected(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	scaled := sess.Clock.ScaledAnchor
	for i := 0; i < 40 && sess.State != StateBattle; i++ {
		scaled += 5
		sess.Step(scaled)
	}
	if sess.State != StateBattle {
		t.Fatalf("session did not reach battle: state=%v", sess.State)
	}

	// The sweep order, read from the sweep itself: players ascending, and
	// within a player the pool slots ascending [04 R-MOV-03 §1] (I1).
	lastPlayer := -1
	lastSlot := pool.Handle(0)
	visited := 0
	sess.Units.VisitActiveSlots(func(v units.SlotVisit) {
		visited++
		owner := int(v.Unit.Owner)
		switch {
		case owner < lastPlayer:
			t.Fatalf("sweep visited player %d after player %d; the sweep runs slots 0..9 ascending [04 R-MOV-03 §1]", owner, lastPlayer)
		case owner > lastPlayer:
			lastPlayer, lastSlot = owner, 0
		}
		if v.Handle <= lastSlot {
			t.Fatalf("player %d slot %d visited after slot %d; within a player the sweep is ascending pool order [04 R-MOV-03 §1]", owner, v.Handle, lastSlot)
		}
		lastSlot = v.Handle
	})
	if visited == 0 {
		t.Fatal("the sweep visited no units")
	}

	// Every mover the idle human owns, displaced and marked dirty. A carried
	// unit is excluded: its commit takes the carried branch and the correction
	// never reaches the mode test [04 R-COLL-01 §1].
	const displaced = numeric.Fixed(99 << 16)
	type probe struct {
		handle pool.Handle
		before numeric.Fixed
	}
	var probes []probe
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Owner != sess.LocalOwner || u.Def == nil {
			continue
		}
		if !u.Def.BMCode || u.Def.CanFly || u.Attachment.Carrier != 0 {
			continue
		}
		if q := orders.QueueOfUnit(u); q != nil && q.Head() != nil {
			continue
		}
		probes = append(probes, probe{handle: u.Handle, before: u.Y})
		u.Y = displaced
		u.Flags |= unitTransformDirtyBit
	}
	if len(probes) == 0 {
		t.Skip("no orderless ground mover on the human slot to probe")
	}

	scaled += 5
	sess.Step(scaled)

	for _, p := range probes {
		u := sess.Units.Unit(p.handle)
		if u == nil || !u.Alive {
			continue
		}
		if u.Y == displaced {
			t.Fatalf("unit %d (%s) still reads the displaced Y after a full tick; the sweep must run the mover tick and the post-move correction for an orderless unit [04 R-MOV-03 §1][04 R-MOV-01 §5]",
				p.handle, u.Def.UnitName)
		}
	}
}
