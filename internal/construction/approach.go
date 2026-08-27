package construction

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Build-site approach selection [07 §9] "The click", [04 §7.4].
//
// The MOBILEBUILD order's stored position is the footprint's centre,
// `((foot + 2*cell) << 19)` per axis with the site height as Y [07 §9]. That
// position is the SITE and does not change here. The builder's MOVEMENT goal
// is a different quantity: "build-site generation enumerates perimeter
// candidates around the footprint, filters by build distance and placement
// validation, and hands a selected point goal to path search" [07 §9], and
// [04 §7.4] adds that it "sorts a bounded list of candidates" before
// selecting one.
//
// Handing the centre straight to path search walks the builder into its own
// site, which is not what [04 §7.4] describes and not where a builder stands
// in retail. A correction to this file's own first version: that behaviour was
// attributed here to the null-self commit check
// [05 "Silent blocked revalidation before allocation"] rejecting the builder's
// occupancy. Measurement disproved it — construction is the only writer of
// plot occupancy, so a mobile builder parked on its footprint is invisible to
// the placement check. Candidates still clear the site, because "perimeter
// candidates around a footprint" [04 §7.4] means around it, but the reason is
// the contract, not a self-blocking commit.
//
// What research establishes and this file implements: perimeter enumeration
// around the footprint, a build-distance filter, a placement-validation
// filter, a sorted bounded candidate list, and a single selected point goal.
//
// TODO(question): retail's candidate ring geometry is not recovered. This
// enumerates square rings outward from the one immediately outside the
// footprint, bounded by the builder's own footprint extent (past that offset a
// further ring cannot change whether the builder clears the site), and takes
// the best candidate found across them. Whether retail emits one ring, a fixed
// offset, or a ring scaled by either footprint would be settled by tracing the
// build-site generator's candidate emission.
//
// TODO(question): the sort key, the tie-break, and the size of the "bounded
// list" [04 §7.4] are not recovered. Candidates here are ordered by squared
// planar distance from the builder's current position, tie-broken by ring
// offset and then by perimeter enumeration order, and the list is bounded by
// the enumerated rings rather than by an invented cap. Tracing the comparator
// and the candidate array's capacity would settle both.
//
// TODO(question): the build-distance filter measures the candidate to the
// nearest point of the site rectangle, the same origin the walk gate uses; see
// siteRangePoint for why the previous centre measurement is disproved by the
// authored data and for what remains untraced [R-P0-06][fmt fbi].
//
// The selected point is bound as the mover's movement-goal handle
// (movement.BindMoveGoal), which is what makes it the steering target rather
// than a suggestion: the mover reads the handle, not the order's stored
// position, so consuming the short published route no longer drags the builder
// onto the centre. See internal/movement/movegoal.go for the handle's shape
// and for what remains untraced about it.

// approachRing returns the lattice rectangle whose border enumerates candidate
// BUILDER ANCHORS at the given offset around the site anchored at
// (anchorX,anchorZ) with extent (footX,footZ) [04 §7.4].
//
// Candidates are anchors, not bare cells, because that is the quantity the
// clearance test needs: a builder anchored at c covers [c, c+builderExtent),
// and the ring at offset 1 is exactly the set of anchors whose footprint sits
// flush against one side of the site. Enumerating cell centres instead made
// clearance depend on how the anchor snap rounded the mover's halt position,
// which is not a property of the candidate at all.
func approachRing(anchorX, anchorZ, footX, footZ, builderX, builderZ, offset int32) path.Rect {
	return path.Rect{
		Min: path.Cell{X: anchorX - builderX - (offset - 1), Z: anchorZ - builderZ - (offset - 1)},
		Max: path.Cell{X: anchorX + footX - 1 + offset, Z: anchorZ + footZ - 1 + offset},
	}
}

// siteAnchorCell resolves the north-west footprint cell and extent of the site
// stored on a MOBILEBUILD node [07 §9]. The node's Goal is the footprint
// centre; the anchor is the same snap the commit path performs.
func (s *Service) siteAnchorCell(node *orders.Node) (anchorX, anchorZ, footX, footZ int32, ok bool) {
	if s == nil || node == nil {
		return 0, 0, 0, 0, false
	}
	def := s.getProductDefForNode(node)
	if def == nil {
		return 0, 0, 0, 0, false
	}
	footX, footZ = world.FootprintForUnit(s.Catalog, def)
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	anchor, err := world.SnapFootprintAnchor(node.GoalX, node.GoalZ, extent)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	cell := anchor.Cell()
	return cell.X, cell.Z, footX, footZ, true
}

// builderStandPoint returns the world point a builder must stand at so its
// footprint anchors exactly at the candidate cell, together with that
// footprint's half-open cell rectangle.
//
// The point is the footprint's own centre, (extent + 2*anchor) << 19
// [07 §9], which is the middle of the anchor's snap interval — a full half
// cell from either boundary. That matters because the mover halts anywhere
// inside the local steering threshold [04 §3.5]; aiming at a cell centre
// instead puts an even-extent builder exactly ON a snap boundary, where a
// one-pixel shortfall lands it in the neighbouring anchor.
func (s *Service) builderStandPoint(builder *units.Unit, cell path.Cell) (x, z numeric.Fixed, minX, minZ, maxX, maxZ int32, ok bool) {
	if s == nil || builder == nil || builder.Def == nil {
		return 0, 0, 0, 0, 0, 0, false
	}
	bx, bz := world.FootprintForUnit(s.Catalog, builder.Def)
	extent, err := world.NewFootprintExtent(bx, bz)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, false
	}
	centre, err := world.CenterForFootprint(world.NewFootprintAnchor(cell.X, cell.Z), extent)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, false
	}
	return centre.X(), centre.Z(), cell.X, cell.Z, cell.X + bx, cell.Z + bz, true
}

// builderFootprintAnchor returns the cell the builder's footprint anchors at
// when its centre stands at the world point (x,z), using the same snap the
// occupancy commit uses [04 §8.2].
func (s *Service) builderFootprintAnchor(builder *units.Unit, x, z numeric.Fixed) (cellX, cellZ int32, ok bool) {
	if s == nil || builder == nil || builder.Def == nil {
		return 0, 0, false
	}
	bx, bz := world.FootprintForUnit(s.Catalog, builder.Def)
	extent, err := world.NewFootprintExtent(bx, bz)
	if err != nil {
		return 0, 0, false
	}
	anchor, err := world.SnapFootprintAnchor(x, z, extent)
	if err != nil {
		return 0, 0, false
	}
	c := anchor.Cell()
	return c.X, c.Z, true
}

// rectsOverlap reports whether two half-open cell rectangles intersect.
func rectsOverlap(aMinX, aMinZ, aMaxX, aMaxZ, bMinX, bMinZ, bMaxX, bMaxZ int32) bool {
	return aMinX < bMaxX && bMinX < aMaxX && aMinZ < bMaxZ && bMinZ < aMaxZ
}

// needsApproach reports whether a mobile builder must still move before its
// build order can commit [04 §3.4][05][R-P0-06]. It is the single gate the
// walk submission and the state-2 handler share, and it is the established
// nanolathe-reach test.
//
// Measured, not assumed: standing on the site is NOT a second reason. Only
// construction stamps plot occupancy (reservePlacement is the sole writer of
// the layer-A occupant), so a mobile builder parked on its own footprint is
// invisible to the placement check and cannot reject its own site. An earlier
// reading of the armlab stall — including this unit's own first commit and the
// registry entry it wrote — attributed the rejection to the builder's
// occupancy. Direct measurement disproved it: the commit validator accepted
// the site every time, and the reservation refused it over an ALREADY BUILT
// armsolar occupying an armlab corner whose yard byte does not test occupancy
// (see reservePlacement).
//
// Gating overlap here was also measured and rejected on its own terms: with
// the term on, a builder that halts inside the local steering threshold
// [04 §3.5] can snap to an anchor one cell off the candidate it was sent to,
// so the runtime overlap test reports overlap forever and the order never
// leaves state 2. The approach point still keeps the builder off its own site
// by construction — every candidate clears it — which is what [04 §7.4]
// describes; no runtime overlap gate is needed to achieve that.
func (s *Service) needsApproach(builder *units.Unit, node *orders.Node) bool {
	if s == nil || s.Movement == nil || builder == nil || node == nil {
		return false
	}
	if builder.Def == nil || builder.Def.BuildDistance == 0 {
		return false
	}
	px, pz, ok := s.siteRangePoint(node, builder.X, builder.Z)
	if !ok {
		px, pz = node.GoalX, node.GoalZ
	}
	return !s.isWithinNanoRange(builder, px, pz)
}

// siteRangePoint returns the point of the site's footprint rectangle nearest
// (fromX,fromZ) — the rectangle clamped to that point.
//
// DISTANCE ORIGIN, and the one part of this file that changes an existing
// reading rather than adding to it. isWithinNanoRange measured to the site's
// CENTRE, and nanoReach and isWithinNanoRange both already carried
// TODO(question) markers saying the reach origin and "whether retail measures
// to the footprint center, edge, or bounds" were unrecovered [R-P0-06]
// [fmt fbi]. Centre measurement is disproved by the authored data: ARMCOM
// authors builddistance 60 and ARMLAB is 6x6, whose half-extent alone is 48
// map pixels, so a commander measuring to the centre must stand within 12
// pixels of the footprint edge — inside its own build site for any builder
// wider than one cell — to build a stock kbot lab at all. Measuring to the
// nearest point of the site rectangle is the other reading the marker already
// named, and it is the one the authored numbers admit.
//
// TODO(question): this is a reading chosen by data evidence, not a traced
// comparison. Retail's own reach test — its origin piece, whether it clamps to
// the footprint rectangle or to a radius, and whether the comparison is planar
// — is still untraced [R-P0-06]. Tracing the build-range check would settle it
// and may replace this clamp.
func (s *Service) siteRangePoint(node *orders.Node, fromX, fromZ numeric.Fixed) (x, z numeric.Fixed, ok bool) {
	anchorX, anchorZ, footX, footZ, resolved := s.siteAnchorCell(node)
	if !resolved {
		return 0, 0, false
	}
	minX, minZ := world.CellToWorld(anchorX), world.CellToWorld(anchorZ)
	maxX, maxZ := world.CellToWorld(anchorX+footX), world.CellToWorld(anchorZ+footZ)
	return clampFixed(fromX, minX, maxX), clampFixed(fromZ, minZ, maxZ), true
}

// SiteRangePointPublic exposes siteRangePoint for wiring tests and session
// diagnostics [R-P0-06].
func (s *Service) SiteRangePointPublic(node *orders.Node, fromX, fromZ numeric.Fixed) (x, z numeric.Fixed, ok bool) {
	return s.siteRangePoint(node, fromX, fromZ)
}

func clampFixed(v, lo, hi numeric.Fixed) numeric.Fixed {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// approachCandidate is one perimeter candidate with its sort keys.
type approachCandidate struct {
	cell   path.Cell     // the builder anchor
	standX numeric.Fixed // world point that snaps to that anchor
	standZ numeric.Fixed
	offset int32 // ring offset — first tie-break
	order  int   // perimeter enumeration index — second tie-break
	distS  int64 // squared planar distance from the builder — the sort key
}

// sortCandidates orders a candidate list by the documented total order.
func sortCandidates(list []approachCandidate) {
	sort.SliceStable(list, func(a, b int) bool {
		// Ring-major: the perimeter immediately around the footprint is where
		// "perimeter candidates around a footprint" [04 §7.4] puts them, and an
		// outer ring is only a fallback when the near one yields nothing usable.
		if list[a].offset != list[b].offset {
			return list[a].offset < list[b].offset
		}
		if list[a].distS != list[b].distS {
			return list[a].distS < list[b].distS
		}
		return list[a].order < list[b].order
	})
}

// SelectBuildApproach is the exported form of selectBuildApproach for the
// subsystem regression test and for session diagnostics. It returns the
// builder anchor chosen for a MOBILEBUILD site and the world point the mover
// is steered at to reach it [07 §9][04 §7.4].
func (s *Service) SelectBuildApproach(builder *units.Unit, node *orders.Node) (anchor path.Cell, standX, standZ numeric.Fixed, ok bool) {
	return s.selectBuildApproach(builder, node)
}

// selectBuildApproach returns the movement goal a mobile builder is given for
// a MOBILEBUILD site [07 §9][04 §7.4]: a perimeter candidate around the
// footprint, filtered by build distance and placement validation, taken from a
// sorted bounded candidate list.
//
// The last result is false when no site or no clearing candidate can be
// resolved at all; callers keep their existing goal in that case.
func (s *Service) selectBuildApproach(builder *units.Unit, node *orders.Node) (path.Cell, numeric.Fixed, numeric.Fixed, bool) {
	anchorX, anchorZ, footX, footZ, ok := s.siteAnchorCell(node)
	if !ok || builder == nil || builder.Def == nil {
		return path.Cell{}, 0, 0, false
	}
	bx, bz := world.FootprintForUnit(s.Catalog, builder.Def)
	// Past this offset a further ring only moves the builder further from the
	// site it is trying to reach; offset 1 already clears the footprint by
	// construction [04 §7.4].
	maxOffset := bx
	if bz > maxOffset {
		maxOffset = bz
	}
	maxOffset++

	reach := nanoReach(builder)
	reachSq := int64(reach) * int64(reach)

	// Three lists in preference order. Every entry of all three clears the
	// site; the lists differ only in which soft filter they also passed.
	var inRangeAndValid, valid, clearing []approachCandidate
	for offset := int32(1); offset <= maxOffset; offset++ {
		ring := approachRing(anchorX, anchorZ, footX, footZ, bx, bz, offset)
		// Perimeter enumeration reuses the rectangle-perimeter goal family,
		// which already enumerates exactly the border in a deterministic order
		// [04 §7.2] C8. No second perimeter policy is introduced.
		cells := path.RectPerimeterGoal(ring).Enumerate(nil)
		for i, c := range cells {
			cx, cz, bMinX, bMinZ, bMaxX, bMaxZ, okPt := s.builderStandPoint(builder, c)
			if !okPt {
				continue
			}
			if rectsOverlap(bMinX, bMinZ, bMaxX, bMaxZ, anchorX, anchorZ, anchorX+footX, anchorZ+footZ) {
				continue // a candidate standing on the site is not "around" it [04 §7.4]
			}
			cand := approachCandidate{
				cell:   c,
				standX: cx,
				standZ: cz,
				offset: offset,
				order:  i,
				distS:  planarDistSq(builder.X, builder.Z, cx, cz),
			}
			clearing = append(clearing, cand)
			// Placement validation [07 §9][04 §7.4]: the builder has to be able
			// to stand there. This is the builder's OWN footprint, so it carries
			// the builder's self identity — unlike the product's commit check,
			// which stays null-self [05 "Silent blocked revalidation before
			// allocation"].
			if !s.builderCanStandAt(builder, cx, cz) {
				continue
			}
			valid = append(valid, cand)
			// Build-distance filter [07 §9]: the candidate must put the site
			// inside the builder's authored reach [fmt fbi] via nanoReach,
			// measured to the nearest point of the site rectangle exactly as
			// the walk gate measures it (see siteRangePoint).
			rx, rz, okR := s.siteRangePoint(node, cx, cz)
			if !okR {
				rx, rz = node.GoalX, node.GoalZ
			}
			if reachSq > 0 && planarDistSq(cx, cz, rx, rz) > reachSq {
				continue
			}
			inRangeAndValid = append(inRangeAndValid, cand)
		}
	}
	// Degradation order. Every tier still clears the site, which is what makes
	// a candidate a perimeter candidate at all; the soft filters are dropped in
	// turn rather than leaving the builder with no approach.
	list := inRangeAndValid
	if len(list) == 0 {
		list = valid // range dropped; see the nanoReach TODO(question) above
	}
	if len(list) == 0 {
		list = clearing // placement validation dropped as well
	}
	if len(list) == 0 {
		return path.Cell{}, 0, 0, false
	}
	sortCandidates(list)
	return list[0].cell, list[0].standX, list[0].standZ, true
}

// builderCanStandAt reports whether the builder's own footprint validates when
// its centre is placed at the world point (x,z) [04 §7.4][R-P0-08].
func (s *Service) builderCanStandAt(builder *units.Unit, x, z numeric.Fixed) bool {
	if s == nil || s.Terrain == nil || builder == nil || builder.Def == nil {
		return false
	}
	bx, bz := world.FootprintForUnit(s.Catalog, builder.Def)
	extent, err := world.NewFootprintExtent(bx, bz)
	if err != nil {
		return false
	}
	anchor, err := world.SnapFootprintAnchor(x, z, extent)
	if err != nil {
		return false
	}
	rect, err := world.NewFootprintRect(anchor, extent)
	if err != nil {
		return false
	}
	rules, err := placementRules(s, builder.Def)
	if err != nil {
		return false
	}
	_, err = s.Terrain.CheckPlacement(world.PlacementQuery{
		Rect:   rect,
		Rules:  rules,
		Self:   uint16(builder.Handle),
		Mobile: true,
	})
	return err == nil
}

// planarDistSq is the squared X/Z distance between two world points. The
// nanolathe range test is planar [05 "Unit reclaim"].
func planarDistSq(ax, az, bx, bz numeric.Fixed) int64 {
	dx := int64(ax) - int64(bx)
	dz := int64(az) - int64(bz)
	return dx*dx + dz*dz
}
