// Cruise altitude, flight levels, and wake/SFX [04 §10.1][04 §9.1].
//
// CruiseAltitude for point and follow commands is targetY = (max(sea level, terrain height at target XZ) + signed offset) ×65536
// capped at 0x1FF0000 (about 511 world units), where terrain height is the bilinear four-corner query and the
// offset is cruisealt (full altitude) or cruisealt/2 for initial climb, or negated attach-piece world Y for hanging cargo [04 §10.1].
// Sea level is terrain header byte; terrain height sampled at cursor or at followed unit's piece world position; no lower clamp [04 §10.1].
// Arrival radii horizontal and strict: explicit air arrivals test hypot(dx,dz) < radius with radii 48/128/320 depending on order [04 §10.1], default dx²+dz² ≤0.25 [04 §10.1].

package movement

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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

// A caller-less `CruiseAltitudeForCarrier` used to stand here, folding the
// definition read and the optional halving into this file. It also halved the
// 32-bit `cruisealt` without the preamble's 16-bit narrowing, so the two
// expressions disagreed for any authored value past that range. Every commanded
// altitude is now CruiseAltitudeForOffset over an offset the caller computes —
// `def.CruiseAlt` whole, or HalfCruiseAlt of it [04 §10.1][04 R-AIR-01 §6].

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
// byte and its model-top word, starting from the band cached for this unit.
//
//	if mode not in {1,2}: band = 0
//	else if wy > wt:      band = 4
//	else:
//	    band = cached
//	    if wy - wt > -5:  band = 1
//	    if wl + wy == wt: band = 2
//	    if mt + wy < wt:  band = 3
//
// The three underwater tests are ordered overwrites, not exclusive branches:
// `3` wins if both `2` and `3` hold, and if none matches the cached band is
// retained. Every comparison is a signed integer in height-byte units, never
// 16.16 world units. Band `4` is strictly above water, `1` the shoreline skirt
// within five units above water, `2` draft exactly at the surface, `3` the
// whole model below water.
//
// Corrected 2026-08-31: this function previously returned an invented mapping —
// `0` for anything above sea level, `1` at exactly sea level, then `2`/`3` split
// on `y + waterline >= sea` — and carried a deferral marker claiming the
// thresholds were untraced. They are traced and Established; the old mapping
// inverted the above-water band (retail's `4`, not `0`), ignored the mover-mode
// gate that owns band `0`, dropped the cached-band retention entirely, and made
// the tests exclusive.
//
// Corrected 2026-09-04 (WU-19-140): the marker that stood here said the band-3
// test needed `mb`, "the definition's signed model-bottom word — the model's
// min-Y bound", that `content.UnitDef` had no counterpart to `ModelTop` to
// supply it, and that the retail loader's min-Y walk over the 3DO would settle
// it. Both halves were wrong. Retail computes **no** min-Y walk at all: the
// definition's minimum-Y word is zeroed immediately before the model-top walk
// runs and is never written from model geometry [02 R-CAT-01 §7]. And the band-3
// operand is not that word — it is the signed 16-bit high half of the model
// total-height dword, the same word the LOS emitter reads as its observer-height
// addend [03 R-P0-18-A §1] and the weapon water gates read as `(int16)`
// [06 R-WPN-05 §1]. Band 3 is therefore "the model's top, lifted by the unit's
// height, is still below the water level" — fully submerged — and it is
// reachable from `UnitDef.ModelTopFixed` with no new field and no new walk.
// Nothing above this note changes; only the operand's identity was misnamed.
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
	if u.Def != nil {
		if u.Def.Waterline+wy == wt {
			band = 2 // draft exactly at the surface [04 §9.1]
		}
		// `mt` is the definition's model total-height word read the way the
		// classifier reads it: the SIGNED 16-BIT high half of the 16.16 dword
		// the model-top walk produced, not the byte the LOS emitter takes
		// [04 R-MOV-01 §8a][03 R-P0-18-A §1]. Reading it at 16-bit width is the
		// contract, so a model taller than 32767 world units wraps negative
		// exactly as retail does rather than saturating — hence the int16 cast
		// rather than a comparison on ModelTopFixed.
		mt := int32(int16(u.Def.ModelTopFixed >> 16))
		if mt+wy < wt {
			band = 3 // the whole model is below the water level [04 §9.1]
		}
	}
	return band
}

// Wake is not an engine effect. The engine emits the band change alone
// [04 §9.1]; bands `2` and `3` are the two a stock hover script answers with
// its wake, and that decision belongs to the script, so no predicate here
// states it.
//
// Nor is there a clamped-altitude predicate: CruiseAltitudeForOffset above is
// the one place the 0x1FF0000 cap is applied [04 §10.1], and a caller asking
// whether a value reached the cap compares it against MaxCruiseAltitude.
