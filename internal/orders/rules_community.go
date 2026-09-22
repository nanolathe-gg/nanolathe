package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// CaptureVeteranLevel applies CP-UD-1 only when the queue's selected
// Community table enables veterancy. The captured unit's own definition and
// stored kills feed the shared combat algorithm.
func (CommunityRules) CaptureVeteranLevel(q CaptureVeteranRequest) uint32 {
	if q.Binding == nil || !q.Binding.Community.Veterancy {
		return StrictRules{}.CaptureVeteranLevel(q)
	}
	return combat.AuthoredVeteranLevel(q.Definition, q.Kills, true)
}

// GuardHomeOption is the Community ground-guard home selection. Its numeric
// order follows the patch's player option: Stay, Cavedog, Scatter.
type GuardHomeOption uint8

const (
	GuardStay GuardHomeOption = iota
	GuardCavedog
	GuardScatter
)

// PatrolWorkOption is the Community repair-patrol selection. Its numeric order
// follows the patch's player option: reclaim only, both, assist only.
type PatrolWorkOption uint8

const (
	PatrolReclaimOnly PatrolWorkOption = iota
	PatrolBoth
	PatrolAssistOnly
)

// BuilderOptions is the six-value per-player Community builder preference.
// Array indices are the unit's standing movement modes: Hold Position (0),
// Maneuver (1), and Roam (2) [04 R-STANCE-01 §2].
type BuilderOptions struct {
	Guard  [3]GuardHomeOption
	Patrol [3]PatrolWorkOption
}

// DefaultBuilderOptions returns the patch defaults: Cavedog for every guard
// stance, Reclaim Only for Hold Position, and Both for Maneuver and Roam.
func DefaultBuilderOptions() BuilderOptions {
	return BuilderOptions{
		Guard:  [3]GuardHomeOption{GuardCavedog, GuardCavedog, GuardCavedog},
		Patrol: [3]PatrolWorkOption{PatrolReclaimOnly, PatrolBoth, PatrolBoth},
	}
}

// GuardHomeRequest is the value needed at ground follow maintenance. OffsetX
// and OffsetZ are the stored random guard anchor, and Spacing is the stored
// follow radius p1 [04 R-ORD-01 §8].
type GuardHomeRequest struct {
	Guard, Ward      *units.Unit
	OffsetX, OffsetZ numeric.Fixed
	Spacing          uint32
}

// PatrolWorkRequest identifies the repair-patrol actor whose standing movement
// mode selects one of the player's three preferences.
type PatrolWorkRequest struct {
	Builder *units.Unit
}

func standingMoveMode(u *units.Unit) uint32 {
	if u == nil {
		return units.StandingFieldMask
	}
	return u.Flags >> units.StandingMoveShift & units.StandingFieldMask
}

func guardOption(u *units.Unit) GuardHomeOption {
	b := bindingFor(u)
	mode := standingMoveMode(u)
	if mode >= 3 {
		return GuardCavedog
	}
	options := DefaultBuilderOptions()
	if b != nil && b.BuilderOptions != nil {
		options = b.BuilderOptions(u.Owner)
	}
	option := options.Guard[mode]
	if option > GuardScatter {
		return GuardCavedog
	}
	return option
}

func patrolOption(u *units.Unit) PatrolWorkOption {
	b := bindingFor(u)
	mode := standingMoveMode(u)
	if mode >= 3 {
		return PatrolBoth
	}
	options := DefaultBuilderOptions()
	if b != nil && b.BuilderOptions != nil {
		options = b.BuilderOptions(u.Owner)
	}
	option := options.Patrol[mode]
	if option > PatrolAssistOnly {
		return PatrolBoth
	}
	return option
}

// replaceFixedWhole replaces only a 16.16 value's signed whole-unit half. The
// patch leaves the stored random anchor's low half untouched.
func replaceFixedWhole(v numeric.Fixed, whole int32) numeric.Fixed {
	raw := uint32(uint16(v.Raw())) | uint32(uint16(whole))<<16
	return numeric.Fixed(int32(raw))
}

// GuardHome applies CP-CON-2 only when this binding's Community table enables
// it. Stay uses unsigned multiply-before-divide 7*spacing/20; Scatter uses the
// full spacing. The sign of each axis follows the unsigned whole-coordinate
// quadrant, with equality on the positive side. Cavedog retains retail's
// random anchor (research/extensions/community-patch-engine.md, CP-CON-2).
func (CommunityRules) GuardHome(req GuardHomeRequest) (numeric.Fixed, numeric.Fixed) {
	b := bindingFor(req.Guard)
	if b == nil || !b.Community.GuardingBuildersHold || req.Guard == nil || req.Ward == nil {
		return req.OffsetX, req.OffsetZ
	}
	option := guardOption(req.Guard)
	if option == GuardCavedog {
		return req.OffsetX, req.OffsetZ
	}
	offset := req.Spacing
	if option == GuardStay {
		offset = 7 * offset / 20
	}
	x, z := int32(offset), int32(offset)
	if uint16(req.Guard.X.Raw()>>16) < uint16(req.Ward.X.Raw()>>16) {
		x = -x
	}
	if uint16(req.Guard.Z.Raw()>>16) < uint16(req.Ward.Z.Raw()>>16) {
		z = -z
	}
	return replaceFixedWhole(req.OffsetX, x), replaceFixedWhole(req.OffsetZ, z)
}

// PatrolWork applies CP-CON-3 only when this binding's Community table enables
// it. The caller owns the exact branch boundaries and consequent RNG/resource
// effects (research/extensions/community-patch-engine.md, CP-CON-3).
func (CommunityRules) PatrolWork(req PatrolWorkRequest) PatrolWorkOption {
	b := bindingFor(req.Builder)
	if b == nil || !b.Community.PatrollingBuilderFilters {
		return PatrolBoth
	}
	return patrolOption(req.Builder)
}

// ScriptAttackSurfaceFire applies CP-WPN-3 only when the selected Community
// table enables weapon target keys, and consults weapon slot 0 only. A tagged
// later slot cannot open the COB script-action ATTACK gate
// [research/extensions/community-patch-engine.md, CP-WPN-3].
func (CommunityRules) ScriptAttackSurfaceFire(q ScriptAttackSurfaceFireRequest) bool {
	if q.Binding == nil || !q.Binding.Community.WeaponTargetKeys || q.Actor == nil {
		return false
	}
	slot := q.Actor.SlotAt(0)
	return slot != nil && slot.Weapon != nil && slot.Weapon.SurfaceFire
}
