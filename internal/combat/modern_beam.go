package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These are Modern confidence limits, not retail constants. A precise beam
// may reserve its predicted direct damage for two seconds; it is not hitscan.
const modernBeamHorizon = 60
const modernBeamMaxFootprint = 8

type modernBeamMotion struct {
	velocity Vec3
	speed    numeric.Fixed
	heading  uint16
	mode     uint8
}

func beamMotion(target *units.Unit) modernBeamMotion {
	return modernBeamMotion{Vec3{target.Move.VelX, target.Move.VelY, target.Move.VelZ}, target.Move.Speed, target.Move.Heading, target.Move.Mode}
}

func modernBeamWeapon(weapon *content.WeaponDef) bool {
	return modernReliableWeapon(weapon) && weapon.BeamWeapon && weapon.Accuracy == 0 && weapon.SprayAngle == 0 &&
		weapon.WeaponAcceleration == 0 && weapon.StartVelocity == 0 && weapon.WeaponVelocity >= 16<<16
}

func modernBeamTarget(target *units.Unit) bool {
	if target == nil || target.Def == nil || !target.Alive || target.Dying || target.Attachment.Carrier != 0 ||
		!terrainPointValid(Vec3{target.X, target.Y, target.Z}) {
		return false
	}
	d := target.Def
	if d.BMCode == 0 {
		return target.Move.Speed == 0
	}
	return d.BMCode == 1 && target.Move.Mode == 1 && !d.CanFly && !d.CanHover && !d.Floater &&
		d.FootprintX > 0 && d.FootprintZ > 0 && d.FootprintX <= modernBeamMaxFootprint && d.FootprintZ <= modernBeamMaxFootprint &&
		target.Move.Speed >= 0 && target.Move.VelY == 0 && terrainPointValid(beamMotion(target).velocity)
}

// Quantization follows the ordinary footprint stamp [04 R-COLL-01 §1]. A
// restored/custom stamp that disagrees is rejected rather than overwritten.
func beamAnchor(x, z numeric.Fixed, fx, fz int32) (int32, int32) {
	return int32(numeric.FloorDiv(x.Raw()+(int64(8-fx*8)<<16), 16<<16)),
		int32(numeric.FloorDiv(z.Raw()+(int64(8-fz*8)<<16), 16<<16))
}

// beamFootprint inspects a predicted ground rectangle. Existing obstacles make
// constant motion uncertain; the lower terrain bound keeps the vertical test
// conservative without reproducing the mover's four-corner conform algorithm.
func beamFootprint(target *units.Unit, terrain *world.Terrain, x, z numeric.Fixed, initial bool) (ax, az int32, upper int32, ok bool) {
	fx, fz := int32(target.Def.FootprintX), int32(target.Def.FootprintZ)
	ax, az = beamAnchor(x, z, fx, fz)
	minHeight := int32(255)
	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			cell := terrain.PlotAt(ax+dx, az+dz)
			if cell == nil || !cell.IsEmpty() || (cell.OccupantA() != 0 && cell.OccupantA() != int16(target.Handle)) ||
				(initial && cell.OccupantA() != int16(target.Handle)) || cell.MinHeight() < terrain.SeaLevel {
				return 0, 0, 0, false
			}
			if h := int32(cell.MinHeight()); h < minHeight {
				minHeight = h
			}
		}
	}
	base := minHeight << 16
	if int32(target.Y.Raw()) < base {
		base = int32(target.Y.Raw())
	}
	top := target.Def.ModelTopFixed
	if top < 0 {
		top = 0
	}
	if int64(base)+int64(top) > int64(1<<31-1) {
		return 0, 0, 0, false
	}
	return ax, az, base + top, true
}

func (s *Service) beamETA(p Projectile, weapon *content.WeaponDef, target *units.Unit, w *units.World, terrain *world.Terrain, tick uint32) int {
	if w == nil || terrain == nil || !terrainPointValid(p.Pos) || !terrainPointValid(p.Velocity) || p.BurstRemaining != 0 {
		return 0
	}
	if tick < s.modernNextProjectileTick {
		tick = s.modernNextProjectileTick
	}
	if tick < p.CreationTick {
		return 0
	}
	mobile := target.Def.BMCode != 0
	x, z := target.X, target.Z
	fx, fz := int32(target.Def.FootprintX), int32(target.Def.FootprintZ)
	if mobile {
		if _, _, _, ok := beamFootprint(target, terrain, x, z, true); !ok {
			return 0
		}
	}
	for step := 0; step < modernBeamHorizon; step++ {
		now := tick + uint32(step)
		if now < tick || AdvanceDirect(&p, weapon, now) != AdvanceAlive || !terrainPointValid(p.Pos) {
			return 0
		}
		var contact func(*Projectile, *units.World, *world.Terrain, int32, int32) pool.Handle
		if mobile {
			ax, az, upper, ok := beamFootprint(target, terrain, x, z, false)
			if !ok {
				return 0
			}
			x += target.Move.VelX
			z += target.Move.VelZ
			if !terrainPointValid(Vec3{X: x, Z: z}) {
				return 0
			}
			bx, bz, upperNext, ok := beamFootprint(target, terrain, x, z, false)
			if !ok {
				return 0
			}
			if upperNext < upper {
				upper = upperNext
			}
			// Unit windows precede projectiles, but the target's window may
			// precede or follow this shooter. Require contact for BOTH possible
			// initial offsets, instead of inventing a movement-phase ordering.
			contact = func(projectile *Projectile, unitsWorld *units.World, ground *world.Terrain, cx, cz int32) pool.Handle {
				cell := ground.PlotAt(cx, cz)
				if cell == nil {
					return 0
				}
				py := int32(projectile.Pos.Y.Raw())
				if u := contactCandidate(unitsWorld, projectile, cell.OccupantA()); u != nil && u != target {
					low, high := contactBand(u)
					if CollisionSlotYGate(py, low, high, 0) {
						return u.Handle
					}
				}
				if cx >= ax && cx < ax+fx && cz >= az && cz < az+fz &&
					cx >= bx && cx < bx+fx && cz >= bz && cz < bz+fz &&
					projectile.ShooterSide != target.Owner && CollisionSlotYGate(py, 0, upper, 0) {
					return target.Handle
				}
				if u := contactCandidate(unitsWorld, projectile, cell.OccupantB()); u != nil && u != target {
					low, high := contactBand(u)
					if CollisionSlotYGate(py, low, high, 1) {
						return u.Handle
					}
				}
				return 0
			}
		}
		hit, feature, water, off, ground, bounce := checkCollisionWithContact(&p, weapon, w, terrain, s.Features, s.OpaqueLiquidMode, contact)
		if hit != 0 {
			if hit == target.Handle {
				return step + 1
			}
			return 0
		}
		if feature != nil || water || off || ground || bounce {
			return 0
		}
	}
	return 0
}
