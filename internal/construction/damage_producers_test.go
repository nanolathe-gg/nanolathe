package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// bindConstructionCombat gives a construction fixture the same real combat
// receiver that session composition binds in a battle. Producer tests must not
// replace the shared packet path with a local health or death substitute.
func bindConstructionCombat(s *Service) {
	s.Combat = &combat.Service{
		ControlByte: func(uint8) uint8 { return combat.ControlByteHuman },
	}
}

// The fixed kind-9 nominal bypasses armor at 30000 but still passes through
// defender veterancy. This exercises cancel-current's producer rather than the
// combat receiver in isolation [05 "Cancel-current and stop interrupts"][06 §9.2].
func TestCancelCurrentKindNineVeteranCanSurvive(t *testing.T) {
	factoryDef := newFactoryDef("cancel_factory", 1, 1, 30)
	productDef := newProductDef("cancel_product", 1, 1, 29000, 100)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		factoryDef.CanonicalKey: factoryDef,
		productDef.CanonicalKey: productDef,
	}}
	w := newConstructionFixtureWorld(4, cat)
	fh, _ := w.Create(factoryDef, 0, 0, 0, 0)
	ph, _ := w.Create(productDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Remaining, product.MaxHealth, product.Health, product.Kills = 0.5, 29000, 29000, 25
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup(FactoryBuildOrder), orders.Node{BuildDefKey: productDef.CanonicalKey, Param2: 1, Phase: uint8(State3)})
	q.Primary()[0].BindTarget(ph) // stage the runtime product relink [04 R-ORD-01 §6]
	svc := NewService(nil, cat, w, nil)
	bindConstructionCombat(svc)

	svc.handleCancelCurrent(factory, q.Primary()[0], 7)

	if product.Dying || product.Health != 5000 {
		t.Fatalf("fixed kind-9 result = dying %t health %d, want survivor at 5000 [06 §9.2]", product.Dying, product.Health)
	}
	if product.LastDamageCause != Kind9Cause || product.EngagementTarget != factory.Handle || uint8(product.BlinkSuppress) != 240 {
		t.Fatalf("shared receiver effects = cause %d attacker %d flash %d", product.LastDamageCause, product.EngagementTarget, uint8(product.BlinkSuppress))
	}
}

// Reclaim's pulse is a construction producer, but its armor, defender-veteran,
// flash, reaction, and provenance behavior belong to the shared intake
// [05 R-WORK-01 §4][06 §9.1][06 §9.2].
func TestUnitReclaimPulseUsesCommonDamageIntake(t *testing.T) {
	s, builder, target, node := reclaimFixture(t, 100, 10)
	builder.Def.WorkerTime = 300 // unit reclaim nominal = 15
	target.Armored = true
	target.Kills = 25
	target.Def.DamageModifier = 32768 // armor halves before veteran reduction
	target.LastDamageSide = target.Owner
	target.LastDamageCause = uint8(combat.CauseCargo)
	node.Phase = 5
	node.Param1, node.Param2 = 15, 16 // fire on this visit

	var reactionCount int
	var sawFlash, sawSide, sawCause uint8
	s.Combat.Reaction = &combat.ReactionSeams{ObserverNotice: func(v *units.Unit) {
		reactionCount++
		sawFlash, sawSide, sawCause = uint8(v.BlinkSuppress), v.LastDamageSide, v.LastDamageCause
	}}

	s.stepUnitReclaim(builder, node, 61)

	// armor: 15 -> 7 at one-half; defender tier five: 7 -> 5 at four-fifths.
	if target.Health != 95 {
		t.Fatalf("reclaim health=%d, want 95 after armored veteran intake [06 §9.2]", target.Health)
	}
	if reactionCount != 1 || sawFlash != 240 || sawSide != target.Owner || sawCause != uint8(combat.CauseCargo) {
		t.Fatalf("reaction saw count/flash/prior-side/prior-cause %d/%d/%d/%d, want 1/240/%d/%d [06 §9.1]",
			reactionCount, sawFlash, sawSide, sawCause, target.Owner, combat.CauseCargo)
	}
	if target.LastDamageCause != uint8(combat.CauseReclaim) || target.LastDamageSide != builder.Owner || target.EngagementTarget != builder.Handle {
		t.Fatalf("reclaim provenance cause/side/attacker=%d/%d/%d, want %d/%d/%d [06 §9.1]",
			target.LastDamageCause, target.LastDamageSide, target.EngagementTarget,
			combat.CauseReclaim, builder.Owner, builder.Handle)
	}
}

// Reverse passes the current GetBuilt visit into the shared terminal packet.
// The reaction seam sees the reverse refund and health floor before intake
// rewrites provenance or subtracts the packet [05 R-WORK-01 §1][06 §9.1].
func TestGetBuiltReverseDeliversCurrentTickAfterRefund(t *testing.T) {
	def := newProductDef("reverse_delivery", 1, 1, 40, 100)
	def.BuildCostEnergy = 0 // reverse quantum is -Inf and clamps in one visit
	def.BuildCostMetal = 200
	cat := exitCatalog(def)
	s, w := exitService(t, exitTerrain(16, 16), cat)
	bindConstructionCombat(s)

	h, err := w.Create(def, 3, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create frame: %v", err)
	}
	frame := w.Unit(h)
	frame.Remaining, frame.MaxHealth, frame.Health = 0.25, 100, 75
	frame.LastDamageSide, frame.LastDamageCause = 7, uint8(combat.CauseCargo)
	q := s.queueForUnit(frame)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2)})

	var sawRefund float32
	var sawHealth int32
	var sawSide, sawCause uint8
	s.Combat.Reaction = &combat.ReactionSeams{ObserverNotice: func(v *units.Unit) {
		sawRefund = (*s.Economy.UnitBuckets(frame.Handle))[economy.Metal].Production
		sawHealth, sawSide, sawCause = v.Health, v.LastDamageSide, v.LastDamageCause
	}}
	var flash combat.Event
	s.Combat.Events = func(ev combat.Event) {
		if ev.Kind == combat.EventDamageFlash {
			flash = ev
		}
	}

	const tick = 73
	if code := s.handleGetBuiltOrder(frame, q.Primary()[0], 0, tick); code != 2 {
		t.Fatalf("reverse visit code=%d, want ordinary hold", code)
	}
	if sawRefund != 150 || sawHealth != 0 || sawSide != 7 || sawCause != uint8(combat.CauseCargo) {
		t.Fatalf("pre-intake refund/health/side/cause=%v/%d/%d/%d, want 150/0/7/%d [05 R-WORK-01 §1][06 §9.1]",
			sawRefund, sawHealth, sawSide, sawCause, combat.CauseCargo)
	}
	if flash.Tick != tick || flash.Source != frame.Handle || flash.Target != frame.Handle {
		t.Fatalf("reverse flash tick/source/target=%d/%d/%d, want %d/%d/%d", flash.Tick, flash.Source, flash.Target, tick, frame.Handle, frame.Handle)
	}
}
