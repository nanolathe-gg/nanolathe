package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

func mobileBuildCompletionFixture(t *testing.T, count uint32) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := newFactoryDef("mobile_builder", 1, 1, 30)
	builderDef.BMCode = true
	builderDef.CanMove = true
	productDef := newProductDef("mobile_product", 1, 1, 1, 1)
	cat.Units[builderDef.CanonicalKey] = builderDef
	cat.Units[productDef.CanonicalKey] = productDef

	w := newConstructionFixtureWorld(8, cat)
	bh, err := w.Create(builderDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ph, err := w.Create(productDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	builder, product := w.Unit(bh), w.Unit(ph)
	product.Remaining = 0
	product.Health = 0
	product.Flags &^= FlagCompleted
	vm := bindScriptBridge(t, builder, "StartBuilding", "StopBuilding")

	q := orders.QueueForUnit(builder)
	q.SetBinding(&orders.QueueBinding{Lookup: func(h pool.Handle) *units.Unit { return w.Unit(h) }})
	q.Push(orders.Lookup(MobileBuildOrder), orders.Node{
		BuildDefKey: productDef.CanonicalKey,
		Param2:      count,
		Phase:       uint8(State4),
		Target:      product.Handle,
		GoalX:       111,
		GoalY:       222,
		GoalZ:       333,
		Owner:       builder.Handle,
	})
	node := q.Primary()[0]
	orders.EmitStartBuilding(builder, node)
	if liveThreads(vm) != 1 {
		t.Fatalf("StartBuilding arrangements = %d, want 1", liveThreads(vm))
	}

	svc := NewService(nil, cat, w, &economy.Service{})
	svc.SetBuilderLink(product.Handle, builder.Handle)
	return svc, builder, product, node
}

func TestMobileBuildCompletionRemovesCoalescedRecordOnce(t *testing.T) {
	for _, count := range []uint32{1, 3} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			svc, builder, product, node := mobileBuildCompletionFixture(t, count)
			vm := builder.Script

			svc.Pump(builder, 90)

			if got := orders.QueueForUnit(builder).LenPrimary(); got != 0 {
				t.Fatalf("completed MobileBuild queue length = %d, want 0", got)
			}
			if node.Param2 != count {
				t.Fatalf("completed MobileBuild Param2 = %d, want unchanged %d", node.Param2, count)
			}
			if node.Target != 0 || node.GoalX != 0 || node.GoalY != 0 || node.GoalZ != 0 {
				t.Fatalf("completion payload survived: target=%d goal=(%d,%d,%d)", node.Target, node.GoalX, node.GoalY, node.GoalZ)
			}
			if node.Flags&orders.FlagStopBuildingPending != 0 {
				t.Fatal("completion cleanup left StopBuilding pending")
			}
			if got := liveThreads(vm); got != 2 {
				t.Fatalf("lifecycle arrangements = %d, want one StartBuilding and one StopBuilding", got)
			}
			if _, ok := svc.BuilderLink(product.Handle); ok {
				t.Fatal("completed product retained builder link")
			}
			if product.Remaining != 0 || product.Flags&FlagCompleted == 0 || product.Health != product.MaxHealth {
				t.Fatalf("product completion posture = remaining %v flags %x health %d/%d", product.Remaining, product.Flags, product.Health, product.MaxHealth)
			}
		})
	}
}

func TestBuildingBuildCountedCompletionStillRestartsSamePass(t *testing.T) {
	svc, factory, product, node := p28CompletionFixture(t, 2)
	product.Def.MinWaterDepth = -10000

	svc.Pump(factory, 70)

	if node.Param2 != 1 || node.Phase != uint8(State3) || node.Target == 0 || node.Target == product.Handle {
		t.Fatalf("counted BuildingBuild successor = count %d phase %d target %d, want count 1 active successor distinct from %d", node.Param2, node.Phase, node.Target, product.Handle)
	}
}
