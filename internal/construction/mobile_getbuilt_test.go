package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// A newly allocated product can reach the session order sweep before its
// construction visit. Its first GetBuilt dispatch must already take the
// researched 300-tick arm [04 §3.8][04 R-ORD-01 §11], without a missing-handler
// fallback consuming an unrelated random draw.
func TestMobileProductGetBuiltReadyForFirstOrderPump(t *testing.T) {
	for _, kind := range []string{"MobileBuild", "VTOL_MobileBuild"} {
		t.Run(kind, func(t *testing.T) {
			svc, builder, node := approachFixture(t, 10, 10)
			svc.Economy = &economy.Service{}
			sim := rng.NewSimulation(7)
			svc.OrderBinding = &orders.QueueBinding{SimRNG: &sim, Lookup: svc.World.Unit}
			builder.Def.CanFly = kind == "VTOL_MobileBuild"
			node.ID = orders.Lookup(kind)
			node.Phase, node.DynamicGate, node.Deadline = 2, 0, -1
			svc.Pump(builder, 100)
			product := svc.World.Unit(node.Target)
			if product == nil {
				t.Fatal("mobile placement did not allocate a product")
			}
			q := orders.QueueForUnit(product)
			// The aircraft row can begin work in the allocation pass. Exercise
			// the first unworked visit here; wake-driven early phases have their
			// own lifecycle tests.
			product.Pending &^= pendingUnderConstructionWake
			before := sim.Draws()
			q.Pump(product, 101)
			if d := q.Diagnostics(); len(d) != 0 {
				t.Fatalf("first product pump: %v", d)
			}
			gb := q.Head()
			if gb == nil || gb.ID != orders.Lookup("GetBuilt") || gb.Phase != 1 || gb.Deadline != 401 || gb.DynamicGate != 0x8001 {
				t.Fatalf("first GetBuilt dispatch = %+v, want phase1, deadline401, gate0x8001", gb)
			}
			if sim.Draws() != before {
				t.Fatal("first GetBuilt dispatch consumed simulation randomness")
			}
		})
	}
}
