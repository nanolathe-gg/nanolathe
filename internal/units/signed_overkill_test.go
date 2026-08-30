package units

// UNIT-05 signed overkill: the damage receiver stores the signed health
// result — no clamp at zero — because the local Killed severity contract
// consumes the signed health, severity = ((−health·100)/maxHealth +
// priorSample)/2 with the unsigned divide, clamped 1..100 [04 §5.1]. Death
// marking still triggers on a non-positive result at the caller's Destroy
// and the slot-end death latch [06 §9.1][04 §5.1]. These tests lock the
// STORAGE contract: the signed value survives through FinalizeDeath and
// teardown cleanup. The synchronous Killed query consumer lands with a separate COB
// round; severity here is asserted through the established formula in
// cob.KilledSeverity [04 §5.1].

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func overkillWorld(t *testing.T) (*World, pool.Handle) {
	t.Helper()
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "overkill", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := world.Unit(h)
	if u == nil || u.Health != 100 {
		t.Fatalf("fixture unit health = %v/%d, want 100", u, u.MaxHealth)
	}
	return world, h
}

func TestApplyDamageExactZeroDeathRetainsZeroThroughFinalizeDeath(t *testing.T) {
	world, h := overkillWorld(t)
	u := world.Unit(h)
	if !world.ApplyDamage(h, 100) {
		t.Fatal("lethal damage rejected")
	}
	if u.Health != 0 {
		t.Fatalf("exact-zero health = %d, want 0", u.Health)
	}
	world.Destroy(h, DeathKilled)
	if !u.Dying {
		t.Fatal("lethal damage did not latch the death mark via Destroy")
	}
	if res := world.FinalizeDeath(h, 7); !res.Freed {
		t.Fatal("death was not finalized")
	}
	if u.Health != 0 {
		t.Fatalf("health after FinalizeDeath = %d, want 0 retained", u.Health)
	}
}

func TestApplyDamageSmallOverkillRetainsSignedValueThroughFinalizeDeath(t *testing.T) {
	world, h := overkillWorld(t)
	u := world.Unit(h)
	if !world.ApplyDamage(h, 130) {
		t.Fatal("overkill damage rejected")
	}
	if u.Health != -30 {
		t.Fatalf("overkill health = %d, want -30 stored signed", u.Health)
	}
	world.Destroy(h, DeathKilled)
	if res := world.FinalizeDeath(h, 7); !res.Freed {
		t.Fatal("death was not finalized")
	}
	if u.Health != -30 {
		t.Fatalf("health after FinalizeDeath = %d, want -30 retained for the severity input [04 §5.1]", u.Health)
	}
	// The established severity arithmetic on the retained signed input:
	// ((30·100)/100 + prior 0)/2 = 15 [04 §5.1]. Exact zero with the same
	// prior sample clamps to the severity floor of 1, so the overkill
	// intermediate is observable through the consumer's formula.
	if got := cob.KilledSeverity(u.Health, u.MaxHealth, u.PriorSample); got != 15 {
		t.Fatalf("KilledSeverity(-30,100,0) = %d, want 15 [04 §5.1]", got)
	}
	if got := cob.KilledSeverity(0, u.MaxHealth, 0); got != 1 {
		t.Fatalf("KilledSeverity(0,100,0) = %d, want the 1..100 clamp floor [04 §5.1]", got)
	}
}

func TestApplyDamageLargeOverkillRetainsSignedValueThroughTeardownCleanup(t *testing.T) {
	world, h := overkillWorld(t)
	u := world.Unit(h)
	if !world.ApplyDamage(h, 2500) {
		t.Fatal("large overkill damage rejected")
	}
	if u.Health != -2400 {
		t.Fatalf("large overkill health = %d, want -2400 stored signed", u.Health)
	}
	world.Destroy(h, DeathKilled)
	if res := world.FinalizeDeath(h, 7); !res.Freed {
		t.Fatal("death was not finalized")
	}
	world.TeardownCleanup()
	if u.Health != -2400 {
		t.Fatalf("health after FinalizeDeath+teardown cleanup = %d, want -2400 retained", u.Health)
	}
	if world.Unit(h) != nil {
		t.Fatal("slot should be free after teardown cleanup")
	}
}

// TestSlotEndDeathLatchStillTriggersOnNonPositive locks that removing the
// zero clamp did not remove the death trigger: the generic slot-end latch
// marks a non-positive-health unit via Destroy [06 §9.1][04 §2.4].
func TestSlotEndDeathLatchStillTriggersOnNonPositive(t *testing.T) {
	world, h := overkillWorld(t)
	u := world.Unit(h)
	if !world.ApplyDamage(h, 130) {
		t.Fatal("overkill damage rejected")
	}
	if u.Dying {
		t.Fatal("damage receiver must not latch death itself; the caller's Destroy does")
	}
	world.slotEndDeathHandling(u, 1)
	if !u.Dying {
		t.Fatal("slot-end death latch did not mark the non-positive-health unit")
	}
	// A still-positive unit is not marked.
	world2, h2 := overkillWorld(t)
	u2 := world2.Unit(h2)
	if !world2.ApplyDamage(h2, 40) {
		t.Fatal("non-lethal damage rejected")
	}
	world2.slotEndDeathHandling(u2, 1)
	if u2.Dying {
		t.Fatal("non-lethal damage marked the unit dying")
	}
	if u2.Health != 60 {
		t.Fatalf("non-lethal health = %d, want 60", u2.Health)
	}
}
