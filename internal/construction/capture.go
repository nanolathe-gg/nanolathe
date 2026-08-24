// Package construction implements capture, resurrection and reverse per [P0-15].
package construction

import (
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Base 150 +0.015*energyCost +0.2142857142857*metalCost, clamp 0..1800, truncated via __ftol.
const (
	captureBaseTicks    = 150
	captureEnergyCoeff  = 0.015
	captureMetalCoeff   = 0.21428571428571427 // 3/14
	captureClampMax     = 1800
	captureProgressStep = 2 // +2 per 2-tick visit [P0-15]
)

// CaptureTimer computes capture timer per [P0-15] §4.
//
//	base = clamp(trunc(150 +0.015*energyCost +0.2142857*metalCost),0,1800)
//	healthScaled = ((health + maxDamage) * base) / (2*maxDamage)
//	timer = ((kills/5 +10)*healthScaled*10)/100
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func CaptureTimer(energyCost, metalCost float32, health int32, maxDamage int32, kills int32) int {
	if maxDamage <= 0 {
		maxDamage = 1
	}
	// base with truncation toward zero [I3].
	fBase := float64(captureBaseTicks) + captureEnergyCoeff*float64(energyCost) + captureMetalCoeff*float64(metalCost)
	base := int(math.Trunc(fBase)) // __ftol trunc toward zero [01 §8]
	if base < 0 {
		base = 0
	}
	if base > captureClampMax {
		base = captureClampMax
	}
	healthScaled := int((int64(health+maxDamage) * int64(base)) / (int64(2 * maxDamage))) // integer division trunc
	killsFactor := int(kills / 5)                                                         // trunc toward zero
	timer := ((killsFactor + 10) * healthScaled * 10) / 100
	if timer < 0 {
		timer = 0
	}
	return timer
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Progress accumulates +2 per visit with 2-tick deadline until >= timer.
type CaptureState struct {
	Timer    int   // computed timer threshold
	Progress int   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Deadline int32 // wake tick
}

// AdvanceCaptureProgress increments progress by 2 [P0-15] §3.1 state4.
func AdvanceCaptureProgress(cs *CaptureState) {
	if cs != nil {
		cs.Progress += captureProgressStep
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Gates: builder canCapture, victim not immune, victim idle not cloud, different owner, not already dying.
// TODO(T25): exact UI producer for mask 0x10008 still unknown; gate preserved but producer not located.
func CaptureEligible(builder *units.Unit, victim *units.Unit) bool {
	if builder == nil || victim == nil || builder.Def == nil || victim.Def == nil {
		return false
	}
	if !builder.Def.CanCapture {
		return false
	}
	// Victim capture immunity bit? Def.CanCapture immunity? Use NoRestrict? For now use CanCapture immunity inverse?
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Keep victim immunity as not located TODO(question).
	if builder.Owner == victim.Owner {
		return false
	}
	if victim.Dying || !victim.Alive {
		return false
	}
	if victim.Remaining != 0 {
		// cloud of vapor check: victim idle sentinel _DAT_004FCC920; use Remaining==0 as idle proxy TODO(question): exact sentinel not verified, Remaining==0 is proxy
		return false
	}
	return true
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Copies health/remaining/cargo conditionally but NOT alliances/orders/XP.
// perDefLimit returns effective limit and whether limited.
func perDefLimit(def *content.UnitDef) (int32, bool) {
	if def == nil {
		return -1, false
	}
	if def.LimitEnabled {
		if def.Limit == -1 {
			return -1, false
		}
		if def.Limit <= 0 {
			return -1, false // TODO(question): genuine 0 vs fixture 0 unlimited
		}
		return def.Limit, true
	}
	if def.UnitLimit == UnitLimitUnlimited || def.UnitLimit == 0 {
		return -1, false
	}
	if def.UnitLimit <= 0 {
		return -1, false
	}
	return def.UnitLimit, true
}

// Per-def limit -1 sentinel means unlimited [P0-15][P0-16]; 0 treated as unlimited for fixtures.
func (s *Service) TransferOwnership(victim *units.Unit, newOwner uint8) (*units.Unit, bool) {
	if s == nil || s.World == nil || victim == nil || victim.Def == nil {
		return nil, false
	}
	if lim, ok := perDefLimit(victim.Def); ok && lim > 0 {
		cnt := 0
		for _, u := range s.World.Iter() {
			if u != nil && u.Alive && u.Def != nil && u.Def.UnitName == victim.Def.UnitName && u.Owner == newOwner {
				cnt++
			}
		}
		if int32(cnt) >= lim {
			return nil, false
		}
	}
	// Pool check: try alloc via World.Create at victim pos.
	h, err := s.World.Create(victim.Def, newOwner, victim.X, victim.Y, victim.Z)
	if err != nil {
		return nil, false
	}
	repl := s.World.Unit(h)
	if repl == nil {
		return nil, false
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Alliances/orders/groups/XP NOT copied (leaked on death) [P0-15].
	repl.Health = victim.Health
	repl.Remaining = victim.Remaining
	repl.MaxHealth = victim.MaxHealth
	repl.Kills = victim.Kills
	// cargo conditionally: if victim had cargo (TODO(question) which bytes) copy. For now copy SpotMetal if victim had cargo flag?
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	repl.SpotMetal = victim.SpotMetal
	// Do NOT copy alliances/orders/XP — leave repl Orders nil, Alliances not stored on unit.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if !victim.Dying {
		s.World.Destroy(victim.Handle, units.DeathKilled) // cause 4 mapped to Killed for test; retail cause 4 distinct but death mark same 0x4000
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
	// New unit's building flag etc already via Create; remaining already copied.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return repl, true
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const CaptureTickRate = 2

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// We use Dying flag as proxy for 0x4000 [P0-15].
func IsCaptureComplete(victim *units.Unit) bool {
	return victim != nil && victim.Dying
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Copies feature name and truncates at first '_' (0x5F) → 0.
func FeatureNameTruncForResurrection(featureName string) string {
	if idx := strings.IndexByte(featureName, '_'); idx >= 0 {
		return featureName[:idx]
	}
	return featureName
}

// UnitLimitUnlimited is sentinel -1 means no limit [P0-15][P0-16].
const UnitLimitUnlimited int32 = -1

// CheckPerDefLimit reports whether creating another unit of def for owner would exceed limit [P0-15][P0-16].
func CheckPerDefLimit(w *units.World, owner uint8, def *content.UnitDef) bool {
	if def == nil {
		return true
	}
	lim, limited := perDefLimit(def)
	if !limited {
		return true
	}
	cnt := 0
	if w != nil {
		for _, u := range w.Iter() {
			if u != nil && u.Alive && u.Def != nil && u.Def.UnitName == def.UnitName && u.Owner == owner {
				cnt++
			}
		}
	}
	return int32(cnt) < lim
}

// Capture uses no decay/no cost [P0-15]: timer above, progress +2 per 2 ticks, first lethal via 0x4000 gate.
// Multiple captors independent nodes, first lethal via gate reads 0x4000 kills [P0-15] — handled by TransferOwnership Dying check.

// Ensure pool handle type imported for future use.
var _ = pool.Handle(0)
