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
//
// The dense-pack rule of [05 R-FEAT-01 §3 step 3] runs first: every covered
// cell that is not empty is torn down, and a teardown that refuses vetoes the
// whole stamp, leaving the cells torn so far torn. That is the error this
// returns.
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
	if !t.writeFeatureRect(anchorX, anchorZ, feature, footX, footZ, nil) {
		return fmt.Errorf("world: feature footprint at (%d,%d) covers an indestructible feature; the stamp is vetoed with the cells torn so far left torn [05 R-FEAT-01 §3-A]", anchorX, anchorZ)
	}
	return nil
}

func validFootprint(cellW, cellH, anchorX, anchorZ, footX, footZ int32) bool {
	if cellW <= 0 || cellH <= 0 || footX <= 0 || footZ <= 0 || anchorX < 0 || anchorZ < 0 {
		return false
	}
	return int64(anchorX)+int64(footX) <= int64(cellW) && int64(anchorZ)+int64(footZ) <= int64(cellH)
}

// writeFeatureRect is the shared byte writer for map and runtime placement. It
// reports whether the stamp completed; false is the dense-pack veto of
// [05 R-FEAT-01 §3 step 3], which leaves the cells torn so far torn.
//
// The fringe write is UNCONDITIONAL [05 R-FEAT-01 §17]: after the dense-pack
// loop and the anchor write, retail walks the footprint and writes fringe into
// every cell except the anchor, reading nothing from the cell first — neither
// its current feature word nor anything the source authored. The per-cell rule
// is exactly "covered and not the anchor → fringe, always". The bootstrap gate
// this writer used to carry, which left an authored-empty covered cell empty,
// encoded a non-retail premise: the plot never holds the authored TNT word in
// retail, so an authored `0xFFFF` under a footprint is indistinguishable from
// an authored `0xFFFE` and both become fringe.
//
// owned is non-nil only during map bootstrap, where it records which cells a
// stamp covered so the pass can turn UNCOVERED authored fringe into empty
// afterwards — the same outcome retail reaches by never writing such a cell at
// all [05 R-FEAT-01 §17 consequence 2].
func (t *Terrain) writeFeatureRect(anchorX, anchorZ int32, feature uint16, footX, footZ int32, owned []bool) bool {
	if t == nil || !validFootprint(t.CellW, t.CellH, anchorX, anchorZ, footX, footZ) {
		return false
	}
	if !t.densePackTeardown(anchorX, anchorZ, footX, footZ) {
		return false
	}
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			cx, cz := anchorX+dx, anchorZ+dz
			idx := int(cz*t.CellW + cx)
			if idx < 0 || idx >= len(t.Plot) {
				continue
			}
			if dx == 0 && dz == 0 {
				t.Plot[idx].SetFeature(feature)
				continue
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
	return true
}

// densePackTeardown is step 3 of the stamp [05 R-FEAT-01 §3], restated for a
// fringe cell by [05 R-FEAT-01 §3-A]: walk the footprint row-major, and for
// every cell whose feature word is not empty call the teardown without honor.
// A teardown that returns false vetoes the stamp immediately, leaving the cells
// already torn torn. So a new feature REPLACES any non-indestructible feature
// it overlaps — the earlier anchor does not survive with a truncated fringe —
// and an indestructible feature under any covered cell vetoes the stamp, whose
// own anchor cell is then not written. Skipping the contested cell and keeping
// both anchors, which this writer used to do, is neither of retail's outcomes.
//
// The rule is the same for a runtime stamp and for map bootstrap, because
// bootstrap now clears the authored raster out of the plot before it stamps
// (see stampFeatureAnchors): retail's loader writes into a grid that starts
// empty, and a raster left doubling as that grid would have the very first
// stamp tear down the features its own source cells still describe.
func (t *Terrain) densePackTeardown(anchorX, anchorZ int32, footX, footZ int32) bool {
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			cx, cz := anchorX+dx, anchorZ+dz
			idx := int(cz*t.CellW + cx)
			if idx < 0 || idx >= len(t.Plot) {
				continue
			}
			if t.Plot[idx].Feature() == PlotFeatureNone {
				continue
			}
			if !t.tearDownFeatureAt(cx, cz) {
				return false
			}
		}
	}
	return true
}

// tearDownFeatureAt is the teardown routine of [05 R-FEAT-01 §4] restricted to
// the plot writes this package owns; every caller in the executable passes
// honor 0, so the honoring variant does not exist here and an indestructible
// feature is never removed by any path.
//
//  1. a `0xFFFE` cell walks back to its anchor;
//  2. an (anchor) word at or above 0xFFFB — empty, void, or any other sentinel
//     — returns false, so a void cell and a stale fringe whose anchor resolves
//     empty both veto the stamp;
//  3. an indestructible definition returns false;
//  4. the anchor's word becomes 0xFFFF and its instance bit clears;
//  5. every cell of the definition's footprint rectangle from that anchor that
//     CURRENTLY holds 0xFFFE becomes 0xFFFF with its instance bit cleared;
//     cells holding anything else are left alone.
//
// The signed offset bytes of a cleared fringe are not touched, and the slot and
// list bookkeeping of step 4 of that section belongs to the feature service,
// not to the plot.
//
// A definition the catalog cannot bind has no footprint to clear and no
// indestructible flag to read. Refusing is Nanolathe's deterministic choice for
// that case, not a reproduction of retail, which would read whatever the
// out-of-range table entry happens to hold.
func (t *Terrain) tearDownFeatureAt(cx, cz int32) bool {
	cell := t.PlotAt(cx, cz)
	if cell == nil {
		return false
	}
	ax, az := cx, cz
	if cell.Feature() == PlotFeatureFringe {
		ax, az = cx+int32(cell.AnchorDXSigned()), cz+int32(cell.AnchorDZSigned())
		cell = t.PlotAt(ax, az)
		if cell == nil {
			return false
		}
	}
	anchor := cell.Feature()
	if anchor >= plotFeatureRealLimit {
		return false
	}
	def, bound := t.FeatureDefAt(anchor)
	if !bound || def.Indestructible {
		return false
	}
	idx := int(az*t.CellW + ax)
	t.Plot[idx].SetFeature(PlotFeatureNone)
	t.Plot[idx].SetOccupied(false)
	footX, footZ := def.FootprintX, def.FootprintZ
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			fringe := t.PlotAt(ax+dx, az+dz)
			if fringe == nil || fringe.Feature() != PlotFeatureFringe {
				continue
			}
			fringe.SetFeature(PlotFeatureNone)
			fringe.SetOccupied(false)
		}
	}
	return true
}

// stampableAnchor reports whether the pass can stamp an authored ordinal at
// this cell: a real index the catalog binds, with a positive footprint that
// fits the plot. Anything else is left in the plot exactly as authored.
func (t *Terrain) stampableAnchor(cx, cz int32, feature uint16) bool {
	if feature >= plotFeatureRealLimit {
		return false
	}
	def, ok := t.FeatureDefAt(feature)
	if !ok || def == nil || def.FootprintX <= 0 || def.FootprintZ <= 0 {
		return false
	}
	return validFootprint(t.CellW, t.CellH, cx, cz, def.FootprintX, def.FootprintZ)
}

// stampFeatureAnchors derives fringe ownership in authored map order. The raw
// feature words are retained as a source snapshot; fringe is then derived
// entirely from the anchors' footprints, and every uncovered authored fringe
// becomes empty after the pass.
//
// Fringe is written over EVERY covered non-anchor cell, whatever the source
// authored there [05 R-FEAT-01 §17]. Retail's loader allocates the plot with
// every feature word empty and never copies the TNT feature word into it; its
// second attribute pass reads ordinals from the attribute array and calls the
// stamp, whose fringe loop reads nothing from the cell. So a covered cell
// authored `0xFFFF` is stamped `0xFFFE` exactly like one authored `0xFFFE` —
// the authored fringe words are redundant data. Leaving an authored-empty
// covered cell empty diverges in every reader that hops a fringe to its anchor:
// the passability classifier [R-DOC04-B], the reclaim scan [05 R-FEAT-01 §6],
// the damage entry [05 R-FEAT-01 §8] and the teardown [05 R-FEAT-01 §4] would
// all treat those cells as neither blocked, reclaimable nor cleared with their
// feature. An authored fringe that no footprint covers is never written by
// retail and stays empty, which is what the post-pass below reproduces.
//
// The snapshot is taken and the cells this pass will re-stamp are cleared out
// of the plot BEFORE the first stamp, because retail's loader stamps into a
// grid that starts empty and reads its ordinals from the TNT attribute array,
// not from the grid it is filling [05 R-FEAT-01 §3]. Leaving the raster in
// place made the plot double as both, and the dense-pack teardown of step 3
// would then tear down the very features the remaining source cells still
// describe. Void sentinels are not cleared — they are the loader's own step-1
// markers — and neither is an ordinal this catalog cannot stamp, which the plot
// preserves verbatim as it always has.
//
// Every completed stamp writes placer into its anchor's flag-byte nibble
// [05 R-FEAT-01 §3 step 6]; a vetoed stamp returns before that step and
// leaves the anchor's nibble as plot expansion wrote it. Expansion already
// wrote TerrainFeaturePlacer into every cell, so the retail placer rewrites
// the same bits. A void cell is never stamped here and never takes the
// nibble (research/extensions/prota-engine.md "Map-owned features drawn
// without line of sight").
func (t *Terrain) stampFeatureAnchors(placer uint8) {
	if t == nil || t.CellW <= 0 || t.CellH <= 0 || len(t.Plot) < int(t.CellW*t.CellH) {
		return
	}
	authored := make([]uint16, len(t.Plot))
	authoredFringe := make([]bool, len(t.Plot))
	stampable := make([]bool, len(t.Plot))
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			idx := int(cz*t.CellW + cx)
			authored[idx] = t.Plot[idx].Feature()
			authoredFringe[idx] = authored[idx] == PlotFeatureFringe
			stampable[idx] = t.stampableAnchor(cx, cz, authored[idx])
			if authoredFringe[idx] || stampable[idx] {
				t.Plot[idx].SetFeature(PlotFeatureNone)
				t.Plot[idx].SetAnchor(0, 0)
			}
		}
	}
	owned := make([]bool, len(t.Plot))
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			idx := int(cz*t.CellW + cx)
			if !stampable[idx] {
				continue
			}
			def, _ := t.FeatureDefAt(authored[idx])
			// A vetoed stamp leaves this anchor unwritten and the cells torn so
			// far torn, which is retail's outcome for a footprint overlapping
			// an indestructible feature [05 R-FEAT-01 §3-A]; the loader has no
			// other recourse and continues with the next source cell.
			if t.writeFeatureRect(cx, cz, authored[idx], def.FootprintX, def.FootprintZ, owned) {
				t.Plot[idx].SetPlacerNibble(placer)
			}
		}
	}
	for i := range t.Plot {
		if authoredFringe[i] && !owned[i] {
			t.Plot[i].SetFeature(PlotFeatureNone)
			t.Plot[i].SetAnchor(0, 0)
		}
	}
}
