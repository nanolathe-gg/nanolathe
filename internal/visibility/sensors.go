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
	// ID correlates the sensor result with its committed unit pool slot.
	ID        uint16
	Owner     PlayerID
	Status    *uint32       // runtime status field; the phase writes 0x100/0x300/0x1000 P0-11
	X, Z      numeric.Fixed // 16.16 world position
	Y         numeric.Fixed
	Alive     bool
	Hidden    bool // hidden/cloaked instance bit [03 §3.2]
	Stealth   bool // definition stealth state also fed into gameplay visibility [03 §3.2][03 §3.4]
	Active    bool // runtime activation/on-state bit required by sensor callbacks [03 §3.4]
	OnOffable bool // definition on/off flag used by selected-unit circle presentation [03 §3.9]

	// Authored sensor distances [02 "Unit record"] P0-11. Zero means absent.
	RadarDistance    int32
	SonarDistance    int32
	RadarJam         int32
	SonarJam         int32
	MinCloakDistance int32

	// DecloakDeadline receives tick+90 when this unit is decloaked by proximity [03 §3.4] P0-11.
	DecloakDeadline *uint32
}

// SensorInput is an immutable per-tick contact snapshot for presentation.
// SensorInputs returns copies so the renderer cannot mutate authoritative
// sensor state [03 §3.4].
type SensorInput struct {
	ID        uint16
	Owner     PlayerID
	X, Y, Z   numeric.Fixed
	Status    uint32
	Hidden    bool
	Stealth   bool // retained separately for the presentation blink gate
	Active    bool
	OnOffable bool
	// Circles is populated on the first input and carries the callback result
	// window without introducing a second mutable service side channel.
	Circles []SensorCircle
}

// SensorCircle is one callback-table result from the completed sensor pass.
// Coordinates are surface cells (one cell per 128 world units); Kind is zero
// for the outer radar/sonar callback and nonzero for jammer callbacks [03 §3.4].
type SensorCircle struct {
	// SourceID identifies the live unit that emitted this callback. The
	// committed publisher uses it to discard circles left behind when cleanup
	// frees a unit after the sensor pass [03 §3.4][I6].
	SourceID uint16
	U, V     int32
	Radius   int32
	Kind     uint8
}

// SensorSurfaces receives the rasterized circles [03 §3.4] C11 P0-11.
//
// Radar, sonar and jammers NEVER author the word mask: they rasterize onto
// separate minimap surfaces (RADAR FINAL etc at 0x142DB/E3/EB) that are wiped each tick while the LOS mask persists [03 §3.4] P0-11.
// The three callback tables are distinct; jammer circles are drawn onto same FINAL with last-writer-wins presentation-only, never OR into word mask.
type SensorSurfaces interface {
	Wipe()                       // wiped each tick [03 §3.4]
	Sensor(u, v, radius int32)   // combined radar/sonar outer circle [03 §3.4]
	RadarJam(u, v, radius int32) // separate radar-jam circle [03 §3.4]
	SonarJam(u, v, radius int32) // separate sonar-jam circle [03 §3.4]
}

// SetSurfaces binds the sensor backing surfaces. Nil discards them.
func (s *Service) SetSurfaces(sf SensorSurfaces) {
	if s != nil {
		s.surfaces = sf
	}
}

// SensorInputs returns the last completed sensor pass's contact inputs.
func (s *Service) SensorInputs() []SensorInput {
	if s == nil || len(s.sensorInputs) == 0 {
		return nil
	}
	out := make([]SensorInput, len(s.sensorInputs))
	copy(out, s.sensorInputs)
	if len(out) != 0 && len(out[0].Circles) != 0 {
		out[0].Circles = append([]SensorCircle(nil), out[0].Circles...)
	}
	return out
}

// SensorCircles returns a copy of the callback results from the last completed
// sensor pass [03 §3.4].
func (s *Service) SensorCircles() []SensorCircle {
	if s == nil || len(s.sensorInputs) == 0 || len(s.sensorInputs[0].Circles) == 0 {
		return nil
	}
	return append([]SensorCircle(nil), s.sensorInputs[0].Circles...)
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
//  1. ownership/status: clear expired decloak state, then set friendly bits;
//  2. sensor circles: each active unit with radar or sonar emits one outer circle, with separate jam circles;
//  3. minimum-cloak proximity: qualifying cloaked units search by squared planar distance;
//  4. final visibility: use the shared four-point predicate and set SeenBit.
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Ally vision never OR'd: writer ORs only own bit, reader tests only local bit [03 §3.4] P0-11.
func (s *Service) SensorTick(tick uint32, playerCount int, allied func(a, b PlayerID) bool, units []SensorUnit) {
	if s == nil {
		return
	}
	// SeenBit is a per-frame marker. Clear it before the final visibility walk,
	// including the single-player skip, so it cannot leak into another
	// committed frame [03 §3.4].
	for i := range units {
		if units[i].Status != nil {
			*units[i].Status &^= SeenBit
		}
	}
	s.sensorInputs = s.sensorInputs[:0]
	if playerCount <= 1 {
		return // more than one player required [03 §3.4] P0-11 activePlayers>1 gate
	}
	var circles []SensorCircle
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
	// Surfaces are wiped each tick while the LOS mask persists [03 §3.4].
	if s.surfaces != nil {
		s.surfaces.Wipe()
	}
	for i := range units {
		u := &units[i]
		if !u.Alive {
			continue
		}
		cu, cv := surfaceProject(u.X), surfaceProject(u.Z)
		// Sensor callbacks require the unit's runtime active/on state. Cloak and
		// hidden state belong to the separate visibility/decloak paths [03 §3.4].
		if u.Active && (u.RadarDistance != 0 || u.SonarDistance != 0) {
			outer := u.RadarDistance // ONE circle, the larger of the two [03 §3.4] P0-11
			if u.SonarDistance > outer {
				outer = u.SonarDistance
			}
			circles = append(circles, SensorCircle{SourceID: u.ID, U: cu, V: cv, Radius: outer})
			if s.surfaces != nil {
				s.surfaces.Sensor(cu, cv, outer) // color 0xDD5 onto FINAL P0-11
			}
		}
		if u.Active && u.RadarJam != 0 {
			circles = append(circles, SensorCircle{SourceID: u.ID, U: cu, V: cv, Radius: u.RadarJam, Kind: 1})
			if s.surfaces != nil {
				s.surfaces.RadarJam(cu, cv, u.RadarJam) // separate table 0x20A color 0xDD7 P0-11
			}
		}
		if u.Active && u.SonarJam != 0 {
			circles = append(circles, SensorCircle{SourceID: u.ID, U: cu, V: cv, Radius: u.SonarJam, Kind: 2})
			if s.surfaces != nil {
				s.surfaces.SonarJam(cu, cv, u.SonarJam) // separate table 0x20C P0-11
			}
		}
	}
	// 3. Minimum-cloak proximity [03 §3.2] C10 [03 §3.4] C12.
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
	// 4. Final visibility pass uses the standard single-point half-height
	// projection through the mode-selected source [03 §3.4]. It sets SeenBit
	// for an admitted point after the hidden/decloak gates above.
	for i := range units {
		u := &units[i]
		if u.Status == nil || !u.Alive {
			continue
		}
		if *u.Status&SeenBit != 0 {
			continue // already seen P0-11
		}
		// Definition stealth participates in the same gameplay hidden predicate
		// as init-cloak/runtime cloak. It remains separately published so the
		// presentation blink gate does not infer gameplay state from art bits
		// [03 §3.2][03 §3.9].
		if (u.Hidden || u.Stealth) && (*u.Status&DecloakBit) == 0 {
			continue // cloaked and not within 90-tick decloak window → not visible P0-11
		}
		// The sensor final pass is the single-point form. It deliberately does
		// not apply the owner bypass or gameplay hull/sea checks [03 §3.4].
		if s.VisiblePoint(s.local, u.X, u.Y, u.Z) {
			*u.Status |= SeenBit
		}
	}
	for i := range units {
		u := &units[i]
		if u.Alive && u.Status != nil {
			s.sensorInputs = append(s.sensorInputs, SensorInput{ID: u.ID, Owner: u.Owner, X: u.X, Y: u.Y, Z: u.Z, Status: *u.Status, Hidden: u.Hidden, Stealth: u.Stealth, Active: u.Active, OnOffable: u.OnOffable})
		}
	}
	if len(s.sensorInputs) != 0 && len(circles) != 0 {
		s.sensorInputs[0].Circles = circles
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
