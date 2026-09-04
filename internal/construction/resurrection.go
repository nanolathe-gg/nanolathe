package construction

import (
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Resurrection delay uses the sole 0.3 constant in the executable: a stored
// double belonging to this state alone, not a general construction-speed,
// repair, reclaim, or capture multiplier
// [05 "Resurrection", "Established fact — delay"][05 R-WORK-01 §7].
// delay = trunc(buildTime*0.3 / floor(workTime/30)) — plus underscore
// truncation of the corpse name. The order's sole simulation-RNG draw is not
// a placement jitter: it is phase 1's approach-point vertical term, bounded
// by the feature's height byte, not by any feature "spread" field — see
// ResurrectionJitter [05 "Resurrection", "Established — cost and
// randomness"].
const resurrectionCoeff = 0.3 // [05 R-WORK-01 §7 "Established — the delay"]

// ResurrectionDelay computes delay ticks [05 R-WORK-01 §7 "Established — the
// delay"] (resurrection's wait phase).
//
//	q     = uint16(workTime) / 30            // integer division
//	delay = trunc(buildTime*0.3 / q)         // toward zero [I3]
//
// The sub-thirty `workertime` edge is a zero delay, not a sentinel: q is
// zero, the x87 divide yields an infinity (or a NaN when buildTime is zero
// too), and the truncating helper's 64-bit indefinite result has a zero low
// 32-bit half, which is the whole of what the state stores in its 32-bit
// delay field [05 R-WORK-01 §7 "Established — the width of the stored
// delay"][01 R-DET-01 §1]. Phase 4's zero test then fires on the first visit
// and the resurrection completes at once. (Settled 2026-09-02, RWU-19-40:
// this used to return the int32 minimum, which never equals zero and would
// have made the wait state repeat forever.)
func ResurrectionDelay(buildTime int32, workerTime int32) int32 {
	worker := int32(uint16(workerTime)) / 30 // retail zero-extends the 16-bit field
	if worker == 0 {
		return 0
	}
	f := float64(buildTime) * resurrectionCoeff / float64(worker)
	return int32(math.Trunc(f)) // trunc toward zero [01 §8] I3
}

// FeatureNameToDefName implements the corpse-name-to-unit-name truncation:
// copy the feature name, truncate it at the first underscore, then look the
// result up in the unit catalog [05 R-WORK-01 §7 phase 3].
func FeatureNameToDefName(featureName string) string {
	if idx := strings.IndexByte(featureName, '_'); idx >= 0 {
		return featureName[:idx]
	}
	return featureName
}

// ResurrectionJitter performs the resurrection order's sole simulation-stream
// draw [P0-15][I4].
//
// Retired (WU-19-143): this was documented as "the placement jitter draw...
// bounded by the feature's spread byte (feature catalog spread field)". Both
// halves were wrong. There is no authored "spread" feature key — retail's
// feature parser reads no such key at all [05 R-FEAT-01 §1], confirmed by a
// full census of every stock feature section, and the draw is not a
// placement jitter: it is the approach phase's vertical walk-target term,
// bounded by the feature's ordinary `height` byte, and it happens before
// this package's Resurrect (which implements only phase 5 "create")
// [05 "Resurrection", "Established — cost and randomness"][05 R-WORK-01
// §7 phase 1]. This helper models the draw's shape — a single bounded pull,
// skipped without advancing the stream when the bound is below two — for
// whichever call site ends up owning the approach phase; `bound` is the
// feature's height byte there, not a spread byte.
func ResurrectionJitter(sim *rng.Simulation, bound uint8) int {
	if sim == nil {
		return 0
	}
	return int(sim.Uint32n(uint32(bound))) // Uint32n already returns 0 without advancing when bound < 2 [I4].
}

// Resurrect performs resurrection allocation [05 R-WORK-01 §7 phase 5
// "create"]. Steps: allocate the new unit at the feature's position, remove
// the feature BEFORE the new unit is marked alive, set its remaining
// fraction to 0 and health to 1, no ledger cost, delay via ResurrectionDelay.
// Per-def limit -1 sentinel unlimited; pool fail returns the same 300-tick
// retry with "Unable to create any more units".
//
// Retired (WU-19-143): this used to look up a per-feature "jitter spread"
// byte here (including a fringe-anchor resolution walk to find it for a
// multi-cell footprint) and feed it to ResurrectionJitter before removing
// the feature. Both the byte and the call site were wrong: no such feature
// key exists in retail [05 R-FEAT-01 §1], and the order's one simulation
// draw belongs to phase 1's approach step, not phase 5's create step which
// this function implements — phase 5 draws no randomness at all
// [05 "Resurrection", "Established — cost and randomness"]. The `sim`
// parameter is kept for call-site stability (phase 1's approach draw is a
// separate, not-yet-wired concern) but this function no longer uses it.
func (s *Service) Resurrect(builder *units.Unit, featureCell *world.PlotCell, def *content.UnitDef, posX, posY, posZ numeric.Fixed, sim *rng.Simulation) (*units.Unit, error) {
	if s == nil || s.World == nil || builder == nil || def == nil {
		return nil, nil
	}
	if !CheckPerDefLimit(s.World, builder.Owner, def) {
		return nil, ErrLimit
	}
	// Feature removal runs BEFORE the new unit's alive word is written: the
	// resurrection state's fifth step calls the feature-removal helper first
	// [P0-15]. The helper clears the terrain plot's filler and anchor words.
	// The caller supplies a featureCell pointer into Terrain.Plot; we clear that
	// cell and, when the terrain dimensions are known, any fringe cells that
	// anchor to it.
	// TODO(T25): the multi-cell footprint sweep is not fully located beyond the
	// single anchor plus its fringe; the removal helper's own footprint handling
	// remains open. Decider: static trace of that helper's cell walk.
	if featureCell != nil {
		// Single-cell clear: feature → none, anchor → 0, filler cleared.
		featureCell.SetFeature(world.PlotFeatureNone)
		featureCell.SetAnchorWord(0)
		// If terrain is available, also clear fringe members that resolve to this anchor.
		if s.Terrain != nil && s.Terrain.Plot != nil && s.Terrain.CellW > 0 && s.Terrain.CellH > 0 {
			// Locate anchor index by pointer equality when featureCell is inside Terrain.Plot.
			anchorIdx := -1
			for i := range s.Terrain.Plot {
				if &s.Terrain.Plot[i] == featureCell {
					anchorIdx = i
					break
				}
			}
			if anchorIdx >= 0 {
				ax := int32(anchorIdx) % s.Terrain.CellW
				az := int32(anchorIdx) / s.Terrain.CellW
				for cz := int32(0); cz < s.Terrain.CellH; cz++ {
					for cx := int32(0); cx < s.Terrain.CellW; cx++ {
						idx := int(cz*s.Terrain.CellW + cx)
						if idx == anchorIdx {
							continue
						}
						cell := &s.Terrain.Plot[idx]
						if cell.Feature() != world.PlotFeatureFringe {
							continue
						}
						dx := int32(cell.AnchorDXSigned())
						dz := int32(cell.AnchorDZSigned())
						if cx+dx == ax && cz+dz == az {
							cell.SetFeature(world.PlotFeatureNone)
							cell.SetAnchorWord(0)
						}
					}
				}
			}
		}
	}
	if s.Allocator != nil {
		prod, err := s.Allocator(builder.Owner, def, posX, posY, posZ)
		if err != nil || prod == nil {
			return nil, ErrLimit
		}
		prod.Remaining = 0
		prod.Health = 1
		return prod, nil
	}
	h, err := s.World.Create(def, builder.Owner, posX, posY, posZ)
	if err != nil {
		return nil, ErrLimit
	}
	prod := s.World.Unit(h)
	if prod == nil {
		return nil, ErrLimit
	}
	prod.Remaining = 0 // finished [05 R-WORK-01 §7 "Established — the transplant"]
	prod.Health = 1    // one hit point, not max [05 R-WORK-01 §7 "Established — the transplant"]
	return prod, nil
}
