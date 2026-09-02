package hud

// Software-cursor shape selection [07 §8] C12.
//
// Retail keeps two bytes: the armed-order latch decides *authorization* and the
// cursor index decides *shape*; they coincide numerically only by table offset
// and must not be conflated [07 §8]. This file owns the shape half.
//
// The shape is chosen once per pointer update from three inputs: whether the
// pointer is over the world (viewport or minimap) at all, the armed latch, and
// the world-pick result under the pointer. When the selection holds several
// units the per-unit answers are reduced by **lowest index wins**, so the
// numbering of the index table is also its shape priority order [07 §8].

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/units"
)

// CursorHover is the world-pick result under the pointer [07 §8].
//
// Picking already distinguishes GUI/modal controls from world space and does
// not expose hidden/fogged objects, so a fogged unit arrives here as a nil
// Target exactly as it does for order commit [07 §8][03 §3.2].
type CursorHover struct {
	// OverWorld is the summary of the two pointer-region bits: the pointer is
	// inside the world viewport, or over the minimap. When it is false the
	// cursor is forced to the idle shape whatever the latch says [07 §8].
	OverWorld bool

	// Target is the picked unit, or nil for open ground [07 §8].
	Target *units.Unit

	// Feature is the definition of the feature occupying the picked cell, or
	// nil. Only its reclaimable flag is consulted [07 §8][05 "Feature reclaim"].
	Feature *content.FeatureDef

	// Placing reports that the mobile-build ghost is live, which routes the
	// shape through the placement branch instead of the per-unit table [07 §8].
	Placing bool

	// PlacementValid reports whether that ghost currently sits on a legal site.
	PlacementValid bool
}

// CursorSelection is the acting side of the decision: the local player index,
// the units currently carrying the selection bit in stable ascending pool order
// (I1), and the stocks the command-fire affordability gate reads [07 §9].
type CursorSelection struct {
	Viewer uint8
	Units  []*units.Unit

	// Hostile routes side tests through the same diplomacy predicate the order
	// resolver uses, so the cursor and the order cannot disagree [04 §3.4].
	Hostile func(actor, target *units.Unit) bool

	// Metal and Energy are the viewer's current stocks. The command-fire shape
	// turns to cursortoofar when the armed weapon's per-shot cost is not
	// covered [07 §8][05 "Weapon per-shot cost"].
	Metal, Energy float32
}

// ChooseCursor returns the cursor index for one pointer update [07 §8].
//
// Decision order, following the retail chooser:
//
//  1. Pointer outside the world viewport and minimap → `cursornormal`.
//  2. Mobile-build placement live → `cursorfindsite` / `cursortoofar`.
//  3. Empty selection → `cursorselect` when the idle latch hovers an own,
//     finished unit, else `cursornormal`.
//  4. Otherwise the minimum of the per-unit shape over the selection.
func ChooseCursor(latch input.Latch, sel CursorSelection, h CursorHover) int {
	if !h.OverWorld {
		return render.CursorNormal
	}
	// Mobile-build placement is decided by site validity, not by the per-unit
	// table; the ghost overlay uses cursorred/cursorgrn alongside it [07 §8].
	if h.Placing || latch == input.LatchMobileBuild {
		return cursorForBuildSite(h.PlacementValid)
	}
	live := make([]*units.Unit, 0, len(sel.Units))
	for _, u := range sel.Units {
		if u != nil && u.Alive && u.Def != nil {
			live = append(live, u)
		}
	}
	if len(live) == 0 {
		// No actor can advertise anything, so the only remaining shape is the
		// inspect cursor over an own idle unit [07 §8].
		if latch == input.LatchNormal && isInspectable(h.Target, sel.Viewer) {
			return render.CursorSelect
		}
		return render.CursorNormal
	}
	best := render.CursorNormal
	for _, u := range live {
		if idx := cursorForActor(latch, u, h, sel); idx < best {
			best = idx
		}
	}
	return best
}

// cursorForBuildSite selects the authored placement shape. Shape choice is a
// HUD decision; the renderer only resolves the resulting index to GAF art
// [07 §8].
func cursorForBuildSite(valid bool) int {
	if valid {
		return render.CursorFindSite
	}
	return render.CursorTooFar
}

// isInspectable is the own-unit predicate shared by the empty-selection branch
// and the contextual branch [07 §8]. The section spells the subject as "an
// own, active, finished, untasked unit", which is four gates: the unit belongs
// to the viewer; the runtime status word carries the active-state bit `0x20`;
// construction has finished; and the per-unit order guard is empty.
//
// The active gate used to be missing. [07 §8] recorded the active-state bit
// and the empty-current-task field as having "no counterpart in the current
// runtime flag word", and `docs/SPEC_CONFLICTS.md` SC16 wrote that up as a
// standing conflict, because bit `0x20` of this build's flag word was then the
// COB port's INBUILDSTANCE and testing it would have made the `cursorselect`
// shape unreachable. That collision is gone: INBUILDSTANCE is now the separate
// `units.Unit.InBuildStance` byte, and `units.ClassifierEligibleStatus` is bit
// `0x20` of the status word — written by the allocator initializer, cleared by
// death finalization, cleared for a scripted unit by the InitialMission
// postlude and set again by `MakeSelectable`
// [R-P0-04 "Runtime eligibility bit lifecycle"][04 §3.6][08 R-TRIG-01 §3]. So a
// mission unit still under script control is not inspectable, which is the
// point of the gate, and SC16's "no implementable counterpart" no longer holds.
//
// TODO(question): the untasked gate is still not implemented, and this is why.
// [07 §8] names it "the empty-current-task field"; the paragraph after it
// identifies the shared eligibility predicate's compared value as a per-unit
// order-guard float compared exactly to `0.0`, but no sentence says the two are
// the same field. `units.Unit.OrderGuard` is this build's order guard, and
// gating on it here makes `cursorselect` unreachable: the guard is written
// nonzero whenever the primary queue is non-empty, and an idle unit's primary
// queue holds a `Standby` node, so every idle own unit reads as mid-order. That
// is the same failure mode SC16 warned about for the `0x20` bit, so the clause
// is left out rather than shipped wrong. Either the guard's writer is too
// coarse — `Standby` is the idle state, not "an order being processed" — or the
// current-task field is a different word; retail's rectangle selection shares
// the same compare [07 §9], so under our writer it would select nothing either.
// A static trace of the inspect predicate's second compare, naming the field it
// reads and what an idle unit holds in it, would settle both.
func isInspectable(t *units.Unit, viewer uint8) bool {
	if t == nil || !t.Alive {
		return false
	}
	if t.Owner != viewer {
		return false
	}
	if t.Remaining != 0 { // finished construction [04 §2.3]
		return false
	}
	// The runtime active-state bit `0x20` [07 §8][07 §9].
	return t.Flags&units.ClassifierEligibleStatus != 0
}

// cursorForActor is the per-selected-unit shape table, dispatched on the armed
// latch exactly as the retail order predicate is [07 §8][07 §9].
//
// Every gate below reads the same authored unit-record capability flags the
// order resolver reads, so the advertised action and the performed action
// cannot disagree [04 §3.4].
func cursorForActor(latch input.Latch, u *units.Unit, h CursorHover, sel CursorSelection) int {
	def := u.Def
	t := h.Target
	hostile := t != nil && isHostileTo(sel, u, t)
	allied := t != nil && !hostile

	switch latch {
	case input.LatchNormal:
		return contextualCursor(u, h, sel, hostile, allied)

	case input.LatchMove:
		if !def.CanMove {
			return render.CursorNormal
		}
		// A resurrect-capable mover advertises revive over a reclaimable wreck
		// ahead of everything else [07 §8].
		if def.CanResurrect && reclaimableFeature(h) {
			return render.CursorRevive
		}
		if t != nil {
			if def.CanCapture && hostile {
				return render.CursorCapture
			}
			if hostile && def.CanReclamate {
				return render.CursorReclamate
			}
			if allied && canAssist(def) && needsWork(t) {
				return render.CursorRepair
			}
			if def.CanFly && t.Def != nil && t.Def.IsAirBase {
				return render.CursorUnload // VTOL landing uses the unload shape [07 §8]
			}
			if canCarry(def, t) {
				return transportCursor(def)
			}
			if def.CanGuard && allied {
				return render.CursorDefend
			}
		}
		return render.CursorMove

	case input.LatchAttack:
		if !def.CanAttack {
			return render.CursorNormal
		}
		// A bomber — a unit whose primary weapon is dropped rather than fired —
		// shows the bomb-sight shape; every other attack flavour shares
		// cursorattack [07 §8][02 "Weapon record"].
		if dropsBombs(def) {
			return render.CursorAirstrike
		}
		return render.CursorAttack

	case input.LatchBlast:
		if !def.CanDGun {
			return render.CursorNormal
		}
		// Command fire is gated on affordability, not on range: the shape flips
		// to cursortoofar when the stocks do not cover the weapon's
		// energypershot/metalpershot [07 §8].
		if affordable(def, sel) {
			return render.CursorAttack
		}
		return render.CursorTooFar

	case input.LatchUnload:
		if !def.CanLoad {
			return render.CursorNormal
		}
		return render.CursorUnload

	case input.LatchPickup:
		if t == nil || !canCarry(def, t) {
			return render.CursorNormal
		}
		return transportCursor(def)

	case input.LatchFollow:
		if !def.CanGuard || t == nil || !allied {
			return render.CursorNormal
		}
		if !def.CanFly && t.Def != nil && t.Def.CanFly {
			return render.CursorNormal // a ground guard cannot ward an air unit [07 §8]
		}
		return render.CursorDefend

	case input.LatchRepair:
		if t == nil || !canAssist(def) {
			return render.CursorNormal
		}
		return render.CursorRepair

	case input.LatchPatrol:
		if !def.CanPatrol {
			return render.CursorNormal
		}
		return render.CursorPatrol

	case input.LatchTeleport:
		return render.CursorTeleport

	case input.LatchReclaim:
		if !def.CanReclamate {
			return render.CursorNormal
		}
		if reclaimableFeature(h) || (t != nil && hostile) {
			return render.CursorReclamate
		}
		return render.CursorNormal

	case input.LatchCapture:
		if !def.CanCapture || t == nil || t.Owner == u.Owner {
			return render.CursorNormal
		}
		return render.CursorCapture

	case input.LatchMobileBuild:
		if !def.Builder {
			return render.CursorNormal
		}
		return render.CursorFindSite
	}
	return render.CursorNormal
}

// contextualCursor is the idle-latch branch [07 §8]. Retail reaches ATTACK and
// RECLAIM from here by rewriting the latch and re-entering the same table, so
// the two rewrites are spelled out first.
func contextualCursor(u *units.Unit, h CursorHover, sel CursorSelection, hostile, allied bool) int {
	def := u.Def
	t := h.Target
	if def.CanAttack && hostile {
		return cursorForActor(input.LatchAttack, u, h, sel)
	}
	if def.CanReclamate && hostile {
		return cursorForActor(input.LatchReclaim, u, h, sel)
	}
	if t != nil {
		if canAssist(def) && allied && needsWork(t) {
			return render.CursorRepair
		}
		if isInspectable(t, sel.Viewer) {
			return render.CursorSelect
		}
	}
	if def.CanResurrect && reclaimableFeature(h) {
		return render.CursorRevive
	}
	if def.CanReclamate && reclaimableFeature(h) {
		return render.CursorReclamate
	}
	if def.CanMove {
		return render.CursorMove
	}
	return render.CursorNormal
}

// transportCursor splits the carry shape by movement class: an air transport
// shows cursorpickup, a ground transport shows cursorload [07 §8].
func transportCursor(def *content.UnitDef) int {
	if def.CanFly {
		return render.CursorPickup
	}
	return render.CursorLoad
}

// reclaimableFeature reports a reclaimable feature under the pointer
// [07 §8][05 "Feature reclaim"].
func reclaimableFeature(h CursorHover) bool {
	return h.Feature != nil && h.Feature.Reclaimable
}

// canAssist is the repair/help-build capability gate. The authored record has
// no separate repair flag: assistance is gated on the builder's nanolathe,
// which is the flag the order resolver reads for HelpBuild/RepairUnit
// [04 §3.4].
func canAssist(def *content.UnitDef) bool { return def.Builder }

// needsWork reports a target assistance would act on: still under construction,
// or damaged [04 §3.4].
func needsWork(t *units.Unit) bool {
	if t == nil {
		return false
	}
	return t.Remaining != 0 || t.Health < t.MaxHealth
}

// canCarry is the transport admission gate for the shape only; the full
// capacity/size admission lives in the order path [04 §10.2].
func canCarry(def *content.UnitDef, t *units.Unit) bool {
	if !def.CanLoad || t == nil || t.Def == nil {
		return false
	}
	return !t.Def.CantBeTransported
}

// dropsBombs reports a unit whose primary weapon is authored `dropped`, the
// gate that selects the airstrike shape over cursorattack [07 §8].
func dropsBombs(def *content.UnitDef) bool {
	return !content.IsWeaponInactive(def.Weapon1Def) && def.Weapon1Def.Dropped
}

// affordable reports that the viewer's stocks cover the command-fire weapon's
// per-shot cost [07 §8][02 "Weapon record"]. The command-fire weapon is the
// first authored slot carrying `commandfire`, falling back to weapon 1.
func affordable(def *content.UnitDef, sel CursorSelection) bool {
	w := commandFireWeapon(def)
	if w == nil {
		return false
	}
	return float64(sel.Energy) >= w.EnergyPerShot && float64(sel.Metal) >= w.MetalPerShot
}

// commandFireWeapon picks the slot the BLAST latch fires [02 "Weapon record"].
func commandFireWeapon(def *content.UnitDef) *content.WeaponDef {
	for _, w := range [3]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if !content.IsWeaponInactive(w) && w.CommandFire {
			return w
		}
	}
	if content.IsWeaponInactive(def.Weapon1Def) {
		// The sentinel a missed link fills is not a command-fire weapon, so
		// the caller sees no weapon rather than a zero-cost one [02 §5
		// R-CONTENT-02].
		return nil
	}
	return def.Weapon1Def
}

// isHostileTo routes through the caller-supplied diplomacy predicate so the
// cursor and the order resolver agree on sides [04 §3.4].
func isHostileTo(sel CursorSelection, actor, target *units.Unit) bool {
	if sel.Hostile != nil {
		return sel.Hostile(actor, target)
	}
	return actor.Owner != target.Owner
}
