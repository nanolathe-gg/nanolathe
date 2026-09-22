package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func (StrictRules) CorpseVelocity(_ *Service, _ Vec3, velocity Vec3, _ *world.Terrain) Vec3 {
	return velocity
}

// CorpseVelocity seeds the existing gravity integrator only for a motionless
// corpse strictly above land. This tests the placed feature, regardless of the
// dead unit's type (community-patch-engine, CP-ENV-2).
func (CommunityRules) CorpseVelocity(s *Service, position, velocity Vec3, terrain *world.Terrain) Vec3 {
	if s == nil || !s.Community.AirCorpseFall || terrain == nil || velocity != (Vec3{}) {
		return velocity
	}
	height := int32(terrain.HeightAt(position.X, position.Z) >> 16)
	if height < 0 || height <= int32(terrain.SeaLevel) || int32(position.Y) <= height<<16 {
		return velocity
	}
	velocity.Y = -1
	return velocity
}

// SeedCorpseVelocity runs immediately after placement, while the new 3D
// feature still belongs to its active list [05 R-FEAT-01 §13].
func (s *Service) SeedCorpseVelocity(corpse *features.Instance, terrain *world.Terrain) {
	if corpse == nil {
		return
	}
	velocity := s.rules().CorpseVelocity(s, Vec3{X: corpse.X, Y: corpse.Y, Z: corpse.Z}, Vec3{Y: corpse.Vy}, terrain)
	corpse.Vy = velocity.Y
	corpse.IsSinking = corpse.Vy != 0
}
