// Play-test PT5 end-to-end lock. It lives in the external test package so it
// can drive the authoritative session the way a player does — issue one order
// through the human-command boundary, then let the ordinary tick loop run —
// which is the only vantage point from which the defect it locks is visible.
// The unit-level twin is TestControlByteBitFourDoesNotGateFiring; that one
// catches the same gate, this one catches an order-side writer of the same
// byte reappearing anywhere in the pipeline.
package combat_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestPT5_OrderedUnitsStillFire reproduces the play-test screenshot: a group of
// player units that have been given an order are standing on top of a hostile
// unit. They must kill it.
//
// The order is load-bearing. Issuing one purges the unit's queue, every removal
// path runs the cleanup walk of [04 R-ORDER-02 §2], and that walk sets bit 4 of
// each assigned slot's control byte and never clears it again. A build that
// read that bit as a firing gate produced exactly what the play-test reported:
// units adjacent to an enemy, with live weapons and full reloads, doing
// nothing at all for the rest of the battle.
func TestPT5_OrderedUnitsStillFire(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind:       headless.ScenarioCampaign,
		Mission:    "camps/Arm Campaign.tdf:MISSION0",
		LocalOwner: -1,
		FS:         fs,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := composed.Session
	scaled := sess.Clock.ScaledAnchor
	step := func(n int) {
		for i := 0; i < n; i++ {
			scaled += 5
			sess.Step(scaled)
		}
	}
	step(6)

	var victim *units.Unit
	var handles []pool.Handle
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		if u.Owner == 1 && victim == nil {
			victim = u
		}
		if u.Owner == 0 && u.Def.CanMove {
			handles = append(handles, u.Handle)
		}
	}
	if victim == nil || len(handles) == 0 {
		t.Skip("mission composed without both sides present")
	}

	if err := sess.EnqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanOrder,
		Order: session.HumanOrderCommand{
			Handles:  handles,
			Code:     int(input.LatchAttack),
			Target:   victim.Handle,
			Position: orders.ResolvePos{X: victim.X, Y: victim.Y, Z: victim.Z},
		},
	}); err != nil {
		t.Fatal(err)
	}
	step(30)

	// Park a handful of the ordered shooters on the target, which is the
	// geometry the screenshot shows; the chase geometry that would walk them
	// there is an open question in the order layer, not this package's.
	parked := 0
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Owner != 0 || parked >= 6 || !u.SlotAt(0).IsPopulated() {
			continue
		}
		offset := numeric.FixedFromInt(int64(24 + 16*parked))
		u.X = victim.X.Add(offset)
		u.Z = victim.Z.Add(offset)
		u.Y = victim.Y
		parked++
	}
	if parked == 0 {
		t.Skip("no armed player unit to park")
	}

	before := victim.Health
	if before <= 0 {
		t.Fatalf("target starts with no health")
	}
	for i := 0; i < 600 && victim.Alive && victim.Health > 0; i++ {
		scaled += 5
		sess.Step(scaled)
	}
	if victim.Health >= before {
		t.Fatalf("%d armed units adjacent to a hostile for 600 ticks took it from %d health to %d: nothing fired",
			parked, before, victim.Health)
	}
	if victim.Alive && !victim.Dying {
		t.Fatalf("target survived at %d of %d health after 600 adjacent ticks", victim.Health, before)
	}
}
