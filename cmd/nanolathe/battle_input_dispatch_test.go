package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestStopDispatchHasNoContextualOriginOrder(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	u.Flags |= client.SelectionFlag
	b.handleHudOrderButton("stop")
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() != 1 {
		t.Fatalf("stop queued %d nodes, want exactly one", q.LenPrimary())
	}
	n := q.Primary()[0]
	if got := orders.DescriptorFor(n.ID).Name; got != "Stop" {
		t.Fatalf("stop descriptor = %q, want Stop", got)
	}
	if n.Target != 0 || n.GoalX != 0 || n.GoalY != 0 || n.GoalZ != 0 {
		t.Fatalf("stop carried contextual target/goal: target=%d goal=(%d,%d,%d)", n.Target, n.GoalX, n.GoalY, n.GoalZ)
	}
}

func TestAttackGroundUsesResolverRejectSentinel(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	attacker := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	attacker.Def.CanAttack = true
	attacker.Flags |= client.SelectionFlag
	b.orderSelected(3, 300, 300, false)
	q := orders.QueueForUnit(attacker)
	if q != nil && q.LenPrimary() != 0 {
		t.Fatalf("ground attack guessed a descriptor; queue length=%d", q.LenPrimary())
	}
}

func TestActivationCommandUsesEconomyStateNotProxyFlag(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	u.Def.OnOffable = true
	u.Activated = false
	u.Flags |= 0x1000 // legacy proxy bit must not control the command.
	u.Flags |= client.SelectionFlag
	var got battleCommand
	b.commandDispatchFn = func(cmd battleCommand) error {
		got = cmd
		return nil
	}
	b.toggleOnOffSelected(false)
	if got.Kind != battleCommandActivation || got.Activation.Unit != u.Handle || !got.Activation.Activate {
		t.Fatalf("activation command = %+v, want Activate for unit %d", got, u.Handle)
	}
	if u.Flags&0x1000 == 0 {
		t.Fatal("activation changed the unrelated proxy flag")
	}
	if u.Activated {
		t.Fatal("activation input directly flipped Unit.Activated")
	}
}

func TestFeatureIdentityDistinguishesCorpseFromReclaimable(t *testing.T) {
	cat := testCatalogON05()
	corpse := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armrecl_dead2"}, Reclaimable: true}
	cat.Features[corpse.CanonicalKey] = corpse
	unknown := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "ghost_dead2"}, Reclaimable: true}
	cat.Features[unknown.CanonicalKey] = unknown
	b := newTestBattle(cat, testWorldON05(10, 10))
	ordinary := cat.Features[content.CanonicalKey("armrock")]
	if b.isCorpseFeature(ordinary) {
		t.Fatal("ordinary reclaimable feature classified as wreck")
	}
	if !b.isCorpseFeature(corpse) {
		t.Fatal("suffixed corpse feature did not resolve its truncated unit key")
	}
	if b.isCorpseFeature(unknown) {
		t.Fatal("unknown corpse prefix classified as wreck")
	}
}

func TestTypedOrderCommandResolvesTargetHandleAtApplication(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	attacker := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	attacker.Def.CanAttack = true
	attacker.Flags |= client.SelectionFlag
	target := placeUnit(b, "armsolar", numeric.Fixed(16*65536), numeric.Fixed(16*65536))
	target.Owner = 1
	sx, sy := screenPos(b.cam, target)
	var captured battleCommand
	b.commandDispatchFn = func(cmd battleCommand) error {
		captured = cmd
		return nil
	}
	b.orderSelected(3, sx, sy, false)
	if captured.Kind != battleCommandOrder || captured.Order.Target != target.Handle {
		t.Fatalf("captured order=%+v, want target handle %d", captured, target.Handle)
	}
	if captured.Order.Position.X == 0 && captured.Order.Position.Z == 0 {
		t.Fatal("typed order lost its resolved world position")
	}
	// The command contains no mutable target pointer. Move the target before
	// application and verify the fallback reads its then-current position.
	target.X += numeric.Fixed(3 * 65536)
	target.Z += numeric.Fixed(2 * 65536)
	b.commandDispatchFn = nil
	b.dispatchOrderFallback(captured.Order)
	q := orders.QueueForUnit(attacker)
	if q == nil || q.LenPrimary() != 1 {
		t.Fatalf("target order queue length=%d, want 1", q.LenPrimary())
	}
	n := q.Primary()[0]
	if n.GoalX != target.X || n.GoalY != target.Y || n.GoalZ != target.Z {
		t.Fatalf("target goal=(%d,%d,%d), want current target=(%d,%d,%d)", n.GoalX, n.GoalY, n.GoalZ, target.X, target.Y, target.Z)
	}
}

func TestBuildPageUsesCompiledCatalogDefinitionIdentity(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	u := placeUnit(b, "armfac", numeric.Fixed(4*65536), numeric.Fixed(4*65536))
	want, ok := b.cat.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || want == 0 {
		t.Fatalf("catalog index missing for %q", u.Def.CanonicalKey)
	}
	if got := b.catalogDefID(u); uint32(got) != want {
		t.Fatalf("page definition identity=%d, want compiled catalog index %d", got, want)
	}
	if got := b.catalogDefID(nil); got != 0 {
		t.Fatalf("nil builder identity=%d, want null sentinel", got)
	}
}

func TestPlacementCancellationClearsAllPendingState(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.buildDef = "armsolar"
	b.buildFootX, b.buildFootZ = 2, 2
	b.buildOK = true
	b.buildMX, b.buildMY = 100, 110
	b.buildCellX, b.buildCellZ = 4, 5
	b.buildSiteH = 7
	b.buildSticky = true
	b.latch = input.LatchMobileBuild
	b.CancelPlacement()
	if b.buildDef != "" || b.buildFootX != 0 || b.buildFootZ != 0 || b.buildOK || b.buildMX != 0 || b.buildMY != 0 || b.buildCellX != 0 || b.buildCellZ != 0 || b.buildSiteH != 0 || b.buildSticky || b.latch != input.LatchNormal {
		t.Fatalf("placement cancellation left state: def=%q foot=%d,%d ok=%t mouse=%d,%d cell=%d,%d h=%d sticky=%t latch=%v", b.buildDef, b.buildFootX, b.buildFootZ, b.buildOK, b.buildMX, b.buildMY, b.buildCellX, b.buildCellZ, b.buildSiteH, b.buildSticky, b.latch)
	}
}
