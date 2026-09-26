package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Builders the survival layer takes. The utility economy decides every
// builder it is shown; the survival layer hides the ones it has taken from
// it for the length of their job (Economy), so no builder is given two
// orders in one think. A job ends when its builder is idle again: the
// building stands (or failed to place, which the next think sees as an idle
// builder with no frame), the repair is done, or the refuge reached.

type jobKind uint8

const (
	jobNone jobKind = iota
	jobTower
	jobWall
	jobRepair
	jobRefuge
)

type job struct {
	kind   jobKind
	who    handleGen
	sector int
	prod   *aikit.UnitInfo
	x, z   int32
	since  uint32
	framed bool // the builder has been seen building since the order
	issued bool // the order has been emitted
	// pts are a wall segment's pieces.
	pts    [wallPieces][2]int32
	npts   int
	target handleGen
	// ally marks a repair of another survivor's building (Obs.Allies).
	ally bool
}

type jobs struct {
	list []job
	// hidden is the scratch the economy's builder list is filtered into.
	hidden []int32
	// rests are the builders' failure records, by handle.
	rests []builderRest
	// blocked are the repair targets a builder could not start on, with
	// the tick until which none is sent to them again.
	blocked []repairBlock
	pts     [][2]int32
	one     [1]pool.Handle
}

// jobTimeout ends a job whose builder never started it (walking into a
// blocked site, a stale order) so the builder returns to the economy;
// buildTimeout ends any job, however long its building takes.
const (
	jobTimeout   = 90 * 30
	buildTimeout = 360 * 30
)

// claimed reports whether own unit u is on a survival job.
func (js *jobs) claimed(u *aikit.OwnUnit) bool {
	for i := range js.list {
		if js.list[i].who.h == u.H && js.list[i].who.gen == u.Gen {
			return true
		}
	}
	return false
}

// refresh ends finished jobs. A job's builder that was never seen building
// and is idle again failed to place its building there.
func (st *state) refreshJobs(b *core.Board) {
	js := &st.jobs
	kept := js.list[:0]
	for _, j := range js.list {
		i := b.Index(j.who.h)
		if i < 0 || b.O.Own[i].Gen != j.who.gen {
			continue // the builder died
		}
		u := &b.O.Own[i]
		if !j.issued {
			if b.Tick-j.since <= jobTimeout {
				kept = append(kept, j)
			}
			continue
		}
		switch j.kind {
		case jobTower, jobWall:
			if u.Order == aikit.OrderBuild {
				j.framed = true
			}
			if u.Order == aikit.OrderIdle && b.Tick > j.since {
				if !j.framed {
					st.rest(u, b.Tick)
				} else {
					st.rested(u)
				}
				if !j.framed && j.sector >= 0 {
					st.defense.failed[j.sector] = min(st.defense.failed[j.sector]+1, 4)
					st.defense.failAt[j.sector] = b.Tick
					st.stat.lastFails[st.stat.nFails%len(st.stat.lastFails)] = j
					st.stat.nFails++
					if j.kind == jobTower {
						st.stat.towerFails++
					} else {
						st.stat.wallFails++
					}
				}
				continue
			}
		case jobRepair:
			hp, maxHP, seen := int32(0), int32(0), false
			if j.ally {
				if t := allyAt(b.O, j.target.h, j.target.gen); t != nil {
					hp, maxHP, seen = t.HP, t.MaxHP, true
				}
			} else if t := b.Index(j.target.h); t >= 0 && b.O.Own[t].Gen == j.target.gen {
				hp, maxHP, seen = b.O.Own[t].HP, b.O.Own[t].MaxHP, true
			}
			if u.Order == aikit.OrderRepair || u.Order == aikit.OrderBuild {
				j.framed = true
			}
			if u.Order == aikit.OrderIdle && b.Tick > j.since && seen && hp < maxHP && !j.framed {
				// The order ended before the repair began (a target the
				// builder cannot reach, say): neither is asked again for a
				// while, or the next think would order the same repair.
				st.rest(u, b.Tick)
				st.blockRepair(j.target, b.Tick)
				continue
			}
			if u.Order == aikit.OrderIdle && b.Tick > j.since || !seen || hp >= maxHP {
				continue
			}
		case jobRefuge:
			if b.Tick-j.since >= refugeTicks {
				continue
			}
		}
		// A job whose builder never started it ends after jobTimeout; one
		// under way runs until its builder is idle, at most buildTimeout.
		if !j.framed && b.Tick-j.since > jobTimeout || b.Tick-j.since > buildTimeout {
			continue
		}
		kept = append(kept, j)
	}
	js.list = kept
}

// hideClaimed filters the builders on survival jobs out of the board's
// builder list, returning the full list to restore.
func (st *state) hideClaimed(b *core.Board) []int32 {
	all := b.Builders
	js := &st.jobs
	js.hidden = js.hidden[:0]
	for _, i := range all {
		if !js.claimed(&b.O.Own[i]) {
			js.hidden = append(js.hidden, i)
		}
	}
	b.Builders = js.hidden
	return all
}

// budgetLeft is how many more commands this think may emit without the
// batch dropping any: the survival layer takes only what the other layers
// left, keeping reserve for the army and production that follow.
func budgetLeft(k *aikit.Kit, reserve int) int {
	if k.Budget < 0 {
		return 1 << 20
	}
	return int(k.Budget) - k.Emitted() - reserve
}

// plan decides the survival jobs this think owes — first the commander's
// refuge, then towers, repairs and walls — each for the constructor
// nearest its site that is on no job, within the share of constructors the
// layer may hold. It runs before the utility economy, which is then shown
// only the builders left; emit issues the orders after it, from the action
// budget it left.
func (st *state) plan(b *core.Board) {
	js := &st.jobs
	st.defense.fade(b.Tick)
	st.towerWant()
	st.refuge(b)
	cons := int64(b.Constructor)
	share := int64(st.p.ClaimShare)
	if len(st.live) > 0 {
		share *= 2
	}
	// At least one while there is a constructor: rounding down kept every
	// survivor with one or two constructors from building a single tower
	// of its own, and a lone buddy then fell to the fourth or fifth wave.
	limit := int((cons*share + 99) / 100)
	held, fresh := 0, 0
	for i := range js.list {
		if js.list[i].kind != jobRefuge {
			held++
		}
	}
	for held < limit && fresh < maxFresh {
		if !st.planOne(b) {
			break
		}
		held++
		fresh++
	}
	// The economy keeps its constructors busy: when a tower is owed and the
	// layer holds no builder, the constructor whose work is cheapest to
	// break off (not a factory or a tower) is taken for it.
	if held == 0 && limit > 0 && b.Tick >= towerStart {
		st.takeBusy = true
		if st.planOne(b) {
			held++
		}
		st.takeBusy = false
	}
	if cons == 0 && held == 0 && b.Tick >= commanderTowers {
		st.planCommanderTower(b)
	}
}

// commanderTowers is the tick from which a survivor with no constructor
// lets its commander build towers: the opening is the economy's.
const commanderTowers = 6 * 1800

// planCommanderTower gives the commander a tower job when the survivor has
// no constructor at all (a poor map, or all of them lost): only while it
// is free (or at work the economy is not paying for: both stores at least
// four-fifths full; or walking to a factory it has not begun while they
// have stood so, stalledWalk), no attacker is near it, both stores can
// fund the tower (holds; so the economy is not starved of what it is
// building) and the site is within comLeash of its start.
func (st *state) planCommanderTower(b *core.Board) {
	st.stat.comWhy = "no commander"
	if b.Commander < 0 {
		return
	}
	u := &b.O.Own[b.Commander]
	banked := st.bankedSince != 0
	st.stat.comWhy = "busy"
	if st.jobs.claimed(u) || st.resting(u, b.Tick) || !freeOrder(u) && !(banked && interruptible(u)) && !st.stalledWalk(b, u) {
		return
	}
	o := b.O
	st.stat.comWhy = "attackers near"
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if (c.Info == nil || c.Info.DPS > 0) && aikit.Dist2(c.X, c.Z, u.X, u.Z) <= refugeNear*refugeNear {
			return
		}
	}
	st.stat.comWhy = "nothing owed"
	s, prod, x, z, ok := st.nextTower(b, u)
	if !ok {
		return
	}
	st.stat.comWhy = "stores"
	if !holds(b.Metal, prod.Metal) || !holds(b.Energy, prod.Energy) {
		return
	}
	st.stat.comWhy = "beyond leash"
	if aikit.Dist2(x, z, st.hx, st.hz) > comLeash*comLeash {
		return
	}
	st.stat.comWhy = "ordered"
	st.jobs.list = append(st.jobs.list, job{kind: jobTower, who: handleGen{u.H, u.Gen}, sector: s, prod: prod, x: x, z: z, since: b.Tick})
}

// stalledWalk reports a commander sent to build a factory that has not
// begun it while the stores have stood full for bankedLong: on a cramped
// start the only factory site may lie across the map, and the commander,
// pulled back by every wave, never gets there. Breaking the walk off for a
// tower at home loses nothing that was being built.
func (st *state) stalledWalk(b *core.Board, u *aikit.OwnUnit) bool {
	if st.bankedSince == 0 || b.Tick-st.bankedSince < bankedLong || u.Order != aikit.OrderBuild || u.Target == nil || !u.Target.Role.Has(aikit.RoleFactory) {
		return false
	}
	for _, i := range b.Frames {
		if b.O.Own[i].Info == u.Target {
			return false
		}
	}
	return true
}

// holds reports whether a store can fund a cost of c: half as much again
// in stock, or the store four-fifths full (a small store cannot hold a
// tower's energy, which it draws over the build).
func holds(r aikit.Res, c int32) bool {
	return int64(r.Stock)*2 >= int64(c)*3 || int64(r.Stock)*5 >= int64(r.Cap)*4
}

// bankedLong is how long both stores must have stood four-fifths full
// before a commander's factory walk counts as stalled.
const bankedLong = 90 * 30

// maxFresh is how many new jobs one think may start.
const maxFresh = 2

// planOne decides one job for one free constructor; false when nothing is
// owed or no constructor is free.
func (st *state) planOne(b *core.Board) bool {
	o := b.O
	js := &st.jobs
	if st.freeBuilder(b) < 0 {
		return false
	}
	// The first free constructor that makes a land tower plans it (a ship
	// makes none, and would otherwise keep every tower waiting).
	if bi := st.freeBuilderThat(b, func(u *aikit.OwnUnit) bool { return st.pickTower(b, u.Info, false) != nil }); bi >= 0 {
		if s, prod, x, z, ok := st.nextTower(b, &o.Own[bi]); ok {
			u := &o.Own[bi]
			// The free constructor nearest the site builds it.
			if nb := st.freeBuilderNear(b, x, z); nb >= 0 {
				aa := st.tab.of(prod).aa && !st.tab.of(prod).tower
				if p := st.pickTower(b, o.Own[nb].Info, aa); p != nil {
					u, prod = &o.Own[nb], p
				}
			}
			js.list = append(js.list, job{kind: jobTower, who: handleGen{u.H, u.Gen}, sector: s, prod: prod, x: x, z: z, since: b.Tick})
			return true
		}
	}
	if t, ok := st.repairTarget(b); ok {
		// The free constructor nearest the building that can reach it: a
		// ship cannot repair a building ashore, nor a land unit one afloat.
		if nb := st.freeRepairerNear(b, &t); nb >= 0 {
			u := &o.Own[nb]
			js.list = append(js.list, job{kind: jobRepair, who: handleGen{u.H, u.Gen}, sector: -1, since: b.Tick, target: t.who, ally: t.ally, x: t.x, z: t.z})
			return true
		}
	}
	wb := st.freeBuilderThat(b, func(u *aikit.OwnUnit) bool { return st.wallPiece(u.Info) != nil })
	if wb < 0 {
		return false
	}
	u := &o.Own[wb]
	if s, wp, ok := st.nextWall(b, u); ok {
		pts := st.wallSite(b, s, js.pts[:0])
		js.pts = pts
		st.defense.wallAt[s] = b.Tick
		if len(pts) == 0 {
			return false
		}
		if nb := st.freeBuilderNear(b, pts[0][0], pts[0][1]); nb >= 0 && o.Own[nb].Info.Buildable(wp) {
			u = &o.Own[nb]
		}
		st.defense.wallN[s]++
		j := job{kind: jobWall, who: handleGen{u.H, u.Gen}, sector: s, prod: wp, x: pts[0][0], z: pts[0][1], since: b.Tick}
		j.npts = copy(j.pts[:], pts)
		js.list = append(js.list, j)
		return true
	}
	return false
}

// emit issues the orders of jobs not yet issued, oldest first, while the
// action budget the other layers left allows; a job that does not fit
// waits for the next think with its builder still held.
func (st *state) emit(b *core.Board) {
	js := &st.jobs
	k := b.K
	for i := range js.list {
		j := &js.list[i]
		if j.issued {
			continue
		}
		ui := b.Index(j.who.h)
		if ui < 0 {
			continue
		}
		u := &b.O.Own[ui]
		switch j.kind {
		case jobTower:
			if budgetLeft(k, 1) <= 0 {
				return
			}
			k.BuildKeep(u.H, j.prod, j.x, j.z, -1, 2)
			st.stat.towers++
			if st.tab.of(j.prod).aa {
				st.stat.aa++
			}
		case jobWall:
			pts := j.pts[:j.npts]
			if budgetLeft(k, 1) < len(pts) {
				return
			}
			for n, pt := range pts {
				k.Build(u.H, j.prod, pt[0], pt[1], -1, 0, n > 0)
			}
			st.stat.walls++
		case jobRepair:
			if budgetLeft(k, 1) <= 0 {
				return
			}
			js.one[0] = u.H
			k.Repair(js.one[:], j.target.h, false)
			st.stat.repairs++
			if j.ally {
				st.stat.allyRepairs++
			}
		case jobRefuge:
			if budgetLeft(k, 0) <= 0 {
				return
			}
			js.one[0] = u.H
			k.Move(js.one[:], j.x, j.z, false)
			st.stat.refuges++
		}
		j.issued = true
		j.since = b.Tick
	}
}

// freeBuilder returns a built constructor (not the commander) that is idle
// or only assisting and on no survival job; -1 when none.
func (st *state) freeBuilder(b *core.Board) int32 {
	for _, i := range b.Builders {
		if st.free(b, i) {
			return i
		}
	}
	return -1
}

// freeBuilderThat is the first free constructor for which ok holds, -1
// when none.
func (st *state) freeBuilderThat(b *core.Board, ok func(u *aikit.OwnUnit) bool) int32 {
	for _, i := range b.Builders {
		if st.free(b, i) && ok(&b.O.Own[i]) {
			return i
		}
	}
	return -1
}

// freeBuilderNear is the free constructor nearest (x, z), -1 when none.
func (st *state) freeBuilderNear(b *core.Board, x, z int32) int32 {
	best := int32(-1)
	var bestD int64
	for _, i := range b.Builders {
		if !st.free(b, i) {
			continue
		}
		u := &b.O.Own[i]
		if d := aikit.Dist2(u.X, u.Z, x, z); best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func (st *state) free(b *core.Board, i int32) bool {
	u := &b.O.Own[i]
	if u.Info.Role.Has(aikit.RoleCommander) || st.jobs.claimed(u) || st.resting(u, b.Tick) {
		return false
	}
	return freeOrder(u) || st.takeBusy && interruptible(u)
}

// interruptible reports whether a constructor's own work may be broken
// off for a tower: anything but a factory or a tower of its own, whose
// frame the economy would leave standing half built.
func interruptible(u *aikit.OwnUnit) bool {
	switch u.Order {
	case aikit.OrderReclaim, aikit.OrderRepair, aikit.OrderMove:
		return true
	case aikit.OrderBuild:
		return u.Target == nil || !u.Target.Role.Any(aikit.RoleFactory|aikit.RoleDefense)
	}
	return false
}

// freeOrder reports whether u's current order may be taken over: idle,
// guarding or patrolling, or assisting someone else's construction.
func freeOrder(u *aikit.OwnUnit) bool {
	switch u.Order {
	case aikit.OrderIdle, aikit.OrderGuard, aikit.OrderPatrol:
		return true
	case aikit.OrderBuild, aikit.OrderRepair:
		// An assisting builder may be taken; one building its own frame is
		// the economy's. A builder's own construction is its Target; an
		// assist has none of its own, so any builder with no target counts.
		return u.Target == nil
	}
	return false
}

// repairPick is a building to repair: this survivor's own, or an allied
// one (another survivor's, seen in Obs.Allies).
type repairPick struct {
	who   handleGen
	x, z  int32
	ally  bool
	water bool // it stands in water (its definition's minimum depth)
}

// repairBlock holds a repair target back until a tick.
type repairBlock struct {
	who   handleGen
	until uint32
}

// repairBlockTicks is how long a building a builder could not start
// repairing is left alone.
const repairBlockTicks = 60 * 30

// blockRepair holds target back for repairBlockTicks, dropping lapsed holds.
func (st *state) blockRepair(target handleGen, tick uint32) {
	js := &st.jobs
	kept := js.blocked[:0]
	for _, r := range js.blocked {
		if r.until > tick && r.who != target {
			kept = append(kept, r)
		}
	}
	js.blocked = append(kept, repairBlock{who: target, until: tick + repairBlockTicks})
}

// repairBlocked reports whether target is held back at tick.
func (st *state) repairBlocked(target handleGen, tick uint32) bool {
	for _, r := range st.jobs.blocked {
		if r.who == target && r.until > tick {
			return true
		}
	}
	return false
}

// canReach reports whether a builder of this kind can come within reach
// of a building on land (water false) or afloat (water true).
func canReach(builder *aikit.UnitInfo, water bool) bool {
	switch r := builder.Role; {
	case r.Any(aikit.RoleAir | aikit.RoleHover | aikit.RoleAmphibious):
		return true
	case r.Has(aikit.RoleNaval):
		return water
	}
	return !water
}

// freeRepairerNear is the free constructor nearest t that can reach it,
// -1 when none.
func (st *state) freeRepairerNear(b *core.Board, t *repairPick) int32 {
	best := int32(-1)
	var bestD int64
	for _, i := range b.Builders {
		u := &b.O.Own[i]
		if !st.free(b, i) || !canReach(u.Info, t.water) {
			continue
		}
		if d := aikit.Dist2(u.X, u.Z, t.x, t.z); best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// repairTarget is the most valuable damaged building (below 70 % of its
// hit points) with no attacker near it — this survivor's own, or another
// survivor's within the team's ground (maxPerimeter of the site); false
// when none. Towers and factories come first. An allied building is
// repaired as an own one is: the repair order takes any target its
// admission passes, with no ownership test [04 R-ORD-02 §1], and the repair
// bills the repairer's energy [05 R-WORK-01 §3].
func (st *state) repairTarget(b *core.Board) (repairPick, bool) {
	o := b.O
	var best repairPick
	var bestV int64
	found := false
	consider := func(who handleGen, info *aikit.UnitInfo, x, z, hp, maxHP int32, ally bool) {
		if hp*10 >= maxHP*7 || maxHP <= 0 {
			return
		}
		if b.Threat.At(x, z) > 0 && b.Threat.At(x, z) > b.OwnPower.At(x, z) {
			return
		}
		if st.repairing(who) || st.repairBlocked(who, b.Tick) {
			return
		}
		v := int64(info.Value) * int64(maxHP-hp) / int64(maxHP)
		if st.tab.of(info).tower || info.Role.Has(aikit.RoleFactory) {
			v *= 2
		}
		if !found || v > bestV {
			best, bestV, found = repairPick{who: who, x: x, z: z, ally: ally, water: info.Def != nil && info.Def.MinWaterDepth > 0}, v, true
		}
	}
	for i := range o.Own {
		u := &o.Own[i]
		if u.Built && !u.Info.Role.Has(aikit.RoleMobile) {
			consider(handleGen{u.H, u.Gen}, u.Info, u.X, u.Z, u.HP, u.MaxHP, false)
		}
	}
	for i := range o.Allies {
		u := &o.Allies[i]
		if !u.Built || u.Info.Role.Has(aikit.RoleMobile) || aikit.Dist2(u.X, u.Z, st.cx, st.cz) > maxPerimeter*maxPerimeter {
			continue
		}
		consider(handleGen{u.H, u.Gen}, u.Info, u.X, u.Z, u.HP, u.MaxHP, true)
	}
	return best, found
}

// repairing reports whether a repair job already names the unit.
func (st *state) repairing(who handleGen) bool {
	for i := range st.jobs.list {
		j := &st.jobs.list[i]
		if j.kind == jobRepair && j.target == who {
			return true
		}
	}
	return false
}

// refugeTicks is how long the commander is kept from the economy once sent
// behind the towers.
const refugeTicks = 450

// refuge keeps the commander, whose death takes the survivor's whole base
// with it — and whose death explosion takes any commander near it — out of
// the waves' way. When the armed attackers near it are more than half the
// strength of the commander itself, the survivor's units and its towers
// there, or it is hurt with attackers near, it is sent away from them
// around its home, never toward the human's commander. (A refuge behind the
// defence centre, between the two starts, put a dying buddy commander
// beside the human's, and its explosion ended the battle.)
func (st *state) refuge(b *core.Board) {
	if b.Commander < 0 {
		return
	}
	u := &b.O.Own[b.Commander]
	if st.jobs.claimed(u) {
		return
	}
	var ex, ez, en, enemy int64
	o := b.O
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info != nil && c.Info.DPS == 0 {
			continue
		}
		if aikit.Dist2(c.X, c.Z, u.X, u.Z) > refugeNear*refugeNear {
			continue
		}
		s := int64(2000) // a radar blip: a modest unknown
		if c.Info != nil {
			s = c.Info.Strength()
		}
		enemy += s
		ex += int64(c.X)
		ez += int64(c.Z)
		en++
	}
	m := b.K.Map
	if en > 0 {
		// The commander fights too: early waves are a few units it beats.
		own := u.Info.Strength() * int64(u.HP) / int64(max(u.MaxHP, 1))
		for _, i := range b.Combat {
			v := &o.Own[i]
			if aikit.Dist2(v.X, v.Z, u.X, u.Z) <= refugeNear*refugeNear {
				own += v.Info.Strength()
			}
		}
		for _, i := range b.Defenses {
			v := &o.Own[i]
			if aikit.Dist2(v.X, v.Z, u.X, u.Z) <= refugeNear*refugeNear {
				own += v.Info.Strength()
			}
		}
		hurt := u.HP*100 < u.MaxHP*60
		// Another survivor's commander beside it while the attackers near
		// it are a real threat (more than a quarter of the strength about
		// it) or it is wounded: were it to die here, its explosion would
		// take that one with it, and the human's ends the battle. A
		// commander that is not in danger stays: it is often what saves
		// the human's commander from a raider at the start site.
		crowd := st.nearAllyCommander(u.X, u.Z, blastKeep) && (enemy*4 > own || u.HP*100 < u.MaxHP*80)
		if enemy*2 > own || hurt || crowd {
			// Away from the attackers, around its home, and never toward
			// the human's commander: of eight points refugeBack from home,
			// the one farthest from the attackers' centroid that stands
			// blastClear from the site and blastKeep from every allied
			// commander, on the home's ground; twice as far out when none
			// does (an allied commander at its home).
			tx, tz := int32(ex/en), int32(ez/en)
			rx, rz, best := st.hx, st.hz, int64(-1)
			for back := int64(refugeBack); back <= 2*refugeBack && best < 0; back += refugeBack {
				for k := range 8 {
					dir := sectorDir[k*2]
					x := clampWorld(int64(st.hx)+dir[0]*back/1000, m.WorldW)
					z := clampWorld(int64(st.hz)+dir[1]*back/1000, m.WorldH)
					if aikit.Dist2(x, z, st.cx, st.cz) < blastClear*blastClear || !st.reachable(x, z) || st.nearAllyCommander(x, z, blastKeep) {
						continue
					}
					if d := aikit.Dist2(x, z, tx, tz); d > best {
						rx, rz, best = x, z, d
					}
				}
			}
			if aikit.Dist2(u.X, u.Z, rx, rz) >= 160*160 {
				st.jobs.list = append(st.jobs.list, job{kind: jobRefuge, who: handleGen{u.H, u.Gen}, sector: -1, x: rx, z: rz, since: b.Tick})
				if crowd && !hurt && enemy*2 <= own {
					st.stat.blastMoves++
				}
				return
			}
		}
	}
	// Home guard: while a warned wave is due or armed attackers are about,
	// the commander stays within comLeash of its home, among its own
	// towers and units rather than out on an extractor run. Not before the
	// first factory stands: on a cramped start the only factory site may
	// lie across the map, and the leash would keep it from ever being
	// built (The Pass).
	if len(b.Factories) == 0 || aikit.Dist2(u.X, u.Z, st.hx, st.hz) <= comLeash*comLeash {
		return
	}
	alert := false
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if (c.Info == nil || c.Info.DPS > 0) && aikit.Dist2(c.X, c.Z, u.X, u.Z) <= leashAlert*leashAlert {
			alert = true
			break
		}
	}
	for i := range st.warn {
		if w := &st.warn[i]; b.Tick+leashWarn >= w.Arrive && b.Tick <= w.Arrive+liveAfter {
			alert = true
		}
	}
	if alert {
		st.jobs.list = append(st.jobs.list, job{kind: jobRefuge, who: handleGen{u.H, u.Gen}, sector: -1, x: st.hx, z: st.hz, since: b.Tick})
	}
}

// Commander safety, world units and ticks: attackers within refugeNear
// count against it; it is sent refugeBack from its home; while
// a wave is due within leashWarn or attackers are within leashAlert of it,
// and when it builds a tower, it keeps within comLeash of its start.
const (
	refugeNear = 650
	refugeBack = 300
	comLeash   = 700
	leashAlert = 1300
	leashWarn  = 900
)

// A builder whose job ended before it ever started building — the site
// would not take the building, or the builder cannot get there (boxed in
// by rows of buildings, say) — is left to the economy for a while before
// the survival layer asks it again: restTicks after one such failure,
// restLong after restStreak in a row.
const (
	restTicks  = 900
	restLong   = 5400
	restStreak = 3
)

// builderRest is one builder's failure record, indexed by handle.
type builderRest struct {
	gen    uint32
	until  uint32
	streak int32
}

func (st *state) restOf(u *aikit.OwnUnit) *builderRest {
	h := int(u.H)
	for len(st.jobs.rests) <= h {
		st.jobs.rests = append(st.jobs.rests, builderRest{})
	}
	r := &st.jobs.rests[h]
	if r.gen != u.Gen {
		*r = builderRest{gen: u.Gen}
	}
	return r
}

// rest records a job of u's that failed at tick.
func (st *state) rest(u *aikit.OwnUnit, tick uint32) {
	r := st.restOf(u)
	r.streak++
	r.until = tick + restTicks
	if r.streak >= restStreak {
		r.until = tick + restLong
	}
}

// rested clears u's failure streak once a job of its started.
func (st *state) rested(u *aikit.OwnUnit) { st.restOf(u).streak = 0 }

// resting reports whether u is left to the economy after failures.
func (st *state) resting(u *aikit.OwnUnit, tick uint32) bool {
	h := int(u.H)
	if h >= len(st.jobs.rests) {
		return false
	}
	r := &st.jobs.rests[h]
	return r.gen == u.Gen && tick < r.until
}
