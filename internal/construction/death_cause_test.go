// The two death causes this package produces, both from [06 §12.1]'s producer
// list: cause 9 on the construction refund paths and cause 4 on the capture
// victim's old record. The finalizer selects the corpse chain, the explosion
// and the credit branch from the recorded damage-kind byte, so an unstamped
// death of either kind reads back as ordinary weapon damage.
package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestCancelCurrentStampsCauseNine is the other of [06 §12.1]'s "two refund
// paths". Cancel-current's step 4 sends "the ordinary kill packet — kind-9
// damage of exactly 30000 through the normal death flow", and cause-9 deaths
// "skip the killed-severity script query entirely: severity is zero, so there
// is no explosion and no corpse — the product simply vanishes"
// [05 "Cancellation boundaries"]. The finalizer reads that skip off the kind
// byte, so an unstamped cancel left a wreck where retail leaves nothing.
func TestCancelCurrentStampsCauseNine(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("causeninefac", 1, 1, 30)
	prodDef := newProductDef("causenineprod", 1, 1, 1, 100)
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(8, cat)
	fh, _ := w.Create(facDef, 2, 0, 0, 0)
	ph, _ := w.Create(prodDef, 2, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Remaining, product.MaxHealth, product.Health = 0.5, 100, 30
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 2, Phase: uint8(State3)})
	q.Primary()[0].BindTarget(ph) // stage the runtime product relink [04 R-ORD-01 §6]
	svc := NewService(nil, cat, w, nil)
	bindConstructionCombat(svc)

	svc.handleCancelCurrent(factory, q.Primary()[0], 4)

	if product.LastDamageCause != Kind9Cause {
		t.Fatalf("cancelled product died with kind %d, want 9 [06 §12.1][05 \"Cancellation boundaries\"]", product.LastDamageCause)
	}
	if product.LastDamageSide != product.Owner {
		t.Fatalf("attacker side = %d, want the shared owner %d [06 §9.1]", product.LastDamageSide, product.Owner)
	}
	// Corrected (2026-09-02): this asserted the product as its own attacker.
	// The packet cancel-current sends is `damage(attacker = the factory, victim
	// = the product, 30000, kind 9, flag 0)`; the self form belongs to the
	// shared step's reverse arm alone
	// [05 "Cancel-current and stop interrupts"][05 R-WORK-01 §1].
	if product.EngagementTarget != factory.Handle {
		t.Fatalf("recorded attacker = %d, want the factory %d [04 R-UNIT-06 §5]", product.EngagementTarget, factory.Handle)
	}
}

// TestDecayClampStampsCauseNine locks the reverse arm's terminator packet:
// `selfKill(target, target, 30000, kind 9)` [05 R-WORK-01 §1]. Victim and
// attacker are the same unit, so the side snapshot the ordinary intake stores
// beside the kind byte is the frame's own owner [06 §9.1] step 4.
func TestDecayClampStampsCauseNine(t *testing.T) {
	def := newProductDef("causeninefr", 1, 1, 40, 100)
	def.BuildCostEnergy = 11 // one whole fraction per decay visit: clamps at once
	cat := exitCatalog(def)
	terrain := exitTerrain(16, 16)
	svc, w := exitService(t, terrain, cat)
	bindConstructionCombat(svc)

	h, err := w.Create(def, 3, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create frame: %v", err)
	}
	frame := w.Unit(h)
	frame.Remaining = 0.5
	frame.MaxHealth, frame.Health = 100, 50
	q := svc.queueForUnit(frame)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2)})

	if code := svc.handleGetBuiltOrder(frame, q.Primary()[0], 0, 40); code != 2 {
		t.Fatalf("decay visit code=%d, want the ordinary hold", code)
	}
	if frame.LastDamageCause != Kind9Cause {
		t.Fatalf("decayed frame died with kind %d, want 9 [06 §12.1][05 R-WORK-01 §1]", frame.LastDamageCause)
	}
	if frame.LastDamageSide != frame.Owner {
		t.Fatalf("attacker side = %d, want the frame's own owner %d: the packet is a self-kill [05 R-WORK-01 §1]",
			frame.LastDamageSide, frame.Owner)
	}
	if frame.EngagementTarget != frame.Handle {
		t.Fatalf("recorded attacker = %d, want the frame itself %d [04 R-UNIT-06 §5]", frame.EngagementTarget, frame.Handle)
	}
	if frame.DeathCause != units.DeathKilled || !frame.Dying {
		t.Fatalf("death label %v dying=%v, want a marked DeathKilled", frame.DeathCause, frame.Dying)
	}
	// The label and the kind must agree through the save-boundary derivation,
	// which is the only other reader of this pair [08 R-SAVE-02 §6].
	if units.DeathCauseFromKind(frame.LastDamageCause) != frame.DeathCause {
		t.Fatalf("label %v disagrees with kind %d", frame.DeathCause, frame.LastDamageCause)
	}
}

// TestCaptureVictimStampsCauseFourWithNoAttacker locks the ownership
// transfer's kill packet: [06 §12.1] gives cause 4 as "capture/owner
// replacement: packet builder invoked with a NULL attacker at both of its call
// sites. Credit branch: none." A captor that ended up in the side snapshot
// would be credited for a kill it never made.
func TestCaptureVictimStampsCauseFourWithNoAttacker(t *testing.T) {
	def := newProductDef("capturevictim", 1, 1, 40, 100)
	cat := exitCatalog(def)
	terrain := exitTerrain(16, 16)
	svc, w := exitService(t, terrain, cat)

	h, err := w.Create(def, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	victim := w.Unit(h)
	victim.MaxHealth, victim.Health = 100, 100
	victim.LastDamageSide = 0 // an earlier hit from player 0, before the capture

	repl, ok := svc.TransferOwnership(victim, 1)
	if !ok || repl == nil {
		t.Fatal("ownership transfer refused")
	}
	if !victim.Dying {
		t.Fatal("the old record was not killed by the transfer [05 \"Capture\"]")
	}
	if victim.LastDamageCause != CaptureDeathCause {
		t.Fatalf("captured record died with kind %d, want 4 [06 §12.1]", victim.LastDamageCause)
	}
	if victim.LastDamageSide != units.NeutralAttackerSide {
		t.Fatalf("attacker side = %d, want the neutral side %d: the cause-4 packet has a null attacker [06 §12.1]",
			victim.LastDamageSide, units.NeutralAttackerSide)
	}
	if victim.LastDamageSide == repl.Owner {
		t.Fatal("the captor was recorded as the attacker; cause 4 credits nobody [06 §12.1]")
	}
	if victim.EngagementTarget != 0 {
		t.Fatalf("recorded attacker = %d, want null [04 R-UNIT-06 §5]", victim.EngagementTarget)
	}
}
