package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// The front rules: tower pacing, mix and siting toward human play. They
// apply only with both the defense plan and the layout switch on (the
// defaults); either switch off plays the earlier game exactly.
//
// Evidence (human benchmark; README §12): original-TA winners hold 0 / 5 /
// 13 / 21 towers alive at 5 / 10 / 15 / 20 minutes, worth 0 / 534 / 1318 /
// 2639; ProTA winners spend 2% / 6% / 8% / 9% of all value built on
// defenses by those minutes (8–10% of what they build after minute 5).
// About nine towers in ten are missile towers — anti-air first, with
// ground fire and the longest reach of the cheap towers — from the first
// one on; light lasers appear from minute ~9, heavy towers from ~16.
//
// Pacing. The ground budget's base share of income is frontEarly (48‰)
// from minute 6 (rising from minute 3:30), flat to minute 20, then rising
// by frontLate (30‰) to minute 30; the plan multiplies it by wScale (×3 at
// the default w_defense), slack (energy is short through most of the
// opening), raids and the army balance as before, so the default spends
// roughly the humans' 8–10% of income. The earlier shares (15‰ to minute
// 12, rising to 70‰ by 25) back-loaded the towers: the mirrors held 2 / 8
// / 20 at 10 / 15 / 20 minutes, worth twice the humans' by minute 20; 40‰
// held 4 / 10 / 22.
//
// Mix. A missile tower (an anti-air tower whose ground fire is worth its
// cost by classify's test) also serves the ground plan: it is scored for
// both classes (the better counts), and once standing, framed or ordered
// it counts toward both. The ground plan's cheapest tower is then the
// missile tower, so it asks for towers a missile tower at a time (the bank
// stays in light-tower units). Direct-fire towers are held to the humans'
// share: from a builder that could build a missile tower instead, one is
// scored only while missile towers make up at least frontMixLo of the
// ground-plan towers, fully from frontMixHi (so the first tower is a
// missile tower too), and the commander, whose only tower is a light
// laser, leaves ground towers to constructors that can build missile
// towers. A side with no missile tower keeps the old mix.

const (
	// frontMixLo and frontMixHi (permille of ground-plan towers by count)
	// are where a direct-fire tower's mix term rises from 0 to full: human
	// players' towers are ~85–95% missile towers (report D3).
	frontMixLo = 600
	frontMixHi = 850
	// frontEarly is the ground budget's base share from minute 6 and
	// frontLate what it gains from minute 20 to 30 (permille of income,
	// before wScale), toward the humans' 5 / 13 / 21 towers standing at
	// 10 / 15 / 20 minutes.
	frontEarly = 48
	frontLate  = 30
)

// setupFront finds the missile towers (for every side: the classes are
// read from any tower seen) and makes our side's cheapest the ground
// plan's reference tower. A missile tower is an anti-air tower whose
// ground fire (groundDPS: not its to-air weapons' fire) passes classify's
// worth-its-cost test on its own; a flak gun fires only at aircraft and
// is not one.
func (pl *defPlan) setupFront(s *shared, b *core.Board) {
	pl.front = true
	k := b.K
	for i, u := range k.Table.Units {
		if pl.cls[i] != dcAir || s.info[i].gndDPS*1000 < 20*max64(s.info[i].costMeq, 1) {
			continue
		}
		pl.dual[i] = true
		if u.Side != k.Side || u.Depth < 0 {
			continue
		}
		pl.haveDual = true
		pl.cRef[dcGround] = min64(pl.cRef[dcGround], s.info[i].costMeq)
	}
}

// frontBase is the ground budget's base share of income (permille, before
// wScale and the other terms) under the front rules.
func frontBase(t int64) int64 {
	return lin(t, 3*1800+900, 6*1800)*frontEarly/one + lin(t, 20*1800, 30*1800)*frontLate/one
}

// mixOf is the mix term of product p in class cl for the builder the
// normalizers were last computed for: 1000 except, under the front rules,
// for a direct-fire tower in the ground plan from a builder that could
// build a missile tower instead, which rises from 0 to 1000 as missile
// towers reach frontMixLo..frontMixHi of the ground-plan towers (so the
// first is a missile tower too, as people's first tower mostly is).
func (pl *defPlan) mixOf(p *aikit.UnitInfo, cl uint8) int64 {
	if !pl.front || !pl.normDual || cl != dcGround || pl.dual[p.Index] {
		return one
	}
	if m, ok := pl.typeMix(); ok {
		return m // army: direct fire where ground attacks came (army.go)
	}
	return pl.missileMix()
}

// missileMix is the direct-fire mix term: 0 until missile towers make up
// frontMixLo of the ground-plan towers (and while there are none), full
// from frontMixHi.
func (pl *defPlan) missileMix() int64 {
	if pl.mixG == 0 {
		return 0
	}
	return lin(int64(pl.mixD)*one/int64(pl.mixG), frontMixLo, frontMixHi)
}

// comLeaves reports whether the commander leaves ground towers to the
// constructors (front rules): while one that can build a missile tower
// stands, the commander's only tower — a light laser — would spend three
// missile towers' budget where people open with a missile tower.
func (pl *defPlan) comLeaves(u *aikit.OwnUnit) bool {
	return pl.front && pl.dualCons && u.Info.Role.Has(aikit.RoleCommander)
}

// noteDualCons records whether a built constructor can build a missile
// tower of our side.
func (pl *defPlan) noteDualCons(b *core.Board) {
	pl.dualCons = false
	if !pl.front || !pl.haveDual {
		return
	}
	o := b.O
	for _, i := range b.Builders {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleCommander) {
			continue
		}
		for _, p := range u.Info.Builds {
			if pl.dual[p.Index] {
				pl.dualCons = true
				return
			}
		}
	}
}

// Siting. People's towers stand a median 21% (original TA 30%) of the way
// toward the enemy start, 42% of them beyond a quarter of the way, 8%
// behind the start and about half within 400 wu of an own extractor; the
// plan's first answer put half of them in the core (median 13% of the way,
// 19% beyond a quarter). Under the front rules a zone's exposure rises
// twice as steeply toward the front (frontExp) and its ground towers stand
// further ahead of its front building (frontAhead), so they follow the
// expansion and the front rather than ring the factories.
//
// Choke posts. The terrain's chokes on the land routes from the enemy
// starts to our start and our side's spots (aikit.Chokes: ramps, passes,
// gaps between cliffs) are zones of their own, held against ground units
// only: their assets are our buildings (and the commander) on the ground
// each guards, their towers stand at the analysis's sites beside the
// passage mouth on our side — outside the passage test of our factories'
// units — never in the passage. A post holds as many towers as it has
// sites.

// frontExp is a zone's ground exposure at front fraction f (0 at home,
// 500 midway to the enemy): 250 + 4f, against the plan's 500 + 2f.
func frontExp(f int64) int64 { return 250 + 4*f }

// frontAhead is how much further ahead of its front building a zone's
// ground tower stands at front fraction f: 0.4 wu per permille (120 wu at
// 30% of the way), toward the ~350 wu people keep between a tower and the
// nearest extractor.
func frontAhead(f int64) int64 { return f * 400 / one }

// analyzeChokes reads the terrain's chokes for our side (setup, simulation
// thread): routes for the land movement class most of our basic
// factories' combat units share (the army's; a rare all-terrain scout does
// not make every cliff a road), sites checked against every basic
// factory's exit class, as the executor's passage rule would.
func analyzeChokes(k *aikit.Kit) *aikit.ChokeMap {
	type tally struct {
		mc aikit.MoveClass
		n  int
	}
	var classes []tally
	var site []aikit.MoveClass
	capped := func(mc aikit.MoveClass) aikit.MoveClass {
		if mc.FootX > 3 {
			mc.FootX = 3
		}
		if mc.FootZ > 3 {
			mc.FootZ = 3
		}
		return mc
	}
	for _, f := range k.Table.Units {
		if f.Side != k.Side || f.Depth != 1 || !f.Role.Has(aikit.RoleFactory) {
			continue
		}
		first := true
		for _, p := range f.Builds {
			mc, ok := aikit.MoveClassOf(p)
			if !ok || mc.MinDepth > 0 {
				continue
			}
			mc = capped(mc)
			if first {
				first = false // the factory's exit class
				dup := false
				for _, c := range site {
					dup = dup || c == mc
				}
				if !dup {
					site = append(site, mc)
				}
			}
			if !p.Role.Has(aikit.RoleCombat) {
				continue
			}
			j := 0
			for j < len(classes) && classes[j].mc != mc {
				j++
			}
			if j == len(classes) {
				classes = append(classes, tally{mc: mc})
			}
			classes[j].n++
		}
	}
	if len(classes) == 0 {
		return nil
	}
	best := 0
	for j := 1; j < len(classes); j++ {
		a, c := &classes[j], &classes[best]
		if a.n > c.n || (a.n == c.n && a.mc.MaxSlope > c.mc.MaxSlope) {
			best = j
		}
	}
	return k.Map.Chokes(classes[best].mc, site)
}

// chokeAsset adds an asset's ground weight to every choke post that
// guards it, activating the post (its anchor is the middle of its sites).
func (pl *defPlan) chokeAsset(s *shared, x, z int32, v int64) {
	if len(pl.chokes) == 0 {
		return
	}
	g := s.zones.choke.Guards(x, z)
	for i := range pl.chokes {
		if g&(1<<uint(i)) == 0 {
			continue
		}
		zn := pl.nGrid + int32(i)
		if pl.zGen[zn] != pl.gen {
			pl.activate(zn)
			c := &pl.chokes[i]
			var sx, sz int32
			for j := int32(0); j < c.NSites; j++ {
				sx += c.Sites[j][0]
				sz += c.Sites[j][1]
			}
			pl.zAncX[zn], pl.zAncZ[zn], pl.zAncD[zn] = sx/c.NSites, sz/c.NSites, 0
		}
		pl.zAsset[zn] += v
	}
}

// chokeLoss remembers a lost building at every choke post guarding it.
func (pl *defPlan) chokeLoss(s *shared, x, z int32, v int64) {
	if len(pl.chokes) == 0 {
		return
	}
	g := s.zones.choke.Guards(x, z)
	for i := range pl.chokes {
		if g&(1<<uint(i)) != 0 {
			pl.heatLoss[pl.nGrid+int32(i)] += v
		}
	}
}

// chokeTowers counts the ground towers (standing, framed, ordered) at a
// choke post.
func (pl *defPlan) chokeTowers(zone int32) int32 { return pl.towersAt(zone, dcGround) }

// chokeFull reports whether a choke post holds a tower per site.
func (pl *defPlan) chokeFull(zone int32) bool {
	return pl.chokeTowers(zone) >= pl.chokes[zone-pl.nGrid].NSites
}

// inNeck reports whether a point lies in a choke's neck: nearer the choke
// than its nearest site is (and within chokeNeck at least). Only the
// post's own towers stand there; the army that gathers or fights at a
// ramp needs the ground in front of it.
func (pl *defPlan) inNeck(x, z int32) bool {
	for i := range pl.chokes {
		c := &pl.chokes[i]
		r := int64(chokeNeck)
		for j := int32(0); j < c.NSites; j++ {
			if d := int64(aikit.Dist(c.X, c.Z, c.Sites[j][0], c.Sites[j][1])); d > r {
				r = d
			}
		}
		if aikit.Dist2(x, z, c.X, c.Z) < r*r {
			return true
		}
	}
	return false
}

// chokeNeck is the least radius of a choke's neck (world units).
const chokeNeck = 320

// chokeSite is the next free site of a choke post.
func (pl *defPlan) chokeSite(b *core.Board, zone int32) (int32, int32, bool) {
	c := &pl.chokes[zone-pl.nGrid]
	n := pl.chokeTowers(zone)
	for j := int32(0); j < c.NSites; j++ {
		st := c.Sites[(n+j)%c.NSites]
		if pl.clear(b, st[0], st[1]) {
			return st[0], st[1], true
		}
	}
	return 0, 0, false
}
