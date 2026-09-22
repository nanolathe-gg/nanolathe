package construction

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The resurrection wait delay and the order's sole simulation draw both live
// in internal/orders, which owns the row: the delay is orders.ResurrectionDelay
// — trunc(buildTime*0.3 / (uint16(workertime)/30)), the 0.3 belonging to this
// state alone — and the draw is the approach phase's bounded vertical term,
// taken through the queue's own stream helper [05 "Resurrection"]
// [05 R-WORK-01 §7]. Second copies of both used to stand here with no caller;
// the one below is what this package implements, phase 5's create step.
//
// Retired (WU-19-143): the draw was documented here as "the placement jitter
// draw ... bounded by the feature's spread byte (feature catalog spread
// field)". Both halves were wrong. There is no authored "spread" feature key —
// retail's feature parser reads no such key at all [05 R-FEAT-01 §1],
// confirmed by a full census of every stock feature section — and the draw is
// not a placement jitter: it is the approach phase's vertical walk-target
// term, bounded by the feature's ordinary `height` byte, and it happens before
// this package's Resurrect [05 "Resurrection"][05 R-WORK-01 §7 phase 1].

// FeatureNameToDefName implements the corpse-name-to-unit-name truncation:
// copy the feature name, truncate it at the first underscore, then look the
// result up in the unit catalog [05 R-WORK-01 §7 phase 3].
func FeatureNameToDefName(featureName string) string {
	if idx := strings.IndexByte(featureName, '_'); idx >= 0 {
		return featureName[:idx]
	}
	return featureName
}

// Resurrect performs resurrection allocation [05 R-WORK-01 §7 phase 5
// "create"]. It first allocates the new unit at the feature's position; only
// a successful allocation may remove the feature. It then sets the product's
// remaining fraction to 0 and health to 1. There is no ledger cost, and delay
// is computed by orders.ResurrectionDelay.
// Per-def limit -1 sentinel unlimited; pool fail returns the same 300-tick
// retry with "Unable to create any more units".
//
// Retired (WU-19-143): this used to look up a per-feature "jitter spread"
// byte here (including a fringe-anchor resolution walk to find it for a
// multi-cell footprint) and draw against it before removing the feature. Both
// the byte and the call site were wrong: no such feature key exists in retail
// [05 R-FEAT-01 §1], and the order's one simulation draw belongs to phase 1's
// approach step, not phase 5's create step which this function implements —
// phase 5 draws no randomness at all [05 "Resurrection"]. The `sim` parameter
// is kept for call-site stability (phase 1's approach draw is a separate
// concern, owned by internal/orders) but this function no longer uses it.
func (s *Service) Resurrect(builder *units.Unit, featureCell *world.PlotCell, def *content.UnitDef, posX, posY, posZ numeric.Fixed, sim *rng.Simulation, request ResurrectionRequest) (ResurrectionResult, error) {
	if s == nil || s.World == nil || builder == nil || def == nil {
		return ResurrectionResult{}, nil
	}
	if !CheckPerDefLimit(s.World, builder.Owner, def) {
		return ResurrectionResult{}, ErrLimit
	}
	snapshot := s.captureResurrectionSnapshot(featureCell)
	var prod *units.Unit
	if s.Allocator != nil {
		allocated, err := s.Allocator(builder.Owner, def, posX, posY, posZ)
		if err != nil || allocated == nil {
			return ResurrectionResult{}, ErrLimit
		}
		prod = allocated
	} else {
		facing := s.ResolveStructureFacing(def, units.FacingFromHeading(request.Heading))
		h, err := s.World.CreateFacing(def, builder.Owner, posX, posY, posZ, facing)
		if err != nil {
			return ResurrectionResult{}, ErrLimit
		}
		prod = s.World.Unit(h)
		if prod == nil {
			return ResurrectionResult{}, ErrLimit
		}
	}
	result := ResurrectionResult{Unit: prod}
	currentTarget := pool.Handle(0)
	orderPresent := request.BindTarget != nil
	if orderPresent {
		currentTarget = request.BindTarget(prod)
	}
	// Retail rereads the feature root after allocation. A real, bound feature
	// continues normally; its identity is deliberately not compared with the
	// pre-allocation definition because same-tick successor identity remains
	// unresolved [05 R-WORK-01 §7][06 R-DMG-01 §4].
	postRoot, postOK := s.resurrectionRootWithDefinition(featureCell)
	if !postOK {
		createdType := uint32(0)
		created := s.World.Unit(currentTarget)
		if s.Catalog != nil && created != nil && created.Def != nil {
			createdType, _ = s.Catalog.UnitDefIndex(created.Def.CanonicalKey)
		}
		finalization := ResurrectionFinalization{
			Snapshot: snapshot, Product: prod,
			PriorTarget: request.PriorTarget, CurrentTarget: currentTarget,
			OrderedType: request.OrderedType, CreatedType: createdType,
			OrderPresent: orderPresent,
		}
		if !s.rules().FinalizeResurrection(s, finalization) {
			return result, nil
		}
	} else {
		// Allocation refusal returns above before this destructive transition.
		// A normal post-create reread removes whichever real feature it found;
		// the unresolved same-tick successor case therefore retains retail's
		// absence of an identity comparison.
		s.removeFeature(postRoot)
	}
	prod.Remaining = 0 // finished [05 R-WORK-01 §7 "Established — the transplant"]
	prod.Health = 1    // one hit point, not max [05 R-WORK-01 §7 "Established — the transplant"]
	result.Finalized = true
	return result, nil
}

// captureResurrectionSnapshot records the root feature state before allocation.
// The recovery contract is narrower than an ordinary real-feature lookup: the
// definition must be bound and carry the reclaimable bit that admits a corpse
// to the retail resurrection row [05 R-WORK-01 §7].
func (s *Service) captureResurrectionSnapshot(cell *world.PlotCell) ResurrectionSnapshot {
	root, ok := s.resurrectionRootWithDefinition(cell)
	if !ok {
		return ResurrectionSnapshot{}
	}
	definition := root.Feature()
	featureDef, ok := s.Terrain.FeatureDefAt(definition)
	if !ok || featureDef == nil || !featureDef.Reclaimable {
		return ResurrectionSnapshot{}
	}
	return ResurrectionSnapshot{
		Root: root, Definition: definition, Animation: root.AnchorWord(), Valid: true,
	}
}

// resurrectionRootWithDefinition follows one fringe cell to its root and
// accepts any real feature identity that is bound by the terrain catalog. The
// post-allocation reread intentionally does not compare identity with the
// pre-allocation snapshot; that same-tick successor question is still unknown.
func (s *Service) resurrectionRootWithDefinition(cell *world.PlotCell) (*world.PlotCell, bool) {
	if s == nil || s.Terrain == nil || cell == nil {
		return nil, false
	}
	root := cell
	if cell.Feature() == world.PlotFeatureFringe {
		idx := -1
		for i := range s.Terrain.Plot {
			if &s.Terrain.Plot[i] == cell {
				idx = i
				break
			}
		}
		if idx < 0 || s.Terrain.CellW <= 0 {
			return nil, false
		}
		cx, cz := int32(idx)%s.Terrain.CellW, int32(idx)/s.Terrain.CellW
		root = s.Terrain.PlotAt(cx+int32(cell.AnchorDXSigned()), cz+int32(cell.AnchorDZSigned()))
		if root == nil {
			return nil, false
		}
	}
	if !root.IsRealFeature() {
		return nil, false
	}
	_, ok := s.Terrain.FeatureDefAt(root.Feature())
	return root, ok
}

// removeFeature is the feature-removal helper the resurrection create step
// calls [05 R-WORK-01 §7 phase 5], restricted to the plot writes this package
// can reach. Its cell walk is [05 R-FEAT-01 §4]'s teardown, which
// internal/world implements for its own callers:
//
//  1. a fringe cell walks back to its anchor through the two stored signed
//     offset bytes;
//  2. the anchor's word becomes empty and its live-instance bit clears;
//  3. every cell of the DEFINITION'S FOOTPRINT RECTANGLE from that anchor that
//     currently holds fringe is cleared the same way; cells holding anything
//     else are left alone.
//
// SETTLED (WU-19-166), retiring an accepted-placeholder marker that read "the multi-cell
// footprint sweep is not fully located beyond the single anchor plus its
// fringe; the removal helper's own footprint handling remains open. Decider:
// static trace of that helper's cell walk." The walk was already traced and
// closed as [05 R-FEAT-01 §4], under the name *teardown* rather than *removal
// helper*, and internal/world's plot-side copy of it cites that section. What
// stood here instead was a sweep of the WHOLE PLOT for any fringe cell whose
// signed offsets happened to resolve to this anchor — quadratic in map area
// per resurrection, and not the traced set: retail visits the footprint
// rectangle and nothing outside it. It also left the flag byte's live-instance
// bit set on every cleared cell, so the resurrected corpse's cells still read
// as feature-occupied afterwards.
//
// The anchor index is still located by pointer identity: the caller hands this
// service a cell pointer rather than a cell pair, and widening that signature
// would change internal/session's call site, which this unit does not own.
func (s *Service) removeFeature(cell *world.PlotCell) {
	clearCell := func(c *world.PlotCell) {
		c.SetFeature(world.PlotFeatureNone)
		c.SetAnchorWord(0)
		c.SetOccupied(false) // the live-instance bit of [05 R-FEAT-01 §4] step 4
	}
	if s == nil || s.Terrain == nil || s.Terrain.Plot == nil || s.Terrain.CellW <= 0 || s.Terrain.CellH <= 0 {
		clearCell(cell) // no plot geometry bound: this cell is all the helper can reach
		return
	}
	idx := -1
	for i := range s.Terrain.Plot {
		if &s.Terrain.Plot[i] == cell {
			idx = i
			break
		}
	}
	if idx < 0 {
		clearCell(cell) // a cell outside this terrain's plot
		return
	}
	ax, az := int32(idx)%s.Terrain.CellW, int32(idx)/s.Terrain.CellW
	anchor := cell
	if anchor.Feature() == world.PlotFeatureFringe { // step 1
		ax += int32(anchor.AnchorDXSigned())
		az += int32(anchor.AnchorDZSigned())
		anchor = s.Terrain.PlotAt(ax, az)
		if anchor == nil {
			clearCell(cell) // a stale fringe whose anchor is off the map
			return
		}
	}
	// The footprint is read before the anchor's word is cleared, because the
	// word is what resolves the definition.
	footX, footZ := int32(1), int32(1)
	if def, ok := s.Terrain.FeatureDefAt(anchor.Feature()); ok && def != nil {
		if def.FootprintX > 0 {
			footX = def.FootprintX
		}
		if def.FootprintZ > 0 {
			footZ = def.FootprintZ
		}
	}
	clearCell(anchor) // step 2
	for dz := int32(0); dz < footZ; dz++ {
		for dx := int32(0); dx < footX; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			fringe := s.Terrain.PlotAt(ax+dx, az+dz)
			if fringe == nil || fringe.Feature() != world.PlotFeatureFringe {
				continue // step 3: anything but fringe is left alone
			}
			clearCell(fringe)
		}
	}
	// The teardown ENDS by restamping every named movement class over the
	// footprint rectangle, so the cells the resurrected wreck vacated unblock
	// in the same call [03 §5.1.2][03 R-LAYER §2] call site 2.
	s.Terrain.NoteFootprintRestamp(ax, az, int16(footX), int16(footZ))
}
