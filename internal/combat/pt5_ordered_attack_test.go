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

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestPT5_OrderedUnitsStillFire reproduces the play-test screenshot: a group of
// player units that have been given an order are standing on top of a hostile
// unit. They must repeatedly fire and damage it.
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

	step(30)

	// Park a handful of the shooters on the target, which is the
	// geometry the screenshot shows; the chase geometry that would walk them
	// there is an open question in the order layer, not this package's.
	//
	// The park has to go through the mover's own state, not just the unit
	// record: the integration step writes `u.X`/`u.Z` back from the collision
	// state on every tick [04 §8.2], so a park written only onto the unit is
	// undone by the next `Step` before a single shot is attempted. Traced
	// before WU-19-73 and after, that is exactly what happened — the "parked"
	// shooters were a hundred cells away one tick later, and whether the target
	// died was down to how the ordinary chase happened to fall out. Writing the
	// collision and steer positions too makes the geometry this test names
	// actually hold.
	parked := 0
	var parkedHandles []pool.Handle
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Owner != 0 || parked >= 6 || !u.SlotAt(0).IsPopulated() {
			continue
		}
		parkedHandles = append(parkedHandles, u.Handle)
		offset := numeric.FixedFromInt(int64(24 + 16*parked))
		u.X = victim.X.Add(offset)
		u.Z = victim.Z.Add(offset)
		u.Y = victim.Y
		if coll := sess.Movement.Collisions[u.Handle]; coll != nil {
			coll.X, coll.Y, coll.Z = int32(u.X.Raw()), int32(u.Y.Raw()), int32(u.Z.Raw())
			coll.Speed, coll.VX, coll.VZ = 0, 0, 0
		}
		if steer := sess.Movement.Steers[u.Handle]; steer != nil {
			steer.X, steer.Z = int32(u.X.Raw()), int32(u.Z.Raw())
			steer.Speed = 0
		}
		parked++
	}
	if parked == 0 {
		t.Skip("no armed player unit to park")
	}

	// The order is issued AFTER the park, so it is the ORDERED path that is
	// under test. `Attack_Chase` phase 1 runs the shot-admission gate of
	// [06 §3.1] at the parked separation, and on success releases slots 0 and 2
	// and binds slot p1 to the target [04 R-ORD-01 §3]. A slot an order holds
	// has its autonomy bit clear [04 R-UNIT-06 §5 part 3], so nothing below
	// depends on the autonomous scan of [06 §3.2] — neither on its per-unit
	// round-robin cursor nor on which candidate its RNG scoring happens to
	// pick. Issuing the order before the park, as this test did until WU-19-87,
	// left the shooters hundreds of world units out: phase 1's gate refused,
	// the record went to the maneuver and the kill came from whatever the
	// autonomous scan picked instead, which is not what this test names.
	if err := sess.EnqueueHumanCommand(session.HumanCommand{
		Kind: session.HumanOrder,
		Order: session.HumanOrderCommand{
			Handles:  parkedHandles,
			Code:     int(input.LatchAttack),
			Target:   victim.Handle,
			Position: orders.ResolvePos{X: victim.X, Y: victim.Y, Z: victim.Z},
		},
	}); err != nil {
		t.Fatal(err)
	}

	before := victim.Health
	if before <= 0 {
		t.Fatalf("target starts with no health")
	}
	priorEvents := sess.Combat.Events
	orderedImpacts := 0
	orderedVictimImpacts := 0
	orderedDamagePackets := 0
	sess.Combat.Events = func(ev combat.Event) {
		if ev.Kind == combat.EventProjectileImpact && isParkedShooter(ev.Source, parkedHandles) {
			orderedImpacts++
			if ev.Target == victim.Handle {
				orderedVictimImpacts++
			}
		}
		if ev.Kind == combat.EventDamageFlash && ev.Target == victim.Handle && isParkedShooter(ev.Source, parkedHandles) {
			orderedDamagePackets++
		}
		if priorEvents != nil {
			priorEvents(ev)
		}
	}
	orderBound := false
	orderedProjectiles := 0
	victimDamageTicks := 0
	lastHealth := victim.Health
	for i := 0; i < 600 && victim.Alive && victim.Health > 0; i++ {
		scaled += 5
		sess.Step(scaled)
		now := sess.Clock.GlobalTick
		for j := 0; j < sess.Combat.Count(); j++ {
			p := sess.Combat.Records[j]
			if p.CreationTick == now && isParkedShooter(p.Shooter, parkedHandles) {
				orderedProjectiles++
			}
		}
		if victim.Health < lastHealth {
			victimDamageTicks++
		}
		lastHealth = victim.Health
		if !orderBound {
			orderBound = someSlotHoldsTargetUnderOrder(sess.Units.Unit(parkedHandles[0]), victim.Handle)
			for _, h := range parkedHandles[1:] {
				orderBound = orderBound || someSlotHoldsTargetUnderOrder(sess.Units.Unit(h), victim.Handle)
			}
		}
	}
	// The order must be what armed the shot. Without this the test passes on a
	// build where the ordered binding never happens and a bystander's
	// autonomously acquired shot kills the target instead.
	if !orderBound {
		t.Fatalf("%d parked shooters were ordered onto the target and no slot was bound to it under the order [04 R-ORD-01 §3]", parked)
	}
	if orderedProjectiles == 0 || orderedImpacts == 0 {
		t.Fatalf("%d ordered shooters created %d observable projectiles and caused %d impacts: the order did not produce a projectile path",
			parked, orderedProjectiles, orderedImpacts)
	}
	if orderedVictimImpacts < 2 || orderedDamagePackets < 2 || victimDamageTicks < 2 || victim.Health >= before {
		t.Fatalf("ordered fire was not repeated on the victim: launches=%d impacts=%d direct-victim-impacts=%d damage-packets=%d damage-ticks=%d health=%d/%d",
			orderedProjectiles, orderedImpacts, orderedVictimImpacts, orderedDamagePackets, victimDamageTicks, victim.Health, before)
	}
	// The precise remaining health is not an order contract: splash falloff is
	// calculated at each impact [06 §9.3]. This test locks that ordered slots
	// still produce repeated shots and accepted damage.
}

func isParkedShooter(h pool.Handle, parked []pool.Handle) bool {
	for _, candidate := range parked {
		if h == candidate {
			return true
		}
	}
	return false
}

// someSlotHoldsTargetUnderOrder reports whether any of the unit's three slots
// holds `target` with its autonomy bit CLEAR — that is, bound by an order
// rather than by the autonomous scan. The release verb clears the bit as the
// attack handler binds the slot, and the scan requires it set
// [04 R-UNIT-06 §5 part 3][06 §3.2][06 R-WPN-05 §3].
func someSlotHoldsTargetUnderOrder(u *units.Unit, target pool.Handle) bool {
	if u == nil {
		return false
	}
	for i := 0; i < units.NumSlots; i++ {
		s := u.SlotAt(i)
		if s == nil || !s.IsPopulated() {
			continue
		}
		if s.Flags&units.SlotFlagAutonomous != 0 {
			continue // the scan owns this slot, not an order
		}
		if s.Target.Kind == units.TargetUnit && s.Target.Unit == target {
			return true
		}
	}
	return false
}
