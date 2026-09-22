package construction

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// StructureGeometry is the immutable, per-session projection of one building
// definition at one facing. Yard is derived storage; UnitDef is never changed
// [community patch engine behavior, CP-CON-5].
type StructureGeometry struct {
	Facing                 units.StructureFacing
	FootprintX, FootprintZ int32
	Yard                   []world.YardCell
}

type rotationCacheKey struct {
	def    *content.UnitDef
	facing units.StructureFacing
}

// AllowedFacings returns the selected gameplay answer for a definition.
func (s *Service) AllowedFacings(def *content.UnitDef) content.FacingMask {
	return s.rules().AllowedFacings(s, def)
}

// StructureGeometry returns selected oriented geometry. A disallowed or
// malformed request becomes south, matching the order-issue clamp. CP-CON-5
// refuses rotation when either authored extent is outside 1..32.
func (s *Service) StructureGeometry(def *content.UnitDef, requested units.StructureFacing) (StructureGeometry, error) {
	if def == nil {
		return StructureGeometry{}, fmt.Errorf("construction: nil structure definition")
	}
	facing := s.ResolveStructureFacing(def, requested)
	return s.structureGeometryApplied(def, facing)
}

// ResolveStructureFacing clamps a request without allocating geometry. Creation
// paths preserve their original validation when rotation resolves to south.
func (s *Service) ResolveStructureFacing(def *content.UnitDef, requested units.StructureFacing) units.StructureFacing {
	if def == nil {
		return units.FacingSouth
	}
	facing := requested & 3
	allowed := s.AllowedFacings(def)
	if def.BMCode != 0 || def.FootprintX <= 0 || def.FootprintZ <= 0 || def.FootprintX > 32 || def.FootprintZ > 32 || allowed&units.FacingMask(facing) == 0 {
		facing = units.FacingSouth
	}
	return facing
}

// StructureGeometryFromHeading recovers the persistent facing then applies
// the selected rule. Save, give and resurrection call this before allocation.
func (s *Service) StructureGeometryFromHeading(def *content.UnitDef, heading uint16) (StructureGeometry, error) {
	return s.StructureGeometry(def, units.FacingFromHeading(heading))
}

// StructureGeometryForUnit returns creation-derived geometry for a live unit.
// Rebinding does not reinterpret existing units; new creation uses the newly
// selected answer before it reaches this method.
func (s *Service) StructureGeometryForUnit(u *units.Unit) (StructureGeometry, error) {
	if u == nil {
		return StructureGeometry{}, fmt.Errorf("construction: nil structure unit")
	}
	return s.structureGeometryApplied(u.Def, u.StructureFacing)
}

func (s *Service) structureGeometryApplied(def *content.UnitDef, facing units.StructureFacing) (StructureGeometry, error) {
	if def == nil {
		return StructureGeometry{}, fmt.Errorf("construction: nil structure definition")
	}
	facing &= 3
	key := rotationCacheKey{def: def, facing: facing}
	if s != nil && s.rotationCache != nil {
		if geometry, ok := s.rotationCache[key]; ok {
			return geometry, nil
		}
	}
	footX, footZ := units.OrientedFootprint(def, facing)
	geometry := StructureGeometry{Facing: facing, FootprintX: footX, FootprintZ: footZ}
	if def.BMCode == 0 {
		yard, err := units.OrientedYardMap(def, facing)
		if err != nil {
			return StructureGeometry{}, fmt.Errorf("construction: oriented yard: %w", err)
		}
		geometry.Yard = yard
	}
	if s != nil {
		if s.rotationCache == nil {
			s.rotationCache = make(map[rotationCacheKey]StructureGeometry)
		}
		s.rotationCache[key] = geometry
	}
	return geometry, nil
}
