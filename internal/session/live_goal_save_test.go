package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Ordinary installed goals, not only previously loaded subtype bytes, must
// reach the save projection [08 R-SAVE-02 §6, §10].
func TestRetailProjectionIncludesLiveMovementGoal(t *testing.T) {
	w := units.NewSliced(2, nil)
	h, err := w.Create(&content.UnitDef{UnitName: "mover", BMCode: 1, FootprintX: 1, FootprintZ: 1, MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}}, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	n := &orders.Node{ID: orders.Lookup("Move_Ground"), Owner: h, Phase: 1, DynamicGate: 0xe0}
	orders.BindQueue(u, orders.NewQueueWith([]*orders.Node{n}, nil))
	m := movement.NewSystem(nil, movement.Profile{}, movement.NewOccupancyGrid())
	m.BindWorld(w)
	m.EnsureUnit(u)
	if !m.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: n, X: 128 << 16, Z: 64 << 16, Radius: 32}) {
		t.Fatal("goal not installed")
	}
	econ := &economy.Service{}
	econ.UnitBuckets(h)
	s := &Session{Clock: &clock.State{GlobalTick: 9}, Units: w, Econ: econ, Movement: m}
	in := RetailSaveInputs{Summary: save.Summary{Gametype: 1}, Mapping: []byte{1}, StableIDs: map[pool.Handle]uint16{h: 1}, UnitWriterScratch: map[pool.Handle]units.RetailUnitWriterScratch{h: {}}, ScriptWriterScratch: map[pool.Handle]cob.RetailScriptWriterScratch{h: {}}}
	p, err := ProjectRetailSession(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Units.Orders) != 1 || p.Units.Orders[0].SubtypeCode != 4 {
		t.Fatalf("live waiting goal omitted from save: %+v", p.Units.Orders)
	}
	m.ReleaseGoalPayload(n)
	p, err = ProjectRetailSession(s, in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Units.Orders[0].SubtypeCode != 0 {
		t.Fatal("released goal was resurrected by save projection")
	}
}

// Marker references clear on final removal, independently of an order's own
// notifying target reference [04 R-AIR-01 §4]. Cover a displaced record too.
func TestAirGoalTargetRemovalDoesNotFollowReusedSlot(t *testing.T) {
	s := newLoopTestSession(t, 0)
	s.Movement.BindWorld(s.Units)
	def := s.Catalog.Units["armcom"]
	def.CanFly, def.BMCode = true, 1
	create := func() *units.Unit {
		t.Helper()
		h, err := s.Units.Create(def, 0, 32<<16, 100<<16, 32<<16)
		if err != nil {
			t.Fatal(err)
		}
		u := s.Units.Unit(h)
		s.Movement.EnsureUnit(u)
		return u
	}
	flyer, target := create(), create()
	head := &orders.Node{ID: orders.Lookup("VTOL_Move"), Owner: flyer.Handle}
	tail := &orders.Node{ID: orders.Lookup("VTOL_Patrol"), Owner: flyer.Handle}
	orders.BindQueue(flyer, orders.NewQueueWith([]*orders.Node{head, tail}, nil))
	for _, n := range []*orders.Node{tail, head} {
		if !s.Movement.InstallAirGoal(orders.AirGoalRequest{Owner: flyer.Handle, Node: n, Target: target.Handle, X: target.X, Y: target.Y, Z: target.Z}) {
			t.Fatal("goal not installed")
		}
		payload, err := s.Movement.RetailOrderPayload(n)
		if err != nil || payload.UnitB != target.Handle {
			t.Fatalf("missing initial marker reference: %+v, %v", payload, err)
		}
	}
	before := [2]uint32{head.Satisfied, tail.Satisfied}
	s.Units.Destroy(target.Handle, units.DeathKilled)
	s.finalizePhase2Death(target.Handle, 2)
	replacement := create()
	if replacement.Handle != target.Handle {
		t.Fatal("fixture did not reuse target slot")
	}
	for i, n := range []*orders.Node{head, tail} {
		payload, err := s.Movement.RetailOrderPayload(n)
		if err != nil || payload.UnitB != 0 {
			t.Fatalf("retained marker followed reused slot: %+v, %v", payload, err)
		}
		if n.Satisfied != before[i] {
			t.Fatalf("non-notifying marker raised pending bits: %x", n.Satisfied)
		}
	}
}
