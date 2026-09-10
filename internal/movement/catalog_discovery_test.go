package movement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The selected numeric class supplies both the unit footprint and the slope
// classifier; duplicate authored names must not silently change either
// [02 "Movement class record"][04 §6.1].
func TestCatalogFirstMovementNameControlsFootprintAndPassability(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "gamedata"), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[CLASS0] { Name=Tank; FootPrintX=2; FootPrintZ=3; MaxSlope=10; }
[CLASS3] { Name=TANK; FootPrintX=4; FootPrintZ=5; MaxSlope=30; }`)
	if err := os.WriteFile(filepath.Join(dir, "gamedata", "moveinfo.tdf"), data, 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	classes, err := content.CompileMovement(fs)
	if err != nil {
		t.Fatal(err)
	}
	unit := &content.UnitDef{MovementClass: "tAnK", FootprintX: 9, FootprintZ: 9}
	content.ApplyMovementFootprints(map[string]*content.UnitDef{"tankunit": unit}, classes)
	if unit.FootprintX != 2 || unit.FootprintZ != 3 {
		t.Fatalf("unit footprint = %d/%d, want first class 2/3", unit.FootprintX, unit.FootprintZ)
	}
	terrain := syntheticTerrain(16, 16, 0)
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetMinHeight(0)
		terrain.Plot[i].SetMaxHeight(20)
	}
	profile := NewScratchProfile(unit)
	if profile.IsPassable(terrain, 8, 8) {
		t.Fatal("slope 20 admitted by class whose maximum is 10")
	}
	// The later duplicate's threshold would admit this same terrain; it is
	// not an unrelated footprint or map-edge rejection.
	profile.MaxSlope = 30
	if !profile.IsPassable(terrain, 8, 8) {
		t.Fatal("fixture did not distinguish the later duplicate's slope")
	}
}
