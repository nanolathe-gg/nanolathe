package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Mid-game expansion (expand parameter, README §13.10).
//
// Traces of 40-minute hard games on the long pool (a per-minute census of
// every metal spot and every builder's assignment) found three reasons
// the economy stops growing after minute 12, and a fourth that wastes
// the income once it grows:
//
//   - Extractors stop competing once metal banks. With the store full the
//     metal need falls to a fifth of nominal, so an extractor site scores
//     below a factory, a tower or guarding a factory, although dozens of
//     safe free spots remain (show down, greenhaven, plains and passes:
//     10–150 at minute 15) and an extractor pays for itself within about
//     a minute of its output.
//   - Constructors lose every factory slot to combat units: the economy
//     share falls to eco_late (40%) by minute 12 and further under
//     pressure, and each constructor owned halves the next one's worth at
//     four, so a constructor scores 100–400 against 600–1200 for the best
//     combat unit, and the count stays near 7 at minute 15 and 9 at 20
//     (original-TA winners: 9 and 16).
//   - Where both halves are taken (great divide, sherwood) the rest of
//     the spots lie on the contested line; they are claimable only on our
//     half of it, so no spot behind our own army ever counts.
//   - Factories do not follow the income. With parts 1–4 the income
//     nearly matched v1's (44 against 47 metal/s at minute 30; 37 before)
//     but the army did not grow (11,300 against v1's 18,400): the extra
//     metal went to overflow (6,100 wasted by minute 30 against 3,600),
//     builders and towers, on five factories against v1's eight. Once
//     the defense plan's budget (a share of income) is behind, a tower's
//     need reaches 6,500–8,000 while a factory's is capped at 2,000, so
//     builders put up towers with the store full (plains and passes: one
//     walled-in factory all game at 47 metal/s).
//
// expand is a sum of parts. Pace, builders and spend act from minute
// expand_from (default 15), where the benchmark finds util+tac's economy leaving the humans'
// (it matches them until minute 15): every investment before it costs the
// 20-minute race, which is decided on the army at minute 20. Started at
// minutes 4–12 the parts cost 4–8 points each against the brain without
// them in 20-minute games (the pace from minute 4 let builders and the
// commander take twenty extractors on metal heck by minute 8, and the
// first army was overrun). Hold, a fix, acts throughout.
//
//   - 1 pace: from minute expand_from an extractor site is scored with a
//     metal need of at least paceNeed while our finished extractors trail
//     the top human tier's extractor curve (topExtractors, scaled by
//     ambition) and energy is not short (every extractor draws energy, and
//     extraction stops in a stall); the travel half-life for extractor
//     sites doubles from minute 10 to 20 as the base grows (as growth
//     part 1).
//   - 2 builders: from minute expand_from a constructor target of
//     consPerMex (0.55 at minute 12 rising to 0.75 by 20, ramped in over
//     xConsRamp minutes) per finished extractor, plus one per
//     spots_per_con claimable free spots (at most xRoomCons), held to the
//     top tier's constructor curve (topConstructors); below it a
//     constructor is wanted outright (as growth part 4, but not only from
//     surplus). Not while the base is under pressure or the enemy army
//     estimate exceeds ours by half: then the army comes first.
//   - 4 hold: an extractor placement that fails where neither a
//     reclaimable feature in view nor a building of ours stands holds the
//     spot on the board (core.Board.HoldSpot: an enemy building we have
//     not seen, the cause of every failure traced) instead of retrying it
//     on a timer (a minute per failure, up to eight), and releases it as
//     soon as a unit of ours sees it empty, so a spot the enemy lost is
//     retaken without waiting out the block.
//   - 8 contest: a spot at least xContestTerr of the way into territory
//     (the contested line) counts as ours while our own armed strength
//     there (Board.OwnPower) is at least xContestPower and twice the
//     threat, so builders follow the army.
//   - 16 spend: from minute expand_from, while metal is not short
//     (coverage at least surplusCov, or the store banking) and our working
//     factories are fewer than one per xFacIncome of metal income (v1's
//     ratio at minute 30), the
//     factory weight is raised for the think by xSpendPer per missing
//     factory (at most xSpendMax times), and guarding a producing factory
//     by half that, so the income becomes army rather than overflow.
const (
	xPace     = 1 << iota // extractors on the expansion pace
	xBuilders             // constructors follow the expansion
	xHold                 // failed spots held until seen empty
	xContest              // contested spots behind our army
	xSpend                // factories follow the income while metal banks
)

const (
	paceNeed      = 1000 // permille: least extractor need while behind the pace curve
	xConsRamp     = 4    // minutes over which the constructor target ramps in
	xRoomCons     = 2    // constructors at most for claimable free spots
	xConsRatio    = 1500 // permille: estimated enemy army to ours above which the army comes first
	xContestTerr  = 300  // permille of territory: the contested line's far edge
	xContestMult  = 2    // own strength at least this multiple of the threat holds a spot
	xContestPower = 80   // least own strength (grid units, about two tanks) that holds a spot
	xHoldR        = 48   // an own building this close to a failed spot blocks it itself
	xFacIncome    = 6000 // milli metal/s of income per working factory (spend)
	xSpendPer     = 2000 // permille added to the factory weight per missing factory
	xSpendMax     = 8000 // permille: the factory weight's largest multiple
)

// xpart reports whether one part of expand acts. Unlike growth it acts at
// every ambition: its curves are scaled by ambition, and the lower
// personas' tier caps (style.go) zero the weights that would exceed them.
func (s *shared) xpart(bit int32) bool { return s.p.Expand&bit != 0 }

// xFrom is the tick from which the pace, builders and spend parts act.
func (s *shared) xFrom() uint32 { return uint32(s.p.ExpandFrom) * 1800 }

// paceFloor is the least metal need an extractor site is scored with
// under the pace part: paceNeed while our finished extractors trail the
// top tier's curve, 0 (no floor) otherwise.
func (s *shared) paceFloor() int64 {
	if !s.xpart(xPace) || s.tick < s.xFrom() || int64(s.mexBuilt)*1000 >= topExtractors.at(ambition(&s.k.Persona), s.tick) {
		return 0
	}
	return paceNeed
}

// consExpand is the builders part's constructor target (milli), 0 when it
// does not act.
func (s *shared) consExpand() int64 {
	if !s.xpart(xBuilders) || s.tick < s.xFrom() || s.pressure >= 200 || s.armyRatio > xConsRatio {
		return 0
	}
	t := int64(s.tick)
	ratio := consPerMexLo + lin(t, 12*1800, 20*1800)*(consPerMexHi-consPerMexLo)/one
	ratio = ratio * lin(t, int64(s.xFrom()), int64(s.xFrom())+xConsRamp*1800) / one
	room := min64(int64(s.freeSafe)*1000/int64(max32(s.p.SpotsPerCon, 1)), xRoomCons*1000)
	return min64(ratio*int64(s.mexBuilt)+room, topConstructors.at(ambition(&s.k.Persona), s.tick))
}

// contested reports, under the contest part, a spot on the contested line
// (territory at least xContestTerr) that our army holds: own strength at
// least xContestPower and xContestMult times the threat.
func (s *shared) contested(b *core.Board, x, z int32, terr int64) bool {
	if !s.xpart(xContest) || terr >= one || terr < xContestTerr {
		return false
	}
	own := int64(b.OwnPower.At(x, z))
	return own >= xContestPower && own >= xContestMult*int64(b.Threat.At(x, z))
}

// holdFailed is the hold part's answer to an extractor placement that
// failed at spot sp: unless a reclaimable feature in view (layout clears
// it, clearSpot) or a building of ours stands on the spot, the building
// that stopped it is one we have not seen, so the board holds the spot
// until a unit of ours sees it empty. On the spot's first failure the
// timed block fail set is lifted (the hold replaces it); a spot that fails
// again after its hold was released keeps the growing block as well.
func (e *Economy) holdFailed(b *core.Board, sp int32) {
	s := e.s
	spot := &b.K.Map.Spots[sp]
	for i := range b.O.Features {
		f := &b.O.Features[i]
		if f.Reclaimable && (f.Blocking || f.Metal > 0) && aikit.Dist2(f.X, f.Z, spot.X, spot.Z) <= spotClearR*spotClearR {
			return
		}
	}
	for i := range b.O.Own {
		u := &b.O.Own[i]
		if !u.Info.Role.Has(aikit.RoleMobile) && aikit.Dist2(u.X, u.Z, spot.X, spot.Z) <= xHoldR*xHoldR {
			return
		}
	}
	b.HoldSpot(sp)
	if s.spotFails[sp] <= 1 {
		s.spotBlock[sp] = 0
	}
}

// spendWeights is the spend part: from minute expand_from, while metal is
// not short (surplus: coverage at least surplusCov or the store banking)
// and working factories (built or framed, not written off) are fewer
// than one per xFacIncome of metal income, it raises this think's factory
// weight by xSpendPer per missing factory (at most xSpendMax) and the
// assist weight, which also rates guarding a producing factory, by half
// that. Params are reset from
// the plan every think (Strategy.Plan), so the raise lasts one think.
func (s *shared) spendWeights(b *core.Board) {
	if !s.xpart(xSpend) || s.tick < s.xFrom() || !s.surplus() {
		return
	}
	n := int64(len(b.Factories)) - int64(s.deadFacs)
	for _, i := range b.Frames {
		if b.O.Own[i].Info.Role.Has(aikit.RoleFactory) {
			n++
		}
	}
	short := s.mInc*1000/xFacIncome - n*1000
	if short <= 0 {
		return
	}
	f := min64(one+short*xSpendPer/one, xSpendMax)
	p := &s.p
	p.WFactory = int32(int64(p.WFactory) * f / one)
	p.WAssist = int32(int64(p.WAssist) * (one + (f-one)/2) / one)
}
