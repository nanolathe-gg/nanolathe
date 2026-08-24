package movement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// syntheticTerrain creates a CellW x CellH terrain with seaLevel and all cells
// initialized to empty (0xFFFF) and height 0 [fmt tnt][GAP T14].
func syntheticTerrain(w, h int32, seaLevel uint8) *world.Terrain {
	t := &world.Terrain{
		CellW:    w,
		CellH:    h,
		SeaLevel: seaLevel,
		Plot:     make([]world.PlotCell, int(w*h)),
	}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone) // [fmt tnt] 0xFFFF none [GAP T14]
		t.Plot[i].SetHeight(0)
		t.Plot[i].SetMinHeight(0)
		t.Plot[i].SetMaxHeight(0)
	}
	return t
}

func setHeight(t *world.Terrain, cx, cz int32, h uint8) {
	c := t.PlotAt(cx, cz)
	if c == nil {
		return
	}
	c.SetHeight(h)
	c.SetMinHeight(h)
	c.SetMaxHeight(h)
}

func TestProfileFromMovementClass(t *testing.T) {
	mc := &content.MovementClass{
		FootprintX:    3,
		FootprintZ:    3,
		MaxWaterDepth: 12,
		MinWaterDepth: 0,
		MaxSlope:      15,
		BadSlope:      7,
		MaxWaterSlope: 30,
		BadWaterSlope: 15,
	}
	mc.CanonicalKey = "test"
	p := NewProfile(mc)
	if p.FootPrintX != 3 || p.FootPrintZ != 3 {
		t.Fatalf("footprint %d/%d want 3/3", p.FootPrintX, p.FootPrintZ)
	}
	if p.MaxWaterDepth != 12 || p.MinWaterDepth != 0 {
		t.Fatalf("depth max=%d min=%d want 12/0", p.MaxWaterDepth, p.MinWaterDepth)
	}
	if p.MaxSlope != 15 || p.BadSlope != 7 || p.MaxWaterSlope != 30 || p.BadWaterSlope != 15 {
		t.Fatalf("slopes max=%d bad=%d mws=%d bws=%d want 15/7/30/15", p.MaxSlope, p.BadSlope, p.MaxWaterSlope, p.BadWaterSlope)
	}
	// Clamp behavior: large values clamp to 255 for byte fields [02 "Movement class record"].
	mc2 := &content.MovementClass{MaxSlope: 999, BadSlope: -5, MaxWaterSlope: 999, BadWaterSlope: -1}
	p2 := NewProfile(mc2)
	if p2.MaxSlope != 255 || p2.BadSlope != 0 || p2.MaxWaterSlope != 255 || p2.BadWaterSlope != 0 {
		t.Fatalf("clamp failed max=%d bad=%d mws=%d bws=%d", p2.MaxSlope, p2.BadSlope, p2.MaxWaterSlope, p2.BadWaterSlope)
	}
	if NewProfile(nil).FootPrintX != 0 {
		t.Fatalf("nil input should return zero profile")
	}
}

func TestLandCellPassable(t *testing.T) {
	// Land cell: height 20 above seaLevel 10 => depth 0, no feature, flat
	// ground profile MaxWaterDepth 12 allows land [02 "Movement class record"].
	ter := syntheticTerrain(4, 4, 10)
	for z := int32(0); z < 4; z++ {
		for x := int32(0); x < 4; x++ {
			setHeight(ter, x, z, 20) // [fmt tnt] height byte 20 [04 §6.1] land
		}
	}
	ground := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MaxSlope: 15, BadSlope: 7}
	if !ground.IsPassable(ter, 1, 1) {
		t.Fatalf("land cell should be passable for ground")
	}
	if !ground.IsPassableGround(ter, 1, 1) {
		t.Fatalf("ground wrapper should be passable")
	}
	if ground.Classify(ter, 1, 1) == ClassBlocked {
		t.Fatalf("land classify should not be blocked")
	}
}

func TestDeepWaterImpassableForGround(t *testing.T) {
	// Deep water: seaLevel 50, height 5 => depth 45 > MaxWaterDepth 12 => blocked for ground [04 §6.1].
	ter := syntheticTerrain(4, 4, 50)
	for z := int32(0); z < 4; z++ {
		for x := int32(0); x < 4; x++ {
			setHeight(ter, x, z, 5)
		}
	}
	ground := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MaxSlope: 15, BadSlope: 7}
	if ground.IsPassable(ter, 1, 1) {
		t.Fatalf("deep water should be impassable for ground (depth 45 > max 12)")
	}
	// Same cell via generic and medium dispatch
	if ground.CanTraverse(ter, 1, 1, MediumGround) {
		t.Fatalf("CanTraverse ground should be false on deep water")
	}
}

func TestShipBandAcceptance(t *testing.T) {
	// Ship needs MinWaterDepth 15 [research/formats/tdf.md] MinWaterDepth.
	// Deep water depth 45 => pass, land depth 0 => fail.
	ter := syntheticTerrain(4, 4, 40)
	// Fill deep water
	for z := int32(0); z < 4; z++ {
		for x := int32(0); x < 4; x++ {
			setHeight(ter, x, z, 10) // depth 30
		}
	}
	ship := Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: 15, MaxWaterDepth: 0, MaxSlope: 12}
	if !ship.IsPassable(ter, 1, 1) {
		t.Fatalf("deep water depth 30 should be passable for ship min 15")
	}
	if !ship.IsPassableShip(ter, 1, 1) {
		t.Fatalf("ship wrapper pass")
	}
	// Land cell: height 45 => depth 0 < min 15 => blocked
	setHeight(ter, 2, 2, 45)
	if ship.IsPassable(ter, 2, 2) {
		t.Fatalf("land depth 0 should be impassable for ship min 15")
	}
	// Shallow water depth 5 <15 => blocked
	setHeight(ter, 1, 1, 35) // sea 40 - 35 =5
	if ship.IsPassable(ter, 1, 1) {
		t.Fatalf("shallow water depth 5 should be impassable for ship min 15")
	}
	// Deep again pass via CanTraverse
	setHeight(ter, 1, 1, 10)
	if !ship.CanTraverse(ter, 1, 1, MediumShip) {
		t.Fatalf("deep water CanTraverse ship should be true")
	}
}

func TestSlopeRejectionAtBadSlopeBoundary(t *testing.T) {
	// Use hover profile where BadSlope == MaxSlope ==12 so Bad boundary == Max boundary.
	// Slope is max cardinal neighbor diff [04 §6.1] computed via PlotAt heights.
	// Center 0, neighbor 12 diff 12 => pass (==limit). Center 0 neighbor 13 diff 13 => blocked.
	// This locks the BadSlope/MaxSlope rejection semantics [02 "Movement class record"].
	ter := syntheticTerrain(3, 3, 0) // land, sea 0 so no water
	for z := int32(0); z < 3; z++ {
		for x := int32(0); x < 3; x++ {
			setHeight(ter, x, z, 0)
		}
	}
	hover := Profile{FootPrintX: 1, FootPrintZ: 1, MaxSlope: 12, BadSlope: 12, MaxWaterSlope: 255, BadWaterSlope: 255}
	// Flat: slope 0 => clear
	if hover.Classify(ter, 1, 1) != ClassClear {
		t.Fatalf("flat should be clear")
	}
	// Set east neighbor to 12 => slope 12 == limit => still passable (steep? but hover Bad==Max so clear)
	setHeight(ter, 2, 1, 12)
	if !hover.IsPassable(ter, 1, 1) {
		t.Fatalf("slope 12 == Max 12 should be passable")
	}
	// Now 13 => exceeds Max 12 => blocked
	setHeight(ter, 2, 1, 13)
	if hover.IsPassable(ter, 1, 1) {
		t.Fatalf("slope 13 > Max 12 should be blocked")
	}
	if hover.Classify(ter, 1, 1) != ClassBlocked {
		t.Fatalf("slope 13 should classify blocked")
	}
	// Reset neighbor to 12, check ground profile with Bad 7 Max 15 distinction:
	// slope 8 => steep but passable [04 §6.1].
	setHeight(ter, 2, 1, 8)
	ground := Profile{FootPrintX: 1, FootPrintZ: 1, MaxSlope: 15, BadSlope: 7}
	if !ground.IsPassable(ter, 1, 1) {
		t.Fatalf("slope 8 with Bad 7 Max 15 should still be passable (steep)")
	}
	if ground.Classify(ter, 1, 1) != ClassSteep {
		t.Fatalf("slope 8 should be steep, got %d", ground.Classify(ter, 1, 1))
	}
	if ground.IsSteep(ter, 1, 1) != true {
		t.Fatalf("IsSteep should be true")
	}
	setHeight(ter, 2, 1, 16)
	if ground.IsPassable(ter, 1, 1) {
		t.Fatalf("slope 16 > Max 15 should be blocked")
	}
}

func TestHoverBandBehavior(t *testing.T) {
	// Hover ignores depth limits (both Min/Max 0 => unlimited) and uses
	// MaxWaterSlope 255 to cross steep sea floor while MaxSlope 12 limits land
	// [research/formats/tdf.md] MaxWaterSlope, [04 §9.1] hover band.
	ter := syntheticTerrain(4, 4, 30)
	// Land cells: height 35 => depth 0
	for z := int32(0); z < 4; z++ {
		for x := int32(0); x < 4; x++ {
			setHeight(ter, x, z, 35)
		}
	}
	hover := Profile{FootPrintX: 1, FootPrintZ: 1, MaxSlope: 12, BadSlope: 12, MaxWaterSlope: 255, BadWaterSlope: 255}
	ground := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MaxSlope: 12, BadSlope: 12}
	if !hover.IsPassableHover(ter, 1, 1) {
		t.Fatalf("hover should pass land cell")
	}
	if !hover.CanTraverse(ter, 1, 1, MediumHover) {
		t.Fatalf("hover CanTraverse land")
	}
	// Water deep: height 5 depth 25
	for z := int32(0); z < 4; z++ {
		for x := int32(0); x < 4; x++ {
			setHeight(ter, x, z, 5)
		}
	}
	// Make center flat to isolate depth vs slope: all heights 5 => slope 0
	if !hover.IsPassable(ter, 1, 1) {
		t.Fatalf("hover should pass deep water depth 25 (no Max limit) [04 §9.1]")
	}
	if ground.IsPassable(ter, 1, 1) {
		t.Fatalf("ground should fail deep water depth 25 > max 12 [04 §6.1]")
	}
	// Ship band on same deep water should pass (Min 15)
	ship := Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: 15, MaxSlope: 12}
	if !ship.IsPassableShip(ter, 1, 1) {
		t.Fatalf("ship should pass deep water")
	}
	// Hover over steep water: water slope check uses 255, so steep sea floor 50 diff still passes
	// Keep seaLevel 30 water, center 5, neighbor 55 diff 50
	setHeight(ter, 1, 1, 5)
	setHeight(ter, 2, 1, 55) // neighbor diff 50
	if !hover.IsPassable(ter, 1, 1) {
		t.Fatalf("hover water slope 50 should be passable with MaxWaterSlope 255")
	}
	// Same slope on land would be blocked (land Max 12): make land heights with diff 50
	landTer := syntheticTerrain(4, 4, 0)
	for z := int32(0); z < 4; z++ {
		for x := int32(0); x < 4; x++ {
			setHeight(landTer, x, z, 0)
		}
	}
	setHeight(landTer, 1, 1, 0)
	setHeight(landTer, 2, 1, 50)
	if hover.IsPassable(landTer, 1, 1) {
		t.Fatalf("hover land slope 50 should be blocked (MaxSlope 12) even though water slope would pass")
	}
	// CanOccupy footprint 2x2 over land should also be passable for hover when all cells land flat
	footTer := syntheticTerrain(5, 5, 0)
	for z := int32(0); z < 5; z++ {
		for x := int32(0); x < 5; x++ {
			setHeight(footTer, x, z, 10)
		}
	}
	hov2 := Profile{FootPrintX: 2, FootPrintZ: 2, MaxSlope: 12, BadSlope: 12, MaxWaterSlope: 255, BadWaterSlope: 255}
	if !hov2.CanOccupy(footTer, 1, 1) {
		t.Fatalf("2x2 footprint over flat land should be occupiable for hover")
	}
	// Place a void cell inside footprint => blocked
	footTer.PlotAt(2, 2).SetFeature(world.PlotFeatureVoid) // [fmt tnt] void 0xFFFD [GAP T14]
	if hov2.CanOccupy(footTer, 1, 1) {
		t.Fatalf("footprint covering void should be blocked [04 §6.2]")
	}
}

// real map walk is asset-guarded and cheap: loads one retail map if present
// and spot-checks that at least one land cell and one water cell are found
// and that a retail ground profile agrees with intuitive passability.
// Never fails the suite when retail assets are absent.
func TestRealMapOptional(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if h := os.Getenv("HOME"); h != "" {
			root = filepath.Join(h, "TotalAnnihilation")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skip("mount failed: " + err.Error())
	}
	// Try to load a common map via world.Load. This exercises real SeaLevel
	// and height bytes [fmt tnt] without copying them.
	// Use Catalog nil to load via fallback path construction: maps/<key>.tnt
	// We attempt "Comet Catcher" which is small; fallback to any loadable.
	candidates := []string{"Comet Catcher", "Ashap Plateau", "The Pass"}
	var ter *world.Terrain
	var loadErr error
	for _, key := range candidates {
		ter, loadErr = world.Load(fs, nil, key)
		if loadErr == nil && ter != nil {
			break
		}
	}
	if loadErr != nil || ter == nil {
		t.Skip("no retail map could be loaded: " + filepath.Join(root, "maps"))
	}
	// Ground profile similar to KBOTSS2: MaxWaterDepth 12, MaxSlope 32 [research/formats/tdf.md].
	ground := Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MaxSlope: 32, BadSlope: 16}
	// Scan for at least one passable land and one impassable deep water if sea >0
	foundPass := false
	foundBlock := false
	for cz := int32(0); cz < ter.CellH && (!foundPass || !foundBlock); cz++ {
		for cx := int32(0); cx < ter.CellW && (!foundPass || !foundBlock); cx++ {
			pass := ground.IsPassable(ter, cx, cz)
			if pass {
				foundPass = true
			} else {
				// Could be void/feature/slope/water depth; count as block example
				foundBlock = true
			}
		}
	}
	if !foundPass {
		t.Fatalf("expected at least one passable cell for ground on map CellW=%d CellH=%d SeaLevel=%d", ter.CellW, ter.CellH, ter.SeaLevel)
	}
	// Not asserting foundBlock strictly, as a tiny dry map might be fully passable,
	// but most maps have void edges.
	t.Logf("real map walk: CellW=%d CellH=%d SeaLevel=%d foundPass=%v foundBlock=%v", ter.CellW, ter.CellH, ter.SeaLevel, foundPass, foundBlock)
}
