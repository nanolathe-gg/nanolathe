package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Initial construction state is installed before wake-Create; the caller must
// preserve the script's writes afterwards [05 "Nanoframe allocation"]. The
// activatewhenbuilt completion edge is a separate operation [04 R-SPEC-01 §12].
func TestNanoframeAllocationPreservesCreateState(t *testing.T) {
	for _, scriptWrites := range []bool{false, true} {
		name := "default-inactive"
		if scriptWrites {
			name = "script-activates-and-sets-stance"
		}
		t.Run(name, func(t *testing.T) {
			factoryDef := newFactoryDef("create_factory", 1, 1, 0)
			productDef := newProductDef("create_product", 1, 1, 0, 100)
			productDef.ActivateWhenBuilt = true
			code := []uint32{0x10065000}
			if scriptWrites {
				// Independently authored Create: ACTIVATION=1; INBUILDSTANCE=1.
				code = []uint32{
					0x10021001, 1, 0x10021001, 1, 0x10082000,
					0x10021001, 5, 0x10021001, 1, 0x10082000,
					0x10065000,
				}
			}
			productDef.Script = &cob.Program{Code: code, Scripts: map[string]int{"Create": 0}, ScriptsByID: []int{0}}
			cat := exitCatalog(factoryDef, productDef)
			svc, unitWorld := exitService(t, exitTerrain(16, 16), cat)
			origin := world.CellToWorld(10)
			h, err := unitWorld.Create(factoryDef, 0, origin, 0, origin)
			if err != nil {
				t.Fatal(err)
			}
			pos := world.NewModelWorldPosition(world.CellToWorld(6), 0, world.CellToWorld(6))
			product, err := svc.allocateNanoframe(unitWorld.Unit(h), productDef, exitRect(6, 6, 1), pos)
			if err != nil {
				t.Fatal(err)
			}
			if product.Remaining != 1 || product.Health != 0 || product.Flags&FlagCompleted != 0 {
				t.Fatal("unfinished allocation applied completion effects")
			}
			if product.Activated != scriptWrites || product.InBuildStance != scriptWrites {
				t.Fatalf("Create state lost: activated=%v stance=%v; want both %v", product.Activated, product.InBuildStance, scriptWrites)
			}
		})
	}
}
