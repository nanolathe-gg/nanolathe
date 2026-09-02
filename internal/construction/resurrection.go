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
// truncation of the corpse name and one placement-jitter RNG draw.
const resurrectionCoeff = 0.3 // [05 R-WORK-01 §7 "Established — the delay"]

// ResurrectionDelay computes delay ticks [05 R-WORK-01 §7 "Established — the
// delay"] (resurrection's wait phase).
//
//	delay = trunc(buildTime*0.3 / floor(workTime/30))
//
// trunc toward zero [I3].
//
// TODO(question): retail's sub-thirty workertime edge computes delay=0 (the
// floating divide by zero produces +Inf, whose out-of-range integer
// conversion yields a zero low word — the only word the caller consumes)
// [05 R-WORK-01 §7 "Established — the sub-thirty workertime edge"]. This
// implementation instead returns the int32 minimum as a sentinel; needs
// reconciling with that established fact.
func ResurrectionDelay(buildTime int32, workerTime int32) int32 {
	worker := workerTime / 30 // floor, trunc toward zero for positive [I3]
	if worker == 0 {
		// Sentinel pending reconciliation with the established zero-delay
		// edge case — see the TODO(question) above.
		return -2147483648 // int32 minimum
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

// ResurrectionJitter performs the single resurrection placement jitter draw
// [P0-15]: one simulation-stream draw bounded by the feature's spread byte
// (feature catalog spread field) [I4] DET-01.
func ResurrectionJitter(sim *rng.Simulation, spreadByte uint8) int {
	if sim == nil {
		return 0
	}
	if spreadByte == 0 {
		return 0
	}
	return int(sim.Uint32n(uint32(spreadByte))) // 0..spread-1
}

// Resurrect performs resurrection allocation [05 R-WORK-01 §7 phase 5
// "create"]. Steps: allocate the new unit at the feature's position, remove
// the feature BEFORE the new unit is marked alive, set its remaining
// fraction to 0 and health to 1, no ledger cost, delay via ResurrectionDelay.
// Per-def limit -1 sentinel unlimited; pool fail returns the same 300-tick
// retry with "Unable to create any more units".
func (s *Service) Resurrect(builder *units.Unit, featureCell *world.PlotCell, def *content.UnitDef, posX, posY, posZ numeric.Fixed, sim *rng.Simulation) (*units.Unit, error) {
	if s == nil || s.World == nil || builder == nil || def == nil {
		return nil, nil
	}
	if !CheckPerDefLimit(s.World, builder.Owner, def) {
		return nil, ErrLimit
	}
	// Capture the jitter spread from the feature catalog BEFORE the feature
	// removal below clears the plot cell [05 R-WORK-01 §7 phase 1].
	var spread uint8
	if featureCell != nil && s.Terrain != nil && s.Terrain.FeatureDefs != nil {
		idx := featureCell.Feature()
		if idx < 0xFFFB && int(idx) < len(s.Terrain.FeatureDefs) {
			if fd := s.Terrain.FeatureDefs[idx]; fd != nil {
				spread = fd.ResurrectSpread
			}
		} else if idx == world.PlotFeatureFringe && s.Terrain.CellW > 0 && s.Terrain.CellH > 0 {
			// TODO(T25): the fringe-anchored jitter spread is not fully located.
			// It needs the anchor resolution the plot cell's anchor word pair
			// carries [03 §2.2]; this build walks the plot for the anchor
			// instead. Decider: static trace of the resurrect spawn's anchor
			// read.
			anchorIdx := -1
			for i := range s.Terrain.Plot {
				if &s.Terrain.Plot[i] == featureCell {
					anchorIdx = i
					break
				}
			}
			if anchorIdx >= 0 {
				cx := int32(anchorIdx) % s.Terrain.CellW
				cz := int32(anchorIdx) / s.Terrain.CellW
				dx := int32(featureCell.AnchorDXSigned())
				dz := int32(featureCell.AnchorDZSigned())
				ax := cx + dx
				az := cz + dz
				if ax >= 0 && ax < s.Terrain.CellW && az >= 0 && az < s.Terrain.CellH {
					aIdx := int(az*s.Terrain.CellW + ax)
					if aIdx >= 0 && aIdx < len(s.Terrain.Plot) {
						aFeat := s.Terrain.Plot[aIdx].Feature()
						if aFeat < 0xFFFB && int(aFeat) < len(s.Terrain.FeatureDefs) {
							if fd := s.Terrain.FeatureDefs[aFeat]; fd != nil {
								spread = fd.ResurrectSpread
							}
						}
					}
				}
			}
		}
	}
	_ = ResurrectionJitter(sim, spread)
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
