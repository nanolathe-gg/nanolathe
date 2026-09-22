package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const (
	communityOffMapWalkLimit     = 4096
	communityOffMapMaxAgeTicks   = 450
	communityProjectileHighWater = 270
)

func communityTilesOutsideMap(x0, z0, x1, z1, mapW, mapH int32) int32 {
	if mapW <= 0 || mapH <= 0 {
		return int32(^uint32(0) >> 1)
	}
	dx := int32(0)
	if x1 < 0 {
		dx = -x1
	} else if x0 >= mapW {
		dx = x0 - (mapW - 1)
	}
	dz := int32(0)
	if z1 < 0 {
		dz = -z1
	} else if z0 >= mapH {
		dz = z0 - (mapH - 1)
	}
	if dz > dx {
		return dz
	}
	return dx
}

func communityUnitWithinMargin(u *units.Unit, terrain *world.Terrain, margin int32) bool {
	if u == nil || terrain == nil || margin <= 0 {
		return false
	}
	x0, z0 := int32(u.CachedOccupancyX), int32(u.CachedOccupancyZ)
	fx, fz := int32(u.FootprintSizeX), int32(u.FootprintSizeZ)
	if fx <= 0 || fz <= 0 {
		return false
	}
	return communityTilesOutsideMap(x0, z0, x0+fx-1, z0+fz-1, terrain.CellW, terrain.CellH) <= margin
}

func communityProjectileWithinMargin(p *Projectile, terrain *world.Terrain, margin int32) bool {
	if p == nil || terrain == nil || margin <= 0 {
		return false
	}
	x, z := world.WorldToCell(p.Pos.X), world.WorldToCell(p.Pos.Z)
	return communityTilesOutsideMap(x, z, x, z, terrain.CellW, terrain.CellH) <= margin
}

// communityScanOffMapAircraft walks movement's canonical bucket order. It
// reports whether any reachable hostile aircraft exists independently of
// whether the projectile overlaps one; that distinction decides whether an
// off-map round remains alive [CP-ENV-1].
func (s *Service) communityScanOffMapAircraft(p *Projectile, w *units.World, terrain *world.Terrain, margin int32) (any bool, victim pool.Handle) {
	if s == nil || p == nil || w == nil || terrain == nil || s.VisitOffMapFiled == nil {
		return false, 0
	}
	if margin <= 0 {
		return false, 0
	}
	projectileX, projectileZ := world.WorldToCell(p.Pos.X), world.WorldToCell(p.Pos.Z)
	projectileY := int32(p.Pos.Y.Raw())
	scanned := 0
	s.VisitOffMapFiled(func(h pool.Handle, _ uint64) bool {
		if scanned >= communityOffMapWalkLimit {
			return false
		}
		scanned++
		u := w.Unit(h)
		if u == nil || u.Def == nil || !u.Alive || u.Dying || u.Move.ModeMirror != 2 || u.Owner == p.ShooterSide || !communityUnitWithinMargin(u, terrain, margin) {
			return true
		}
		any = true
		x0, z0 := int32(u.CachedOccupancyX), int32(u.CachedOccupancyZ)
		fx, fz := int32(u.FootprintSizeX), int32(u.FootprintSizeZ)
		if projectileX < x0 || projectileX >= x0+fx || projectileZ < z0 || projectileZ >= z0+fz {
			return true
		}
		boundsMin, boundsMax := u.Def.BoundingExtents()
		lo := int32(u.Y.Raw()) + boundsMin[1]
		hi := int32(u.Y.Raw()) + boundsMax[1]
		if projectileY < lo || projectileY > hi {
			return true
		}
		victim = h
		return false
	})
	return any, victim
}

func (s *Service) communityOffMapProjectile(p *Projectile, w *units.World, terrain *world.Terrain, tick uint32) (victim pool.Handle, keep bool) {
	if s == nil || p == nil {
		return 0, false
	}
	margin := s.rules().OffMapAircraftMargin(s)
	// The source reads the pool's active-span count. Dead records retain their
	// slots until the phase-tail compactor, so Service.Count is the same
	// collision-time answer and avoids a scan for each off-map projectile.
	if margin <= 0 || !communityProjectileWithinMargin(p, terrain, margin) || s.Count() >= communityProjectileHighWater || int32(tick-p.CreationTick) > communityOffMapMaxAgeTicks {
		return 0, false
	}
	any, victim := s.communityScanOffMapAircraft(p, w, terrain, margin)
	return victim, any
}

func (s *Service) communityOnMapOffMapVictim(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain) pool.Handle {
	if s == nil || weapon == nil || weapon.NoExplode {
		return 0
	}
	margin := s.rules().OffMapAircraftMargin(s)
	if margin <= 0 {
		return 0
	}
	_, victim := s.communityScanOffMapAircraft(p, w, terrain, margin)
	return victim
}

func communityOffMapFalloff(distance, radius int32, edge float32) float32 {
	if distance == 0 {
		return 1
	}
	t := float32(distance)/float32(radius) - 1
	// The extension source stores binary32 throughout and associates the two
	// multiplies from the left: ((1-edge)*t)*t.
	scaled := float32((float32(1) - edge) * t)
	scaled = float32(scaled * t)
	return float32(scaled + edge)
}

// communityOffMapSplash is the first recipient pass of every area blast. It
// deliberately has no air filter; canonical off-map filing and the margin
// make its victims disjoint from the later tile walk [CP-ENV-1].
func (s *Service) communityOffMapSplash(feedback *impactFeedback, w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, shooterSide uint8, tick uint32, observedVelocity Vec3, radius int32) {
	if s == nil || feedback == nil || w == nil || terrain == nil || weapon == nil || radius <= 0 || s.VisitOffMapFiled == nil {
		return
	}
	margin := s.rules().OffMapAircraftMargin(s)
	if margin <= 0 {
		return
	}
	scanned := 0
	s.VisitOffMapFiled(func(h pool.Handle, _ uint64) bool {
		if scanned >= communityOffMapWalkLimit {
			return false
		}
		scanned++
		if shooter != 0 && h == shooter {
			return true
		}
		u := w.Unit(h)
		if u == nil || u.Def == nil || !u.Alive || u.Dying || !communityUnitWithinMargin(u, terrain, margin) {
			return true
		}
		distance := communityOffMapDistance(impact, u)
		if distance >= radius {
			return true
		}
		falloff := communityOffMapFalloff(distance, radius, float32(weapon.EdgeEffectiveness))
		p := &Projectile{Pos: impact, Shooter: shooter, ShooterSide: shooterSide, Velocity: observedVelocity}
		feedback.add(applyDamageToUnit(s, u, p, weapon, falloff, distance, w, tick), shooterSide == u.Owner)
		return true
	})
}
