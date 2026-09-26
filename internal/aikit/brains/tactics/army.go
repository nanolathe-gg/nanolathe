// Package tactics is the tactical-army prototype for the Modern AI research
// framework: an army policy (core.Policy) that operates combat units as
// squads with roles, reads influence maps built from the fair observation,
// predicts engagements with Lanchester's square law, routes approaches
// through low-threat sectors and, as the persona's skill allows, pulls back
// badly damaged units and focuses fire.
//
// Everything is integer arithmetic over the observation and the policy's
// own state; commands are tracked per unit so an order is re-sent only when
// it changed or evidently did not take effect, and every command is charged
// against a mirror of the persona's action budget.
package tactics

import (
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Squad ids double as unit tags (Kit.SetTag). Slot ids are fixed by role.
const (
	sqNone    int32 = 0
	sqMain    int32 = 1 // the main army
	sqRaid    int32 = 2 // fast units hunting exposed economy
	sqDefend  int32 = 3 // home guard
	sqEscort  int32 = 4 // anti-air escort of the main army
	sqFighter int32 = 5 // air superiority: intercept, escort strikes, patrol
	sqStrike  int32 = 6 // bombers and gunships striking valuable targets
	sqNaval   int32 = 7 // ships contesting the sea and the coast
	sqAmph    int32 = 8 // hovercraft and amphibious units when walkers cannot reach the enemy
	numSq           = 9

	tagScout     int32 = 20 // individually operated scouts
	tagWithdrawn int32 = 21 // damaged units walking home
	tagAirScout  int32 = 22 // unarmed aircraft scouting for the strikes
)

var roleNames = [numSq]string{"", "main", "raid", "defend", "escort", "fighter", "strike", "naval", "amph"}

type squadState uint8

const (
	stGather squadState = iota
	stApproach
	stEngage
	stRetreat
	stDefend
)

var stateNames = [...]string{"gather", "approach", "engage", "retreat", "defend"}

// Target kinds beyond a zone index.
const (
	tgtNone     int32 = -1
	tgtExplore  int32 = -2 // the enemy base guess, nothing known there
	tgtSkirmish int32 = -3 // an enemy force met in the field
)

type squad struct {
	id     int32
	active bool
	state  squadState
	since  uint32

	members   []int32 // O.Own indices, rebuilt every think
	total     force
	present   force // members within presentRadius of the centre
	hist      rangeHist
	cx, cz    int32
	hasCentre bool
	speed     int32 // slowest present member, wu/s
	rng       int32 // longest present range

	target         int32 // zone index or tgt*
	tx, tz         int32 // target point
	sx, sz         int32 // stage point
	enemy          force // predicted opposition at the target / in the fight
	ratio          int64 // own vs enemy, permille
	launch         int64 // present strength when the engagement began
	arrived        uint32
	defX, defZ     int32
	lastIncident   uint32
	focus          pool.Handle
	focusG         uint32 // instance (Contact.Gen) of the focus target
	focusTick      uint32
	idleSince      uint32 // gather state without any viable target
	wps            [maxWaypoints][2]int32
	nwp            int32
	routed         bool
	lastRouteCheck uint32
	// Air strike: target handle, the sortie (launch tick) its members
	// carry, and the loss the launch expected.
	tgtH    pool.Handle
	tgtG    uint32 // instance (Remembered.Gen) of the strike target
	sortie  uint32
	expLoss int64
	// Strike calibration (unseen.go): the model's loss and gain for the
	// chosen target, and at launch; the sortie's value at launch, the
	// current target's gain and what the sortie destroyed.
	rawLoss, rawGain       int64
	launchLoss, launchGain int64
	launchValue            int64
	tgtGain, sortieGain    int64
	// Damage the present members took, hit points per second (smoothed),
	// and the fleet's surface-gun force (what can shell a coast).
	dmgRate int64
	surf    force
	// Movement groups (class and region) of the members, for reach.
	groups  [maxGroups]rgroup
	ngroups int32
	why     uint8 // reason of the last retreat (whyRatio...)
	// Posture (main squad): the squad is on an offensive the posture
	// allowed, and whether its latest target choice was held to clearly
	// undefended targets (posture.go).
	offensive bool
	held      bool
	// soft: the main squad is out on a raid the hold allowed (a soft raid
	// or a probe) and turns back like a raider (harass.go).
	soft bool
}

// Retreat reasons (Explain).
const (
	whyNone uint8 = iota
	whyRatio
	whySpent
	whyBlind
)

var whyNames = [...]string{"", "ratio", "spent", "blind"}

// Order kinds recorded per unit.
const (
	okNone uint8 = iota
	okMove
	okPatrol
	okAttack
)

// unitMem is the policy's per-unit memory, indexed by handle and reset
// when the slot holds a different unit (another definition, or another
// instance: aikit.OwnUnit.Gen).
type unitMem struct {
	info     *aikit.UnitInfo
	gen      uint32
	hp       int32
	hurtTick uint32
	ordKind  uint8
	retries  uint8
	ordX     int32
	ordZ     int32
	ordT     pool.Handle
	ordG     uint32 // instance of ordT
	ordTick  uint32
	withdraw uint32 // tick the unit was sent home damaged (0 = no)
	// Movement watch: last position that differed by a step, and when.
	lastX, lastZ int32
	movedTick    uint32
	airDmg       int32  // hit points lost since the previous think
	sortie       uint32 // strike the aircraft flies with (launch tick)
	seen         uint32 // last think the unit was observed (loss accounting)
	built        bool
	// Reach of the last point tested for this unit (where it stood, what
	// was asked, the answer).
	rReg    uint16
	rX, rZ  int32
	rRadius int32
	rOK     bool
	rTested bool
}

// stuckTicks is how long a unit ordered somewhere far may stand still
// before the squad stops counting on it: trapped behind buildings, it
// cannot arrive, and counting it would overstate the squad.
const stuckTicks = 900

// stuck reports whether an own unit has an order it evidently cannot
// carry out: ordered somewhere far, it has not moved for stuckTicks and is
// not fighting where it stands. A carried order is not re-sent while the
// unit is busy (needs), so the order's age alone cannot tell a unit
// fighting in place from one trapped behind buildings; left out of its
// squad, a fighting unit would get no retreat.
func (a *Army) stuck(b *core.Board, u *aikit.OwnUnit, um *unitMem) bool {
	return um.ordKind != okNone && b.Tick-um.movedTick > stuckTicks && b.Tick-um.ordTick > stuckTicks/2 &&
		aikit.Dist2(u.X, u.Z, um.ordX, um.ordZ) > 400*400 && !a.fighting(b, u, um)
}

// fighting reports whether an own unit is in a fight where it stands: hurt
// in the last five seconds, or an enemy it can shoot within its weapon
// range.
func (a *Army) fighting(b *core.Board, u *aikit.OwnUnit, um *unitMem) bool {
	if um.hurtTick != 0 && b.Tick-um.hurtTick <= 150 {
		return true
	}
	r := int64(u.Info.Range) + 64
	for i := range b.O.Enemy {
		c := &b.O.Enemy[i]
		if c.Info != nil && c.Info.Role.Has(aikit.RoleAir) && u.Info.AirDPS <= 0 {
			continue
		}
		if aikit.Dist2(c.X, c.Z, u.X, u.Z) <= r*r {
			return true
		}
	}
	return false
}

// Params switch components off for ablation (player-spec keys route=0,
// micro=0, raid=0, defend=0, escort=0, budget=0, air=0, naval=0,
// posture=0, passage=0).
type Params struct {
	Route, Micro, Raid, Defend, Escort, Budget bool
	// Air enables the air task forces (fighters, strike wing, air scouts)
	// and keeps anti-air escorting the army while enemy aircraft are about.
	Air bool
	// Naval enables the fleet, the reach tables (every squad goal reachable
	// for its members' movement classes) and the amphibious assault squad.
	Naval bool
	// EngageAdj and RetreatAdj shift the skill-derived engage and retreat
	// margins (permille of square-law strength) for tuning sweeps;
	// NavalAdj shifts the fleet's extra margin (nm=).
	EngageAdj, RetreatAdj, NavalAdj int64
	// Posture makes the army honor the strategy's posture (posture.go): the
	// main squad launches no offensive before the army it can attack with
	// reaches Posture.AttackValue, and Posture.Aggression can shift the
	// engage and retreat margins (AggrSlope). posture=0 restores the army
	// that launches on its own prediction alone (identical games).
	Posture bool
	// PostureMain measures only the main squad's value against the attack
	// value instead of the whole available army's (pv=main).
	PostureMain bool
	// AggrSlope is the margin shift, permille of strength per point of
	// aggression away from 50 (pagg=).
	AggrSlope int64
	// Passage keeps the ground squads' gather and stage points out of
	// terrain passages (passage.go; passage=0 restores the old points).
	Passage bool
	// Unseen counts the remembered enemy land army that is out of sight
	// where it defends: for the fleet's targets and the strike wing's
	// anti-air (unseen.go; unseen=0 restores the old judgments).
	Unseen bool
	// Harass lets the main squad, while the posture holds it, raid the
	// enemy's economy with a small early army and turn back like a raider
	// (harass.go; harass=0 restores the old soft raids). HarassUnits and
	// HarassValue are that raid's least size (hn=, hv=).
	Harass      bool
	HarassUnits int32
	HarassValue int64
	// Probe lets a held main squad of the harass size explore unseen start
	// positions while no target is known (harass.go; probe=0 off).
	Probe bool
	// Tour sends each scout to the unseen start position nearest to itself
	// rather than to home (harass.go; tour=0 off).
	Tour bool
	// SoftMargin overrides the soft-target margin while held, permille
	// (sm=; 0 keeps the raid margin). RaidOn overrides the value at which
	// the raid squad splits off (raidv=; it folds back below 60 % of it).
	SoftMargin int64
	RaidOn     int64
	// Temper, when set, gives the army a per-game character once, at
	// Init: the strategy's drawn personality (posture.go). ParamsFrom
	// leaves it nil, which is no temper.
	Temper TemperSource
}

// DefaultParams enables every component.
func DefaultParams() Params {
	return Params{Route: true, Micro: true, Raid: true, Defend: true, Escort: true, Budget: true, Air: true, Naval: true,
		Posture: true, AggrSlope: defaultAggrSlope, Passage: true, Unseen: true,
		Harass: true, HarassUnits: harassUnits, HarassValue: harassValue, Probe: true, Tour: true}
}

// ParamsFrom reads ablation switches from player-spec parameters.
func ParamsFrom(kv map[string]string) Params {
	p := DefaultParams()
	off := func(k string) bool { v, ok := kv[k]; return ok && (v == "0" || v == "false") }
	p.Route = !off("route")
	p.Micro = !off("micro")
	p.Raid = !off("raid")
	p.Defend = !off("defend")
	p.Escort = !off("escort")
	p.Budget = !off("budget")
	p.Air = !off("air")
	p.Naval = !off("naval")
	p.Posture = !off("posture")
	p.Passage = !off("passage")
	p.Unseen = !off("unseen")
	p.Harass = !off("harass")
	p.Probe = !off("probe")
	p.Tour = !off("tour")
	if v, err := strconv.Atoi(kv["hn"]); err == nil && v > 0 {
		p.HarassUnits = int32(v)
	}
	if v, err := strconv.Atoi(kv["hv"]); err == nil && v >= 0 {
		p.HarassValue = int64(v)
	}
	if v, err := strconv.Atoi(kv["sm"]); err == nil && v > 0 {
		p.SoftMargin = int64(v)
	}
	if v, err := strconv.Atoi(kv["raidv"]); err == nil && v > 0 {
		p.RaidOn = int64(v)
	}
	p.PostureMain = kv["pv"] == "main"
	if v, err := strconv.Atoi(kv["pagg"]); err == nil {
		p.AggrSlope = int64(v)
	}
	if v, err := strconv.Atoi(kv["em"]); err == nil {
		p.EngageAdj = int64(v)
	}
	if v, err := strconv.Atoi(kv["rm"]); err == nil {
		p.RetreatAdj = int64(v)
	}
	if v, err := strconv.Atoi(kv["nm"]); err == nil {
		p.NavalAdj = int64(v)
	}
	return p
}

// Army is the tactical army policy.
type Army struct {
	P Params

	ready bool
	// Influence grids (sector resolution).
	danger *aikit.Grid // enemy ground damage reaching each sector
	static *aikit.Grid // the static-defense part of danger
	aa     *aikit.Grid // enemy anti-air reach
	asset  *aikit.Grid // own buildings and builders (what to defend)
	hurt   *aikit.Grid // recent damage taken by own units, decaying
	known  []uint8     // sectors ground units were seen in (learned passability)

	zoneW, zoneH int32
	zones        []zone
	candidates   []int32
	incidents    []incident
	// This think's armed ground enemies (memindex.go): static defenses and
	// radar blips as lists, remembered mobiles bucketed by zone.
	memStat, blips []int32
	mobHead        []int32 // zone → first index in mobIdx; len zones+1
	mobIdx         []int32 // Memory indices, zone by zone
	indexTick      uint32  // the think the index was built for
	indexed        bool

	enemyMob             force // the enemy's mobile army, freshness-weighted
	enemyMobX, enemyMobZ int32
	enemyMobKnown        bool

	sq    [numSq]squad
	units []unitMem
	rt    router

	gx, gz     int32 // main gather point
	dgx, dgz   int32 // home guard's point (with Passage)
	gatherTick uint32
	gatherSet  bool

	// Passage verdicts per movement class on a 64 wu grid (passage.go):
	// 0 unknown, 1 open, 2 passage.
	passV        [][]uint8
	passW, passH int32

	scoutNext int32
	startSeen []uint32  // per start position: last tick an own unit looked at it
	coms      []sideCom // commander definition by side (commanderOf)

	// Defense sizing.
	threatMemory int64 // decaying peak of enemy value seen threatening assets
	threatK      int64 // threatMemory in thousandths (the fade's precision)
	threatTick   uint32

	repulses [maxRepulses]repulse

	// Merge/split bookkeeping.
	raidBlockUntil uint32

	// Action-budget mirror.
	tokens                            int64
	fill                              uint32
	prevThink                         uint32
	budgetInit                        bool
	avail                             int64 // commands this think may still spend
	prodReserve                       int64
	spent                             int64
	starved                           int64 // commands withheld for lack of budget (cumulative)
	issued                            int64 // commands emitted (cumulative)
	droppedTotal                      int64
	droppedAfterSpend, droppedStarved int64

	stuckN int32 // members standing still far from their orders

	stats struct {
		launched, engaged, skirmish, retreatFight, retreatEarly, cleared, defended, withdrawn, focus int64
		intercepts, strikes, strikeKills, strikeAborts                                               int64
		passage                                                                                      int64 // waiting points moved out of a passage
		waits, waitPassage                                                                           int64 // thinks the main squad waited gathered, in a passage
	}
	// Main-squad offensives and the posture's hold (Report and Explain
	// instrumentation; never read by a decision).
	off offStats
	// First-contact milestones (harass.go; Report only).
	early earlyStats

	// Unit classes by definition index.
	classes []uclass

	// Air picture.
	aaMem       *aikit.Grid // remembered anti-air (decaying) and unexplained hits on aircraft
	aaMemK      []int64     // aaMem in thousandths (fadeGrid)
	aaTick      uint32
	airSeen     uint32 // last tick an armed enemy aircraft was seen
	enemyAirSup force  // enemy aircraft that shoot at aircraft (air-to-air)
	enemyAirN   int32
	enemyAirX   int32
	enemyAirZ   int32
	zoneSeen    []uint32 // per zone: last tick an own unit stood in it
	rallyX      int32    // air rally point (kept for a while)
	rallyZ      int32
	rallyTick   uint32
	rallySet    bool
	scand       []strikeCand
	airOK       []uint8

	// Water and reach knowledge.
	water           []uint8 // sectors known to hold navigable water
	wdist           []uint8 // sectors to the nearest known water (255 = far)
	bfs             []int32
	homeWaterSet    bool
	homeWOK         bool
	homeWX, homeWZ  int32
	ngx, ngz        int32 // naval gather point
	navalGatherTick uint32
	navalGatherSet  bool
	navalTargets    int32 // zones the fleet can engage from known water
	enemyFleet      force // enemy ships and hovercraft, freshness-weighted
	fleetMem        force // the largest enemy fleet seen, fading
	fleetMemK       force // fleetMem in thousandths (the fade's precision)
	fleetX, fleetZ  int32 // where it was seen
	enemyLand       force // enemy land army in sight, freshness-weighted (unseen.go)
	landMem         force // the largest enemy land army seen, fading
	landMemK        force // landMem in thousandths (the fade's precision)
	landX, landZ    int32 // where it was seen
	// Strike calibration (unseen.go): cumulative model loss and gain of
	// finished sorties, and what they really lost and destroyed.
	calExpLoss, calLoss, calExpGain, calGain int64
	seaHurt                                  *aikit.Grid // where our ships were hurt (hp/s), fading
	seaHurtK                                 []int64     // seaHurt in thousandths (fadeGrid)
	seaTick                                  uint32

	// Reach tables (reach.go).
	rcls       []rclass
	secW       int32    // sectors per row (the tables' layout)
	defCls     []int8   // per definition: movement class index, -1 none
	homeReg    []uint16 // per class: region at home
	reachReady bool
	fleetSig   int64
	cut        bool    // this think: walkers cannot reach the enemy base
	strand     []int32 // issue: members the order would strand (scratch)
	// Members whose standing order leads out of their reach, found by
	// membership, and their squads; recalled once the squads are
	// summarized.
	strandM, strandSq []int32
	sbuf              []int32
	rbuf              []int32
	recalling         bool
	curSq             *squad // the squad whose orders are being issued (recall)
	lastSent          int    // members the last issueOnly sent an order to
	stranded          int64  // members re-ordered because their goal was out of reach (cumulative)

	// Own losses by bucket (Report instrumentation).
	lost     [numBuckets]int64
	seenTick uint32
	// seenNow and seenPrev list the own units observed at this think and
	// the previous one (their unitMem.seen), so finding the units that
	// vanished walks the previous think's units rather than every handle.
	seenNow, seenPrev []pool.Handle
	// hurtCells lists the hurt grid's sectors that hold damage (addHurt).
	hurtCells []int32
	dt        uint32 // ticks since the previous think

	// Scratch.
	kernels   [][]int32 // disc falloff weights by radius (addDisc)
	buf       []pool.Handle
	bufIdx    []int32
	tmp       []int32
	tmp2      []int32
	tmp3      []int32
	order     [numSq]int32
	wpBuf     [][2]int32 // route compression (routeInto)
	goals     []goalRec
	goalsTick uint32
}

// goalRec is one target evaluation, kept for Explain.
type goalRec struct {
	squad  int32
	zone   int32
	x, z   int32
	value  int64
	ratio  int64
	score  int64
	chosen bool
}

const maxGoals = 16

// New returns an army policy.
func New(p Params) *Army { return &Army{P: p} }

// Init implements core.Policy.
func (a *Army) Init(b *core.Board) {
	a.applyTemper()
	if a.P.Naval && b.K != nil {
		a.setupReach(b.K)
		if a.reachReady {
			m := b.K.Map
			a.homeReg = make([]uint16, len(a.rcls))
			for c := range a.rcls {
				id, _ := a.rcls[c].r.Nearest(m.HomeX, m.HomeZ, 600)
				a.homeReg[c] = uint16(id)
			}
		}
	}
}

func (a *Army) setup(b *core.Board) {
	m := b.K.Map
	a.danger = aikit.NewGrid(m)
	a.static = aikit.NewGrid(m)
	a.aa = aikit.NewGrid(m)
	a.asset = aikit.NewGrid(m)
	a.hurt = aikit.NewGrid(m)
	a.known = make([]uint8, m.SectorW*m.SectorH)
	a.zoneW = (m.SectorW + zoneSectors - 1) / zoneSectors
	a.zoneH = (m.SectorH + zoneSectors - 1) / zoneSectors
	a.zones = make([]zone, a.zoneW*a.zoneH)
	a.candidates = make([]int32, 0, len(a.zones))
	a.incidents = make([]incident, 0, maxIncidents)
	a.rt.init(m.SectorW, m.SectorH)
	a.goals = make([]goalRec, 0, maxGoals)
	a.classes = classify(b.K.Table, m.SeaLevel)
	a.aaMem = aikit.NewGrid(m)
	a.aaMemK = make([]int64, len(a.aaMem.V))
	a.zoneSeen = make([]uint32, len(a.zones))
	a.scand = make([]strikeCand, 0, maxStrikeCands)
	a.airOK = make([]uint8, m.SectorW*m.SectorH)
	a.water = make([]uint8, m.SectorW*m.SectorH)
	a.wdist = make([]uint8, m.SectorW*m.SectorH)
	a.bfs = make([]int32, 0, m.SectorW*m.SectorH)
	a.seaHurt = aikit.NewGrid(m)
	a.seaHurtK = make([]int64, len(a.seaHurt.V))
	for i := range m.Spots {
		if m.Spots[i].Water {
			a.markWater(m, m.Spots[i].X, m.Spots[i].Z)
		}
	}
	a.updateWaterDist(m)
	for i := range a.sq {
		a.sq[i].id = int32(i)
		a.sq[i].target = tgtNone
		a.sq[i].members = make([]int32, 0, 64)
	}
	a.startSeen = make([]uint32, len(m.Starts))
	// Start positions and land metal spots are walkable ground.
	for _, s := range m.Starts {
		a.known[m.Sector(s[0], s[1])] = 1
	}
	for i := range m.Spots {
		if !m.Spots[i].Water {
			a.known[m.Sector(m.Spots[i].X, m.Spots[i].Z)] = 1
		}
	}
	a.ready = true
}

// unit returns the policy memory of an own unit. A slot that holds a
// different unit than at the last look starts over: orders, sortie,
// withdrawal and hit points belonged to the unit that died there (whose
// loss is counted when it was alive at the previous think).
func (a *Army) unit(u *aikit.OwnUnit) *unitMem {
	h := int(u.H)
	for len(a.units) <= h {
		a.units = append(a.units, unitMem{})
	}
	um := &a.units[h]
	if um.info != u.Info || um.gen != u.Gen {
		if um.info != nil && um.built && a.seenTick != 0 && um.seen == a.seenTick {
			a.lost[a.bucket(um.info)] += int64(um.info.Value)
		}
		*um = unitMem{info: u.Info, gen: u.Gen}
	}
	return um
}

// Skill-derived thresholds (permille of square-law strength).
// A skilled persona commits at a smaller advantage and holds its nerve to
// a smaller disadvantage before pulling out (hard, skill 85: engage at
// 1.26, retreat below 0.79; easy, skill 20: 1.52 and 0.53).
func (a *Army) engageMargin(b *core.Board) int64 {
	return 1600 - 4*int64(b.K.Persona.Skill) + a.P.EngageAdj - a.aggrShift(b)
}

// squadMargin is the engage margin for one squad (the fleet demands more).
func (a *Army) squadMargin(b *core.Board, s *squad) int64 {
	if s.id == sqNaval {
		return a.engageMargin(b) + navalMargin + a.P.NavalAdj
	}
	return a.engageMargin(b)
}

func (a *Army) retreatMargin(b *core.Board) int64 {
	return 450 + 4*int64(b.K.Persona.Skill) + a.P.RetreatAdj - a.aggrShift(b)/2
}

const raidMargin = 3000

// Plan implements core.Policy.
func (a *Army) Plan(b *core.Board) {
	if !a.ready {
		a.setup(b)
	}
	a.refreshBudget(b)
	if a.seenTick != 0 {
		a.dt = b.Tick - a.seenTick
	}
	a.buildPicture(b)
	if a.P.Air {
		a.airPicture(b)
	}
	if a.P.Naval {
		if a.reachReady {
			a.fleetWater(b)
		}
		a.cut = a.landCutNow(b)
		a.fleetPicture(b)
		a.seaHurtPicture(b)
	}
	if a.P.Unseen && (a.P.Naval || a.P.Air) {
		a.landPicture(b)
	}
	a.activate(b)
	a.membership(b)
	a.checkpoint(b)
	a.updateGather(b)
	a.assignDefense(b)
	// Urgent squads first: they get the action budget before the others.
	n := 0
	for pass := 0; pass < 2; pass++ {
		for id := int32(1); id < numSq; id++ {
			s := &a.sq[id]
			if !s.active {
				continue
			}
			urgent := s.state == stDefend || s.state == stRetreat || s.state == stEngage
			if urgent == (pass == 0) {
				a.order[n] = id
				n++
			}
		}
	}
	for _, id := range a.order[:n] {
		a.runSquad(b, &a.sq[id])
	}
	a.mergeSplit(b)
	a.noteWait(b)
	a.noteEarly(b)
	a.scouts(b)
	if a.P.Air {
		a.airScouts(b)
	}
}

// activate turns squad slots on by attention and by army size: one squad
// does everything at attention 1; raiders may split off at 2 and a home
// guard at 3 (while threats to the base are remembered), an anti-air escort
// at 4 once enemy aircraft have been seen. A small army is never split —
// the thresholds have hysteresis so squads do not flicker.
func (a *Army) activate(b *core.Board) {
	att := b.K.Persona.Attention
	army := int64(b.ArmyValue)
	a.fadeThreat(b.Tick)
	for i := range a.incidents {
		if v := a.incidents[i].enemy.value * 13 / 10 * 1000; v > a.threatK {
			a.threatK = v
		}
	}
	a.threatMemory = a.threatK / 1000
	on := func(id int32, allowed bool, onAt, offAt int64) {
		s := &a.sq[id]
		was := s.active
		s.active = allowed && (army >= onAt || (was && army >= offAt))
		if was && !s.active {
			s.target = tgtNone
			s.state = stGather
			s.since = b.Tick
		}
	}
	a.sq[sqMain].active = true
	rOn, rOff := int64(raidOnValue), int64(raidOffValue)
	if a.P.RaidOn > 0 {
		rOn, rOff = a.P.RaidOn, a.P.RaidOn*raidOffValue/raidOnValue
	}
	on(sqRaid, a.P.Raid && att >= 2, rOn, rOff)
	on(sqDefend, a.P.Defend && att >= 3 && a.threatMemory > 0, defendOnValue, defendOffValue)
	// With the air forces on, anti-air keeps escorting for a while after
	// enemy aircraft were last seen (the host forgets mobiles after a
	// minute; bombers come back).
	airAbout := b.EnemyAir > 0 || (a.P.Air && a.airSeen != 0 && b.Tick-a.airSeen < airSeenTicks)
	on(sqEscort, a.P.Escort && att >= 4 && airAbout, 0, 0)
	// Air and naval forces operate in their own domains at any attention:
	// they have nothing else to do.
	on(sqFighter, a.P.Air, 0, 0)
	on(sqStrike, a.P.Air, 0, 0)
	on(sqNaval, a.P.Naval, 0, 0)
	on(sqAmph, a.landCut(b), 0, 0)
}

// threatTicks is the time constant of the threat memory's fade.
const threatTicks = 1800

// fadeThreat ages the threat memory (the largest recent incident) to tick:
// in thousandths, so it fades to nothing rather than stopping where the
// per-think decrement truncates to zero (below 1800/dt: 119 at a 15-tick
// think), which kept the home guard on duty for the rest of the game.
func (a *Army) fadeThreat(tick uint32) {
	if a.threatTick != 0 && tick > a.threatTick {
		a.threatK -= a.threatK * int64(tick-a.threatTick) / threatTicks
		if a.threatK < 0 {
			a.threatK = 0
		}
	}
	a.threatTick = tick
	a.threatMemory = a.threatK / 1000
}

// Army value at which optional squads split off (and fold back in).
const (
	raidOnValue    = 1500
	raidOffValue   = 900
	defendOnValue  = 800
	defendOffValue = 500
)

func isAAUnit(info *aikit.UnitInfo) bool {
	return info.AirDPS > 0 && info.AirDPS >= info.DPS*2
}

func isFast(info *aikit.UnitInfo) bool {
	return info.Speed >= 50 && !info.Role.Any(aikit.RoleArtillery|aikit.RoleAir)
}

// Squad sizing.
const (
	raidCap       = 6  // units
	raidShare     = 20 // percent of the army value at most
	defendShare   = 15 // percent of the army value at most
	presentRadius = 650
)

// membership rebuilds every squad's member list from unit tags, assigning
// new units (tag 0 or an inactive slot) to a role.
func (a *Army) membership(b *core.Board) {
	o := b.O
	k := b.K
	for id := range a.sq {
		s := &a.sq[id]
		s.members = s.members[:0]
		s.total = force{}
		s.surf = force{}
		s.ngroups = 0
	}
	a.strandM, a.strandSq = a.strandM[:0], a.strandSq[:0]
	a.tmp = a.tmp[:0]   // withdrawn units walking home
	a.tmp3 = a.tmp3[:0] // withdrawn aircraft and ships going to their own rally
	a.stuckN = 0
	defendNeed := a.defendNeed(b)
	for _, i := range b.Combat {
		u := &o.Own[i]
		info := u.Info
		tag := u.Tag
		if info.Role.Has(aikit.RoleScout) && (tag == 0 || tag == tagScout) {
			if tag == 0 {
				k.SetTag(u.H, tagScout)
			}
			continue
		}
		if tag == tagScout {
			continue
		}
		if tag == tagWithdrawn {
			um := a.unit(u)
			wx, wz, own := a.withdrawPoint(b, info)
			if aikit.Dist2(u.X, u.Z, wx, wz) > 600*600 && b.Tick-um.withdraw < 1800 {
				if own {
					a.tmp3 = append(a.tmp3, i)
				} else {
					a.tmp = append(a.tmp, i)
				}
				continue
			}
			// Home: rejoin, preferring the home guard.
			um.withdraw = 0
			tag = sqMain
			if a.sq[sqDefend].active {
				tag = sqDefend
			}
			if own {
				tag = a.assign(b, info, defendNeed)
			}
			k.SetTag(u.H, tag)
		}
		if tag <= sqNone || tag >= numSq || !a.sq[tag].active {
			tag = a.assign(b, info, defendNeed)
			k.SetTag(u.H, tag)
		} else if a.sq[sqAmph].active && tag != sqAmph && tag != sqEscort && a.cls(info).anyTerrain() {
			// The land route is cut: hovercraft and amphibious units leave
			// the walkers for the assault squad.
			tag = sqAmph
			k.SetTag(u.H, tag)
		}
		um := a.unit(u)
		// A member whose order leads where its movement cannot go (given
		// before its squad knew, or by a squad of other classes) is called
		// back instead of pressing against a shore.
		if a.reachReady && a.P.Naval && um.ordKind != okNone && !a.unitReaches(b, u, um, um.ordX, um.ordZ, um.ordKind) {
			a.strandM = append(a.strandM, i)
			a.strandSq = append(a.strandSq, tag)
			continue
		}
		// A stuck unit keeps its tag and its order but is left out of the
		// squad until it moves again.
		if a.stuck(b, u, um) {
			a.stuckN++
			continue
		}
		s := &a.sq[tag]
		s.members = append(s.members, i)
		s.total.add(info, int64(u.HP), 1000)
		if a.reachReady {
			c, reg := a.unitRegion(u)
			s.addGroup(c, reg, int64(info.Value), info.Range)
		}
		if tag == sqNaval {
			s.surf.addDPS(info, int64(u.HP), int64(a.cls(info).surfDPS))
		}
	}
	for id := int32(1); id < numSq; id++ {
		a.summarize(b, &a.sq[id])
	}
	// Withdrawn units keep walking home.
	if len(a.tmp) > 0 {
		a.issue(b, a.tmp, okMove, b.HomeX, b.HomeZ, 0, true)
	}
	// Stranded members are called back by their own squad: with its body,
	// to its gather point, or home (recall). The list has its own buffer:
	// issue reuses a.strand for the members its order would strand.
	for id := int32(1); id < numSq && len(a.strandM) > 0; id++ {
		a.sbuf = a.sbuf[:0]
		for k, i := range a.strandM {
			if a.strandSq[k] == id {
				a.sbuf = append(a.sbuf, i)
			}
		}
		if len(a.sbuf) > 0 {
			a.curSq = &a.sq[id]
			a.recall(b, a.sbuf)
			a.curSq = nil
		}
	}
	for _, i := range a.tmp3 {
		u := &o.Own[i]
		wx, wz, _ := a.withdrawPoint(b, u.Info)
		one := a.tmp[:0]
		one = append(one, i)
		a.issue(b, one, okMove, wx, wz, 0, true)
	}
}

// withdrawPoint is where a damaged unit goes: aircraft to the air rally
// and ships to the fleet's gather point when those forces are on (own is
// then true), everything else home.
func (a *Army) withdrawPoint(b *core.Board, info *aikit.UnitInfo) (int32, int32, bool) {
	c := a.cls(info)
	switch {
	case a.P.Air && c.air():
		x, z := a.airRally(b)
		return x, z, true
	case a.P.Naval && c.kind == ukNaval:
		x, z := a.navalGather(b)
		return x, z, true
	}
	return b.HomeX, b.HomeZ, false
}

// defendNeed sizes the home guard: the recent threat, bounded by a share
// of the army.
func (a *Army) defendNeed(b *core.Board) int64 {
	need := a.threatMemory
	if limit := int64(b.ArmyValue) * defendShare / 100; need > limit {
		need = limit
	}
	return need
}

func (a *Army) assign(b *core.Board, info *aikit.UnitInfo, defendNeed int64) int32 {
	c := a.cls(info)
	switch {
	case a.P.Air && c.kind == ukFighter:
		return sqFighter
	case a.P.Air && c.air():
		return sqStrike
	case a.P.Naval && c.kind == ukNaval:
		return sqNaval
	case a.sq[sqAmph].active && c.anyTerrain():
		return sqAmph
	}
	if a.sq[sqEscort].active && isAAUnit(info) {
		return sqEscort
	}
	if a.sq[sqDefend].active && a.sq[sqDefend].total.value < defendNeed {
		return sqDefend
	}
	r := &a.sq[sqRaid]
	if r.active && isFast(info) && b.Tick >= a.raidBlockUntil && r.total.n < raidCap && r.total.value*100 < int64(b.ArmyValue)*raidShare {
		return sqRaid
	}
	return sqMain
}

// summarize computes a squad's centre and present force. The centre is
// the densest point of the squad — the member with the most value within
// clusterRadius, re-centred on its neighbours — so a straggler tail or a
// split squad does not put the centre in empty ground.
func (a *Army) summarize(b *core.Board, s *squad) {
	o := b.O
	s.present = force{}
	s.hist.clear()
	s.speed, s.rng = 0, 0
	if len(s.members) == 0 {
		s.hasCentre = false
		s.dmgRate = 0
		return
	}
	// The body keeps its identity: while a good share of the squad is still
	// near the previous centre, the densest point is searched among those
	// members only, so reinforcements gathering at home do not move it.
	body := s.members
	if s.hasCentre {
		a.tmp2 = a.tmp2[:0]
		var near, all int64
		for _, i := range s.members {
			u := &o.Own[i]
			all += int64(u.Info.Value)
			if aikit.Dist2(u.X, u.Z, s.cx, s.cz) <= bodyRadius*bodyRadius {
				a.tmp2 = append(a.tmp2, i)
				near += int64(u.Info.Value)
			}
		}
		// (Members may all be worth nothing with modded content: then an
		// empty body keeps the whole squad rather than none of it.)
		if len(a.tmp2) > 0 && near*100 >= all*30 {
			body = a.tmp2
		}
	}
	stride := len(body)/densitySamples + 1
	best := body[0]
	var bestV int64 = -1
	for k := 0; k < len(body); k += stride {
		c := &o.Own[body[k]]
		var v int64
		for _, j := range body {
			u := &o.Own[j]
			if aikit.Dist2(u.X, u.Z, c.X, c.Z) <= clusterRadius*clusterRadius {
				v += int64(u.Info.Value)
			}
		}
		if v > bestV {
			best, bestV = body[k], v
		}
	}
	bx, bz := o.Own[best].X, o.Own[best].Z
	var x, z, n int64
	for _, i := range s.members {
		u := &o.Own[i]
		if aikit.Dist2(u.X, u.Z, bx, bz) <= clusterRadius*clusterRadius {
			x += int64(u.X)
			z += int64(u.Z)
			n++
		}
	}
	mx, mz := int32(x/n), int32(z/n)
	s.cx, s.cz = mx, mz
	s.hasCentre = true
	var dmg int64
	for _, i := range s.members {
		u := &o.Own[i]
		dmg += int64(a.units[u.H].airDmg)
		if aikit.Dist2(u.X, u.Z, mx, mz) > presentRadius*presentRadius {
			continue
		}
		s.present.add(u.Info, int64(u.HP), 1000)
		s.hist.add(int64(u.Info.Range), int64(u.Info.DPS))
		if s.speed == 0 || u.Info.Speed < s.speed {
			s.speed = u.Info.Speed
		}
		if u.Info.Range > s.rng {
			s.rng = u.Info.Range
		}
	}
	s.hist.finish()
	if dt := int64(a.dt); dt > 0 {
		s.dmgRate = (s.dmgRate*2 + dmg*30/dt) / 3
	}
}

// Centre search: at most this many candidate members, neighbours within
// clusterRadius.
const (
	densitySamples = 48
	clusterRadius  = 500
	bodyRadius     = 1200
)

func (a *Army) setState(b *core.Board, s *squad, st squadState) {
	if s.state != st {
		switch st {
		case stEngage:
			a.stats.engaged++
			if s.target == tgtSkirmish {
				a.stats.skirmish++
			}
		case stRetreat:
			if s.state == stEngage {
				a.stats.retreatFight++
			} else {
				a.stats.retreatEarly++
			}
		case stDefend:
			a.stats.defended++
		case stApproach:
			a.stats.launched++
		}
		if st == stGather || st == stRetreat || st == stDefend {
			s.offensive = false // an offensive ends with a regroup, a retreat or a defense
			s.soft = false
		}
		a.noteState(b, s, st)
		s.state = st
		s.since = b.Tick
		s.arrived = 0
	}
}

// updateGather places the main gather point on the line from home toward
// the enemy — their army when it has been seen, else their base — as far
// forward as the danger map allows (at most 45% of the way) and on known
// ground, so the waiting army stands between the enemy and the base.
func (a *Army) updateGather(b *core.Board) {
	if a.gatherSet && b.Tick < a.gatherTick+300 {
		return
	}
	a.gatherTick = b.Tick
	m := b.K.Map
	hx, hz := b.HomeX, b.HomeZ
	ex, ez := b.EnemyX, b.EnemyZ
	if a.enemyMobKnown && a.enemyMob.value >= 400 {
		ex, ez = a.enemyMobX, a.enemyMobZ
	}
	d := int64(aikit.Dist(hx, hz, ex, ez))
	bx, bz := hx+(ex-hx)/5, hz+(ez-hz)/5
	// With reach tables the gather point must be ground the walkers at home
	// can stand on (a point on the line may be sea).
	gc, greg := int8(-1), uint16(0)
	if a.reachReady && a.P.Naval {
		if g := a.sq[sqMain].mainGroup(); g != nil {
			gc, greg = g.cls, g.reg
		} else {
			gc, greg = a.walkerHome(b)
		}
		if gc >= 0 && (!a.hasRegion(gc, greg, m.Sector(bx, bz)) || a.nearFactory(b, bx, bz)) {
			bx, bz = a.homeGround(b, gc, greg, ex, ez)
		}
	}
	main := &a.sq[sqMain]
	dx0, dz0 := bx, bz
	var cand [gatherCands][2]int32
	nc := 0
	for f := int64(200); f <= 450; f += 25 {
		x := hx + int32(int64(ex-hx)*f/1000)
		z := hz + int32(int64(ez-hz)*f/1000)
		if a.danger.At(x, z) > gatherDanger {
			break
		}
		if d*f/1000 < 400 {
			continue
		}
		if gc >= 0 {
			if !a.hasRegion(gc, greg, m.Sector(x, z)) || a.nearFactory(b, x, z) {
				continue
			}
		} else if a.known[m.Sector(x, z)] == 0 {
			continue
		}
		cand[nc] = [2]int32{x, z}
		nc++
		bx, bz = x, z
	}
	extent := blobFull
	if a.blobInPassage(b, main, bx, bz) {
		bx, bz, extent = a.gatherOutOfPassage(b, main, bx, bz, dx0, dz0, cand[:nc], ex, ez)
	}
	if !a.gatherSet || aikit.Dist2(bx, bz, a.gx, a.gz) > 300*300 || a.blobIn(b, main, a.gx, a.gz, extent) {
		a.gx, a.gz = bx, bz
	}
	a.gatherSet = true
	// The home guard waits a third of the way to the gather point.
	a.dgx, a.dgz = b.HomeX+(a.gx-b.HomeX)/3, b.HomeZ+(a.gz-b.HomeZ)/3
	if d := &a.sq[sqDefend]; a.blobInPassage(b, d, a.dgx, a.dgz) {
		a.dgx, a.dgz = a.outOfPassage(b, d, a.dgx, a.dgz, a.gx, a.gz, 0)
	}
}

// mainResponseValue is the smallest incident the main army turns around
// for; the commander and base defenses handle a lone raider.
const mainResponseValue = 200

// gatherDanger is the enemy damage (per second) a gather point tolerates.
const gatherDanger = 60

// assignDefense sends squads to incidents, most valuable first, until the
// predicted ratio at each is comfortable.
func (a *Army) assignDefense(b *core.Board) {
	for i := range a.incidents {
		in := &a.incidents[i]
		nearHome := aikit.Dist2(in.x, in.z, b.HomeX, b.HomeZ) < core.BaseRadius*core.BaseRadius
		have := a.supportAt(b, in.x, in.z)
		for _, id := range [...]int32{sqDefend, sqNaval, sqRaid, sqAmph, sqMain, sqEscort} {
			s := &a.sq[id]
			if !s.active || len(s.members) == 0 {
				continue
			}
			if s.state == stDefend && s.lastIncident == b.Tick {
				continue // already answering another incident this think
			}
			if have.dps > 0 && ratio(&have, &in.enemy) >= 1500 {
				break
			}
			d := aikit.Dist(s.cx, s.cz, in.x, in.z)
			r := ratio(&s.total, &in.enemy)
			ok := false
			walkers := id == sqDefend || id == sqRaid || id == sqMain
			if walkers && a.P.Naval && ((in.naval && !nearHome) || !a.reachable(b, s, in.x, in.z)) {
				continue // ships at sea, or ground walkers cannot reach
			}
			switch id {
			case sqNaval:
				wet := int32(a.wdist[b.K.Map.Sector(in.x, in.z)])*aikit.SectorWorld <= navalReach
				ok = wet && (nearHome || in.com || in.assets >= 300 || d < 2500)
			case sqAmph:
				ok = nearHome || in.com || d < 2500
			case sqDefend:
				// The home guard answers at home, not across the map.
				ok = nearHome || in.com || d < 1500
			case sqRaid:
				ok = d < 2500 && r >= 1500
			case sqMain:
				important := (in.com || nearHome || in.assets >= 600) && in.enemy.value >= mainResponseValue
				winningAway := s.state == stEngage && s.ratio >= 1500 && !in.com && d > 2500
				ok = important && !winningAway
			case sqEscort:
				ok = nearHome || in.com
			}
			if !ok {
				continue
			}
			if s.state != stDefend {
				a.setState(b, s, stDefend)
			}
			s.defX, s.defZ = in.x, in.z
			s.lastIncident = b.Tick
			s.enemy = in.enemy
			have.merge(&s.total)
			in.taken = int8(id)
		}
	}
	for id := int32(1); id < numSq; id++ {
		s := &a.sq[id]
		if id == sqFighter || id == sqStrike {
			continue // they keep their own states
		}
		if s.state == stDefend && b.Tick-s.lastIncident > 90 {
			s.target = tgtNone
			a.setState(b, s, stGather)
		}
	}
}

// ---------------------------------------------------------------------------
// Action budget.

// refreshBudget mirrors the host's action bucket: it refills at the
// persona's rate up to the burst, and every command that reached the
// executor (applied, stale or failed — only dropped ones are free) spent
// one action. The batch this think emits is applied after the reaction
// latency, so the budget is projected to then, less what the earlier
// policies already emitted this think.
func (a *Army) refreshBudget(b *core.Board) {
	k := b.K
	p := &k.Persona
	a.droppedTotal += int64(k.Last.DroppedAPM)
	if a.spent > 0 {
		a.droppedAfterSpend += int64(k.Last.DroppedAPM)
	}
	if a.avail-a.spent < 0 {
		a.droppedStarved += int64(k.Last.DroppedAPM)
	}
	a.spent = 0
	if p.APM <= 0 || !a.P.Budget {
		a.avail = 1 << 30
		a.prodReserve = 0
		return
	}
	burst := int64(p.Burst)
	if burst < 1 {
		burst = 1
	}
	burst *= 1000
	if a.budgetInit {
		a.refillTo(a.prevThink+p.Reaction, burst, int64(p.APM))
		used := int64(k.Last.Applied + k.Last.Stale + k.Last.Failed)
		a.tokens -= used * 1000
		if a.tokens < 0 {
			a.tokens = 0
		}
	}
	a.budgetInit = true
	a.prevThink = b.Tick
	t, f := a.tokens, a.fill
	if f == 0 && t == 0 {
		t = burst
	}
	due := b.Tick + p.Reaction
	if due > f {
		t += int64(due-f) * int64(p.APM) * 1000 / 1800
		if t > burst {
			t = burst
		}
	}
	a.avail = t/1000 - int64(k.Emitted())
	// Production runs after the army: leave one action per factory that
	// will ask for one.
	a.prodReserve = 0
	for _, fi := range b.Factories {
		if b.O.Own[fi].QueueLen < 2 {
			a.prodReserve++
		}
	}
}

func (a *Army) refillTo(tick uint32, burst, apm int64) {
	if a.fill == 0 && a.tokens == 0 {
		a.tokens = burst
	}
	if tick > a.fill {
		a.tokens += int64(tick-a.fill) * apm * 1000 / 1800
		if a.tokens > burst {
			a.tokens = burst
		}
		a.fill = tick
	}
}

// spend takes one action from the budget; urgent orders may use the
// production reserve.
func (a *Army) spend(urgent bool) bool {
	left := a.avail - a.spent
	if !urgent {
		left -= a.prodReserve
	}
	if left <= 0 {
		a.starved++
		return false
	}
	a.spent++
	a.issued++
	return true
}

// ---------------------------------------------------------------------------
// Orders.

// needs reports whether member i must be (re)sent an order: it carries a
// different one, or the same one evidently did not take effect (idle and
// not at the destination) after the reaction latency. Retries are bounded
// so an unreachable destination is not re-sent every think.
func (a *Army) needs(b *core.Board, i int32, kind uint8, x, z int32, t pool.Handle, tg uint32) bool {
	u := &b.O.Own[i]
	um := a.unit(u)
	same := um.ordKind == kind && um.ordT == t && um.ordG == tg && aikit.Dist2(um.ordX, um.ordZ, x, z) <= 160*160
	if !same && kind == okPatrol && um.ordKind == okAttack && aikit.Dist2(um.ordX, um.ordZ, x, z) <= 160*160 {
		// A focus-fire attack carries this patrol queued behind it.
		same = true
	}
	if !same {
		return true
	}
	if b.Tick < um.ordTick+b.K.Persona.Reaction+4 || u.Order != aikit.OrderIdle {
		return false
	}
	if kind == okMove && aikit.Dist2(u.X, u.Z, x, z) <= 250*250 {
		return false // arrived
	}
	if um.retries >= 2 && b.Tick < um.ordTick+600 {
		return false
	}
	return true
}

// collect fills a.buf/a.bufIdx with the members that need the order; t
// and tg name the target unit and its instance (0 for none).
func (a *Army) collect(b *core.Board, members []int32, kind uint8, x, z int32, t pool.Handle, tg uint32) {
	a.buf = a.buf[:0]
	a.bufIdx = a.bufIdx[:0]
	for _, i := range members {
		if a.needs(b, i, kind, x, z, t, tg) {
			a.buf = append(a.buf, b.O.Own[i].H)
			a.bufIdx = append(a.bufIdx, i)
		}
	}
}

// record notes the order on every unit in a.bufIdx.
func (a *Army) record(b *core.Board, kind uint8, x, z int32, t pool.Handle, tg uint32) {
	for _, i := range a.bufIdx {
		um := a.unit(&b.O.Own[i])
		if um.ordKind == kind && um.ordT == t && um.ordG == tg && aikit.Dist2(um.ordX, um.ordZ, x, z) <= 160*160 {
			if um.retries < 255 {
				um.retries++
			}
		} else {
			um.retries = 0
		}
		um.ordKind, um.ordX, um.ordZ, um.ordT, um.ordG, um.ordTick = kind, x, z, t, tg, b.Tick
	}
}

// issue sends one group order to the members that need it. It returns
// false when the budget withheld it.
func (a *Army) issue(b *core.Board, members []int32, kind uint8, x, z int32, t pool.Handle, urgent bool) bool {
	if a.reachReady && a.P.Naval && kind != okAttack && !a.recalling {
		// Members that cannot get there are called back instead.
		a.rbuf = a.rbuf[:0]
		a.strand = a.strand[:0]
		for _, i := range members {
			u := &b.O.Own[i]
			if a.unitReaches(b, u, a.unit(u), x, z, kind) {
				a.rbuf = append(a.rbuf, i)
			} else {
				a.strand = append(a.strand, i)
			}
		}
		if len(a.strand) > 0 {
			ok := a.issueOnly(b, a.rbuf, kind, x, z, t, urgent)
			a.recall(b, a.strand)
			return ok
		}
	}
	return a.issueOnly(b, members, kind, x, z, t, urgent)
}

// issueOnly sends one group order to the members that need it, without
// the reach filter.
func (a *Army) issueOnly(b *core.Board, members []int32, kind uint8, x, z int32, t pool.Handle, urgent bool) bool {
	a.collect(b, members, kind, x, z, t, 0)
	a.lastSent = 0
	if len(a.buf) == 0 {
		return true
	}
	if !a.spend(urgent) {
		return false
	}
	a.lastSent = len(a.buf)
	k := b.K
	switch kind {
	case okMove:
		k.Move(a.buf, x, z, false)
	case okPatrol:
		k.Patrol(a.buf, x, z, false)
	case okAttack:
		k.Attack(a.buf, t, false)
	}
	a.record(b, kind, x, z, t, 0)
	return true
}

// issueRoute moves the members to (x, z) through the squad's waypoints as
// queued moves, falling back to a direct move when the budget cannot pay
// for the whole route.
func (a *Army) issueRoute(b *core.Board, s *squad, x, z int32, urgent bool) {
	a.collect(b, s.members, okMove, x, z, 0, 0)
	if len(a.buf) == 0 {
		return
	}
	n := int64(s.nwp) + 1
	left := a.avail - a.spent
	if !urgent {
		left -= a.prodReserve
	}
	k := b.K
	if s.nwp > 0 && left >= n {
		for i := int32(0); i < s.nwp; i++ {
			a.spend(urgent)
			k.Move(a.buf, s.wps[i][0], s.wps[i][1], i > 0)
		}
		a.spend(urgent)
		k.Move(a.buf, x, z, true)
	} else {
		if !a.spend(urgent) {
			return
		}
		k.Move(a.buf, x, z, false)
	}
	a.record(b, okMove, x, z, 0, 0)
}
