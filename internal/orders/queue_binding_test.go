package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestQueueBindingKeepsSessionInputsInterleaved(t *testing.T) {
	def := &content.UnitDef{UnitName: "binding-test", MaxDamage: 1}
	w := units.New(4, nil)
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
	if got := q1.Lookup(target1.Handle); got != target1 || q2.Lookup(target1.Handle) != nil {
		t.Fatal("target lookup crossed queue binding")
	}
	if !isHostile(u1, target1) || isHostile(u2, target1) {
		t.Fatal("hostility crossed queue binding")
	}

	moveID := Lookup("Move_Ground")
	restore := setHandler(moveID, func(_ *units.Unit, _ *Node, _ uint32) Code { return Code(3) })
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

func TestBoundQueueCannotAdmitStockpileWithoutEconomy(t *testing.T) {
	w := units.New(4, nil)
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
	w := units.New(4, nil)
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
