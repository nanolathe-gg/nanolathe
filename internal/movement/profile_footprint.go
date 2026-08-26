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
// footprint. Feature checks remain row-major/immediate; aggregate gates run
// after the scan [04 §8.2] C25, [R-P0-08].
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
	// Inclusive water-depth boundaries: only strict < and > reject
	// [R-P0-08 mobile terrain validator].
	if p.MaxWaterDepth > 0 && minLow < sea-p.MaxWaterDepth {
		return ClassBlocked
	}
	if p.MinWaterDepth > 0 && maxHigh > sea-p.MinWaterDepth {
		return ClassBlocked
	}
	slope := maxHigh - minLow
	if slope < 0 {
		slope = 0
	}
	var maxSlope, badSlope uint8
	if sea <= minLow {
		maxSlope, badSlope = p.MaxSlope, p.BadSlope
	} else {
		maxSlope, badSlope = p.MaxWaterSlope, p.BadWaterSlope
		if maxSlope == 0 && badSlope == 0 {
			// Omitted maxwaterslope template initialization remains unresolved;
			// retain the install-compatible fallback [docs/SPEC_CONFLICTS SC5].
			maxSlope, badSlope = p.MaxSlope, p.BadSlope
		}
	}
	if maxSlope != 0 && slope > int32(maxSlope) {
		return ClassBlocked
	}
	if badSlope != 0 && slope > int32(badSlope) {
		// Exact HOT cost and forward-speed factor remain unknown [R-P1-11].
		// Preserve the soft terrain state without inventing a multiplier.
		return ClassSteep
	}
	return ClassClear
}

// IsPassableFootprint is the path/commit predicate for a footprint anchor.
func (p Profile) IsPassableFootprint(t *world.Terrain, ax, az int32) bool {
	return p.ClassifyFootprint(t, ax, az) != ClassBlocked
}
