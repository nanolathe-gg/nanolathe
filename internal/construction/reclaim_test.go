package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
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
	// Eight admitted visits are required: 2,4,...,16 exceeds the 14 gate.
	reclaimVisits(s, builder, node, 0, 2, 4, 6, 8, 10, 12, 14)
	// Signed overkill: the fatal pulse's signed health remainder survives
	// until severity and death callbacks finish — no clamp at zero
	// [04 §5.1] Killed severity contract (UNIT-05).
	if !target.Dying || target.Health >= 0 {
		t.Fatalf("fatal reclaim did not latch target: dying=%v health=%d", target.Dying, target.Health)
	}
	// Death hooks are deferred until slot-end finalization [04 "unit sweep"].
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
