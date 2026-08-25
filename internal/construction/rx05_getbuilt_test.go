package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RX-05: a completed product whose queue head is a stale GetBuilt node must
// not block factory production behind it [05 C18][05 "Rally inheritance"].
func TestRX05_GetBuiltDropsAndUnblocksFactoryWork(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armfac"):  {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfac")}, UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "oo", Builder: true, MaxDamage: 100},
			content.CanonicalKey("armflea"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflea")}, UnitName: "armflea", FootprintX: 1, FootprintZ: 1, CanMove: true, MaxDamage: 50},
		},
	}
	w := units.NewSliced(10, cat)
	facH, _ := w.Create(cat.Units[content.CanonicalKey("armfac")], 0, 0, 0, 0)
	fac := w.Unit(facH)
	fac.Remaining = 0 // completed
	svc := NewService(nil, cat, w, &economy.Service{})
	gb := orders.Lookup("GetBuilt")
	if gb == 0 {
		t.Fatalf("GetBuilt descriptor missing")
	}
	bid := orders.Lookup(FactoryBuildOrder)
	if bid == 0 {
		t.Fatalf("factory build descriptor missing")
	}
	q := orders.QueueForUnit(fac)
	q.Push(gb, orders.Node{Param2: 0}) // stale get-built at head
	if err := QueueFactoryBuild(fac, "armflea", 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}
	ctx := TickContext{Tick: 1, World: w, Economy: svc.Economy}
	res := svc.StepUnit(ctx, facH)
	head := orders.QueueForUnit(fac).Primary()[0]
	if head.ID == gb {
		t.Fatalf("GetBuilt still at head after StepUnit (did not drop itself)")
	}
	if head.BuildDefKey != content.CanonicalKey("armflea") && res.DefKey != content.CanonicalKey("armflea") {
		t.Fatalf("factory work did not proceed to armflea: head=%d res=%+v", head.ID, res)
	}
}

func TestRX05_GetBuiltWaitsWhileUnderConstruction(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armfac"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfac")}, UnitName: "armfac", FootprintX: 2, FootprintZ: 2, YardMap: "oo", Builder: true, MaxDamage: 100},
		},
	}
	w := units.NewSliced(10, cat)
	labH, _ := w.Create(cat.Units[content.CanonicalKey("armfac")], 0, 0, 0, 0)
	lab := w.Unit(labH)
	lab.Remaining = 0.5 // still under construction
	svc := NewService(nil, cat, w, &economy.Service{})
	gb := orders.Lookup("GetBuilt")
	q := orders.QueueForUnit(lab)
	q.Push(gb, orders.Node{Param2: 0})
	svc.StepUnit(TickContext{Tick: 1, World: w, Economy: svc.Economy}, labH)
	if orders.QueueForUnit(lab).Primary()[0].ID != gb {
		t.Fatalf("GetBuilt must wait while product is under construction [05]")
	}
}
