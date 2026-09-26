package movement

import "github.com/nanolathe-gg/nanolathe/internal/world"

func commitRectInBounds(t *world.Terrain, anchor Cell, fx, fz int16) bool {
	if t == nil {
		return true
	}
	if fx < 0 {
		fx = 1
	}
	if fz < 0 {
		fz = 1
	}
	return anchor.X >= 0 && anchor.Z >= 0 &&
		anchor.X+int32(fx) < t.CellW && anchor.Z+int32(fz) < t.CellH
}

// footprintSize is the single derived footprint source used by path,
// movement legality, and occupancy admission. Zero authored dimensions use
// the runtime one-cell fallback.
func (p Profile) footprintSize() (int32, int32) {
	fx, fz := int32(p.FootPrintX), int32(p.FootPrintZ)
	if fx <= 0 {
		fx = 1
	}
	if fz <= 0 {
		fz = 1
	}
	return fx, fz
}

// classifyCell is the per-cell classifier chain, the single source of the
// terrain tier for one attribute cell [04 §6.1 R-DOC04-B][04 R-SLOPE-01 §2].
//
// Every gate reads THIS cell's own derived pair — the plot's derived maximum
// (byte 0x05) and minimum (byte 0x06) over its 2×2 height neighbourhood. There
// is no height aggregate across a footprint here or in any other movement
// classifier: the `min of mins` / `max of maxes` form belongs to the structure
// placement validator's yard-map walk and the spawner height probe alone
// [04 R-SLOPE-01 §3 "Bounded census"].
//
// Order is the documented chain: feature gate, deep gate, shallow gate, medium
// split, slope tier. The occupant-age gate (step 2) is not here — it needs the
// class layer's grid and revision watermark, so ClassLayer interposes it.
func (p Profile) classifyCell(t *world.Terrain, cx, cz int32) CellClass {
	if t == nil || cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return ClassBlocked
	}
	// Step 1, feature gate: a resolved blocking feature, a stale feature
	// identity and a void cell all block [04 §6.1 R-DOC04-B step 1].
	if isFeatureBlocked(t, cx, cz) {
		return ClassBlocked
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return ClassBlocked
	}
	low, high := int32(cell.MinHeight()), int32(cell.MaxHeight())
	sea := int32(t.SeaLevel)
	// Steps 3-4, depth gates, signed 32-bit on the record's depth fields:
	// blocked iff hmin < SeaLevel − MaxWaterDepth or hmax > SeaLevel −
	// MinWaterDepth; a depth exactly at the limit passes. The depths are
	// record values, not presence flags: the startup template supplies ±10000
	// wherever a class omits the key, so an unlimited direction never fires
	// [04 §6.1 R-DOC04-A].
	if low < sea-p.MaxWaterDepth {
		return ClassBlocked
	}
	if high > sea-p.MinWaterDepth {
		return ClassBlocked
	}
	// Step 5, medium split: land iff hmin >= SeaLevel, which selects the land
	// or water slope pair for THIS cell [04 R-SLOPE-01 §2].
	maxSlope, badSlope := p.MaxWaterSlope, p.BadWaterSlope
	if low >= sea {
		maxSlope, badSlope = p.MaxSlope, p.BadSlope
	}
	// Step 6, slope tier, unsigned byte subtraction of this cell's own pair:
	// slope <= Bad is clear, slope > Max is blocked, anything between is
	// steep. Equality with the bad threshold is clear; equality with the max
	// threshold is steep, not blocked [04 R-SLOPE-01 §2].
	slope := high - low
	if slope <= int32(badSlope) {
		return ClassClear
	}
	if slope > int32(maxSlope) {
		return ClassBlocked
	}
	// The steep tier is not a movement multiplier here: its whole cost lives
	// in the path search, where a step onto a steep-tier cell costs 30 more
	// than a step onto a clear or unexplored one [04 R-PATH-01 §3]. This
	// classifier only reports the tier.
	return ClassSteep
}

// classifyRectMin runs classifyCell over an inclusive rectangle and returns the
// MINIMUM tier over its cells [04 R-SLOPE-01 §3 item 2]. A blocked cell returns
// immediately; a steep cell lowers a running clear to steep. Cells outside the
// map are tier 0, so any rectangle leaving the map is blocked.
func (p Profile) classifyRectMin(t *world.Terrain, x1, z1, x2, z2 int32) CellClass {
	if x2 < x1 || z2 < z1 {
		return ClassBlocked
	}
	result := ClassClear
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			switch p.classifyCell(t, x, z) {
			case ClassBlocked:
				return ClassBlocked
			case ClassSteep:
				result = ClassSteep
			}
		}
	}
	return result
}

// ClassifyFootprint classifies a footprint anchored at (ax, az) the way every
// movement-side classifier does: each covered cell on its own derived pair, the
// MINIMUM tier over the footprint, and a clear result demoted to steep unless
// every cell of the surrounding one-cell ring is clear too
// [04 R-SLOPE-01 §3][04 §6.1 R-DOC04-B].
//
// Corrected by WU-19-46. This used to aggregate the footprint's heights as
// min-of-mins/max-of-maxes and classify that single span, which judged a 2×2
// class on the height range of a 3×3 corner window. The aggregate range is at
// least every cell's own range, so it is strictly harsher, and the gap grows
// with the footprint: on `ashap plateau` it turned the computer player's start
// plateau into a 2735-cell pocket for TANKSH2 where the per-cell rule reaches
// 53279. ([04 R-SLOPE-01 §4] records 53370 for that flood; both figures are
// this loader's, and the recorded one was taken while the loader still carried
// the pre-correction south strip — see that section's Unknown.)
//
// Bounds are the map-load layer builder's: its sliding window zeroes exactly
// the anchors whose footprint leaves the map [04 R-SLOPE-01 §3 item 1]. The
// rectangle restamp and the mobile commit validator carry a stricter bound —
// a rectangle reaching column W−1 or row H−1 is 0 outright — and it lives
// where they do, in ClassLayer.RestampRect and in commitRectInBounds
// [04 R-SLOPE-01 §3 item 2][04 R-COLL-01 §2 steps 1-4]. The two bounds agree on
// the last COLUMN of a loaded map: the sweep of [03 R-TERR-01 §2] voids both
// right columns outright and a void cell is tier 0. They do not agree on the
// last ROW — that sweep's south walk voids the row ABOVE the one it tests, so
// it never reaches row H−1, and an interior anchor whose extent reaches the
// bottom row is 0 for the restamp while the window need not zero it. Which
// half is which is the correction under [04 R-SLOPE-01 §3] item 2; the sweep
// itself is world.applyVoidFixup, implemented to §2 by WU-19-48.
//
// Only the blocked verdict rejects: steep and clear are both passable.
func (p Profile) ClassifyFootprint(t *world.Terrain, ax, az int32) CellClass {
	fx, fz := p.footprintSize()
	if t == nil || ax < 0 || az < 0 || ax+fx > t.CellW || az+fz > t.CellH {
		return ClassBlocked
	}
	result := p.classifyRectMin(t, ax, az, ax+fx-1, az+fz-1)
	if result != ClassClear {
		return result
	}
	// The four strips cover the complete ring including its corners. A ring
	// cell that is blocked demotes the anchor to steep — it never blocks it
	// [04 R-SLOPE-01 §3 item 1 closed form].
	if p.classifyRectMin(t, ax-1, az-1, ax+fx, az-1) != ClassClear ||
		p.classifyRectMin(t, ax+fx, az-1, ax+fx, az+fz) != ClassClear ||
		p.classifyRectMin(t, ax-1, az+fz, ax+fx, az+fz) != ClassClear ||
		p.classifyRectMin(t, ax-1, az-1, ax-1, az+fz) != ClassClear {
		return ClassSteep
	}
	return ClassClear
}

// IsPassableFootprint is the path/commit predicate for a footprint anchor.
func (p Profile) IsPassableFootprint(t *world.Terrain, ax, az int32) bool {
	return p.ClassifyFootprint(t, ax, az) != ClassBlocked
}

// IsPassableCommitCell applies the final movement validator to one covered
// cell. Unlike path-layer classification, the final commit treats both slope
// tiers as a strict maximum and does not aggregate heights across neighboring
// footprint cells [04 R-COLL-01 §2][04 R-COLL-01 §8].
func (p Profile) IsPassableCommitCell(t *world.Terrain, cx, cz int32) bool {
	if t == nil || isFeatureBlocked(t, cx, cz) {
		return false
	}
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return false
	}
	low, high := int32(cell.MinHeight()), int32(cell.MaxHeight())
	sea := int32(t.SeaLevel)
	if low < sea-p.MaxWaterDepth || high > sea-p.MinWaterDepth {
		return false
	}
	slope := high - low
	if slope > int32(p.MaxSlope) {
		if low >= sea {
			return false
		}
		if slope > int32(p.MaxWaterSlope) {
			return false
		}
	}
	return true
}
