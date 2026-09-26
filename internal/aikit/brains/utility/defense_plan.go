package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Defense classes: what a static defense is for, from its UnitInfo alone.
const (
	dcNone   uint8 = iota // not a base defense (strategic gun, anti-missile, unarmed)
	dcGround              // direct or ballistic fire at ground units
	dcAir                 // anti-air (most of its fire can engage aircraft)
	dcWater               // must stand in water (torpedo launchers, floating towers)
	dcCount
)

// Plan constants. They are prototype choices for the Modern AI research
// brain, not retail behavior; README §3 "Defense plan" explains each.
const (
	// defZone is the edge of a defense zone (four planning sectors).
	defZone = 512
	// defCover: a tower this close to a zone's anchor defends that zone.
	defCover = 450
	// defStrategic: weapons reaching this far are strategic guns, not base
	// defense (long-range plasma cannons, super guns, anti-missile systems).
	defStrategic = 2000
	// defAhead is how far in front of a zone's front building a tower
	// stands, toward the approach.
	defAhead = 96
	// defLane is the lateral spacing between towers of one zone, so they
	// form a line across the approach with walkable gaps between them.
	defLane = 128
	// defFacClear keeps tower points this far from any factory centre, and
	// defFacExit out of the corridor this deep in front of its exit (+Z).
	defFacClear = 224
	defFacExit  = 640
	// defSpace keeps tower points this far from other towers (a gap of
	// about three tower widths), defGap this far from other buildings.
	defSpace = 160
	defGap   = 96
	// defBank: unspent budget banks at most this many light towers.
	defBank = 4
	// defRefSecs: a tower costing this many seconds of spendable supply is
	// "normal" — the blend in the tower quality measure (see quality).
	defRefSecs = 30
	// The plan's amounts scale with w_defense / defWRef, capped at
	// defWMax‰; its score weight is defWeight per point of w_defense (the
	// reactive evaluation uses 10). At the default w_defense (60) that is
	// three times the documented shares: the pacing measured closest to
	// human players' at no measurable cost against def_plan=0 (README §3).
	// A turtle style can double it again; a rush style cuts it to a third.
	defWRef   = 20
	defWMax   = 6000
	defWeight = 30
	// defTau and defTauAtk are the decay constants (ticks) of the raid
	// history and of the attack-direction history.
	defTau    = 5400
	defTauAtk = 18000
	// defFresh: an enemy sighting at most this old shows where it is now.
	defFresh = 150
	// defLossTopUp: a lost non-defense building funds this permille of its
	// cost into the defense budget (scaled by w_defense).
	defLossTopUp = 500
	// defComBonus is the asset weight (cost-equivalent) the commander adds
	// to the home zone: a commander kill ends the game.
	defComBonus = 1500
	// Own-unit flow: every sector a ground unit of ours crosses gains
	// defCross, decaying with defTauFlow (flowAt). A tower site is refused in a
	// sector carrying more than 1/defFlowShare of the heaviest flow within
	// defFlowReach sectors (and at least defFlowMin): those are the
	// corridors our units walk, which a tower would narrow or plug.
	defCross     = 1000
	defTauFlow   = 18000
	defFlowShare = 4
	defFlowReach = 5
	defFlowMin   = 8 * defCross
)

// trackedBld is an own building seen at the last refresh, so its loss can
// be noticed at the next one.
type trackedBld struct {
	h    pool.Handle
	gen  uint32
	info *aikit.UnitInfo
	x, z int32
}

// lastPos is where an own ground unit was at the last refresh (by handle,
// for the unit instance gen), and the terrain model's region there when
// observeFlow's walk to it found it (known): the next walk starts from it.
type lastPos struct {
	def   *aikit.UnitInfo
	gen   uint32
	x, z  int32
	reg   uint16 // a Reach region id (its labels are 16-bit)
	known bool
}

// defCommit is a builder's standing defense order.
type defCommit struct {
	cls  uint8
	dual bool // a missile tower: it also serves the ground plan (front rules)
	v    int64
	x, z int32
}

// defPlan is the proactive defense plan's state (see defense.go).
type defPlan struct {
	ready bool
	tick  uint32 // tick of the last refresh
	fresh bool   // a refresh has happened
	gen   uint32 // refresh generation (zone activity stamp)
	emit  int    // bumped whenever the plan is finished again (normalizer cache key)

	// Static.
	cls    []uint8        // per definition
	defIdx []int32        // definitions that are defenses (for clearing valByDef)
	cRef   [dcCount]int64 // cheapest tower of our side per class, cost-equivalent
	// The front rules (defense_front.go): on with the layout switch.
	front    bool
	typeOn   bool   // army's type part (army.go), set at every refresh
	dual     []bool // per definition: a missile tower that also serves the ground plan (front rules)
	haveDual bool   // our side can build one
	dualCons bool   // a built constructor can build one (this refresh)
	cBank    int64  // cheapest direct-fire tower of our side: the bank's unit
	nGnd     int32  // ground-plan towers (built or framed)
	nDual    int32  // missile towers among them
	nGrid    int32  // grid zones; choke posts follow them (front rules)
	chokes   []aikit.Choke
	mixG     int32 // with the standing orders: ground-plan towers, for the mix
	mixD     int32 // and missile towers among them
	sRef     int64 // strength of our side's best water tower (naval target unit)
	wX, wZ   int32 // where water towers go this refresh (waterSite)
	zw, zh   int32

	// Persistent history per zone (decaying). Sightings and threat are
	// weighted by the ticks they stood (the refresh interval), so the
	// history does not depend on how often the plan refreshes.
	heatLoss []int64 // own building value lost in the zone recently
	heatThr  []int64 // threat-ticks at the zone's anchor
	atkX     []int64 // Σ weight × x of enemy ground units seen near the zone
	atkZ     []int64
	atkW     []int64  // Σ weight: strength-ticks
	block    []uint32 // tick until which the zone's site is not tried again
	airX     int64    // Σ weight × position of enemy aircraft seen near home
	airZ     int64
	airW     int64

	tracked, spare []trackedBld
	flow           []int64        // per planning sector: decaying crossings by own ground units, as of flowT
	flowT          []uint32       // per planning sector: the tick flow was last brought up to date
	last           []lastPos      // per handle
	failSum        [dcCount]int32 // Σ prodFails over each class's definitions at the last refresh
	lastPick       [dcCount]int32

	// Budget.
	budG       int64 // accrued ground defense budget, cost-equivalent
	budFrac    int64 // accrual below one cost-equivalent unit, ×30000 (carried)
	lossRecent int64 // own non-defense building value lost recently (decaying)

	// Per refresh, per zone (valid when zGen == gen).
	zGen     []uint32
	zAsset   []int64 // ground asset weight (cost-equivalent × role factor)
	zAssetA  []int64 // anti-air asset weight
	zSumX    []int64 // anti-air-weighted position sums (cluster centre)
	zSumZ    []int64
	zAncX    []int32 // anchor: the zone's building nearest the enemy
	zAncZ    []int32
	zAncD    []int64
	zAncFac  []bool           // anchor is a factory (no other building in the zone)
	zBuilt   [dcCount][]int64 // built tower value near the anchor, by class
	zFrame   [dcCount][]int64
	zCommit  [dcCount][]int64
	zCnt     [dcCount][]int32 // built towers near the anchor
	zCntF    [dcCount][]int32 // framed towers near the anchor
	active   []int32
	homeZone int32
	commits  []defCommit
	nOld     int // commits from earlier thinks (the rest are this think's)
	valByDef []int64

	protect int64 // own non-defense building value, cost-equivalent
	exposed bool  // enemy ground units seen near our zones, or a building lost (recently)
	built   [dcCount]int64
	frame   [dcCount]int64
	commit  [dcCount]int64
	have    [dcCount]int64
	cnt     [dcCount]int32  // towers, built or framed
	cntF    [dcCount]int32  // framed towers among them
	share   int64           // ground budget accrual, permille of income (for explain/tests)
	terms   [4]int64        // shareG's base, slack, threat and army terms, permille (audit)
	deficit [dcCount]int64  // by class, cost-equivalent
	behind  [dcCount]uint32 // tick since which the class has been a tower or more behind (0 = not)
	x, z    [dcCount]int32  // chosen site by class
	zone    [dcCount]int32  // chosen zone by class (-1 none)
	urg     [dcCount]int64  // zone urgency by class (permille)

	// Spacing index for clear, rebuilt once per observation: our factories,
	// and our other buildings by clearCell cells.
	spc spacing

	// Normalizer cache: the best quality per class among one builder's products.
	normDef  *aikit.UnitInfo
	normTick uint32
	normEmit int
	normBest [dcCount]int64
	normQV   [dcCount]int64
	normDual bool // the builder can build a missile tower (front rules)
}

// classify says what a defense definition is for.
func classify(u *aikit.UnitInfo, si *staticInfo) uint8 {
	if !u.Role.Has(aikit.RoleDefense) || u.Role.Has(aikit.RoleMobile) || u.DPS <= 0 {
		return dcNone
	}
	if u.Range >= defStrategic {
		return dcNone
	}
	// Negligible sustained fire for its cost: an anti-missile system's
	// nominal weapon, or a building armed only incidentally.
	if int64(u.DPS)*1000 < 20*max64(si.costMeq, 1) {
		return dcNone
	}
	switch {
	case si.water:
		return dcWater
	case si.aaOnly:
		return dcAir
	}
	return dcGround
}

func (pl *defPlan) setup(s *shared, b *core.Board) {
	pl.ready = true
	k := b.K
	t := k.Table
	n := len(t.Units)
	pl.cls = make([]uint8, n)
	pl.dual = make([]bool, n)
	pl.valByDef = make([]int64, n)
	for i, u := range t.Units {
		c := classify(u, &s.info[i])
		pl.cls[i] = c
		if u.Role.Has(aikit.RoleDefense) {
			pl.defIdx = append(pl.defIdx, int32(i))
		}
		if c == dcNone || u.Side != k.Side || u.Depth < 0 {
			continue
		}
		v := s.info[i].costMeq
		if pl.cRef[c] == 0 || v < pl.cRef[c] {
			pl.cRef[c] = v
		}
		if c == dcWater && u.Strength() > pl.sRef {
			pl.sRef = u.Strength()
		}
	}
	for c := range pl.cRef {
		if pl.cRef[c] <= 0 {
			pl.cRef[c] = 500
		}
	}
	pl.cBank = pl.cRef[dcGround]
	if s.p.Layout != 0 {
		pl.setupFront(s, b)
	}
	if pl.sRef <= 0 {
		pl.sRef = 10000
	}
	m := k.Map
	pl.zw = max32((m.WorldW+defZone-1)/defZone, 1)
	pl.zh = max32((m.WorldH+defZone-1)/defZone, 1)
	nz := int(pl.zw * pl.zh)
	pl.nGrid = int32(nz)
	if pl.front && s.zones.choke != nil {
		// Choke posts are zones of their own after the grid's.
		pl.chokes = s.zones.choke.Chokes
		nz += len(pl.chokes)
	}
	pl.heatLoss = make([]int64, nz)
	pl.heatThr = make([]int64, nz)
	pl.atkX = make([]int64, nz)
	pl.atkZ = make([]int64, nz)
	pl.atkW = make([]int64, nz)
	pl.block = make([]uint32, nz)
	pl.zGen = make([]uint32, nz)
	pl.zAsset = make([]int64, nz)
	pl.zAssetA = make([]int64, nz)
	pl.zSumX = make([]int64, nz)
	pl.zSumZ = make([]int64, nz)
	pl.zAncX = make([]int32, nz)
	pl.zAncZ = make([]int32, nz)
	pl.zAncD = make([]int64, nz)
	pl.zAncFac = make([]bool, nz)
	for c := 0; c < int(dcCount); c++ {
		pl.zBuilt[c] = make([]int64, nz)
		pl.zFrame[c] = make([]int64, nz)
		pl.zCommit[c] = make([]int64, nz)
		pl.zCnt[c] = make([]int32, nz)
		pl.zCntF[c] = make([]int32, nz)
	}
	for c := range pl.lastPick {
		pl.lastPick[c] = -1
	}
	pl.flow = make([]int64, int(max32(m.SectorW, 1)*max32(m.SectorH, 1)))
	pl.flowT = make([]uint32, len(pl.flow))
}

func (pl *defPlan) zoneOf(x, z int32) int32 {
	zx, zz := x/defZone, z/defZone
	if zx < 0 {
		zx = 0
	}
	if zz < 0 {
		zz = 0
	}
	if zx >= pl.zw {
		zx = pl.zw - 1
	}
	if zz >= pl.zh {
		zz = pl.zh - 1
	}
	return zz*pl.zw + zx
}

// wScale is w_defense relative to defWRef, permille, capped: the plan's
// amounts (budget share, floor, anti-air and naval targets) follow it, so a
// style that raises w_defense builds more, not only sooner.
func wScale(p *Params) int64 {
	return clamp(int64(p.WDefense)*1000/defWRef, 0, defWMax)
}

// refresh brings the plan up to date for this think: once per tick the
// history, zones, budget and sites; again whenever a builder has taken a
// defense order since (later builders in the same think must see it).
// Economy.Plan refreshes it every think, so losses, sightings and flow are
// seen as they happen (a building built and lost between two refreshes is
// still a loss) and the history's decay follows game time, not how often
// a builder happens to price a tower.
func (pl *defPlan) refresh(s *shared, b *core.Board) {
	if !pl.ready {
		pl.setup(s, b)
	}
	if pl.fresh && pl.tick == s.tick {
		n := len(pl.commits)
		pl.sameThink(s, b)
		if len(pl.commits) != n {
			pl.emit++
			pl.finish(s, b)
		}
		return
	}
	dt := int64(s.tick)
	if pl.fresh {
		dt = int64(s.tick - pl.tick)
	}
	pl.typeOn = s.apart(aType)
	pl.fresh = true
	pl.tick = s.tick
	pl.gen++
	pl.emit++
	pl.decay(dt)
	pl.trackLosses(s, b)
	pl.observeFlow(s, b)
	pl.scan(s, b)
	pl.noteDualCons(b)
	pl.observeEnemy(s, b, dt)
	pl.noteFailures(s)
	pl.accrue(s, b, dt)
	pl.sameThink(s, b)
	pl.finish(s, b)
	// Unspent budget banks at most defBank light towers.
	if lim := pl.have[dcGround] + defBank*pl.cBank; pl.budG > lim {
		pl.budG = lim
	}
	pl.auditThink(s, b)
}

// decay ages the raid history: v × τ / (τ + dt) per refresh, which at a
// refresh every think is close to an exponential decay with time constant
// τ.
func (pl *defPlan) decay(dt int64) {
	if dt <= 0 {
		return
	}
	for i := range pl.heatLoss {
		pl.heatLoss[i] = pl.heatLoss[i] * defTau / (defTau + dt)
		pl.heatThr[i] = pl.heatThr[i] * defTau / (defTau + dt)
		pl.atkX[i] = pl.atkX[i] * defTauAtk / (defTauAtk + dt)
		pl.atkZ[i] = pl.atkZ[i] * defTauAtk / (defTauAtk + dt)
		pl.atkW[i] = pl.atkW[i] * defTauAtk / (defTauAtk + dt)
	}
	pl.airX = pl.airX * defTauAtk / (defTauAtk + dt)
	pl.airZ = pl.airZ * defTauAtk / (defTauAtk + dt)
	pl.airW = pl.airW * defTauAtk / (defTauAtk + dt)
	pl.lossRecent = pl.lossRecent * defTau / (defTau + dt)
}

// observeFlow walks each own ground unit's move since the last refresh,
// crediting every sector it crossed once. Flow, not presence: an army
// parked at its rally adds nothing, a corridor units keep walking through
// adds a lot. The move is walked in a straight line (a think apart, it is
// short), but only while the line stays on ground the unit's movement
// class can stand on and in the region it started from (with the terrain
// model): past a cliff or a lake the unit went around, not through.
func (pl *defPlan) observeFlow(s *shared, b *core.Board) {
	m := b.K.Map
	o := b.O
	for i := range o.Own {
		u := &o.Own[i]
		if !u.Built || !u.Info.Role.Has(aikit.RoleMobile) || u.Info.Role.Has(aikit.RoleAir) {
			continue
		}
		pl.last = handleSlots(pl.last, u.H, s.hcap)
		lp := &pl.last[u.H]
		same := lp.def == u.Info && lp.gen == u.Gen
		if same && lp.x == u.X && lp.z == u.Z {
			continue // unmoved: the record, and its region, still hold
		}
		var end int32
		known := false
		if same {
			var r *aikit.Reach
			var reg int32
			if c := s.info[u.Info.Index].cls; c >= 0 && s.terr.ready {
				r = s.terr.cls[c].r
				if reg = int32(lp.reg); !lp.known {
					reg = r.At(lp.x, lp.z)
				}
			}
			n := aikit.Dist(lp.x, lp.z, u.X, u.Z)/aikit.SectorWorld + 1
			prev := m.Sector(lp.x, lp.z)
			for k := int32(1); k <= n; k++ {
				x := lp.x + int32(int64(u.X-lp.x)*int64(k)/int64(n))
				z := lp.z + int32(int64(u.Z-lp.z)*int64(k)/int64(n))
				if reg != 0 {
					at := r.At(x, z)
					if k == n {
						// The last step stands where the unit stands now:
						// the next walk starts from this region.
						end, known = at, true
					}
					if at != reg {
						break
					}
				}
				if sec := m.Sector(x, z); sec != prev {
					pl.addFlow(sec)
					prev = sec
				}
			}
		}
		*lp = lastPos{def: u.Info, gen: u.Gen, x: u.X, z: u.Z, reg: uint16(end), known: known}
	}
}

// flowAt is a sector's flow at the plan's tick: it decays v × τ / (τ + dt)
// from the last crossing, so it does not depend on how often the plan
// refreshes.
func (pl *defPlan) flowAt(sec int32) int64 {
	f := pl.flow[sec]
	if dt := int64(pl.tick - pl.flowT[sec]); f > 0 && dt > 0 {
		f = f * defTauFlow / (defTauFlow + dt)
	}
	return f
}

// addFlow credits one crossing of a sector.
func (pl *defPlan) addFlow(sec int32) {
	pl.flow[sec] = pl.flowAt(sec) + defCross
	pl.flowT[sec] = pl.tick
}

// busy reports whether (x, z) lies on a corridor our units keep walking.
func (pl *defPlan) busy(m *aikit.MapInfo, x, z int32, limit int64) bool {
	return pl.flowAt(m.Sector(x, z)) > limit
}

// flowLimit is the flow above which a site near (x, z) is a corridor.
func (pl *defPlan) flowLimit(m *aikit.MapInfo, x, z int32) int64 {
	cx, cz := x/aikit.SectorWorld, z/aikit.SectorWorld
	var peak int64
	for sz := cz - defFlowReach; sz <= cz+defFlowReach; sz++ {
		if sz < 0 || sz >= m.SectorH {
			continue
		}
		for sx := cx - defFlowReach; sx <= cx+defFlowReach; sx++ {
			if sx < 0 || sx >= m.SectorW {
				continue
			}
			if f := pl.flowAt(sz*m.SectorW + sx); f > peak {
				peak = f
			}
		}
	}
	return max64(peak/defFlowShare, defFlowMin)
}

// trackLosses compares the own buildings seen at the last refresh with this
// observation. A building that is gone, with nothing of ours standing in its
// place (an upgrade or rebuild), was destroyed: its zone remembers the
// loss, and a non-defense loss tops up the defense budget.
func (pl *defPlan) trackLosses(s *shared, b *core.Board) {
	o := b.O
	w := wScale(&s.p)
	for _, t := range pl.tracked {
		if i := b.Index(t.h); i >= 0 {
			u := &o.Own[i]
			if u.Info == t.info && u.Gen == t.gen && u.X == t.x && u.Z == t.z {
				continue
			}
		}
		replaced := false
		for i := range o.Own {
			u := &o.Own[i]
			if !u.Info.Role.Has(aikit.RoleMobile) && aikit.Dist2(u.X, u.Z, t.x, t.z) <= 32*32 {
				replaced = true
				break
			}
		}
		if replaced {
			continue
		}
		v := s.info[t.info.Index].costMeq
		pl.heatLoss[pl.zoneOf(t.x, t.z)] += v
		pl.chokeLoss(s, t.x, t.z, v)
		if !t.info.Role.Has(aikit.RoleDefense) {
			pl.lossRecent += v
			pl.budG += v * defLossTopUp / 1000 * w / 1000
		}
	}
	pl.spare = pl.spare[:0]
	for i := range o.Own {
		u := &o.Own[i]
		if u.Built && !u.Info.Role.Has(aikit.RoleMobile) {
			pl.spare = append(pl.spare, trackedBld{h: u.H, gen: u.Gen, info: u.Info, x: u.X, z: u.Z})
		}
	}
	pl.tracked, pl.spare = pl.spare, pl.tracked
}

// activate marks a zone as holding assets this refresh, resetting its
// per-refresh fields the first time.
func (pl *defPlan) activate(z int32) {
	if pl.zGen[z] == pl.gen {
		return
	}
	pl.zGen[z] = pl.gen
	pl.zAsset[z], pl.zAssetA[z], pl.zSumX[z], pl.zSumZ[z] = 0, 0, 0, 0
	pl.zAncD[z], pl.zAncFac[z] = -1, false
	for c := 0; c < int(dcCount); c++ {
		pl.zBuilt[c][z], pl.zFrame[c][z], pl.zCommit[c][z], pl.zCnt[c][z], pl.zCntF[c][z] = 0, 0, 0, 0, 0
	}
	pl.active = append(pl.active, z)
}

// assetWeights returns a building's ground and anti-air asset factors
// (permille of its cost). Raiders hunt extractors, bombers hunt factories
// and power; energy fields sit behind the base and are cheap to replace.
func assetWeights(r aikit.Role) (int64, int64) {
	switch {
	case r.Has(aikit.RoleExtractor):
		return 3000, 500
	case r.Has(aikit.RoleFactory):
		return 2000, 2000
	case r.Has(aikit.RoleMetalMaker):
		return 1000, 1000
	case r.Any(aikit.RoleEnergy | aikit.RoleStorage):
		return 400, 1000
	case r.Any(aikit.RoleRadar | aikit.RoleSonar):
		return 0, 0
	}
	return 700, 700
}

// scan rebuilds the zones (asset weight, anchor, cluster centre) and the
// own defenses and pending defense orders near each zone's anchor.
func (pl *defPlan) scan(s *shared, b *core.Board) {
	o := b.O
	pl.active = pl.active[:0]
	pl.protect = 0
	for _, idx := range pl.defIdx {
		pl.valByDef[idx] = 0
	}
	for c := 0; c < int(dcCount); c++ {
		pl.built[c], pl.frame[c], pl.cnt[c], pl.cntF[c] = 0, 0, 0, 0
	}
	pl.nGnd, pl.nDual = 0, 0
	ex, ez := s.enemyX, s.enemyZ
	for i := range o.Own {
		u := &o.Own[i]
		r := u.Info.Role
		if !u.Built || r.Any(aikit.RoleMobile|aikit.RoleDefense) {
			continue
		}
		wg, wa := assetWeights(r)
		if wg == 0 && wa == 0 {
			continue
		}
		v := s.info[u.Info.Index].costMeq
		pl.protect += v
		z := pl.zoneOf(u.X, u.Z)
		pl.activate(z)
		pl.zAsset[z] += v * wg / 1000
		ga := v * wa / 1000
		pl.zAssetA[z] += ga
		pl.zSumX[z] += int64(u.X) * ga
		pl.zSumZ[z] += int64(u.Z) * ga
		// Anchor: the building nearest the enemy, a factory only when the
		// zone holds nothing else (towers stay clear of factory exits).
		fac := r.Has(aikit.RoleFactory)
		d := aikit.Dist2(u.X, u.Z, ex, ez)
		if pl.zAncD[z] < 0 || (pl.zAncFac[z] && !fac) || (fac == pl.zAncFac[z] && d < pl.zAncD[z]) {
			pl.zAncX[z], pl.zAncZ[z], pl.zAncD[z], pl.zAncFac[z] = u.X, u.Z, d, fac
		}
		pl.chokeAsset(s, u.X, u.Z, v*wg/1000)
	}
	pl.homeZone = pl.zoneOf(b.HomeX, b.HomeZ)
	if b.Commander >= 0 {
		// The commander's safety is the game: home always has a zone.
		z := pl.homeZone
		pl.activate(z)
		if pl.zAncD[z] < 0 {
			pl.zAncX[z], pl.zAncZ[z], pl.zAncD[z] = b.HomeX, b.HomeZ, aikit.Dist2(b.HomeX, b.HomeZ, ex, ez)
		}
		cb := s.comBonus() // army: the cover part (army.go)
		pl.zAsset[z] += cb
		pl.zAssetA[z] += cb
		pl.zSumX[z] += int64(b.HomeX) * cb
		pl.zSumZ[z] += int64(b.HomeZ) * cb
		pl.chokeAsset(s, b.HomeX, b.HomeZ, cb)
	}
	for _, i := range b.Defenses {
		pl.addTower(s, &o.Own[i], true)
	}
	for _, i := range b.Frames {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleDefense) && !u.Info.Role.Has(aikit.RoleMobile) {
			pl.addTower(s, u, false)
		}
	}
	// Standing defense orders from earlier thinks whose builder is still at
	// it (walking there, or building the frame counted above).
	pl.commits = pl.commits[:0]
	for _, i := range b.Builders {
		u := &o.Own[i]
		if u.Order == aikit.OrderIdle {
			continue
		}
		c := s.commitIf(u)
		if c == nil || c.kind != cDefense || c.prod == nil || c.tick >= s.tick || s.tick-c.tick > 2700 {
			continue
		}
		cl := pl.cls[c.prod.Index]
		if cl == dcNone {
			continue
		}
		pl.commits = append(pl.commits, defCommit{cls: cl, dual: pl.dual[c.prod.Index], v: s.info[c.prod.Index].costMeq, x: c.x, z: c.z})
	}
	pl.nOld = len(pl.commits)
}

// addTower counts one own defense (built, or a frame) toward its class,
// its definition's share, and every zone whose anchor it covers.
func (pl *defPlan) addTower(s *shared, u *aikit.OwnUnit, built bool) {
	cl := pl.cls[u.Info.Index]
	if cl == dcNone {
		return
	}
	v := s.info[u.Info.Index].costMeq
	pl.valByDef[u.Info.Index] += v
	pl.addTo(cl, u, v, built)
	if pl.dual[u.Info.Index] {
		// A missile tower also defends against ground units.
		pl.addTo(dcGround, u, v, built)
		pl.nDual++
	}
	if cl == dcGround || pl.dual[u.Info.Index] {
		pl.nGnd++
	}
}

// addTo counts one tower toward a class and the zones whose anchor it covers.
func (pl *defPlan) addTo(cl uint8, u *aikit.OwnUnit, v int64, built bool) {
	pl.cnt[cl]++
	if built {
		pl.built[cl] += v
	} else {
		pl.cntF[cl]++
		pl.frame[cl] += v
	}
	for _, z := range pl.active {
		if aikit.Dist2(u.X, u.Z, pl.zAncX[z], pl.zAncZ[z]) > defCover*defCover {
			continue
		}
		if built {
			pl.zCnt[cl][z]++
			pl.zBuilt[cl][z] += v
		} else {
			pl.zCntF[cl][z]++
			pl.zFrame[cl][z] += v
		}
	}
}

// sameThink appends the defense orders given earlier in this think.
func (pl *defPlan) sameThink(s *shared, b *core.Board) {
	pl.commits = pl.commits[:pl.nOld]
	o := b.O
	for _, i := range b.Builders {
		u := &o.Own[i]
		c := s.commitIf(u)
		if c == nil || c.kind != cDefense || c.prod == nil || c.tick != s.tick {
			continue
		}
		cl := pl.cls[c.prod.Index]
		if cl == dcNone {
			continue
		}
		pl.commits = append(pl.commits, defCommit{cls: cl, dual: pl.dual[c.prod.Index], v: s.info[c.prod.Index].costMeq, x: c.x, z: c.z})
	}
}

// observeEnemy records where enemy ground units are seen near our zones
// (the approaches they use), the threat standing on each zone's anchor,
// and where enemy aircraft come from.
func (pl *defPlan) observeEnemy(s *shared, b *core.Board, dt int64) {
	o := b.O
	if dt < 1 {
		dt = 1
	}
	for _, z := range pl.active {
		pl.heatThr[z] += int64(b.Threat.At(pl.zAncX[z], pl.zAncZ[z])) * dt
	}
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		if info.DPS <= 0 || !info.Role.Has(aikit.RoleMobile) || o.Tick-r.LastSeen > defFresh {
			continue
		}
		w := (info.Strength()/64 + 1) * dt
		if info.Role.Has(aikit.RoleAir) {
			if aikit.Dist2(r.X, r.Z, b.HomeX, b.HomeZ) <= 3000*3000 {
				pl.airX += int64(r.X) * w
				pl.airZ += int64(r.Z) * w
				pl.airW += w
			}
			continue
		}
		zx, zz := r.X/defZone, r.Z/defZone
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				nx, nz := zx+dx, zz+dz
				if nx < 0 || nz < 0 || nx >= pl.zw || nz >= pl.zh {
					continue
				}
				z := nz*pl.zw + nx
				if pl.zGen[z] != pl.gen {
					continue
				}
				pl.atkX[z] += int64(r.X) * w
				pl.atkZ[z] += int64(r.Z) * w
				pl.atkW[z] += w
			}
		}
	}
}

// noteFailures blames a failed defense placement (the economy counts it in
// prodFails) on the zone last picked for that class: its site is not tried
// again for two minutes.
func (pl *defPlan) noteFailures(s *shared) {
	var sum [dcCount]int32
	for _, idx := range pl.defIdx {
		sum[pl.cls[idx]] += int32(s.prodFails[idx])
		if pl.dual[idx] {
			sum[dcGround] += int32(s.prodFails[idx])
		}
	}
	for c := dcGround; c < dcCount; c++ {
		if z := pl.lastPick[c]; sum[c] > pl.failSum[c] && z >= 0 && pl.block[z] <= s.tick {
			pl.block[z] = s.tick + 3600
		}
	}
	pl.failSum = sum
}

// accrue adds this interval's ground defense budget: shareG of income.
// What falls below a whole unit carries to the next refresh (a think's
// accrual is only a few units).
func (pl *defPlan) accrue(s *shared, b *core.Board, dt int64) {
	pl.share = pl.shareG(s, b)
	inc := s.mInc + s.eInc*10/int64(s.p.ERatio)
	if inc < 0 || dt <= 0 {
		return
	}
	pl.budFrac += pl.share * inc * dt / 1000
	pl.budG += pl.budFrac / 30000
	pl.budFrac %= 30000
}

// shareG is the ground budget's share of income, permille: 15‰ until
// minute 12 (the expansion race, where every unit of economy compounds),
// rising to 70‰ by minute 25, scaled by wScale (3× at the default), by slack (half while
// energy is short or metal is starved: towers cost several times more
// energy than metal, and must not stall the factories), by
// raids (buildings recently lost, danger at our buildings: up to 3×) and by
// the army balance. A siege of the base itself adds nothing: the army and
// production answer it, and towers started under fire die as frames.
func (pl *defPlan) shareG(s *shared, b *core.Board) int64 {
	p := &s.p
	t := int64(s.tick)
	base := 15 + lin(t, 12*1800, 25*1800)*55/one
	if pl.front {
		base = frontBase(t)
		if s.zones.f2.towers {
			base = base * timeBoost(t) / one // tower timing (defense_time.go)
		}
	}
	slack := 500 + min64(lin(s.covE, 700, 1200), lin(s.covM, 400, 900))/2
	th := int64(p.ThreatHalf)
	threat := one + lin(s.danger, 0, 2*th)/2
	if pl.protect > 0 {
		threat += lin(pl.lossRecent*one/pl.protect, 0, 200) * 3 / 2
	}
	threat = min64(threat, 3000)
	slack, threat = s.surplusTerms(slack, threat) // army: towers from surplus (army.go)
	army := armyBalance(s, b)
	pl.terms = [4]int64{base, slack, threat, army}
	return base * wScale(p) / one * slack / one * threat / one * army / one
}

// armyBalance is the army term of the budget share, permille: down to half
// while our army outnumbers the enemy's estimate, up to 1.5× while it is
// outnumbered three to one. Neutral until both our army and the enemy army
// we have actually seen are worth a wave: before that the ratio says
// nothing (seeing only scouts resets the model's staleness prior, so an
// unseen main army reads as no army).
func armyBalance(s *shared, b *core.Board) int64 {
	if b.ArmyValue < 1500 || s.enArmy < 1500 {
		return one
	}
	if r := s.armyRatio; r < one {
		return 500 + r/2
	} else {
		return one + lin(r, one, 3000)/2
	}
}

// floorG is the opening's ground target, whatever the income: the first
// light tower between 3:30 and 5:00, and a second between 8:00 and 10:00
// once the approaches have proven exposed (enemy ground units seen near
// our buildings, or a building lost). Two unconditional opening towers
// cost util+tac a few points of share against itself; one does not.
func (pl *defPlan) floorG(s *shared) int64 {
	t := int64(s.tick)
	n := lin(t, 3*1800+900, 5*1800)
	if pl.exposed {
		n += lin(t, 8*1800, 10*1800)
	}
	return pl.cRef[dcGround] * n / one * min64(wScale(&s.p), 1500) / one
}

// finish totals the defenses with the pending orders, sets each class's
// deficit, and picks where the next tower of each class goes.
func (pl *defPlan) finish(s *shared, b *core.Board) {
	for c := 0; c < int(dcCount); c++ {
		pl.commit[c] = 0
		for _, z := range pl.active {
			pl.zCommit[c][z] = 0
		}
	}
	pl.mixG, pl.mixD = pl.nGnd, pl.nDual
	for i := range pl.commits {
		c := &pl.commits[i]
		pl.commit[c.cls] += c.v
		if c.dual {
			pl.commit[dcGround] += c.v
			pl.mixD++
		}
		if c.dual || c.cls == dcGround {
			pl.mixG++
		}
		for _, z := range pl.active {
			if aikit.Dist2(c.x, c.z, pl.zAncX[z], pl.zAncZ[z]) <= defCover*defCover {
				pl.zCommit[c.cls][z] += c.v
				if c.dual {
					pl.zCommit[dcGround][z] += c.v
				}
			}
		}
	}
	for c := 0; c < int(dcCount); c++ {
		pl.have[c] = pl.built[c] + max64(pl.frame[c], pl.commit[c])
	}
	pl.exposed = pl.lossRecent > 0
	for _, z := range pl.active {
		if pl.atkW[z] > 0 {
			pl.exposed = true
		}
	}
	w := wScale(&s.p)
	t := int64(s.tick)
	// Ground: the accrued budget, or the opening floor.
	pl.deficit[dcGround] = min64(max64(pl.budG, pl.floorG(s))-pl.have[dcGround], defBank*pl.cBank)
	if s.zones.f2.towers && pl.timeCeiling(s) {
		pl.deficit[dcGround] = min64(pl.deficit[dcGround], 0) // tower timing's ceiling (defense_time.go)
	}
	if pl.ceiling(s) {
		pl.deficit[dcGround] = min64(pl.deficit[dcGround], 0) // army: the value ceiling (army.go)
	}
	// Anti-air: a share of what we protect while enemy aircraft are a
	// concern (s.aaNeed falls as our own anti-air grows); once aircraft are
	// possible, a standing share of the ground defenses (to 40% of their
	// value by minute 16 — human players' towers are mostly cheap missile
	// towers) and at least a screen of two towers by minute 20.
	aa := s.aaNeed * max64(pl.protect, 3000) / one * 120 / one * w / one
	screen := pl.cRef[dcAir] * 2 * lin(t, 15*1800, 20*1800) / one * min64(w, 2000) / one
	share := pl.have[dcGround] * 400 / one * lin(t, 10*1800, 16*1800) / one
	pl.deficit[dcAir] = max64(max64(aa, screen), share) - pl.have[dcAir]
	// Water: against a seen navy, up to three towers' worth; with the
	// naval switch also one while danger stands at the naval base or at a
	// water extractor (waterDanger's case: ships raiding the sea economy).
	pl.deficit[dcWater] = 0
	wx, wz, wok := pl.waterSite(s, b)
	if wok {
		n := min64(b.EnemyNaval*one/pl.sRef, 3*one)
		if s.p.Naval != 0 && s.terr.ready && s.danger > 0 && pl.waterRaid(s, b) {
			n = max64(n, one)
		}
		if n > 0 {
			pl.deficit[dcWater] = pl.cRef[dcWater]*n/one*w/one - pl.have[dcWater]
		}
	}
	pl.wX, pl.wZ = wx, wz
	for c := dcGround; c < dcCount; c++ {
		switch {
		case pl.deficit[c] < pl.cRef[c]:
			pl.behind[c] = 0
		case pl.behind[c] == 0:
			pl.behind[c] = s.tick
		}
	}
	pl.pickZone(s, b, dcGround)
	pl.pickZone(s, b, dcAir)
	pl.pickWater(s, b)
}

// overdue is the need multiplier (permille) of a class that has been a
// tower or more behind: up to 2× over two minutes, so a plan the economy
// keeps deferring becomes timely without growing.
func (pl *defPlan) overdue(s *shared, cl uint8) int64 {
	if pl.behind[cl] == 0 {
		return one
	}
	return one + lin(int64(s.tick-pl.behind[cl]), 0, 3600)
}

// front is 0 at home rising to 500 midway to the enemy base.
func front(s *shared, b *core.Board, x, z int32) int64 {
	dh := int64(aikit.Dist(x, z, b.HomeX, b.HomeZ))
	de := int64(aikit.Dist(x, z, s.enemyX, s.enemyZ))
	return clamp(dh*one/(dh+de+1), 0, one)
}

// priority is a zone's claim on the next tower of a class: its asset
// weight (square-root compressed, so the home base does not absorb every
// tower) times its exposure, per tower already there — the next tower goes
// to the zone with the most exposure per defense (a D'Hondt allocation).
func (pl *defPlan) priority(s *shared, b *core.Board, z int32, cl uint8) (int64, int64) {
	have := pl.zBuilt[cl][z] + max64(pl.zFrame[cl][z], pl.zCommit[cl][z])
	f := front(s, b, pl.zAncX[z], pl.zAncZ[z])
	cr := pl.cRef[cl]
	raid := lin(pl.heatLoss[z], 0, 3*cr)
	var asset, exp int64
	if z >= pl.nGrid && (cl != dcGround || pl.chokeFull(z)) {
		return -1, 0 // a choke post is held against ground units only
	}
	if cl == dcAir {
		asset = pl.zAssetA[z]
		exp = one + f/2
	} else {
		asset = pl.zAsset[z]
		// Threat standing on the anchor: full at threat_half for a minute
		// (heatThr is in threat-ticks).
		heat := lin(pl.heatThr[z], 0, int64(s.p.ThreatHalf)*60*30) / 2
		exp = 500 + 2*f + raid + heat
		if pl.front {
			exp = s.coverExp(z, pl.homeZone, pl.placeExp(s, z, frontExp(f))) + raid + heat // army: where attacks came, the commander's zone (army.go)
		}
	}
	if asset <= 0 {
		return -1, 0
	}
	w := aikit.ISqrt64(asset*one) * exp / one
	// Towers in range of each other multiply (square law) while a lone
	// tower away from home is picked off and hands its killer points, so
	// outside the home zone a first tower is only worth half: the plan
	// completes a pair before it opens another strongpoint.
	div := have + cr
	if have == 0 && z != pl.homeZone {
		div = 2 * cr
	}
	// A zone that keeps losing buildings needs its tower before the
	// rebuild, or the rebuild dies too: urgency up to 3×.
	return w * one / div, one + 2*raid
}

// towersAt counts a class's towers at a zone's anchor, standing and to
// come: built, then the larger of the frames and the standing orders
// (a frame's builder holds an order for it until it is done, so the two
// overlap, as in have).
func (pl *defPlan) towersAt(zone int32, cl uint8) int32 {
	var orders int32
	for i := range pl.commits {
		c := &pl.commits[i]
		if (c.cls == cl || (c.dual && cl == dcGround)) && aikit.Dist2(c.x, c.z, pl.zAncX[zone], pl.zAncZ[zone]) <= defCover*defCover {
			orders++
		}
	}
	if f := pl.zCntF[cl][zone]; f > orders {
		orders = f
	}
	return pl.zCnt[cl][zone] + orders
}

// pickZone chooses the zone and site of the next tower of a class.
func (pl *defPlan) pickZone(s *shared, b *core.Board, cl uint8) {
	pl.zone[cl], pl.urg[cl] = -1, 0
	if pl.deficit[cl] <= 0 {
		return
	}
	var tried [timeZones]int32
	tries := 3
	if s.zones.f2.towers {
		tries = timeZones // tower timing (defense_time.go)
	}
	for attempt := 0; attempt < tries; attempt++ {
		best, bestP, bestU := int32(-1), int64(-1), int64(0)
		for _, z := range pl.active {
			if pl.block[z] > s.tick {
				continue
			}
			skip := false
			for j := 0; j < attempt; j++ {
				if tried[j] == z {
					skip = true
				}
			}
			if skip {
				continue
			}
			p, u := pl.priority(s, b, z, cl)
			if p > bestP {
				best, bestP, bestU = z, p, u
			}
		}
		if best < 0 {
			return
		}
		tried[attempt] = best
		if x, z, ok := pl.site(s, b, best, cl); ok {
			pl.zone[cl], pl.urg[cl], pl.x[cl], pl.z[cl] = best, bestU, x, z
			pl.lastPick[cl] = best
			return
		}
	}
}

// approach is the unit direction (×1000) from (x, z) toward where attacks
// on zone z come from: the enemy base, pulled toward the enemy ground units
// seen near the zone (or, for anti-air, toward where aircraft came from).
func (pl *defPlan) approach(s *shared, x, z int32, zone int32, cl uint8) (int64, int64) {
	dx, dz := unit(int64(s.enemyX-x), int64(s.enemyZ-z))
	var ax, az, aw int64
	if cl == dcAir {
		if pl.airW > 0 {
			ax, az, aw = pl.airX/pl.airW, pl.airZ/pl.airW, pl.airW
		}
	} else if pl.atkW[zone] > 0 {
		ax, az, aw = pl.atkX[zone]/pl.atkW[zone], pl.atkZ[zone]/pl.atkW[zone], pl.atkW[zone]
	}
	if aw > 0 {
		// Observed approaches outweigh the prior once a few units' worth
		// of sightings (by strength and time: 2000 strength-seconds) have
		// been seen.
		k := lin(aw, 0, 2000*30) * 2
		hx, hz := unit(ax-int64(x), az-int64(z))
		dx, dz = unit(dx*one+hx*k, dz*one+hz*k)
	}
	if dx == 0 && dz == 0 {
		dz = one
	}
	return dx, dz
}

// unit normalizes a vector to length 1000 (0 for the zero vector).
func unit(x, z int64) (int64, int64) {
	d := aikit.ISqrt64(x*x + z*z)
	if d == 0 {
		return 0, 0
	}
	return x * one / d, z * one / d
}

// lateral is the slot offset of the k-th tower of a zone: 0, +1, −1, +2,
// −2, +3, −3 lanes; later towers start a second line further out.
func lateral(k int32) (int64, int64) {
	row := int64(k / 7)
	k %= 7
	side := int64((k + 1) / 2)
	if k%2 == 0 {
		side = -side
	}
	return side * defLane, row * defLane
}

// site places the next tower of a class in a zone: ground towers ahead of
// the zone's front building, anti-air towers ahead of the cluster centre,
// each in the zone's next lateral slot, and never beside a factory, in
// front of its exit, or on a corridor our own units keep walking.
func (pl *defPlan) site(s *shared, b *core.Board, zone int32, cl uint8) (int32, int32, bool) {
	if zone >= pl.nGrid {
		return pl.chokeSite(b, zone)
	}
	m := b.K.Map
	x, z := pl.zAncX[zone], pl.zAncZ[zone]
	ahead := int64(defAhead)
	if pl.front && cl == dcGround {
		ahead += frontAhead(front(s, b, x, z))
	}
	if cl == dcAir && pl.zAssetA[zone] > 0 {
		x = int32(pl.zSumX[zone] / pl.zAssetA[zone])
		z = int32(pl.zSumZ[zone] / pl.zAssetA[zone])
		ahead = 128
	}
	dx, dz := pl.approach(s, x, z, zone, cl)
	side, row := lateral(pl.towersAt(zone, cl))
	limit := pl.flowLimit(m, x, z)
	// Candidate offsets (ahead, lateral): the slot, then further out, then
	// wider to either side — the flanks of a corridor the slot would block.
	cands := [...][2]int64{{0, 0}, {128, 0}, {256, 0}, {384, 0},
		{0, 2 * defLane}, {0, -2 * defLane}, {128, 3 * defLane}, {128, -3 * defLane},
		{0, 4 * defLane}, {0, -4 * defLane}, {256, 5 * defLane}, {256, -5 * defLane}}
	for _, c := range cands {
		a := ahead + row + c[0]
		l := side + c[1]
		px := clampWorld(x+int32((dx*a-dz*l)/one), m.WorldW)
		pz := clampWorld(z+int32((dz*a+dx*l)/one), m.WorldH)
		if pl.clear(b, px, pz) && !pl.busy(m, px, pz, limit) && !pl.inNeck(px, pz) {
			return px, pz, true
		}
	}
	return 0, 0, false
}

// clearCell is the spacing index's cell edge: at least defSpace and
// defGap, so every building close enough to refuse a point lies in the
// 3×3 cells around it.
const clearCell = 256

// spacing indexes our buildings for clear: factories in a list (their exit
// corridor reaches defFacExit ahead), everything else by cell.
type spacing struct {
	obs   *aikit.Obs
	tick  uint32
	n     int
	facs  []int32 // O.Own indices of our factories (built or framed)
	w, h  int32
	start []int32 // per cell: first index into item (then one past the last cell)
	item  []int32 // O.Own indices of our other buildings, by cell
	fill  []int32 // scratch
}

// index rebuilds the spacing index when the observation has changed.
func (sp *spacing) index(b *core.Board) {
	o := b.O
	if sp.obs == o && sp.tick == o.Tick && sp.n == len(o.Own) {
		return
	}
	sp.obs, sp.tick, sp.n = o, o.Tick, len(o.Own)
	m := b.K.Map
	sp.w, sp.h = max32(m.WorldW/clearCell+1, 1), max32(m.WorldH/clearCell+1, 1)
	sp.facs = sp.facs[:0]
	sp.start = append(sp.start[:0], make([]int32, sp.w*sp.h+1)...)
	for i := range o.Own {
		u := &o.Own[i]
		switch r := u.Info.Role; {
		case r.Has(aikit.RoleMobile):
		case r.Has(aikit.RoleFactory):
			sp.facs = append(sp.facs, int32(i))
		default:
			sp.start[sp.cell(u.X, u.Z)+1]++
		}
	}
	for c := int32(1); c <= sp.w*sp.h; c++ {
		sp.start[c] += sp.start[c-1]
	}
	sp.item = append(sp.item[:0], make([]int32, sp.start[sp.w*sp.h])...)
	sp.fill = append(sp.fill[:0], sp.start[:sp.w*sp.h]...)
	for i := range o.Own {
		u := &o.Own[i]
		if r := u.Info.Role; !r.Has(aikit.RoleMobile) && !r.Has(aikit.RoleFactory) {
			c := sp.cell(u.X, u.Z)
			sp.item[sp.fill[c]] = int32(i)
			sp.fill[c]++
		}
	}
}

func (sp *spacing) cell(x, z int32) int32 {
	cx := min(max(x/clearCell, 0), sp.w-1)
	cz := min(max(z/clearCell, 0), sp.h-1)
	return cz*sp.w + cx
}

// clear reports whether a tower point keeps its distance: from every own
// factory (built or framed) not within defFacClear of its centre, nor in
// the corridor defFacExit deep in front of its exit (+Z, the side units
// leave by and the executor keeps clear); defSpace from every other own
// tower, framed tower or tower order; defGap from any other own building.
// Towers never clump into a wall, and every gap they leave is walkable.
func (pl *defPlan) clear(b *core.Board, x, z int32) bool {
	o := b.O
	sp := &pl.spc
	sp.index(b)
	for _, i := range sp.facs {
		f := &o.Own[i]
		if aikit.Dist2(x, z, f.X, f.Z) < defFacClear*defFacClear {
			return false
		}
		hw := f.Info.FootX*8 + 64
		hz := f.Info.FootZ * 8
		if x >= f.X-hw && x <= f.X+hw && z >= f.Z-hz && z <= f.Z+hz+defFacExit {
			return false
		}
	}
	c := sp.cell(x, z)
	cx, cz := c%sp.w, c/sp.w
	for zz := max(cz-1, 0); zz <= min(cz+1, sp.h-1); zz++ {
		for xx := max(cx-1, 0); xx <= min(cx+1, sp.w-1); xx++ {
			k := zz*sp.w + xx
			for _, i := range sp.item[sp.start[k]:sp.start[k+1]] {
				f := &o.Own[i]
				d2 := aikit.Dist2(x, z, f.X, f.Z)
				if f.Info.Role.Has(aikit.RoleDefense) {
					if d2 < defSpace*defSpace {
						return false
					}
				} else if d2 < defGap*defGap {
					return false
				}
			}
		}
	}
	for i := range pl.commits {
		c := &pl.commits[i]
		if aikit.Dist2(x, z, c.x, c.z) < defSpace*defSpace {
			return false
		}
	}
	return true
}

// waterSite is where water towers go: with the naval switch's terrain
// model, the naval base (the shipyard site, in a sea that matters);
// otherwise the water anchor near home.
func (pl *defPlan) waterSite(s *shared, b *core.Board) (int32, int32, bool) {
	if s.p.Naval != 0 && s.terr.ready {
		if s.terr.navOK {
			return s.terr.navX, s.terr.navZ, true
		}
		return 0, 0, false
	}
	return s.waterX, s.waterZ, s.haveWater
}

// waterRaid reports whether the remembered danger stands at the naval
// base or at one of our water extractors.
func (pl *defPlan) waterRaid(s *shared, b *core.Board) bool {
	const near = 900 * 900
	if s.terr.navOK && aikit.Dist2(s.dangerX, s.dangerZ, s.terr.navX, s.terr.navZ) < near {
		return true
	}
	o := b.O
	for i := range o.Own {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleExtractor) && s.info[u.Info.Index].water && aikit.Dist2(s.dangerX, s.dangerZ, u.X, u.Z) < near {
			return true
		}
	}
	return false
}

// pickWater places the next water tower at the water site, in its next
// lateral slot (placement then finds water nearby).
func (pl *defPlan) pickWater(s *shared, b *core.Board) {
	pl.zone[dcWater] = -1
	if pl.deficit[dcWater] <= 0 {
		return
	}
	m := b.K.Map
	var orders int32
	for i := range pl.commits {
		if pl.commits[i].cls == dcWater {
			orders++
		}
	}
	// Built towers, then frames or orders (which overlap), as towersAt.
	n := pl.cnt[dcWater] - pl.cntF[dcWater] + max(pl.cntF[dcWater], orders)
	dx, dz := unit(int64(s.enemyX-pl.wX), int64(s.enemyZ-pl.wZ))
	side, row := lateral(n)
	pl.x[dcWater] = clampWorld(pl.wX+int32((dx*row-dz*side)/one), m.WorldW)
	pl.z[dcWater] = clampWorld(pl.wZ+int32((dz*row+dx*side)/one), m.WorldH)
	pl.zone[dcWater], pl.urg[dcWater] = 0, one
}

// quality is a tower's fighting value for its cost: Lanchester strength
// (damage per second of the class's fire — anti-air fire for the air
// class, ground fire (groundDPS) for the ground class — × hit points)
// times a range factor
// — √(range/300) for direct fire (reach, but a tower still fights whatever
// reaches it); range/300 at half for ballistic fire, which shells a group
// for its whole approach but misses moving targets — over
// C × (C + vr) where vr = defRefSecs of spendable supply. With little income
// that is the square law (cheap towers win); as income grows it tends to
// strength per cost, so heavier towers take over.
func quality(s *shared, p *aikit.UnitInfo, cl uint8, vr int64) int64 {
	c := max64(s.info[p.Index].costMeq, 1)
	dps := int64(p.DPS)
	switch cl {
	case dcAir:
		dps = int64(p.AirDPS)
	case dcGround:
		dps = s.info[p.Index].gndDPS
	}
	r := clamp(int64(p.Range)*one/300, 250, 4000)
	rf := aikit.ISqrt64(r * one)
	if p.Role.Has(aikit.RoleArtillery) {
		rf = r / 2
	}
	return dps * int64(p.HP) * rf * one / (c * (c + vr))
}

// blend is vr in quality: defRefSecs of spendable supply.
func blend(s *shared) int64 {
	if s.apart(aType) {
		return aTypeVR // army: the square law among towers (army.go)
	}
	return clamp(s.spendable*defRefSecs/1000, 150, 6000)
}

// varietyOf is half(share of the product among our towers of its class,
// 400‰): 1000 for a product we have none of.
func (pl *defPlan) varietyOf(p *aikit.UnitInfo, cl uint8) int64 {
	return half(pl.valByDef[p.Index]*one/max64(pl.have[cl], 1), 400)
}

// defMinQ: a product with less than this share (permille) of the builder's
// best quality in its class is not worth a tower, whatever variety says.
const defMinQ = 250

// norms returns the builder's best quality per class and its best
// quality × variety among the products worth building, this think. A
// product is ranked against what this builder could build instead, so a
// builder with one tower type is not penalized for owning several of it.
func (pl *defPlan) norms(s *shared, bi *aikit.UnitInfo, cl uint8, vr int64) (int64, int64) {
	if pl.normDef != bi || pl.normTick != s.tick || pl.normEmit != pl.emit {
		pl.normDef, pl.normTick, pl.normEmit = bi, s.tick, pl.emit
		for c := range pl.normBest {
			pl.normBest[c], pl.normQV[c] = 1, 1
		}
		pl.normDual = false
		for _, p := range bi.Builds {
			c := pl.cls[p.Index]
			if c == dcNone {
				continue
			}
			if q := quality(s, p, c, vr); q > pl.normBest[c] {
				pl.normBest[c] = q
			}
			if pl.dual[p.Index] {
				pl.normDual = true
				if q := quality(s, p, dcGround, vr); q > pl.normBest[dcGround] {
					pl.normBest[dcGround] = q
				}
			}
		}
		for _, p := range bi.Builds {
			c := pl.cls[p.Index]
			if c == dcNone {
				continue
			}
			pl.normQVOf(s, p, c, vr)
			if pl.dual[p.Index] {
				pl.normQVOf(s, p, dcGround, vr)
			}
		}
	}
	return pl.normBest[cl], pl.normQV[cl]
}

// normQVOf raises the builder's best quality × variety (× mix) of class c
// with product p, when p is worth building in that class.
func (pl *defPlan) normQVOf(s *shared, p *aikit.UnitInfo, c uint8, vr int64) {
	qn := quality(s, p, c, vr) * one / pl.normBest[c]
	if qn < defMinQ {
		return
	}
	if qv := mul(mul(qn, pl.varietyOf(p, c)), pl.mixOf(p, c)); qv > pl.normQV[c] {
		pl.normQV[c] = qv
	}
}
