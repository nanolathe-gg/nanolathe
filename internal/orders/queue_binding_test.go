package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestQueueBindingKeepsSessionInputsInterleaved(t *testing.T) {
	def := &content.UnitDef{UnitName: "binding-test", MaxDamage: 1}
	w := newOrdersFixtureWorld(4, nil)
	h1, _ := w.Create(def, 0, 0, 0, 0)
	h2, _ := w.Create(def, 1, 0, 0, 0)
	u1, u2 := w.Unit(h1), w.Unit(h2)
	target1 := &units.Unit{Handle: 41}
	target2 := &units.Unit{Handle: 42}
	sim1 := rng.NewSimulation(1)
	sim2 := rng.NewSimulation(2)
	q1 := BindQueueBinding(u1, &QueueBinding{
		Lookup: func(h pool.Handle) *units.Unit {
			if h == target1.Handle {
				return target1
			}
			return nil
		},
		Hostility: func(a, b *units.Unit) bool { return a == u1 && b == target1 },
		SimRNG:    &sim1,
	})
	q2 := BindQueueBinding(u2, &QueueBinding{
		Lookup: func(h pool.Handle) *units.Unit {
			if h == target2.Handle {
				return target2
			}
			return nil
		},
		Hostility: func(a, b *units.Unit) bool { return a == u2 && b == target2 },
		SimRNG:    &sim2,
	})
	if q1.Binding() == q2.Binding() {
		t.Fatal("two sessions must not share a queue binding")
	}
	if got := q1.Binding().Lookup(target1.Handle); got != target1 || q2.Binding().Lookup(target1.Handle) != nil {
		t.Fatal("target lookup crossed queue binding")
	}
	if !isHostile(u1, target1) || isHostile(u2, target1) {
		t.Fatal("hostility crossed queue binding")
	}

	moveID := Lookup("Move_Ground")
	restore := setHandler(moveID, func(_ *units.Unit, _ *Node, _ uint32, _ uint32) Code { return Code(3) })
	defer restore()
	q1.Push(moveID, Node{})
	q2.Push(moveID, Node{})
	q1.Pump(u1, 100)
	q2.Pump(u2, 100)
	if sim1.Draws() != 1 || sim2.Draws() != 1 {
		t.Fatalf("interleaved queues consumed sim draws %d and %d, want one each", sim1.Draws(), sim2.Draws())
	}
	wantSim1 := rng.NewSimulation(1)
	wantSim2 := rng.NewSimulation(2)
	want1 := int32(100 + 30 + wantSim1.Uint32n(15))
	want2 := int32(100 + 30 + wantSim2.Uint32n(15))
	if q1.Primary()[0].Deadline != want1 || q2.Primary()[0].Deadline != want2 {
		t.Fatalf("queue jitter did not follow its own stream: %d/%d want %d/%d", q1.Primary()[0].Deadline, q2.Primary()[0].Deadline, want1, want2)
	}
}

func TestQueueBindingIsStoredAndClearedAsOneContext(t *testing.T) {
	u := &units.Unit{Handle: 1}
	q := QueueForUnit(u)
	sim := rng.NewSimulation(9)
	b := &QueueBinding{SimRNG: &sim}
	q.SetBinding(b)
	if got := q.Binding(); got != b {
		t.Fatalf("Binding returned %p, want stored binding %p", got, b)
	}
	q.SetBinding(nil)
	if got := q.Binding(); got != nil {
		t.Fatalf("Binding after SetBinding(nil) = %p, want nil", got)
	}
}

func TestBoundQueueCannotAdmitStockpileWithoutEconomy(t *testing.T) {
	w := newOrdersFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "armsilo", MaxDamage: 1}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	u.Slots[0].Weapon = &content.WeaponDef{Stockpile: true, ReloadTime: 30}
	sim := rng.NewSimulation(7)
	q := BindQueueBinding(u, &QueueBinding{SimRNG: &sim})
	id := Lookup("BuildWeapon")
	q.CoalesceTail(id, Node{Param1: 0, Param2: 1})
	q.Pump(u, 0)
	n := q.Secondary()[0]
	if n.Param3 != 0 || n.Param2 != 1 {
		t.Fatalf("bound queue bypassed missing economy admission: progress=%d count=%d", n.Param3, n.Param2)
	}
}

func TestUnboundQueueCannotAdvanceStockpile(t *testing.T) {
	w := newOrdersFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "armsilo", MaxDamage: 1}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	u.Slots[0].Weapon = &content.WeaponDef{Stockpile: true, ReloadTime: 30}
	q := QueueForUnit(u)
	id := Lookup("BuildWeapon")
	q.CoalesceTail(id, Node{Param1: 0, Param2: 1})
	q.Pump(u, 0)
	n := q.Secondary()[0]
	if n.Param3 != 0 || n.Param2 != 1 {
		t.Fatalf("unbound queue bypassed missing economy admission: progress=%d count=%d", n.Param3, n.Param2)
	}
}

func TestPumpUnitDoesNotMaterializeAbsentQueue(t *testing.T) {
	w := newOrdersFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "idle", MaxDamage: 1}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	u := w.Unit(h)
	if u == nil {
		t.Fatal("created unit is missing")
	}
	if u.Orders != nil {
		t.Fatal("fixture unexpectedly started with an order queue")
	}
	res := (&Pump{World: w}).PumpUnit(h, 1)
	if res.Err != nil {
		t.Fatalf("pump idle unit: %v", res.Err)
	}
	if u.Orders != nil {
		t.Fatal("pumping an idle unit materialized an empty queue")
	}
}

func TestQueueBindingTraversalPreservesAdapterOrder(t *testing.T) {
	var unitsSeen []pool.Handle
	var featuresSeen []int32
	b := &QueueBinding{World: &WorldQueryAdapter{
		ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			for _, h := range []pool.Handle{7, 3, 9} {
				if visit(h, &units.Unit{Handle: h}) {
					return
				}
			}
		},
		ForEachFeature: func(visit func(FeatureView) bool) {
			for _, cx := range []int32{2, 5, 8} {
				if visit(FeatureView{CX: cx}) {
					return
				}
			}
		},
	}}
	b.ForEachUnit(func(h pool.Handle, _ *units.Unit) bool {
		unitsSeen = append(unitsSeen, h)
		return h == 3
	})
	b.ForEachFeature(func(f FeatureView) bool {
		featuresSeen = append(featuresSeen, f.CX)
		return false
	})
	if got, want := fmt.Sprint(unitsSeen), "[7 3]"; got != want {
		t.Fatalf("unit traversal = %s, want %s", got, want)
	}
	if got, want := fmt.Sprint(featuresSeen), "[2 5 8]"; got != want {
		t.Fatalf("feature traversal = %s, want %s", got, want)
	}
}

func TestQueueBindingValidationRejectsMissingProductionAdapters(t *testing.T) {
	sim := rng.NewSimulation(1)
	b := &QueueBinding{SimRNG: &sim,
		Economy:   &economy.Service{},
		Lookup:    func(pool.Handle) *units.Unit { return nil },
		Hostility: func(*units.Unit, *units.Unit) bool { return false },
	}
	if err := b.Validate(); err == nil {
		t.Fatal("missing single-player adapters accepted by composition seam")
	}
}

func TestQueueReplacementInheritsConcreteBinding(t *testing.T) {
	w := newOrdersFixtureWorld(2, nil)
	def := &content.UnitDef{UnitName: "replacement-binding", MaxDamage: 1}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	u := w.Unit(h)
	sim := rng.NewSimulation(13)
	b := &QueueBinding{SimRNG: &sim}
	old := BindQueueBinding(u, b)
	replacement := NewQueueWith(nil, nil)
	BindQueue(u, replacement)
	if got := QueueOfUnit(u).Binding(); got != b {
		t.Fatalf("replacement binding = %p, want original %p (old %p)", got, b, old.Binding())
	}
}
