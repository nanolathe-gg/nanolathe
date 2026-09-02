// Package orders implements command resolution and attack-chase/guard handlers [04 §3.4, §3.5][PLAN_06 WU-06-4].
package orders

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Hostility is settled. [04 R-ORD-02 §1] corrects §3.4's wording: the byte is
// the acting **player's** diplomacy byte toward the target's side, not a
// per-side byte on the acting unit's definition, and it is read once at
// resolver entry — a value of 0 is hostile, any other value friendly; with no
// target both hostile and friendly are false. That is exactly the session's
// alliance row A of [05 R-SHARE-01 §1], which composition injects as
// Queue.Binding().Hostility.
//
// P0-I16: hostility and target lookup moved onto Queue.Hostility/Lookup; package globals removed.

func getHostility(actor *units.Unit) func(*units.Unit, *units.Unit) bool {
	if actor != nil {
		if q := QueueForUnit(actor); q != nil {
			if binding := q.Binding(); binding != nil && binding.Hostility != nil {
				return binding.Hostility
			}
		}
	}
	return nil
}

func isHostile(actor, target *units.Unit) bool {
	// "with no target both hostile and friendly are false" [04 R-ORD-02 §1].
	if actor == nil || target == nil {
		return false
	}
	if fn := getHostility(actor); fn != nil {
		return fn(actor, target)
	}
	// Retired 2026-09-01 (WU-19-34): a `Def.Side != Def.Side` arm stood here as
	// the unbound fallback, cited to §3.4's sentence "hostility comes from a
	// per-side diplomacy byte on the acting unit's definition, indexed by the
	// target's side". [04 R-ORD-02 §1] corrects that sentence: the byte is the
	// acting PLAYER's diplomacy byte toward the target's side, which is row A
	// of the player slot's two alliance rows, indexed by the target's slot
	// [05 R-SHARE-01 §1][05 "Player slot"]. Reading the definition's side made
	// two players who picked the same side permanently friendly and two allies
	// of different sides permanently hostile — neither is a fact about the
	// alliance rows.
	//
	// With no rows to read (an unbound queue: tests and tools) the answer is
	// the rows as slot initialization leaves them — "slot initialization zeroes
	// both rows and sets the self entry of each to one" [05 R-SHARE-01 §1] — so
	// a unit is friendly to its own player's units and hostile to every other
	// slot. That is a citable state, not a stand-in.
	return actor.Owner != target.Owner
}

// Capability gates map to the definition's two capability words
// [04 R-SPEC-01 §0]: word A carries `builder`, `isairbase`, `canfly`,
// `canhover`, `hoverattack` and `kamikaze`; word B carries `canattack`,
// `canguard`, `canpatrol`, `canmove`, `canload`, the `canreclamate` mirror,
// `canreclamate`, `canresurrect`, `cancapture` and `candgun` [02 R-KEYS-01].
// Each is one authored key, so each gate below is one compiled flag.
//
// Retired 2026-09-01 (WU-19-16): an `opaqueDefBit5` constant stood here under an
// accepted-blocked marker calling word B's bit 5 unknown. It is `canguard`,
// which canGuard already gates, and the constant had no reader.

func canMove(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanMove
}
func canAttack(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanAttack
}
func canDGun(u *units.Unit) bool { // special-attack capability [04 §3.4] code 4
	if u == nil || u.Def == nil {
		return false
	}
	// "Code 4 — special attack. `candgun` → `AttackSpecial`; else reject"
	// [04 R-ORD-02 §1]. The special-attack capability is the authored `candgun`
	// key and nothing else.
	return u.Def.CanDGun
}
func canUnload(u *units.Unit) bool { // can-unload [04 §3.4] code 5
	if u == nil || u.Def == nil {
		return false
	}
	// There is no can-unload capability: "Code 5 — unload. `canload`, `canfly`,
	// a target, and target `isairbase` → `VTOL_Landing`; else `canload` →
	// `VTOL_Unload` or `Ground_Unload`; else reject" [04 R-ORD-02 §1]. Both
	// directions of transport are gated by the one authored `canload` key.
	return u.Def.CanLoad
}
func canGuard(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanGuard
}
func canReclaim(u *units.Unit) bool { // can-reclaim gate covers reclaim/resurrect [04 §3.4] code 12
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanReclamate || u.Def.CanResurrect
}
func canCapture(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanCapture
}

// hasLiveMover is retail's "live mover" test — the first thing the move and
// patrol resolutions ask after their capability gate [04 R-ORD-02 §1].
//
// The mover is the locomotion controller a unit owns; a building-class unit
// owns none. The two are the same test in the resolver's own terms: the
// code-3 arm picks `Attack_Chase` "when the unit has a mover" and
// `Attack_NoMove` "when state bit 29 is set" [04 R-SPEC-01 §1], and
// `GetBuilt` calls the case it skips "a product without a mover (a
// building-class product)" [04 R-FAC-02 §4]. Status bit 29 is written at
// creation from the definition's authored `bmcode` being zero
// [04 R-COLL-01 §2], which units.initialStatusFlags already does.
//
// This is why a factory can be given a move order at all: stock factories
// author `CanMove=1` on a `BMcode=0` definition (ARMLAB, ARMVP, ARMAAP and
// their CORE counterparts, [03 R-RND-02A] asset census), so they pass the
// can-move gate and then fail this one — which is exactly the `QMove` rally
// arm below.
func hasLiveMover(u *units.Unit) bool {
	return u != nil && u.Flags&units.BuildingClassStatus == 0
}

func canPatrol(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanPatrol
}

// hasBuildList is command code 14's gate: "the definition's build list is
// non-empty" [04 R-ORD-02 §1]. It is NOT the authored `builder` key — retail
// tests the compiled `CANBUILD` page, and the two are independent authorings
// [02 "Build-menu catalog keys"].
//
// The page lives in the catalog, which this package holds no handle to, so the
// question is asked through the queue's session-owned binding. A queue with no
// build-list query refuses, the same way canEngageSlot refuses without a weapon
// adapter: the alternative is answering a catalog question from a different
// key and calling the answer retail's.
func hasBuildList(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	b := bindingOfUnit(u)
	if b == nil || b.BuildList == nil {
		return false
	}
	return b.BuildList(u.Def)
}

// isCarriable is the carriable test of [04 R-ORD-02 §1] codes 1, 2 and 6:
// "*Carriable* is the nine-reject transport admission of §10.2", applied in
// §10.2's order with the carrier's runtime cargo state included.
//
// internal/movement owns that ladder (its CanTransport walks the nine rejects
// and names the one that fired); the resolver reaches it through the binding
// rather than keeping a second copy, because rejects 3, 5, 6, 8 and 9 read the
// carrier's live cargo list, the candidate's mover reference and committed
// mover mode, and the map's sea level — state the order package does not own.
// An unbound queue refuses rather than applying a partial ladder: the previous
// site applied reject 1 alone, which admitted a submerged, airborne or
// still-under-construction candidate onto a full transport.
func isCarriable(carrier, candidate *units.Unit) bool {
	if carrier == nil || candidate == nil || carrier.Def == nil || candidate.Def == nil {
		return false
	}
	b := bindingOfUnit(carrier)
	if b == nil || b.TransportAdmission == nil {
		return false
	}
	return b.TransportAdmission(carrier, candidate)
}

// nanoReach is the repair admission of [04 R-ORD-01 §7], which
// [04 R-ORD-02 §1] names *nano-reach* and shares between command codes 1, 2
// and 8. Its terms, in the order that section gives them:
//
//	the target exists; my definition carries the `canreclamate` mirror bit
//	(word B bit 9, the parser's second copy of `canreclamate`); the target's
//	16-bit health differs from its `maxdamage`; the target's mover mode is not
//	airborne (≠ 2); and the water clause
//	  (not canfly(me) or amphibious(me) or seaLevel <= targetTop)
//	  and (canfly(me) or seaLevel − MaxWaterDepth(me) <= targetTop)
//	with targetTop = the whole part of the target's Y plus the whole part of
//	its definition's model-height word ([R-COB-03 §2] port 11, the model
//	bounding box's maximum Y, [04 R-MOV-03 §5]).
//
// Both whole parts are the high word of a 16.16 dword, which is an arithmetic
// shift and therefore floors (I3); numeric.Fixed.Floor is that shift, and
// content.UnitDef.ModelTop is already the definition word's whole part.
//
// For an aircraft the clause reduces to `seaLevel <= targetTop`: it will not
// repair a unit whose top is under water [04 R-ORD-01 §7].
func nanoReach(actor, target *units.Unit) bool {
	if actor == nil || actor.Def == nil || target == nil || target.Def == nil {
		return false
	}
	// The mirror bit is a second copy of `canreclamate` alone; `canresurrect`
	// is a different bit of word B and is not part of it [04 R-ORD-02 §1].
	if !actor.Def.CanReclamate {
		return false
	}
	// TODO(question): [04 R-ORD-02 §1] code 1 step 3 adds, in parentheses,
	// "no health test — a full-health friendly resolves to a repair", while
	// the nano-reach admission it requires, [04 R-ORD-01 §7], carries this
	// health term. The two sentences cannot both be literal. The traced
	// admission is implemented; the parenthetical is read as "no test beyond
	// nano-reach's own", the way code 2's arm adds a second, stricter one.
	// What would settle it: a trace of code 1's friendly arm showing whether
	// it calls the shared admission or an inlined copy without the health term.
	if target.Health == target.Def.MaxDamage {
		return false
	}
	if target.Move.Mode == 2 { // airborne [04 R-MOV-01 §8]
		return false
	}
	targetTop := int32(target.Y.Floor()) + target.Def.ModelTop
	sea := seaLevelWholeUnits(actor)
	if actor.Def.CanFly {
		// (canfly and not amphibious) leaves the first conjunct as the sea
		// test; the second is satisfied by `canfly`.
		return actor.Def.Amphibious || sea <= targetTop
	}
	// A ground actor satisfies the first conjunct outright and keeps the
	// second: its own movement class's maximum water depth is how far below
	// the surface it can still reach.
	return sea-actor.Def.MaxWaterDepth <= targetTop
}

// seaLevelWholeUnits reads the map's sea level through the queue's world
// adapter. The terrain header's sea level is a byte in whole world units
// [04 §10.2]; a queue with no world adapter reports 0, which is the height of
// a map with no water and makes nano-reach's water clause vacuous rather than
// inventing a level.
func seaLevelWholeUnits(u *units.Unit) int32 {
	b := bindingOfUnit(u)
	if b == nil || b.World == nil || b.World.SeaLevel == nil {
		return 0
	}
	return int32(b.World.SeaLevel())
}

// isFollowable is the alive gate and nothing else. [04 R-ORD-02 §1] settles
// what §3.4's "followable target" means: there is no per-target followable
// predicate. Code 7 and the follow arms of codes 1 and 2 gate on the ACTOR's
// `canguard` and on the target being friendly; separately, "a target that
// exists but lacks the alive bit 28 rejects **every** code before the switch".
func isFollowable(t *units.Unit) bool {
	return t != nil && t.Alive
}

// isLandingPad is the authored `isairbase` key — word A bit 9 of
// [04 R-SPEC-01 §0], the only pad test the resolver makes [04 R-ORD-02 §1].
func isLandingPad(t *units.Unit) bool {
	return t != nil && t.Def != nil && t.Def.IsAirBase
}

// Retired 2026-08-31: `isStructure` stood here, a `!CanMove` stub carrying an
// open-question marker about "structure detection for the no-move attack
// variant". Its only caller was code 3's ground tail, which [R-ORD-02 §1] settles as a
// test on the ACTOR's mover reference and state bit 29, not on the target's
// class. There is no structure-detection question left to answer.
// Retired 2026-09-01 (WU-19-34): `isDamaged` stood here, `Health < MaxHealth`.
// Its only caller was code 1's repair arm, whose real admission is nano-reach
// [04 R-ORD-02 §1] — and nano-reach's own health term is `health differs from
// maxdamage`, not `below` it, so the two are not the same test.

// isUnfinished is the "target unfinished" predicate the assist-or-repair
// resolutions read (command codes 1, 2 and 8, [04 R-ORD-01 §5]).
//
// Correction (PT3-04). This used to read `0 < remaining < 1`, excluding a
// remaining fraction of exactly 1 — which is precisely the state a freshly
// allocated nanoframe is created in (internal/construction sets the product's
// fraction to 1 and steps it down from there [04 §2.3]). A right-click on a
// brand-new frame therefore never resolved to `HelpBuild`, and the frame only
// became assistable after its own builder had landed one admitted work step.
// Every predicate retail spends on this quantity is a comparison against zero —
// the shared work step's own entry test is `remaining == 0.0f`
// [05 R-WORK-01 §1], `Capture`'s "cloud of vapor" rejection is `remaining != 0`,
// and `HelpBuild`'s own "nothing left to assist" arm is `remaining == 0` — so
// unfinished is "not zero", with no upper bound.
func isUnfinished(u *units.Unit) bool {
	if u == nil {
		return false
	}
	return u.Remaining != 0 // remaining 1→0 [04 §2.3][05 R-WORK-01 §1]
}
func canResurrect(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanResurrect
}

// ResolvePos holds optional ground position and feature probe for codes 1 and 12 [04 §3.4].
type ResolvePos struct {
	X, Y, Z              numeric.Fixed
	HasFeature           bool
	IsWreck              bool // wreck feature
	FeatureResurrectable bool // wreck that can resurrect when actor canResurrect
	IsLandingPad         bool // feature is landing pad (rare)
}

// Ensure handlers are registered even if init ordering placed this file before table.go.
var handlersRegistered bool

func ensureHandlers() {
	if handlersRegistered {
		return
	}
	if len(table) == 0 {
		return
	}
	if id := Lookup("Attack_Chase"); id != 0 && table[int(id)].Handler != nil {
		handlersRegistered = true
		return
	}
	registerChaseGuardHandlers()
	if id := Lookup("Attack_Chase"); id != 0 && table[int(id)].Handler != nil {
		handlersRegistered = true
	}
}

// Resolve maps command code 1..14 + acting unit + optional target + optional ground position
// to canonical command NAME [04 §3.4] verbatim, then via Lookup to ID; failed gate yields 0 [04 §3.1].
func Resolve(code int, actor *units.Unit, target *units.Unit, pos *ResolvePos) ID {
	ensureHandlers()
	name := resolveName(code, actor, target, pos)
	if name == "" {
		return 0
	}
	return Lookup(name)
}

// resolveName produces the canonical name; caller looks it up [04 §3.4].
func resolveName(code int, actor *units.Unit, target *units.Unit, pos *ResolvePos) string {
	if actor == nil {
		return ""
	}
	// A target that exists but lacks the alive bit rejects every code before
	// the switch [04 R-ORD-02 §1]. §3.4's summary row for code 2 ("a dead unit
	// target becomes a queued move") reads the queued-move arm onto the wrong
	// condition; the traced resolver never reaches the switch with a dead
	// target, and its queued-move arm is the live-mover test below.
	if target != nil && !target.Alive {
		return ""
	}
	switch code {
	case 1:
		return resolveContextual(actor, target, pos)
	case 2:
		return resolveMove(actor, target)
	case 3:
		return resolveAttackAt(actor, target, pos)
	case 4:
		if !canDGun(actor) {
			return ""
		}
		return "AttackSpecial"
	case 5:
		if !canUnload(actor) {
			return ""
		}
		// "`canload`, `canfly`, a target, and target `isairbase` →
		// `VTOL_Landing`" [04 R-ORD-02 §1] code 5; the canfly term was missing,
		// so a ground transport ordered to unload onto an air base resolved the
		// air-only landing executor.
		if actor.Def != nil && actor.Def.CanFly && target != nil && isLandingPad(target) {
			return "VTOL_Landing"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Unload"
		}
		return "Ground_Unload"
	case 6:
		// "A target that is carriable → pickup or air twin; else reject"
		// [04 R-ORD-02 §1]; carriable is the whole nine-reject admission, so
		// the acting unit is the carrier of that pair.
		if target == nil || !isCarriable(actor, target) {
			return ""
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Pickup"
		}
		return "Ground_Pickup"
	case 7:
		if !canGuard(actor) || target == nil || isHostile(actor, target) {
			return ""
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Follow"
		}
		return "Follow_Ground"
	case 8:
		// "Nano-reach must pass (else reject); target unfinished →
		// `HelpBuild` or air twin, else `RepairUnit` or air twin — no health
		// test here either" [04 R-ORD-02 §1]. "No health test here" means no
		// test beyond the one nano-reach already carries (health differs from
		// `maxdamage`), which is why a full-health finished friendly rejects at
		// the gate rather than resolving a repair with nothing to repair.
		//
		// The gate used to be `isBuilder`, the authored `builder` key, which
		// admitted a factory (builder, no nanolathe reach) and rejected a
		// reclaimer, and applied none of the other three terms.
		if target == nil || !nanoReach(actor, target) {
			return ""
		}
		if isUnfinished(target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_HelpBuild"
			}
			return "HelpBuild"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_RepairUnit"
		}
		return "RepairUnit"
	case 9:
		if !canPatrol(actor) {
			return ""
		}
		// The patrol rally marker. An immobile builder records the patrol
		// instead of executing it: `QPatrol` is a 60-tick delayed tail rotate
		// with no goal binding, and the factory's `GetBuilt` copies it onto
		// each finished product as a patrol [04 R-ORD-02 §1][04 §3.8].
		//
		// The old test here was "no target → QPatrol", which is not the
		// resolver's condition and left a mobile unit unable to patrol at all:
		// a patrol is issued against a ground point with no target, so every
		// patrol resolved to the queued marker.
		if !hasLiveMover(actor) {
			return "QPatrol"
		}
		// Mirror bit 9 is the second copy the definition parser makes of
		// `canreclamate`; set selects the repair patrol [04 R-ORD-02 §1]
		// [04 R-ORD-01 §7]. This used to stub the gate with the Builder flag.
		if canReclaim(actor) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_RepairPatrol"
			}
			return "RepairPatrol"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Patrol"
		}
		return "Patrol"
	case 10:
		// There is no code-10 arm to establish: "neither resolver has a code-10
		// arm; both fall to their defaults — the identity-form resolver returns
		// the `GetBuilt`-shaped identity 0x13, the name-form resolver writes an
		// empty name (reject) — and no caller inside the bounded census emits
		// code 10" [04 §3.4 row 10]. This is the name-form resolver.
		return ""
	case 11:
		return "Teleport"
	case 12:
		if !canReclaim(actor) {
			return ""
		}
		if pos != nil && pos.HasFeature {
			if pos.IsWreck && canResurrect(actor) && pos.FeatureResurrectable {
				return "Resurrect"
			}
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_Reclaim"
			}
			return "Reclaim"
		}
		if target != nil {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_ReclaimUnit"
			}
			return "ReclaimUnit"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Reclaim"
		}
		return "Reclaim"
	case 13:
		// "Code 13 — capture. `cancapture`, a target, and the target's owner
		// differing from mine → `Capture`. **Correction to §3.4's row 13:** it
		// says 'target hostile and differently owned'; hostility is **not**
		// tested — an allied unit of another player is capturable by this code"
		// [04 R-ORD-02 §1].
		if !canCapture(actor) || target == nil {
			return ""
		}
		if actor.Owner == target.Owner {
			return ""
		}
		return "Capture"
	case 14:
		// "The definition's build list is non-empty **and a live mover exists**
		// → `MobileBuild` or air twin; else reject" [04 R-ORD-02 §1]. The mover
		// term is why a factory — which authors `CanMove=1` on a `BMcode=0`
		// definition and owns a build list — does not resolve a mobile build
		// from its own build page.
		if !hasBuildList(actor) || !hasLiveMover(actor) {
			return ""
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_MobileBuild"
		}
		return "MobileBuild"
	default:
		return ""
	}
}

func resolveContextual(actor *units.Unit, target *units.Unit, pos *ResolvePos) string {
	// Hostile and able to attack becomes an attack order [04 §3.4] code 1
	if target != nil && isHostile(actor, target) && canAttack(actor) {
		name := resolveAttackAt(actor, target, pos)
		if name != "" {
			return name
		}
	}
	// "friendly and nano-reach passes: target unfinished → `HelpBuild` or air
	// twin, else `RepairUnit` or air twin" [04 R-ORD-02 §1] code 1 step 3. The
	// arm used to be gated on `isDamaged(target) || isUnfinished(target)` with
	// no admission at all, so any unit at all — a tank with no nanolathe —
	// resolved a repair on a scratched friendly and then stood there.
	if target != nil && !isHostile(actor, target) && nanoReach(actor, target) {
		if isUnfinished(target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_HelpBuild"
			}
			return "HelpBuild"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_RepairUnit"
		}
		return "RepairUnit"
	}
	if target != nil && isCarriable(actor, target) {
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Pickup"
		}
		return "Ground_Pickup"
	}
	// "`canguard` and friendly → `VTOL_Follow` or `Follow_Ground`" — the follow
	// arm is gated on the ACTOR's canguard and on the target being friendly,
	// never on a property of the target [04 R-ORD-02 §1] code 1 step 6.
	if target != nil && isFollowable(target) && canGuard(actor) && !isHostile(actor, target) {
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Follow"
		}
		return "Follow_Ground"
	}
	if pos != nil && pos.HasFeature {
		if pos.IsWreck && canResurrect(actor) && pos.FeatureResurrectable {
			return "Resurrect"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Reclaim"
		}
		return "Reclaim"
	}
	// Both interface variants of the contextual code end at the same move arm:
	// `canmove` **and a live mover**, else reject [04 R-ORD-02 §1]. There is no
	// queued-move arm in code 1, so a contextual click with an immobile builder
	// selected resolves to nothing — a factory rally is set with the explicit
	// move command (code 2), not contextually.
	if !canMove(actor) || !hasLiveMover(actor) {
		return ""
	}
	if actor.Def != nil && actor.Def.CanFly {
		return "VTOL_Move"
	}
	return "Move_Ground"
}

func resolveMove(actor *units.Unit, target *units.Unit) string {
	if !canMove(actor) {
		return ""
	}
	// The rally marker. `QMove` is resolved before any target test: an
	// immobile builder records the move instead of executing it
	// [04 R-ORD-02 §1]. The record is a 60-tick delayed tail rotate that binds
	// no goal, so the factory never moves; its `GetBuilt` walks the factory's
	// primary queue at completion and re-issues each `QMove` against the
	// product [04 §3.8][04 R-FAC-02 §4].
	if !hasLiveMover(actor) {
		return "QMove"
	}
	if target != nil {
		if isHostile(actor, target) && (canCapture(actor) || canReclaim(actor)) {
			if canCapture(actor) {
				return "Capture"
			}
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_ReclaimUnit"
			}
			return "ReclaimUnit"
		}
		// "friendly and nano-reach passes and the target is unfinished →
		// `HelpBuild` or air twin" [04 R-ORD-02 §1] code 2. The same section's
		// next arm — friendly, nano-reach, and health below `maxdamage` →
		// `RepairUnit` — is not built here: PLAN 19 §2.4's row is code 8's gate
		// and the shared helper, and adding a resolution code 2 does not have
		// today is a separate change with its own before/after.
		if !isHostile(actor, target) && nanoReach(actor, target) && isUnfinished(target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_HelpBuild"
			}
			return "HelpBuild"
		}
		// "I am `canfly`, friendly, target `isairbase` → `VTOL_Landing`"
		// [04 R-ORD-02 §1] code 2. A ground unit ordered onto a pad is not
		// landing on it, and a hostile pad is not a landing site.
		if actor.Def != nil && actor.Def.CanFly && !isHostile(actor, target) && isLandingPad(target) {
			return "VTOL_Landing"
		}
		if isCarriable(actor, target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_Pickup"
			}
			return "Ground_Pickup"
		}
		// "`canguard` and friendly → follow or air twin" [04 R-ORD-02 §1] code 2.
		if isFollowable(target) && canGuard(actor) && !isHostile(actor, target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_Follow"
			}
			return "Follow_Ground"
		}
	}
	if actor.Def != nil && actor.Def.CanFly {
		return "VTOL_Move"
	}
	return "Move_Ground"
}

func resolveAttack(actor *units.Unit, target *units.Unit) string {
	return resolveAttackAt(actor, target, nil)
}

// resolveAttackAt includes the established position-only code-3 arm. A ground
// unit with an armed runtime record resolves to Suppress; an aircraft resolves
// to the dropped-weapon run or the ordinary air-to-ground run. This belongs in
// the canonical resolver shared by UI, AI, mission, and network producers
// [04 R-ORD-02 §1].
func resolveAttackAt(actor *units.Unit, target *units.Unit, pos *ResolvePos) string {
	if !canAttack(actor) {
		return ""
	}
	if target == nil {
		if pos != nil && actor.Flags&units.ArmedStatus != 0 {
			if actor.Def != nil && !actor.Def.CanFly {
				return "Suppress"
			}
			if actor.Def != nil && actor.Def.Weapon1Def != nil && actor.Def.Weapon1Def.Dropped {
				return "AirStrike"
			}
			return "AirToGround"
		}
		if actor.Def != nil && actor.Def.Kamikaze {
			return "Attack_Kamikaze"
		}
		return ""
	}
	// "The armed branch runs only when my state word has bit 31 (the armed bit
	// of [R-ORD-01 §3]); an unarmed unit falls straight to the kamikaze test"
	// [R-ORD-02 §1]. That is what makes a weaponless suicide unit resolve
	// `Attack_Kamikaze` while an armed mobile one resolves `Attack_Chase`.
	//
	// Retired 2026-08-31: a "suppression for the non-air special case" stub
	// stood here, keyed on the actor having no authored primary weapon and on
	// the presence of a literal `suppress` key in the definition's unparsed
	// leftovers — a test hook, not a retail predicate, and one no asset
	// authors. `Suppress` is the *position-only* arm of code 3 above; a code-3
	// call with a target never reaches it [R-ORD-02 §1].
	if actor.Flags&units.ArmedStatus == 0 {
		if actor.Def != nil && actor.Def.Kamikaze {
			return "Attack_Kamikaze"
		}
		return ""
	}
	// The four air variants, exactly as [04 R-ORD-02 §1] gives them for code 3:
	// with *W1* my definition's first-weapon definition and `dropped` doc 06's
	// flag — "*W1* `dropped` and target not `canfly` → `AirStrike`; *W1* not
	// `dropped` and target `canfly` → `AirToAir`; target not `canfly` and I
	// lack `hoverattack` → `AirToGround`; target not `canfly` and `hoverattack`
	// → `AirToGroundHover`; a `dropped` *W1* against a flying target → reject".
	//
	// Corrected 2026-09-01 (WU-19-16). The stub selected `AirToGroundHover` on
	// the TARGET's `canhover` — a property of the thing being attacked — where
	// every term of the real rule is on the actor except the target's `canfly`;
	// it also invented an `AirToAir` arm keyed on my own `toairweapon` and an
	// `AirStrike` arm keyed on the target being an air base, neither of which
	// exists. A `dropped` bomber against an aircraft now rejects rather than
	// resolving a run it cannot fly.
	if actor.Def != nil && actor.Def.CanFly {
		// A sentinel primary (a missed link) is inactive, so it is not a
		// dropped weapon [02 §5 R-CONTENT-02].
		w1 := actor.Def.Weapon1Def
		dropped := !content.IsWeaponInactive(w1) && w1.Dropped
		targetFlies := target.Def != nil && target.Def.CanFly
		switch {
		case dropped && !targetFlies:
			return "AirStrike"
		case dropped: // a dropped primary against a flying target
			return ""
		case targetFlies:
			return "AirToAir"
		case actor.Def.HoverAttack:
			return "AirToGroundHover"
		default:
			return "AirToGround"
		}
	}
	// The ground tail of code 3 [R-ORD-02 §1]: "I am not `canfly` → a live
	// mover → `Attack_Chase`; state bit 29 (immobile) → `Attack_NoMove`; else
	// fall through", and the fall-through is "word A `kamikaze` →
	// `Attack_Kamikaze`; else reject".
	//
	// Corrected 2026-08-31. This branched on `isStructure(target)` — a property
	// of the THING BEING ATTACKED — so ordering a mobile unit to attack any
	// building resolved `Attack_NoMove`, which never moves. The unit stood
	// wherever it was told and, unless the building was already inside its
	// range, never fired a shot. Every one of the three tests is on the ACTOR.
	// The kamikaze arm also moved: it is the fall-through for a unit that is
	// neither a mover nor immobile, not a test that precedes them.
	if hasLiveMover(actor) {
		return "Attack_Chase"
	}
	if actor.Flags&units.BuildingClassStatus != 0 {
		return "Attack_NoMove"
	}
	if actor.Def != nil && actor.Def.Kamikaze {
		return "Attack_Kamikaze"
	}
	return ""
}

// Ensure content import is used.
var _ = (*content.UnitDef)(nil)

// ---------------------------------------------------------------------------
// Attack-chase state machine [04 §3.5]
// ---------------------------------------------------------------------------

// Retired 2026-08-31: `chaseAbandonMask` (0x01), `chaseDisengageMask` (0x06)
// and `stubStandoffWorld` (64) stood here, each carrying an open-question marker
// saying its value was "not located". None of the three exists in retail.
// [04 R-ORD-01 §3] gives the handler's real pre-checks — pending `0x800`, a
// null target, pending `0x10008`, and the leash — and the standoff is the
// slot's engagement distance, which [06 R-WPN-05 §1] closes as the weapon's
// authored `range`. The invented masks additionally aliased real bits: `0x01`
// is the deadline bit and `0x02`/`0x04` are movement bits, so an ordinary
// deadline expiry or a path verdict completed the order outright.

// Retired (WU-18-8): `leashExceeded` stood here. It was this package's second
// pursuit-leash test, cited to the same contract as combat.go's `leashBroken`
// and disagreeing with it below one world unit, so which one an order used
// decided whether it abandoned. What it did: it promoted the record's 16-bit
// anchor pair to 16.16 (`anchor * 65536`), subtracted it from the unit's
// FRACTIONAL 16.16 position, promoted the leash the same way, and compared
// `dx² + dz² >= leash²` entirely in 16.16 — carrying two open-question markers
// that said the units of the anchor and of the leash were not established.
//
// [R-STANCE-01 §4] settles both questions and the arithmetic with them: the
// deltas are taken between WHOLE world units on both sides (the unit's
// position's high half against the sign-extended 16-bit anchor), the distance
// is `trunc(hypot(dx, dz))` — truncated toward zero before the compare — and
// the test is the inclusive `leash <= d`. Truncating the distance is what the
// retired form got wrong: keeping the fractional parts inside the hypot
// abandons early on any diagonal. With an anchor at the origin and a leash of
// 5, a unit at (3.9, 3.9) whole units is at `trunc(hypot(3, 3)) = 4` by the
// contract and continues; the 16.16 form measured 5.51 and abandoned. The
// contract's form is combat.go's `leashBroken`, which is now the package's only
// leash test — used by `Attack_Chase` (resolve.go), the ground `RepairUnit`
// (work.go), the air executors, and `VTOL_RepairUnit` (vtolwork.go).

// Retired 2026-09-01 (WU-19-6): `setBandedGoalAroundWard` stood here. It wrote
// the record's goal to `ward + (standoff, 0, 0)` — a due-east point at a
// standoff its two callers took from p1 with a fallback of 20 world units —
// under an open-question marker saying the banded geometry was not located. It is
// located. [04 R-ORD-01 §8] gives the whole closure: the radius is computed by
// the handler from the two footprints and never read from the issuer, the
// direction is one RNG draw taken once at admit, the record's goal triple
// holds an OFFSET rather than a position, and the payload is a point goal —
// never an annulus and never a rectangle. The fallback 20 has no retail
// counterpart at all: "a guard record never carries a caller-supplied radius,
// so 'the radius when p1 is zero' is not a case retail has". Both callers are
// below; nothing replaces the literal.

// guardFollowRadius is `p1` of [04 R-ORD-01 §8 point 1], in whole world units:
//
//	s  = FootPrintX(me) + FootPrintX(ward) + 2      (whole cells)
//	p1 = s · 16                                     (whole world units)
//
// Both terms are the X word of the unit's copied footprint SIZE pair — the
// same word `Park` and the transport size gate read [04 R-ORD-01 §1] — so a
// Z-asymmetric footprint contributes only its X size. The handler writes p1
// unconditionally at admit; there is no issuer input and no zero case.
func guardFollowRadius(me, ward *units.Unit) int32 {
	return (footprintXOf(me) + footprintXOf(ward) + 2) * 16
}

func footprintXOf(u *units.Unit) int32 {
	if u == nil || u.Def == nil {
		return 0
	}
	return u.Def.FootprintX
}

// GuardFollowPoint is the world point and arrival radius a `Follow_Ground`
// record's follow-maintenance leg installs [04 R-ORD-01 §8 point 3]: the
// ward's position plus the record's stored anchor offset, with
//
//	radius = p1 / 2 = (FootPrintX(me) + FootPrintX(ward) + 2) · 8
//
// The division is signed and toward zero (I3); p1 is 16·s and therefore even
// and positive, so it is exact. The Y sum is formed here and ignored by the
// installer, which takes X and Z only.
//
// It is exported because the movement side derives the same goal for a record
// whose payload is not currently bound (internal/movement/goals.go), and the
// two must not carry separate arithmetic.
func GuardFollowPoint(n *Node, wardX, wardY, wardZ numeric.Fixed) (x, y, z numeric.Fixed, radius int32) {
	if n == nil {
		return wardX, wardY, wardZ, 0
	}
	return wardX + n.GoalX, wardY + n.GoalY, wardZ + n.GoalZ, int32(n.Param1) / 2
}

// chaseVerticalJump is the substate 1-4 test of [04 R-ORD-01 §3]: the strafe
// arm jumps to substate 6 when the two units are separated by MORE than eight
// world units on Y. The compare is on the raw 16.16 difference against eight
// world units, so it is strict and exact, not a whole-unit truncation.
const chaseVerticalJump int64 = 8 << 16

// canEngageSlot is the shot-admission gate `Attack_Chase` phases 1 and 3 ask
// before they bind a slot [04 R-ORD-01 §3]. It routes to the combat owner
// through the queue binding; a queue with no weapon adapter, or an adapter that
// does not supply the gate, refuses — which sends the handler down its own
// established "gate failed" arm rather than binding a slot the weapon layer
// would then refuse to fire.
func canEngageSlot(u *units.Unit, target pool.Handle, slot int) bool {
	b := bindingOfUnit(u)
	if b == nil || b.Weapons == nil || b.Weapons.CanEngage == nil {
		return false
	}
	return b.Weapons.CanEngage(u, target, slot)
}

// attackChaseHandler is the pursuing attack [04 R-ORD-01 §3].
//
//	Pre-checks, in order: satisfied 0x800 → complete; target null → complete;
//	satisfied ∩ 0x10008 → complete; the leash of [R-STANCE-01 §4] → complete.
//	Phase 0: requires a mover reference, no `canfly`, and state bit 31 (else
//	cancel-all); caption clear; goal = own position; p2 = 0; if p1 is 0 take
//	the default slot pick; advance. Phase 1: release the payload; satisfied ∩
//	0x3000 → advance; the shot-admission gate for slot p1 fails → advance; else
//	release slots 0 and 2, bind slot p1 to the target, gate = 0x13808; hold.
//	Phase 2 (maneuver): see below. Phase 3: satisfied ∩ 0x40E0 → phase = 1,
//	return 4; else if the shot gate passes: release slots 0 and 2, bind slot p1,
//	gate = 0x148E8, deadline 30, hold; else inhibit all, gate = 0x100E8,
//	deadline 30, hold. Other phase: cancel-all.
//
// Rewritten 2026-08-31. What stood here was written against [04 §3.5]'s prose
// summary and three invented constants, and [04 R-ORD-01 §3] carries an
// explicit correction to that summary. The differences that mattered in play:
// the standoff was a fixed 64 world units rather than the weapon's range, so an
// ordered unit orbited far outside its own reach and never fired; no phase ever
// bound a weapon slot at all (phases 1 and 3 were bare open-question advances);
// the goals were written straight into the record's goal fields instead of
// through the payload installers, so the mover was steered by whatever the
// movement side's own per-order fallback invented; and the pre-check masks
// aliased the deadline and movement bits, completing the order on an ordinary
// path verdict.
func attackChaseHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	// Pre-checks in the order [04 R-ORD-01 §3] fixes. 0x800 is the disengage
	// bit; 0x10008 is the target-removed/target-cloaked pair of §6.
	if satisfied&0x800 != 0 {
		return Code(5) // *complete*
	}
	if n.Target == 0 {
		return Code(5) // *complete*
	}
	if satisfied&pendTargetGone != 0 {
		return Code(5) // *complete*
	}
	// The leash is tested before the phase switch, on every dispatch, and only
	// when it is non-zero — leashBroken carries that guard itself
	// [R-STANCE-01 §4].
	if leashBroken(u, n) {
		return Code(5) // at or beyond the leash completes; the compare is inclusive
	}
	switch n.Phase {
	case 0:
		// "requires a mover reference, no canfly, and state-word bit 31".
		// hasLiveMover is this package's mover-reference test; bit 31 is the
		// armed bit, set when the definition resolved a weapon [R-ORD-01 §3].
		if !hasLiveMover(u) || u == nil || (u.Def != nil && u.Def.CanFly) || u.Flags&units.ArmedStatus == 0 {
			return Code(7) // *cancel-all*
		}
		captionClear(u)
		n.GoalX, n.GoalY, n.GoalZ = u.X, u.Y, u.Z
		n.Param2 = 0
		if n.Param1 == 0 {
			n.Param1 = uint32(defaultAttackSlot(u))
		}
		return Code(1) // *advance*
	case 1:
		// "release the payload" is the installers' own release arm, reached
		// here without an install [04 R-ORD-01 §1].
		if b := bindingOfUnit(u); b != nil && b.Movement != nil && b.Movement.Release != nil {
			b.Movement.Release(n)
		}
		if satisfied&0x3000 != 0 {
			return Code(1) // *advance*
		}
		if !canEngageSlot(u, n.Target, int(n.Param1)) {
			return Code(1) // *advance* — out of reach, go maneuver
		}
		releaseSlot(u, 0)
		releaseSlot(u, 2)
		bindSlotToUnit(u, int(n.Param1), n.Target)
		n.DynamicGate = 0x13808
		return Code(2) // *hold*
	case 2:
		return chaseManeuver(u, n)
	case 3:
		if satisfied&0x40E0 != 0 {
			n.Phase = 1
			return Code(4) // *hold* with the phase already reset
		}
		if canEngageSlot(u, n.Target, int(n.Param1)) {
			releaseSlot(u, 0)
			releaseSlot(u, 2)
			bindSlotToUnit(u, int(n.Param1), n.Target)
			n.DynamicGate = 0x148E8
			armDeadline(n, tick, 30)
			return Code(2) // *hold*
		}
		inhibitSlot(u, slotAll)
		n.DynamicGate = 0x100E8
		armDeadline(n, tick, 30)
		return Code(2) // *hold*
	default:
		return Code(7) // *cancel-all*
	}
}

// chaseManeuver is `Attack_Chase` phase 2, the orbit [04 R-ORD-01 §3].
//
// Let `d` be the slot's engagement distance — the weapon's authored range
// [06 R-WPN-05 §1]. By p2: **0** → point goal at the target radius d, p2 = 1.
// **1-4** → if |myY − targetY| > 8 world units: point goal radius d/2, p2 = 6;
// else draw `a = bearing(me → target) − 0x4000 + RNG(0x8000)` and install a
// point goal at `target − d·(sin a, 0, cos a)` with radius d/4 — **p2
// unchanged**. **5** → point goal radius d/2, p2 = 6. **6** → point goal at
// the target radius 0, p2 = 7. **7** → annulus (outer d, inner d/2), p2 = 8.
// **8** → annulus (outer 2d, inner d), p2 = 0. **≥ 9** → cancel-all. Every arm
// advances.
//
// [04 R-ORD-01 §3]'s correction to §3.5 applies here: because the strafe arm
// never increments p2, the reachable cycle is 0 → 1 (repeated strafes) → 6 → 7
// → 8 → 0, entered at 6 only through the vertical jump. Substates 2, 3, 4 and 5
// are dead under this handler and are written out anyway, because p2 is a
// record field a save can restore into any of them.
func chaseManeuver(u *units.Unit, n *Node) Code {
	if n.Param2 >= 9 {
		return Code(7) // *cancel-all*
	}
	d := engagementDistance(u, n.Param1)
	tgt := targetOf(u, n)
	if tgt == nil {
		// Every arm below reads the target's position; the pre-check only
		// rejects a null handle, so an unresolvable one waits rather than
		// installing a goal at the origin.
		return Code(3)
	}
	switch n.Param2 {
	case 0:
		installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, d)
		n.Param2++
	case 1, 2, 3, 4:
		if absFixed(u.Y.Raw()-tgt.Y.Raw()) > chaseVerticalJump {
			installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, truncHalf(d))
			n.Param2 = 6
			break
		}
		sx, sy, sz, radius := chaseStrafePoint(u, tgt, d)
		installPointGoal(u, n, sx, sy, sz, radius)
		// p2 unchanged: the strafe arm is the one that repeats.
	case 5:
		installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, truncHalf(d))
		n.Param2++
	case 6:
		installPointGoal(u, n, tgt.X, tgt.Y, tgt.Z, 0)
		n.Param2++
	case 7:
		installAnnulusGoal(u, n, tgt.X, tgt.Y, tgt.Z, d, truncHalf(d))
		n.Param2++
	case 8:
		installAnnulusGoal(u, n, tgt.X, tgt.Y, tgt.Z, 2*d, d)
		n.Param2 = 0
	}
	return Code(1) // *advance*
}

// chaseStrafePoint is the strafe arm's goal: a point one standoff away from the
// target along a bearing drawn from the half-circle centred on the line from
// the target back toward me, with an arrival radius of a quarter standoff
// [04 R-ORD-01 §3].
//
// The bearing is `atan2q(target.X − my.X, target.Z − my.Z)`, less a quarter
// turn, plus one simulation draw below 0x8000 — a half turn — so the result
// sweeps the semicircle from 90 degrees left of the line to 90 degrees right of
// it, which is what makes the unit circle its target instead of walking at it.
// The offset is subtracted from the target on X and Z and the target's own Y is
// kept, so the goal stays in the target's horizontal plane.
//
// The scaled sine and cosine are numeric's shared table helpers. [06 §3.3]
// states retail's index arithmetic as `((int16)angle + 32) >> 6` over even byte
// offsets — a half-step rounding bias numeric.Sin and numeric.Cos do not apply,
// since they index `angle >> 7` directly. Adding it belongs in numeric, where
// it would move every trig consumer in the simulation at once; it is not made
// inside this unit, and the difference here is at most one table entry of a
// randomly drawn bearing.
func chaseStrafePoint(u, tgt *units.Unit, d int32) (x, y, z numeric.Fixed, radius int32) {
	bearing := numeric.AngleFromAtan2(int64(tgt.X.Raw()-u.X.Raw()), int64(tgt.Z.Raw()-u.Z.Raw()))
	angle := numeric.Angle(uint16(bearing) - 0x4000 + uint16(drawBelow(u, 0x8000)))
	magnitude := int32(d << 16)
	offX := numeric.MulRound(numeric.Sin(angle), magnitude)
	offZ := numeric.MulRound(numeric.Cos(angle), magnitude)
	return tgt.X - numeric.Fixed(offX), tgt.Y, tgt.Z - numeric.Fixed(offZ), truncQuarter(d)
}

// truncHalf and truncQuarter divide toward zero, which is what retail's
// `cltd; sub; sar` sequences do for the d/2 and d/4 radii [04 R-ORD-01 §3].
// Go's `/` already truncates toward zero, so these only name the contract.
func truncHalf(d int32) int32    { return d / 2 }
func truncQuarter(d int32) int32 { return d / 4 }

func absFixed(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------------------------------------------------------------------------
// Guard assistance triggers [04 §3.5] Follow_Ground / VTOL_Follow / Guard_NoMove
// ---------------------------------------------------------------------------

func isInDedupArr(arr *[units.GuardLatchSize]pool.Handle, h pool.Handle) bool {
	for i := 0; i < units.GuardLatchSize; i++ {
		if arr[i] == h {
			return true
		}
	}
	return false
}
func pushDedupArr(arr *[units.GuardLatchSize]pool.Handle, h pool.Handle) {
	copy(arr[0:], arr[1:])
	arr[units.GuardLatchSize-1] = h
}

func getLookupForWard(n *Node, u *units.Unit) *units.Unit {
	if n == nil || n.Target == 0 {
		return nil
	}
	if u != nil {
		if q := QueueForUnit(u); q != nil {
			if binding := q.Binding(); binding != nil && binding.Lookup != nil {
				if tgt := binding.Lookup(n.Target); tgt != nil {
					return tgt
				}
			}
		}
	}
	return nil
}

func wardHasConstruction(ward *units.Unit) bool {
	// An unfinished ward, on the same "not zero" reading isUnfinished carries
	// [04 §2.3][05 R-WORK-01 §1]; a ward whose fraction is still exactly 1 is a
	// nanoframe nobody has worked yet, which is the case a guard is most likely
	// to be pointed at (PT3-04).
	return isUnfinished(ward)
}
func isFriendlyConstruction(actor, ward *units.Unit) bool {
	return !isHostile(actor, ward)
}
func canRepairGuard(actor *units.Unit) bool {
	// Leg 3 of [04 R-UNIT-06 §1]: "when the ward's health (signed word) compares
	// below its definition's maximum-damage word **and the guard's definition
	// has the builder bit**". It is the authored `builder` key alone; the
	// `canreclamate` disjunct that stood here admitted reclaimers with no
	// nanolathe to the repair leg.
	return actor != nil && actor.Def != nil && actor.Def.Builder
}
func wardIsDamaged(ward *units.Unit) bool {
	return ward != nil && ward.Health < ward.MaxHealth
}
func wardHasBuildOrder(ward *units.Unit) bool {
	if ward == nil {
		return false
	}
	q := QueueForUnit(ward)
	if q == nil || len(q.primary) == 0 {
		return false
	}
	head := q.primary[0]
	// "0x100000 marks the nanolathe/build-site class, tested by the guard-assist
	// branch" — a named bit of the descriptor gate mask, Established [04 §3.1].
	return head.StaticGate&0x100000 != 0
}

// guardHandler is the follow guard: `Follow_Ground` and its air twin
// `VTOL_Follow` [04 R-ORD-01 §8][04 R-UNIT-06 §1]. `Guard_NoMove` no longer
// shares it — it is a different order, not a follow variant, and its body is
// guardNoMoveHandler in combat.go [04 R-ORD-01 §8 point 5].
//
// Entry gates, in order [04 R-UNIT-06 §1]: a missing ward completes the order
// (code 5); a guard that is itself carried cancels its whole queue (code 7); a
// ward whose definition can fly removes the order (code 8 — a ground guard
// follows only ground wards); a phase byte beyond 1 cancels all (code 7).
//
// Phase 0 is the admit of [04 R-ORD-01 §8 points 1 and 2]; phase 1 is the
// assist evaluation of [04 R-UNIT-06 §1], whose fall-through is the follow
// maintenance of [04 R-ORD-01 §8 points 3 and 4].
func guardHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	_ = satisfied
	if n == nil || n.Target == 0 {
		return Code(5) // no ward → *complete* [04 R-UNIT-06 §1]
	}
	ward := getLookupForWard(n, u)
	if ward == nil {
		return Code(5)
	}
	if u != nil && u.Attachment.Carrier != 0 {
		return Code(7) // a carried guard cancels its whole queue [04 R-UNIT-06 §1]
	}
	// The flying-ward gate is stated for the GROUND guard ("a ground guard
	// follows only ground wards"); [04 R-UNIT-06 §1]'s air paragraph lists the
	// air twin's additions and does not repeat it, so it is applied on the same
	// canfly fork the command resolver uses to pick between the two names
	// [04 R-ORD-02 §1] rather than to both.
	if !unitCanFly(u) && unitCanFly(ward) {
		return Code(8) // *abandon* — the order is removed [04 R-UNIT-06 §1]
	}
	if n.Phase > 1 {
		return Code(7) // *cancel-all* [04 R-UNIT-06 §1]
	}
	if n.Phase == 0 {
		return guardAdmit(u, n, ward)
	}
	// slot 0 is null [01 §6.1] — a live unit never has Handle 0; the assist
	// legs cannot address such a guard, so it falls straight to maintenance.
	if u.Handle == 0 {
		return guardFollowMaintenance(u, n, ward, tick)
	}
	// (a) build assist — if ward has active construction op that is friendly per diplomacy byte and not already latched [04 §3.5]
	if wardHasConstruction(ward) && isFriendlyConstruction(u, ward) {
		wardH := ward.Handle
		if wardH == 0 {
			wardH = n.Target
		}
		arr := &u.GuardLatches.BuildAssist
		if !isInDedupArr(arr, wardH) {
			q := QueueForUnit(u)
			helpID := Lookup("HelpBuild")
			if u.Def != nil && u.Def.CanFly {
				helpID = Lookup("VTOL_HelpBuild")
				if helpID == 0 {
					helpID = Lookup("HelpBuild")
				}
			}
			if helpID != 0 {
				q.Push(helpID, Node{Target: n.Target, GoalX: ward.X, GoalY: ward.Y, GoalZ: ward.Z})
			}
			pushDedupArr(arr, wardH)
			// The retry deadline is set by result code 3, so the handler needs no
			// tick [04 §3.3]. Unimplemented: [04 R-UNIT-06 §1] corrects the
			// dedup array away — "there is **no** dedup array and no latch in
			// either guard handler", re-enqueue discipline coming from the pump's
			// deadline cadence and the satisfied-bit gates — and makes this leg
			// the ward's-order join of item 4, not a bare help-build. Retiring
			// the latches is the same change as legs 1 and 2 below; see
			// PLAN 19 §2.3.
			return Code(3)
		}
	}
	// Unimplemented: leg 2 of [04 R-UNIT-06 §1] is not an acquisition at all.
	// Gated on the guard's standing-fire field alone ([04 R-STANCE-01 §3]
	// corrects §1's mention of the standing-move field), it walks slots 0..2
	// requiring the slot's assigned bit, its tracking bit, and a weapon whose
	// `commandfire` definition bit is clear, and REBINDS a slot with no target,
	// an out-of-range target, or a target in the slot's bad-target category
	// array onto the ward's engagement target. Slots already holding a legal
	// in-range target are untouched. It shares the ward's engagement-target
	// reference with leg 1, so it lands with the TODO(T25) below; the empty
	// three-slot loop that stood here is gone — see PLAN 19 §2.3.
	// (c) repair assist — when ward is damaged and guard can repair [04 §3.5]
	if wardIsDamaged(ward) && canRepairGuard(u) {
		wardH := ward.Handle
		if wardH == 0 {
			wardH = n.Target
		}
		arr := &u.GuardLatches.Repair
		if !isInDedupArr(arr, wardH) {
			q := QueueForUnit(u)
			repName := "RepairUnit"
			if u.Def != nil && u.Def.CanFly {
				repName = "VTOL_RepairUnit"
			}
			repID := Lookup(repName)
			if repID != 0 {
				q.Push(repID, Node{Target: n.Target, GoalX: ward.X, GoalY: ward.Y, GoalZ: ward.Z})
			}
			pushDedupArr(arr, wardH)

			return Code(3)
		}
	}
	// (d) join the ward's build — when ward's own front order is a nanolathe-class build elsewhere [04 §3.5]
	if wardHasBuildOrder(ward) {
		wardH := ward.Handle
		if wardH == 0 {
			wardH = n.Target
		}
		arr := &u.GuardLatches.HelpBuild
		if !isInDedupArr(arr, wardH) {
			q := QueueForUnit(u)
			var tgt pool.Handle
			if wq := QueueForUnit(ward); wq != nil && len(wq.primary) > 0 {
				tgt = wq.primary[0].Target
			}
			helpID := Lookup("HelpBuild")
			if u.Def != nil && u.Def.CanFly {
				helpID = Lookup("VTOL_HelpBuild")
				if helpID == 0 {
					helpID = Lookup("HelpBuild")
				}
			}
			if helpID != 0 {
				q.Push(helpID, Node{Target: tgt, GoalX: ward.X, GoalY: ward.Y, GoalZ: ward.Z})
			}
			pushDedupArr(arr, wardH)

			return Code(3)
		}
	}
	// Leg 5, the follow maintenance, on every visit that falls through the
	// legs above [04 R-UNIT-06 §1][04 R-ORD-01 §8 point 3].
	return guardFollowMaintenance(u, n, ward, tick)
}

// guardAdmit is the ground guard's phase 0 [04 R-ORD-01 §8 points 1 and 2],
// which is [04 R-UNIT-06 §1]'s admit phase:
//
//	Clear all three weapon-slot build targets (the weapon-clear walk with its
//	TargetCleared signals); write p1 = (FootPrintX(me) + FootPrintX(ward) + 2)
//	· 16; draw ONE RNG(65536) for the anchor direction and store
//	(−sin(h)·r, 0, −cos(h)·r) in the record's goal triple as an offset from the
//	ward, with r the same radius promoted to 16.16; advance.
//
// The draw is the unit's own queue-bound simulation stream and happens exactly
// once per guard record: the maintenance leg draws nothing (I4). p1 is
// overwritten unconditionally — whatever the issuer stored in it survives only
// until this phase runs, and there is no zero case to fall back from.
//
// The stored triple is an OFFSET, not a position. Every reader of a guard
// record's goal — the order-line overlay, a save, the movement side's own goal
// derivation — sees the offset and must add the ward's position to it
// [04 R-ORD-01 §8 point 2]; GuardFollowPoint is that sum.
func guardAdmit(u *units.Unit, n *Node, ward *units.Unit) Code {
	clearWeaponBuildTargets(u)
	radius := guardFollowRadius(u, ward)
	n.Param1 = uint32(radius)
	h := numeric.Angle(uint16(drawBelow(u, 0x10000)))
	r := int32(radius) << 16 // the radius promoted to 16.16 for the multiply
	n.GoalX = -numeric.Fixed(numeric.MulRound(numeric.Sin(h), r))
	n.GoalY = 0
	n.GoalZ = -numeric.Fixed(numeric.MulRound(numeric.Cos(h), r))
	return Code(1) // *advance* — the same pump cascade re-enters phase 1
}

// guardFollowMaintenance is leg 5 of [04 R-UNIT-06 §1] as closed by
// [04 R-ORD-01 §8 points 3 and 4]:
//
//	Install a POINT goal at `ward + storedOffset` with radius p1 / 2; set the
//	deadline to tick + 30 (fixed, no draw) through the shared setter, which
//	also ORs gate bit 0x01; OR 0x18 into the dynamic gate; return hold (2) with
//	the phase left at 1.
//
// The gate on the way out is 0x19 — the pump wipes the gate before dispatch,
// so the OR always lands on an empty word. Arrival (`0x20`) and path failure
// (`0x40`) are deliberately NOT gated: the follow goal is re-issued on a fixed
// 30-tick period whether or not the guard has arrived, each re-issue releasing
// the previous payload and starting a fresh path request, and a failed path
// never ends the guard.
//
// TODO(T25): [04 R-UNIT-06 §1]'s legs 1 and 2 — the combat join and the
// per-slot re-target onto the ward's engagement target — are not implemented,
// because the ward's engagement-target link has no located producer (that
// section records the link's writer as Unknown), so the condition both legs
// open on cannot be evaluated. The legs above are this build's earlier
// approximation of legs 3 and 4 and predate the section's correction; WU-19-6
// owns the radius, the goal shape and the cadence only. Every visit that falls
// through reaches this leg.
func guardFollowMaintenance(u *units.Unit, n *Node, ward *units.Unit, tick uint32) Code {
	x, y, z, radius := GuardFollowPoint(n, ward.X, ward.Y, ward.Z)
	// The payload form is used rather than installPointGoal so the record's
	// goal triple keeps the offset [04 R-ORD-01 §8 point 2]. An air guard takes
	// the installer's canfly arm, which is release-only.
	//
	// TODO(T25): the air twin's own maintenance is airspace circling with a
	// `0x80` arrival radius [04 R-UNIT-06 §1]; that marker family belongs to
	// internal/movement's air goals and is outside this unit, so a `VTOL_Follow`
	// record installs nothing here and keeps only its cadence.
	installPointGoalPayload(u, n, x, y, z, radius)
	armDeadline(n, tick, 30) // fixed 30, no draw; the setter ORs gate bit 0x01
	n.DynamicGate |= 0x18    // the two re-arm bits [04 R-ORD-01 §8 point 4]
	return Code(2)           // *hold*, phase left at 1
}

func unitCanFly(u *units.Unit) bool {
	return u != nil && u.Def != nil && u.Def.CanFly
}

// ---------------------------------------------------------------------------
// Registration [PLAN_06 WU-06-4] additive registration onto existing table's descriptors
// ---------------------------------------------------------------------------

func init() {
	if len(Table()) == 0 {
		return
	}
	registerChaseGuardHandlers()
	if len(table) != 0 {
		if id := Lookup("Attack_Chase"); id != 0 && table[int(id)].Handler != nil {
			handlersRegistered = true
		}
	}
}

// EnsureHandlers forces handler registration for fixtures where init ordering placed this file before table.go.
func EnsureHandlers() { ensureHandlers() }

func registerChaseGuardHandlers() {
	if id := Lookup("Attack_Chase"); id != 0 {
		table[int(id)].Handler = attackChaseHandler
	}
	if id := Lookup("Follow_Ground"); id != 0 {
		table[int(id)].Handler = guardHandler
	}
	if id := Lookup("VTOL_Follow"); id != 0 {
		table[int(id)].Handler = guardHandler
	}
	// `Guard_NoMove` used to be assigned guardHandler here. It is a different
	// order, not a follow variant: it installs no goal and never moves
	// [04 R-ORD-01 §8 point 5]. combat.go's installer owns it now.
}
