package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Strategy wraps the utility strategy. Before it runs, the board is shown
// the Survival geometry — the point the economy faces, and the metal spots
// that belong to the human or the other buddy held back — and afterwards
// the posture is set never to attack out: there is no base to attack, and
// an army walking to a map edge leaves the team's buildings to the wave.
type Strategy struct {
	st    *state
	inner core.Policy
}

// Init implements core.Policy. It also labels the dry land the survivor's
// commander class can walk (MapInfo.Reach builds it once per battle, on the
// preparation goroutine, as Init runs).
func (s *Strategy) Init(b *core.Board) {
	if s.inner != nil {
		s.inner.Init(b)
	}
	st := s.st
	k := b.K
	if k.Map == nil || k.Table == nil {
		return
	}
	for _, u := range k.Table.Units {
		if !u.Role.Has(aikit.RoleCommander) || u.Side != k.Side {
			continue
		}
		if mc, ok := aikit.MoveClassOf(u); ok {
			// Dry land only: a commander walks the seabed, but a tower,
			// a wall or a base cannot stand there.
			mc.MaxDepth = 0
			st.reach = k.Map.Reach(mc)
		}
		break
	}
	if st.reach == nil {
		return
	}
	x, z := k.Map.HomeX, k.Map.HomeZ
	for i, p := range st.sc.Team {
		if p == st.sc.Me && i < len(st.sc.Starts) {
			x, z = st.sc.Starts[i][0], st.sc.Starts[i][1]
		}
	}
	st.homeRegion = st.reach.At(x, z)
}

// Plan implements core.Policy.
func (s *Strategy) Plan(b *core.Board) {
	st := s.st
	if !st.ready {
		st.setup(b)
	}
	st.observe(b)
	st.faceEconomy(b)
	st.reserveSpots(b)
	if s.inner != nil {
		s.inner.Plan(b)
	}
	b.Posture.AttackValue = 1 << 30
	b.Posture.Label = "survival"
}

// observe refreshes the per-think picture: the warnings in force, the
// attackers seen by bearing, the income received, the towers and the
// perimeter by sector.
func (st *state) observe(b *core.Board) {
	o := b.O
	st.tick = b.Tick
	// Resources received since the last think, metal-equivalent: what the
	// stock gained plus what was spent meanwhile (expense is per second).
	// Unlike the production figure this counts the wave rewards and the
	// teammates' shares of their production (DESIGN_SURVIVAL §4.3, §6.9),
	// which on a poor map are most of what a survivor gets.
	if st.lastInc != 0 && b.Tick > st.lastInc {
		dt := int64(b.Tick - st.lastInc)
		m := int64(o.Metal.Stock-st.lastM) + int64(o.Metal.Expense)*dt/30
		e := int64(o.Energy.Stock-st.lastE) + int64(o.Energy.Expense)*dt/30
		st.income += max(m, 0) + max(e, 0)/aikit.EnergyPerMetal
	}
	st.lastInc, st.lastM, st.lastE = b.Tick, o.Metal.Stock, o.Energy.Stock
	st.stock.metal, st.stock.energy = o.Metal, o.Energy
	if o.Metal.Stock*5 >= o.Metal.Cap*4 && o.Energy.Stock*5 >= o.Energy.Cap*4 {
		if st.bankedSince == 0 {
			st.bankedSince = max(b.Tick, 1)
		}
	} else {
		st.bankedSince = 0
	}
	// Warnings in force: announced, and not yet a minute and a half past
	// their arrival (a wave's units keep arriving and walking in).
	st.warn = st.warn[:0]
	if st.sc.Feed != nil {
		st.warn = st.sc.Feed.Warnings(b.Tick, st.warn)
	}
	st.live = st.live[:0]
	st.liveArr = 0
	st.air = b.EnemyAir > 0
	for i := range st.warn {
		w := &st.warn[i]
		if b.Tick > w.Arrive+liveAfter {
			continue
		}
		for _, g := range w.Groups {
			st.live = append(st.live, g)
			if g.Air {
				st.air = true
			}
		}
		if st.liveArr == 0 || w.Arrive < st.liveArr {
			st.liveArr = w.Arrive
		}
	}
	st.observeHistory(b)
	st.observeSectors(b)
}

// liveAfter is how long past its arrival a warning still steers the plan.
const liveAfter = 2700

// histTicks is the time constant of the attacker history's fade.
const histTicks = 9000

// observeHistory adds the armed attackers in view to the bearings they are
// on, fading older sightings: the directions waves have actually come from,
// which the terrain funnels (ramps, passes) more than the random draw does.
func (st *state) observeHistory(b *core.Board) {
	if st.histAt != 0 && b.Tick > st.histAt {
		dt := int64(b.Tick - st.histAt)
		for s := range st.hist {
			st.hist[s] -= st.hist[s] * min(dt, histTicks) / histTicks
		}
	}
	st.histAt = b.Tick
	o := b.O
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info == nil || c.Info.DPS == 0 || !c.Info.Role.Has(aikit.RoleMobile) {
			continue
		}
		vx, vz := int64(c.X-st.cx), int64(c.Z-st.cz)
		if vx*vx+vz*vz < 300*300 || vx*vx+vz*vz > 3000*3000 {
			continue
		}
		st.hist[sectorOf(vx, vz)] += int64(c.Info.Value)
	}
}

// observeSectors measures, per sector, the perimeter (how far out from the
// site this survivor's buildings reach on that bearing) and the towers
// already standing or framed there, and derives the sector's weight.
func (st *state) observeSectors(b *core.Board) {
	o := b.O
	for s := range st.radius {
		st.radius[s] = humanRoom
		st.have[s], st.towers[s], st.walls[s], st.aaTowers[s] = 0, 0, 0, 0
	}
	for i := range o.Own {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleMobile) {
			continue
		}
		vx, vz := int64(u.X-st.cx), int64(u.Z-st.cz)
		if vx*vx+vz*vz < 32*32 {
			continue
		}
		s := sectorOf(vx, vz)
		d := st.tab.of(u.Info)
		switch {
		case d.tower || d.aa:
			st.have[s] += int64(u.Info.Value)
			st.towers[s]++
			if d.aa {
				st.aaTowers[s]++
			}
		case d.wall:
			st.walls[s]++
		case u.Info.Role.Any(aikit.RoleFactory | aikit.RoleEnergy | aikit.RoleMetalMaker | aikit.RoleStorage | aikit.RoleRadar):
			// The perimeter is the base proper: an extractor out on its
			// own is not worth a ring drawn around it.
			if r := int32(aikit.ISqrt64(vx*vx+vz*vz)) + perimeterAhead; r > st.radius[s] {
				st.radius[s] = min(r, maxPerimeter)
			}
		}
	}
	st.observeAllies(b)
	st.observeWeights()
}

// observeWeights derives each sector's weight: every owned sector with
// reachable ground counts; warned directions and the attackers' history
// add to theirs and to their neighbours'.
func (st *state) observeWeights() {
	for s := range st.weight {
		st.weight[s] = 0
		if st.mine[s] && st.siteable[s] {
			st.weight[s] = 1000
		}
	}
	for _, g := range st.live {
		if g.Air {
			continue
		}
		c := sectorOfAngle(g.Angle)
		for s := range st.weight {
			if !st.mine[s] || !st.siteable[s] {
				continue
			}
			switch ringDist(s, c) {
			case 0:
				st.weight[s] += warnWeight
			case 1:
				st.weight[s] += warnWeight / 2
			case 2:
				st.weight[s] += warnWeight / 6
			}
		}
	}
	var hsum int64
	for s := range st.hist {
		hsum += st.hist[s]
	}
	if hsum > 0 {
		for s := range st.weight {
			if st.mine[s] && st.siteable[s] {
				st.weight[s] += histWeight * st.hist[s] / hsum
			}
		}
	}
}

const (
	// perimeterAhead is how far beyond a sector's outermost building of
	// ours its towers stand; maxPerimeter is the farthest from the site a
	// sector's perimeter is drawn.
	perimeterAhead = 72
	maxPerimeter   = 1300
	// warnWeight is the weight a warned direction adds to its sector (half
	// to each neighbour, a sixth to the next), against 1000 for any owned
	// sector; histWeight is the weight the whole attacker history spreads.
	warnWeight = 5000
	histWeight = 6000
)

// faceEconomy points the board's enemy estimate — which the utility layers
// orient the base by — outward from the start site through this start, at
// a distance that keeps the zones compact. EnemyKnown stays false, so the
// utility's territory keeps its prior (the farthest start) rather than
// treating the ground behind the facing point as the enemy's.
func (st *state) faceEconomy(b *core.Board) {
	b.EnemyX, b.EnemyZ = st.ex, st.ez
	b.EnemyKnown = false
	b.HomeX, b.HomeZ = st.hx, st.hz
}

// The human's room: the metal spots within humanSpots of the human's start
// and nearer it than this survivor's are the human's for good — an
// extractor of the buddy's there is a building a wave goes for, and it
// brings the fight to the human's commander. The other spots nearer the
// human's start stay reserved for its opening, reserveTicks; after that a
// spot still free is anyone's (income is split evenly, DESIGN_SURVIVAL
// §4.3, so whoever builds the extractor, the team gains).
const (
	reserveTicks = 3 * 1800
	humanSpots   = 480
	// spotTaken is how near a spot's centre an allied extractor must stand
	// to take it.
	spotTaken = 40
)

// reserveSpots holds back the metal spots that are another survivor's to
// take: the human's (above), for good the spots nearer the other buddy's
// start than this one's (both buddies are survival brains and would
// otherwise race for them), and any spot an allied extractor already
// stands on (the board's picture of the spots holds only the
// survivor's own buildings and its enemies').
func (st *state) reserveSpots(b *core.Board) {
	m := b.K.Map
	sc := &st.sc
	for i := range m.Spots {
		if b.Spots[i] != core.SpotFree {
			continue
		}
		sp := &m.Spots[i]
		taken := false
		for _, e := range st.ally.ext {
			if aikit.Dist2(e[0], e[1], sp.X, sp.Z) <= spotTaken*spotTaken {
				taken = true
				break
			}
		}
		if taken {
			b.Spots[i] = core.SpotHeld
			continue
		}
		mine := aikit.Dist2(sp.X, sp.Z, st.hx, st.hz)
		for j, p := range sc.Team {
			if p == sc.Me || j >= len(sc.Starts) {
				continue
			}
			d := aikit.Dist2(sp.X, sp.Z, sc.Starts[j][0], sc.Starts[j][1])
			if d >= mine {
				continue
			}
			buddy := j < len(sc.Computer) && sc.Computer[j]
			if buddy || b.Tick < reserveTicks || d <= humanSpots*humanSpots {
				b.Spots[i] = core.SpotHeld
				break
			}
		}
	}
}

// Report implements aikit.Reporter: the utility strategy's own counters,
// then the survival layer's.
func (s *Strategy) Report(add func(name string, value int64)) {
	if r, ok := s.inner.(aikit.Reporter); ok {
		r.Report(add)
	}
	c := &s.st.stat
	add("sv_towers", int64(c.towers))
	add("sv_aa", int64(c.aa))
	add("sv_walls", int64(c.walls))
	add("sv_repairs", int64(c.repairs))
	add("sv_ally_repairs", int64(c.allyRepairs))
	add("sv_blast_moves", int64(c.blastMoves))
	add("sv_refuges", int64(c.refuges))
	add("sv_tower_fails", int64(c.towerFails))
	add("sv_wall_fails", int64(c.wallFails))
}

// Explain implements core.Explaining.
func (s *Strategy) Explain(b *core.Board, x *aikit.Explain) {
	if e, ok := s.inner.(core.Explaining); ok {
		e.Explain(b, x)
	}
	s.st.explain(x)
}
