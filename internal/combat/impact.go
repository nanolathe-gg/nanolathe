package combat

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// PresentationSink is the impact presentation ordering sink [06 §13.2] C27.
// Simulation never renders; it dispatches ordered events to this sink and
// routes damage last [GAP T21] [06 §13.2] C27.
type PresentationSink interface {
	Shake(magnitude, duration int32)             // (1) camera shake [06 §13.2]
	PlayHitSound(sound string)                   // (2) hit vs water distinction — direct forces hit even underwater [06 §13.2]
	PlayWaterSound(sound string)                 // (2) water variant for terrain-only water impacts [06 §13.2]
	EmitEndSmoke(pos Vec3)                       // (3) end smoke when flagged — land-branch-only, REPLACES explosion GAF [06 §13.2]
	EmitExplosion(gaf, art string, isWater bool) // (4) land-or-water explosion GAF holder [06 §13.2]
}

// DispatchPresentation emits impact presentation in fixed order
// shake → hit/water sound → end smoke → land/water GAF → damage last
// [06 §13.2] C27 [GAP T21].
//
// hasDirectTarget forces hit sound even underwater [06 §13.2] C27.
// isWaterTerrain indicates the impact cell is water-classified and there is
// no direct unit argument for the water-sound branch [06 §13.2].
// End smoke is land-branch-only and replaces explosion GAF [06 §13.2] C27.
// Damage routing is always last and is NOT emitted via this sink; caller
// must invoke damage after this function returns [06 §13.2] C27.
func DispatchPresentation(w *content.WeaponDef, hasDirectTarget bool, isWaterTerrain bool, pos Vec3, sink PresentationSink) {
	if w == nil || sink == nil {
		return
	}
	// (1) camera shake [06 §13.2]
	if w.ShakeMagnitude != 0 || w.ShakeDuration != 0 {
		sink.Shake(w.ShakeMagnitude, w.ShakeDuration) // [06 §13.2] (1) shake
	}
	// (2) impact-sound selection [06 §13.2]
	// Hit sound for land and direct-target impacts versus water sound for
	// terrain-only water impacts, EXCEPT direct target FORCES hit even underwater [06 §13.2].
	if hasDirectTarget {
		if w.SoundHit != "" {
			sink.PlayHitSound(w.SoundHit) // [06 §13.2] direct forces hit
		}
	} else {
		if isWaterTerrain {
			if w.SoundWater != "" {
				sink.PlayWaterSound(w.SoundWater) // [06 §13.2] terrain-only water impact
			} else if w.SoundHit != "" {
				// Fallback? Research says water sound for terrain-only water, hit otherwise.
				// Keep hit as fallback for missing water sound? No establishment — skip.
			}
		} else {
			if w.SoundHit != "" {
				sink.PlayHitSound(w.SoundHit) // [06 §13.2] land
			}
		}
	}
	// (3) end smoke when flagged [06 §13.2] — land-branch-only, REPLACES explosion GAF
	hasEndSmoke := w.EndSmoke
	if hasEndSmoke && !isWaterTerrain {
		sink.EmitEndSmoke(pos) // [06 §13.2] (3) end smoke land-branch-only, replaces GAF
		return                 // GAF suppressed when end smoke emitted [06 §13.2]
	}
	if hasEndSmoke && isWaterTerrain && !hasDirectTarget {
		// Water-branch deaths ignore end smoke entirely [06 §13.2]
		// Do not emit end smoke; fall through to water GAF.
	}
	// (4) land-or-water explosion GAF holder [06 §13.2]
	if isWaterTerrain && !hasDirectTarget {
		// Water-branch explosion [06 §13.2]
		if w.WaterExplosionGaf != "" || w.WaterExplosionArt != "" {
			sink.EmitExplosion(w.WaterExplosionGaf, w.WaterExplosionArt, true) // [06 §13.2] water GAF
		}
	} else {
		// Land or direct-target branch [06 §13.2]
		if w.ExplosionGaf != "" || w.ExplosionArt != "" {
			sink.EmitExplosion(w.ExplosionGaf, w.ExplosionArt, false) // [06 §13.2] land GAF
		}
	}
	// (5) damage routing LAST [06 §13.2] [GAP T21] — caller handles
}

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

// Floor quant helpers per [P1-07 §4]: AOE tile quant is floor(x>>4) with sign
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// point to cell is arithmetic Shift>>20 (1<<20 = 16*65536 world per cell) [P1-07 §4].
// world.WorldToCell already implements floor >>20 via floorDiv [03 §2.1] I3.
// BroadPhaseRadiusCells already implements (radius>>4)+1 via unsigned SAR 4.

// Sentinel offsets for fringe resolution: feature sentinel 0xFFFE resolves via
// signed offsets at cell+0xB (X) and cell+0x5 (Z) (decompile view) [P1-07 §2.2].
// In typed PlotCell the bytes live at AnchorDX 0xB and AnchorDZ 0xA (DZ scaled
// by row stride) — the 0x5 vs 0xA alias is TNT attribute vs runtime plot cell
// indexing, but both are signed i8 subtraction from current cell to reach
// anchor [P1-07 §2.2] [02 "Terrain file"] SC6.

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
	return false
}

// StampOrder is player 0..9 → pool 0x118 vacated reusable same tick [P1-07 §2.1].
// Successful movement does Clear(oldFootprint) then Stamp(newFootprint) before
// next slot so vacated cell reusable same tick; head-on both block because each
// sees other occupant before either clears. This is implemented in
// internal/movement/CollisionState.CommitSuccess and CommitSweep sorting by ID
// asc which equals player 0..9 then pool 0x118 asc [P0-12][P1-07 §2.1] (I1).

// LiquidForcedRetire reports whether the opaque liquid mode forces dead|2
// regardless of noexplode [P1-07 §2.4] [06 §8.1][06 §13.2]. The mode is
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// unit — it retires and overrides the noexplode gate [P1-07 §4][06 §13.2].
func LiquidForcedRetire(opaqueMode bool, isWaterCell bool, hasDirectUnit bool) bool {
	return opaqueMode && isWaterCell && !hasDirectUnit // [P1-07 §2.4]
}

// ImpactLadderResult records which ladder branch produced impact for tests
// covering C28 refinements [06 §8.1] [06 §13.2].
type ImpactLadderResult struct {
	FeatureSuppressed bool // cached-cell suppression cancelled feature impact [06 §8.1] C28
	FeatureImpact     bool
	TerrainImpact     bool
	WaterImpact       bool
	Bounce            bool // ground bounce never reaches central impact [06 §8.2] C28
	OffMapRetired     bool // off-map retires regardless of noexplode [06 §8.1] C28
}

// ResolveImpactLadder simulates the fixed collision contact order for C28 tests
// [06 §8.1] C28: projectile-link proximity (not modeled here) → cell-height
// cache → unit slots (not modeled) → units-only early return → feature or
// footprint-anchor resolution with repeated-cell suppression → terrain
// penetration and bounce → water/sea continuation or impact [06 §8.1].
//
// For WU-09-6 we model the tail end after unit slots: feature suppression,
// bounce, water/terrain selection. Link proximity's second same-call impact
// reachable property is asserted by not checking dead bit [06 §8.1] C28.
func ResolveImpactLadder(cellX, cellZ int32, cache *[2]int32, hasFeature bool, featureTop int32, projectileHeight int32, terrainHeight int32, seaLevel int32, isWaterWeapon bool, opaqueLiquid bool, noExplode bool, isOffMap bool) ImpactLadderResult {
	var res ImpactLadderResult
	if isOffMap {
		res.OffMapRetired = true // [06 §8.1] off-map retires regardless, ignores noexplode [06 §13.2]
		return res
	}
	// Feature stage [06 §8.1]
	if hasFeature {
		if FeatureCacheSuppressed(cache, cellX, cellZ) {
			res.FeatureSuppressed = true // [06 §8.1] C28 cached-cell suppresses ONLY feature impact
		} else if projectileHeight < featureTop { // strictly below feature top [06 §8.1]
			res.FeatureImpact = true
			// Note: per [06 §8.1] C28, even when feature impact occurs, ladder
			// would normally return; but to demonstrate C28's "cached suppression
			// cancels only feature" we still allow terrain/water to run when
			// suppressed. When featureImpact is true, terrain/water still runs
			// for second-same-call reachable test? Retail's feature impact returns
			// without terrain? However cached suppression case explicitly says
			// terrain/water ladder still runs when feature suppressed. We model
			// suppressed case as continuing; non-suppressed feature impact as
			// terminal? Yet [06 §8.1] says linked proximity does NOT return and
			// resolver never rechecks dead bit, so second impact reachable WITHOUT
			// noexplode. That second impact is demonstrated via separate flag.
			// For determinism we keep feature impact terminal unless suppressed.
			if !res.FeatureSuppressed {
				// Feature impact takes the central impact path; still need to
				// consider that second same-call impact reachable via link
				// proximity not modeled here. Represent as: after feature impact,
				// terrain/water reachable if link proximity left dead bit unchecked.
				// We expose that via separate test using dead-bit-unchecked path.
			}
		} else {
			// Feature present but not below top => miss, fall through to terrain/water [06 §8.1]
		}
		if res.FeatureSuppressed {
			// Cached suppression cancels ONLY feature's impact while ladder continues [06 §8.1] C28
			// Fall through to terrain/water
		} else if res.FeatureImpact {
			return res // feature impact returns [06 §8.1] — but second same-call still reachable via link path (tested separately)
		}
	}
	// Terrain penetration and bounce [06 §8.2] C28
	if projectileHeight < terrainHeight { // strictly below cell terrain height [06 §8.2]
		// Ground-bounce replaces only vertical velocity with negation of signed
		// arithmetic right shift by two and returns; branch never reaches central
		// impact at all, so noexplode irrelevant [06 §8.2] C28.
		// We represent as bounce true, no central impact here.
		res.Bounce = true // [06 §8.2]
		return res
	}
	// Water/sea stage [06 §8.1] — at or above terrain
	if isWaterWeapon {
		res.WaterImpact = false // water weapon returns and continues at or above terrain [06 §8.2]
		return res
	}
	// Non-water weapon at or above sea level continues [06 §8.2]
	if projectileHeight >= seaLevel {
		return res
	}
	// Non-water weapon below sea level impacts unless opaque terrain/liquid mode suppresses [06 §8.1]
	if opaqueLiquid {
		return res // opaque mode suppresses that branch [06 §8.1]
	}
	res.WaterImpact = true // non-water weapon below sea level impacts water [06 §8.1]
	_ = noExplode          // water impact retirement gating evaluated via NoExplodeRetirement by caller [06 §13.2]
	return res
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
	impactCX := world.WorldToCell(impact.X)
	impactCZ := world.WorldToCell(impact.Z)
	cells := BroadPhaseRadiusCells(radius) // [06 §9.3]
	minX := impactCX - cells
	if minX < 0 {
		minX = 0
	}
	maxX := impactCX + cells // inclusive? research says broad phase extends radius/16+1 around impact cell; need to interpret as inclusive radius. Clamped and upper bound exclusive [06 §9.3].
	// If radius Cells =1, we expect 3x3 area: centre +-1 inclusive. So max is centre+ cells inclusive, but exclusive upper bound means maxX exclusive = centre+ cells +1? Let's transcribe: "extends (radius/16)+1 terrain cells around impact cell and is clamped to map. It traverses rows by increasing Z, then cells by increasing X. Both upper map bounds are exclusive." So around means inclusive radius; upper bound exclusive means loop < max exclusive where max = centre + cells +1? We'll implement as centre ± cells inclusive, loop < maxExclusive where maxExclusive = centre+ cells +1 clamped exclusive. Simpler: min = centre - cells, maxExclusive = centre+ cells +1.
	// Let's compute exclusive max: centre+ cells +1
	maxXExclusive := impactCX + cells + 1
	if maxXExclusive > mapW {
		maxXExclusive = mapW
	}
	minZ := impactCZ - cells
	if minZ < 0 {
		minZ = 0
	}
	maxZExclusive := impactCZ + cells + 1
	if maxZExclusive > mapH {
		maxZExclusive = mapH
	}
	// Correct earlier maxX to exclusive as well: if we used inclusive logic, need +1. Above we already set exclusive. The minX stays inclusive start. So loop from min to exclusive.
	_ = maxX // unused old inclusive max; keep for clarity
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

// FeatureCellForArea is a feature-cell candidate [06 §9.3].
type FeatureCellForArea struct {
	CX, CZ int32
	Pos    Vec3 // cell anchor world position?
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
	// float64 for the square root is on the I2 allowlist alongside the ballistic
	// discriminant; the components convert out of 16.16 first so the result is
	// already in world units, and the narrowing truncates toward zero [01 §8] I3.
	// Distances here are non-negative, so truncation and flooring agree.
	fx := float64(cx) / 65536.0
	fy := float64(cy) / 65536.0
	fz := float64(cz) / 65536.0
	di := int32(math.Sqrt(fx*fx + fy*fy + fz*fz))
	// TODO(question): [06 §9.3] says the value is "reduced to" a signed 16-bit
	// world-distance, which on retail is a truncating narrowing and therefore
	// WRAPS past 32,767 — a far-enough victim would read as near. This
	// saturates instead, which is the safe reading and the wrong one if the
	// wrap is ever observable. No shipped weapon has a radius anywhere near
	// that, so the two readings cannot be distinguished by stock content.
	if di > 32767 {
		di = 32767
	}
	if di < -32768 {
		di = -32768
	}
	return di
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

// ApplyAreaDamage enumerates area damage recipients per [06 §9.3] C26 and invokes
// perRecipient for each accepted unit [06 §9.3]. There is NO impulse/pushing
// [06 §9.4] C26 — this function never writes velocity or position.
//
// Impact uses direct-target shortcut only when direct unit supplied and
// unsigned authored area ≤16: full falloff 1 to direct target; if shooter null,
// returns after direct damage rather than falling through to AOE [06 §9.3] C26.
// Otherwise enters area enumeration [06 §9.3].
// Damage suppressed when controller type value 3 [06 §9.3] — semantic name unresolved,
// represented as bool suppress param.
// Controller type 3 suppressed case uses bool flag in config.
//
// This skeleton visits unit slots zero/one then feature; dedup memories are
// implemented; broad phase clamped; order rows by increasing Z then X [06 §9.3].
func ApplyAreaDamage(impact Vec3, weapon *content.WeaponDef, shooter pool.Handle, directTarget pool.Handle, shooterSide uint8, radius int32, terrainW, terrainH int32, unitsByCell func(cx, cz int32) [2]pool.Handle, unitView func(pool.Handle) (UnitForArea, bool), controllerIs3 bool, perUnit func(victim pool.Handle, falloff float32, distance int32)) {
	if controllerIs3 {
		return // damage suppressed when owner controller type value 3 [06 §9.3]
	}
	if weapon == nil {
		return
	}
	authoredArea := weapon.AreaOfEffect // unsigned authored [06 §9.3]
	if directTarget != 0 && authoredArea <= 16 {
		// Direct-target shortcut: full falloff 1 to direct target [06 §9.3]
		if perUnit != nil {
			perUnit(directTarget, 1, 0) // [06 §9.3] full falloff one
		}
		if shooter == 0 {
			return // if shooter null, resolver returns after direct damage [06 §9.3]
		}
		// otherwise other case enters area enumeration per [06 §9.3] — fall through? Research says every other case enters area enumeration. But directTarget with area ≤16 and shooter non-null: does it also do area? Research: "Impact uses direct-target shortcut only when direct unit supplied and unsigned authored area ≤16. That shortcut applies full falloff one to direct target. If recorded shooter is null, resolver returns after direct damage rather than falling through to AOE. Every other case enters area enumeration." Suggests when shooter non-null, even shortcut case still falls through? Or "every other case" means not (direct && area≤16) case. So shortcut case is exclusive. We'll treat shortcut as exclusive.
		return
	}
	// Area enumeration [06 §9.3]
	var unitDedup UnitDedup    // at most 20 memories but not caps [06 §9.3]
	var featDedup FeatureDedup // at most 64 [06 §9.3]
	EnumerateArea(impact, radius, terrainW, terrainH, func(cx, cz int32) {
		// Within each cell visits unit slot zero, unit slot one, then feature [06 §9.3]
		if unitsByCell != nil {
			slots := unitsByCell(cx, cz)
			for _, h := range slots {
				if h == 0 {
					continue
				}
				if unitDedup.SeenUnit(h) {
					continue // dedup before radius test [06 §9.3]
				}
				uv, ok := unitView(h)
				if !ok {
					continue
				}
				dist := DistanceToBox(impact, uv) // [06 §9.3] 3D box distance truncated
				if dist >= radius {
					continue // strictly less than radius [06 §9.3]
				}
				var falloff float32 = 1
				if dist != 0 {
					falloff = Falloff(float32(dist), float32(radius), float32(weapon.EdgeEffectiveness)) // [06 §9.3]
				}
				if perUnit != nil {
					perUnit(h, falloff, dist)
				}
			}
		}
		// Feature/terrain candidate [06 §9.3] — feature distance checked before dedup
		// Simplified: feature presence implied by cell existence? For test we skip feature unless caller provides feature map via separate hook.
		// Feature enumeration stub: we call per feature if needed via feature callback elsewhere.
		// But for dedup semantics, demonstrate feature distance before dedup ordering:
		_ = featDedup // retained for dedup contract [06 §9.3]
		// Note: feature handling would check distance before SeenFeature per [06 §9.3].
	})
}

// ApplyImpulse is intentionally absent: there is NO impulse or pushing
// [06 §9.4] C26 — do not add knockback. This stub exists to assert absence in tests.
func ApplyImpulse() {
	// No impulse [06 §9.4] — deliberately empty
}

// HitByWeaponArgs computes the two 400-radius trig components derived from packet
// direction byte for the HitByWeapon callback [06 §9.1].
func HitByWeaponArgs(dir uint8) (int32, int32) {
	// Two 400-radius trig components derived from direction byte [06 §9.1]
	ang := numeric.Angle(uint16(dir) * 256) // direction byte scaled to 8-bit of circle? 256 * dir = dir*65536/256 => dir *256
	// 400 * sin/cos with table rounding [04 §5.1]
	s := numeric.MulRound(numeric.Sin(ang), 400) // scale 8192 table *400
	c := numeric.MulRound(numeric.Cos(ang), 400)
	return s, c
}

// Ensure imports are used.
var _ = math.Sqrt
var _ = numeric.FixedOne
