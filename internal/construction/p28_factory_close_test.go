package construction

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func p28FactoryLifecycleBinding(u *units.Unit) *cob.CallbackBridge {
	// The four lifecycle callbacks return immediately. QueryBuildInfo uses the
	// same authored-style cell-zero writer as the construction fixture so the
	// counted-successor test exercises the real state-2 allocation boundary.
	prog := &cob.Program{
		Code: []uint32{
			0x10065000, 0x10065000, 0x10065000, 0x10065000,
			0x10021001, 0, 0x10023002, 0, 0x10065000,
		},
		Scripts: map[string]int{
			"Activate": 0, "StopBuilding": 1, "Deactivate": 2,
			"StartBuilding": 3, "QueryBuildInfo": 4,
		},
		ScriptsByID: []int{0, 1, 2, 3, 4},
		Pieces:      []string{"base"},
	}
	vm := cob.NewVM(prog)
	bridge := cob.NewCallbackBridge(vm)
	binding := &cob.Binding{VM: vm, Model: trivialModel(1, nil), PieceMap: []int{0}, Callbacks: bridge}
	u.Script = vm
	u.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
	return bridge
}

func p28CompletionFixture(t *testing.T, count int) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	factoryDef := newFactoryDef("fixture_factory", 1, 1, 30)
	productDef := newProductDef("fixture_product", 1, 1, 1, 1)
	productDef.ActivateWhenBuilt = true
	cat.Units[factoryDef.CanonicalKey] = factoryDef
	cat.Units[productDef.CanonicalKey] = productDef
	w := newConstructionFixtureWorld(12, cat)
	fh, err := w.Create(factoryDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ph, err := w.Create(productDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	factory, product := w.Unit(fh), w.Unit(ph)
	factory.Flags |= units.BuildingClassStatus
	factory.BuildingState = true
	factory.Activated = true
	factory.InBuildStance = true
	product.Remaining = 1
	product.Health = 0
	product.Flags &^= FlagCompleted
	// A nanoframe is created inactive so completion's raise is a real edge
	// [04 R-SPEC-01 §12][04 R-UNIT-06 §2].
	product.Activated = false
	p28FactoryLifecycleBinding(factory)
	p28FactoryLifecycleBinding(product)
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup(FactoryBuildOrder), orders.Node{
		BuildDefKey: productDef.CanonicalKey,
		Param2:      uint32(count),
		Phase:       uint8(State3),
	})
	q.Primary()[0].BindTarget(ph) // stage the runtime product relink [04 R-ORD-01 §6]
	svc := NewService(exitTerrain(16, 16), cat, w, &economy.Service{})
	bindConstructionCombat(svc)
	svc.ModelForFactory = func(*units.Unit) *model.Model { return trivialModel(1, nil) }
	return svc, factory, product, q.Primary()[0]
}

func TestP28FactoryFinalIncrementCompletesBeforeStopThenDeactivates(t *testing.T) {
	svc, factory, product, _ := p28CompletionFixture(t, 1)
	var got []string
	product.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Phase == "start" {
			got = append(got, "product:"+e.Name)
		}
	})
	factory.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Phase == "start" {
			got = append(got, "factory:"+e.Name)
		}
	})

	svc.Pump(factory, 40)

	want := []string{"product:Activate", "factory:StopBuilding", "factory:Deactivate"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("completion callback order = %v, want %v", got, want)
	}
	if product.Remaining != 0 || product.Flags&FlagCompleted == 0 || product.Health != product.MaxHealth {
		t.Fatalf("product not completed synchronously: remaining=%v flags=%x health=%d/%d", product.Remaining, product.Flags, product.Health, product.MaxHealth)
	}
	if q := orders.QueueForUnit(factory); q.LenPrimary() != 0 {
		t.Fatalf("empty-count production node survived: %d", q.LenPrimary())
	}
	if factory.Activated || factory.BuildingState {
		t.Fatalf("empty path retained factory edges: activated=%t building=%t", factory.Activated, factory.BuildingState)
	}
}

func TestP28FactoryCountedSuccessorStaysActiveAndRestartsSamePass(t *testing.T) {
	svc, factory, product, node := p28CompletionFixture(t, 2)
	product.Def.MinWaterDepth = -10000 // established land-profile template [04 §6.1]
	var got []string
	factory.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Phase == "start" {
			got = append(got, e.Name)
		}
	})

	svc.Pump(factory, 70)

	if node.Param2 != 1 || node.Phase != uint8(State3) || node.Target == 0 || node.Target == product.Handle {
		t.Fatalf("successor was not allocated in the completion pass: count=%d phase=%d target=%d first=%d", node.Param2, node.Phase, node.Target, product.Handle)
	}
	if !reflect.DeepEqual(got, []string{"StopBuilding", "StartBuilding"}) {
		t.Fatalf("queued callback order = %v, want StopBuilding then StartBuilding", got)
	}
	if !factory.Activated || !factory.BuildingState {
		t.Fatalf("queued path lowered active/building state: activated=%t building=%t", factory.Activated, factory.BuildingState)
	}
}

func TestP28FactoryState4CompletionIsIdempotent(t *testing.T) {
	svc, factory, product, node := p28CompletionFixture(t, 1)
	var productStarts int
	product.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Phase == "start" && e.Name == "Activate" {
			productStarts++
		}
	})

	// Model the shared helper's synchronous transition, then enter state 4 as
	// the primary pump does. The repeated transition must not raise a new edge.
	svc.applyCompletionPosture(product)
	node.Phase = uint8(State4)
	svc.handleState4(factory, node, 90)

	if productStarts != 1 {
		t.Fatalf("Activate starts after idempotent state 4 = %d, want 1", productStarts)
	}
}

func TestP28FactoryCancelWinsBeforeWorkVisit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining float32
	}{
		{name: "during-work", remaining: 0.5},
		{name: "near-completion", remaining: 0.01},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, factory, product, node := p28CompletionFixture(t, 1)
			product.Remaining = tc.remaining
			factory.Pending = InterruptCancel
			var got []string
			factory.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
				if e.Phase == "start" {
					got = append(got, e.Name)
				}
			})

			svc.Pump(factory, 120)

			if !product.Dying || node.Param2 != 1 || product.Remaining != 0 {
				t.Fatalf("cancel did not win: dying=%t count=%d remaining=%v", product.Dying, node.Param2, product.Remaining)
			}
			if !reflect.DeepEqual(got, []string{"Deactivate", "StopBuilding"}) {
				t.Fatalf("cancel edge order = %v, want Deactivate then StopBuilding", got)
			}
		})
	}
}
