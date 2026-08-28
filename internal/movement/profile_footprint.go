package movement

import "github.com/nanolathe/nanolathe/internal/world"

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

// footprintRange reads the derived low/high terrain pair from every covered
// cell [R-P0-08 mobile terrain validator].
func (p Profile) footprintRange(t *world.Terrain, ax, az int32) (minLow, maxHigh int32, ok bool) {
	if t == nil {
		return 0, 0, false
	}
	fx, fz := p.footprintSize()
	if ax < 0 || az < 0 || ax+fx > t.CellW || az+fz > t.CellH {
		return 0, 0, false
	}
	minLow, maxHigh = 255, 0
	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			cell := t.PlotAt(ax+dx, az+dz)
			if cell == nil {
				return 0, 0, false
			}
			if h := int32(cell.MinHeight()); h < minLow {
				minLow = h
			}
			if h := int32(cell.MaxHeight()); h > maxHigh {
				maxHigh = h
			}
		}
	}
	return minLow, maxHigh, true
}

// ClassifyFootprint applies terrain and feature legality to the complete
// footprint. Feature checks remain row-major/immediate; the depth and slope
// gates then run over the aggregate height span (min of mins, max of maxes).
//
// This is the aggregate footprint VALIDATOR of the commit stage [04 §8.2]:
// path search uses the pre-stamped per-class 2-bit layer (layer.go, per-cell
// classifier chain of [04 §6.1 R-DOC04-B]), while movement commit checks the
// current rectangle here [04 §8.2]. Only the blocked verdict rejects.
func (p Profile) ClassifyFootprint(t *world.Terrain, ax, az int32) CellClass {
	fx, fz := p.footprintSize()
	if t == nil || ax < 0 || az < 0 || ax+fx > t.CellW || az+fz > t.CellH {
		return ClassBlocked
	}
	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			if isFeatureBlocked(t, ax+dx, az+dz) {
				return ClassBlocked
			}
		}
	}
	minLow, maxHigh, ok := p.footprintRange(t, ax, az)
	if !ok {
		return ClassBlocked
	}
	sea := int32(t.SeaLevel)
	// Depth gates, signed 32-bit on the record's depth fields: blocked iff
	// hmin < SeaLevel − MaxWaterDepth or hmax > SeaLevel − MinWaterDepth;
	// a depth exactly at the limit passes [04 §6.1 R-DOC04-B steps 3-4].
	// The depths are record values, not presence flags: the startup template
	// supplies ±10000 wherever a class omits the key, so an unlimited
	// direction never fires [04 §6.1 R-DOC04-A].
	if minLow < sea-p.MaxWaterDepth {
		return ClassBlocked
	}
	if maxHigh > sea-p.MinWaterDepth {
		return ClassBlocked
	}
	// slope = hmax − hmin over the footprint (min of mins, max of maxes).
	slope := maxHigh - minLow
	// Medium split: land iff hmin >= SeaLevel, which selects the land or
	// water slope pair [04 §6.1 R-DOC04-B step 5].
	maxSlope, badSlope := p.MaxWaterSlope, p.BadWaterSlope
	if minLow >= sea {
		maxSlope, badSlope = p.MaxSlope, p.BadSlope
	}
	// Slope tier, unsigned byte comparisons in the documented chain order
	// [04 §6.1 R-DOC04-B step 6]: slope <= Bad is clear, slope > Max is
	// blocked, anything between is steep. Equality with the bad threshold is
	// clear; equality with the max threshold is steep, not blocked. Only the
	// blocked verdict rejects a footprint.
	if slope <= int32(badSlope) {
		return ClassClear
	}
	if slope > int32(maxSlope) {
		return ClassBlocked
	}
	// Exact HOT cost and forward-speed factor remain unknown [R-P1-11].
	// Preserve the soft terrain state without inventing a multiplier.
	return ClassSteep
}

// IsPassableFootprint is the path/commit predicate for a footprint anchor.
func (p Profile) IsPassableFootprint(t *world.Terrain, ax, az int32) bool {
	return p.ClassifyFootprint(t, ax, az) != ClassBlocked
}
