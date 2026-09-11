package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// Mobile placement preserves the structure's authored standing orders, while
// BuildingBuild passes the factory's orders to its products [04 R-STANCE-01 §6].
func TestMobileBuiltFactoryKeepsAuthoredStandingOrders(t *testing.T) {
	for _, kind := range []string{"MobileBuild", "VTOL_MobileBuild"} {
		t.Run(kind, func(t *testing.T) {
			svc, builder, node := approachFixture(t, 10, 10)
			svc.Economy = &economy.Service{}
			builder.Def.CanFly = kind == "VTOL_MobileBuild"
			builder.Flags = (builder.Flags &^ (StandingMoveMask | StandingFireMask)) | 1<<20
			factoryDef := svc.Catalog.Units["corlab"]
			factoryDef.StandingMoveOrder, factoryDef.StandingFireOrder = 1, 2
			factoryDef.Builder, factoryDef.CanMove = true, true
			factoryDef.YardMap = "c"
			node.ID = orders.Lookup(kind)
			node.Phase, node.DynamicGate, node.Deadline = 2, 0, -1

			// Begin at placement after the approach has completed.
			svc.Pump(builder, 100)
			factory := svc.World.Unit(node.Target)
			if factory == nil {
				t.Fatalf("no factory allocated: phase=%d messages=%v", node.Phase, svc.Messages())
			}
			want := uint32(1<<18 | 2<<20)
			check := func(stage string) {
				t.Helper()
				if got := factory.Flags & (StandingMoveMask | StandingFireMask); got != want {
					t.Fatalf("%s: factory move/fire = %d/%d, want authored 1/2", stage, got>>18&3, got>>20&3)
				}
			}
			check("allocation")
			factory.Remaining = 0
			svc.applyCompletionPosture(factory)
			pq := orders.QueueForUnit(factory)
			gb := pq.Head()
			if gb == nil || gb.ID != orders.Lookup("GetBuilt") {
				t.Fatal("factory has no GetBuilt record")
			}
			if code := svc.handleGetBuiltOrder(factory, gb, 0, 101); code != 5 {
				t.Fatalf("GetBuilt result = %d, want complete", code)
			}
			check("completion")
			pq.CancelAll()
			if !svc.YardOpenTransaction(factory, true) {
				t.Fatal("completed factory yard refused open")
			}

			// Different product defaults prove that factory inheritance remains.
			productDef := newProductDef("corak", 2, 2, 100, 50)
			productDef.MinWaterDepth = -10000
			productDef.BMCode, productDef.YardMap = 1, ""
			productDef.MovementClass = "kb"
			productDef.StandingMoveOrder, productDef.StandingFireOrder = 2, 0
			svc.Catalog.Units[productDef.CanonicalKey] = productDef
			bindConstructionFixture(factory, trivialModel(1, nil), true)
			pq.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: "corak", Param2: 1, Phase: 2, Deadline: -1})
			build := pq.Head()
			svc.Pump(factory, 102)
			product := svc.World.Unit(build.Target)
			if product == nil {
				t.Fatalf("factory produced no nanoframe: phase=%d admissions=%v messages=%v", build.Phase, svc.AdmissionDiagnostics(), svc.Messages())
			}
			if got := product.Flags & (StandingMoveMask | StandingFireMask); got != want {
				t.Fatalf("factory product move/fire = %d/%d, want inherited 1/2", got>>18&3, got>>20&3)
			}
		})
	}
}
