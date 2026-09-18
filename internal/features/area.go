package features

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
	// A resting sprite's convenience record is not an attached instance.
	// The anchor bit selects the stored-position branch [06 R-WPN-04 §3].
	if s.Terrain.Plot[az*w+ax].Occupied() {
		inst := s.instances[az*w+ax]
		if inst == nil {
			return AreaCandidate{}, false // no record available for the attached slot
		}
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

// reclaimPayoutTarget is the reclaim payout's admission, shared by the two
// entries that carry it: it takes the position the ORDER recorded, resolves it,
// applies the payout guard of [05 R-FEAT-01 §15] and reports the ANCHOR cell
// everything after the guard operates on.
//
// The two cells are deliberately separate. Retail's payout helper resolves the
// recorded position twice and keeps the FIRST, unhopped result for the guard's
// instance-attached cell bit, while the guard's definition bit comes from the
// anchor's catalog entry [05 R-FEAT-01 §15][05 R-WORK-01 §5]. For a single-cell
// feature the two coincide; a multi-cell sprite definition reclaimed from one of
// its fringe cells reads that fringe cell's bit, which the stamp leaves clear,
// so the payout is not refused even while the anchor carries a live instance.
//
// The conjunction means "a sprite feature that currently has a live animation
// instance" — burning, or already playing its death or reclaim sequence — which
// is what "burning blocks reclaim" describes, and is also what keeps a second
// visit of the executor from crediting the pools twice while the sequence runs.
// It never applies to a 3D wreck: the stamp sets a 3D definition's instance bit
// always, but its definition bit is clear, so a sinking wreck stays reclaimable
// throughout.
func reclaimPayoutTarget(t *world.Terrain, cx, cz int) (ax, az int, def *content.FeatureDef, ok bool) {
	recorded := t.PlotAt(int32(cx), int32(cz))
	if recorded == nil {
		return 0, 0, nil, false
	}
	// The second resolution is the hop to the anchor, exactly as FeatureAt
	// makes it [05 R-ECO-02 §2]; everything below the guard is the anchor's.
	ax, az = cx, cz
	if recorded.IsFringe() {
		ax += int(recorded.AnchorDXSigned())
		az += int(recorded.AnchorDZSigned())
	}
	cell := t.PlotAt(int32(ax), int32(az))
	if cell == nil || !cell.IsRealFeature() {
		return 0, 0, nil, false
	}
	def, bound := t.FeatureDefAt(cell.Feature())
	if !bound || def == nil {
		return 0, 0, nil, false
	}
	if !def.Reclaimable || def.Indestructible {
		return 0, 0, nil, false
	}
	if isSpriteDef(def) && recorded.Occupied() {
		return 0, 0, nil, false // the payout guard's two bits [05 R-FEAT-01 §15]
	}
	return ax, az, def, true
}

// ReclaimAt is the reclaim payout's cell entry [05 R-WORK-01 §5]: it reports
// the pools the builder is credited with and settles the cell, or reports false
// and changes nothing.
//
// It is the service-owned twin of the package-level ReclaimTransition, and its
// gates and pool arithmetic are that function's, unchanged — cx, cz are the
// position the ORDER recorded, admission is reclaimPayoutTarget's, and the
// credit is the definition's own metal and energy pools crossing into the
// ledger as float32 [05 "Feature reclaim"][05 R-ECO-01 §2] (I2 allowlist).
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
	ax, az, def, ok := reclaimPayoutTarget(s.Terrain, cx, cz)
	if !ok {
		return 0, 0, false
	}
	metal = float32(def.Metal)
	energy = float32(def.Energy)
	if !s.transitionFeatureAt(ax, az, def, true) {
		s.replaceFeatureAt(ax, az, def.FeatureReclamateDef)
	}
	return metal, energy, true
}
