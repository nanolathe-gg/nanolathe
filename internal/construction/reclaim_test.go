package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

func reclaimFixture(t *testing.T, targetHealth int32, buildDistance int32) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	builderDef := &content.UnitDef{
		UnitName:      "armrec",
		CanReclamate:  true,
		WorkerTime:    30,
		BuildDistance: buildDistance,
		MaxDamage:     100,
	}
	targetDef := &content.UnitDef{
		UnitName:       "corlab",
		BuildCostMetal: 100,
		BuildTime:      300,
		MaxDamage:      100,
	}
	cat := catWithDefs(builderDef, targetDef)
	w := newConstructionFixtureWorld(16, cat)
	bh, err := w.Create(builderDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	th, err := w.Create(targetDef, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	builder, target := w.Unit(bh), w.Unit(th)
	target.Health = targetHealth
	target.MaxHealth = targetDef.MaxDamage
	id := orders.Lookup("ReclaimUnit")
	if id == 0 {
		t.Fatal("ReclaimUnit descriptor missing")
	}
	q := orders.QueueForUnit(builder)
	// Production reclaim records carry the owning unit [04 §3.2]; the fixture
	// mirrors the resolver-created record so cleanup resolves the builder.
	q.Push(id, orders.Node{Owner: builder.Handle, Target: target.Handle, DynamicGate: 0, Deadline: -1})
	node := q.Head()
	node.DynamicGate = 0
	return NewService(nil, cat, w, &economy.Service{}), builder, target, node
}

func reclaimVisits(s *Service, builder *units.Unit, n *orders.Node, ticks ...uint32) {
	for _, tick := range ticks {
		if s.World.Unit(builder.Handle) == nil {
			return
		}
		s.StepUnit(TickContext{Tick: tick, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
		if n == nil || orders.QueueForUnit(builder).Head() != n {
			return
		}
	}
}

func TestUnitReclaimPulseIsAuthoredOnceAndClamped(t *testing.T) {
	builder := &units.Unit{Def: &content.UnitDef{WorkerTime: 30}, Kills: 0}
	target := &units.Unit{MaxHealth: 100, Def: &content.UnitDef{BuildCostMetal: 100}}
	if got := UnitReclaimPulse(builder, target); got != 1 {
		t.Fatalf("pulse=%d want 1", got)
	}
	builder.Def.WorkerTime = 300
	if got := UnitReclaimPulse(builder, target); got != 15 {
		t.Fatalf("pulse=%d want 15", got)
	}
	target.Def.BuildCostMetal = 0
	if got := UnitReclaimPulse(builder, target); got != 150 {
		t.Fatalf("zero-metal clamp pulse=%d want 150", got)
	}
}

// TestUnitReclaimPulseProductWrapsAtThirtyTwoBits locks the width of the pulse
// product [05 R-WORK-01 §4]: "the 32-bit product can overflow silently for large
// `workertime × maxdamage`; because it is then re-read as an *unsigned* 32-bit
// quantity, an overflowed product becomes a very large positive pulse rather
// than a negative one."
//
// Two cases, because they separate three different implementations:
//
//   - a product that passes 2^31 but not 2^32 makes the SIGNED 32-bit value
//     negative while the unsigned re-read is still the true product. A signed
//     re-read would clamp the pulse to 1 here; the unsigned re-read gives the
//     un-wrapped answer, so this case pins the *sign* of the re-read.
//   - a product that passes 2^32 loses its high bits for good, and the pulse
//     collapses to the low 32 bits divided by the same denominator. This is the
//     case a 64-bit product cannot reproduce: it answers 594 where retail
//     answers 21.
//
// The operands are a builder with a large `workertime` and a long kill list
// against a target whose `maxdamage` is in the tens of thousands — the
// Krogoth-class reach the audit that found this identified. The numbers are
// stated, not sampled from the catalog: what is locked is the arithmetic.
func TestUnitReclaimPulseProductWrapsAtThirtyTwoBits(t *testing.T) {
	builder := &units.Unit{Def: &content.UnitDef{WorkerTime: 300}}
	target := &units.Unit{Def: &content.UnitDef{MaxDamage: 45000, BuildCostMetal: 25000}}

	// killsFactor = (50 + 5) / 5 = 11; 300 * 11 * 45000 * 15 = 2_227_500_000,
	// which is above 2^31 and below 2^32. Denominator = 25000 * 300.
	builder.Kills = 50
	if got := UnitReclaimPulse(builder, target); got != 297 {
		t.Fatalf("pulse over 2^31 = %d, want 297: the 32-bit product is re-read UNSIGNED, so it is still the true product here [05 R-WORK-01 §4]", got)
	}

	// killsFactor = (105 + 5) / 5 = 22; 300 * 22 * 45000 * 15 = 4_455_000_000,
	// which is above 2^32. The low 32 bits are 160_032_704, and 160_032_704 /
	// 7_500_000 truncates to 21. A 64-bit product answers 594.
	builder.Kills = 105
	if got := UnitReclaimPulse(builder, target); got != 21 {
		t.Fatalf("pulse over 2^32 = %d, want 21: the product wraps at 32 bits before the widening, so the high bits are gone (a 64-bit product answers 594) [05 R-WORK-01 §4]", got)
	}
}

func TestUnitReclaimBlockedRangeMakesNoProgress(t *testing.T) {
	s, builder, target, node := reclaimFixture(t, 100, 1)
	target.X = numeric.FixedFromInt(2)
	before := target.Health
	s.StepUnit(TickContext{Tick: 0, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
	if target.Health != before || node.Param2 != 0 || node.Param1 != 0 {
		t.Fatalf("out-of-range reclaim mutated target: health %d cadence %d pulse %d", target.Health, node.Param2, node.Param1)
	}
	if node.MoveState != orders.MoveEnRoute {
		t.Fatalf("out-of-range reclaim state=%d want en-route", node.MoveState)
	}
}

func TestUnitReclaimFriendlyTargetIsAdmitted(t *testing.T) {
	s, builder, target, _ := reclaimFixture(t, 100, 10)
	target.Owner = builder.Owner
	q := orders.QueueForUnit(builder)
	s.StepUnit(TickContext{Tick: 0, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
	if target.Health != 100 {
		t.Fatalf("first admitted visit changed target health: %d", target.Health)
	}
	if q.LenPrimary() != 1 || q.Head() == nil || q.Head().Param2 != reclaimCadenceStep {
		t.Fatalf("friendly reclaim visit was not admitted, len=%d node=%+v", q.LenPrimary(), q.Head())
	}
}

// TestUnitReclaimStampsSharedRevealDeadline locks the ReclaimUnit phase-5
// stamp: a qualifying visit writes the builder's shared reveal/cloak
// deadline to tick + 900 outright [04 R-ORD-01 §5][03 R-VIS-01 §6].
func TestUnitReclaimStampsSharedRevealDeadline(t *testing.T) {
	s, builder, target, _ := reclaimFixture(t, 100, 10)
	target.Owner = builder.Owner
	builder.RevealDeadline = 12345 // a prior, larger value must not survive: no maximum is taken.
	const tick = 7
	s.StepUnit(TickContext{Tick: tick, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
	if want := uint32(tick) + 900; builder.RevealDeadline != want {
		t.Fatalf("RevealDeadline=%d want %d (tick+900)", builder.RevealDeadline, want)
	}
}

func TestUnitReclaimCadenceAndFatalRefundCleanup(t *testing.T) {
	s, builder, target, node := reclaimFixture(t, 1, 10)
	builder.Def.WorkerTime = 300 // pulse 15, exercising ordinary lethal health clamp
	target.Remaining = 0.25
	s.SetBuilderLink(target.Handle, builder.Handle)
	s.getBuiltLinks[target.Handle] = builder.Handle
	var deaths, extras int
	s.World.OnDeath = func(_ pool.Handle, cause units.DeathCause, _ *units.Unit) {
		deaths++
		if cause != units.DeathReclaimed {
			t.Errorf("death cause=%d want reclaimed", cause)
		}
	}
	s.World.OnDeathExtra = func(_ pool.Handle, cause units.DeathCause, _ *units.Unit) {
		extras++
		if cause != units.DeathReclaimed {
			t.Errorf("extra death cause=%d want reclaimed", cause)
		}
	}
	// Nine admitted visits are required. [05 R-WORK-01 §4] tests the counter
	// BEFORE raising it, so the pre-check values are 0, 2, ... 16 and the pulse
	// fires on the visit that sees 16 — the ninth, at tick 16, "eight
	// qualifying visits = sixteen ticks" after the setup visit. Corrected with
	// the handler (PT3-05): the old ordering raised the counter first and fired
	// one visit early.
	reclaimVisits(s, builder, node, 0, 2, 4, 6, 8, 10, 12, 14, 16)
	// Signed overkill: the fatal pulse's signed health remainder survives
	// until severity and death callbacks finish — no clamp at zero
	// [04 §5.1] Killed severity contract (UNIT-05).
	if !target.Dying || target.Health >= 0 {
		t.Fatalf("fatal reclaim did not latch target: dying=%v health=%d", target.Dying, target.Health)
	}
	// Death hooks are deferred until slot-end finalization [04 R-MOV-03 §1].
	if deaths != 0 || extras != 0 {
		t.Fatalf("death observers ran before finalization primary=%d extra=%d want 0/0", deaths, extras)
	}
	if got := s.Economy.UnitBuckets(builder.Handle); got == nil || (*got)[economy.Metal].Production != 75 {
		t.Fatalf("metal refund=%v want 75", got)
	}
	if _, ok := s.BuilderLink(target.Handle); ok {
		t.Fatal("builder link survived reclaim")
	}
	if _, ok := s.getBuiltLinks[target.Handle]; ok {
		t.Fatal("get-built link survived reclaim")
	}
	if orders.QueueForUnit(builder).LenPrimary() != 0 {
		t.Fatal("reclaim node survived fatal completion")
	}
	// Finalization is the only free point and must not duplicate either hook.
	if got := s.World.FinalizeDeath(target.Handle, 20); !got.Freed {
		t.Fatal("target did not finalize")
	}
	if deaths != 1 || extras != 1 {
		t.Fatalf("death observers duplicated at finalization primary=%d extra=%d", deaths, extras)
	}
}

// TestUnitReclaimRefusesACommanderAndAnAircraft locks the two target clauses of
// the eligibility predicate [05 R-WORK-01 §4]. Before PT3-05 neither was
// evaluated, so a construction unit ordered onto a commander (or onto anything
// airborne) started reclaiming it. There is no separate capture-immunity bit:
// the predicate reads the same `cancapture` key the capture executor rejects.
func TestUnitReclaimRefusesACommanderAndAnAircraft(t *testing.T) {
	t.Run("cancapture target", func(t *testing.T) {
		s, builder, target, _ := reclaimFixture(t, 100, 10)
		target.Def.CanCapture = true // a commander
		s.StepUnit(TickContext{Tick: 0, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
		if orders.QueueForUnit(builder).LenPrimary() != 0 {
			t.Fatalf("a cancapture target was admitted for reclaim")
		}
		if target.Health != 100 {
			t.Fatalf("a cancapture target lost health: %d", target.Health)
		}
	})
	t.Run("airborne target", func(t *testing.T) {
		s, builder, target, _ := reclaimFixture(t, 100, 10)
		target.Move.Mode = 2 // airborne [04 R-MOV-01 §8]
		s.StepUnit(TickContext{Tick: 0, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
		if orders.QueueForUnit(builder).LenPrimary() != 0 {
			t.Fatalf("an airborne target was admitted for reclaim")
		}
	})
	t.Run("grounded ordinary target stays admitted", func(t *testing.T) {
		s, builder, target, _ := reclaimFixture(t, 100, 10)
		target.Move.Mode = 1
		s.StepUnit(TickContext{Tick: 0, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
		if orders.QueueForUnit(builder).LenPrimary() != 1 {
			t.Fatalf("an ordinary grounded target was refused")
		}
	})
}

// TestUnitReclaimEmitsOneSegmentPerVisit locks the presentation cadence §4
// separates from the damage gate: one nano segment per qualifying work visit,
// not one per bite. Before PT3-05 the emission sat behind the pulse gate, so
// seven of every eight visits drew nothing.
func TestUnitReclaimEmitsOneSegmentPerVisit(t *testing.T) {
	s, builder, target, node := reclaimFixture(t, 100, 10)
	target.Move.Mode = 1
	sink := &countingNanoSink{}
	s.Presentation = sink
	bindConstructionFixture(builder, trivialModel(1, nil), true)
	reclaimVisits(s, builder, node, 0, 2, 4, 6)
	if sink.count != 4 {
		t.Fatalf("four qualifying visits emitted %d nano segments, want 4 [05 R-WORK-01 §4]", sink.count)
	}
}

type countingNanoSink struct{ count int }

func (c *countingNanoSink) EmitNanolathe(frame.Event) bool { c.count++; return true }
