package ai

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// This locks Nanolathe's bounded malformed-source policy, not an inferred
// retail empty-yard default: the shared parser fills exhausted text with o.
// Both placement helpers must see that same compiled footprint [fmt fbi].
func TestCompiledEmptyYardUsesSharedPlacementPolicy(t *testing.T) {
	for _, source := range []struct{ name, yard string }{
		{"absent", ""}, {"empty", "YardMap=;"}, {"whitespace", "YardMap=   ;"},
	} {
		t.Run(source.name, func(t *testing.T) {
			fbi := fmt.Sprintf("[UNITINFO]{UnitName=yardcase;Copyright=Copyright 1997 Humongous Entertainment. All rights reserved.;BMcode=0;FootprintX=2;FootprintZ=2;%s}", source.yard)
			var archive bytes.Buffer
			if err := vfs.WriteArchive(&archive, []vfs.ArchiveFile{{Path: "units/yardcase.fbi", Data: []byte(fbi)}}, vfs.ArchiveWriteOptions{}); err != nil {
				t.Fatal(err)
			}
			fs := vfs.New()
			t.Cleanup(func() { _ = fs.Close() })
			if _, err := fs.MountArchiveReader("yardcase.ufo", bytes.NewReader(archive.Bytes()), int64(archive.Len()), 1, vfs.ArchiveOptions{}); err != nil {
				t.Fatal(err)
			}
			defs, err := content.CompileUnits(fs)
			if err != nil {
				t.Fatal(err)
			}
			def := defs["yardcase"]
			if def == nil || def.BMCode != 0 {
				t.Fatal("compiled source must retain a building definition")
			}
			for _, extractor := range []float64{0, 0.001} {
				candidate := *def
				candidate.ExtractsMetal = extractor
				reference := candidate
				reference.YardMap = "o"
				terrain := placementTerrain(40, 40, 3)
				got := makePlacementManager(&content.Catalog{Units: map[string]*content.UnitDef{"yardcase": &candidate}}, terrain, 3)
				want := makePlacementManager(&content.Catalog{Units: map[string]*content.UnitDef{"yardcase": &reference}}, terrain, 3)
				a, b := PlaceCandidate(got, "yardcase", terrain), PlaceCandidate(want, "yardcase", terrain)
				if a.Valid != b.Valid || a.Helper != b.Helper || a.Reason != b.Reason || a.CellX != b.CellX || a.CellZ != b.CellZ || got.Strategic.Radius != want.Strategic.Radius || got.RNG.State != want.RNG.State || got.RNG.Draws() != want.RNG.Draws() {
					t.Fatalf("extractor=%v: empty yard %+v radius=%d draws=%d differs from occupied policy %+v radius=%d draws=%d", extractor, a, got.Strategic.Radius, got.RNG.Draws(), b, want.Strategic.Radius, want.RNG.Draws())
				}
				if extractor == 0 && !a.Valid {
					t.Fatalf("ordinary empty-yard building was not placeable: %+v", a)
				}
				if extractor != 0 && (a.Helper != HelperA || got.RNG.Draws() != 1 || got.Strategic.Radius != placementGrowthWorld) {
					t.Fatalf("extractor must reach the ordinary selector before empty-spot failure: %+v", a)
				}
			}
		})
	}
}
