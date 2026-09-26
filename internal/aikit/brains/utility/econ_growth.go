package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// The human economy shape (growth parameter, README §13.4).
//
// The recorded-game benchmark (tools/ai-human-bench, REPORT.md sections 8
// and 10 (d)) found util+tac's economy matching the human winners' until
// minute 15 and then stalling: constructors per extractor 0.38 / 0.41 /
// 0.41 at minutes 10 / 15 / 20 where human land-map winners hold 0.57 /
// 0.66 / 0.76, extractors plateauing near 14 while people grow from 25 to
// 33 between minutes 15 and 20, twice the factories for the income (people
// build one per ~14 metal/s, the second around minute 8), and a tech-2
// factory in a quarter of its games where 73% of winners have one by a
// median minute 12. growth is a sum of parts, at full ambition only (the
// lower personas' plans are their tier caps, style.go, and stay as they
// are). The investments (claimable territory, the extractor need floor,
// the constructor target, the factory cap) wait for growSafe: from minute
// 8 (the opening and the first push are the brain's own), while our army
// is at least the estimated enemy army and the base is not under
// pressure; tech 2 has its own safety.
//
//   - 1 expansion: extraction territory is full up to two thirds of the
//     way to the enemy base (the neutral line moves a sixth of the way
//     forward, so contested spots count as claimable), the travel
//     half-life for extractors doubles from minute 10 to 20 as the base
//     grows (human bases' economy radius grows from ~850 to ~1,200 wu), an
//     extractor site is scored with a metal need of at least mexNeedFloor
//     while energy is not short, and a helper (guarding a factory,
//     assisting a frame) leaves for a new building that beats its help
//     without the hysteresis margin, helping factories from a lower metal
//     coverage.
//   - 2 tech 2 on the human timeline: the tech timeline runs to three
//     quarters of tech_time (minute 12 by default) on an income gate of
//     12–24 metal/s, held back by pressure or an enemy army estimate of
//     1.5–3× ours, and the first tech-2 factory's suitability is at least
//     nominal.
//   - 4 constructors per extractor: while metal is a surplus, a target of
//     consPerMex (0.55 at minute 12 rising to 0.75 by minute 20, the
//     humans' band; ramped in from minute 8) per finished extractor plus
//     one per spots_per_con claimable free spots, at most expandCons.
//     Below it a constructor is wanted at up to twice the nominal need
//     without diminishing returns; above it each one more is worth a
//     third, a fifth...
//   - 8 factories per income: another factory only while there are fewer
//     than one per facIncome metal/s of income, or two from minute 8 (the
//     first tech-2 factory is exempt; a store capLiftPercent full lifts
//     the cap).
//   - 16 constructor floor: at least one constructor from minute 5 and two
//     from minute 8, on any map (consFloor). Not an investment gated by
//     growSafe: without it the commander built alone all game on The
//     Pass.
//
// Parts 1–8 move their measures toward the humans' and cost points in
// 20-minute games against the brain without them (README §13.4); the
// default is the floor alone (16).

const (
	consPerMexLo   = 550    // constructors per extractor, permille, at minute 12
	consPerMexHi   = 750    // ... by minute 20
	facIncome      = 14000  // milli metal/s of income per factory
	expandCons     = 2      // constructors beyond the ratio for claimable free spots
	mexNeedFloor   = 600    // permille: least metal need an extractor site is scored with
	capLiftPercent = 80     // metal store percent full (income covering expense) that lifts the factory cap
	secondFacAt    = 8 * 60 // seconds: a second factory is allowed from here
	growSafeRatio  = 1000   // permille: estimated enemy army to ours at or below which growth invests
	growFrom       = 8      // minute from which growth invests (the opening and first push are the brain's own)
)

// Parts of growth (the parameter is their sum).
const (
	gExpand       = 1 << iota // claimable territory, extractor travel and need, helpers leave for builds
	gTech                     // tech 2 on the human timeline
	gConstructors             // constructors per extractor, from surplus
	gFactories                // factories per income
	gConsFloor                // at least a constructor or two, on any map
)

// surplusCov: metal coverage (permille) from which metal is a surplus the
// constructors part may spend.
const surplusCov = 1100

// growth reports whether the growth switch acts: at full ambition only.
func (s *shared) growth() bool {
	if s.p.Growth == 0 {
		return false
	}
	return ambition(&s.k.Persona) >= 100
}

// setupGrowth lists the factory definitions (setup).
func (s *shared) setupGrowth(k *aikit.Kit) {
	for i, u := range k.Table.Units {
		if u.Role.Has(aikit.RoleFactory) {
			s.facDefs = append(s.facDefs, int32(i))
		}
	}
}

func (s *shared) countDefs(defs []int32) int64 {
	var n int64
	for _, i := range defs {
		n += int64(s.count[i])
	}
	return n
}

// consTarget is how many constructors the economy wants (milli): the
// human ratio per finished extractor (frames and walking builders would
// feed back into the target), plus one builder per spots_per_con
// claimable free spots, at most expandCons.
func (s *shared) consTarget() int64 {
	ratio := consPerMexLo + lin(int64(s.tick), 12*1800, 20*1800)*(consPerMexHi-consPerMexLo)/one
	ratio = ratio * lin(int64(s.tick), growFrom*1800, 12*1800) / one
	room := min64(int64(s.freeSafe)*1000/int64(max32(s.p.SpotsPerCon, 1)), expandCons*1000)
	return ratio*int64(s.mexBuilt) + room
}

// spotTerritory is territory for extraction: with growth it is full up to
// two thirds of the way to the enemy base, falling to 0 there; with
// expand's contest part a spot on the contested line that our army holds
// counts as ours (econ_expand.go).
func (s *shared) spotTerritory(b *core.Board, x, z int32) int64 {
	var t int64
	if !s.part(gExpand) || !s.growSafe() {
		t = s.territory(b, x, z)
	} else {
		dh := int64(aikit.Dist(x, z, b.HomeX, b.HomeZ))
		de := int64(aikit.Dist(x, z, s.enemyX, s.enemyZ))
		t = clamp(de*3000/(de+dh+1), 0, one)
	}
	if s.contested(b, x, z, t) {
		return one
	}
	return t
}

// mexTravelHalf is the travel half-life for extractor sites: with growth
// (or expand's pace part) it doubles from minute 10 to 20.
func (s *shared) mexTravelHalf() int64 {
	h := int64(s.p.TravelHalf)
	if !s.part(gExpand) && !s.xpart(xPace) {
		return h
	}
	return h + h*lin(int64(s.tick), 10*1800, 20*1800)/one
}

// factoryFull reports, with growth, that the factories already match the
// income: one per facIncome of metal income, or two from minute 8. A
// metal store capLiftPercent full while income covers expense lifts the
// cap: the factories, helped by the builders, cannot spend what comes in.
// A factory written off (its exit walled in) does not count.
func (s *shared) factoryFull() bool {
	if !s.part(gFactories) || !s.growSafe() || (s.mCap > 0 && s.mStock*100 >= s.mCap*capLiftPercent && s.mInc >= s.mExp) {
		return false
	}
	allowed := max64(1000+lin(int64(s.tick), secondFacAt*30-900, secondFacAt*30)*1000/one, s.mInc*1000/facIncome)
	return (s.countDefs(s.facDefs)-int64(s.deadFacs))*1000 >= allowed
}

// mexNeed is the metal need an extractor site is scored with: with growth
// at least mexNeedFloor while energy is not short (an extractor keeps
// paying, and people keep adding them at about 1.7 a minute whatever
// their store holds; but every extractor draws energy, and extraction
// stops in an energy stall).
//
// Expand's pace part sets its own floor (paceFloor, econ_expand.go) on the
// same energy condition, the larger of the two applying.
func (s *shared) mexNeed() int64 {
	floor := s.paceFloor()
	if s.part(gExpand) && s.growSafe() {
		floor = max64(floor, mexNeedFloor)
	}
	if floor == 0 {
		return s.needM
	}
	short := max64(s.needE, s.needFirm)
	return max64(s.needM, floor*lin(short, 1500, 1000)/one)
}

// growSafe reports that investing is safe: our army at least the
// estimated enemy army and no pressure on the base. Growth invests (more
// constructors, contested spots, fewer factories) only then; behind, the
// brain builds as before.
func (s *shared) growSafe() bool {
	return s.tick >= growFrom*1800 && s.armyRatio <= growSafeRatio && s.pressure < 200
}

// consFloor is the constructor floor (milli): one from minute 5, two from
// minute 8. The economy share alone wants none where income is small: on
// The Pass (four metal spots, both of ours taken early, no expansion
// room) the commander's own build power covers it, so the commander built
// alone all game. The floor binds only where the brain is that short: by
// minute 5 it has no constructor in 9 of 96 land-pool games, fewer than
// two by minute 8 in 15. People build their first at a median minute 2.3
// and original-TA winners hold three at minute 5, but an earlier floor
// (one from minute 2, two from 5) moved the opening in most games and cost
// points (47.9% over 96 games against the brain without it).
func (s *shared) consFloor() int64 {
	switch {
	case s.tick >= 8*1800:
		return 2000
	case s.tick >= 5*1800:
		return 1000
	}
	return 0
}

// surplus reports metal the army is not using: supply above demand by a
// tenth, or a store at least bankPercent full while income covers expense.
func (s *shared) surplus() bool {
	return s.covM >= surplusCov || (s.mCap > 0 && s.mStock*100 >= s.mCap*bankPercent && s.mInc >= s.mExp)
}

// part reports whether one part of growth acts.
func (s *shared) part(bit int32) bool { return s.growth() && s.p.Growth&bit != 0 }
