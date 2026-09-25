package session

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

const placerFixtureW, placerFixtureH = 32, 32

// placerFixtureTNT is an authored canonical (0x2000) TNT of flat cells with
// one terrain-file feature anchor at (10,10), naming record 0 "wallfixture"
// [fmt tnt].
func placerFixtureTNT() []byte {
	le := binary.LittleEndian
	const tileMapOffset = 0x40
	tileMapBytes := (placerFixtureW / 2) * (placerFixtureH / 2) * 2
	attrOffset := tileMapOffset + tileMapBytes
	gfxOffset := attrOffset + placerFixtureW*placerFixtureH*4
	featOffset := gfxOffset + 1024
	miniOffset := featOffset + 132
	b := make([]byte, miniOffset+8)
	le.PutUint32(b[0x00:], 0x2000)
	le.PutUint32(b[0x04:], placerFixtureW)
	le.PutUint32(b[0x08:], placerFixtureH)
	le.PutUint32(b[0x0c:], tileMapOffset)
	le.PutUint32(b[0x10:], uint32(attrOffset))
	le.PutUint32(b[0x14:], uint32(gfxOffset))
	le.PutUint32(b[0x18:], 1)
	le.PutUint32(b[0x1c:], 1)
	le.PutUint32(b[0x20:], uint32(featOffset))
	le.PutUint32(b[0x28:], uint32(miniOffset))
	for i := 0; i < placerFixtureW*placerFixtureH; i++ {
		rec := b[attrOffset+i*4 : attrOffset+i*4+4]
		rec[0] = 20
		feature := uint16(world.PlotFeatureNone)
		if i == 10*placerFixtureW+10 {
			feature = 0
		}
		le.PutUint16(rec[1:3], feature)
	}
	copy(b[featOffset+4:], "wallfixture")
	return b
}

func placerFixtureLoad(t *testing.T, entry community.Features) (*world.Terrain, *content.Catalog) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maps", "placerfixture.tnt"), placerFixtureTNT(), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	wall := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wallfixture"},
		FootprintX:       2, FootprintZ: 2, Height: 20, Filename: "walls", NoDrawUnderGray: true, Blocking: true,
	}
	cat := &content.Catalog{
		Features: map[string]*content.FeatureDef{
			"wallfixture": wall,
			"treefixture": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "treefixture"}, FootprintX: 1, FootprintZ: 1, Filename: "trees", Blocking: true},
			"wreckfixture": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreckfixture"}, FootprintX: 1, FootprintZ: 1, Object: "wreck",
				Damage: 100, NoDrawUnderGray: true},
		},
		Maps: map[string]*content.MapHeader{"placerfixture": {Schemas: []content.MapSchema{{Name: "Schema 0"}}}},
	}
	m := &mission.Mission{TerrainKey: "placerfixture", Schema: mission.Schema{Name: "Schema 0"}}
	terrain, err := loadTerrainStrict(fs, cat, m, entry)
	if err != nil {
		t.Fatal(err)
	}
	return terrain, cat
}

// TestMapFeatureOwnerElevenStampsTerrainFileAnchorsOnly locks the loader half
// of the ProTA 4.8 package behaviour (research/extensions/prota-engine.md
// "Map-owned features drawn without line of sight";
// docs/DESIGN_COMMUNITY_PATCH.md §4.7):
// with the entry table's switch the terrain-file stamp writes placer 11 into
// its anchor, and without it 10. Mission-file features keep 10 and corpses
// keep their owner's slot; the published owner selector carries 11; and the
// PlayerFeatures image restores it [08 R-SAVE-02 §12].
func TestMapFeatureOwnerElevenStampsTerrainFileAnchorsOnly(t *testing.T) {
	off, _ := placerFixtureLoad(t, community.Features{})
	if got := off.PlotAt(10, 10).PlacerNibble(); got != world.TerrainFeaturePlacer {
		t.Fatalf("switch off: terrain-file anchor placer %d, want %d", got, world.TerrainFeaturePlacer)
	}

	terrain, cat := placerFixtureLoad(t, community.Features{MapFeatureOwnerEleven: true})
	if got := terrain.PlotAt(10, 10).PlacerNibble(); got != world.MapOwnedFeaturePlacer {
		t.Fatalf("switch on: terrain-file anchor placer %d, want %d", got, world.MapOwnedFeaturePlacer)
	}
	if got := terrain.PlotAt(11, 11).PlacerNibble(); got != world.TerrainFeaturePlacer {
		t.Fatalf("switch on: fringe placer %d, want the expansion's %d", got, world.TerrainFeaturePlacer)
	}

	s := &Session{Catalog: cat, World: terrain, Features: features.NewService(terrain, nil, nil, nil)}
	s.Features.PopulateFromTerrain()
	if err := stampMissionFeatures(s, &mission.Mission{Features: []mission.FeaturePlacement{placementAt("treefixture", 14, 14)}}); err != nil {
		t.Fatal(err)
	}
	if got := terrain.PlotAt(14, 14).PlacerNibble(); got != world.TerrainFeaturePlacer {
		t.Fatalf("mission-file feature placer %d, want %d", got, world.TerrainFeaturePlacer)
	}
	const owner = 3
	corpse := s.Features.PlaceCorpse(world.Cell{X: 12, Z: 8}, [3]numeric.Fixed{world.CellToWorld(12), 0, world.CellToWorld(8)},
		features.Orientation{}, cat.Features["wreckfixture"], false, owner)
	if corpse == nil {
		t.Fatal("corpse placement refused")
	}
	if got := terrain.PlotAt(12, 8).PlacerNibble(); got != owner {
		t.Fatalf("corpse placer %d, want owner slot %d", got, owner)
	}

	wall := s.Features.InstanceAt(10, 10)
	if selector, ok := featureOwnerSelector(wall); !ok || selector != world.MapOwnedFeaturePlacer {
		t.Fatalf("published owner selector %d/%v, want %d", selector, ok, world.MapOwnedFeaturePlacer)
	}

	image, err := terrain.RetailPlayerFeaturesImage()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := placerFixtureLoad(t, community.Features{})
	if err := restored.RestoreRetailPlayerFeatures(image); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x, z int32
		want uint8
	}{{10, 10, world.MapOwnedFeaturePlacer}, {14, 14, world.TerrainFeaturePlacer}, {12, 8, owner}} {
		if got := restored.PlotAt(c.x, c.z).PlacerNibble(); got != c.want {
			t.Fatalf("restored (%d,%d) placer %d, want %d", c.x, c.z, got, c.want)
		}
	}
}
