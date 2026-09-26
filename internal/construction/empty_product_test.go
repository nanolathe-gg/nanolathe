package construction

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// An empty mobile product uses the ordinary factory queue, allocator, cargo
// attachment and paid work path, even over occupied cells [04 R-P0-08-C].
func TestEmptyFactoryProductLifecycle(t *testing.T) {
	for _, pair := range [][2]int32{{0, 0}, {0, 2}, {2, 0}} {
		t.Run(fmt.Sprint(pair), func(t *testing.T) {
			factoryDef := newFactoryDef("empty-parent", 4, 4, 300)
			productDef := newProductDef("empty-product", pair[0], pair[1], 50, 100)
			productDef.BMCode = 1
			productDef.YardMap = ""
			productDef.CanMove = false
			productDef.CanPatrol = false
			cat := &content.Catalog{Units: map[string]*content.UnitDef{factoryDef.CanonicalKey: factoryDef, productDef.CanonicalKey: productDef}}
			terrain := exitTerrain(24, 24)
			svc, w := exitService(t, terrain, cat)
			for i := range svc.Economy.Players {
				svc.Economy.Players[i].Stock = [2]float32{1e6, 1e6}
				svc.Economy.Players[i].Capacity = [2]float32{1e6, 1e6}
			}
			h, err := w.Create(factoryDef, 0, numeric.Fixed(136<<16), numeric.Fixed(23<<16), numeric.Fixed(136<<16))
			if err != nil {
				t.Fatal(err)
			}
			factory := w.Unit(h)
			mdl := trivialModel(1, nil)
			bindConstructionFixture(factory, mdl, true)
			svc.Movement = movement.NewSystem(terrain, movement.Template(), movement.NewOccupancyGrid())
			svc.Movement.BindWorld(w)
			// Dense foreign occupancy must survive admission, carried updates,
			// completion and teardown exactly, in both planes.
			for i := range terrain.Plot {
				terrain.Plot[i].SetOccupantA(77)
				terrain.Plot[i].SetOccupantB(78)
			}
			before := append([]world.PlotCell(nil), terrain.Plot...)
			if err := QueueFactoryBuild(factory, productDef.UnitName, 1, cat); err != nil {
				t.Fatal(err)
			}
			cell, ok := svc.QueryBuildInfo(factory, mdl)
			if !ok || cell.X != int32((136+8-pair[0]*8)/16) || cell.Z != int32((136+8-pair[1]*8)/16) {
				t.Fatalf("empty snap=%v,%v", cell, ok)
			}
			node := orders.QueueForUnit(factory).Primary()[0]
			node.Phase = uint8(State2)
			svc.Pump(factory, 1)
			product := w.Unit(node.Target)
			if product == nil {
				t.Fatalf("no product: %+v", svc.AdmissionDiagnostics())
			}
			if product.X != factory.X || product.Y != factory.Y || product.Z != factory.Z || product.Attachment.Carrier != h {
				t.Fatalf("transform/attachment changed: product=%+v", product.Attachment)
			}
			rect, ok := svc.PlacementForProduct(product.Handle)
			if !ok || rect.Width() != pair[0] || rect.Depth() != pair[1] {
				t.Fatalf("placement=%v,%v", rect, ok)
			}
			if product.FootprintSizeX != int16(pair[0]) || product.FootprintSizeZ != int16(pair[1]) {
				t.Fatal("allocator replaced empty extents")
			}
			for tick := uint32(2); tick < 100 && product.Remaining > 0; tick++ {
				svc.Pump(factory, tick)
				svc.Movement.SyncCarriedMotion(w)
			}
			if product.Remaining != 0 || product.Attachment.Carrier != 0 || product.Flags&FlagCompleted == 0 {
				t.Fatalf("ordinary completion failed: remaining=%v carrier=%d diagnostics=%+v", product.Remaining, product.Attachment.Carrier, svc.AdmissionDiagnostics())
			}
			svc.Movement.RestampFootprint(int(product.Handle))
			svc.Movement.ForgetUnit(product.Handle)
			if !reflect.DeepEqual(before, terrain.Plot) {
				t.Fatal("empty product changed occupied plot cells")
			}
		})
	}
}
