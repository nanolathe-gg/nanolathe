package survival

import "fmt"

// TicksPerSecond is the simulation rate every tuning duration is written in.
const TicksPerSecond = 30

// Pace is the setup screen's wave pace (DESIGN_SURVIVAL §9). It scales the
// downtime between waves and the budget growth per wave.
type Pace uint8

const (
	PaceNormal Pace = iota
	PaceRelaxed
	PaceRelentless
)

// ParsePace reads a pace word: normal (or empty), relaxed or relentless.
func ParsePace(word string) (Pace, error) {
	switch word {
	case "", "normal":
		return PaceNormal, nil
	case "relaxed":
		return PaceRelaxed, nil
	case "relentless":
		return PaceRelentless, nil
	}
	return PaceNormal, fmt.Errorf("nanolathe: unknown survival pace %q: logical path <command line>, providers searched [survival-pace], expected normal, relaxed or relentless", word)
}

// Tuning is DESIGN_SURVIVAL §7 in one place. Durations are ticks. Every value
// is a Survival design choice for play-testing, not retail behaviour.
//
// Difficulty follows game time, not the wave count: a wave's budget doubles
// every Doubling ticks of battle, so pressure keeps rising while a slow wave
// is still being cleared. The pause after a wave grows with that wave's size,
// so early waves come quickly and late ones leave time to rebuild.
type Tuning struct {
	FirstWaveDelay uint32 // battle start to wave 1's warning
	WarningTime    uint32 // warning before a wave arrives
	Straggle       uint32 // the next wave's warning waits at most this long past arrival + downtime

	// Downtime after a wave = DowntimeBase + DowntimePerUnit per tier-1
	// median unit of that wave's budget, at most DowntimeMax.
	DowntimeBase, DowntimePerUnit, DowntimeMax uint32

	// WaveReward is the metal and the energy each living survivor receives
	// when a wave counts as survived, up to their storage.
	WaveReward float32

	// A survived wave scores its budget, plus FastClearBonus percent of it
	// for a wave destroyed at once after arriving, falling linearly to none at
	// the next wave's deadline, plus CleanWaveBonus percent when no finished
	// structure fell to it (§8).
	FastClearBonus, CleanWaveBonus int64

	BaseUnits int64  // the budget at battle start, in median tier-1 units
	Doubling  uint32 // ticks for the budget to double

	UnlockUnits    int64  // a tier unlocks once a budget buys this many of its median unit
	NewTierWeight  int    // weight of the newest unlocked tier; older tiers weigh 1
	DirectionEvery uint32 // ticks per extra direction
	MaxDirections  int
	AirFrom        uint32 // first tick a wave may be airborne

	ThemeWeights [DomainCount]int // before the eligibility filter

	SpawnPerTick  int    // units created per tick
	EdgeInset     int32  // entry point inset from the map edge, cells
	RetargetEvery uint32 // idle retarget cadence
	BuddyRing     int32  // buddy distance from the centre site, cells
}

const minute = 60 * TicksPerSecond

// DefaultTuning returns the §7 values for a pace.
func DefaultTuning(p Pace) Tuning {
	t := Tuning{
		FirstWaveDelay:  60 * TicksPerSecond,
		WarningTime:     30 * TicksPerSecond,
		Straggle:        60 * TicksPerSecond,
		DowntimeBase:    30 * TicksPerSecond,
		DowntimePerUnit: 5 * TicksPerSecond / 2,
		DowntimeMax:     150 * TicksPerSecond,
		WaveReward:      1000,
		FastClearBonus:  50,
		CleanWaveBonus:  25,
		BaseUnits:       2,
		Doubling:        5 * minute,
		UnlockUnits:     8,
		NewTierWeight:   3,
		DirectionEvery:  12 * minute,
		MaxDirections:   3,
		AirFrom:         5 * minute,
		SpawnPerTick:    2,
		EdgeInset:       2,
		RetargetEvery:   90,
		BuddyRing:       20,
	}
	t.ThemeWeights[Ground] = 6
	t.ThemeWeights[Amphibious] = 0 // amphibious units join ground waves
	t.ThemeWeights[Hover] = 2
	t.ThemeWeights[Naval] = 2
	t.ThemeWeights[Air] = 2
	switch p {
	case PaceRelaxed:
		t.FirstWaveDelay = 90 * TicksPerSecond
		t.Doubling = 13 * minute / 2
	case PaceRelentless:
		t.FirstWaveDelay = 45 * TicksPerSecond
		t.Doubling = 4 * minute
	}
	return t
}

// Downtime is the pause after a wave of the given budget clears.
func (t Tuning) Downtime(budget, tier1Median int64) uint32 {
	if tier1Median < 1 {
		tier1Median = 1
	}
	d := int64(t.DowntimeBase) + int64(t.DowntimePerUnit)*budget/tier1Median
	if d > int64(t.DowntimeMax) {
		d = int64(t.DowntimeMax)
	}
	return uint32(d)
}

// maxBudget bounds the growth so late waves cannot overflow; the attacker's
// unit limit caps a wave long before this.
const maxBudget = int64(1) << 40

// Budget is the budget of a wave planned at tick, in cost units:
// BaseUnits × tier1Median × 2^(tick/Doubling), interpolated linearly within
// each doubling so it rises smoothly, in integers.
func (t Tuning) Budget(tick uint32, tier1Median int64) int64 {
	if tier1Median < 1 {
		tier1Median = 1
	}
	b := t.BaseUnits * tier1Median
	if t.Doubling == 0 {
		return b
	}
	for k := tick / t.Doubling; k > 0 && b < maxBudget; k-- {
		b *= 2
	}
	b += b * int64(tick%t.Doubling) / int64(t.Doubling)
	return min(b, maxBudget)
}

// UnlockedTier is the highest tier a budget may draw from: tier 1 always, and
// each higher tier once the budget buys UnlockUnits of its median unit.
func (t Tuning) UnlockedTier(budget int64, pool *Pool) int {
	top := 1
	for tier := 2; tier <= pool.MaxTier; tier++ {
		m := pool.TierMedian(tier)
		if m > 0 && budget >= t.UnlockUnits*m {
			top = tier
		}
	}
	return top
}

// Directions is how many entry directions a wave planned at tick uses.
func (t Tuning) Directions(tick uint32) int {
	d := 1
	if t.DirectionEvery > 0 {
		d = 1 + int(tick/t.DirectionEvery)
	}
	if t.MaxDirections > 0 && d > t.MaxDirections {
		d = t.MaxDirections
	}
	return d
}
