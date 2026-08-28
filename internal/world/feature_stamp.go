package world

import "fmt"

// StampFeatureRect writes one feature footprint through the same low-level
// rectangle writer used by map bootstrap. The caller owns instance flags,
// collision policy, static-revision policy, and any anchor payload; this
// method only writes feature sentinels and fringe-to-anchor deltas
// [03 §2.2][03 §5.1.2]. Runtime feature services bump the static revision at
// their semantic lifecycle boundary, keeping bootstrap and replacement writes
// from adding low-level duplicate bumps.
//
// The rectangle is half-open, with the anchor at (anchorX, anchorZ). A
// footprint is rejected when it is not wholly inside the plot or when one of
// its deltas cannot be represented by the signed byte fields. The latter
// leaves the cell untouched, preserving the unresolved field-limit behavior.
func (t *Terrain) StampFeatureRect(anchorX, anchorZ int32, feature uint16, footX, footZ int32) error {
	if t == nil {
		return fmt.Errorf("world: nil terrain")
	}
	if feature >= plotFeatureRealLimit {
		return fmt.Errorf("world: feature %#x is not a live feature index", feature)
	}
	if footX <= 0 || footZ <= 0 {
		return fmt.Errorf("world: feature footprint must be positive")
	}
	if !validFootprint(t.CellW, t.CellH, anchorX, anchorZ, footX, footZ) {
		return fmt.Errorf("world: feature footprint out of bounds at (%d,%d), size %dx%d", anchorX, anchorZ, footX, footZ)
	}
	t.writeFeatureRect(anchorX, anchorZ, feature, footX, footZ, nil, nil)
	return nil
}

func validFootprint(cellW, cellH, anchorX, anchorZ, footX, footZ int32) bool {
	if cellW <= 0 || cellH <= 0 || footX <= 0 || footZ <= 0 || anchorX < 0 || anchorZ < 0 {
		return false
	}
	return int64(anchorX)+int64(footX) <= int64(cellW) && int64(anchorZ)+int64(footZ) <= int64(cellH)
}

// writeFeatureRect is the shared byte writer for map and runtime placement.
// authoredFringe/owned are non-nil only during map bootstrap: they prevent an
// authored empty cell from becoming a synthetic fringe and let bootstrap turn
// uncovered raw fringe into empty after all source-order stamps.
func (t *Terrain) writeFeatureRect(anchorX, anchorZ int32, feature uint16, footX, footZ int32, authoredFringe, owned []bool) {
	if t == nil || !validFootprint(t.CellW, t.CellH, anchorX, anchorZ, footX, footZ) {
		return
	}
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			cx, cz := anchorX+dx, anchorZ+dz
			idx := int(cz*t.CellW + cx)
			if idx < 0 || idx >= len(t.Plot) {
				continue
			}
			if dx != 0 || dz != 0 {
				if authoredFringe != nil && (idx >= len(authoredFringe) || !authoredFringe[idx]) {
					continue
				}
			}
			if dx == 0 && dz == 0 {
				if existing := t.Plot[idx].Feature(); existing < plotFeatureRealLimit && existing != feature {
					if !allowLiveAnchorOverlap(existing, feature) {
						continue
					}
				}
				t.Plot[idx].SetFeature(uint16(feature))
				continue
			}

			// A later footprint owns a fringe seam, but it must not silently
			// replace an unrelated live anchor. The exact dense-pack policy is
			// unresolved; keep this decision in this one guarded helper.
			if existing := t.Plot[idx].Feature(); existing < plotFeatureRealLimit {
				if !allowLiveAnchorOverlap(existing, feature) {
					continue
				}
			}
			dxToAnchor, dzToAnchor := -dx, -dz
			if dxToAnchor < -128 || dxToAnchor > 127 || dzToAnchor < -128 || dzToAnchor > 127 {
				continue
			}
			t.Plot[idx].SetFeature(PlotFeatureFringe)
			t.Plot[idx].SetAnchorSigned(int8(dxToAnchor), int8(dzToAnchor))
			if owned != nil && idx < len(owned) {
				owned[idx] = true
			}
		}
	}
}

func allowLiveAnchorOverlap(existing, _ uint16) bool {
	if existing >= plotFeatureRealLimit {
		return true
	}
	// TODO(question): retail's dense-pack rule for a footprint covering a
	// different live anchor is unresolved; do not invent overwrite behavior.
	return false
}

// stampFeatureAnchors derives fringe ownership in authored map order. The
// raw feature words are retained as a mask: only authored fringe cells can be
// stamped, and every uncovered authored fringe becomes empty after the pass.
func (t *Terrain) stampFeatureAnchors() {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 || len(t.Plot) < int(t.CellW*t.CellH) {
		return
	}
	authoredFringe := make([]bool, len(t.Plot))
	for i := range t.Plot {
		authoredFringe[i] = t.Plot[i].Feature() == PlotFeatureFringe
	}
	owned := make([]bool, len(t.Plot))
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			idx := int(cz*t.CellW + cx)
			feature := t.Plot[idx].Feature()
			if feature >= plotFeatureRealLimit {
				continue
			}
			def, ok := t.FeatureDefAt(feature)
			if !ok || def == nil || def.FootprintX <= 0 || def.FootprintZ <= 0 {
				continue
			}
			if !validFootprint(t.CellW, t.CellH, cx, cz, def.FootprintX, def.FootprintZ) {
				continue
			}
			t.writeFeatureRect(cx, cz, feature, def.FootprintX, def.FootprintZ, authoredFringe, owned)
		}
	}
	for i := range t.Plot {
		if authoredFringe[i] && !owned[i] {
			t.Plot[i].SetFeature(PlotFeatureNone)
			t.Plot[i].SetAnchor(0, 0)
		}
	}
}
