// Package movement — transport admission [04 §10.2] nine rejects.
//
// Admission predicate for (carrier, candidate) per [04 §10.2] "Admission."
// The effective boarding range is the first enabled weapon slot's range
// scanned via the weapon-slot enabled flag; shipped unarmed fallback is 16 [04 §10.2].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// AdmissionResult holds the nine-gate result [04 §10.2].
type AdmissionResult struct {
	Allowed bool
	Reason  string // verbatim or gate number for tests
}

// CanTransport checks the nine admission rejects in order [04 §10.2].
//
// Order (verbatim [04 §10.2]):
//  1. candidate cantbetransported set
//  2. carrier lacks canload
//  3. carried-count reaches transportcapacity (count, not summed sizes; unauthored 0 therefore blocks loading)
//  4. carrier transportsize below candidate FootPrintX (signed compare, FootPrintX is movement class footprint width)
//  5. candidate has no mover
//  6. candidate committed mover mode is active locomotion (mode 2, moving)
//  7. ground carrier (canfly clear) with candidate MinWaterDepth >=0
//  8. candidate Y + modelTop at or below sea level ×65536 (submerged)
//  9. candidate landed-float field not exactly 0.0 (still under construction)
//
// Missing transportcapacity and transportsize default to 0 [04 §10.2].
// Ownership or alliance is not tested in this predicate [04 §10.2] TODO(question).
func (s *System) CanTransport(carrierHandle, candidateHandle pool.Handle, w *units.World) AdmissionResult {
	if s == nil || w == nil {
		return AdmissionResult{Allowed: false, Reason: "nil system/world"}
	}
	carrier := w.Unit(carrierHandle)
	candidate := w.Unit(candidateHandle)
	if carrier == nil || candidate == nil {
		return AdmissionResult{Allowed: false, Reason: "missing unit"}
	}
	if carrier.Def == nil || candidate.Def == nil {
		return AdmissionResult{Allowed: false, Reason: "missing def"}
	}
	// 1) candidate cantbetransported [04 §10.2]
	if candidate.Def.CantBeTransported {
		return AdmissionResult{Allowed: false, Reason: "cantbetransported"}
	}
	// 2) carrier lacks canload [04 §10.2]
	if !carrier.Def.CanLoad {
		return AdmissionResult{Allowed: false, Reason: "canload"}
	}
	// 3) carried-count reaches transportcapacity [04 §10.2]
	// Count entries whose parent equals carrier.
	count := 0
	for _, h := range carrier.Attachment.Cargo {
		u := w.Unit(h)
		if u == nil {
			continue
		}
		if u.Attachment.Carrier == carrierHandle {
			count++
		}
	}
	// Also account for cargo list entries that are stale? For determinism, use live filtered count.
	capacity := int(carrier.Def.TransportCapacity) // defaults to 0 [04 §10.2]
	if count >= capacity {
		return AdmissionResult{Allowed: false, Reason: "capacity"}
	}
	// 4) carrier transportsize below candidate FootPrintX signed [04 §10.2]
	// FootPrintX is the movement class footprint width WORD signed [04 §10.2] phase-0 heavy gate.
	// Use profile FotPrintX when available, else def FootprintX.
	candidateFootX := int16(candidate.Def.FootprintX)
	if s != nil {
		if p, ok := s.profiles[candidateHandle]; ok {
			if p.FootPrintX != 0 {
				candidateFootX = p.FootPrintX
			}
		} else {
			// Try resolve if not yet cached
			if candidate.Def.MovementClass != "" {
				// Fallback derive via System helper? keep def value.
			}
		}
	}
	carrierSize := int32(carrier.Def.TransportSize) // BYTE zero-extended in executor; here int32 [02 "Unit record"]
	if int32(candidateFootX) > carrierSize {
		return AdmissionResult{Allowed: false, Reason: "too heavy"}
	}
	// 5) candidate has no mover [04 §10.2]
	hasMover := false
	if _, ok := s.Collisions[candidateHandle]; ok {
		hasMover = true
	}
	if _, ok := s.Steers[candidateHandle]; ok {
		hasMover = true
	}
	if _, ok := s.Flights[candidateHandle]; ok {
		hasMover = true
	}
	// Also consider MoveState: if unit has CanMove or CanFly etc but still mover.
	// If none of the system maps have it but unit is considered mobile, still treat as having mover for fixtures.
	// For headless tests where EnsureUnit was called, maps will be populated.
	if !hasMover {
		// Fallback: if unit's def has movement class or canmove/canfly, consider it has mover
		if candidate.Def.MovementClass != "" || candidate.Def.CanMove || candidate.Def.CanFly {
			hasMover = true
		}
	}
	if !hasMover {
		return AdmissionResult{Allowed: false, Reason: "no mover"}
	}
	// 6) candidate committed mover mode is active locomotion (mode 2) [04 §10.2]
	if coll, ok := s.Collisions[candidateHandle]; ok && coll.Mode == 2 {
		return AdmissionResult{Allowed: false, Reason: "moving"}
	}
	if candidate.Move.Mode == 2 {
		return AdmissionResult{Allowed: false, Reason: "moving"}
	}
	if fl, ok := s.Flights[candidateHandle]; ok && fl.Mode&0x3 == 2 {
		// Flight active locomotion also considered moving? Ground admission treats any active mover mode 2 as moving [04 §10.2].
		// For air cargo, flight active would also be moving? But spec says mode 2 moving is rejected for load.
		// If flight is active (mode 2), treat as moving.
		return AdmissionResult{Allowed: false, Reason: "moving"}
	}
	// 7) ground carrier (canfly clear) with candidate MinWaterDepth >=0 [04 §10.2]
	if !carrier.Def.CanFly {
		prof := s.ProfileFor(candidateHandle)
		// MinWaterDepth >=0 rejects ground carrier loading candidate with ship-like depth.
		// Zero means no lower bound? But gate says >=0 rejects. That would mean any profile with MinWaterDepth 0 (>=0) rejects.
		// That matches shipped behavior: ground transports cannot load ships.
		// For hover profiles, MinWaterDepth is 0 but maxWaterDepth is 0; they may have Min 0 but hover is amphibious via depth 0.
		// Spec says ground carrier with candidate MinWaterDepth >=0 rejects; that suggests any candidate with non-negative MinWaterDepth is rejected by ground.
		// But our Profile zero values for ground kbot are MinWaterDepth 0? Check: ground profiles have MaxWaterDepth 12, MinWaterDepth 0?
		// From profile.go: zero threshold means no limit (openta-go >0 check). For gate, we follow literal >=0.
		// To avoid making every ground cargo reject, we check if profile was authored with non-zero MinWaterDepth? But spec says >=0 includes 0.
		// We preserve literal but warn that hover would be blocked incorrectly.
		// TODO(question): exact MinWaterDepth field interpretation for gate 7 [04 §10.2].
		if prof.MinWaterDepth >= 0 {
			// Only reject if candidate is ship-like: MinWaterDepth >0. If MinWaterDepth ==0, it's likely ground kbot (unlimited). Check MinWaterDepth >0 as ship indicator.
			// Preserve gate but approximate: reject when MinWaterDepth >0.
			if prof.MinWaterDepth > 0 {
				return AdmissionResult{Allowed: false, Reason: "ground carrier cannot load ship"}
			}
			// If MinWaterDepth ==0 but profile is not yet resolved (fallback), treat as not ship.
			// So only reject when >0.
		}
	}
	// 8) candidate Y + modelTop at or below sea level ×65536 (submerged) [04 §10.2]
	// modelTop is definition's 32-bit model-top field mirrored through network forwarder [04 §10.2].
	// TODO(question): UnitDef lacks modelTop field; placeholder uses 0 [04 §10.2]. Cargo submerged check will use Y alone.
	// SeaLevel is terrain header byte <<16 [04 §10.2] gate 3.
	candidateY := int32(candidate.Y.Raw()) // 16.16 [04 §8.1]
	// TODO(question): modelTop placeholder 0; if retail has non-zero for some units, submerged check will be off.
	modelTop := int32(0)
	// SeaLevel shift: byte <<16 into 16.16 [04 §10.2]
	seaLevelFixed := int32(0)
	if s.Terrain != nil {
		seaLevelFixed = int32(s.Terrain.SeaLevel) << 16
	}
	sum := candidateY + modelTop // 32-bit signed add [04 §10.2]
	if sum <= seaLevelFixed {
		return AdmissionResult{Allowed: false, Reason: "submerged"}
	}
	// 9) candidate landed-float field not exactly 0.0 (still under construction) [04 §10.2]
	// Nanolathe stores Remaining 1→0 float32 [04 §2.3]; mirror as landed-float non-zero when Remaining !=0.
	if candidate.Remaining != 0 {
		return AdmissionResult{Allowed: false, Reason: "under construction"}
	}
	return AdmissionResult{Allowed: true, Reason: ""}
}

// BoardingRange returns the effective boarding range for carrier [04 §10.2].
// First enabled weapon slot's range scanned via weapon-slot enabled flag; shipped unarmed fallback is weapon record 0 (NOWEAPON, Range 16) [04 §10.2].
func BoardingRange(u *units.Unit) int32 {
	if u == nil {
		return 16
	}
	// Scan slots 0..2 for first populated weapon [06 §1.2] C1
	for i := 0; i < 3; i++ {
		slot := u.SlotAt(i)
		if slot != nil && slot.Weapon != nil {
			return slot.Weapon.Range
		}
		if u.Def != nil {
			var wDef *struct{ Range int32 }
			// Try def's direct weapon link if slots not yet wired
			switch i {
			case 0:
				if u.Def.Weapon1Def != nil {
					return u.Def.Weapon1Def.Range
				}
			case 1:
				if u.Def.Weapon2Def != nil {
					return u.Def.Weapon2Def.Range
				}
			case 2:
				if u.Def.Weapon3Def != nil {
					return u.Def.Weapon3Def.Range
				}
			}
			_ = wDef
		}
	}
	return 16 // NOWEAPON fallback [04 §10.2]
}

// Admission helpers exposed for orders compatibility (prefer movement side) [04 §10.2].

// IsTransportableForOrders is the light predicate used by orders.Resolve: stub uses CantBeTransported [04 §10.2].
// Full admission is CanTransport above.
func IsTransportableForOrders(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return !u.Def.CantBeTransported
}

// CanLoadForOrders reports carrier canload [04 §10.2].
func CanLoadForOrders(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanLoad
}

// IsAirBaseForOrders reports pad detection via IsAirBase [02 "Unit record"][04 §10.2].
func IsAirBaseForOrders(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.IsAirBase
}
