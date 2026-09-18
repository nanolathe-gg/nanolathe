// This file: transport admission [04 §10.2], the nine rejects.
//
// Admission predicate for (carrier, candidate) per [04 §10.2] "Admission."

package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// AdmissionResult holds the nine-gate result [04 §10.2].
type AdmissionResult struct {
	Allowed bool
	Reason  string // verbatim or gate number for tests
}

// CanTransport checks the nine admission rejects in order [04 §10.2].
//
// Order ([04 §10.2], with rejects 4 and 5 in the executable's order — the
// mover test precedes the size compare [04 R-AIR-01 §12]):
//  1. candidate cantbetransported set
//  2. carrier lacks canload
//  3. carried-count reaches transportcapacity (count, not summed sizes; unauthored 0 therefore blocks loading)
//  4. candidate has no mover
//  5. carrier transportsize below candidate FootPrintX (signed compare, FootPrintX is movement class footprint width)
//  6. candidate committed mover mode is active locomotion (mode 2, moving)
//  7. ground carrier (canfly clear) with candidate MinWaterDepth >=0
//  8. candidate Y + modelTop at or below sea level ×65536 (submerged)
//  9. candidate landed-float field not exactly 0.0 (still under construction)
//
// The result of the pair is the same either way — both arms reject — so the
// order is observable only through the reported Reason, which a reimplementation
// uses to say why a pickup was refused.
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
	// 4) candidate has no mover [04 §10.2][04 R-AIR-01 §12]. The executable
	// tests the candidate's mover reference itself, and does so BEFORE the size
	// compare, so a moverless candidate is refused as such rather than as too
	// heavy.
	//
	// The extra bmcode test is not redundant here. Retail allocates a mover
	// exactly for a definition whose bmcode is 1 [04 §5][08 R-AI-03 §7.4], while
	// this build's HasMover answers "a live collision record that is not a
	// building", and the record's building flag is derived from bmcode == 0. The
	// two predicates therefore agree for bmcode 0 and 1 and part for an authored
	// bmcode above 1, where retail has no mover and HasMover alone would say it
	// does. Testing the byte restores retail's exactly-one gate; definition
	// capabilities are never a substitute for it.
	if candidate.Def.BMCode != 1 || !s.HasMover(candidateHandle) {
		return AdmissionResult{Allowed: false, Reason: "no mover"}
	}
	// 5) carrier transportsize below candidate FootPrintX signed [04 §10.2]
	// FootPrintX is the movement class footprint width WORD signed [04 §10.2] phase-0 heavy gate.
	// Use profile FotPrintX when available, else def FootprintX.
	candidateFootX := int16(candidate.Def.FootprintX)
	if s != nil {
		if p := handleRow(s.profiles, candidateHandle); p != nil {
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
	// 6) Admission reads the committed unit mirror, which can differ from a
	// pending takeoff, touchdown or detach request [04 R-AIR-01 §12].
	if candidate.Move.ModeMirror&3 == 2 {
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
	//
	// Retail compares the float against zero and continues only on the equal
	// condition, which an UNORDERED result also raises: a NaN in that field would
	// be admitted, not rejected, where this ordered `!= 0` refuses it. Remaining
	// is written only by the construction settlement as a value in 1→0, so no
	// path in this build can put a NaN there; the difference is recorded because
	// it is the one input on which the two forms disagree [04 R-AIR-01 §12].
	if candidate.Remaining != 0 {
		return AdmissionResult{Allowed: false, Reason: "under construction"}
	}
	return AdmissionResult{Allowed: true, Reason: ""}
}

// The nine rejects above are the whole load gate. Two things retail states
// about loading have no consumer in this build, and neither is implemented
// here rather than half-implemented:
//
//   - The effective boarding range — the first enabled weapon slot's range,
//     with the shipped unarmed fallback of the inactive weapon record's 16
//     [04 §10.2] — gates nothing: no command path tests a distance before
//     admitting a load.
//   - The three one-line definition reads the resolver needs (`cantbetransported`,
//     `canload`, `isairbase`) belong to internal/orders, which cannot call into
//     this package — internal/movement imports internal/orders, not the other
//     way round — so the resolver reads its own definition fields and this file
//     offers no forwarding twin that would drift from them.
