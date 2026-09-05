package construction

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

func p28RetailFactoryWorld(t *testing.T) (*content.Catalog, *units.World, func(*units.Unit) *model.Model) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	w := newConstructionFixtureWorld(len(cat.Units), cat)
	sim := rng.NewSimulation(1)
	w.SetSimulationRNG(&sim)
	models := make(map[string]*model.Model)
	modelFor := func(u *units.Unit) *model.Model {
		if u == nil || u.Def == nil {
			return nil
		}
		key := content.CanonicalKey(strings.TrimSpace(u.Def.ObjectName))
		if mdl := models[key]; mdl != nil {
			return mdl
		}
		path := "objects3d/" + key + ".3do"
		mdl, err := model.Load(fs, path)
		if err != nil {
			t.Fatalf("load model %q: %v", path, err)
		}
		models[key] = mdl
		return mdl
	}
	w.SetCOBBinder(func(u *units.Unit) error {
		binding, err := units.BindCOBWithPortsAndVisibilityForUnit(fs, u, modelFor(u), &sim, nil, nil)
		if err != nil {
			return err
		}
		return u.AttachCOBBinding(binding)
	})
	return cat, w, modelFor
}

// TestP28COB01RFactoryProductUsesIndependentStrictARMCKBinding compares a
// direct common-allocation ARMCK with an ARMCK admitted by the real factory
// state-2 path. The relationship is diagnostic: both routes must reach the
// same post-Create value state, while the product owns independent VM and
// stance storage [R-P28-COB-01R].
func TestP28COB01RFactoryProductUsesIndependentStrictARMCKBinding(t *testing.T) {
	cat, w, modelFor := p28RetailFactoryWorld(t)
	armck, ok := cat.Unit("armck")
	if !ok || armck == nil {
		t.Fatal("retail ARMCK definition missing")
	}
	armlab, ok := cat.Unit("armlab")
	if !ok || armlab == nil {
		t.Fatal("retail ARMLAB definition missing")
	}
	directHandle, err := w.Create(armck, 0, world.CellToWorld(40), 0, world.CellToWorld(40))
	if err != nil {
		t.Fatalf("direct ARMCK allocation: %v", err)
	}
	factoryHandle, err := w.Create(armlab, 0, world.CellToWorld(20), 0, world.CellToWorld(20))
	if err != nil {
		t.Fatalf("ARMLAB allocation: %v", err)
	}
	direct, factory := w.Unit(directHandle), w.Unit(factoryHandle)
	if direct == nil || factory == nil {
		t.Fatal("allocation returned missing unit")
	}
	q := orders.QueueForUnit(factory)
	if err := QueueFactoryBuild(factory, armck.CanonicalKey, 1, cat); err != nil {
		t.Fatalf("queue ARMCK: %v", err)
	}
	if q == nil || q.LenPrimary() != 1 {
		t.Fatalf("factory queue after insert: %#v", q)
	}
	node := q.Primary()[0]
	node.Phase = uint8(State2)
	node.Deadline = -1
	factory.InBuildStance = true
	svc := NewService(exitTerrain(96, 96), cat, w, &economy.Service{})
	svc.ModelForFactory = modelFor
	svc.ModelForUnit = modelFor
	svc.Pump(factory, 0)
	product := w.Unit(node.Target)
	if product == nil || product.Def != armck {
		t.Fatalf("factory ARMCK allocation: target=%d product=%#v admissions=%v messages=%v", node.Target, product, svc.AdmissionDiagnostics(), svc.Messages())
	}
	directBinding, productBinding := direct.COBBinding(), product.COBBinding()
	if directBinding == nil || productBinding == nil || directBinding.VM == nil || productBinding.VM == nil {
		t.Fatal("ARMCK route lacks strict binding")
	}
	if directBinding == productBinding || directBinding.VM == productBinding.VM {
		t.Fatal("factory product aliases another ARMCK binding or VM")
	}
	if productBinding.Model != directBinding.Model || productBinding.ScriptPath != directBinding.ScriptPath || productBinding.Provider != directBinding.Provider || !reflect.DeepEqual(productBinding.Program.Pieces, directBinding.Program.Pieces) {
		t.Fatal("ARMCK routes did not resolve the same immutable model and authored COB identity")
	}
	if !productBinding.CreateInvoked || !productBinding.Callbacks.CreateInvoked() || productBinding.VM.DrainCalls != 1 {
		t.Fatalf("product Create route invoked=%t bridge=%t drains=%d", productBinding.CreateInvoked, productBinding.Callbacks.CreateInvoked(), productBinding.VM.DrainCalls)
	}
	if !reflect.DeepEqual(productBinding.PieceMap, directBinding.PieceMap) || !reflect.DeepEqual(productBinding.VM.Pieces, directBinding.VM.Pieces) || !reflect.DeepEqual(product.RenderPieceFlags, direct.RenderPieceFlags) {
		t.Fatal("direct and factory ARMCK differ immediately after strict Create")
	}
	if product.InBuildStance || product.Busy || product.Flags&FlagStartBuilding != 0 {
		t.Fatalf("factory product inherited factory lifecycle state: stance=%t busy=%t start=%t", product.InBuildStance, product.Busy, product.Flags&FlagStartBuilding != 0)
	}
	if factory.Flags&FlagStartBuilding == 0 {
		t.Fatal("factory did not receive its own StartBuilding edge")
	}
	before := productBinding.VM.Pieces[0]
	directBinding.VM.Pieces[0].RotX++
	if productBinding.VM.Pieces[0] != before {
		t.Fatal("factory product piece state aliases direct unit state")
	}
}
