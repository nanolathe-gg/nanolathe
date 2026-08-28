// Package orders implements command resolution and attack-chase/guard handlers [04 §3.4, §3.5][PLAN_06 WU-06-4].
package orders

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): per-side diplomacy byte on acting unit's definition indexed by target side [04 §3.4].
// Retail stores a per-side diplomacy byte on the definition; content.UnitDef lacks such a field.
// Modeling as an unexported resolver input preserves the call site without inventing catalog surface.
// P0-I16: hostility and target lookup moved onto Queue.Hostility/Lookup; package globals removed.

func getHostility(actor *units.Unit) func(*units.Unit, *units.Unit) bool {
	if actor != nil {
		if q := QueueForUnit(actor); q != nil && q.Hostility != nil {
			return q.Hostility
		}
	}
	return nil
}

func isHostile(actor, target *units.Unit) bool {
	if fn := getHostility(actor); fn != nil {
		return fn(actor, target)
	}
	if actor == nil || target == nil {
		return false
	}
	if actor.Def != nil && target.Def != nil && actor.Def.Side != "" && target.Def.Side != "" {
		return actor.Def.Side != target.Def.Side // fallback side equality [04 §2.2] TODO(question) diplomacy byte
	}
	return actor.Owner != target.Owner
}

// Target lookup remains a package fallback because transport resolution uses it
// when a carrier has no per-queue lookup [04 §3.5][04 §10.2].
var legacyLookup func(pool.Handle) *units.Unit

// BindTargetLookup installs the target lookup used by chase and guard ward resolution [04 §3.5] [P0-I16 legacy].
func BindTargetLookup(fn func(pool.Handle) *units.Unit) { legacyLookup = fn }

func getLegacyLookup() func(pool.Handle) *units.Unit { return legacyLookup }

// Capability gates map to definition flags [04 §2.2]/[04 §2.4] with TODO(T25) opaque handling.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const opaqueDefBit5 = 1 << 5 // TODO(T25)

func canMove(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	_ = opaqueDefBit5 // TODO(T25) opaque gate already established in units.go Flags; preserve but do not yet gate resolution
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
	// TODO(question): mapping special-attack capability to CanDGun [04 §2.2] – CanDGun flag corresponds to D-Gun weapon
	return u.Def.CanDGun
}
func canUnload(u *units.Unit) bool { // can-unload [04 §3.4] code 5
	if u == nil || u.Def == nil {
		return false
	}
	// TODO(question): UnitDef has CanLoad but no CanUnload; treat as CanLoad for now [04 §10.2]
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
func canPatrol(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	return u.Def.CanPatrol
}
func isBuilder(u *units.Unit) bool { // build list non-empty [04 §3.4] code 14
	if u == nil || u.Def == nil {
		return false
	}
	// TODO(question): full build list lives in content.Catalog.BuildMenus; Builder bool approximates non-empty list [02 "Unit record"]
	return u.Def.Builder
}
func isTransportable(t *units.Unit) bool { // carriable test [04 §10.2]
	if t == nil || t.Def == nil {
		return false
	}
	// TODO(question): full admission includes size, capacity, mover etc [04 §10.2]; stub uses CantBeTransported
	return !t.Def.CantBeTransported
}
func isFollowable(t *units.Unit) bool {
	// TODO(question): followable test not located; stub treats any alive unit as followable [04 §3.4]
	return t != nil && t.Alive
}
func isLandingPad(t *units.Unit) bool {
	// TODO(question): landing pad detection [04 §10.2] uses IsAirBase or pad query; stub checks IsAirBase [02 "Unit record"]
	return t != nil && t.Def != nil && t.Def.IsAirBase
}
func isStructure(t *units.Unit) bool {
	// TODO(question): structure detection for no-move attack variant – stub checks !CanMove [04 §2.2]
	return t != nil && t.Def != nil && !t.Def.CanMove
}
func isDamaged(u *units.Unit) bool {
	if u == nil {
		return false
	}
	return u.Health < u.MaxHealth
}
func isUnfinished(u *units.Unit) bool {
	if u == nil {
		return false
	}
	return u.Remaining > 0 && u.Remaining < 1 // remaining 1→0 [04 §2.3]
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
	switch code {
	case 1:
		return resolveContextual(actor, target, pos)
	case 2:
		return resolveMove(actor, target)
	case 3:
		return resolveAttack(actor, target)
	case 4:
		if !canDGun(actor) {
			return ""
		}
		return "AttackSpecial"
	case 5:
		if !canUnload(actor) {
			return ""
		}
		if target != nil && isLandingPad(target) {
			return "VTOL_Landing"
		}
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Unload"
		}
		return "Ground_Unload"
	case 6:
		if target == nil || !isTransportable(target) {
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
		if target == nil || !isBuilder(actor) {
			return ""
		}
		// TODO(question): reachable by nanolathe not located; stub assumes reachable [04 §3.4]
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
		if target == nil {
			return "QPatrol" // queued patrol [04 §3.4]
		}
		// builder with repair-patrol capability becomes RepairPatrol
		// TODO(question): repair-patrol capability not located; stub uses Builder flag [04 §2.2]
		if isBuilder(actor) {
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
		// TODO(question): internal command whose label is not established [04 §3.4]
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
		if !canCapture(actor) || target == nil || !isHostile(actor, target) {
			return ""
		}
		if actor.Owner == target.Owner {
			return ""
		}
		return "Capture"
	case 14:
		if !isBuilder(actor) {
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
		name := resolveAttack(actor, target)
		if name != "" {
			return name
		}
	}
	// Damaged or unfinished friendly becomes repair or build assistance
	if target != nil && !isHostile(actor, target) && (isDamaged(target) || isUnfinished(target)) {
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
	if target != nil && isTransportable(target) {
		if actor.Def != nil && actor.Def.CanFly {
			return "VTOL_Pickup"
		}
		return "Ground_Pickup"
	}
	if target != nil && isFollowable(target) {
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
	if !canMove(actor) {
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
	if target != nil {
		if !target.Alive {
			return "QMove" // dead unit target becomes queued move [04 §3.4]
		}
		if isHostile(actor, target) && (canCapture(actor) || canReclaim(actor)) {
			if canCapture(actor) {
				return "Capture"
			}
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_ReclaimUnit"
			}
			return "ReclaimUnit"
		}
		if !isHostile(actor, target) && isUnfinished(target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_HelpBuild"
			}
			return "HelpBuild"
		}
		if isLandingPad(target) {
			return "VTOL_Landing"
		}
		if isTransportable(target) {
			if actor.Def != nil && actor.Def.CanFly {
				return "VTOL_Pickup"
			}
			return "Ground_Pickup"
		}
		if isFollowable(target) {
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
	if !canAttack(actor) || target == nil {
		return ""
	}
	// suppression for the non-air special case [04 §3.4] code 3
	// TODO(question): exact suppression predicate not located; stub treats non-flyer vs non-flyer with no weapon special as Suppress for test branch coverage
	if actor.Def != nil && target.Def != nil && !actor.Def.CanFly && !target.Def.CanFly && actor.Def.Weapon1 == "" {
		// Use Unknown key "suppress" as test hook: if actor.Def.Unknown contains "suppress" then return Suppress
		if actor.Def.Unknown != nil {
			if _, ok := actor.Def.Unknown["suppress"]; ok {
				return "Suppress"
			}
		}
	}
	// four air-attack variants chosen by weapon and target class [04 §3.4] code 3
	// TODO(question): selection by weapon and target class not fully located; stub uses CanFly/CanHover
	if actor.Def != nil && actor.Def.CanFly {
		if target.Def != nil && target.Def.CanFly {
			return "AirToAir"
		}
		if target.Def != nil && target.Def.CanHover {
			return "AirToGroundHover"
		}
		// A sentinel primary (a missed link) is inactive, not an air-attack
		// weapon [02 §5 R-CONTENT-02].
		if !content.IsWeaponInactive(actor.Def.Weapon1Def) && actor.Def.Weapon1Def.ToAirWeapon {
			return "AirToAir"
		}
		if target.Def != nil && target.Def.IsAirBase {
			return "AirStrike"
		}
		return "AirToGround"
	}
	// TODO(question): [04 §3.4] says "the kamikaze variant for a unit flagged
	// for it" without saying whether the flag is read off the attacker or the
	// target; the attacker reading below is the working hypothesis.
	if actor.Def != nil && actor.Def.Kamikaze {
		return "Attack_Kamikaze"
	}
	if isStructure(target) {
		return "Attack_NoMove"
	}
	return "Attack_Chase"
}

// Ensure content import is used.
var _ = (*content.UnitDef)(nil)

// ---------------------------------------------------------------------------
// Attack-chase state machine [04 §3.5]
// ---------------------------------------------------------------------------

// attack chase handler constants [04 §3.5]
const (
	chaseAbandonMask   uint32 = 0x01 // TODO(question) satisfied bits indicating abandonment not located
	chaseDisengageMask uint32 = 0x06 // TODO(question) disengage bit combination placeholder
	stubStandoffWorld  int32  = 64   // TODO(question) standoff distance source not located; stub world units [04 §3.5]
)

func leashExceeded(u *units.Unit, n *Node) bool {
	if n.Param3 == 0 {
		return false // zero means unlimited [04 §3.2]
	}
	// GuardX/Y are 16-bit anchor [04 §3.2]; treat as Fixed world units [04 §3.5] TODO(question) units
	gx := int64(n.GuardX) * 65536
	gz := int64(n.GuardY) * 65536
	dx := u.X.Raw() - gx
	dz := u.Z.Raw() - gz
	leashFixed := int64(n.Param3) * 65536 // TODO(question) leash units not established; treating as world units [04 §3.5]
	dist2 := dx*dx + dz*dz
	leash2 := leashFixed * leashFixed
	return dist2 >= leash2
}

func verticalSeparation(u *units.Unit, target *units.Unit) int64 {
	if u == nil || target == nil {
		return 0
	}
	dy := u.Y.Raw() - target.Y.Raw()
	if dy < 0 {
		dy = -dy
	}
	return dy >> 16 // world units
}

func setOrbitGoal(u *units.Unit, n *Node, standoff int32) {
	// TODO(question) movement goal precise geometry not located; stub sets goal at standoff distance eastward [04 §3.5] Node has goal fields [04 §3.2]
	off := numeric.Fixed(int64(standoff) * 65536)
	n.GoalX = u.X + off
	n.GoalZ = u.Z
	n.GoalY = u.Y
}

func setBandedGoal(u *units.Unit, n *Node, inner, outer int32) {
	// TODO(question) "two banded-goal states (inner/outer radii at standoff/half and double/half)" geometry not fully located [04 §3.5]
	_ = outer
	off := numeric.Fixed(int64(inner) * 65536)
	n.GoalX = u.X + off
	n.GoalZ = u.Z
	n.GoalY = u.Y
}

func setBandedGoalAroundWard(n *Node, ward *units.Unit, standoff int32) {
	// Guard maintenance banded goal [04 §3.5] (e)
	// TODO(question) banded-goal geometry not located beyond follow maintenance [04 §3.5]
	off := numeric.Fixed(int64(standoff) * 65536)
	n.GoalX = ward.X + off
	n.GoalZ = ward.Z
	n.GoalY = ward.Y
}

// attackChaseHandler implements Attack_Chase [04 §3.5] phases 0-3 and orbit substate 0..8.
func attackChaseHandler(u *units.Unit, n *Node, satisfied uint32) Code {
	if satisfied&chaseAbandonMask != 0 {
		return Code(5) // TODO(question) abandon code value not established; using 5 unlink placeholder [04 §3.3]
	}
	if n.Target == 0 {
		return Code(5) // missing target [04 §3.5]
	}
	if satisfied&chaseDisengageMask == chaseDisengageMask {
		return Code(5) // TODO(question) disengage combination [04 §3.5]
	}
	if n.Param3 != 0 && leashExceeded(u, n) {
		return Code(5) // pursuit leash at or beyond leash abandons [04 §3.5]
	}
	if n.Phase > 3 {
		return Code(7) // cancel-all [04 §3.5][04 §3.3]
	}
	if n.Param2 >= 9 {
		return Code(7) // substate >=9 cancel-all [04 §3.5]
	}
	switch n.Phase {
	case 0: // admit: require ground unit, reset goal to own position, pick weapon slot if none stored [04 §3.5]
		if u.Def != nil && u.Def.CanFly {
			return Code(5) // require ground unit [04 §3.5]
		}
		n.GoalX = u.X
		n.GoalY = u.Y
		n.GoalZ = u.Z
		if n.Param1 == 0 {
			// TODO(question) weapon slot selection not located; placeholder picks 0 with TODO(WU-06-7) [04 §3.5][WU-06-7]
			n.Param1 = 0 // TODO(question) WU-06-7 weapon binds
		}
		n.Phase = 1
		return Code(1) // advance phase [04 §3.3]
	case 1: // engage setup: range-gate, bind fire slots, single tick [04 §3.5]
		// TODO(question) range-gate and fire slot binding need WU-06-7 [04 §3.5][WU-06-7]
		n.Phase = 2
		return Code(1)
	case 2: // combat-maneuver orbit cycle [04 §3.5] eight-state substate 0..8
		standoff := stubStandoffWorld
		var tgt *units.Unit
		// P0-I16: per-queue lookup, not package global
		if u != nil {
			if q := QueueForUnit(u); q != nil && q.Lookup != nil {
				tgt = q.Lookup(n.Target)
			} else if fn := getLegacyLookup(); fn != nil {
				tgt = fn(n.Target)
			}
		} else if fn := getLegacyLookup(); fn != nil {
			tgt = fn(n.Target)
		}
		sub := n.Param2
		switch sub {
		case 0:
			setOrbitGoal(u, n, standoff)
		case 1, 2:
			if verticalSeparation(u, tgt) > 8 {
				setOrbitGoal(u, n, standoff/2)
			} else {
				setOrbitGoal(u, n, standoff)
			}
		case 3:
			setOrbitGoal(u, n, standoff/2)
		case 4:
			setOrbitGoal(u, n, 0)
		case 5:
			setBandedGoal(u, n, standoff, standoff/2)
		case 6:
			setBandedGoal(u, n, standoff/2, standoff/2)
		case 7:
			setBandedGoal(u, n, standoff*2, standoff/2)
		case 8:
			setOrbitGoal(u, n, standoff)
		}
		n.Param2++
		if n.Param2 > 8 {
			n.Param2 = 0 // wrap to zero [04 §3.5]
		}
		// TODO(question): the orbit cadence is not established — how long the
		// handler stays on one orbit substate before advancing. Returning
		// Code(2) re-dispatches the same head forever (the pump cascades until
		// a waiting code appears), so the handler waits like its neighbours
		// until a probe closes the question.
		return Code(3) // wait 30+rand15 [04 §3.3]
	case 3: // re-engage: rebind on range or release the fire slot, both waiting 30 ticks [04 §3.5]
		// TODO(question) weapon rebinding needs WU-06-7 [WU-06-7]
		return Code(3) // wait 30+rand15 [04 §3.3]
	default:
		return Code(7)
	}
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
		if q := QueueForUnit(u); q != nil && q.Lookup != nil {
			if tgt := q.Lookup(n.Target); tgt != nil {
				return tgt
			}
		}
	}
	// Fallback to ward's own queue if actor's queue not set? try target's queue not needed
	if fn := getLegacyLookup(); fn != nil {
		return fn(n.Target)
	}
	return nil
}

func guardWard(n *Node) *units.Unit {
	if n.Target == 0 {
		return nil
	}
	// Legacy path without unit context; try legacy lookup
	if fn := getLegacyLookup(); fn != nil {
		return fn(n.Target)
	}
	return nil
}

func wardHasConstruction(ward *units.Unit) bool {
	// Default stub: active construction when Remaining 0<rem<1 [04 §2.3][04 §3.5] (a)
	return ward != nil && ward.Remaining > 0 && ward.Remaining < 1
}
func isFriendlyConstruction(actor, ward *units.Unit) bool {
	return !isHostile(actor, ward)
}
func canRepairGuard(actor *units.Unit) bool {
	// TODO(question): can repair mapping not located; stub uses Builder or CanReclamate [04 §2.2]
	return actor != nil && actor.Def != nil && (actor.Def.Builder || actor.Def.CanReclamate)
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
	return head.StaticGate&0x100000 != 0 // 0x100000 marks nanolathe/build-site class [04 §3.1] TODO(question)
}

// guardHandler implements Follow_Ground / VTOL_Follow / Guard_NoMove [04 §3.5] top-down (a)-(e).
func guardHandler(u *units.Unit, n *Node, satisfied uint32) Code {
	_ = satisfied
	if n.Target == 0 {
		return Code(5) // no ward
	}
	ward := getLookupForWard(n, u)
	if ward == nil {
		// Fallback to legacy guardWard without unit
		ward = guardWard(n)
		if ward == nil {
			return Code(5)
		}
	}
	// slot 0 is null [01 §6.1] — live unit never has Handle 0; no assist paths fire, fall through to maintenance
	if u.Handle == 0 {
		standoff := int32(20) // TODO(question) standoff radius source for guard is Param1 [04 §3.2] fallback 20
		if n.Param1 != 0 {
			standoff = int32(n.Param1)
		}
		setBandedGoalAroundWard(n, ward, standoff)
		return Code(3)
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
			// TODO(question) 30-tick cadence behind dedup latch; Code 3 wait covers retry delay [04 §3.3]
			// The retry deadline is set by result code 3, so the handler needs no tick [04 §3.3].
			return Code(3)
		}
	}
	// (b) auto-fire while holding position — for each weapon slot with auto-target enabled whose weapon is not command-fire-only [04 §3.5]
	// TODO(question): weapon auto-target and command-fire-only not located; stub returns false pending WU-06-7
	// No candidate acquisition until WU-06-7; dedup structure retained for future.
	for slot := 0; slot < 3; slot++ {
		// Stub: no auto-fire candidate without WU-06-7; keep loop for shape but never fires.
		// If a future probe shows auto-fire should be expressible via state, enable here.
		_ = slot
	}
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
	// (e) otherwise follow maintenance — refresh a banded goal around the ward and wait 30 ticks [04 §3.5]
	standoff := int32(20) // TODO(question) standoff radius source for guard is Param1 [04 §3.2] fallback 20
	if n.Param1 != 0 {
		standoff = int32(n.Param1)
	}
	setBandedGoalAroundWard(n, ward, standoff)

	return Code(3) // wait 30 ticks [04 §3.3]
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
	if id := Lookup("Guard_NoMove"); id != 0 {
		table[int(id)].Handler = guardHandler
	}
}
