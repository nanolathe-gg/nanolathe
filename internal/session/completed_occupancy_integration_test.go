package session

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func completedOccupancySessionTerrain(w, h int32) *world.Terrain {
	terrain := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, w*h)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	return terrain
}

func completedOccupancyBuildingDef(name string, footX, footZ int32, yard string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
		UnitName:         name, ObjectName: name, MaxDamage: 100, Limit: -1,
		FootprintX: footX, FootprintZ: footZ, YardMap: yard, BMCode: 0,
		MobilityDomain: content.MobilityFixed, MaxSlope: 255,
		MaxWaterDepth: 10000, MinWaterDepth: -10000,
	}
}

func TestCompletedBuildingPreviewAndDirectPlacementAgreeAcrossDeath(t *testing.T) {
	def := completedOccupancyBuildingDef("previewyard", 3, 3, "o")
	def.Script = fixtureCOBProgram()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	terrain := completedOccupancySessionTerrain(12, 12)
	unitWorld := units.NewSliced(4, cat)
	extent, err := world.NewFootprintExtent(def.FootprintX, def.FootprintZ)
	if err != nil {
		t.Fatal(err)
	}
	anchor := world.NewFootprintAnchor(3, 4)
	center, err := world.CenterForFootprint(anchor, extent)
	if err != nil {
		t.Fatal(err)
	}
	h, err := unitWorld.Create(def, 0, center.X(), center.Y(), center.Z())
	if err != nil {
		t.Fatal(err)
	}
	build := construction.NewService(terrain, cat, unitWorld, nil)
	if err := build.RegisterBuildingPlacement(unitWorld.Unit(h)); err != nil {
		t.Fatal(err)
	}
	s := &Session{World: terrain, Catalog: cat, Units: unitWorld, Build: build}
	yard, err := world.ParseYardMap(def.YardMap, int(def.FootprintX), int(def.FootprintZ))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := world.PlacementRulesForUnit(cat, def)
	if err != nil {
		t.Fatal(err)
	}
	rect, ok := build.PlacementForProduct(h)
	if !ok {
		t.Fatal("completed building placement record missing")
	}
	direct := func() error {
		_, err := terrain.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: 0})
		return err
	}
	preview := func() error {
		_, err := s.PreviewPlacement(rect.MinX(), rect.MinZ(), def, def.FootprintX, def.FootprintZ, 0)
		return err
	}
	if direct() == nil || preview() == nil {
		t.Fatal("direct or preview placement accepted the live completed building")
	}

	unitWorld.OnDeathExtra = func(handle pool.Handle, _ units.DeathCause, _ *units.Unit) {
		build.ReleasePlacement(handle)
	}
	unitWorld.Destroy(h, units.DeathKilled)
	if result := unitWorld.FinalizeDeath(h, 1); !result.Freed {
		t.Fatalf("death finalization = %#v", result)
	}
	directErr, previewErr := direct(), preview()
	if directErr != nil || previewErr != nil {
		t.Fatalf("direct and preview placement did not both accept after death: direct=%v preview=%v", directErr, previewErr)
	}
}

func TestStrictCreateYardTransactionRestampsBeforeAttachment(t *testing.T) {
	root := t.TempDir()
	writeCompositionModel(t, root, "yardcreateoccupancy", 1)
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "yardcreateoccupancy.cob"), completedOccupancyYardOpenCOB(), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	def := completedOccupancyBuildingDef("yardcreateoccupancy", 3, 1, "cCc")
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	terrain := completedOccupancySessionTerrain(12, 12)
	unitWorld := units.NewSliced(4, cat)
	s := &Session{
		World: terrain, Catalog: cat, Units: unitWorld,
		rngSim: rng.NewSimulation(77), rngCrt: rng.NewCRT(9), rngInitialized: true,
		publication: newPublicationState(frame.NewEventBuffer(frame.Limits{})),
	}
	s.Build = construction.NewService(terrain, cat, unitWorld, nil)
	unitWorld.SetCOBSource(fs, globalCobLoader)
	unitWorld.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(fs, u) })
	extent, err := world.NewFootprintExtent(def.FootprintX, def.FootprintZ)
	if err != nil {
		t.Fatal(err)
	}
	center, err := world.CenterForFootprint(world.NewFootprintAnchor(3, 3), extent)
	if err != nil {
		t.Fatal(err)
	}
	h, err := unitWorld.Create(def, 0, center.X(), center.Y(), center.Z())
	if err != nil {
		t.Fatalf("strict building Create: %v", err)
	}
	u := unitWorld.Unit(h)
	if u == nil || u.COBBinding() == nil || !u.COBBinding().Callbacks.CreateInvoked() {
		t.Fatalf("strict Create did not attach initialized binding: %#v", u)
	}
	if !u.YardOpen {
		t.Fatal("Create-time port-18 request did not commit the yard-open bit")
	}
	rect, ok := s.Build.PlacementForProduct(h)
	if !ok {
		t.Fatal("strict Create lost its pre-Create placement record")
	}
	for x := rect.MinX(); x < rect.MaxX(); x++ {
		cell := terrain.PlotAt(x, rect.MinZ())
		if cell.OccupantA() != 0 || !cell.StructureYard() {
			t.Fatalf("Create-time open transaction cell %d = occupant %d yard=%t", x, cell.OccupantA(), cell.StructureYard())
		}
	}
}

func completedOccupancyYardOpenCOB() []byte {
	code := []uint32{
		0x10021001, 18,
		0x10021001, 1,
		0x10082000,
		0x10065000,
	}
	const headerSize = 44
	offScriptIndex := headerSize + len(code)*4
	offScriptNames := offScriptIndex + 4
	offPieceNames := offScriptNames + 4
	stringsStart := offPieceNames + 4
	createNameOffset := stringsStart
	pieceNameOffset := createNameOffset + len("Create") + 1
	data := make([]byte, pieceNameOffset+len("modelroot")+1)
	put := func(off int, value uint32) { binary.LittleEndian.PutUint32(data[off:], value) }
	put(0, 4)
	put(4, 1)
	put(8, 1)
	put(12, uint32(len(code)))
	put(24, uint32(offScriptIndex))
	put(28, uint32(offScriptNames))
	put(32, uint32(offPieceNames))
	put(36, headerSize)
	put(40, uint32(createNameOffset))
	for i, word := range code {
		put(headerSize+i*4, word)
	}
	put(offScriptIndex, 0)
	put(offScriptNames, uint32(createNameOffset))
	put(offPieceNames, uint32(pieceNameOffset))
	copy(data[createNameOffset:], "Create\x00")
	copy(data[pieceNameOffset:], "modelroot\x00")
	return data
}
