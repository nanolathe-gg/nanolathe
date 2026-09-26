package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Production that follows reach (reach_mix switch, README §13.1).
//
// Until enemy buildings are seen, the reach table is a prior over where the
// enemy may be. Without the switch it is the mean over every other start
// position, which on a map with clustered starts counts the starts right
// beside ours (on "shore to shore" three of the nine others stand within
// 256 wu of us, all land-reachable, while the real enemy is across the
// water). With the switch the prior is the mean over the plausible starts:
// those farther than startNearHome from home that no unit of ours has looked
// at (the board's own rules for "our start" and for a cleared start).
//
// A factory none of whose combat products reaches the plausible enemy is
// stranded: it still makes constructors, and a home guard while it is small
// or the base is threatened, but no army beyond that, and a stranded
// factory is built only while no factory of ours makes land constructors.
// The first factory is not an air plant while the land army may reach
// (land reach at least strandReach): the lower
// prior tipped air-favouring styles into an air-first opening on river
// maps (evad river confluence, style twofac: a points draw against retail),
// and air-first openings score a third of the points where tanks reach
// (econ_family.go).

const (
	// startNearHome: a start this close to home is ours (core.Board uses
	// the same radius).
	startNearHome = 400
	// startLookR: an own mobile unit this close to a start, with no enemy
	// building seen anywhere, shows the start is empty (core.Board's rule).
	startLookR = 350
	// strandReach: below this reach (permille) a factory's army cannot get
	// to the enemy; 300 is below the reach of a unit that ends 500 wu short
	// (the curve falls from range+100 to range+700).
	strandReach = 300
	// reachMixSel marks the plausible-start table in shared.reachSel.
	reachMixSel = -3
)

// reachMixState is the plausible-start prior's per-game state.
type reachMixState struct {
	near  []bool  // per start: ours (within startNearHome of home)
	seen  []bool  // per start: looked at and found empty
	mask  uint64  // plausible starts behind the current table
	table []int16 // per definition: mean reach over the plausible starts
}

// setupReachMix allocates the prior's tables (setup, after the terrain).
func (s *shared) setupReachMix(k *aikit.Kit) {
	m := k.Map
	rm := &s.rmix
	rm.near = make([]bool, len(m.Starts))
	rm.seen = make([]bool, len(m.Starts))
	rm.table = make([]int16, len(k.Table.Units))
	for j, st := range m.Starts {
		rm.near[j] = aikit.Dist2(st[0], st[1], m.HomeX, m.HomeZ) < startNearHome*startNearHome
	}
}

// nearHome reports whether start j is ours (reach_mix only).
func (s *shared) nearHome(j int) bool {
	return j < len(s.rmix.near) && s.rmix.near[j]
}

// selectReachMix installs the mean reach over the plausible enemy starts.
func (s *shared) selectReachMix(b *core.Board) {
	t := &s.terr
	rm := &s.rmix
	m := b.K.Map
	o := b.O
	n := len(m.Starts)
	if n > 64 {
		n = 64
	}
	var mask, all uint64
	for j := 0; j < n; j++ {
		if rm.near[j] || !m.MaybeEnemyStart(j) {
			continue
		}
		all |= 1 << uint(j)
		if !rm.seen[j] {
			st := m.Starts[j]
			for i := range o.Own {
				u := &o.Own[i]
				if u.Built && u.Info.Role.Has(aikit.RoleMobile) && aikit.Dist2(u.X, u.Z, st[0], st[1]) < startLookR*startLookR {
					rm.seen[j] = true
					break
				}
			}
		}
		if !rm.seen[j] {
			mask |= 1 << uint(j)
		}
	}
	if mask == 0 {
		mask = all // every start looked at: the enemy moved; keep the whole prior
	}
	if s.reachSel == reachMixSel && mask == rm.mask && s.reach != nil {
		return
	}
	rm.mask = mask
	var cnt int64
	for j := 0; j < n; j++ {
		if mask&(1<<uint(j)) != 0 {
			cnt++
		}
	}
	for i := range rm.table {
		if cnt == 0 {
			rm.table[i] = t.reachAvg[i]
			continue
		}
		var sum int64
		for j := 0; j < n; j++ {
			if mask&(1<<uint(j)) != 0 {
				sum += int64(t.reachAt[j][i])
			}
		}
		rm.table[i] = int16(sum / cnt)
	}
	s.reach = rm.table
	s.reachSel = reachMixSel
}

// stranded reports a factory whose army cannot reach the plausible enemy
// (reach_mix only; false while the terrain model is off).
func (s *shared) stranded(f *aikit.UnitInfo) bool {
	return s.p.ReachMix != 0 && s.reach != nil && s.factoryReach(f) < strandReach
}

// homeGuardFull reports whether the stranded army at home is as large as a
// home guard needs to be: the minimum wave value plus a quarter of the
// estimated enemy army, in combat units whose own reach is below
// strandReach, and the whole army at the strategy's army target (the value
// production pushes toward, which follows the enemy estimate). Below the
// target a stranded factory keeps filling the army when what reaches
// cannot: on shore to shore the guard alone left the army at half the
// enemy's while its bombers were shot down, and the game went on points
// to the brain without the switch's caps. A threat near home lifts the
// cap, and so does metal banking (the store bankPercent full while income
// covers expense).
func (s *shared) homeGuardFull(b *core.Board) bool {
	if b.NearHomeThreat > 0 || (s.mCap > 0 && s.mStock*100 >= s.mCap*bankPercent && s.mInc >= s.mExp) {
		return false
	}
	if int64(b.ArmyValue) < s.armyTarget {
		return false
	}
	var v int64
	for _, i := range b.Combat {
		u := &b.O.Own[i]
		if s.reachOf(u.Info) < strandReach {
			v += int64(u.Info.Value)
		}
	}
	return v >= int64(s.p.AttackMin)+s.enEst/4
}

// consSourceSuit is the least suitability (permille of the best factory)
// of a factory built as the source of land constructors: as suitable as
// the best factory, so that a land factory is also the first factory
// where one fits (a shipyard or air plant first leaves the commander the
// only land builder until the constructor source comes).
const consSourceSuit = one

// landConsFactory reports whether we own (built, framed or committed) a
// factory that makes a constructor able to work land. Until one exists, a
// land factory is built as the constructor source whether or not its
// army reaches the enemy (need as urgent as a first factory's,
// suitability at least consSourceSuit): on pillopeens the first factory
// was a shipyard, whose constructors only work water, and with land reach
// 0 no land factory rated at all, which left the commander as the only
// land builder for ten minutes (income flat at 11 metal/s, 26 without the
// switch).
func (s *shared) landConsFactory() bool {
	for _, fi := range s.factories {
		if s.count[fi] > 0 && makesLandCons(s, s.k.Table.Units[fi]) {
			return true
		}
	}
	return false
}

// makesLandCons reports whether a factory makes a constructor able to work
// land (a builder of extractors or energy that is not a water unit).
func makesLandCons(s *shared, f *aikit.UnitInfo) bool {
	for _, q := range f.Builds {
		if si := &s.info[q.Index]; si.canEco && !si.water && !q.Role.Has(aikit.RoleNaval) {
			return true
		}
	}
	return false
}
