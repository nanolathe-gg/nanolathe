package movement

import (
	"math/rand"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// TestRestampRectMatchesPerAnchorClassify locks the restamp's tier cache to
// the per-anchor classifier it replaced: over random terrain, water and
// occupants of mixed age, every anchor a restamp rewrites must read what
// classify answers for it, and the restamp bound must still block the anchors
// whose extent reaches the last column or row [04 R-MOV-03 §3]
// [04 R-SLOPE-01 §3].
func TestRestampRectMatchesPerAnchorClassify(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	profiles := []Profile{
		{MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 32, BadSlope: 16, MaxWaterSlope: 255, BadWaterSlope: 127},
		kbotsSS2, tankDS2, spid3, boats4,
		{FootPrintX: 1, FootPrintZ: 3, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 20, BadSlope: 8, MaxWaterSlope: 40, BadWaterSlope: 10},
	}
	for trial := 0; trial < 40; trial++ {
		w, h := int32(12+rng.Intn(30)), int32(12+rng.Intn(30))
		tr := layerTerrain(w, h, 20)
		for z := int32(0); z < h; z++ {
			for x := int32(0); x < w; x++ {
				lo := uint8(rng.Intn(60))
				setDerived(tr, x, z, lo, lo+uint8(rng.Intn(45)))
			}
		}
		grid := NewOccupancyGrid()
		type occ struct{ id, commit int }
		var occs []occ
		for id := 1; id <= 12; id++ {
			grid.Stamp(Cell{X: int32(rng.Intn(int(w))), Z: int32(rng.Intn(int(h)))}, int16(1+rng.Intn(3)), int16(1+rng.Intn(3)), id)
			occs = append(occs, occ{id, rng.Intn(80)})
		}
		for _, p := range profiles {
			l := NewClassLayer(p, tr, grid)
			for _, o := range occs {
				l.NoteCommit(pool.Handle(o.id), uint32(o.commit))
			}
			l.watermark = uint32(rng.Intn(80))
			fx, fz := l.footprintSize()
			for r := 0; r < 20; r++ {
				x1, z1 := int32(rng.Intn(int(w)+4))-2, int32(rng.Intn(int(h)+4))-2
				x2, z2 := x1+int32(rng.Intn(10))-1, z1+int32(rng.Intn(10))-1
				l.RestampRect(x1, z1, x2, z2)
				for z := max(z1, 0); z <= min(z2, h-1); z++ {
					for x := max(x1, 0); x <= min(x2, w-1); x++ {
						want := LayerBlocked
						if x+fx < w && z+fz < h {
							want = l.classify(x, z)
						}
						if got := l.Value(x, z); got != want {
							t.Fatalf("trial %d footprint %dx%d rect (%d,%d)-(%d,%d): anchor (%d,%d) restamped %d, classify %d",
								trial, fx, fz, x1, z1, x2, z2, x, z, got, want)
						}
					}
				}
			}
		}
	}
}
