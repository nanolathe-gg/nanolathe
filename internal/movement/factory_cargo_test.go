package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestFactoryCargoFollowsPieceAndKeepsGroundStamp(t *testing.T) {
	terrain := syntheticTerrainFlat()
	grid := NewOccupancyGrid()
	system := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1}, grid)
	w := newMovementFixtureWorld(8)
	factoryDef := &content.UnitDef{UnitName: "armlab", FootprintX: 2, FootprintZ: 2, MaxDamage: 100}
	productDef := &content.UnitDef{UnitName: "armflash", FootprintX: 1, FootprintZ: 1, MaxDamage: 100, BMCode: 1}
	fh, _ := w.Create(factoryDef, 0, world.CellToWorld(6), numeric.FixedFromInt(10), world.CellToWorld(7))
	ph, _ := w.Create(productDef, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	factory, product := w.Unit(fh), w.Unit(ph)
	mdl := &model.Model{Name: "factory", Root: 0, Pieces: []model.Piece{{Name: "build", Parent: -1, Translate: [3]numeric.Fixed{numeric.FixedFromInt(16), numeric.FixedFromInt(3), 0}}}}
	prog := &cob.Program{Pieces: []string{"build"}}
	vm := cob.NewVM(prog)
	vm.Pieces[0].RotZ = 11
	vm.Pieces[0].RotY = 22
	vm.Pieces[0].RotX = 33
	binding := &cob.Binding{VM: vm, Model: mdl, PieceMap: []int{0}}
	factory.ScriptState = &units.ScriptState{VM: vm, Binding: binding}
	factory.Script = vm
	factory.Move.Bank, factory.Move.Heading, factory.Move.Pitch = 100, 200, 300
	system.BindWorld(w)
	system.EnsureUnit(factory)
	system.EnsureUnit(product)
	system.Collisions[factory.Handle].VX = 1234
	system.Collisions[factory.Handle].VZ = -5678
	system.Collisions[factory.Handle].Speed = 9012
	system.Collisions[product.Handle].VX = 77
	system.Collisions[product.Handle].VZ = 88
	oldAnchor := system.Collisions[product.Handle].OldAnchor
	if !AttachCargo(w, factory.Handle, product.Handle, 0) {
		t.Fatal("factory product attach failed")
	}
	product.Move.Mode = 1
	wantOffset, ok := binding.ComposePiece(0, factory.Move.Heading, factory.Move.Pitch, factory.Move.Bank)
	if !ok {
		t.Fatal("piece composition failed")
	}
	system.SyncCarriedMotion(w)
	if product.X != factory.X.Add(wantOffset[0]) || product.Y != factory.Y.Add(wantOffset[1]) || product.Z != factory.Z.Add(wantOffset[2]) {
		t.Fatalf("carried position=(%d,%d,%d), want composed piece", product.X.Raw(), product.Y.Raw(), product.Z.Raw())
	}
	if product.Move.Bank != factory.Move.Bank+11 || product.Move.Heading != factory.Move.Heading+22 || product.Move.Pitch != factory.Move.Pitch+33 {
		t.Fatalf("carried orientation bank/heading/pitch=%d/%d/%d", product.Move.Bank, product.Move.Heading, product.Move.Pitch)
	}
	coll := system.Collisions[product.Handle]
	if coll.VX != 1234 || coll.VZ != -5678 || coll.Speed != 9012 {
		t.Fatalf("carried ground velocity=(%d,%d) speed=%d, want carrier mover", coll.VX, coll.VZ, coll.Speed)
	}
	if coll.OldAnchor == oldAnchor {
		t.Fatal("carried setter did not move cached footprint anchor")
	}
	if got, present := grid.OccupantAt(coll.OldAnchor); !present || got != coll.ID {
		t.Fatalf("carried footprint stamp=(%d,%t), want product %d", got, present, coll.ID)
	}
	if _, present := grid.OccupantAt(oldAnchor); present {
		t.Fatal("old footprint remained stamped after carried cross-cell update")
	}
	delete(system.Collisions, factory.Handle)
	system.SyncCarriedMotion(w)
	if coll.VX != 0 || coll.VZ != 0 || coll.Speed != 0 || product.Move.Speed != 0 {
		t.Fatalf("carried stale velocity not zeroed: (%d,%d) speed=%d unit=%d", coll.VX, coll.VZ, coll.Speed, product.Move.Speed.Raw())
	}

	x, y, z := product.X, product.Y, product.Z
	if _, ok := DetachCargo(w, product.Handle); !ok {
		t.Fatal("detach failed")
	}
	if product.X != x || product.Y != y || product.Z != z {
		t.Fatal("detach wrote product position")
	}
}

func TestAttachFactoryProductRejectsEstablishedGates(t *testing.T) {
	w := newMovementFixtureWorld(8)
	factoryDef := &content.UnitDef{UnitName: "armlab", MaxDamage: 100}
	productDef := &content.UnitDef{UnitName: "armflash", MaxDamage: 100, BMCode: 1}
	fh, _ := w.Create(factoryDef, 0, 0, 0, 0)
	ph, _ := w.Create(productDef, 0, 0, 0, 0)
	other, _ := w.Create(productDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Attachment.Cargo = []pool.Handle{other}
	if AttachFactoryProduct(w, factory.Handle, product.Handle, 0) {
		t.Fatal("factory product carrying cargo was accepted")
	}
	if product.Attachment.Carrier != 0 || len(factory.Attachment.Cargo) != 0 {
		t.Fatal("rejected attach mutated shared attachment state")
	}
	product.Attachment.Cargo = nil
	product.Flags |= units.BuildingClassStatus
	if AttachFactoryProduct(w, factory.Handle, product.Handle, 0) {
		t.Fatal("building-class product was accepted as factory cargo")
	}
	product.Flags &^= units.BuildingClassStatus
	factory.Attachment.Carrier = other
	if AttachFactoryProduct(w, factory.Handle, product.Handle, 0) {
		t.Fatal("carried factory was accepted as a carrier")
	}
	if product.Attachment.Carrier != 0 {
		t.Fatal("rejected carried-factory attach mutated product")
	}
}
