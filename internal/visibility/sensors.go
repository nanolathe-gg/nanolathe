// Package visibility sensors implements C11, C12 [PLAN_05 WU-05-4] [03 §3.4].
package visibility

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Status-field roles [03 §3.4]. The upper bit of the friendly mask doubles as
// the underwater-rejection exemption of [03 §3.2], which is why owned and
// allied units are implicitly exempt from the sea-level test.
const (
	SeenBit      uint32 = 0x100  // per-frame seen marker [03 §3.4]
	FriendlyMask uint32 = 0x300  // friendly contact [03 §3.4]
	DecloakBit   uint32 = 0x1000 // decloak timer running [03 §3.4]
)

// DecloakDeadlineAdd is the decloak deadline offset in ticks [03 §3.4].
const DecloakDeadlineAdd = 90 // 0x5A

// sensorSurfaceShift projects world coordinates onto the sensor backing
// surfaces: one surface cell per 128 world units [03 §3.4].
const sensorSurfaceShift = 23

// SensorUnit is one unit as the sensor phase sees it [03 §3.4].
//
// The phase mutates Status through the pointer — that is its entire
// authoritative output. Everything else it produces lands on the backing
// surfaces, which are presentation.
type SensorUnit struct {
	Owner  PlayerID
	Status *uint32       // runtime status field; the phase writes 0x100/0x300/0x1000
	X, Z   numeric.Fixed // 16.16 world position
	Y      numeric.Fixed
	Alive  bool
	Hidden bool // hidden/cloaked instance bit [03 §3.2]

	// Authored sensor distances [02 "Unit record"]. Zero means absent.
	RadarDistance    int32
	SonarDistance    int32
	RadarJam         int32
	SonarJam         int32
	MinCloakDistance int32

	// DecloakDeadline receives tick+90 when this unit is struck by a cloaked
	// unit's proximity search [03 §3.4].
	DecloakDeadline *uint32
}

// SensorSurfaces receives the rasterized circles [03 §3.4] C11.
//
// Radar, sonar and jammers NEVER author the word mask: they rasterize onto
// separate surfaces that are wiped each tick while the LOS mask persists. The
// three callbacks are three distinct tables in retail, kept distinct here.
//
// TODO(question): arbitration among the three tables — what overlapping marks
// write to the backing surfaces — is an open residual [03 §3.4]. A nil sink
// discards, which is what a headless session does.
type SensorSurfaces interface {
	Wipe()
	Sensor(u, v, radius int32) // combined radar/sonar circle
	RadarJam(u, v, radius int32)
	SonarJam(u, v, radius int32)
}

// SetSurfaces binds the sensor backing surfaces. Nil discards them.
func (s *Service) SetSurfaces(sf SensorSurfaces) {
	if s != nil {
		s.surfaces = sf
	}
}

// surfaceProject maps a world coordinate onto a sensor surface cell: one cell
// per 128 world units, a shift of 23 [03 §3.4].
func surfaceProject(v numeric.Fixed) int32 {
	return int32(int64(v) >> sensorSurfaceShift)
}

// SensorTick runs the per-tick sensor and proximity phase [03 §3.4] C11 C12.
//
// It runs ONLY when more than one player is present. The passes, in order:
//
//  1. ownership/status: clear the decloak-timer bit on every unit, then set
//     the friendly bits on own and alliance-qualified units and clear that bit
//     group otherwise;
//  2. sensor circles: each active unit with a radar or sonar distance emits
//     ONE circle whose outer radius is the LARGER of the two; nonzero jam
//     distances emit their own circles through two further tables;
//  3. minimum-cloak proximity: qualifying cloaked units search by squared
//     planar distance up to their authored minimum-cloak distance, writing
//     tick+90 and the decloak bit into each unit they strike;
//  4. final visibility: units neither already seen nor hidden are projected
//     with the standard half-height shear and tested through the mode-selected
//     source; admission sets the seen marker.
//
// The phase never writes the word mask.
func (s *Service) SensorTick(tick uint32, playerCount int, allied func(a, b PlayerID) bool, units []SensorUnit) {
	if s == nil || playerCount <= 1 {
		return // more than one player required [03 §3.4] C12
	}
	// 1. Ownership and status.
	for i := range units {
		u := &units[i]
		if u.Status == nil {
			continue
		}
		*u.Status &^= DecloakBit // cleared for every unit [03 §3.4]
		friendly := u.Owner == s.local
		if !friendly && allied != nil {
			friendly = allied(s.local, u.Owner)
		}
		if friendly {
			*u.Status |= FriendlyMask
		} else {
			*u.Status &^= FriendlyMask
		}
	}
	// 2. Sensor and jam circles onto the backing surfaces [C11].
	if s.surfaces != nil {
		s.surfaces.Wipe() // rebuilt each tick; the LOS mask persists [03 §3.4]
		for i := range units {
			u := &units[i]
			if !u.Alive {
				continue
			}
			cu, cv := surfaceProject(u.X), surfaceProject(u.Z)
			if u.RadarDistance != 0 || u.SonarDistance != 0 {
				outer := u.RadarDistance // ONE circle, the larger of the two [03 §3.4]
				if u.SonarDistance > outer {
					outer = u.SonarDistance
				}
				s.surfaces.Sensor(cu, cv, outer)
			}
			if u.RadarJam != 0 {
				s.surfaces.RadarJam(cu, cv, u.RadarJam)
			}
			if u.SonarJam != 0 {
				s.surfaces.SonarJam(cu, cv, u.SonarJam)
			}
		}
	}
	// 3. Minimum-cloak proximity [03 §3.2] C10 [03 §3.4] C12.
	for i := range units {
		src := &units[i]
		if !src.Alive || !src.Hidden || src.MinCloakDistance <= 0 {
			continue
		}
		r2 := int64(src.MinCloakDistance) * int64(src.MinCloakDistance)
		for j := range units {
			if i == j {
				continue
			}
			dst := &units[j]
			if !dst.Alive || dst.Status == nil {
				continue
			}
			dx := int64(pixel(dst.X)) - int64(pixel(src.X))
			dz := int64(pixel(dst.Z)) - int64(pixel(src.Z))
			if dx*dx+dz*dz > r2 { // squared planar distance [03 §3.2] C10
				continue
			}
			*dst.Status |= DecloakBit
			if dst.DecloakDeadline != nil {
				*dst.DecloakDeadline = tick + DecloakDeadlineAdd
			}
		}
	}
	// 4. Final visibility pass [03 §3.4] C12.
	for i := range units {
		u := &units[i]
		if u.Status == nil || !u.Alive {
			continue
		}
		if *u.Status&SeenBit != 0 || u.Hidden {
			continue // neither already seen nor hidden [03 §3.4]
		}
		if s.sample(s.local, u.X, u.Y, u.Z) {
			*u.Status |= SeenBit
		}
	}
}

// ClearSeen drops the per-frame seen markers. The marker is per frame, so the
// caller clears it at the start of a pass rather than the sensor phase clearing
// it mid-walk [03 §3.4].
func ClearSeen(status *uint32) {
	if status != nil {
		*status &^= SeenBit
	}
}
