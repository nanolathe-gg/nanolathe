package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// TestStopDispatchHasNoContextualOriginOrder guards the HUD stop button
// against dispatching a contextual order at the map origin instead of the
// `Stop` descriptor [04 §3.4][07 §9].
//
// Rewritten by WU-18-0. It used to assert that exactly one record — `Stop`
// with a zero target and goal — was still queued after the applying tick. That
// held only because the pump read its descriptor copy before running the lazy
// handler installers, so the first record of the first pump saw a nil handler
// and parked itself for 30..44 ticks. With the installers run before the
// descriptor is read, `Stop` dispatches on that first visit and completes
// ([04 R-ORD-01 §2] — its row ends in *complete*), so the queue is empty when
// the tick ends and the record can no longer be inspected there. The contract
// the test exists for is checked on the dispatched command instead, plus the
// stronger post-condition that no contextual record was left behind.
func TestStopDispatchHasNoContextualOriginOrder(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)
	b.handleHudOrderButton("stop")
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanStop {
		t.Fatalf("stop dispatched %+v, want exactly one HumanStop command", pending)
	}
	if pending[0].Order.Code != 0 || pending[0].Order.Target != 0 || pending[0].Order.Position != (orders.ResolvePos{}) {
		t.Fatalf("stop carried a contextual order payload: %+v", pending[0].Order)
	}
	applyPendingBattleCommands(b)
	q := orders.QueueForUnit(u)
	if q != nil && q.LenPrimary() != 0 {
		t.Fatalf("stop left %q queued; Stop completes on its first visit", orders.DescriptorFor(q.Primary()[0].ID).Name)
	}
}

func TestAttackGroundUsesResolverRejectSentinel(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	attacker := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	attacker.Def.CanAttack = true
	// armcons resolves no weapon slot, so its state word has no armed bit and
	// code 3 falls straight through the position-only arm to the kamikaze test
	// and rejects [R-ORD-02 §1]. `canattack` alone is not enough.
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
	// Code 3's armed branch runs only when the unit's state word carries the
	// armed bit, which the allocator sets from the definition's weapon links
	// [R-ORD-02 §1]; armcons has none, so the fixture supplies it alongside the
	// forced `canattack`.
	attacker.Flags |= units.ArmedStatus
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
