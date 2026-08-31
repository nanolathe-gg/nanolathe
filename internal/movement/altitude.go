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

// MediumBand is the `setSFXoccupy` classifier of [04 §9.1][04 R-MOV-01 §8a]:
// five values, computed by sequential overwrite from the committed mover-mode
// mirror, the signed height word, the map sea level, the definition's waterline
// byte and its model-bottom word, starting from the band cached for this unit.
//
//	if mode not in {1,2}: band = 0
//	else if wy > wt:      band = 4
//	else:
//	    band = cached
//	    if wy - wt > -5:  band = 1
//	    if wl + wy == wt: band = 2
//	    if mb + wy < wt:  band = 3
//
// The three underwater tests are ordered overwrites, not exclusive branches:
// `3` wins if both `2` and `3` hold, and if none matches the cached band is
// retained. Every comparison is a signed integer in height-byte units, never
// 16.16 world units. Band `4` is strictly above water, `1` the shoreline skirt
// within five units above water, `2` draft exactly at the surface, `3` model
// bottom below water.
//
// Corrected 2026-08-31: this function previously returned an invented mapping —
// `0` for anything above sea level, `1` at exactly sea level, then `2`/`3` split
// on `y + waterline >= sea` — and carried a deferral marker claiming the
// thresholds were untraced. They are traced and Established; the old mapping
// inverted the above-water band (retail's `4`, not `0`), ignored the mover-mode
// gate that owns band `0`, dropped the cached-band retention entirely, and made
// the tests exclusive.
//
// TODO(question): the band-3 test needs `mb`, the definition's signed
// model-bottom word — the model's min-Y bound, a different word from the
// model total-height dword that [04 R-AIR-01 §9] reads, and there is no
// authored `model-bottom` key to read it from. `content.UnitDef` carries
// `ModelTop` but no counterpart, so this build has no source for `mb` and the
// overwrite is not run: band `3` is unreachable today. What would settle it is
// the retail loader's min-Y walk over the 3DO, the counterpart of the
// established model-top walk in `formats.ThreeDO.ModelTop`.
func MediumBand(terrain *world.Terrain, u *units.Unit, cached int) int {
	if u == nil {
		return 0
	}
	// Mode 0 (attached/parked) and 3 (save-installed) classify as band 0; only
	// grounded (1) and airborne (2) movers reach the height tests
	// [04 R-MOV-01 §8].
	if mode := u.Move.Mode & 0x3; mode != 1 && mode != 2 {
		return 0
	}
	var wt int32
	if terrain != nil {
		wt = int32(terrain.SeaLevel)
	}
	wy := int32(u.Y.Raw() >> 16) // signed height high word [04 §8.1] C21
	if wy > wt {
		return 4 // strictly above water [04 §9.1]
	}
	band := cached
	if wy-wt > -5 {
		band = 1 // shoreline skirt [04 §9.1]
	}
	if u.Def != nil && u.Def.Waterline+wy == wt {
		band = 2 // draft exactly at the surface [04 §9.1]
	}
	// The band-3 overwrite is not run; see the note above the classifier.
	return band
}

// ShouldEmitWake reports whether a shipped hover script would spawn its wake
// effect for this unit's current band. The engine itself emits only the band
// change — wake is not an engine effect [04 §9.1] — so this is a description of
// what the stock scripts do with bands `2` and `3`, offered for tests and
// diagnostics, not an engine emitter.
func ShouldEmitWake(terrain *world.Terrain, u *units.Unit, cached int) bool {
	band := MediumBand(terrain, u, cached)
	return band == 2 || band == 3
}

// IsCruiseClamped reports whether altitude was capped at 0x1FF0000 [04 §10.1].
func IsCruiseClamped(y numeric.Fixed) bool {
	return int32(y.Raw()) >= MaxCruiseAltitude
}
