package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// PlacementSnapSample is a read-only command-time projection for CP-CON-6.
// The host owns the search and tie-break; mutable terrain stays at this boundary.
type PlacementSnapSample struct {
	Valid             bool
	MetalAboveSurface int
	UnitOccupied      bool
	Reclaimable       bool
	FeatureDef        *content.FeatureDef // Immutable authored definition.
	WX, WY, WZ        numeric.Fixed
}

func (s *Session) PlacementSnapSample(cx, cz, footX, footZ int32) PlacementSnapSample {
	var out PlacementSnapSample
	if s == nil || s.World == nil {
		return out
	}
	cell := s.World.PlotAt(cx, cz)
	if cell == nil {
		return out
	}
	out.Valid = true
	out.UnitOccupied = cell.OccupantA() != 0
	if threshold, err := battleSurfaceMetal(s); err == nil && footX > 0 && footZ > 0 {
		for dx := -footX / 2; dx <= (footX-1)/2; dx++ {
			for dz := -footZ / 2; dz <= (footZ-1)/2; dz++ {
				tile := s.World.PlotAt(cx+dx, cz+dz)
				if tile == nil || s.snapCellHeldByStructure(tile) || int32(tile.Metal()) <= threshold {
					continue
				}
				out.MetalAboveSurface++
			}
		}
	}
	def, rootX, rootZ, ok := features.FeatureAt(s.World, numeric.Fixed(cx)<<20, numeric.Fixed(cz)<<20)
	if !ok {
		return out
	}
	out.FeatureDef = def
	out.Reclaimable = def.Reclaimable && (def.Metal > 0 || def.Energy > 0)
	out.WX = numeric.Fixed(int32(rootX)*16+def.FootprintX*8) << 16
	out.WZ = numeric.Fixed(int32(rootZ)*16+def.FootprintZ*8) << 16
	mean := (int32(cell.MinHeight()) + int32(cell.MaxHeight())) / 2
	out.WY = numeric.Fixed(max(mean, int32(s.World.SeaLevel))) << 16
	return out
}

func (s *Session) snapCellHeldByStructure(cell *world.PlotCell) bool {
	if s == nil || s.Units == nil || cell == nil {
		return false
	}
	occupant := cell.OccupantA()
	if occupant == 0 {
		return false
	}
	u := s.Units.Unit(pool.Handle(uint16(occupant)))
	return u != nil && u.Def != nil && u.Def.BMCode == 0
}
