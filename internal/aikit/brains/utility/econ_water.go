package utility

import (
	"math/bits"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Economy candidates behind the naval, air and tech switches.

// navalIdleMax is how many ships may sit unmoved since launch before
// shipyards and ships stop being built: the army layer is not using them.
const navalIdleMax = 3

// freeSafeN counts free, safe spots on our side that a builder we have can
// reach and extract (water spots included), and per constructor type the
// room it would have: the spots its class can reach and extract, plus
// standing water work (tidal generators, floating makers) when it builds
// those at the naval base.
//
// Which spots a builder's extractors fit (fitsOf), and which spots a
// constructor type counts (consSpot), depend only on the map and the
// definitions, so they are found once; a think asks only what changes:
// where builders stand, whether a spot is free and safe, and that only for
// a spot that counts somewhere.
func (s *shared) freeSafeN(b *core.Board) {
	m := b.K.Map
	o := b.O
	// Distinct builder places: class, region and one builder (for its
	// extractor list); a handful at most.
	var places [8][3]int32
	var fits [8][]bool
	var com [8]bool
	n := 0
	for _, bi := range b.Builders {
		u := &o.Own[bi]
		if len(s.info[u.Info.Index].mexes) == 0 {
			continue
		}
		c, reg := s.placeOf(u)
		dup := false
		for j := 0; j < n; j++ {
			if places[j][0] == c && places[j][1] == reg && o.Own[places[j][2]].Info == u.Info {
				dup = true
				break
			}
		}
		if dup || n == len(places) {
			continue
		}
		places[n] = [3]int32{c, reg, bi}
		fits[n] = s.fitsOf(m, u.Info)
		com[n] = u.Info.Role.Has(aikit.RoleCommander)
		n++
	}
	for _, qi := range s.consDefs {
		s.consRoom[qi] = s.info[qi].waterWork
	}
	if s.consSpot == nil {
		s.setupConsSpot(m)
	}
	nw := consWords(len(s.consDefs))
	leash := int64(s.p.ComRadius) * int64(s.p.ComRadius)
	for i := range m.Spots {
		sp := &m.Spots[i]
		if b.Spots[i] != core.SpotFree || s.spotBlock[i] > s.tick {
			continue
		}
		far := aikit.Dist2(sp.X, sp.Z, b.HomeX, b.HomeZ) > leash
		ok := false
		for j := 0; j < n && !ok; j++ {
			// The commander stays near home.
			ok = fits[j][i] && !(far && com[j]) && s.canWorkSpot(places[j][0], places[j][1], int32(i))
		}
		cons := s.consSpot[i*nw : (i+1)*nw]
		if !ok && !anyBit(cons) {
			continue // counted nowhere, safe or not
		}
		// Threat first: both tests are pure, and territory takes two
		// square roots.
		if int64(b.Threat.At(sp.X, sp.Z)) >= int64(s.p.ThreatHalf) || s.spotTerr(b, i) < 600 {
			continue
		}
		if ok {
			s.freeSafe++
		}
		for w, mask := range cons {
			for ; mask != 0; mask &= mask - 1 {
				s.consRoom[s.consDefs[w*64+bits.TrailingZeros64(mask)]]++
			}
		}
	}
}

// fitsOf returns, per metal spot, whether one of the builder definition's
// extractors can stand on it, found on first use.
func (s *shared) fitsOf(m *aikit.MapInfo, info *aikit.UnitInfo) []bool {
	si := &s.info[info.Index]
	if si.spotFits == nil {
		si.spotFits = make([]bool, len(m.Spots))
		for i := range m.Spots {
			si.spotFits[i] = fitsAny(m, si.mexes, &m.Spots[i])
		}
	}
	return si.spotFits
}

// setupConsSpot fills consSpot: the spots each constructor type in our tree
// counts as room, from its class's home region. Advanced builders count
// none: they are made for the tech plan, not for room.
func (s *shared) setupConsSpot(m *aikit.MapInfo) {
	nw := consWords(len(s.consDefs))
	s.consSpot = make([]uint64, nw*len(m.Spots))
	for j, qi := range s.consDefs {
		si := &s.info[qi]
		if si.advCon {
			continue
		}
		c := int32(si.cls)
		var reg int32
		if c >= 0 {
			reg = s.terr.cls[c].home
		}
		for i := range m.Spots {
			if s.canWorkSpot(c, reg, int32(i)) && fitsAny(m, si.mexes, &m.Spots[i]) {
				s.consSpot[i*nw+j/64] |= 1 << (j % 64)
			}
		}
	}
}

// consWords is the number of 64-bit words a spot's consSpot mask takes.
func consWords(n int) int { return (n + 63) / 64 }

// anyBit reports whether any bit of a mask is set.
func anyBit(ws []uint64) bool {
	for _, w := range ws {
		if w != 0 {
			return true
		}
	}
	return false
}

// fitsAny reports whether one of the extractors can stand on the spot.
func fitsAny(m *aikit.MapInfo, exts []*aikit.UnitInfo, sp *aikit.MetalSpot) bool {
	for _, x := range exts {
		if fitsSpot(m, x, sp) {
			return true
		}
	}
	return false
}

// placeOf is builderPlace remembered per builder while it stands still:
// the region depends only on the terrain model and where the builder
// stands.
func (s *shared) placeOf(u *aikit.OwnUnit) (int32, int32) {
	if s.info[u.Info.Index].cls < 0 || !s.terr.ready || s.p.Naval == 0 {
		return s.builderPlace(u) // no region to look up
	}
	bs := s.bstateOf(u)
	if !bs.placed || bs.placeX != u.X || bs.placeZ != u.Z {
		c, reg := s.builderPlace(u)
		bs.placed, bs.placeX, bs.placeZ, bs.placeC, bs.placeReg = true, u.X, u.Z, int8(c), reg
	}
	return int32(bs.placeC), bs.placeReg
}

// spotMex picks the builder's extractor for a free spot: the cheapest whose
// water depth band holds the spot (the cheapest land one without naval).
func (s *shared) spotMex(m *aikit.MapInfo, exts []*aikit.UnitInfo, sp *aikit.MetalSpot) *aikit.UnitInfo {
	for _, x := range exts {
		if s.p.Naval != 0 {
			if fitsSpot(m, x, sp) {
				return x
			}
			continue
		}
		if !sp.Water && !s.info[x.Index].water {
			return x
		}
	}
	return nil
}

// evalMexN is evalMex with the terrain model and the tech upgrade: every
// spot the builder can reach, the extractor that fits it (water spots take
// a water extractor), and — with tech — replacing one of our extractors by
// a richer one the builder can make (gain = the difference).
func (e *Economy) evalMexN(b *core.Board, u *aikit.OwnUnit, bs *builderState, d *decision) {
	s := e.s
	p := &s.p
	exts := s.info[u.Info.Index].mexes
	if len(exts) == 0 {
		return
	}
	m := b.K.Map
	com := u.Info.Role.Has(aikit.RoleCommander)
	speed := speedOf(u.Info)
	base := mul(int64(p.WMetal)*10, s.mexNeed())
	// An upgrade's return is its extra metal over the whole game against a
	// lump cost, where the energy options it competes with pay only while
	// energy is short: w_upgrade weights it.
	baseUp := base * int64(p.WUpgrade) / 100
	th := int64(p.ThreatHalf)
	c, reg := s.placeOf(u)
	upgrade := p.Tech != 0 && len(s.spotOwn) == len(m.Spots)
	travelHalf := s.mexTravelHalf()
	best := cand{kind: cMex, spot: -1}
	for i := range m.Spots {
		sp := &m.Spots[i]
		st := b.Spots[i]
		if st != core.SpotFree && !(upgrade && st == core.SpotOurs) {
			continue
		}
		if s.spotBlock[i] > s.tick || (int32(i) == bs.blockSpot && bs.blockUntil > s.tick) {
			continue
		}
		if com && aikit.Dist2(sp.X, sp.Z, b.HomeX, b.HomeZ) > int64(p.ComRadius)*int64(p.ComRadius) {
			continue
		}
		if !s.canWorkSpot(c, reg, int32(i)) {
			continue
		}
		var mex *aikit.UnitInfo
		var gain int64
		var target pool.Handle
		kind := cMex
		if st == core.SpotFree {
			if s.outOfPlan(b, sp.X, sp.Z) {
				continue // metal: beyond home while the ambition cap binds
			}
			if mex = s.spotMex(m, exts, sp); mex == nil {
				continue
			}
			gain = spotGain(mex, sp.Metal)
		} else {
			oi := s.spotOwn[i]
			if oi < 0 {
				continue
			}
			old := &b.O.Own[oi]
			if !old.Built {
				continue
			}
			for _, x := range exts {
				if x.MetalMake > old.Info.MetalMake && fitsSpot(m, x, sp) && (mex == nil || x.MetalMake > mex.MetalMake) {
					mex = x
				}
			}
			if mex == nil {
				continue
			}
			gain = spotGain(mex, sp.Metal) - spotGain(old.Info, sp.Metal)
			target, kind = old.H, cUpgrade
		}
		cost := max64(s.info[mex.Index].costMeq, 1)
		ret := gain * 100 / cost
		dist := int64(aikit.Dist(u.X, u.Z, sp.X, sp.Z))
		travel := half(dist/speed, travelHalf)
		bw := base
		if kind == cUpgrade {
			bw = baseUp
		}
		sc := mul(mul(bw, ret), travel)
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
		route := half(int64(b.Threat.LineMax(u.X, u.Z, sp.X, sp.Z)), th)
		if com {
			route = mul(route, route)
		}
		sc = mul(sc, route)
		if sc > best.score {
			best.score, best.spot, best.x, best.z, best.gainM = sc, int32(i), sp.X, sp.Z, gain
			best.prod, best.kind, best.target = mex, kind, target
			best.f = [4]int64{s.needM, ret, travel, mul(terr, route)}
		}
	}
	if best.spot >= 0 {
		d.offer(&best)
	}
}

// evalFactoryN: the factory need of evalFactory with army capacity counted
// by how far each factory's units reach the enemy; suitability from the
// reach-weighted product quality against the best factory on offer; air
// plants wanted when land cannot reach (or, later, as a second line when
// enemy anti-air is weak); the tech-2 factory on the tech timeline.
func (e *Economy) evalFactoryN(b *core.Board, u *aikit.OwnUnit, p *aikit.UnitInfo) cand {
	s := e.s
	sp := &s.p
	si := &s.info[p.Index]
	c := cand{kind: cFactory, prod: p, spacing: 3}
	if si.water && (sp.Naval == 0 || !s.terr.siteOK[p.Index] || s.navalIdle >= navalIdleMax) {
		return c
	}
	if !si.water && s.terr.ready && !s.terr.landRoom[p.Index] {
		return c
	}
	if si.airFac && sp.Air == 0 {
		return c
	}
	if sp.ReachMix != 0 && si.airFac && len(b.Factories) == 0 && s.pendFacBP == 0 && s.landReach >= strandReach && s.firstFam != famAir {
		// Not an air plant first while the land army may reach the enemy
		// (econ_reach.go): the opening's army comes from the first factory.
		return c
	}
	// reach_mix: a factory for land constructors while no factory of ours
	// makes them (econ_reach.go), whether or not its army reaches.
	consSource := sp.ReachMix != 0 && !si.water && makesLandCons(s, p) && !s.landConsFactory()
	if si.qualityR <= 0 && !consSource {
		return c
	}
	t2 := sp.Tech != 0 && si.t2
	if sp.ReachMix != 0 && s.stranded(p) && !(t2 && s.tech.t2Fac == 0) && !consSource {
		// Its army cannot get to the enemy: only as the constructor
		// source, or the tech plan's first tech-2 factory.
		return c
	}
	drain := max64(int64(p.BuildPower)*s.rMU/1000, 1)
	desired := s.spendable * int64(100-b.Posture.EcoShare) / 100
	have := (s.bpFacUseful + s.pendFacBP) * s.rMU / 1000
	need := clamp((desired-have)*1000/drain, 0, 2000)
	if len(b.Factories) == 0 && s.pendFacBP == 0 {
		need = max64(need, lin(int64(s.tick), 0, int64(sp.FacTime)*30)*3)
	}
	if !(t2 && s.tech.t2Fac == 0) && s.factoryFull() {
		return c // growth: as many factories as the income carries
	}
	if t2 && s.tech.t2Fac == 0 {
		// The first tech-2 factory is wanted on the tech timeline whether
		// or not the army needs more factory capacity.
		need = max64(need, s.tech.want*3/2)
	}
	if consSource {
		// As urgent as a first factory: without it only the commander
		// builds on land.
		need = max64(need, lin(int64(s.tick), 0, int64(sp.FacTime)*30)*3)
	}
	// army: factories follow banked income, and a walled-in only factory
	// is replaced (army.go).
	need = max64(need, s.factoryNeed(b, lin(int64(s.tick), 0, int64(sp.FacTime)*30)*3))
	if need == 0 {
		return c
	}
	suit := si.qualityR * 1000 / max64(e.bestQR, 1)
	if consSource {
		suit = max64(suit, consSourceSuit)
	}
	if si.airFac {
		suit = max64(suit, s.airWant())
		suit = mul(suit, half(s.enAA, 4000)) * int64(sp.WAir) / 100
	}
	suit = mul(suit, half(int64(s.count[p.Index])*1000, 1500))
	suit = s.familySuit(p, suit)
	switch {
	case t2 && s.tech.t2Fac == 0 && s.part(gTech):
		suit = max64(suit, one) * s.tech.want / one
	case t2 && s.tech.t2Fac == 0:
		suit = max64(suit, 600) * s.tech.want / one
	case t2:
		suit = mul(suit, lin(s.mInc, 20000, 40000))
	case p.Depth >= 3:
		gate := mul(int64(sp.WTech)*10, lin(s.mInc, 12000, 30000))
		if s.reach != nil {
			gate = max64(gate, one-s.landReach)
		}
		suit = mul(suit, gate)
	}
	if suit == 0 {
		return c
	}
	travel, _, ok := e.place(b, u, &c)
	if !ok || e.backedOff(p, c.x, c.z) {
		return c
	}
	aff := e.afford(p)
	c.score = mul(mul(mul(mul(int64(sp.WFactory)*10, need), suit), aff), travel)
	c.f = [4]int64{need, suit, aff, travel}
	return c
}

// airWant is the air plant's suitability floor: full when land units
// cannot reach the enemy, and a modest second line from minute 8 on land.
func (s *shared) airWant() int64 {
	if s.p.Air == 0 {
		return 0
	}
	w := lin(int64(s.minutes), 8, 14) * 350 / one
	if s.reach != nil {
		w = max64(w, one-s.landReach)
	}
	return w
}

// waterDanger is the danger a water defense answers: enemy ships seen (for
// torpedo towers), or the ordinary danger when it is near the naval base.
func (s *shared) waterDanger(b *core.Board, p *aikit.UnitInfo) int64 {
	d := lin(b.EnemyNaval, 0, 40000)
	if p.WaterDPS == 0 && s.terr.navOK && aikit.Dist2(s.dangerX, s.dangerZ, s.terr.navX, s.terr.navZ) < 900*900 {
		d = max64(d, lin(s.danger, 0, 2*int64(s.p.ThreatHalf)))
	}
	return d
}
