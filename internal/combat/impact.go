package combat

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// NoExplodeRetirement reports whether the projectile should retire after impact
// considering the noexplode flag [06 §13.2] C28.
//
// In the ordinary impact branch, no-explode gates ONLY the normal projectile
// retirement and follow-camera finalization block; shake, sounds, effects,
// authoritative direct/area damage always execute [06 §13.2] C28.
// Retirements outside that block ignore the flag entirely: LOS expiry,
// burst-root completion, lava underwater self-expire, off-map exit [06 §13.2]
// C28. A separate water/hazard override retires regardless of noexplode
// (opaque liquid mode, water-classified cell, no direct unit) [06 §13.2] C28.
func NoExplodeRetirement(noExplode bool, isOrdinaryImpactBranch bool, isOffMap bool, isWaterHazardOverride bool) bool {
	if isOffMap {
		return true // off-map retires regardless, ignores noexplode [06 §8.1] [06 §13.2] C28
	}
	if isWaterHazardOverride {
		return true // water/hazard override retires regardless [06 §13.2] C28
	}
	if !isOrdinaryImpactBranch {
		return true // retirements outside ordinary impact block ignore flag [06 §13.2] C28
	}
	if noExplode {
		return false // gates ONLY normal retirement block [06 §13.2] C28
	}
	return true
}

// CollisionSlotYGate distinguishes the two fixed collision slots [P1-07 §2.1].
// Slot0 requires projectile height strictly below unit upper/reference bound and
// has NO lower-bound test; slot1 requires inclusively between lower and upper
// [P1-07 §2.1]. Visitation is slot0→slot1 [P1-07 §4].
func CollisionSlotYGate(projectileY, lower, upper int32, slot int) bool {
	if slot == 0 {
		return projectileY < upper // [P1-07 §2.1] slot0: Y < upper, no lower gate
	}
	return lower <= projectileY && projectileY <= upper // [P1-07 §2.1] slot1: lower<=Y<=upper
}

// Coordinate conversion and fringe-anchor resolution are owned by the world
// helpers; see [03 §2.1] and [06 §8.1]. Keep their behavior at those call sites
// rather than duplicating it in impact selection.

// FeatureCacheSuppressed reports whether a feature contact should be suppressed
// due to cached quantized cell pair [06 §8.1] C28.
//
// A cached quantized cell pair suppresses repeated feature contact in the same
// cached cell [06 §8.1] C28. A new cell updates the cache and selects impact
// without a direct unit. Cached-cell suppression cancels ONLY that feature's
// impact: the terrain/water ladder later in the SAME resolver call still runs
// and can select another impact [06 §8.1] C28.
func FeatureCacheSuppressed(cache *[2]int32, cellX, cellZ int32) bool {
	if cache == nil {
		return false
	}
	if cache[0] == cellX && cache[1] == cellZ {
		return true // same cached cell suppresses feature contact [06 §8.1] C28
	}
	cache[0] = cellX // new cell updates cache [06 §8.1]
	cache[1] = cellZ
	return false // not seen — process; dedup before radius test [06 §9.3]
}

// EnumerateArea enumerates the rectangular broad phase around impact for area
// damage per [06 §9.3] C26.
//
// Authoritative blast radius is unsigned authored area >>1 world units
// [06 §9.3]. Broad phase extends (radius/16)+1 terrain cells around impact
// cell and is clamped to map [06 §9.3]. It traverses rows by increasing Z,
// then cells by increasing X [06 §9.3]. Both upper map bounds are exclusive
// [06 §9.3]. There is NO impulse or pushing [06 §9.4] C26.
func EnumerateArea(impact PosVec, radius int32, mapW, mapH int32, visit func(cx, cz int32)) {
	if visit == nil {
		return
	}
	if mapW <= 0 || mapH <= 0 {
		return
	}
	// Area quantization reads the signed whole word, then divides toward
	// zero. Collision uses a different arithmetic-shift cell conversion
	// [06 §8.1][06 §9.3].
	impactCX := int32(int16(impact.X.Raw()>>16)) / 16
	impactCZ := int32(int16(impact.Z.Raw()>>16)) / 16
	cells := BroadPhaseRadiusCells(radius) // [06 §9.3]
	minX := impactCX - cells
	if minX < 0 {
		minX = 0
	}
	maxXExclusive := impactCX + cells
	if maxXExclusive > mapW {
		maxXExclusive = mapW
	}
	minZ := impactCZ - cells
	if minZ < 0 {
		minZ = 0
	}
	maxZExclusive := impactCZ + cells
	if maxZExclusive > mapH {
		maxZExclusive = mapH
	}
	for cz := minZ; cz < maxZExclusive; cz++ {
		for cx := minX; cx < maxXExclusive; cx++ {
			visit(cx, cz) // [06 §9.3] rows by increasing Z, then cells by increasing X (I1)
		}
	}
}

// PosVec is a fixed-point world position for area helpers.
type PosVec = Vec3

// UnitForArea is a minimal unit view for area distance tests [06 §9.3].
type UnitForArea struct {
	Handle pool.Handle
	Pos    Vec3
	Min    Vec3 // inclusive model/footprint bounding box min [06 §9.3]
	Max    Vec3 // inclusive max [06 §9.3]
}

// DistanceToBox computes three-dimensional distance from impact point to
// nearest point of target's inclusive bounding box, sqrt truncated toward zero
// and reduced to signed 16-bit world-distance value [06 §9.3] C26.
// Strictly less than radius accepted [06 §9.3].
func DistanceToBox(impact Vec3, u UnitForArea) int32 {
	// Clamp impact to box inclusive.
	var cx, cy, cz int64
	ix, iy, iz := impact.X.Raw(), impact.Y.Raw(), impact.Z.Raw()
	minX, minY, minZ := u.Min.X.Raw(), u.Min.Y.Raw(), u.Min.Z.Raw()
	maxX, maxY, maxZ := u.Max.X.Raw(), u.Max.Y.Raw(), u.Max.Z.Raw()
	if ix < minX {
		cx = int64(minX - ix)
	} else if ix > maxX {
		cx = int64(ix - maxX)
	} else {
		cx = 0
	}
	if iy < minY {
		cy = int64(minY - iy)
	} else if iy > maxY {
		cy = int64(iy - maxY)
	} else {
		cy = 0
	}
	if iz < minZ {
		cz = int64(minZ - iz)
	} else if iz > maxZ {
		cz = int64(iz - maxZ)
	} else {
		cz = 0
	}
	// The separation is the Euclidean distance from the impact point to the
	// victim's box, reduced to a signed 16-bit world-distance value; a recipient
	// is accepted only when that value is strictly below the radius [06 §9.3].
	if cx == 0 && cy == 0 && cz == 0 {
		return 0 // impact on or inside the box has distance zero [06 §9.3]
	}
	// `d = (int16)( trunc(sqrt(dx*dx + dy*dy + dz*dz)) >> 16 )` [06 §9.3]: the
	// square root runs on the raw 16.16 deltas and the shift converts the
	// truncated result to whole world units, so the shift is the only place the
	// fraction is dropped. float64 for the square root is on the I2 allowlist
	// alongside the ballistic discriminant. The shared narrowing retains the
	// signed low word before the arithmetic shift [01 R-DET-01 §1]; the deltas
	// are non-negative here, so the root itself truncates toward zero [01 §8] I3.
	// Each square is rounded before it is summed, as retail's separate x87
	// operations do. Every separation stock content produces keeps the squares
	// inside 53 bits, so the barrier changes no shipped value; it is what holds
	// the association across backends with a fused multiply-add, and near the
	// wrap boundary below the squares are inexact and it is load-bearing.
	fx, fy, fz := float64(cx), float64(cy), float64(cz)
	x2, y2, z2 := float64(fx*fx), float64(fy*fy), float64(fz*fz)
	whole := int64(numeric.TruncateFloat64ToLow32(math.Sqrt(x2+y2+z2))) >> 16
	// The reduction to sixteen bits is a truncating narrowing and therefore
	// WRAPS: [06 §9.3] states that "a distance at or above 32,768 world units
	// wraps negative and passes the acceptance test", which is the whole reason
	// the acceptance test's strictness is worth recording. This site used to
	// SATURATE behind an open-question marker that called the wrap the untraced arm;
	// it is the Established one, and saturating turns a far victim that retail
	// accepts into one that is rejected. Stock content cannot reach it — no
	// shipped weapon has a radius anywhere near 32,767 — but a blast at that
	// separation is decided the wrong way round without this.
	return int32(int16(uint16(whole)))
}

// Deduplication memories per [06 §9.3] C26.

// UnitDedup remembers at most 20 unit pointers but is dedup memory not cap;
// after full, new candidate still processed but not remembered, later occurrence
// can be processed again [06 §9.3] C26. Dedup happens before radius test so
// out-of-radius first sighting can consume entry [06 §9.3].
type UnitDedup struct {
	entries [20]pool.Handle
	used    int
}

// SeenUnit reports whether h was already remembered. If not remembered and
// space remains, it remembers h. After full, returns false (not remembered)
// so caller must still process the candidate [06 §9.3].
func (d *UnitDedup) SeenUnit(h pool.Handle) bool {
	for i := 0; i < d.used; i++ {
		if d.entries[i] == h {
			return true
		}
	}
	if d.used < len(d.entries) {
		d.entries[d.used] = h
		d.used++
	}
	return false // not seen — process; dedup before radius test [06 §9.3]
}

// FeatureDedup remembers at most 64 feature-cell pointers, dedup memory not cap
// [06 §9.3] C26. Feature distance checked before dedup [06 §9.3].
type FeatureDedup struct {
	entries [64]int // encoded cell key cx<<16|cz? Use map from cx*H+cz
	used    int
}

func featureKey(cx, cz int32) int { return int(cx)*10000 + int(cz) }

// SeenFeature reports whether cell was already remembered. Distance is checked
// BEFORE dedup per [06 §9.3], so caller must check distance first.
func (d *FeatureDedup) SeenFeature(cx, cz int32) bool {
	k := featureKey(cx, cz)
	for i := 0; i < d.used; i++ {
		if d.entries[i] == k {
			return true
		}
	}
	if d.used < len(d.entries) {
		d.entries[d.used] = k
		d.used++
	}
	return false
}

// The area sweep itself lives in the combat service's ExplodeWeaponAt, which is
// the single authoritative entry for projectile splash and death explosions
// alike: it carries the unit phase and the feature phase in one ordered victim
// list, using the two dedup memories above [06 §9.3][06 R-WPN-04 §3]. A second,
// unit-only copy of the sweep used to sit here with a stubbed feature phase and
// an unresolved question in its comment; it had no non-test caller and read as
// a live gap in the feature half, so it was removed (AU-14 W-6).

// The feature-ignition gate is not restated here. `firestarter` is read exactly
// once, at the live ignite entry in internal/features, which tests the compiled
// byte with a plain nonzero compare because internal/content already truncated
// the authored integer to the loader's low byte at compile time
// [06 §13.1][06 R-WPN-05 §10][02 "Weapon record"]. The area sweep's feature
// phase above hands that entry the weapon's field directly; a second copy of
// the test here had no non-test caller.
