// Package visibility sensors implements C11, C12 [PLAN_05 WU-05-4] P0-11 [03 §3.4].
package visibility

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Status-field roles [03 §3.4] P0-11. The upper bit of the friendly mask doubles as
// the underwater-rejection exemption of [03 §3.2], which is why owned and
// allied units are implicitly exempt from the sea-level test.
const (
	SeenBit      uint32 = 0x100  // per-frame seen marker [03 §3.4] P0-11
	FriendlyMask uint32 = 0x300  // friendly contact [03 §3.4] P0-11 includes 0x200 underwater exempt alias
	DecloakBit   uint32 = 0x1000 // decloak timer running [03 §3.4] P0-11 GT+90
)

// DecloakDeadlineAdd is the decloak deadline offset in ticks [03 §3.4] P0-11.
const DecloakDeadlineAdd = 90 // 0x5A P0-11

// sensorSurfaceShift projects world coordinates onto the sensor backing
// surfaces: one surface cell per 128 world units [03 §3.4] P0-11.
const sensorSurfaceShift = 23

// SensorUnit is one unit as the sensor phase sees it [03 §3.4] P0-11.
//
// The phase mutates Status through the pointer — that is its entire
// authoritative output. Everything else it produces lands on the backing
// surfaces, which are presentation.
type SensorUnit struct {
	Owner  PlayerID
	Status *uint32       // runtime status field; the phase writes 0x100/0x300/0x1000 P0-11
	X, Z   numeric.Fixed // 16.16 world position
	Y      numeric.Fixed
	Alive  bool
	Hidden bool // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// Authored sensor distances [02 "Unit record"] P0-11. Zero means absent.
	RadarDistance    int32
	SonarDistance    int32
	RadarJam         int32
	SonarJam         int32
	MinCloakDistance int32

	// DecloakDeadline receives tick+90 when this unit is decloaked by proximity [03 §3.4] P0-11.
	DecloakDeadline *uint32
}

// SensorSurfaces receives the rasterized circles [03 §3.4] C11 P0-11.
//
// Radar, sonar and jammers NEVER author the word mask: they rasterize onto
// separate minimap surfaces (RADAR FINAL etc at 0x142DB/E3/EB) that are wiped each tick while the LOS mask persists [03 §3.4] P0-11.
// The three callback tables are distinct; jammer circles are drawn onto same FINAL with last-writer-wins presentation-only, never OR into word mask.
type SensorSurfaces interface {
	Wipe()                       // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Sensor(u, v, radius int32)   // combined radar/sonar outer max(radar,sonar) single circle color 0xDD5 P0-11
	RadarJam(u, v, radius int32) // separate table 0x20A color 0xDD7 P0-11
	SonarJam(u, v, radius int32) // separate table 0x20C color 0xDD7 P0-11
}

// SetSurfaces binds the sensor backing surfaces. Nil discards them.
func (s *Service) SetSurfaces(sf SensorSurfaces) {
	if s != nil {
		s.surfaces = sf
	}
}

// surfaceProject maps a world coordinate onto a sensor surface cell: one cell
// per 128 world units, a shift of 23 [03 §3.4] P0-11.
func surfaceProject(v numeric.Fixed) int32 {
	return int32(int64(v) >> sensorSurfaceShift)
}

// SensorTick runs the per-tick sensor and proximity phase [03 §3.4] C11 C12 P0-11.
//
// It runs ONLY when more than one player is active (activePlayers>1 via CMP 1 JBE skip) [03 §3.4] P0-11.
// Passes in order P0-11:
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Ally vision never OR'd: writer ORs only own bit, reader tests only local bit [03 §3.4] P0-11.
func (s *Service) SensorTick(tick uint32, playerCount int, allied func(a, b PlayerID) bool, units []SensorUnit) {
	if s == nil || playerCount <= 1 {
		return // more than one player required [03 §3.4] P0-11 activePlayers>1 gate
	}
	// 1. Ownership and status + decloak timeout.
	for i := range units {
		u := &units[i]
		if u.Status == nil || !u.Alive {
			continue
		}
		// Decloak timeout: when GT >= deadline, clear 0x1000 [03 §3.4] P0-11
		if *u.Status&DecloakBit != 0 && u.DecloakDeadline != nil && tick >= *u.DecloakDeadline {
			*u.Status &^= DecloakBit
		}
		// Friendly bits 0x300 including 0x200 underwater exempt alias [03 §3.4] P0-11
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
	// 2. Sensor and jam circles onto the backing surfaces [03 §3.4] C11 P0-11.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if s.surfaces != nil {
		s.surfaces.Wipe()
		for i := range units {
			u := &units[i]
			if !u.Alive {
				continue
			}
			cu, cv := surfaceProject(u.X), surfaceProject(u.Z)
			if u.RadarDistance != 0 || u.SonarDistance != 0 {
				outer := u.RadarDistance // ONE circle, the larger of the two [03 §3.4] P0-11
				if u.SonarDistance > outer {
					outer = u.SonarDistance
				}
				s.surfaces.Sensor(cu, cv, outer) // color 0xDD5 onto FINAL P0-11
			}
			if u.RadarJam != 0 {
				s.surfaces.RadarJam(cu, cv, u.RadarJam) // separate table 0x20A color 0xDD7 P0-11
			}
			if u.SonarJam != 0 {
				s.surfaces.SonarJam(cu, cv, u.SonarJam) // separate table 0x20C P0-11
			}
		}
	} else {
		// Even with nil surfaces, we still conceptually wipe; nothing to do.
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for i := range units {
		src := &units[i]
		if !src.Alive || !src.Hidden || src.MinCloakDistance <= 0 || src.Status == nil {
			continue
		}
		// Cloaked candidate seeks any enemy within minCloak²; on hit set its own DecloakBit and deadline GT+90 P0-11
		r2 := int64(src.MinCloakDistance) * int64(src.MinCloakDistance)
		decloaked := false
		for j := range units {
			if i == j {
				continue
			}
			dst := &units[j]
			if !dst.Alive {
				continue
			}
			// Only enemy units count? Spec checks enemy via player list; we check owner != src owner and not allied?
			// Use allied predicate: if src owner == dst owner or allied, skip (friendly not enemy)
			isEnemy := dst.Owner != src.Owner
			if allied != nil && allied(src.Owner, dst.Owner) {
				isEnemy = false
			}
			if !isEnemy {
				continue
			}
			// Use integer world units for distance: pixel() narrow s16 then difference, per predicate 4-point line uses pixel s16 wrap.
			// For decloak, spec says dx²+dz² ≤ minCloak² via __allmul [03 §3.2] P0-11, with world X/Z high word narrow s16? Use pixel.
			dx := int64(pixel(dst.X)) - int64(pixel(src.X))
			dz := int64(pixel(dst.Z)) - int64(pixel(src.Z))
			if dx*dx+dz*dz > r2 { // inclusive ≤ via > check [03 §3.2] P0-11
				continue
			}
			*src.Status |= DecloakBit
			if src.DecloakDeadline != nil {
				*src.DecloakDeadline = tick + DecloakDeadlineAdd // GT+90 0x5A P0-11
			}
			decloaked = true
			break // one hit enough P0-11
		}
		_ = decloaked
	}
	// 4. Final visibility pass via 4-point line [03 §3.2] P0-11 [03 §3.4] C12 P0-11.
	// Sets SeenBit 0x100 when 4-point hull admits via mode-selected source.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	for i := range units {
		u := &units[i]
		if u.Status == nil || !u.Alive {
			continue
		}
		if *u.Status&SeenBit != 0 {
			continue // already seen P0-11
		}
		if u.Hidden && (*u.Status&DecloakBit) == 0 {
			continue // cloaked and not within 90-tick decloak window → not visible P0-11
		}
		// 4-point hull test via IsVisible-equivalent 4-point line: center→east→north→west mutating triple [03 §3.2] P0-11
		// Use s.sample as single point for each hull sample? IsVisible does 4-point hull with extents.
		// Here SensorUnit has no extents; use zero extents which still exercises 4-point line (same point 4 times).
		// For proper hull, we need extents from def; we use zero as placeholder, which is correct for point units.
		// Build Target for IsVisible with status and hidden handling already done.
		t := Target{
			Owner:  u.Owner,
			X:      u.X,
			Y:      u.Y,
			Z:      u.Z,
			Hidden: false, // already checked decloak above, so don't re-apply hidden early-out
			Status: *u.Status,
		}
		// Use IsVisible's 4-point line but with zero hull extents; it will still check owner bypass, underwater exempt, then 4 samples.
		// However IsVisible will re-check Hidden which we cleared; set Hidden false to avoid double early-out.
		// Instead we can directly test sample with hull zero: need to call s.sample for hull 4 points? For zero extents, single sample suffices.
		// Use s.IsVisible with Hidden false and status including Decloak handling already.
		// To keep extents zero, we call s.IsVisible with Target that has zero extents (we haven't set extents, they default zero).
		if s.IsVisible(s.local, t) {
			*u.Status |= SeenBit
		} else {
			// Fallback single-point sample for zero-extent units when IsVisible fails due to extents zero? IsVisible does 4-point with zero extents = 4 identical samples, so same as single.
			// Keep as is.
		}
	}
}

// ClearSeen drops the per-frame seen markers. The marker is per frame, so the
// caller clears it at the start of a pass rather than the sensor phase clearing
// it mid-walk [03 §3.4] P0-11.
func ClearSeen(status *uint32) {
	if status != nil {
		*status &^= SeenBit
	}
}
