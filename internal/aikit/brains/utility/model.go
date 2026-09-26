package utility

import (
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// windDivisor converts an authored wind speed into the generator scalar
// (speed ÷ 5000, capped at 1) [05 "Wind generation"].
const windDivisor = 5000

// affordHalf is the number of seconds of current supply a purchase may take
// before its affordability consideration halves.
const affordHalf = 90

// staticInfo is what the brain derives once per definition (indexed by
// UnitInfo.Index).
type staticInfo struct {
	geo        bool  // needs a geothermal vent
	water      bool  // must stand in water
	canEco     bool  // mobile builder that can build an extractor or energy
	aaOnly     bool  // armed only (or mostly) against aircraft
	gndDPS     int64 // damage per second its weapons can put on ground targets (groundDPS)
	combat     bool  // an eligible combat product
	landCombat int32 // factory: number of land combat products
	quality    int64 // factory: best ground efficiency among its products
	costMeq    int64 // metal + energy priced at e_ratio
	cls        int8  // movement class in the terrain model, -1 for aircraft and buildings
	// With the switches on: an eligible combat product (ships included),
	// and a factory's best reach-weighted efficiency (refreshed when the
	// believed enemy base changes).
	combatN   bool
	qualityR  int64
	airFac    bool              // factory whose combat products all fly
	mexes     []*aikit.UnitInfo // mobile builder: its extractor products, cheapest first
	advCon    bool              // advanced constructor (tech)
	spotFits  []bool            // mobile builder: per metal spot, one of its extractors stands on it (fitsOf)
	waterWork int32             // constructor: standing water work at the naval base (naval)
	t2        bool              // tech-2 factory: makes an advanced constructor
	hull      bool              // naval combat product (fleet)
	light     bool              // light boat: a quarter of its shipyard's heaviest hull or less (fleet)
}

// commitKind is what a builder was last told to do.
type commitKind uint8

const (
	cNone commitKind = iota
	cMex
	cEnergy
	cMaker
	cStorage
	cFactory
	cDefense
	cRadar
	cUpgrade // replace one of our extractors by a richer one
	cAssist  // help a nanoframe
	cGuard   // guard (assist) a factory
	cRetreat
	cUnblock // reclaiming what seals a factory's exit
	cClear   // reclaiming wrecks and street features
)

var commitNames = [...]string{"none", "mex", "energy", "maker", "storage", "factory", "defense", "radar", "upgrade", "assist", "guard", "retreat", "unblock", "clear"}

func (c commitKind) build() bool { return c >= cMex && c <= cUpgrade }

// commitment is a builder's standing assignment, indexed by handle. def
// and gen say which unit it belongs to: the pool recycles slots without a
// generation, so a handle alone does not (aikit.OwnUnit.Gen).
type commitment struct {
	def    *aikit.UnitInfo // builder definition
	gen    uint32          // builder instance (slot reuse guard)
	kind   commitKind
	prod   *aikit.UnitInfo
	spot   int32
	x, z   int32
	target pool.Handle
	tick   uint32
	score  int64
	gainM  int64 // milli-metal per second expected
	gainE  int64 // milli-energy per second expected
}

// owns reports whether the commitment belongs to this unit (not to an
// earlier unit that held the same slot).
func (c *commitment) owns(u *aikit.OwnUnit) bool { return c.def == u.Info && c.gen == u.Gen }

// builderState tracks placement failures of one builder (by handle).
type builderState struct {
	def        *aikit.UnitInfo
	blockProd  *aikit.UnitInfo
	gen        uint32
	stuckUntil uint32 // boxed in: only assist until then
	blockSpot  int32
	blockUntil uint32
	// placeOf's answer (builderPlace's region, and its class in placeC)
	// and where the builder stood when it was asked: a region depends only
	// on the terrain and that position.
	placeX, placeZ int32
	placeReg       int32
	streak         uint8 // consecutive builds that left it idle
	placed         bool
	placeC         int8
}

// factoryState tracks whether a factory is making progress (by handle).
type factoryState struct {
	def       *aikit.UnitInfo
	gen       uint32
	lastFrame uint32 // last tick a nanoframe stood on its pad (or first seen)
	lastClear uint32
	clears    uint8
	blocked   bool // queued work but nothing started for padStallTicks
	dead      bool // still blocked after repeated clears: its exit is walled in
	// Layout switch: when a builder was last sent to open its exit, and
	// how many times.
	lastUnblock uint32
	unblocks    uint8
}

// deadClears is how many pad clears a blocked factory gets before it is
// written off.
const deadClears = 3

// padStallTicks is how long a factory may hold a queue without starting a
// unit before its pad is considered blocked.
const padStallTicks = 600

// onPad reports whether a point lies on a factory's footprint (plus a cell).
func onPad(f *aikit.OwnUnit, x, z int32) bool {
	hx, hz := f.Info.FootX*8+16, f.Info.FootZ*8+16
	return x >= f.X-hx && x <= f.X+hx && z >= f.Z-hz && z <= f.Z+hz
}

// shared is the state the three policies share: static tables, the
// per-think economic and enemy model, commitments and the action budget.
type shared struct {
	p    Params
	k    *aikit.Kit
	init bool

	// Static.
	info       []staticInfo
	rMB, rEB   int64 // metal / energy per work unit of buildings, ppm
	rMU, rEU   int64 // metal / energy per work unit of mobile products, ppm
	windExp    int64 // expected wind scalar, permille (map range)
	windStd    int64 // wind scalar standard deviation relative to its mean, permille
	haveWater  bool  // a water anchor near home exists
	waterX     int32
	waterZ     int32
	homeLocked bool

	// Per think.
	tick      uint32
	lastThink uint32
	minutes   int32
	curWind   int32   // observed wind scalar, permille
	count     []int32 // own units (built + frames) per definition
	frameN    []int32 // own immobile frames per definition
	frameGM   []int64 // their expected metal gain
	commitN   []int32 // live build commitments per definition
	commitGM  []int64
	touched   []int32
	commit    []commitment
	bstate    []builderState
	fstate    []factoryState
	// hcap is the length the per-handle tables take on their first growth
	// (handleSlots), past every handle our pool slice holds; 0 until an
	// observation lists an own unit and the unit limit.
	hcap int

	bpFac, bpBld   int64
	upkeepE        int64
	mexUpkeep      int64 // energy extractors draw, milli per second
	firmE          int64 // steady (non-wind, non-tidal) energy output, milli per second
	windE          int64 // expected wind and tidal output of own generators, milli per second
	eIncExp        int64 // expected energy income (firm + expected wind), milli per second
	needFirm       int64 // permille: steady output against extractor upkeep
	pendM, pendE   int64 // milli per second from frames and walking builders
	pendFacBP      int64
	mInc, eInc     int64 // milli per second
	mExp, eExp     int64
	mStock, eStock int64
	mCap, eCap     int64
	capM, capE     int64 // build-power spend capacity, milli per second
	supplyM        int64
	supplyE        int64
	demandM        int64
	demandE        int64
	spendable      int64 // milli metal-equivalent per second the scarcer resource supports
	covM, covE     int64 // permille
	needM, needE   int64 // permille (1000 = balanced)
	needBP         int64
	cons           int32 // mobile builders able to build economy (built)
	consPending    int32
	builders       int32
	freeSafe       int32
	ownAA          int64
	scouts         int32
	airScouts      int32
	armyCount      int32

	// Places.
	enemyX, enemyZ int32 // where the enemy base is believed to be
	safeX, safeZ   int32 // behind the base: energy field and retreat point
	sideX, sideZ   int32 // beside the base: makers and storage
	frontX, frontZ int32
	facX, facZ     int32
	danger         int64 // decayed max threat at an own building
	dangerX        int32
	dangerZ        int32
	dangerNow      int64

	// Enemy.
	enArmy     int64 // age-discounted seen mobile army value
	enAir      int64 // seen air strength
	enAA       int64 // seen anti-air strength (units and defenses)
	enDef      int64 // seen defense value
	enEst      int64 // army estimate used for attack sizing
	lastArmyAt uint32
	aaNeed     int64 // permille
	defShare   int64 // permille: enemy defense among enemy strength

	// Strategy output mirrors (for explain).
	pressure  int64
	armyRatio int64
	ecoPhase  int32
	ecoShare  int32
	// armyTarget is the army value production pushes toward this think
	// (strategy.go): at least the posture's attack value.
	armyTarget int64

	// Action budget mirror (the executor's APM bucket).
	tokens    int64
	lastFill  uint32
	apmInit   bool
	budget    int32
	spotBlock []uint32
	spotFails []uint8
	prodBlock []uint32
	prodFails []uint8

	// Terrain model and the reach table in use (nil with naval and air off).
	terr        terrain
	reach       []int16
	reachSel    int32
	factories   []int32     // factory definitions in our build tree
	landReach   int64       // best reach of a basic land factory's combat product
	navalShare  int64       // permille: enemy naval strength among enemy mobile strength
	bpFacUseful int64       // factory build power weighted by how far its units reach
	spotOwn     []int32     // per spot: index in O.Own of our extractor on it, -1 (tech)
	traps       []trapTrack // per handle: where a ground combat unit was first seen (layout)
	facTrapped  []int32     // per b.Factories entry: units stuck near it this think (layout)
	navalIdle   int32       // ships that have not moved since launch (naval)
	lastEStall  uint32      // last think the energy store was nearly empty and falling
	clearRest   uint32      // no clearing before this tick (layout)
	zones       zoneCounts  // zoned building counts (layout)
	pile        pileSet     // reclaimable metal around each feature, this think (layout)
	mexSpot     []spotMemo  // per handle: the metal spot a building of ours stands on
	zoneOf      []uint8     // per definition: its zone class (layout)
	zoneDefs    []int32     // definitions with a zone class (layout)
	consDefs    []int32     // constructor types in our build tree
	consRoom    []int32     // per definition: expansion room of that constructor type (naval)
	sdist       spotDist    // per spot: distances to home and the enemy base (spotTerr)
	consSpot    []uint64    // per spot, a word per 64 consDefs: bit j when consDefs[j] counts it as room (freeSafeN)
	tech        techState
	rmix        reachMixState     // plausible-start reach prior (reach_mix)
	hullDefs    []int32           // naval combat products (fleet)
	lightDefs   []int32           // light boats among them (fleet)
	fields      []waterFields     // per definition: water economy field sites (tidal_field)
	fieldDefs   []int32           // definitions with field sites (tidal_field)
	yards       waterFields       // shipyard sites: the naval base, then a ring on its sea (tidal_field)
	yardDefs    []int32           // water factory definitions in our tree (tidal_field)
	yardCls     [maxFields]uint64 // per yard site: the classes that reach it from home (tidal_field)
	mexBuilt    int32             // own finished extractors
	deadFacs    int32             // own factories written off (exit walled in)
	facDefs     []int32           // factory definitions (growth)
	facFam      []int8            // per definition: a factory's family (fac_first)
	firstFam    int32             // this game's first-factory family, famNone for none (fac_first)
	famFacs     []int32           // factories of that family (fac_first)
	arm         armyState         // production against income (army, army.go)
	mt          metalState        // metal use: rules and audit (metal, econ_metal.go)
	open        openState         // the opening policy (open_*, opening.go)
}

func (s *shared) setup(k *aikit.Kit) {
	if s.init {
		return
	}
	s.init = true
	s.k = k
	t := k.Table
	n := len(t.Units)
	s.info = make([]staticInfo, n)
	s.count = make([]int32, n)
	s.frameN = make([]int32, n)
	s.frameGM = make([]int64, n)
	s.commitN = make([]int32, n)
	s.commitGM = make([]int64, n)
	s.prodBlock = make([]uint32, n)
	s.prodFails = make([]uint8, n)
	s.spotBlock = make([]uint32, len(k.Map.Spots))
	s.spotFails = make([]uint8, len(k.Map.Spots))
	er := int64(s.p.ERatio)
	for i, u := range t.Units {
		si := &s.info[i]
		d := u.Def
		si.geo = d != nil && strings.ContainsAny(d.YardMap, "Gg")
		si.water = d != nil && d.MinWaterDepth > 0
		si.costMeq = int64(u.Metal) + int64(u.Energy)*10/er
		si.aaOnly = u.AirDPS > 0 && u.AirDPS*2 >= u.DPS
		si.gndDPS = groundDPS(u)
		si.combat = u.Role.Has(aikit.RoleCombat) && !u.Role.Any(aikit.RoleKamikaze|aikit.RoleNaval|aikit.RoleTransport) && !si.water
		if u.Role.Has(aikit.RoleMobile) && u.Role.Has(aikit.RoleBuilder) {
			for _, p := range u.Builds {
				if p.Role.Any(aikit.RoleExtractor | aikit.RoleEnergy) {
					si.canEco = true
					break
				}
			}
		}
	}
	for i, u := range t.Units {
		if !u.Role.Has(aikit.RoleFactory) {
			continue
		}
		si := &s.info[i]
		for _, q := range u.Builds {
			if !s.info[q.Index].combat {
				continue
			}
			si.landCombat++
			if e := s.eff(q, 0); e > si.quality {
				si.quality = e
			}
		}
	}
	// Typical spend rates per work unit for this side's buildings and the
	// mobile products of its factories: medians, so one odd definition
	// cannot skew them.
	var com *aikit.UnitInfo
	for _, u := range t.Units {
		if !u.Role.Has(aikit.RoleCommander) {
			continue
		}
		if com == nil || (com.Side != k.Side && u.Side == k.Side) {
			com = u
		}
	}
	for i := range s.info {
		s.info[i].cls = -1
	}
	s.reachSel = -2
	if s.p.Naval != 0 || s.p.Air != 0 || s.p.Tech != 0 {
		s.setupN(k, com)
	}
	if s.p.Layout != 0 {
		s.setupZones(k)
	}
	if s.p.Growth != 0 {
		s.setupGrowth(k)
	}
	if s.p.FacFirst != 0 {
		s.setupFamily(k)
	}
	var mb, eb, mu, eu []int64
	if com != nil {
		for _, p := range com.Builds {
			if p.BuildTime <= 0 || p.Role.Has(aikit.RoleMobile) || s.info[p.Index].water || s.info[p.Index].geo {
				continue
			}
			mb = append(mb, int64(p.Metal)*1000000/int64(p.BuildTime))
			eb = append(eb, int64(p.Energy)*1000000/int64(p.BuildTime))
			if !p.Role.Has(aikit.RoleFactory) {
				continue
			}
			for _, q := range p.Builds {
				if q.BuildTime <= 0 || !(s.info[q.Index].combat || s.info[q.Index].canEco) {
					continue
				}
				mu = append(mu, int64(q.Metal)*1000000/int64(q.BuildTime))
				eu = append(eu, int64(q.Energy)*1000000/int64(q.BuildTime))
			}
		}
	}
	s.rMB, s.rEB = median(mb, 50000), median(eb, 350000)
	s.rMU, s.rEU = median(mu, 36000), median(eu, 450000)
	// Expected wind: the scalar is uniform over the authored speed range and
	// capped at 1 per draw.
	lo, hi := int64(k.Map.WindMin), int64(k.Map.WindMax)
	if hi < lo {
		hi = lo
	}
	switch {
	case lo >= windDivisor:
		s.windExp = one
	case hi <= windDivisor:
		s.windExp = (lo + hi) / 2 * one / windDivisor
	default:
		// Fraction f below the cap averages (lo+cap)/2; the rest is capped.
		f := (windDivisor - lo) * one / (hi - lo)
		s.windExp = (f*((lo+windDivisor)/2)*one/windDivisor + (one-f)*one) / one
	}
	// Spread of a uniform draw is range/√12 (√12 ≈ 3.464); every generator
	// shares the one global wind, so this risk does not average out.
	if s.windExp > 0 {
		rng := (min64(hi, windDivisor) - min64(lo, windDivisor)) * one / windDivisor
		s.windStd = rng * one / 3464 * one / s.windExp
	}
}

func median(v []int64, def int64) int64 {
	if len(v) == 0 {
		return def
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return v[len(v)/2]
}

// eff is the cost-efficiency of a combat product: Lanchester-style damage
// × durability over V×(V+v_ref), so v_ref blends between the square law
// (cheap units win) and plain strength per cost. aaNeed (permille) credits
// anti-air damage when enemy aircraft are a concern.
func (s *shared) eff(q *aikit.UnitInfo, aaNeed int64) int64 {
	v := s.info[q.Index].costMeq
	if v < 1 {
		v = 1
	}
	dps := int64(q.DPS) + int64(q.AirDPS)*aaNeed/one
	return dps * int64(q.HP) * 1000000 / (v * (v + int64(s.p.VRef)))
}

// groundDPS is the part of a definition's DPS that can engage ground
// targets: DPS less the fire of its to-air weapons, which never fire at a
// target that is not airborne [06 R-WPN-05 §1]. AirDPS is not that part:
// a stock missile tower's anti-air is credited from its weapon's damage
// table (aikit computeAntiAir), and the same weapon fires at ground units
// for its default damage, which DPS already holds (ARMRL: DPS 23, AirDPS
// 48). Each to-air weapon's DPS is subtracted as aikit's summarize added
// it (the same terms, so the two must change together; aikit.UnitInfo
// could carry the split instead); without a definition (a hand-built
// table) all of DPS counts.
func groundDPS(u *aikit.UnitInfo) int64 {
	g := int64(u.DPS)
	if u.Def == nil {
		return g
	}
	for _, w := range [...]*content.WeaponDef{u.Def.Weapon1Def, u.Def.Weapon2Def, u.Def.Weapon3Def} {
		if w == nil || content.IsWeaponInactive(w) || !w.ToAirWeapon {
			continue
		}
		dmg := int64(w.DamageDefault)
		if dmg <= 0 || dmg >= 4000 {
			continue
		}
		burst, reload := int64(w.Burst), int64(w.ReloadTime)
		if burst < 1 {
			burst = 1
		}
		if reload < 1 {
			reload = 1
		}
		if w.Dropped && reload < 90 {
			reload = 90
		}
		g -= int64(int32(dmg * burst * 30 / reload))
	}
	return max64(g, 0)
}

// gainE is a product's expected energy output, milli per second.
func (s *shared) gainE(p *aikit.UnitInfo) int64 {
	g := int64(p.EnergyMake) * 1000
	if p.WindGen > 0 {
		// Mostly the map's long-run expectation; the current wind nudges it.
		w := (3*s.windExp + int64(s.curWind)) / 4
		g += int64(p.WindGen) * w
	}
	if p.TidalGen > 0 {
		g += int64(p.TidalGen) * int64(s.k.Map.TidalPermille)
	}
	return g
}

// gainM is a non-extractor's metal output, milli per second.
func gainMaker(p *aikit.UnitInfo) int64 {
	if p.Def != nil && p.Def.MakesMetal > 0 {
		return int64(p.Def.MakesMetal) * 1000
	}
	return int64(p.MetalMake) * 10
}

// spotGain is an extractor's output on a spot, milli metal per second.
func spotGain(mex *aikit.UnitInfo, metal int32) int64 {
	return int64(metal) * int64(mex.MetalMake) / 100
}

// spotMemo is the metal spot a building of ours stands on, found once
// (spotOf): a building does not move while it holds its slot.
type spotMemo struct {
	def  *aikit.UnitInfo
	gen  uint32
	spot int32
}

// spotOf is nearestSpot for one of our buildings, remembered per handle
// and instance.
func (s *shared) spotOf(m *aikit.MapInfo, u *aikit.OwnUnit) int32 {
	s.mexSpot = handleSlots(s.mexSpot, u.H, s.hcap)
	sm := &s.mexSpot[u.H]
	if sm.def != u.Info || sm.gen != u.Gen {
		*sm = spotMemo{def: u.Info, gen: u.Gen, spot: nearestSpot(m, u.X, u.Z)}
	}
	return sm.spot
}

// enemyIncome is the milli metal per second the extractors in our memory
// make (for Explain).
func enemyIncome(b *core.Board) int64 {
	m := b.K.Map
	var inc int64
	for i := range b.O.Memory {
		r := &b.O.Memory[i]
		if r.Info.Role.Has(aikit.RoleExtractor) {
			if sp := nearestSpot(m, r.X, r.Z); sp >= 0 {
				inc += spotGain(r.Info, m.Spots[sp].Metal)
			}
		}
	}
	return inc
}

// nearestSpot returns the metal spot within 48 world units of a point.
func nearestSpot(m *aikit.MapInfo, x, z int32) int32 {
	for i := range m.Spots {
		sp := &m.Spots[i]
		if aikit.Dist2(sp.X, sp.Z, x, z) <= 48*48 {
			return int32(i)
		}
	}
	return -1
}

func (s *shared) touch(idx int32) {
	if s.frameN[idx] == 0 && s.commitN[idx] == 0 {
		s.touched = append(s.touched, idx)
	}
}

// handleSlots returns t long enough to index handle h, zero entries added.
// A player's handles all lie in its pool slice, whose first handle is at
// most the lowest one it has observed, so the first growth sizes the table
// once for every handle the slice holds (hcap, set by observeHandles)
// instead of appending entry by entry as new handles appear. With hcap
// unknown (0) or passed, it grows as append does.
func handleSlots[T any](t []T, h pool.Handle, hcap int) []T {
	if int(h) < len(t) {
		return t
	}
	return append(t, make([]T, max(int(h)+1, hcap)-len(t))...)
}

// observeHandles sets hcap from the first observation that lists an own
// unit and the session's per-player unit limit: the owner's lowest handle
// plus the limit is past the end of its slice [05 R-SHARE-01 §7].
func (s *shared) observeHandles(o *aikit.Obs) {
	if s.hcap != 0 || o.UnitLimit <= 0 || len(o.Own) == 0 {
		return
	}
	lo := o.Own[0].H
	for i := range o.Own {
		lo = min(lo, o.Own[i].H)
	}
	s.hcap = min(int(lo)+int(o.UnitLimit)+1, 1<<16)
}

// commitOf returns the unit's commitment slot, growing the table and
// clearing a slot another unit held before.
func (s *shared) commitOf(u *aikit.OwnUnit) *commitment {
	s.commit = handleSlots(s.commit, u.H, s.hcap)
	c := &s.commit[u.H]
	if !c.owns(u) {
		*c = commitment{def: u.Info, gen: u.Gen}
	}
	return c
}

// commitIf returns the unit's commitment when it has one, nil otherwise
// (never growing the table).
func (s *shared) commitIf(u *aikit.OwnUnit) *commitment {
	if int(u.H) >= len(s.commit) {
		return nil
	}
	if c := &s.commit[u.H]; c.owns(u) {
		return c
	}
	return nil
}

func (s *shared) fstateOf(f *aikit.OwnUnit) *factoryState {
	s.fstate = handleSlots(s.fstate, f.H, s.hcap)
	fs := &s.fstate[f.H]
	if fs.def != f.Info || fs.gen != f.Gen {
		*fs = factoryState{def: f.Info, gen: f.Gen, lastFrame: s.tick}
	}
	return fs
}

// observeFactories marks factories whose pad has held no nanoframe for a
// while although their queue is not empty (something is parked on the pad).
func (s *shared) observeFactories(b *core.Board) {
	o := b.O
	for _, fi := range b.Factories {
		f := &o.Own[fi]
		fs := s.fstateOf(f)
		if f.QueueLen == 0 {
			fs.lastFrame, fs.blocked, fs.clears = s.tick, false, 0
			if fs.dead {
				s.bpFac -= int64(f.Info.BuildPower)
			}
			continue
		}
		for _, i := range b.Frames {
			u := &o.Own[i]
			if u.Info.Role.Has(aikit.RoleMobile) && onPad(f, u.X, u.Z) {
				fs.lastFrame = s.tick
				break
			}
		}
		fs.blocked = s.tick-fs.lastFrame > padStallTicks
		if !fs.blocked {
			// Producing again (a clear worked, or the wall was reclaimed).
			fs.clears, fs.dead = 0, false
		}
		if fs.blocked && fs.clears >= deadClears && !fs.dead {
			// Written off: its build power no longer counts, and the next
			// factory of this type is placed in a rotated field.
			fs.dead = true
			s.bpFac -= int64(f.Info.BuildPower)
			if s.prodFails[f.Info.Index] < 250 {
				s.prodFails[f.Info.Index]++
			}
		} else if fs.dead {
			s.bpFac -= int64(f.Info.BuildPower)
		}
	}
	s.deadFacs = 0
	for _, fi := range b.Factories {
		if s.fstateOf(&o.Own[fi]).dead {
			s.deadFacs++
		}
	}
}

func (s *shared) bstateOf(u *aikit.OwnUnit) *builderState {
	s.bstate = handleSlots(s.bstate, u.H, s.hcap)
	bs := &s.bstate[u.H]
	if bs.def != u.Info || bs.gen != u.Gen {
		*bs = builderState{def: u.Info, gen: u.Gen, blockSpot: -1}
	}
	return bs
}

// observe rebuilds the per-think model. It runs first (strategy layer).
func (s *shared) observe(b *core.Board) {
	o := b.O
	k := b.K
	m := k.Map
	p := &s.p
	s.lastThink = s.tick
	s.tick = o.Tick
	s.minutes = int32(o.Tick / 1800)
	s.curWind = o.WindPermille
	s.observeHandles(o)
	for i := range s.count {
		s.count[i] = 0
	}
	for _, idx := range s.touched {
		s.frameN[idx], s.frameGM[idx], s.commitN[idx], s.commitGM[idx] = 0, 0, 0, 0
	}
	s.touched = s.touched[:0]
	s.bpFac, s.bpBld, s.upkeepE, s.mexUpkeep, s.firmE, s.windE = 0, 0, 0, 0, 0, 0
	s.cons, s.consPending, s.ownAA, s.scouts, s.armyCount, s.airScouts = 0, 0, 0, 0, 0, 0
	s.mexBuilt = 0
	var consFrames int32
	for i := range o.Own {
		u := &o.Own[i]
		info := u.Info
		s.count[info.Index]++
		r := info.Role
		if !u.Built {
			if r.Has(aikit.RoleMobile) {
				if s.info[info.Index].canEco {
					consFrames++
				}
				continue
			}
			s.touch(info.Index)
			s.frameN[info.Index]++
			if r.Has(aikit.RoleExtractor) {
				if sp := s.spotOf(m, u); sp >= 0 {
					s.frameGM[info.Index] += spotGain(info, m.Spots[sp].Metal)
					s.spotFails[sp] = 0 // placed at last: earlier failures are history
				}
			} else if r.Has(aikit.RoleMetalMaker) {
				s.frameGM[info.Index] += gainMaker(info)
			}
			continue
		}
		switch {
		case r.Has(aikit.RoleFactory):
			s.bpFac += int64(info.BuildPower)
		case r.Any(aikit.RoleBuilder | aikit.RoleCommander):
			s.bpBld += int64(info.BuildPower)
			if s.info[info.Index].canEco && !r.Has(aikit.RoleCommander) {
				s.cons++
			}
		}
		s.upkeepE += int64(info.EnergyUse) * 1000
		s.firmE += int64(info.EnergyMake) * 1000
		if info.WindGen > 0 || info.TidalGen > 0 {
			s.windE += s.gainE(info) - int64(info.EnergyMake)*1000
		}
		if r.Has(aikit.RoleExtractor) {
			s.mexUpkeep += int64(info.EnergyUse) * 1000
			s.mexBuilt++
		}
		if r.Has(aikit.RoleCombat) {
			if !r.Has(aikit.RoleScout) {
				s.armyCount++
			}
			if r.Has(aikit.RoleScout) {
				s.scouts++
			}
		} else if r.Has(aikit.RoleScout | aikit.RoleAir) {
			s.airScouts++
		}
		if info.AirDPS > 0 && r.Any(aikit.RoleCombat|aikit.RoleDefense) {
			s.ownAA += int64(info.AirDPS) * int64(info.HP) / 16
		}
	}
	s.builders = int32(len(b.Builders))
	// Live build commitments not yet visible as frames.
	for _, i := range b.Builders {
		u := &o.Own[i]
		c := s.commitIf(u)
		if c == nil || !c.kind.build() || c.prod == nil || u.Order == aikit.OrderIdle || o.Tick-c.tick > 2700 {
			continue
		}
		idx := c.prod.Index
		s.touch(idx)
		s.commitN[idx]++
		s.commitGM[idx] += c.gainM
	}
	s.pendM, s.pendE, s.pendFacBP = 0, 0, 0
	for _, idx := range s.touched {
		info := k.Table.Units[idx]
		n := s.frameN[idx]
		if s.commitN[idx] > n {
			// Builders still walking count toward diminishing returns.
			s.count[idx] += s.commitN[idx] - n
			n = s.commitN[idx]
		}
		s.pendM += max64(s.frameGM[idx], s.commitGM[idx])
		if info.Role.Has(aikit.RoleEnergy) {
			s.pendE += int64(n) * s.gainE(info)
		}
		if info.Role.Has(aikit.RoleFactory) {
			s.pendFacBP += int64(n) * int64(info.BuildPower)
		}
		if (p.Layout != 0 || p.Tech != 0) && info.EnergyUse > 0 && !info.Role.Any(aikit.RoleMobile|aikit.RoleEnergy) {
			// A maker (moho mine, radar) under way will draw its upkeep:
			// planned now, so several are not started on the same surplus.
			s.pendE -= int64(n) * int64(info.EnergyUse) * 1000
		}
	}
	s.consPending = consFrames

	// Resources and spend capacity.
	s.mInc, s.eInc = int64(o.Metal.Income)*1000, int64(o.Energy.Income)*1000
	s.mExp, s.eExp = int64(o.Metal.Expense)*1000, int64(o.Energy.Expense)*1000
	s.mStock, s.eStock = int64(o.Metal.Stock), int64(o.Energy.Stock)
	s.mCap, s.eCap = int64(o.Metal.Cap), int64(o.Energy.Cap)
	if s.eStock*20 < s.eCap && s.eInc < s.eExp {
		s.lastEStall = s.tick // the energy store is nearly empty and falling
	}
	s.eIncExp = s.firmE + s.windE
	s.observeFactories(b)
	s.capM = (s.bpFac*s.rMU + s.bpBld*s.rMB) / 1000
	s.capE = (s.bpFac*s.rEU + s.bpBld*s.rEB) / 1000
	h := int64(p.Horizon)
	s.recompute(h)

	s.observeEnemy(b)
	if p.Naval != 0 || p.Air != 0 || p.Tech != 0 {
		s.observeN(b)
	}
	if p.Layout != 0 || p.Naval != 0 {
		s.observeTraps(b)
	}
	if p.Layout != 0 {
		s.observeZones(b)
	}
	s.observePlaces(b)
}

// recompute derives coverage and needs from supply and demand. Short-run
// coverage counts stock as stock/h of supply; long-run coverage counts
// income only (a starting stock is spent once, income is what grows). The
// need for a resource is the mean of the two reciprocals. The economy layer
// calls this again after each reservation within a think.
func (s *shared) recompute(h int64) {
	p := &s.p
	incM, incE := s.mInc+s.pendM, s.eInc+s.pendE
	expE := s.eIncExp + s.pendE // long run: expected wind, not this gust
	s.supplyM = incM + s.mStock*1000/h
	s.supplyE = incE + s.eStock*1000/h
	// Demand is what build power could spend, but no more metal than the
	// energy supply can accompany (at e_ratio) and no more energy than the
	// metal supply can use: a starved resource makes the other look ample.
	er := int64(p.ERatio)
	wantM := min64(s.capM*int64(p.DemandBP)/1000, max64(s.supplyE-s.upkeepE, 0)*10/er)
	wantE := min64(s.capE*int64(p.DemandBP)/1000, s.supplyM*er/10)
	s.demandM = max64(max64(s.mExp, wantM), 1000)
	s.demandE = max64(max64(s.eExp, wantE+s.upkeepE), 1000)
	s.spendable = min64(s.supplyM, s.supplyE*10/er)
	s.covM = clamp(s.supplyM*1000/s.demandM, 1, 20000)
	s.covE = clamp(s.supplyE*1000/s.demandE, 1, 20000)
	longM := clamp(incM*1000/s.demandM, 1, 20000)
	longE := clamp(expE*1000/s.demandE, 1, 20000)
	s.needM = (inv(s.covM, 100, 5000) + inv(longM, 100, 5000)) / 2
	s.needE = (inv(s.covE, 100, 5000) + inv(longE, 100, 5000)) / 2
	// A full store is being wasted: more of that resource is worth up to
	// three quarters less until it drains.
	if s.mCap > 0 {
		s.needM = mul(s.needM, one-lin(s.mStock*1000/s.mCap, 850, 1000)*3/4)
	}
	if s.eCap > 0 {
		s.needE = mul(s.needE, one-lin(s.eStock*1000/s.eCap, 850, 1000)*3/4)
	}
	s.needBP = clamp(s.covM, 100, 5000)
	// Extraction stops in an energy stall, so steady output should carry
	// the extractors' upkeep (with a quarter margin) whatever the wind does.
	s.needFirm = inv((s.firmE+s.eStock*1000/h)*1000/max64(s.mexUpkeep*5/4, 1000), 100, 5000)
}

func (s *shared) observeEnemy(b *core.Board) {
	o := b.O
	s.enArmy, s.enAir, s.enAA, s.enDef = 0, 0, 0, 0
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		st := int64(info.DPS) * int64(info.HP) / 16
		switch {
		case info.Role.Has(aikit.RoleExtractor):
			// Income from seen extractors is for Explain only (enemyIncome).
		case info.Role.Has(aikit.RoleDefense):
			s.enDef += int64(info.Value)
			if info.AirDPS > 0 {
				s.enAA += int64(info.AirDPS) * int64(info.HP) / 16
			}
		case info.Role.Has(aikit.RoleCombat):
			age := int64(o.Tick - r.LastSeen)
			w := half(age, 1800)
			s.enArmy += int64(info.Value) * w / one
			if info.Role.Has(aikit.RoleAir) {
				s.enAir += st * w / one
			}
			if info.AirDPS > 0 {
				s.enAA += int64(info.AirDPS) * int64(info.HP) / 16 * w / one
			}
			if r.LastSeen > s.lastArmyAt {
				s.lastArmyAt = r.LastSeen
			}
		}
	}
	// Unseen for a while: assume the enemy fields at least a share of our
	// own army, rising with staleness.
	stale := lin(int64(o.Tick-s.lastArmyAt), 0, 3600)
	prior := int64(b.ArmyValue) * stale / 2 / one
	s.enEst = max64(s.enArmy, prior)
	s.aaNeed = s.enAir * one / (s.enAir + s.ownAA + 1)
	ground := b.EnemyGround
	s.defShare = b.EnemyDefense * one / (b.EnemyDefense + ground + 1)
}

func (s *shared) observePlaces(b *core.Board) {
	o := b.O
	m := b.K.Map
	hx, hz := b.HomeX, b.HomeZ
	// Until enemy buildings are seen, the enemy is assumed at the start
	// farthest from ours: the board's nearest-start guess is a poor prior
	// on maps with many start positions.
	// With the start assignment public (MapInfo.StartEnemy) the prior is the
	// opponent's start nearest ours instead.
	s.enemyX, s.enemyZ = b.EnemyX, b.EnemyZ
	if !b.EnemyKnown {
		var far, near int64 = -1, -1
		for i, st := range m.Starts {
			d := aikit.Dist2(st[0], st[1], hx, hz)
			if m.StartEnemy != nil {
				if m.MaybeEnemyStart(i) && (near < 0 || d < near) {
					near, s.enemyX, s.enemyZ = d, st[0], st[1]
				}
				continue
			}
			if d > far {
				far, s.enemyX, s.enemyZ = d, st[0], st[1]
			}
		}
	}
	// Field directions follow the board's guess (empirically better
	// placements); territory uses the farthest-start prior.
	dx, dz := int64(b.EnemyX-hx), int64(b.EnemyZ-hz)
	d := aikit.ISqrt64(dx*dx + dz*dz)
	if d < 1 {
		d = 1
	}
	s.safeX = clampWorld(hx-int32(dx*320/d), m.WorldW)
	s.safeZ = clampWorld(hz-int32(dz*320/d), m.WorldH)
	s.sideX = clampWorld(hx+int32(dz*320/d), m.WorldW)
	s.sideZ = clampWorld(hz-int32(dx*320/d), m.WorldH)
	s.facX = clampWorld(hx+int32(dx*160/d), m.WorldW)
	s.facZ = clampWorld(hz+int32(dz*160/d), m.WorldH)
	if !s.homeLocked && b.Commander >= 0 {
		s.homeLocked = true
		best := int64(-1)
		for i := range m.Spots {
			sp := &m.Spots[i]
			if !sp.Water {
				continue
			}
			dd := aikit.Dist2(sp.X, sp.Z, hx, hz)
			if dd <= 1200*1200 && (best < 0 || dd < best) {
				best, s.waterX, s.waterZ = dd, sp.X, sp.Z
			}
		}
		s.haveWater = best >= 0
	}
	// Danger at own buildings and the building nearest the enemy.
	s.dangerNow = 0
	var bestFront int64 = -1
	s.frontX, s.frontZ = b.RallyX, b.RallyZ
	for i := range o.Own {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleMobile) {
			continue
		}
		t := int64(b.Threat.At(u.X, u.Z))
		if t > s.dangerNow {
			s.dangerNow = t
			if t*10 >= s.danger*8 {
				s.dangerX, s.dangerZ = u.X, u.Z
			}
		}
		if fd := aikit.Dist2(u.X, u.Z, b.EnemyX, b.EnemyZ); bestFront < 0 || fd < bestFront {
			bestFront = fd
			s.frontX, s.frontZ = u.X, u.Z
		}
	}
	// Remembered danger decays by about half a minute's worth per 30 s.
	s.danger = max64(s.dangerNow, s.danger*97/100)
	// Free spots on our side that are safe now: expansion room.
	s.freeSafe = 0
	if s.terr.ready && s.p.Naval != 0 {
		s.freeSafeN(b)
		return
	}
	for i := range m.Spots {
		sp := &m.Spots[i]
		if b.Spots[i] != core.SpotFree || sp.Water || s.spotBlock[i] > s.tick {
			continue
		}
		if s.spotTerr(b, i) < 600 || int64(b.Threat.At(sp.X, sp.Z)) >= int64(s.p.ThreatHalf) {
			continue
		}
		s.freeSafe++
	}
}

// territory is 1000 on our half of the line between the two bases, falling
// to 0 at the enemy base.
func (s *shared) territory(b *core.Board, x, z int32) int64 {
	dh := int64(aikit.Dist(x, z, b.HomeX, b.HomeZ))
	de := int64(aikit.Dist(x, z, s.enemyX, s.enemyZ))
	return territoryOf(dh, de)
}

// territoryOf is territory from a point's distances to home (dh) and to the
// believed enemy base (de).
func territoryOf(dh, de int64) int64 { return clamp(de*2000/(de+dh+1), 0, one) }

// spotDist keeps each metal spot's distance to home and to the believed
// enemy base, the two square roots territory takes per spot: home is fixed
// once set, and the enemy base moves only when the belief does.
type spotDist struct {
	homeX, homeZ int32
	enX, enZ     int32
	gen          uint32   // enemy positions seen; de[i] is current when deGen[i] == gen
	dh, de       []int32  // per spot; dh is -1 until found
	deGen        []uint32 // per spot
}

// forget drops the distances to home, which is now (hx, hz).
func (d *spotDist) forget(hx, hz int32) {
	d.homeX, d.homeZ = hx, hz
	for j := range d.dh {
		d.dh[j] = -1
	}
}

// spotTerr is spotTerritory for metal spot i: territory from the kept
// distances, or with growth's territory in force spotTerritory itself.
// TestSpotTerrIsSpotTerritory holds the two equal.
func (s *shared) spotTerr(b *core.Board, i int) int64 {
	m := b.K.Map
	sp := &m.Spots[i]
	if s.part(gExpand) && s.growSafe() {
		return s.spotTerritory(b, sp.X, sp.Z)
	}
	d := &s.sdist
	if n := len(m.Spots); len(d.dh) != n {
		*d = spotDist{dh: make([]int32, n), de: make([]int32, n), deGen: make([]uint32, n), gen: 1, enX: s.enemyX, enZ: s.enemyZ}
		d.forget(b.HomeX, b.HomeZ)
	} else if d.homeX != b.HomeX || d.homeZ != b.HomeZ {
		d.forget(b.HomeX, b.HomeZ)
	}
	if d.enX != s.enemyX || d.enZ != s.enemyZ {
		d.enX, d.enZ = s.enemyX, s.enemyZ
		d.gen++
	}
	if d.dh[i] < 0 {
		d.dh[i] = aikit.Dist(sp.X, sp.Z, b.HomeX, b.HomeZ)
	}
	if d.deGen[i] != d.gen {
		d.de[i], d.deGen[i] = aikit.Dist(sp.X, sp.Z, s.enemyX, s.enemyZ), d.gen
	}
	t := territoryOf(int64(d.dh[i]), int64(d.de[i]))
	if s.contested(b, sp.X, sp.Z, t) {
		return one
	}
	return t
}

func clampWorld(v, hi int32) int32 {
	if v < 32 {
		return 32
	}
	if v > hi-32 {
		return hi - 32
	}
	return v
}

// refillAPM mirrors the executor's action bucket so a think never emits
// more commands than will be applied.
func (s *shared) refillAPM() {
	per := &s.k.Persona
	if per.APM <= 0 {
		s.budget = 1 << 20
		return
	}
	burst := int64(per.Burst)
	if burst < 1 {
		burst = 1
	}
	if !s.apmInit {
		s.apmInit = true
		s.tokens = burst * 1000
		s.lastFill = s.tick
	}
	if s.tick > s.lastFill {
		s.tokens += int64(s.tick-s.lastFill) * int64(per.APM) * 1000 / 1800
		if s.tokens > burst*1000 {
			s.tokens = burst * 1000
		}
		s.lastFill = s.tick
	}
	s.budget = int32(s.tokens / 1000)
}

func (s *shared) spendAPM(n int) {
	if s.k.Persona.APM <= 0 {
		return
	}
	s.tokens -= int64(n) * 1000
	if s.tokens < 0 {
		s.tokens = 0
	}
}
