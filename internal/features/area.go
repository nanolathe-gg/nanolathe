package features

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// AreaCandidate is the feature one covered cell of a blast offers to the area
// enumeration of [06 §9.3]: the ANCHOR cell the damage entry is called with,
// and the reference point the blast measures its distance to.
type AreaCandidate struct {
	// CX, CZ are the anchor cell. A fringe cell hops back to its anchor first,
	// so the entry is always called with the anchor [05 R-FEAT-01 §8].
	CX, CZ int
	// X, Y, Z is the reference point [06 R-WPN-04 §3]: the live instance's
	// stored position when the anchor carries one, otherwise the definition's
	// footprint centre at that anchor with the bilinear terrain height there —
	// and no sea-level floor, so a feature on the sea bed is measured at the
	// bed.
	X, Y, Z numeric.Fixed
}

// AreaCandidateAt resolves the feature candidate covering cell (cx, cz) for the
// area-damage walk of [06 §9.3]. It reports false when the cell offers none:
// off the map, empty, or resolving to a word in the sentinel band.
//
// It does not test the radius and does not deduplicate — both belong to the
// caller, and their ORDER is a contract: [06 §9.3] tests a feature's distance
// BEFORE the 64-entry anchor memory, the opposite of the unit walk, so an
// out-of-radius feature never consumes a memory entry.
func (s *Service) AreaCandidateAt(cx, cz int) (AreaCandidate, bool) {
	if s == nil || s.Terrain == nil || s.Terrain.Plot == nil {
		return AreaCandidate{}, false
	}
	w := int(s.Terrain.CellW)
	h := int(s.Terrain.CellH)
	if w <= 0 || h <= 0 || cx < 0 || cx >= w || cz < 0 || cz >= h {
		return AreaCandidate{}, false
	}
	if len(s.Terrain.Plot) < w*h {
		return AreaCandidate{}, false
	}
	cell := s.Terrain.Plot[cz*w+cx]
	if cell.IsEmpty() {
		return AreaCandidate{}, false
	}
	// The fringe hop of [05 R-FEAT-01 §8] step 1, the same signed-offset walk
	// the damage entry uses [SPEC_CONFLICTS SC6].
	ax, az := cx, cz
	if cell.IsFringe() {
		ax = cx + int(cell.AnchorDXSigned())
		az = cz + int(cell.AnchorDZSigned())
		if ax < 0 || ax >= w || az < 0 || az >= h {
			return AreaCandidate{}, false
		}
	}
	// The anchor's word must resolve below the sentinel band; ResolveFeature
	// returns false for every sentinel, which is step 2's `>= 0xFFFB` return.
	feat, ok := world.ResolveFeature(s.Terrain.Plot, w, h, ax, az)
	if !ok {
		return AreaCandidate{}, false
	}
	cand := AreaCandidate{CX: ax, CZ: az}
	if inst, live := s.instances[az*w+ax]; live && inst != nil {
		cand.X, cand.Y, cand.Z = inst.X, inst.Y, inst.Z
		return cand, true
	}
	def, bound := s.Terrain.FeatureDefAt(feat)
	if !bound || def == nil {
		return AreaCandidate{}, false
	}
	cand.X = footprintCentreWorld(ax, def.FootprintX)
	cand.Z = footprintCentreWorld(az, def.FootprintZ)
	cand.Y = s.Terrain.HeightAt(cand.X, cand.Z)
	return cand, true
}

// ReclaimAt is the reclaim payout's cell entry [05 R-WORK-01 §5]: it reports
// the pools the builder is credited with and settles the cell, or reports false
// and changes nothing.
//
// It is the service-owned twin of the package-level ReclaimTransition, and its
// gates and pool arithmetic are that function's, unchanged — the cell must hold
// a real feature whose definition is `reclaimable` and not `indestructible`,
// and the credit is the definition's own metal and energy pools crossing into
// the ledger as float32 [05 "Feature reclaim"][05 R-ECO-01 §2] (I2 allowlist).
//
// What differs is the cell's fate. ReclaimTransition clears the footprint and
// stamps `featurereclamate` unconditionally; this routes through the transition
// of [05 R-FEAT-01 §5], so a definition naming `seqnamereclamate` plays that
// sequence out and the feature phase stamps the successor when it ends, while
// one naming none still replaces at once (step 3). The payout itself is
// unchanged either way, and lands on the same visit it always did.
func (s *Service) ReclaimAt(cx, cz int) (metal, energy float32, ok bool) {
	if s == nil || s.Terrain == nil {
		return 0, 0, false
	}
	cell := s.Terrain.PlotAt(int32(cx), int32(cz))
	if cell == nil || !cell.IsRealFeature() {
		return 0, 0, false
	}
	def, bound := s.Terrain.FeatureDefAt(cell.Feature())
	if !bound || def == nil {
		return 0, 0, false
	}
	if !def.Reclaimable || def.Indestructible {
		return 0, 0, false
	}
	// A cell already carrying an event record — burning, dying or reclaiming —
	// is inert to every further cause, the reclaim executor's payout included
	// [05 R-FEAT-01 §5 "same-tick precedence"][05 R-FEAT-01 §15].
	if inst := s.instances[cz*int(s.Terrain.CellW)+cx]; inst != nil && (inst.IsBurning || inst.IsAnimating) {
		return 0, 0, false
	}
	metal = float32(def.Metal)
	energy = float32(def.Energy)
	if !s.transitionFeatureAt(cx, cz, def, true) {
		s.replaceFeatureAt(cx, cz, def.FeatureReclamateDef)
	}
	return metal, energy, true
}
