package tactics

import "github.com/nanolathe-gg/nanolathe/internal/aikit"

// force is an aggregate fighting summary of a group of units.
//
// Lanchester's square law models an aimed-fire battle between two groups as
// a race in which each side's fighting strength is its total firepower times
// its total staying power: a group twice as large is four times as strong.
// Comparing dps×hp products therefore predicts both the winner and how much
// of the winner survives.
type force struct {
	dps   int64 // ground damage per second
	hp    int64 // hit points (current where known)
	aa    int64 // anti-air damage per second
	rngW  int64 // Σ range×dps, for the dps-weighted mean range
	value int64 // metal-equivalent value
	n     int32
}

// add counts one unit at weight w permille (1000 = fully).
func (f *force) add(info *aikit.UnitInfo, hp int64, w int64) {
	dps := int64(info.DPS)
	f.dps += dps * w / 1000
	f.hp += hp * w / 1000
	f.aa += int64(info.AirDPS) * w / 1000
	f.rngW += int64(info.Range) * dps * w / 1000
	f.value += int64(info.Value) * w / 1000
	f.n++
}

// addDPS counts one unit with only part of its firepower (for example the
// guns of a ship that can shell a coast, not its torpedoes).
func (f *force) addDPS(info *aikit.UnitInfo, hp, dps int64) {
	f.dps += dps
	f.hp += hp
	f.aa += int64(info.AirDPS)
	f.rngW += int64(info.Range) * dps
	f.value += int64(info.Value)
	f.n++
}

// addEnemy counts an enemy unit; the commander is valued as a target
// rather than by its authored cost, and its manual super-weapon adds to its
// sustained damage.
func (f *force) addEnemy(info *aikit.UnitInfo, hp int64, w int64) {
	f.add(info, hp, w)
	if info.Role.Has(aikit.RoleCommander) {
		f.value += (commanderValue - int64(info.Value)) * w / 1000
		f.dps += commanderDPSBonus * w / 1000
	}
}

// addGeneric counts an unidentified radar contact as a modest ground unit.
func (f *force) addGeneric(w int64) {
	f.dps += blipDPS * w / 1000
	f.hp += blipHP * w / 1000
	f.rngW += blipRange * blipDPS * w / 1000
	f.value += blipValue * w / 1000
	f.n++
}

func (f *force) merge(g *force) {
	f.dps += g.dps
	f.hp += g.hp
	f.aa += g.aa
	f.rngW += g.rngW
	f.value += g.value
	f.n += g.n
}

// scaled returns f with firepower scaled by num/den (range weights follow).
func (f force) scaledDPS(num, den int64) force {
	if den <= 0 {
		return f
	}
	f.dps = f.dps * num / den
	f.rngW = f.rngW * num / den
	return f
}

// meanRange is the dps-weighted weapon range, world units.
func (f *force) meanRange() int64 {
	if f.dps <= 0 {
		return 0
	}
	return f.rngW / f.dps
}

// strength is the square-law fighting strength.
func (f *force) strength() int64 { return f.dps * f.hp }

// Radar blips of unknown type are counted as a mid-weight ground unit.
const (
	blipDPS   = 40
	blipHP    = 600
	blipRange = 250
	blipValue = 120
)

// ratioCap bounds a ratio against a harmless opponent.
const ratioCap = 100000

// ratio predicts own strength over enemy strength, in permille, after a
// range adjustment: the side with the longer dps-weighted range fires while
// the other closes, worth up to a quarter more effective damage.
func ratio(own, en *force) int64 {
	if en.dps <= 0 || en.hp <= 0 {
		return ratioCap
	}
	if own.dps <= 0 || own.hp <= 0 {
		return 0
	}
	od, ed := own.dps, en.dps
	adj := (own.meanRange() - en.meanRange()) / 8
	if adj > 25 {
		adj = 25
	} else if adj < -25 {
		adj = -25
	}
	if adj > 0 {
		od = od * (100 + adj) / 100
	} else {
		ed = ed * (100 - adj) / 100
	}
	r := od * own.hp / ed * 1000 / en.hp
	if r > ratioCap {
		r = ratioCap
	}
	return r
}

// lossPermille is the share of its force the winner of a square-law fight
// at strength ratio r (permille) is expected to lose: 1 − √(1 − 1/r). A
// side that does not out-strength its opponent loses everything.
func lossPermille(r int64) int64 {
	if r <= 1000 {
		return 1000
	}
	x := 1000000 - 1000000*1000/r
	return 1000 - aikit.ISqrt64(x)
}

// rangeHist buckets a squad's damage by weapon range so a static defense's
// "how much of this squad outranges me" is one lookup.
type rangeHist [rangeBuckets]int64

const (
	rangeBucket  = 50
	rangeBuckets = 32
)

func (h *rangeHist) clear() { *h = rangeHist{} }

func (h *rangeHist) add(rng, dps int64) {
	b := rng / rangeBucket
	if b >= rangeBuckets {
		b = rangeBuckets - 1
	}
	if b < 0 {
		b = 0
	}
	h[b] += dps
}

// finish turns the histogram into "damage at or beyond bucket b".
func (h *rangeHist) finish() {
	for b := rangeBuckets - 2; b >= 0; b-- {
		h[b] += h[b+1]
	}
}

// beyond is the damage of units whose range exceeds rng by a margin.
func (h *rangeHist) beyond(rng int64) int64 {
	b := (rng+outrangeMargin)/rangeBucket + 1
	if b >= rangeBuckets {
		return 0
	}
	return h[b]
}

// outrangeMargin is how much longer a unit's range must be before it can
// pick a static defense apart from outside that defense's reach.
const outrangeMargin = 24

// staticDiscount is the share (permille) of a static defense's fire that
// still lands when the whole attacking group outranges it: units that
// outrange it are shot at only while they walk in.
const staticDiscount = 400

// effectiveStatic discounts a static-defense force by the share of the
// attacker's damage that outranges it.
func effectiveStatic(stat *force, h *rangeHist, ownDPS int64) force {
	if stat.dps <= 0 || ownDPS <= 0 {
		return *stat
	}
	out := h.beyond(stat.meanRange())
	if out > ownDPS {
		out = ownDPS
	}
	keep := 1000 - (1000-staticDiscount)*out/ownDPS
	return stat.scaledDPS(keep, 1000)
}
