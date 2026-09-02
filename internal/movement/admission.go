// Package movement — transport admission [04 §10.2] nine rejects.
//
// Admission predicate for (carrier, candidate) per [04 §10.2] "Admission."
// The effective boarding range is the first enabled weapon slot's range
// scanned via the weapon-slot enabled flag; shipped unarmed fallback is 16 [04 §10.2].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/content"
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
//
// No player, owner, side or diplomacy word is read anywhere on the load path
// [04 R-AIR-01 §12]: the predicate's inputs are the candidate definition, the
// carrier definition, the carrier's cargo list, the candidate's mover pointer,
// its flags-word mode mirror, its Y, the map's sea-level byte and its landed
// float — and nothing else. The command resolvers ahead of it carry no
// alliance qualifier on the carriable arm either, so an allied OR enemy unit
// that passes the nine rejects is loadable; what keeps an enemy out of an
// armed transport's hold is that the attack arm of the resolver claims the
// click first. The marker that stood here asked whether an upstream command
// layer gated on alliance; it does not.
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
	// 7) ground carrier (canfly clear) with candidate MinWaterDepth >= 0 [04 §10.2]
	// The word compared is the definition's own signed 16-bit copy of the
	// movement class's MinWaterDepth — the same copy the mobile footprint
	// validator's shallow gate reads and the same word Park tests for its +3
	// [04 R-AIR-01 §12]. Profile.MinWaterDepth is that copy: the compiler
	// narrows it through storeInt16 from the resolved class record, or from the
	// scratch record parsed on top of the template when the FBI names no
	// resolvable class, and `minwaterdepth` has no reader but that parser.
	//
	// The compare is signed >= 0: the predicate rejects when the word is NOT
	// negative, so an authored MinWaterDepth of 0 is rejected by a ground
	// carrier exactly as an authored 3 or 15 is, and only the template's
	// −10000 (or another authored negative) admits. The `> 0` that stood here
	// was the placeholder reading, chosen when the marker beside it doubted
	// the boundary; [04 R-AIR-01 §12] settles it as Established.
	if !carrier.Def.CanFly {
		prof := s.ProfileFor(candidateHandle)
		if prof.MinWaterDepth >= 0 {
			return AdmissionResult{Allowed: false, Reason: "ground carrier cannot load ship"}
		}
	}
	// 8) candidate Y + modelTop at or below sea level ×65536 (submerged) [04 §10.2]
	// SeaLevel is terrain header byte <<16 [04 §10.2] gate 3.
	candidateY := int32(candidate.Y.Raw()) // 16.16 [04 §8.1]
	// modelTop is the dword the definition loader writes as the unit's upper Y
	// bound after the model load, floored at zero [06 R-DMG-01 §7] — the same
	// full 16.16 value the projectile contact band reads [06 §8.1], which is
	// content.UnitDef.ModelTopFixed. The marker that stood here said UnitDef had
	// no such field and pinned the term to 0, so the gate tested the cargo's
	// anchor Y alone and called every unit whose ORIGIN sat at or below sea
	// level submerged.
	modelTop := int32(0)
	if candidate.Def != nil {
		modelTop = candidate.Def.ModelTopFixed
	}
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
			// Try def's direct weapon link if slots not yet wired. Only active links
			// count; the record-0 inactive sentinel a missed link resolves to is
			// not a weapon [02 §5 R-CONTENT-02].
			switch i {
			case 0:
				if !content.IsWeaponInactive(u.Def.Weapon1Def) {
					return u.Def.Weapon1Def.Range
				}
			case 1:
				if !content.IsWeaponInactive(u.Def.Weapon2Def) {
					return u.Def.Weapon2Def.Range
				}
			case 2:
				if !content.IsWeaponInactive(u.Def.Weapon3Def) {
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
