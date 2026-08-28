package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

func TestStopDispatchHasNoContextualOriginOrder(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)
	b.handleHudOrderButton("stop")
	applyPendingBattleCommands(b)
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
	replaceSelectionForTest(t, b, attacker)
	b.orderSelected(3, 300, 300, false)
	applyPendingBattleCommands(b)
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
	replaceSelectionForTest(t, b, u)
	b.toggleOnOffSelected(false)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanActivation || pending[0].Activation.Unit != u.Handle || !pending[0].Activation.Activate {
		t.Fatalf("activation command = %+v, want Activate for unit %d", pending, u.Handle)
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
	replaceSelectionForTest(t, b, attacker)
	target := placeUnit(b, "armsolar", numeric.Fixed(16*65536), numeric.Fixed(16*65536))
	target.Owner = 1
	// Foreign targets require committed coverage; the picker must not inspect
	// the live pool as a visibility bypass [03 §3.2][07 §8].
	b.sess.Vis = visibility.New(b.sess.World, 0)
	b.sess.Vis.SetLocal(0)
	applyPendingBattleCommands(b)
	sx, sy := screenPos(b.cam, target)
	b.orderSelected(3, sx, sy, false)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Target != target.Handle {
		t.Fatalf("queued order=%+v, want target handle %d", pending, target.Handle)
	}
	if pending[0].Order.Position.X == 0 && pending[0].Order.Position.Z == 0 {
		t.Fatal("typed order lost its resolved world position")
	}
	// The command contains no mutable target pointer. Move the target before
	// the session input phase and verify it resolves the then-current position.
	target.X += numeric.Fixed(3 * 65536)
	target.Z += numeric.Fixed(2 * 65536)
	applyPendingBattleCommands(b)
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
	b.battleState().Input.BuildDef = "armsolar"
	b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ = 2, 2
	b.battleState().Input.BuildOK = true
	b.battleState().Input.BuildMX, b.battleState().Input.BuildMY = 100, 110
	b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ = 4, 5
	b.battleState().Input.BuildSiteH = 7
	b.battleState().Input.BuildSticky = true
	b.battleState().Input.Latch = input.LatchMobileBuild
	b.CancelPlacement()
	if b.battleState().Input.BuildDef != "" || b.battleState().Input.BuildFootX != 0 || b.battleState().Input.BuildFootZ != 0 || b.battleState().Input.BuildOK || b.battleState().Input.BuildMX != 0 || b.battleState().Input.BuildMY != 0 || b.battleState().Input.BuildCellX != 0 || b.battleState().Input.BuildCellZ != 0 || b.battleState().Input.BuildSiteH != 0 || b.battleState().Input.BuildSticky || b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("placement cancellation left state: def=%q foot=%d,%d ok=%t mouse=%d,%d cell=%d,%d h=%d sticky=%t latch=%v", b.battleState().Input.BuildDef, b.battleState().Input.BuildFootX, b.battleState().Input.BuildFootZ, b.battleState().Input.BuildOK, b.battleState().Input.BuildMX, b.battleState().Input.BuildMY, b.battleState().Input.BuildCellX, b.battleState().Input.BuildCellZ, b.battleState().Input.BuildSiteH, b.battleState().Input.BuildSticky, b.battleState().Input.Latch)
	}
}
