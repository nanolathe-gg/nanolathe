package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestRetailRetaliatorStockpilesAndFiresANuke drives one BUILDWEAPON round to
// completion on real content, through the same session command the
// MAKENUKE/MAKEANTI gadget dispatches, and then launches it.
//
// The contract it locks is [06 §11.1] / [05 "Stockpile production"]: progress
// advances by five per visit capped at the weapon's compiled reload time, the
// admitted energy and metal for a visit are the differences of two
// independently truncated cumulative proportional costs, and completion
// increments the slot's byte-sized completed-round remainder and decrements the
// queue node's count. The two truncated series telescope, so a whole round
// costs the weapon's `energypershot` and `metalpershot` exactly — which is what
// this asserts against the player's cumulative consumption counters. Launch
// then requires the `stockpile` flag and a nonzero remainder, and decrements it
// without any per-launch resource debit.
//
// The Retaliator was the play-test report: it could not build nukes at all,
// because the command page that carries its one MAKENUKE toy was suppressed by
// the publication boundary's FBI `Builder` gate. This probe is the sim half of
// that chain; TestRetailSiloPageCarriesTheStockpileToy in cmd/nanolathe is the
// presentation half.
func TestRetailRetaliatorStockpilesAndFiresANuke(t *testing.T) {
	f := loadRetailFixture(t)
	s := f.session(t)
	stepRetail(s, 2)
	if s.State != StateBattle {
		t.Fatalf("session state %v, want battle", s.State)
	}

	siloX := numeric.Fixed(int64(1000) << 16)
	siloZ := numeric.Fixed(int64(1000) << 16)
	silo := placeCompleteRetailUnit(t, s, retailNukeSilo, 0, siloX, siloZ)
	slot := silo.SlotAt(0)
	if slot == nil || slot.Weapon == nil || !slot.Weapon.Stockpile {
		t.Skipf("authored %s slot 0 carries no stockpile weapon", retailNukeSilo)
	}
	weapon := slot.Weapon
	reload := weapon.ReloadTime
	if reload <= 0 {
		t.Skipf("authored %s has reload time %d", weapon.CanonicalKey, reload)
	}

	// Selecting the Retaliator must put it on the committed command page: that
	// page is what carries its authored ARMSILO1.GUI to the side rail, and the
	// MAKENUKE toy lives on it [07 R-HUD-03 §6]. The FBI `Builder` gate that
	// used to stand at the publication boundary published no page at all for a
	// silo, so the toy was unreachable however well the sim below worked.
	// A command page is a single-selection surface, so the battle's own opening
	// selection has to come off first [07 §9].
	for _, u := range s.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == 0 {
			u.Flags &^= 0x10
		}
	}
	silo.Flags |= 0x10 // the selection bit [07 §9]
	s.Step(3)
	if committed := s.Snapshot.Current(); committed == nil {
		t.Fatal("no committed frame after the selection step")
	} else if committed.CommandPage.Builder != silo.Handle || committed.CommandPage.PageCount < 2 {
		t.Fatalf("a selected %s published command page %+v, want it named with its authored page count [07 R-HUD-03 §6]",
			retailNukeSilo, committed.CommandPage)
	}

	// The alias is the button's whole behavior: build type zero, count one
	// [06 §11.1]. This is exactly what DispatchStockpileGadget enqueues.
	if err := s.EnqueueHumanCommand(HumanCommand{
		Kind: HumanStockpile, Stockpile: HumanStockpileCommand{Unit: silo.Handle},
	}); err != nil {
		t.Fatalf("enqueue stockpile round: %v", err)
	}

	player := &s.Econ.Players[0]
	beforeEnergy := player.TotalConsumed[economy.Energy]
	beforeMetal := player.TotalConsumed[economy.Metal]

	// A nuke's authored cost outruns any starting stock, and an unpaid visit is
	// held for ten ticks rather than advanced [06 §11.1]. Keeping the player
	// solvent isolates the production arithmetic from the economy's own
	// scarcity, which is not what this probe is about.
	solvent := func() {
		player.Capacity[economy.Energy] = 1e9
		player.Capacity[economy.Metal] = 1e9
		player.Stock[economy.Energy] = 1e6
		player.Stock[economy.Metal] = 1e6
	}

	// Progress climbs five per visit on a five-tick retry, so a full round needs
	// reload ticks plus the retry slack. The margin is generous rather than
	// tight: the cadence, not the wall-clock bound, is what §11.1 fixes.
	deadline := int(reload) + 2*int(combatStockpileVisitSlack)
	tick := 4
	for ; tick < deadline && slot.Ammo == 0; tick++ {
		solvent()
		s.Step(int32(tick))
	}
	if slot.Ammo != 1 {
		q := orders.QueueForUnit(silo)
		progress := uint32(0)
		if q != nil && q.LenSecondary() > 0 {
			progress = q.Secondary()[0].Param3
		}
		t.Fatalf("%s completed %d rounds after %d ticks, want 1 (progress %d of reload %d) [06 §11.1]",
			retailNukeSilo, slot.Ammo, tick, progress, reload)
	}
	// Completion decrements the node's signed count; the queue driver unlinks a
	// node whose count has reached zero [06 §11.1].
	if q := orders.QueueForUnit(silo); q != nil && q.LenSecondary() != 0 {
		t.Fatalf("secondary queue still holds %d nodes after the round completed [06 §11.1]", q.LenSecondary())
	}

	// The held byte the MAKENUKE toy prints is published on the command page
	// [07 R-P0-11 §2]; without it the button can never show that a round is
	// ready.
	if committed := s.Snapshot.Current(); committed == nil || committed.CommandPage.Stockpile != 1 {
		t.Fatalf("committed command page publishes stockpile %v, want the one held round [07 R-P0-11 §2]", committed.CommandPage.Stockpile)
	}

	// The cumulative counters are written by the player's periodic settlement
	// pass, not by admission, so the last batch of admitted visits is still
	// outstanding at the tick the round completed [05 "Authoritative settlement
	// order"]. Run past one more settlement deadline before reading them.
	for end := tick + settlementDrainTicks; tick < end; tick++ {
		solvent()
		s.Step(int32(tick))
	}

	// The telescoping sum of the two truncated series is the weapon's whole
	// per-shot cost [06 §11.1]. Player 0 owns only its commander besides the
	// silo, and an idle commander consumes nothing, so the counters carry the
	// round alone.
	gotEnergy := player.TotalConsumed[economy.Energy] - beforeEnergy
	gotMetal := player.TotalConsumed[economy.Metal] - beforeMetal
	wantEnergy := float64(weapon.EnergyPerShot)
	wantMetal := float64(weapon.MetalPerShot)
	if gotEnergy != wantEnergy || gotMetal != wantMetal {
		t.Fatalf("one round consumed energy %v metal %v, want the weapon's per-shot %v / %v [06 §11.1]",
			gotEnergy, gotMetal, wantEnergy, wantMetal)
	}

	// Launch: the stockpile gate is the flag plus a nonzero remainder, and a
	// successful spawn decrements the byte with no per-launch debit [06 §11.1].
	slot.Reload = 0
	slot.Target = units.Target{
		Kind: units.TargetGround,
		X:    siloX + numeric.Fixed(int64(600)<<16),
		Z:    siloZ + numeric.Fixed(int64(600)<<16),
	}
	launchEnergy := player.TotalConsumed[economy.Energy]
	launchMetal := player.TotalConsumed[economy.Metal]
	launched := 0
	for end := tick + stockpileLaunchWindowTicks; tick < end && launched == 0; tick++ {
		solvent()
		s.Step(int32(tick))
		launched, _, _ = projectilesOfWeapon(s, weapon.CanonicalKey)
	}
	if launched == 0 {
		t.Fatalf("the stockpiled %s never launched: slot ammo %d, target kind %v [06 §11.1]",
			weapon.CanonicalKey, slot.Ammo, slot.Target.Kind)
	}
	if slot.Ammo != 0 {
		t.Fatalf("slot remainder is %d after the launch, want the decremented 0 [06 §11.1]", slot.Ammo)
	}
	if player.TotalConsumed[economy.Energy] != launchEnergy || player.TotalConsumed[economy.Metal] != launchMetal {
		t.Fatalf("the launch debited energy %v metal %v; stockpile launch bypasses the per-launch debit [06 §11.1]",
			player.TotalConsumed[economy.Energy]-launchEnergy, player.TotalConsumed[economy.Metal]-launchMetal)
	}
}

// These size the probe's tick budgets only. combatStockpileVisitSlack is the
// accepted-but-incomplete retry of [06 §11.1]; settlementDrainTicks covers one
// player settlement period; stockpileLaunchWindowTicks is slack for the
// round-robin autonomous weapon scan that carries the armed slot to its fire
// gate [06 §3.2].
const (
	combatStockpileVisitSlack  = 5
	settlementDrainTicks       = 40
	stockpileLaunchWindowTicks = 600
)
