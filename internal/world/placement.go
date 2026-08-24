// Package world provides placement validation and yard-map handling.
//
// Yard-map control bytes and geothermal/metal contracts are per
// [04 §6.2], [05 "Terrain metal extraction"], [05 "Geothermal requirement"] and [GAP T15].
package world

import (
	"fmt"
	"strings"
)

// YardCell is a yard-map control byte per [04 §6.2] C10 [GAP T15].
//
// Ten control bytes are exactly:
//
//	'.' 0x00  'C' 0x35  'G' 0x8f  'O' 0x2b  'Y' 0x31
//	'c' 0x2d  'f' 0x6f  'o' 0x2f  'w' 0x37  'y' 0x29
//
// Per-cell bits drive visibility (bit0), occupancy (bits1-2), slope (bit3),
// height (bit4), feature-free (bit5), blocked-class (bit6), and geothermal
// requirement (bit7) [04 §6.2][05 "Geothermal requirement"].
type YardCell uint8

// ParseYardMap parses a yard-map string into row-major control bytes sized by
// the packed footprint extents [04 §6.2] C10.
//
//   - Whitespace (space, tab, \r, \n) is stripped; the remaining characters must
//     be exactly footX*footZ drawn from the ten control characters above.
//   - A single-character input is repeated to fill the footprint, matching the
//     retail convenience for 1-cell shorthand (e.g. "o" with 2x2 → "oooo").
//   - Unknown characters are rejected with a diagnostic.
//
// Non-building classes do not allocate a yard-map buffer; callers should not
// invoke this parser when the definition has no yard map [05 "Geothermal requirement"].
func ParseYardMap(s string, footX, footZ int) ([]YardCell, error) {
	if footX <= 0 || footZ <= 0 {
		return nil, fmt.Errorf("world: invalid footprint %dx%d [04 §6.2]", footX, footZ)
	}
	// Strip whitespace [fmt tdf] yard maps are authored with spaces for readability.
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	if compact == "" {
		return nil, fmt.Errorf("world: empty yardmap for %dx%d [04 §6.2]", footX, footZ)
	}
	if len(compact) == 1 {
		compact = strings.Repeat(compact, footX*footZ)
	}
	expected := footX * footZ
	if len(compact) != expected {
		return nil, fmt.Errorf("world: yardmap length %d != footprint %dx%d=%d [04 §6.2]", len(compact), footX, footZ, expected)
	}
	out := make([]YardCell, expected)
	for i, ch := range compact {
		var b YardCell
		switch ch {
		case '.':
			b = 0x00 // [04 §6.2] C10
		case 'C':
			b = 0x35 // [04 §6.2] C10
		case 'G':
			b = 0x8f // [04 §6.2] C10 bit7 geothermal [05 "Geothermal requirement"]
		case 'O':
			b = 0x2b // [04 §6.2] C10
		case 'Y':
			b = 0x31 // [04 §6.2] C10
		case 'c':
			b = 0x2d // [04 §6.2] C10
		case 'f':
			b = 0x6f // [04 §6.2] C10
		case 'o':
			b = 0x2f // [04 §6.2] C10
		case 'w':
			b = 0x37 // [04 §6.2] C10
		case 'y':
			b = 0x29 // [04 §6.2] C10
		default:
			return nil, fmt.Errorf("world: unknown yardmap character %q at %d [04 §6.2]", ch, i)
		}
		out[i] = b
	}
	return out, nil
}

// hasBlockingFeatureAt reports whether the plot cell at (cx,cz) is considered
// blocking for yard bit5 (feature-free) [04 §6.2][05 "Geothermal requirement"].
// It resolves the fringe sentinel (0xFFFE) via the successor hop [GAP T14] and
// treats the three reserved sentinels 0xFFFB/0xFFFC/0xFFFD as blocking.
// Empty (0xFFFF) is not blocking.
func (t *Terrain) hasBlockingFeatureAt(cx, cz int32) bool {
	if t == nil || t.Plot == nil {
		return false
	}
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return false
	}
	idx := int(cz*t.CellW + cx)
	if idx < 0 || idx >= len(t.Plot) {
		return false
	}
	f := t.Plot[idx].Feature()
	if f < plotFeatureRealLimit {
		// Real feature index <0xFFFB — bounds-checked against catalog; out-of-range
		// behaves as blocking for bit5 [05 "Geothermal requirement"].
		return true
	}
	if f == PlotFeatureFringe {
		dx := int(t.Plot[idx].AnchorDXSigned())
		dz := int(t.Plot[idx].AnchorDZSigned())
		ax := cx + int32(dx)
		az := cz + int32(dz)
		if ax < 0 || az < 0 || ax >= t.CellW || az >= t.CellH {
			return false
		}
		aIdx := int(az*t.CellW + ax)
		if aIdx < 0 || aIdx >= len(t.Plot) {
			return false
		}
		af := t.Plot[aIdx].Feature()
		if af < plotFeatureRealLimit {
			return true
		}
		// Fringe whose anchor is not a real feature is not blocking.
		return false
	}
	if f == PlotFeatureNone {
		return false
	}
	// 0xFFFB, 0xFFFC, 0xFFFD — reserved/void sentinels behave as occupied [05].
	if f == PlotFeatureVoid || f == 0xFFFC || f == 0xFFFB {
		return true
	}
	return false
}

// hasGeothermalFeatureAt reports whether the plot cell at (cx,cz) resolves to
// a feature that could satisfy the geothermal requirement [05 "Geothermal requirement"].
// It follows the fringe successor hop (0xFFFE) and returns true only for a real
// feature index <0xFFFB. Void/empty sentinels do not satisfy. The final
// geothermal-flag check requires the catalog entry's geothermal bit [02 "Feature record"];
// without a catalog this counts any real feature as candidate — callers with a
// catalog should additionally verify FeatureDef.Geothermal.
func (t *Terrain) hasGeothermalFeatureAt(cx, cz int32) bool {
	if t == nil || t.Plot == nil {
		return false
	}
	if cx < 0 || cz < 0 || cx >= t.CellW || cz >= t.CellH {
		return false
	}
	idx := int(cz*t.CellW + cx)
	if idx < 0 || idx >= len(t.Plot) {
		return false
	}
	f := t.Plot[idx].Feature()
	if f < plotFeatureRealLimit {
		return true
	}
	if f == PlotFeatureFringe {
		dx := int(t.Plot[idx].AnchorDXSigned())
		dz := int(t.Plot[idx].AnchorDZSigned())
		ax := cx + int32(dx)
		az := cz + int32(dz)
		if ax < 0 || az < 0 || ax >= t.CellW || az >= t.CellH {
			return false
		}
		aIdx := int(az*t.CellW + ax)
		if aIdx < 0 || aIdx >= len(t.Plot) {
			return false
		}
		af := t.Plot[aIdx].Feature()
		if af < plotFeatureRealLimit {
			return true
		}
	}
	return false
}

// ValidatePlacement checks a placement at cell (cx,cz) with the given yard
// footprint against the terrain plot cells [04 §6.2] C10–C11 [GAP T15].
//
//   - Bounds-checks the rectangle against the map.
//   - Validates yard length matches footX*footZ.
//   - Per covered cell, applies yard control-byte bits: occupancy (bits1-2),
//     feature-free (bit5), blocked-class (bit6, catalog-gated), and the
//     geothermal requirement (bit7) resolved through the covered cell's feature
//     and successor hop with no registry [05 "Geothermal requirement"].
//   - Slope (bit3) and height (bit4) sampling and visibility (bit0) are
//     TODO(question) — stored but not gating placement in this phase [PLAN_04].
func (t *Terrain) ValidatePlacement(cx, cz int32, yard []YardCell, footX, footZ int) error {
	if t == nil {
		return fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 || footZ <= 0 {
		return fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if len(yard) != footX*footZ {
		return fmt.Errorf("world: yard length %d != footprint %dx%d=%d", len(yard), footX, footZ, footX*footZ)
	}
	if cx < 0 || cz < 0 || cx+int32(footX) > t.CellW || cz+int32(footZ) > t.CellH {
		return fmt.Errorf("world: placement %d,%d %dx%d out of bounds %dx%d", cx, cz, footX, footZ, t.CellW, t.CellH)
	}
	if t.Plot == nil || len(t.Plot) < int(t.CellW*t.CellH) {
		return fmt.Errorf("world: terrain plot not initialized")
	}
	// Determine whether any yard cell carries the geothermal requirement (bit7) [05].
	geothermalNeeded := false
	for _, y := range yard {
		if y&0x80 != 0 { // bit7 [04 §6.2] G 0x8f
			geothermalNeeded = true
			break
		}
	}
	// Scan footprint for per-cell occupancy / feature gates and for geothermal satisfaction.
	hasGeothermal := false
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			idx := dz*footX + dx
			y := yard[idx]
			px := cx + int32(dx)
			pz := cz + int32(dz)
			pIdx := int(pz*t.CellW + px)
			if pIdx < 0 || pIdx >= len(t.Plot) {
				return fmt.Errorf("world: plot index %d out of range", pIdx)
			}
			cell := t.Plot[pIdx]

			// Bits1-2 reject any nonzero occupant other than self [05][04 §6.2].
			// For construction self is nil, so any Occupied cell rejects.
			if y&0x06 != 0 { // bits1-2
				if cell.Occupied() {
					return fmt.Errorf("world: cell %d,%d occupied [04 §6.2]", px, pz)
				}
			}
			// Bit0 enemy-visibility occupancy test [04 §6.2] — TODO(question): requires player visibility state, not checked here.

			// Bit5 requires cell to be free of blocking features [05].
			if y&0x20 != 0 { // bit5
				if t.hasBlockingFeatureAt(px, pz) {
					return fmt.Errorf("world: cell %d,%d blocked by feature [04 §6.2]", px, pz)
				}
			}
			// Bit6 fails when resolved feature's catalog entry carries a specific non-reclaimable flag [05].
			// TODO(T25): requires FeatureDef catalog lookup for the non-reclaimable flag. Without it we cannot
			// enforce bit6 precisely; placeholder accepts the placement (do not falsely reject).
			// If a future catalog is threaded through, enforce here.

			// Bit3 slope and bit4 height sampling — TODO(question): slope/height sampling over footprint not yet gated [04 §6.2].
			// Bit4 height tracking and bit3 slope enable are stored in the yard byte but have no terrain-height gate in this phase.

			// Track geothermal satisfaction: at least one covered cell holds a feature whose catalog entry
			// carries geothermal flag [05 "Geothermal requirement"]. The resolver follows the fringe hop.
			// Without a catalog we treat any real feature as geothermal candidate; a catalog-aware caller
			// can extend this with FeatureDef.Geothermal.
			if t.hasGeothermalFeatureAt(px, pz) {
				hasGeothermal = true
			}
		}
	}
	if geothermalNeeded && !hasGeothermal {
		return fmt.Errorf("world: geothermal requirement not satisfied [05 \"Geothermal requirement\"]")
	}
	return nil
}

// SampleMetal computes the metal content sampled at placement [05 "Terrain metal extraction"].
//
// For each cell in the footprint at (cx,cz) sized footX×footZ, it sums the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// extractsMetal multiplier:
//
//	sampled = extractsMetal × Σ(cellMetal + 1)
//
// The intermediate sum uses integer arithmetic then converts to single-precision
// (float32) [05]. The result is stored on the unit instance at placement and not
// recomputed each economy pass [05]. Later terrain or feature changes do not
// automatically change an already stored amount.
func (t *Terrain) SampleMetal(cx, cz int32, footX, footZ int, extractsMetal float64) (float32, error) {
	if t == nil {
		return 0, fmt.Errorf("world: nil terrain")
	}
	if footX <= 0 || footZ <= 0 {
		return 0, fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if cx < 0 || cz < 0 || cx+int32(footX) > t.CellW || cz+int32(footZ) > t.CellH {
		return 0, fmt.Errorf("world: sample %d,%d %dx%d out of bounds %dx%d", cx, cz, footX, footZ, t.CellW, t.CellH)
	}
	if t.Plot == nil || len(t.Plot) < int(t.CellW*t.CellH) {
		return 0, fmt.Errorf("world: terrain plot not initialized")
	}
	sum := 0
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			px := cx + int32(dx)
			pz := cz + int32(dz)
			pIdx := int(pz*t.CellW + px)
			if pIdx < 0 || pIdx >= len(t.Plot) {
				return 0, fmt.Errorf("world: plot index %d out of range", pIdx)
			}
			metal := t.Plot[pIdx].Metal()
			sum += int(metal) + 1 // [05 "Terrain metal extraction"] cellMetal+1
		}
	}
	// Retail converts the integer sum to single-precision after multiplication [05].
	result := float32(float64(sum) * extractsMetal)
	return result, nil
}

// ExtractedMetal is an alias for SampleMetal for callers that prefer the
// placement-verb naming. It satisfies C12.
func (t *Terrain) ExtractedMetal(cx, cz int32, footX, footZ int, extractsMetal float64) (float32, error) {
	return t.SampleMetal(cx, cz, footX, footZ, extractsMetal)
}

// ValidatePlacementWithFeatures is a catalog-aware geothermal validator that
// checks the geothermal flag on the resolved feature definition [05 "Geothermal requirement"].
// It otherwise delegates to ValidatePlacement for bounds/occupancy/feature-free gates.
// Pass nil features to fall back to the plot-only heuristic (any real feature satisfies).
func (t *Terrain) ValidatePlacementWithFeatures(cx, cz int32, yard []YardCell, footX, footZ int, geothermalFeatures map[uint16]bool) error {
	// First run the base validator without the geothermal gate, then re-check geothermal with catalog.
	// To avoid double geothermal error, run base with a copy that has bit7 cleared, then handle geothermal ourselves.
	if len(yard) != footX*footZ {
		return fmt.Errorf("world: yard length %d != footprint %dx%d", len(yard), footX, footZ)
	}
	hasBit7 := false
	for _, y := range yard {
		if y&0x80 != 0 {
			hasBit7 = true
			break
		}
	}
	if !hasBit7 || geothermalFeatures == nil {
		return t.ValidatePlacement(cx, cz, yard, footX, footZ)
	}
	// Validate non-geothermal gates via base validator with bit7 temporarily cleared.
	cleared := make([]YardCell, len(yard))
	for i, y := range yard {
		cleared[i] = y &^ 0x80
	}
	if err := t.ValidatePlacement(cx, cz, cleared, footX, footZ); err != nil {
		return err
	}
	// Now check geothermal flag via supplied map.
	hasGeothermal := false
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			px := cx + int32(dx)
			pz := cz + int32(dz)
			// Resolve through fringe hop.
			if t == nil || t.Plot == nil {
				continue
			}
			if px < 0 || pz < 0 || px >= t.CellW || pz >= t.CellH {
				continue
			}
			pIdx := int(pz*t.CellW + px)
			if pIdx < 0 || pIdx >= len(t.Plot) {
				continue
			}
			f := t.Plot[pIdx].Feature()
			var idx uint16
			found := false
			if f < plotFeatureRealLimit {
				idx = f
				found = true
			} else if f == PlotFeatureFringe {
				adx := int(t.Plot[pIdx].AnchorDXSigned())
				adz := int(t.Plot[pIdx].AnchorDZSigned())
				ax := px + int32(adx)
				az := pz + int32(adz)
				if ax >= 0 && az >= 0 && ax < t.CellW && az < t.CellH {
					aIdx := int(az*t.CellW + ax)
					if aIdx >= 0 && aIdx < len(t.Plot) {
						af := t.Plot[aIdx].Feature()
						if af < plotFeatureRealLimit {
							idx = af
							found = true
						}
					}
				}
			}
			if found {
				if geothermalFeatures[idx] {
					hasGeothermal = true
					break
				}
			}
		}
		if hasGeothermal {
			break
		}
	}
	if !hasGeothermal {
		return fmt.Errorf("world: geothermal requirement not satisfied [05 \"Geothermal requirement\"]")
	}
	return nil
}

// ComputeMetal is a package-level helper for C12 that does not require a Terrain method receiver.
// It is useful for tests that synthesize a plot slice directly.
func ComputeMetal(plot []PlotCell, cellW int32, cx, cz int32, footX, footZ int, extractsMetal float64) (float32, error) {
	if plot == nil {
		return 0, fmt.Errorf("world: nil plot")
	}
	if footX <= 0 || footZ <= 0 {
		return 0, fmt.Errorf("world: invalid footprint %dx%d", footX, footZ)
	}
	if cellW <= 0 {
		return 0, fmt.Errorf("world: invalid cellW %d", cellW)
	}
	cellH := int32(len(plot)) / cellW
	if cx < 0 || cz < 0 || cx+int32(footX) > cellW || cz+int32(footZ) > cellH {
		return 0, fmt.Errorf("world: sample %d,%d %dx%d out of bounds %dx%d", cx, cz, footX, footZ, cellW, cellH)
	}
	sum := 0
	for dz := 0; dz < footZ; dz++ {
		for dx := 0; dx < footX; dx++ {
			px := cx + int32(dx)
			pz := cz + int32(dz)
			pIdx := int(pz*cellW + px)
			if pIdx < 0 || pIdx >= len(plot) {
				return 0, fmt.Errorf("world: plot index %d out of range", pIdx)
			}
			sum += int(plot[pIdx].Metal()) + 1 // [05 "Terrain metal extraction"]
		}
	}
	return float32(float64(sum) * extractsMetal), nil
}
