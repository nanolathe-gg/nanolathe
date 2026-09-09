// Play-test PT6 end-to-end lock, the sequel to pt5_ordered_attack_test.go.
// PT5 proved that units standing next to an enemy fire; it had to PARK them
// there, because nothing in the order layer would walk them. This one removes
// the parking: the ordered units must close the distance themselves.
package combat_test

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestPT6_OrderedAttackClosesAndKills is the play-test report "units are still
// not attacking each other" in its second, harder half. Auto-acquisition works;
// an EXPLICIT attack order did not, because `Attack_Chase` never bound a weapon
// slot and its standoff was a placeholder 64 world units rather than the slot's
// engagement distance [06 R-WPN-05 §1]. Ordered units orbited outside their own
// range forever.
//
// The lock is the whole loop, because every link in it was broken at once: the
// resolver must choose `Attack_Chase` from the ACTOR's mover reference
// [R-ORD-02 §1], phase 0 must admit and pick a slot, phase 2 must install goals
// sized from the weapon's range, the mover must walk them, phase 1's
// shot-admission gate must pass at that distance, and the slot binding must
// survive the release of slots 0 and 2 beside it [04 R-ORD-01 §3][04 R-ORD-01 §7].
// Asserting only "a slot got bound" would pass on a build that never moved.
func TestPT6_OrderedAttackClosesAndKills(t *testing.T) {
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
	var shooters []pool.Handle
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		if u.Owner == 1 && victim == nil {
			victim = u
		}
		if u.Owner == 0 && u.Def.CanMove && u.Flags&units.ArmedStatus != 0 {
			shooters = append(shooters, u.Handle)
		}
	}
	if victim == nil || len(shooters) == 0 {
		t.Skip("mission composed without an armed attacker and a hostile")
	}

	// Distance before the order, so "it closed" is measured, not assumed.
	startGap := planarGap(sess.Units.Unit(shooters[0]), victim)

	if err := sess.EnqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanOrder,
		Order: session.HumanOrderCommand{
			Handles:  shooters,
			Code:     int(input.LatchAttack),
			Target:   victim.Handle,
			Position: orders.ResolvePos{X: victim.X, Y: victim.Y, Z: victim.Z},
		},
	}); err != nil {
		t.Fatal(err)
	}
	step(2)

	// The order must have resolved to the chase, not to the stationary variant.
	lead := sess.Units.Unit(shooters[0])
	q := orders.QueueForUnit(lead)
	if q == nil || q.LenPrimary() == 0 {
		t.Fatalf("the attack order queued nothing on an armed mover")
	}
	if name := orders.DescriptorFor(q.Primary()[0].ID).Name; name != "Attack_Chase" {
		t.Fatalf("ordered attack resolved %q, want Attack_Chase [R-ORD-02 §1]", name)
	}

	before := victim.Health
	closest := startGap
	for i := 0; i < 1800 && victim.Alive && victim.Health > 0; i++ {
		scaled += 5
		sess.Step(scaled)
		for _, h := range shooters {
			if gap := planarGap(sess.Units.Unit(h), victim); gap >= 0 && gap < closest {
				closest = gap
			}
		}
	}

	// The standoff is the weapon's range, so a chase that works ends with some
	// shooter inside it. A build that orbits at a fixed placeholder never gets
	// there no matter how long it runs.
	reach := int64(0)
	if s := lead.SlotAt(0); s != nil && s.Weapon != nil {
		reach = int64(s.Weapon.Range)
	}
	if reach <= 0 {
		t.Skip("lead shooter has no ranged primary")
	}
	if closest > reach {
		// WU-19-90 closed the stall this arm used to skip on, so it fails now.
		// That stall was: the goal phase 2 installs is one the mover already
		// stands inside, so the search reports start-satisfied and raises only
		// `0x100` — a bit every satisfied set masks out — and the empty
		// publication that follows does not raise `0x40` either, because the
		// goal answers "already there" [04 R-PATH-01 §4 step 6][04 R-PATH-01 §7].
		// The `0x20` phase 3's `0x40E0` consumes is the follower's, and no
		// arrival handle was bound for a chase, so it had no producer at all
		// [04 R-MOV-03 §1][04 R-ORD-01 §0]. Report the phase and the satisfied
		// word, because that is what separates a regression of that fix from a
		// chase that is merely slow.
		if n := chaseHeadOf(lead); n != nil {
			t.Fatalf("ordered attackers never closed: nearest approach %d world units against a weapon range of %d "+
				"(they started %d away); `Attack_Chase` sits in phase %d with satisfied=%#x against the 0x40E0 "+
				"re-arm mask [04 R-ORD-01 §3]", closest, reach, startGap, n.Phase, n.Satisfied)
		}
		t.Fatalf("ordered attackers never closed: nearest approach %d world units against a weapon range of %d (they started %d away)",
			closest, reach, startGap)
	}
	if victim.Health >= before && victim.Alive {
		t.Fatalf("ordered attackers closed to %d of %d range but never fired: target still at %d of %d health",
			closest, reach, victim.Health, before)
	}
}

// chaseHeadOf returns the unit's primary head record when it is an
// `Attack_Chase`, and nil otherwise.
func chaseHeadOf(u *units.Unit) *orders.Node {
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return nil
	}
	n := q.Primary()[0]
	if n == nil || orders.DescriptorFor(n.ID).Name != "Attack_Chase" {
		return nil
	}
	return n
}

// planarGap is the whole-world-unit planar separation, or -1 when either unit
// is gone. It is a test measurement, not a contract; the game's own range test
// is [06 §3.3]'s squared form.
func planarGap(a, b *units.Unit) int64 {
	if a == nil || b == nil || !a.Alive {
		return -1
	}
	dx := int64(a.X.Raw()>>16) - int64(b.X.Raw()>>16)
	dz := int64(a.Z.Raw()>>16) - int64(b.Z.Raw()>>16)
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	// Chebyshev-free: the larger axis bounds the true distance from below and
	// the sum bounds it from above; use the true squared distance instead.
	d2 := dx*dx + dz*dz
	var r int64
	for r*r <= d2 {
		r++
	}
	return r - 1
}
