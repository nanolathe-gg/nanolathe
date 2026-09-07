// Command resolution [04 §3.4][04 R-ORD-02 §1]: the capability predicates and
// the code-to-descriptor resolvers the interface, the AI and COB all enter
// through.

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

// signExtendedHealth is a unit's health the way the repair admission reads it:
// the 16-bit health field sign-extended to 32 bits [04 R-ORD-02 §7]. Our own
// health word is wider, so the narrowing is explicit — it is what makes a
// death-latched target's negative health stay negative under nano-reach's
// signed inequality and become a very large value under code 2's unsigned
// compare.
//
// `health16` in vtolwork.go is the same field ZERO-extended. The two readings
// separate only where `maxdamage` itself is 0x8000 or more: below that, a
// health whose 16-bit pattern has the high bit set reads as a large value under
// either, and every other value reads identically. `maxdamage` is a 32-bit
// authored word [02 "Unit record"], so nothing in the format forbids such a
// definition — but none exists in the reference install (the largest is 29918),
// so the two readings agree on all of it. §7's wording is the sign-extended
// one, so that is what this admission uses; `health16` stays for the other
// sites that quote their own compare as unsigned.
func signExtendedHealth(u *units.Unit) int32 {
	return int32(int16(u.Health))
}

// repairAdmitsCode2 is code 2's arm of the repair admission: nano-reach AND a
// second, stricter compare that codes 1 and 8 do not make — the same
// sign-extended health, taken as UNSIGNED, is below `maxdamage` taken as
// unsigned [04 R-ORD-02 §7]. That excludes the two targets nano-reach's plain
// inequality admits: an over-full one, and a death-latched one whose health has
// already gone to zero or below. So a move-click on a latched friendly is a
// move, while a code-8 request on the same unit is a repair order that the next
// slot visit finds dead.
func repairAdmitsCode2(actor, target *units.Unit) bool {
	if !nanoReach(actor, target) {
		return false
	}
	return uint32(signExtendedHealth(target)) < uint32(target.Def.MaxDamage)
}

// nanoReach is THE repair admission of [04 R-ORD-01 §7] — the one function
// [04 R-ORD-02 §7] establishes the command resolver's codes 1, 2 and 8,
// `VTOL_RepairUnit` phase 0 and the repair-patrol scan all call. It was two
// copies here (this one and `repairAdmission` in vtolwork.go) until they were
// collapsed; the copies differed only in reading the health field zero- rather
// than sign-extended, which no authored `maxdamage` can tell apart.
//
// [04 R-ORD-02 §1] names it *nano-reach*. Its terms, in the order that section
// gives them:
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
	// The health term stands, and it belongs to this admission alone. Codes 1,
	// 2 and 8 call this one function — not an inlined copy — and only code 2
	// adds a test of its own on top of it [04 R-ORD-02 §7]. §1's old
	// parentheticals ("no health test — a full-health friendly resolves to a
	// repair") meant "no health test beyond nano-reach's own" and are reworded
	// there; a full-health friendly fails HERE and the click falls through to
	// the later arms.
	//
	// The compare is an INEQUALITY between the target's 16-bit health field,
	// sign-extended to 32 bits, and the definition's 32-bit `maxdamage` word.
	// An over-full target and a death-latched one (health zero or negative
	// between the lethal packet and its own slot visit, [06 R-DMG-01 §3]
	// steps 1–2) therefore both pass here; code 2 is where they are excluded
	// again [04 R-ORD-02 §7].
	if signExtendedHealth(target) == target.Def.MaxDamage {
		return false
	}
	if moverMode(target) == 2 { // airborne [04 R-MOV-01 §8]
		return false
	}
	return nanoReachWaterClause(actor, target)
}

// nanoReachWaterClause is the water half of nano-reach, split out so the
// unbound-binding arm is visible at its own site:
//
//	(not canfly(me) or amphibious(me) or seaLevel <= targetTop) and
//	(canfly(me)     or seaLevel − MaxWaterDepth(me) <= targetTop)
//
// Both halves are written literally, because the disjuncts are not exclusive
// and collapsing them by case has already gone wrong once.
//
// A binding with no world adapter passes the clause rather than failing it: an
// unbound queue is a test or bootstrap arrangement, not a submerged target, and
// failing there would abandon repairs retail completes. (Reading the missing
// level as 0 instead would refuse a target whose top is below zero, which is a
// different invention.)
func nanoReachWaterClause(actor, target *units.Unit) bool {
	b := bindingOfUnit(actor)
	if b == nil || b.World == nil || b.World.SeaLevel == nil {
		return true
	}
	// The terrain header's sea level is a byte in whole world units [04 §10.2].
	sea := int32(b.World.SeaLevel())
	targetTop := int32(target.Y.Floor()) + target.Def.ModelTop
	airHalf := !actor.Def.CanFly || actor.Def.Amphibious || sea <= targetTop
	wadeHalf := actor.Def.CanFly || sea-actor.Def.MaxWaterDepth <= targetTop
	return airHalf && wadeHalf
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
		// `HelpBuild` or air twin, else `RepairUnit` or air twin — again no
		// health test beyond nano-reach's own" [04 R-ORD-02 §1], confirmed by
		// trace in [04 R-ORD-02 §7]: code 8 calls the shared admission and adds
		// nothing, which is why a full-health finished friendly rejects at the
		// gate rather than resolving a repair with nothing to repair, and why a
		// death-latched one — which code 2's stricter compare excludes — is
		// accepted here into a repair order the next slot visit finds dead.
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

// Interface-type constants for the contextual code's two traced variants.
//
// Retail keeps one dword for this. The interface options page's `LEFTCLICK`
// two-stage button (`Left Click|Right Click`) writes it, the chat command
// `+IFace n` writes it, and it is persisted as the registry value
// `Interface Type` whose absence reads `0`; the same word gates the frame
// handler's click dispatch and the world-click cursor resolver
// [07 R-CAM-01 §5][07 §8]. It is therefore a settable single-player option,
// not a build-time constant — which is why the `1` variant below stays.
const (
	// InterfaceTypeLeftClick is retail's default (`Left Click`): the left
	// button issues every world order and the contextual code runs the default
	// variant of [04 R-ORD-02 §1] code 1.
	InterfaceTypeLeftClick = 0
	// InterfaceTypeRightClick is the `Right Click` polarity, under which the
	// right button issues orders and the contextual code runs the `1` variant.
	InterfaceTypeRightClick = 1
)

// interfaceType is the live value of that option. It starts at the registry
// default and nothing in this build writes it yet: `internal/settings` carries
// no `Interface Type` field, so the option page cannot produce the other value.
//
// TODO(T23): bind the settings load/save and interface options control to
// this shared word; until then retain the researched registry default. The
// source is the registry word named above — it must not become a
// build flag or a per-call parameter, because retail reads one word for both
// the click dispatch and the cursor resolver [07 R-CAM-01 §5].
var interfaceType = InterfaceTypeLeftClick

// InterfaceType reports the current value of the option [07 R-CAM-01 §5].
func InterfaceType() int { return interfaceType }

// resolveContextual is command code 1. It has two traced variants selected by
// the `Interface Type` option [04 R-ORD-02 §1][07 R-CAM-01 §5]; retail's
// default, and this build's only reachable value, is `0`.
func resolveContextual(actor *units.Unit, target *units.Unit, pos *ResolvePos) string {
	if interfaceType == InterfaceTypeRightClick {
		return resolveContextualRightClick(actor, target, pos)
	}
	return resolveContextualLeftClick(actor, target, pos)
}

// resolveContextualLeftClick is the `Interface Type = 0` variant — the default,
// and the one a stock single-player session runs [04 R-ORD-02 §1] code 1:
//
//	(1) `canattack` and hostile → code 3
//	(2) `canreclamate` and hostile → code 12
//	(3) with a target: nano-reach and unfinished → code 8; then the own-unit
//	    reject below
//	(4) `canresurrect` + position + feature → Resurrect
//	(5) `canreclamate` + position + feature → Reclaim or air twin
//	(6) `canmove` and a live mover → move or air twin; else reject
//
// "The default variant therefore never turns a click on a damaged friendly into
// a repair (only an unfinished one into assistance) and never resolves pickup,
// follow, or landing contextually; those need the explicit codes." §7 states the
// same path from the other side: a full-health friendly fails nano-reach and
// "in the default variant it reaches the own-unit reject and then the feature
// and move tests" [04 R-ORD-02 §7].
func resolveContextualLeftClick(actor *units.Unit, target *units.Unit, pos *ResolvePos) string {
	if target != nil && isHostile(actor, target) && canAttack(actor) {
		if name := resolveAttackAt(actor, target, pos); name != "" {
			return name
		}
	}
	// Step 2 resolves the WHOLE of code 12, not a bare `ReclaimUnit`: "resolve
	// as **code 12** (so the feature tests below run first and a hostile unit is
	// reclaimed only when no feature is at the position)" [04 R-ORD-02 §1].
	if target != nil && isHostile(actor, target) && canReclaim(actor) {
		if name := resolveName(12, actor, target, pos); name != "" {
			return name
		}
	}
	if target != nil {
		// Step 3's first half is code 8 restricted to the unfinished target:
		// "nano-reach passes and the target is unfinished → resolve as code 8".
		// Unlike the `1` variant, step 3 as traced carries no separate friendly
		// term — and it needs none, because nano-reach demands the actor's
		// canreclamate mirror bit, which is exactly step 2's gate, so a hostile
		// target has already been consumed by step 2 before this line is
		// reached.
		if nanoReach(actor, target) && isUnfinished(target) {
			if name := resolveName(8, actor, target, pos); name != "" {
				return name
			}
		}
		if rejectsOwnSelectableTarget(actor, target) {
			return ""
		}
	}
	// Steps 4 and 5. The reclaim arm carries its own `canreclamate` gate in both
	// variants ([04 R-ORD-02 §1] code 1 steps 5 and 8); it was missing here, so
	// a unit with no nanolathe at all answered a click on a tree with `Reclaim`.
	if pos != nil && pos.HasFeature {
		if pos.IsWreck && canResurrect(actor) && pos.FeatureResurrectable {
			return "Resurrect"
		}
		if canReclaim(actor) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_Reclaim"
			}
			return "Reclaim"
		}
	}
	return contextualMoveArm(actor)
}

// rejectsOwnSelectableTarget is the default variant's own-unit reject
// [04 R-ORD-02 §1] code 1 step 3: "when the target is **my own** (its owner slot
// byte equals the local slot), selectable (state bit 5), complete, its
// post-capture byte is 0, and it is either not carried or its carrier has state
// bit 30 → **reject** (a click on one's own idle unit is a selection, not an
// order)". The first four terms are the shared eligibility predicate `E(u)` of
// [07 R-WGT-01 §9][07 R-WGT-01 §10], which is why the click path's cursor
// answers `cursorselect` over exactly this target [07 R-CAM-01 §14 step 2].
//
// Two of the clauses need a word about how they are read here.
//
// *The owner term.* The traced word is the **local player's** slot byte, not the
// acting unit's owner. This package has no local-slot source — the order queue's
// binding carries the diplomacy row, not the viewing slot — and every code-1
// issuer in this build acts for the slot that issued it: the battle click path
// issues code 1 only for the local human's own selection, and the skirmish
// planner issues codes 2, 3 and 9, never 1 [08 R-AI-01 §7]. So the actor's owner
// slot IS the local slot at every reachable call site, and the two readings
// cannot be told apart here. Note: if a second local-slot notion ever
// appears — a spectator view, or a code-1 issuer acting for another slot — the
// order package needs the session's local slot on the queue binding rather than
// this equivalence.
//
// *The post-capture byte.* [04 R-ORD-02 §1] records its decrement site as
// **Unknown** and directs that "until then a reimplementation treats it as
// always 0"; [08 R-TRIG-01 §3] adds that the counter is armed only by a capture
// whose new owner is a remote controller, so in single-player it is always zero.
// There is no field to read and no term to write.
func rejectsOwnSelectableTarget(actor, target *units.Unit) bool {
	if actor == nil || target == nil {
		return false
	}
	if target.Owner != actor.Owner {
		return false
	}
	// Selectable (state bit 5) and complete — `E(u)`'s first two clauses.
	if target.Flags&units.ClassifierEligibleStatus == 0 || target.Remaining != 0 {
		return false
	}
	// "either not carried or its carrier has state bit 30". The bit is a static
	// mirror of the carrier definition's `isairbase` flag [04 R-UNIT-06 §3], so
	// cargo aboard an ordinary transport is not a selection and the click stays
	// an order; cargo attached to an airbase is.
	if target.Attachment.Carrier != 0 {
		carrier := lookupUnitFor(actor, target.Attachment.Carrier)
		if carrier == nil || carrier.Flags&units.CargoSelectableStatus == 0 {
			return false
		}
	}
	return true
}

// lookupUnitFor resolves a handle through the acting unit's queue binding, the
// package's only unit-table access [P0-I16].
func lookupUnitFor(actor *units.Unit, h pool.Handle) *units.Unit {
	b := bindingOfUnit(actor)
	if b == nil || b.Lookup == nil {
		return nil
	}
	return b.Lookup(h)
}

// contextualMoveArm is the tail both variants share: "`canmove` **and a live
// mover**, else reject" [04 R-ORD-02 §1]. There is no queued-move arm in code 1,
// so a contextual click with an immobile builder selected resolves to nothing —
// a factory rally is set with the explicit move command (code 2), not
// contextually.
func contextualMoveArm(actor *units.Unit) string {
	if !canMove(actor) || !hasLiveMover(actor) {
		return ""
	}
	if actor.Def != nil && actor.Def.CanFly {
		return "VTOL_Move"
	}
	return "Move_Ground"
}

// resolveContextualRightClick is the `Interface Type = 1` variant
// [04 R-ORD-02 §1] code 1. It is unreachable until the interface options page
// can write the option (see interfaceType), but it is a traced retail path a
// single-player session can select, so it is kept rather than deleted.
func resolveContextualRightClick(actor *units.Unit, target *units.Unit, pos *ResolvePos) string {
	// Hostile and able to attack becomes an attack order [04 §3.4] code 1
	if target != nil && isHostile(actor, target) && canAttack(actor) {
		name := resolveAttackAt(actor, target, pos)
		if name != "" {
			return name
		}
	}
	// "`canreclamate` and hostile → `ReclaimUnit` or air twin" — step 2 of this
	// variant is the unit reclaim itself, not a re-entry into code 12: the
	// feature tests come after, which is the whole difference the default
	// variant's "resolve as code 12" note points at [04 R-ORD-02 §1].
	if target != nil && isHostile(actor, target) && canReclaim(actor) {
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_ReclaimUnit"
		}
		return "ReclaimUnit"
	}
	// "friendly and nano-reach passes: target unfinished → `HelpBuild` or air
	// twin, else `RepairUnit` or air twin" [04 R-ORD-02 §1] code 1 step 3. The
	// arm used to be gated on `isDamaged(target) || isUnfinished(target)` with
	// no admission at all, so any unit at all — a tank with no nanolathe —
	// resolved a repair on a scratched friendly and then stood there.
	//
	// Nano-reach alone, as traced: this variant's step 3 adds no health test of
	// its own, so a full-health friendly falls out of the arm into the landing,
	// pickup, follow and move tests below [04 R-ORD-02 §7].
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
	// "(4) I am `canfly`, friendly, and the target has `isairbase` →
	// `VTOL_Landing`" [04 R-ORD-02 §1]. The arm was missing, so a flyer's
	// contextual click on its own pad was picked up or followed instead of
	// landed.
	if target != nil && actor.Def != nil && actor.Def.CanFly && !isHostile(actor, target) && isLandingPad(target) {
		return "VTOL_Landing"
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
	// Steps 7 and 8, each carrying its own capability gate [04 R-ORD-02 §1].
	if pos != nil && pos.HasFeature {
		if pos.IsWreck && canResurrect(actor) && pos.FeatureResurrectable {
			return "Resurrect"
		}
		if canReclaim(actor) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_Reclaim"
			}
			return "Reclaim"
		}
	}
	return contextualMoveArm(actor)
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
		// `HelpBuild` or air twin; friendly and nano-reach passes and the
		// target's 16-bit health is below its `maxdamage` (unsigned) →
		// `RepairUnit` or air twin" [04 R-ORD-02 §1] code 2, in that order.
		//
		// The repair arm's second compare is code 2's alone — codes 1 and 8 add
		// nothing to nano-reach [04 R-ORD-02 §7] — and it is what makes a
		// move-click on a death-latched friendly stay a move.
		if !isHostile(actor, target) && nanoReach(actor, target) && isUnfinished(target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_HelpBuild"
			}
			return "HelpBuild"
		}
		if !isHostile(actor, target) && repairAdmitsCode2(actor, target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_RepairUnit"
			}
			return "RepairUnit"
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

// slotZeroIsAntiAir is code 3's *w0* term: "*w0* be my weapon slot 0's weapon
// definition" [04 R-ORD-02 §1], read for its `toairweapon` flag. The runtime
// slot is what the resolver reads, not the definition's authored `weapon1`, so
// a slot whose weapon link never resolved has no flag to offer.
//
// Only the not-hostile arm's reject uses it. The hostile arm's own target-class
// rejects (the airborne/`toairweapon` pair, the submerged `waterweapon` tests
// and the hovercraft clauses of [04 R-ORD-02 §1]) have no implementation in
// this build; that gap predates this unit and is not narrowed here.
func slotZeroIsAntiAir(u *units.Unit) bool {
	if u == nil {
		return false
	}
	s := u.SlotAt(0)
	return s != nil && s.Weapon != nil && s.Weapon.ToAirWeapon
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
		// The not-hostile arm of code 3, whose condition [04 R-ORD-02 §1] gives
		// as "friendly OR NO TARGET": "*w0* has `toairweapon` → reject; I am not
		// `canfly` → `Suppress`; else *W1* has `dropped` → `AirStrike`, else
		// `AirToGround`."
		//
		// Corrected (WU-19-130): this arm was additionally gated on a non-nil
		// position, which is nowhere in the traced condition. The gate made
		// `AttackSpecial` — whose whole body resolves code 3 against the
		// record's target WITH NO POSITION [04 R-ORD-01 §2] — reject whenever
		// the D-gun was aimed at bare ground, so the record re-identified as
		// descriptor 0 and completed silently [04 R-ORD-01 §12]: a manual D-gun
		// on the ground did nothing at all. It also left `Suppress` phase 1's
		// `p1 = 2` arm — release all slots, bind slot 2 to the goal
		// [04 R-ORD-01 §3] — unreachable, since `AttackSpecial` is the only
		// writer of that parameter.
		if actor.Flags&units.ArmedStatus != 0 {
			if slotZeroIsAntiAir(actor) {
				return ""
			}
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
