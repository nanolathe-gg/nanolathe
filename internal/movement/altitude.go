// Package movement — cruise altitude, flight levels, and wake/SFX [04 §10.1][04 §9.1].
//
// CruiseAltitude for point and follow commands is targetY = (max(sea level, terrain height at target XZ) + signed offset) ×65536
// capped at 0x1FF0000 (about 511 world units), where terrain height is the bilinear four-corner query and the
// offset is cruisealt (full altitude) or cruisealt/2 for initial climb, or negated attach-piece world Y for hanging cargo [04 §10.1].
// Sea level is terrain header byte; terrain height sampled at cursor or at followed unit's piece world position; no lower clamp [04 §10.1].
// Arrival radii horizontal and strict: explicit air arrivals test hypot(dx,dz) < radius with radii 48/128/320 depending on order [04 §10.1], default dx²+dz² ≤0.25 [04 §10.1].
package movement

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// MaxCruiseAltitude is the cap 0x1FF0000 [04 §10.1] about 511 world units.
const MaxCruiseAltitude int32 = 0x1FF0000 // about 511 <<16 [04 §10.1]

// CruiseAltitudeForOffset computes targetY for a given world XZ and integer cruise offset [04 §10.1].
//
// offset is cruisealt or cruisealt/2 (signed, round toward zero) or negated attach-piece Y integer part [04 §10.2].
// Uses max(sea level, terrain height at XZ) + offset, capped at 0x1FF0000 [04 §10.1].
// terrain height is bilinear four-corner query [03 §2.3] C7; sea level is header byte [03 §2.2] C9.
// If terrain is nil, uses sea level only.
// No lower clamp [04 §10.1].
func CruiseAltitudeForOffset(terrain *world.Terrain, x, z numeric.Fixed, offset int32) numeric.Fixed {
	var base int32 // in height units (0..255)
	if terrain != nil {
		sea := int32(terrain.SeaLevel)
		// Sample terrain height at XZ via bilinear HeightAt [03 §2.3] C7
		h := terrain.HeightAt(x, z)
		if h == numeric.Fixed(-1) {
			// OOB sentinel -1: treat as sea level [03 §2.3]
			base = sea
		} else {
			// HeightAt returns Fixed 16.16 world units; convert to height byte equivalent (value>>16)
			// The max(sea level, terrain height at target XZ) is in height units before ×65536 [04 §10.1].
			// Do max on integer height units.
			ht := int32(h.Raw() >> 16)
			if ht > sea {
				base = ht
			} else {
				base = sea
			}
		}
	} else {
		base = 0
	}
	val := (int64(base) + int64(offset)) * 65536
	if val > int64(MaxCruiseAltitude) {
		val = int64(MaxCruiseAltitude)
	}
	// No lower clamp [04 §10.1]
	return numeric.Fixed(val)
}

// CruiseAltitudeForCarrier computes targetY for carrier's point/follow command [04 §10.1].
// offset is carrier.Def.CruiseAlt or CruiseAlt/2.
func CruiseAltitudeForCarrier(terrain *world.Terrain, x, z numeric.Fixed, carrier *units.Unit, half bool) numeric.Fixed {
	if carrier == nil || carrier.Def == nil {
		return CruiseAltitudeForOffset(terrain, x, z, 0)
	}
	alt := carrier.Def.CruiseAlt
	if half {
		// Signed round toward zero [04 §10.2] altitude cruisealt/2
		if alt >= 0 {
			alt = alt / 2
		} else {
			alt = -((-alt) / 2)
		}
	}
	return CruiseAltitudeForOffset(terrain, x, z, alt)
}

// AltitudesEqual tests |dy| < 65537 for explicit-altitude arrival [04 §10.1] one world unit plus one subunit.
func AltitudesEqual(a, b numeric.Fixed) bool {
	dy := int64(a) - int64(b)
	if dy < 0 {
		dy = -dy
	}
	return dy < 65537
}

// AirArrival checks horizontal arrival for air orders [04 §10.1].
// explicitRadius: 0 means default test dx²+dz² ≤0.25 (0.5 wu) [04 §10.1]; otherwise test hypot < radius.
func AirArrival(ax, az, bx, bz numeric.Fixed, explicitRadius int32, ay, by numeric.Fixed, checkAltitude bool) bool {
	dx := float64(int64(ax)-int64(bx)) / 65536.0
	dz := float64(int64(az)-int64(bz)) / 65536.0
	if explicitRadius != 0 {
		if math.Hypot(dx, dz) >= float64(explicitRadius) {
			return false
		}
	} else {
		// dx²+dz² ≤0.25 (0.5 wu) [04 §10.1]
		if dx*dx+dz*dz > 0.25 {
			return false
		}
	}
	if checkAltitude {
		if !AltitudesEqual(ay, by) {
			return false
		}
	}
	return true
}

// MediumBand returns the setSFXoccupy band 0..4 [04 §9.1] using unit Y vs sea level and waterline/modelBottom.
// Bands: 0 land, 1 hover fringe, 2/3 water/wake, 4 deeper? Actual mapping per §9.1 not fully enumerated [04 §9.1];
// we return simplified:
//
//	0: height > seaLevel (land)
//	1: height == seaLevel fringe (hover stripe)
//	2: height < seaLevel but above waterline threshold (shallow water)
//	3: height deep water below modelBottom threshold
//	4: submerged
//
// TODO(question): exact band thresholds untraced [04 §9.1].
func MediumBand(terrain *world.Terrain, u *units.Unit) int {
	if u == nil {
		return 0
	}
	var sea int32
	if terrain != nil {
		sea = int32(terrain.SeaLevel)
	}
	y := int32(u.Y.Raw() >> 16) // signed height high word [04 §8.1] C21
	if y > sea {
		return 0 // land [04 §9.1]
	}
	if y == sea {
		return 1
	}
	// Below water: use waterline/modelBottom (waterline is draft for band 2 [02 "Unit record"])
	if u.Def != nil {
		wl := u.Def.Waterline
		// Simple: shallow if y + wl >= sea? Actually waterline is draft depth.
		// Keep bands 2 and 3 distinguished by waterline.
		if y+wl >= sea {
			return 2 // shallow hover wake [04 §9.1]
		}
		// Check upright/floater modelBottom not available; treat wl deep as 3
		return 3
	}
	return 2
}

// ShouldEmitWake reports whether hover wake SFX should emit [04 §9.1].
// Engine emits only band change; shipped hover scripts gate wake on bands 2 or 3 and spawn via emit-sfx types 2..5 from dedicated wake pieces [04 §9.1].
// This helper mirrors that band check.
func ShouldEmitWake(terrain *world.Terrain, u *units.Unit) bool {
	band := MediumBand(terrain, u)
	return band == 2 || band == 3
}

// WakeSFXType returns the SFX emit type for wake per band [04 §9.1].
// Types 2..5 from dedicated wake pieces (single-vertex pieces) [04 §9.1]; single-vertex pieces are leaf attachments [03 §2.4].
// Returns 0 if no wake.
func WakeSFXType(terrain *world.Terrain, u *units.Unit) int {
	if !ShouldEmitWake(terrain, u) {
		return 0
	}
	band := MediumBand(terrain, u)
	if band == 2 {
		return 2
	}
	return 3 // band 3 => type 3 etc [04 §9.1]
}

// IsCruiseClamped reports whether altitude was capped at 0x1FF0000 [04 §10.1].
func IsCruiseClamped(y numeric.Fixed) bool {
	return int32(y.Raw()) >= MaxCruiseAltitude
}
