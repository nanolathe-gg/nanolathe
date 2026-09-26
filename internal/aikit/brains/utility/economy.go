package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// cand is one scored option. f holds the main considerations (permille)
// so Explain can show why it scored as it did.
type cand struct {
	kind    commitKind
	prod    *aikit.UnitInfo
	spot    int32
	x, z    int32
	target  pool.Handle
	spacing int32
	score   int64
	gainM   int64
	gainE   int64
	f       [4]int64
}

const topK = 5

// decision is a record of one scored choice, kept in a ring for Explain.
type decision struct {
	tick   uint32
	who    *aikit.UnitInfo
	x, z   int32
	n      int
	top    [topK]cand
	chosen int8
	note   uint8
}

const (
	noteAssign uint8 = iota
	noteKeep
	noteNothing
)

func (d *decision) reset(tick uint32, who *aikit.UnitInfo, x, z int32) {
	d.tick, d.who, d.x, d.z, d.n, d.chosen, d.note = tick, who, x, z, 0, -1, noteNothing
}

// offer inserts c into the sorted top list.
func (d *decision) offer(c *cand) {
	if c.score <= 0 {
		return
	}
	pos := d.n
	for pos > 0 && d.top[pos-1].score < c.score {
		pos--
	}
	if pos >= topK {
		return
	}
	end := d.n
	if end >= topK {
		end = topK - 1
	}
	for j := end; j > pos; j-- {
		d.top[j] = d.top[j-1]
	}
	d.top[pos] = *c
	if d.n < topK {
		d.n++
	}
}

// makerStallRest is how long after an energy stall no metal maker is
// started (layout or tech switch): makers and extractors stop together in a
// stall, so more makers only deepen it.
const makerStallRest = 2700

// minScore is the least a choice must score (nominal = 1000) to be worth an
// action rather than leaving the builder idle.
const minScore = 30

// reassessTicks is how often an assisting builder reconsiders.
const reassessTicks = 300

// retreatTicks is how long a retreat order stands before the builder is
// reconsidered (and re-retreated if still in danger).
const retreatTicks = 450

// Economy assigns idle builders by scoring every candidate build.
type Economy struct {
	s     *shared
	one   [1]pool.Handle
	ring  [8]decision
	next  int
	rot   int
	fails int32
	// scratch per decision
	bestQ, bestDef int64
	bestQR         int64   // best reach-weighted factory quality on offer (switches on)
	def            defPlan // the proactive defense plan's state (defense*.go)
	// fac_backoff: factory site searches that found no site lately.
	facOff     [facOffSlots]facBackoff
	facOffNext int
}

// facBackoff is a factory placement whose site search found no site: the
// product and the request point, not asked for again before until
// (fac_backoff).
type facBackoff struct {
	prod  *aikit.UnitInfo
	x, z  int32
	until uint32
}

const (
	// facBackoffTicks is how long a factory request point whose search
	// found no site rests (20 s). A failed search tries up to thirteen
	// thousand anchors, 1-2 ms of the simulation thread. The failure
	// blocks in fail (the product for 300 ticks after its first failure,
	// the builder's product for 450) cover only a builder left idle and
	// only that product: another factory at the same point in the same
	// think, another builder, or a builder that carried on with its
	// earlier order asked again, most often within a second.
	facBackoffTicks = 600
	// facOffSlots is how many failed request points are remembered.
	facOffSlots = 8
)

func (e *Economy) Init(b *core.Board) { e.s.setup(b.K) }

func (e *Economy) Plan(b *core.Board) {
	s := e.s
	o := b.O
	// Keep one action for the army and one per factory that will want one.
	budget := int(s.budget) - 1
	nf := 0
	for _, fi := range b.Factories {
		if o.Own[fi].QueueLen < 2 && nf < 2 {
			nf++
		}
	}
	budget -= nf
	if att := int(b.K.Persona.Attention); budget > att {
		budget = att
	}
	// A build issued last think that left its builder idle failed to place.
	// When the batch it went out in reports a search that found no site, a
	// factory's request point rests (fac_backoff), also when its builder
	// carried on with what it was doing (a failed build replaces no order):
	// it is not building that factory.
	noSite := b.K.Last.Reasons[aikit.FailNoSite] > 0
	for _, i := range b.Builders {
		u := &o.Own[i]
		c := s.commitIf(u)
		if c == nil || !c.kind.build() || c.tick != s.lastThink || c.tick == 0 {
			continue
		}
		if noSite && c.kind == cFactory && (u.Order == aikit.OrderIdle || u.Target != c.prod) {
			e.backOff(c)
		}
		bs := s.bstateOf(u)
		if u.Order != aikit.OrderIdle {
			bs.streak = 0
			continue
		}
		sp := e.fail(c, bs)
		if sp < 0 {
			continue
		}
		if s.xpart(xHold) {
			e.holdFailed(b, sp) // expand: an unseen building holds the spot
		}
		if budget > 0 && e.clearSpot(b, u, c, sp) {
			budget--
		}
	}
	if s.p.DefPlan != 0 {
		// The defense plan follows the game every think (losses, sightings,
		// flow, budget), not only when a builder prices a tower; after the
		// failures above, which it reads.
		e.plan().refresh(s, b)
	}
	n := len(b.Builders)
	if n == 0 {
		return
	}
	for j := 0; j < n && budget > 0; j++ {
		if e.retreat(b, b.Builders[j]) {
			budget--
		}
	}
	if s.p.Layout != 0 {
		e.clearFailures(b)
		if budget > 0 {
			budget -= e.unblock(b, budget)
		}
	}
	e.rot++
	for j := 0; j < n && budget > 0; j++ {
		i := b.Builders[(j+e.rot)%n]
		u := &o.Own[i]
		c := s.commitOf(u)
		if c.kind != cNone && c.tick == s.tick {
			// Ordered already this think (a retreat, an exit to open, a
			// spot to clear): a second order would replace it.
			continue
		}
		reassess := false
		switch {
		case u.Order == aikit.OrderIdle:
		case c.kind == cRetreat && s.tick-c.tick >= retreatTicks:
			// Arrived, or the way home is blocked: back to work.
		case (c.kind == cGuard || c.kind == cAssist) && s.tick-c.tick >= reassessTicks:
			reassess = true
		default:
			continue
		}
		if e.decide(b, u, c, reassess) {
			budget--
		}
	}
}

// fail records a build that left its builder idle. The builder avoids that
// product or spot for a while; a builder that keeps failing is probably
// boxed in and only assists for a minute. Only a first failure counts
// against the spot or the product's field, since a boxed-in builder says
// nothing about the site. It returns the metal spot blamed, -1 for none.
func (e *Economy) fail(c *commitment, bs *builderState) int32 {
	s := e.s
	e.fails++
	bs.blockProd, bs.blockSpot, bs.blockUntil = c.prod, c.spot, s.tick+450
	if bs.streak < 250 {
		bs.streak++
	}
	if bs.streak >= 3 {
		bs.stuckUntil = s.tick + 1800
	}
	if bs.streak > 1 {
		c.kind = cNone
		return -1
	}
	sp := int32(-1)
	if (c.kind == cMex || c.kind == cUpgrade) && c.spot >= 0 && int(c.spot) < len(s.spotBlock) {
		// Blocked for a minute per failure there; the count resets once a
		// frame of ours stands on the spot (observe).
		f := s.spotFails[c.spot]
		if f < 8 {
			f++
		}
		s.spotFails[c.spot] = f
		s.spotBlock[c.spot] = s.tick + 1800*uint32(f)
		sp = c.spot
	} else if c.prod != nil {
		idx := c.prod.Index
		if s.prodFails[idx] < 250 {
			s.prodFails[idx]++
		}
		s.prodBlock[idx] = s.tick + 300
	}
	c.kind = cNone
	return sp
}

// backOff rests the request point of a factory placement that found no
// site (fac_backoff): the same product there, or with part 2 every
// factory there, is not scored until facBackoffTicks have passed.
func (e *Economy) backOff(c *commitment) {
	s := e.s
	if s.p.FacBackoff == 0 || c.prod == nil {
		return
	}
	slot := -1
	for i := range e.facOff {
		f := &e.facOff[i]
		if f.prod == c.prod && f.x == c.x && f.z == c.z {
			slot = i // the same placement failed again: rest it anew
			break
		}
		if slot < 0 && f.until <= s.tick {
			slot = i
		}
	}
	if slot < 0 {
		slot = e.facOffNext
		e.facOffNext = (e.facOffNext + 1) % facOffSlots
	}
	e.facOff[slot] = facBackoff{prod: c.prod, x: c.x, z: c.z, until: s.tick + facBackoffTicks}
}

// backedOff reports whether factory p at request point (x, z) rests
// (backOff).
func (e *Economy) backedOff(p *aikit.UnitInfo, x, z int32) bool {
	s := e.s
	if s.p.FacBackoff == 0 {
		return false
	}
	for i := range e.facOff {
		f := &e.facOff[i]
		if f.until > s.tick && f.x == x && f.z == z && (f.prod == p || s.p.FacBackoff >= 2) {
			return true
		}
	}
	return false
}

// spotClearR is the radius a failed extractor's builder clears around the
// spot: a wreck standing on the footprint has its anchor cell within it
// (up to a 4×4 wreck over a 2×2 extractor, diagonally).
const spotClearR = 96

// clearSpot sends the builder whose extractor failed to place at spot sp
// to reclaim what stands there (layout switch, which owns reclaiming),
// when the observation lists a reclaimable feature there that blocks or
// holds metal (Kit.Clear reclaims wrecks and other features holding metal,
// and blocking features in factory lanes); left alone, a wreck on a spot
// keeps failing it. Measured, a wreck is the rare cause: in three
// 40-minute games (comet catcher and sherwood mirrors, great divide
// against v1) every one of 213 failed extractor placements met a building
// already on the spot, mostly on the enemy's side, where an enemy
// extractor we had not seen stood; a Clear there finds nothing, so none is
// sent without a feature in view.
func (e *Economy) clearSpot(b *core.Board, u *aikit.OwnUnit, c *commitment, sp int32) bool {
	s := e.s
	if s.p.Layout == 0 || u.Info.Def == nil || !u.Info.Def.CanReclamate {
		return false
	}
	spot := &b.K.Map.Spots[sp]
	found := false
	for i := range b.O.Features {
		f := &b.O.Features[i]
		if f.Reclaimable && (f.Blocking || f.Metal > 0) && aikit.Dist2(f.X, f.Z, spot.X, spot.Z) <= spotClearR*spotClearR {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	b.K.Clear(u.H, spot.X, spot.Z, spotClearR)
	*c = commitment{def: u.Info, gen: u.Gen, kind: cClear, spot: sp, x: spot.X, z: spot.Z, tick: s.tick}
	d := e.record(u)
	d.note = noteAssign
	d.top[0] = cand{kind: cClear, spot: sp, x: spot.X, z: spot.Z, spacing: spotClearR, score: 1, f: [4]int64{int64(s.spotFails[sp]), 0, 0, 0}}
	d.n, d.chosen = 1, 0
	return true
}

// retreat pulls a builder out of danger toward the safe point behind home.
func (e *Economy) retreat(b *core.Board, i int32) bool {
	s := e.s
	u := &b.O.Own[i]
	c := s.commitOf(u)
	if c.kind == cRetreat && s.tick-c.tick < retreatTicks {
		return false
	}
	t := int64(b.Threat.At(u.X, u.Z))
	own := int64(b.OwnPower.At(u.X, u.Z))
	th := int64(s.p.ThreatHalf)
	var danger bool
	if u.Info.Role.Has(aikit.RoleCommander) {
		away := aikit.Dist2(u.X, u.Z, b.HomeX, b.HomeZ) > 350*350
		hurt := u.HP*100 < u.MaxHP*60
		danger = t >= th/2 && t > own && (away || hurt)
	} else {
		danger = t >= th && t > own*2
	}
	// Home is always reachable (the commander started there) and is where
	// the army defends.
	if !danger || aikit.Dist2(u.X, u.Z, b.HomeX, b.HomeZ) < 250*250 {
		return false
	}
	e.one[0] = u.H
	b.K.Move(e.one[:], b.HomeX, b.HomeZ, false)
	*c = commitment{def: u.Info, gen: u.Gen, kind: cRetreat, spot: -1, x: b.HomeX, z: b.HomeZ, tick: s.tick}
	d := e.record(u)
	d.note = noteAssign
	d.top[0] = cand{kind: cRetreat, x: b.HomeX, z: b.HomeZ, score: t, f: [4]int64{t, own}}
	d.n, d.chosen = 1, 0
	return true
}

func (e *Economy) record(u *aikit.OwnUnit) *decision {
	d := &e.ring[e.next]
	e.next = (e.next + 1) % len(e.ring)
	d.reset(e.s.tick, u.Info, u.X, u.Z)
	return d
}

// decide scores every option for one builder and issues the best.
func (e *Economy) decide(b *core.Board, u *aikit.OwnUnit, c *commitment, reassess bool) bool {
	s := e.s
	var d decision
	d.reset(s.tick, u.Info, u.X, u.Z)
	cur := int64(-1)
	bs := s.bstateOf(u)
	if bs.stuckUntil <= s.tick {
		if s.p.Naval != 0 || s.p.Tech != 0 {
			e.evalMexN(b, u, bs, &d)
		} else {
			e.evalMex(b, u, bs, &d)
		}
		e.evalBuildings(b, u, bs, &d)
	}
	e.evalAssist(b, u, c, &d, &cur)
	if s.p.Layout != 0 && bs.stuckUntil <= s.tick {
		e.evalClear(b, u, &d)
	}
	if bs.stuckUntil <= s.tick {
		e.evalOpenReclaim(b, u, &d) // open_reclaim (open_reclaim.go)
	}
	if d.n == 0 || d.top[0].score < minScore {
		if reassess {
			c.tick = s.tick
		}
		e.keep(&d, noteNothing)
		return false
	}
	best := &d.top[0]
	if reassess {
		same := best.kind == c.kind && best.target == c.target
		margin := int64(100 + s.p.Hysteresis)
		if s.part(gExpand) && best.kind.build() {
			// growth: a helper leaves for a new building that beats its
			// help; the margin only keeps it from hopping between helps.
			margin = 100
		}
		if same || (cur >= 0 && best.score*100 < cur*margin) {
			c.tick = s.tick
			e.keep(&d, noteKeep)
			return false
		}
	}
	e.assign(b, u, c, best)
	d.chosen, d.note = 0, noteAssign
	e.ring[e.next] = d
	e.next = (e.next + 1) % len(e.ring)
	return true
}

// keep records a decision that issued nothing, at most once per builder
// per explain window, so the ring shows why builders stay put.
func (e *Economy) keep(d *decision, note uint8) {
	last := &e.ring[(e.next+len(e.ring)-1)%len(e.ring)]
	if last.who == d.who && last.note == note && last.x == d.x && last.z == d.z {
		return
	}
	d.note = note
	e.ring[e.next] = *d
	e.next = (e.next + 1) % len(e.ring)
}

func (e *Economy) assign(b *core.Board, u *aikit.OwnUnit, c *commitment, best *cand) {
	s := e.s
	k := b.K
	switch best.kind {
	case cMex:
		if s.p.Layout != 0 {
			k.BuildKeep(u.H, best.prod, best.x, best.z, best.spot, 0)
		} else {
			k.Build(u.H, best.prod, best.x, best.z, best.spot, 0, false)
		}
		b.ClaimSpot(best.spot, u.H)
	case cUpgrade:
		k.Replace(u.H, best.target, best.prod, best.spot)
		b.ClaimSpot(best.spot, u.H)
	case cClear:
		k.Clear(u.H, best.x, best.z, best.spacing)
		if best.gainM > 0 {
			// An opening Clear (open_reclaim.go): its pile is skipped
			// until the feature listing refreshes.
			s.open.rec.sent(best.x, best.z, s.tick, best.gainM)
		}
	case cAssist:
		e.one[0] = u.H
		k.Repair(e.one[:], best.target, false)
	case cGuard:
		e.one[0] = u.H
		k.Guard(e.one[:], best.target, false)
	default:
		if s.p.Layout != 0 {
			k.BuildKeep(u.H, best.prod, best.x, best.z, -1, best.spacing)
		} else {
			k.Build(u.H, best.prod, best.x, best.z, -1, best.spacing, false)
		}
	}
	*c = commitment{def: u.Info, gen: u.Gen, kind: best.kind, prod: best.prod, spot: best.spot, x: best.x, z: best.z,
		target: best.target, tick: s.tick, score: best.score, gainM: best.gainM, gainE: best.gainE}
	s.metalStarted(best.kind) // the metal audit (econ_metal.go)
	// Reserve: later builders this think see the planned output.
	if best.kind.build() && best.prod != nil {
		s.count[best.prod.Index]++
		s.pendM += best.gainM
		s.pendE += best.gainE
		if best.kind == cFactory {
			s.pendFacBP += int64(best.prod.BuildPower)
		}
		s.recompute(int64(s.p.Horizon))
	}
}

// mexFor returns the cheapest land extractor a builder can make.
func (e *Economy) mexFor(bi *aikit.UnitInfo) *aikit.UnitInfo {
	var best *aikit.UnitInfo
	for _, p := range bi.Builds {
		if !p.Role.Has(aikit.RoleExtractor) || e.s.info[p.Index].water {
			continue
		}
		if best == nil || p.Value < best.Value {
			best = p
		}
	}
	return best
}

func speedOf(u *aikit.UnitInfo) int64 {
	if u.Speed < 1 {
		return 1
	}
	return int64(u.Speed)
}

// evalMex scores the best free metal spot for this builder: metal need ×
// return per cost × travel × route threat × territory.
func (e *Economy) evalMex(b *core.Board, u *aikit.OwnUnit, bs *builderState, d *decision) {
	s := e.s
	p := &s.p
	mex := e.mexFor(u.Info)
	if mex == nil {
		return
	}
	m := b.K.Map
	com := u.Info.Role.Has(aikit.RoleCommander)
	cost := max64(s.info[mex.Index].costMeq, 1)
	speed := speedOf(u.Info)
	base := mul(int64(p.WMetal)*10, s.mexNeed())
	th := int64(p.ThreatHalf)
	best := cand{kind: cMex, prod: mex, spot: -1}
	best.score = 0
	travelHalf := s.mexTravelHalf()
	for i := range m.Spots {
		sp := &m.Spots[i]
		if b.Spots[i] != core.SpotFree || s.spotBlock[i] > s.tick || sp.Water {
			continue
		}
		if int32(i) == bs.blockSpot && bs.blockUntil > s.tick {
			continue
		}
		if com && aikit.Dist2(sp.X, sp.Z, b.HomeX, b.HomeZ) > int64(p.ComRadius)*int64(p.ComRadius) {
			continue
		}
		if s.outOfPlan(b, sp.X, sp.Z) {
			continue // metal: beyond home while the ambition cap binds
		}
		gain := spotGain(mex, sp.Metal)
		ret := gain * 100 / cost
		dist := int64(aikit.Dist(u.X, u.Z, sp.X, sp.Z))
		travel := half(dist/speed, travelHalf)
		sc := mul(mul(base, ret), travel)
		if sc <= best.score {
			continue
		}
		terr := s.spotTerr(b, i)
		sc = mul(sc, terr)
		if sc <= best.score {
			continue
		}
		thr := half(int64(b.Threat.At(sp.X, sp.Z)), th)
		if mul(sc, thr) <= best.score {
			continue
		}
		// The route's worst sector covers the site itself.
		route := half(int64(b.Threat.LineMax(u.X, u.Z, sp.X, sp.Z)), th)
		if com {
			route = mul(route, route)
		}
		sc = mul(sc, route)
		if sc > best.score {
			best.score, best.spot, best.x, best.z, best.gainM = sc, int32(i), sp.X, sp.Z, gain
			best.f = [4]int64{s.needM, ret, travel, mul(terr, route)}
		}
	}
	if best.spot >= 0 {
		d.offer(&best)
	}
}

// siteFor chooses where a non-extractor building goes. Each class has its
// own field so the executor's placement lattice (footprint + spacing) keeps
// lanes: energy behind the base, makers and storage to one side, factories
// toward the enemy (their exit lanes are kept clear by the executor). A
// product that failed to place rotates its field a quarter turn about home.
func (e *Economy) siteFor(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo, kind commitKind) (int32, int32) {
	s := e.s
	var x, z int32
	if s.terr.ready && s.p.Naval != 0 && s.info[p.Index].water {
		if s.p.TidalField != 0 {
			if x, z, ok := s.fieldSite(p); ok {
				return x, z // a field clear of the shipyard (econ_tidal.go)
			}
		}
		return s.terr.siteX[p.Index], s.terr.siteZ[p.Index]
	}
	if s.p.Layout != 0 {
		if x, z, ok := e.zoneFor(b, p, kind); ok {
			return x, z
		}
	}
	switch kind {
	case cFactory:
		x, z = s.facX, s.facZ
	case cDefense:
		if s.danger > 0 {
			x, z = s.dangerX, s.dangerZ
		} else {
			x, z = s.frontX, s.frontZ
		}
		x += (b.EnemyX - x) / 16
		z += (b.EnemyZ - z) / 16
		return clampWorld(x, b.K.Map.WorldW), clampWorld(z, b.K.Map.WorldH)
	case cRadar:
		x, z = (s.frontX+b.RallyX)/2, (s.frontZ+b.RallyZ)/2
		return clampWorld(x, b.K.Map.WorldW), clampWorld(z, b.K.Map.WorldH)
	case cMaker, cStorage:
		x, z = s.sideX, s.sideZ
	default:
		if s.info[p.Index].water {
			return s.waterX, s.waterZ
		}
		x, z = s.safeX, s.safeZ
	}
	// Quarter turns of the field vector about home.
	dx, dz := x-b.HomeX, z-b.HomeZ
	switch s.prodFails[p.Index] % 4 {
	case 1:
		dx, dz = -dz, dx
	case 2:
		dx, dz = dz, -dx
	case 3:
		dx, dz = -dx, -dz
	}
	m := b.K.Map
	return clampWorld(b.HomeX+dx, m.WorldW), clampWorld(b.HomeZ+dz, m.WorldH)
}

// evalBuildings scores every non-extractor building the builder can make.
func (e *Economy) evalBuildings(b *core.Board, u *aikit.OwnUnit, bs *builderState, d *decision) {
	s := e.s
	bi := u.Info
	ext := s.p.Naval != 0 || s.p.Air != 0 || s.p.Tech != 0
	c0, reg := s.placeOf(u)
	// Normalizers: the best factory quality and defense efficiency on offer.
	e.bestQ, e.bestDef, e.bestQR = 1, 1, 1
	for _, p := range bi.Builds {
		si := &s.info[p.Index]
		if p.Role.Has(aikit.RoleFactory) && si.quality > e.bestQ {
			e.bestQ = si.quality
		}
		if ext && p.Role.Has(aikit.RoleFactory) && si.qualityR > e.bestQR && (!si.water || s.p.Naval != 0) && (!si.airFac || s.p.Air != 0) {
			e.bestQR = si.qualityR
		}
		if p.Role.Has(aikit.RoleDefense) {
			if ef := s.eff(p, s.aaNeed); ef > e.bestDef {
				e.bestDef = ef
			}
		}
	}
	for _, p := range bi.Builds {
		si := &s.info[p.Index]
		if p.Role.Has(aikit.RoleMobile) || si.geo || s.prodBlock[p.Index] > s.tick {
			continue
		}
		if p == bs.blockProd && bs.blockUntil > s.tick {
			continue
		}
		if s.terr.ready && s.p.Naval != 0 {
			if !s.canWorkSite(c0, reg, p) {
				continue
			}
		} else if si.water && !s.haveWater {
			continue
		}
		var c cand
		switch {
		case p.Role.Has(aikit.RoleExtractor):
			continue
		case p.Role.Has(aikit.RoleFactory) && ext:
			c = e.evalFactoryN(b, u, p)
		case p.Role.Has(aikit.RoleFactory):
			c = e.evalFactory(b, u, p)
		case p.Role.Has(aikit.RoleEnergy):
			c = e.evalEnergy(b, u, p)
		case p.Role.Has(aikit.RoleMetalMaker):
			c = e.evalMaker(b, u, p)
		case p.Role.Has(aikit.RoleDefense):
			c = e.defenseCand(b, u, p)
		case p.Role.Has(aikit.RoleRadar):
			c = e.evalRadar(b, u, p)
		case p.Role.Has(aikit.RoleStorage):
			c = e.evalStorage(b, u, p)
		default:
			continue
		}
		if c.score > 0 {
			d.offer(&c)
		}
	}
}

// place fills a candidate's site and returns travel and site-threat
// considerations; ok is false when the commander may not go there.
func (e *Economy) place(b *core.Board, u *aikit.OwnUnit, c *cand) (travel, thr int64, ok bool) {
	s := e.s
	p := &s.p
	c.x, c.z = e.siteFor(b, u, c.prod, c.kind)
	c.spot = -1
	if u.Info.Role.Has(aikit.RoleCommander) && aikit.Dist2(c.x, c.z, b.HomeX, b.HomeZ) > int64(p.ComRadius)*int64(p.ComRadius) {
		return 0, 0, false
	}
	dist := int64(aikit.Dist(u.X, u.Z, c.x, c.z))
	travel = half(dist/speedOf(u.Info), int64(p.TravelHalf))
	thr = half(int64(b.Threat.At(c.x, c.z)), int64(p.ThreatHalf))
	return travel, thr, true
}

// afford halves with every affordHalf seconds of total supply a purchase
// would take.
func (e *Economy) afford(p *aikit.UnitInfo) int64 {
	s := e.s
	supply := s.supplyM + s.supplyE*10/int64(s.p.ERatio)
	secs := s.info[p.Index].costMeq * 1000 / max64(supply, 1)
	return half(secs, affordHalf)
}

func (e *Economy) evalEnergy(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	c := cand{kind: cEnergy, prod: p, spacing: 2}
	g := s.gainE(p)
	if g <= 0 || s.energyHeld() {
		return c
	}
	travel, thr, ok := e.place(b, u, &c)
	if !ok {
		return c
	}
	meq := g * 10 / int64(s.p.ERatio)
	ret := meq * 100 / max64(s.info[p.Index].costMeq, 1)
	need := s.needE
	risk := int64(one)
	if p.WindGen == 0 && p.TidalGen == 0 {
		need = max64(need, s.needFirm)
	} else if p.WindGen > 0 {
		// Variable output is worth its mean less its spread, in proportion
		// to how much of our energy would ride on the wind.
		share := (s.windE + g) * one / max64(s.eIncExp+g, 1)
		risk = clamp(one-mul(s.windStd, share), 0, one)
	}
	c.gainE = g
	c.score = mul(mul(mul(mul(mul(int64(s.p.WEnergy)*10, need), ret), risk), travel), thr)
	c.f = [4]int64{need, ret, risk, travel}
	return c
}

func (e *Economy) evalMaker(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	c := cand{kind: cMaker, prod: p, spacing: 2}
	gm := gainMaker(p)
	if gm <= 0 || s.eCap <= 0 || s.makerHeld() {
		return c
	}
	// Surplus energy that still covers demand once this maker draws its
	// upkeep (a maker and every extractor stop in an energy stall); a full
	// store lowers the bar.
	// Judged on expected income, not the current gust.
	if (s.p.Layout != 0 || s.p.Tech != 0) && s.lastEStall != 0 && s.tick-s.lastEStall < makerStallRest {
		// Energy ran out lately: a maker would starve the extractors.
		return c
	}
	use := int64(p.EnergyUse) * 1000
	exp := s.eIncExp + s.pendE
	after := exp * 1000 / max64(s.demandE+use, 1000)
	surplus := lin(after, 1000, 1500)
	if s.eStock*20 >= s.eCap*19 {
		// Full store: enough if expected income alone still carries every
		// standing draw plus this maker's (construction spending can pause).
		surplus = max64(surplus, lin(exp-s.upkeepE-use, -10000, 10000))
	}
	if surplus == 0 {
		return c
	}
	travel, thr, ok := e.place(b, u, &c)
	if !ok {
		return c
	}
	// Converting energy into metal only pays while metal is genuinely short
	// (a maker keeps drawing when metal overflows, starving everything that
	// spends metal of its energy).
	short := lin(s.needM, 700, 1500)
	if short == 0 {
		return c
	}
	ret := gm * 100 / max64(s.info[p.Index].costMeq, 1)
	c.gainM = gm
	c.gainE = -int64(p.EnergyUse) * 1000
	c.score = mul(mul(mul(mul(mul(mul(int64(s.p.WMaker)*10, s.needM), short), ret), surplus), travel), thr)
	c.f = [4]int64{s.needM, ret, surplus, short}
	return c
}

func (e *Economy) evalStorage(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	c := cand{kind: cStorage, prod: p, spacing: 2}
	var v int64
	if p.MetalStore > 0 && s.mCap > 0 {
		v = max64(v, mul(lin(s.mStock*1000/s.mCap, 700, 1000), lin(s.covM, 1200, 3000)))
	}
	if p.EnergyStore > 0 && s.eCap > 0 && !s.energyHeld() {
		v = max64(v, mul(lin(s.eStock*1000/s.eCap, 700, 1000), lin(s.covE, 1200, 3000)))
	}
	if v == 0 {
		return c
	}
	travel, thr, ok := e.place(b, u, &c)
	if !ok {
		return c
	}
	dim := half(int64(s.count[p.Index])*1000, 1000)
	c.score = mul(mul(mul(mul(int64(s.p.WStorage)*10, v), dim), travel), thr)
	c.f = [4]int64{v, dim, travel, thr}
	return c
}

// evalFactory: need = unmet army spending (supply × army share − factory
// spend capacity) in units of this factory's drain; suitability = product
// quality, air exposure to enemy anti-air, same-type diminishing returns
// and, for tech 2, income readiness.
func (e *Economy) evalFactory(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	sp := &s.p
	si := &s.info[p.Index]
	c := cand{kind: cFactory, prod: p, spacing: 3}
	if si.landCombat == 0 || si.water || s.factoryFull() {
		return c
	}
	drain := max64(int64(p.BuildPower)*s.rMU/1000, 1)
	desired := s.spendable * int64(100-b.Posture.EcoShare) / 100
	have := (s.bpFac + s.pendFacBP) * s.rMU / 1000
	need := clamp((desired-have)*1000/drain, 0, 2000)
	// Without any factory there are no constructors and no army: urgency
	// rises linearly to 3000 by fac_time.
	if len(b.Factories) == 0 && s.pendFacBP == 0 {
		need = max64(need, lin(int64(s.tick), 0, int64(sp.FacTime)*30)*3)
	}
	if need == 0 {
		return c
	}
	suit := si.quality * 1000 / e.bestQ
	suit = mul(suit, half(int64(s.count[p.Index])*1000, 1500))
	suit = s.familySuit(p, suit)
	if p.Depth >= 3 {
		suit = mul(suit, mul(int64(sp.WTech)*10, lin(s.mInc, 12000, 30000)))
	}
	if suit == 0 {
		return c
	}
	travel, _, ok := e.place(b, u, &c)
	if !ok || e.backedOff(p, c.x, c.z) {
		return c
	}
	aff := e.afford(p)
	c.gainM = 0
	c.score = mul(mul(mul(mul(int64(sp.WFactory)*10, need), suit), aff), travel)
	c.f = [4]int64{need, suit, aff, travel}
	return c
}

// evalDefense: danger at our most threatened building (remembered), or a
// small baseline at the front later in the game; anti-air towers answer
// seen aircraft instead. Efficiency is normalized to the best on offer;
// defenses already near the site give diminishing returns.
func (e *Economy) evalDefense(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	sp := &s.p
	c := cand{kind: cDefense, prod: p, spacing: 1}
	var danger int64
	switch {
	case s.info[p.Index].aaOnly:
		danger = s.aaNeed
	case s.terr.ready && s.p.Naval != 0 && s.info[p.Index].water:
		danger = s.waterDanger(b, p)
	default:
		base := lin(int64(s.tick)/30, 240, 600) * 150 / 1000
		danger = max64(lin(s.danger, 0, 2*int64(sp.ThreatHalf)), base)
	}
	if danger == 0 {
		return c
	}
	travel, _, ok := e.place(b, u, &c)
	if !ok {
		return c
	}
	eff := s.eff(p, s.aaNeed) * 1000 / e.bestDef
	var near int64
	for _, i := range b.Defenses {
		d := &b.O.Own[i]
		if aikit.Dist2(d.X, d.Z, c.x, c.z) <= 500*500 {
			near++
		}
	}
	for _, i := range b.Frames {
		d := &b.O.Own[i]
		if d.Info.Role.Has(aikit.RoleDefense) && aikit.Dist2(d.X, d.Z, c.x, c.z) <= 500*500 {
			near++
		}
	}
	dim := half(near*1000, 1500)
	aff := e.afford(p)
	c.score = mul(mul(mul(mul(mul(int64(sp.WDefense)*10, danger), eff), dim), aff), travel)
	c.f = [4]int64{danger, eff, dim, aff}
	return c
}

func (e *Economy) evalRadar(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	c := cand{kind: cRadar, prod: p, spacing: 1}
	need := mul(lin(int64(s.tick), 1800, 5400), half(int64(s.count[p.Index])*3000, 1000))
	if need == 0 {
		return c
	}
	travel, thr, ok := e.place(b, u, &c)
	if !ok {
		return c
	}
	aff := e.afford(p)
	c.score = mul(mul(mul(mul(int64(s.p.WRadar)*10, need), aff), travel), thr)
	c.f = [4]int64{need, aff, travel, thr}
	return c
}

// evalAssist scores helping the most useful nearby nanoframe and guarding
// the nearest factory. Assisting only pays when resources can feed the
// extra build power.
func (e *Economy) evalAssist(b *core.Board, u *aikit.OwnUnit, cm *commitment, d *decision, cur *int64) {
	s := e.s
	sp := &s.p
	o := b.O
	speed := speedOf(u.Info)
	w := int64(sp.WAssist) * 10
	res := lin(min64(s.covM, s.covE), 500, 1400)
	if res > 0 {
		best := cand{kind: cAssist, spot: -1}
		for _, i := range b.Frames {
			f := &o.Own[i]
			r := f.Info.Role
			if r.Has(aikit.RoleMobile) || f.Progress >= 95 {
				continue
			}
			dist := int64(aikit.Dist(u.X, u.Z, f.X, f.Z))
			if dist > 1500 {
				continue
			}
			prio := int64(700)
			switch {
			case r.Has(aikit.RoleFactory):
				prio = 1500
			case r.Any(aikit.RoleEnergy | aikit.RoleExtractor):
				prio = 1000
			case r.Has(aikit.RoleDefense):
				prio = 1000 + lin(s.danger, 0, 2*int64(sp.ThreatHalf))
			}
			travel := half(dist/speed, int64(sp.TravelHalf))
			sc := mul(mul(mul(w, res), prio), travel)
			if sc > best.score {
				best.score, best.target, best.x, best.z, best.prod = sc, f.H, f.X, f.Z, f.Info
				best.f = [4]int64{res, prio, travel, 0}
			}
		}
		if best.score > 0 {
			if cm.kind == cAssist && cm.target == best.target {
				*cur = best.score
			}
			d.offer(&best)
		}
	}
	armyF := clamp(int64(100-b.Posture.EcoShare)*20, 200, 1500)
	cov := lin(s.covM, 900, 2500)
	if s.part(gExpand) {
		// growth: with fewer factories for the income, builders help them
		// as soon as metal is ample (econ_growth.go).
		cov = lin(s.covM, 700, 1500)
	}
	need := mul(cov, armyF)
	if need == 0 {
		return
	}
	best := cand{kind: cGuard, spot: -1}
	var bestD int64 = -1
	for _, i := range b.Factories {
		f := &o.Own[i]
		// Guarding helps only a factory that is producing.
		if f.QueueLen == 0 || s.fstateOf(f).blocked {
			continue
		}
		dd := aikit.Dist2(u.X, u.Z, f.X, f.Z)
		if bestD < 0 || dd < bestD {
			bestD = dd
			best.target, best.x, best.z, best.prod = f.H, f.X, f.Z, f.Info
		}
	}
	if bestD < 0 {
		return
	}
	travel := half(aikit.ISqrt64(bestD)/speed, int64(sp.TravelHalf))
	best.score = mul(mul(w, need), travel)
	best.f = [4]int64{s.covM, armyF, travel, 0}
	if cm.kind == cGuard && cm.target == best.target {
		*cur = best.score
	}
	d.offer(&best)
}
