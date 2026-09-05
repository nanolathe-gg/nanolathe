package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestInitCommonRetainsCollisionCachePair locks [R-DMG-01 §13]: neither the
// reservation nor the common initializer writes the quantized cell pair, so a
// reused record carries its last occupant's pair into its next life. Clearing
// it here would deliver a feature contact retail suppresses.
func TestInitCommonRetainsCollisionCachePair(t *testing.T) {
	p := &Projectile{CacheCellX: 7, CacheCellZ: 9, CachedFloorHeight: 33}
	InitCommon(p, 100, Vec3{X: 1 << 16, Y: 2 << 16, Z: 3 << 16}, nil, 0, 0, 0, -1, nil)
	if p.CacheCellX != 7 || p.CacheCellZ != 9 {
		t.Fatalf("common initializer touched the cache pair: (%d,%d), want (7,9) [R-DMG-01 §13]", p.CacheCellX, p.CacheCellZ)
	}
	if p.CachedFloorHeight != 33 {
		t.Fatalf("common initializer touched the floor scratch: %d, want 33 [R-DMG-01 §14]", p.CachedFloorHeight)
	}
}

// TestCachePairWrittenOnlyByFeatureContact locks the write discipline of
// [06 §8.1] step 6 as restated in [R-DMG-01 §13]: a featureless in-map tick
// leaves the pair alone; a feature contact that passes the height test writes
// it; a second contact with the same pair is suppressed and leaves the cache
// untouched — including a FRESH record whose pair is a previous occupant's.
func TestCachePairWrittenOnlyByFeatureContact(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 20, 20
	weapon := wu1913Weapon(10)

	// Featureless cell: no write.
	p := &Projectile{ShooterSide: 0, CacheCellX: -5, CacheCellZ: -5,
		Pos: Vec3{X: cellCentre(cx + 3), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz + 3)}}
	if _, feature, _, _, _ := checkCollision(p, weapon, w, ter, nil); feature != nil {
		t.Fatal("featureless cell resolved a feature")
	}
	if p.CacheCellX != -5 || p.CacheCellZ != -5 {
		t.Fatalf("featureless tick wrote the cache pair: (%d,%d) [R-DMG-01 §13]", p.CacheCellX, p.CacheCellZ)
	}

	// Feature contact: written, and the contact is delivered.
	ter.PlotAt(cx, cz).SetFeature(0)
	p.Pos = Vec3{X: cellCentre(cx), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz)}
	if _, feature, _, _, _ := checkCollision(p, weapon, w, ter, nil); feature == nil {
		t.Fatal("first contact with the feature cell was not delivered")
	}
	if p.CacheCellX != cx || p.CacheCellZ != cz {
		t.Fatalf("feature contact did not write the cache pair: (%d,%d), want (%d,%d)", p.CacheCellX, p.CacheCellZ, cx, cz)
	}

	// A fresh record that inherits the pair — the reused-slot case — has its
	// first contact with that same cell suppressed, and the pair stays.
	reused := &Projectile{ShooterSide: 0, CacheCellX: p.CacheCellX, CacheCellZ: p.CacheCellZ,
		Pos: Vec3{X: cellCentre(cx), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz)}}
	if _, feature, _, _, _ := checkCollision(reused, weapon, w, ter, nil); feature != nil {
		t.Fatal("a reused record's inherited pair must suppress its first contact with the same cell [R-DMG-01 §13]")
	}
	if reused.CacheCellX != cx || reused.CacheCellZ != cz {
		t.Fatalf("suppressed contact touched the cache pair: (%d,%d)", reused.CacheCellX, reused.CacheCellZ)
	}
}

// TestFloorScratchWrittenInMapOnly locks [R-DMG-01 §14]: the gate caches the
// cell's average floor height on every in-map tick, before the unit slots,
// and an off-map record retires without sampling.
func TestFloorScratchWrittenInMapOnly(t *testing.T) {
	w, ter := newContactFixture(t)
	const cx, cz = 12, 12
	cell := ter.PlotAt(cx, cz)
	want := int16((uint16(cell.MaxHeight()) + uint16(cell.MinHeight())) / 2)

	p := &Projectile{ShooterSide: 0, CachedFloorHeight: -1,
		Pos: Vec3{X: cellCentre(cx), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz)}}
	checkCollision(p, wu1913Weapon(10), w, ter, nil)
	if p.CachedFloorHeight != want {
		t.Fatalf("in-map tick: floor scratch = %d, want %d [R-DMG-01 §14]", p.CachedFloorHeight, want)
	}

	off := &Projectile{ShooterSide: 0, CachedFloorHeight: -1,
		Pos: Vec3{X: numeric.Fixed(-1), Y: numeric.Fixed(8 * 65536), Z: cellCentre(cz)}}
	if _, _, _, isOffMap, _ := checkCollision(off, wu1913Weapon(10), w, ter, nil); !isOffMap {
		t.Fatal("negative X must be off-map")
	}
	if off.CachedFloorHeight != -1 {
		t.Fatalf("off-map retirement sampled a floor: %d [R-DMG-01 §14]", off.CachedFloorHeight)
	}
}
