package combat

import "github.com/nanolathe-gg/nanolathe/internal/content"

func (StrictRules) InterceptorCoverage(_ *Service, aim, origin Vec3, coverage int32) bool {
	return WithinInterceptorCoverage(aim, origin, coverage)
}

// Community keeps the pool traversal and claim tests, replacing only the
// square with the inclusive circle (community-patch-engine, CP-FIX-5).
func (CommunityRules) InterceptorCoverage(s *Service, aim, origin Vec3, coverage int32) bool {
	if s == nil || !s.Community.AntinukeCircularCoverage {
		return StrictRules{}.InterceptorCoverage(s, aim, origin, coverage)
	}
	radius := int64(uint32(coverage)) << 16
	dx := int64(int32(origin.X)) - int64(int32(aim.X))
	dz := int64(int32(origin.Z)) - int64(int32(aim.Z))
	if dx < -radius || dx > radius || dz < -radius || dz > radius {
		return false
	}
	return dx*dx+dz*dz <= radius*radius
}

func (StrictRules) InterceptorRingRadius(_ *Service, coverage int32) int32 {
	return coverage - 512 // [03 §3.9]
}

func (CommunityRules) InterceptorRingRadius(s *Service, coverage int32) int32 {
	if s != nil && s.Community.AntinukeCircularCoverage {
		return coverage
	}
	return StrictRules{}.InterceptorRingRadius(s, coverage)
}

// InterceptorRingRadius publishes the chosen radius, keeping the renderer
// independent of gameplay selection (DESIGN_COMMUNITY_PATCH §4.2, CP-FIX-5).
func (s *Service) InterceptorRingRadius(w *content.WeaponDef) int32 {
	if w == nil {
		return 0
	}
	return s.rules().InterceptorRingRadius(s, w.Coverage)
}
