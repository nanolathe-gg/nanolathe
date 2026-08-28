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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// delay = trunc(buildTime*0.3 / floor(workTime/30)) — sole 0.3 use in binary, '_' truncation, 1 RNG jitter.
const resurrectionCoeff = 0.3 // TODO(question): Historical analysis omitted; independently worded behavior is needed.

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
//	delay = trunc(buildTime*0.3 / floor(workTime/30))
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// If floor(workTime/30)==0, FDIV by zero → inf → __ftol overflow 0x80000000 [P0-15]; we return large sentinel.
func ResurrectionDelay(buildTime int32, workerTime int32) int32 {
	worker := workerTime / 30 // floor, trunc toward zero for positive [I3]
	if worker == 0 {
		// FDIV by zero → inf → __ftol overflow 0x80000000 wedge [P0-15]
		return -2147483648 // 0x80000000
	}
	f := float64(buildTime) * resurrectionCoeff / float64(worker)
	return int32(math.Trunc(f)) // __ftol trunc toward zero [01 §8] I3
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Per-def limit -1 sentinel unlimited; pool fail returns same 300-tick retry with "Unable...".
func (s *Service) Resurrect(builder *units.Unit, featureCell *world.PlotCell, def *content.UnitDef, posX, posY, posZ numeric.Fixed, sim *rng.Simulation) (*units.Unit, error) {
	if s == nil || s.World == nil || builder == nil || def == nil {
		return nil, nil
	}
	if !CheckPerDefLimit(s.World, builder.Owner, def) {
		return nil, ErrLimit
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	var spread uint8
	if featureCell != nil && s.Terrain != nil && s.Terrain.FeatureDefs != nil {
		idx := featureCell.Feature()
		if idx < 0xFFFB && int(idx) < len(s.Terrain.FeatureDefs) {
			if fd := s.Terrain.FeatureDefs[idx]; fd != nil {
				spread = fd.ResurrectSpread
			}
		} else if idx == world.PlotFeatureFringe && s.Terrain.CellW > 0 && s.Terrain.CellH > 0 {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// we clear that cell and, when terrain dimensions known, any fringe cells that anchor to it.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	prod.Remaining = 0 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	prod.Health = 1    // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return prod, nil
}
