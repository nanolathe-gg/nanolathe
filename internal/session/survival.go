package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Survival is a scenario, not a rule (docs/DESIGN_SURVIVAL.md §3): a skirmish
// session with a commanderless attacker slot and a wave director that creates
// units through the ordinary allocator and gives them ordinary orders. It is
// available under every gameplay mode, adds no RuleSet seam, and exists only
// when the battle-entry request selects it, so no other session changes.

// SurvivalOptions selects a Survival battle and its setup-screen switches
// (DESIGN_SURVIVAL §9). The zero value is an ordinary skirmish.
type SurvivalOptions struct {
	Enabled bool
	Pace    survival.Pace
	NoAir   bool
	NoNaval bool
}

// SurvivalMaxBuddies is the number of allied computer players Survival offers.
const SurvivalMaxBuddies = 2

// survivalAttackerColor is the attacker's logo colour ordinal. The human takes
// 0 and buddies 2 and 3, so the attacker is always the one red side.
const survivalAttackerColor = 1

// SurvivalSkirmishConfig builds the default Survival slot layout
// (DESIGN_SURVIVAL §4.1): the human on side 0, buddies alternating sides, all
// with the skirmish starting resources.
func SurvivalSkirmishConfig(mapName string, buddies int, opts SurvivalOptions) SkirmishConfig {
	buddies = max(0, min(buddies, SurvivalMaxBuddies))
	players := make([]SkirmishPlayer, 1+buddies)
	for i := range players {
		players[i] = SkirmishPlayer{Side: i & 1, Color: 0, Metal: SkirmishDefaultMetal, Energy: SkirmishDefaultEnergy}
		if i > 0 {
			players[i].Color = 1 + i
		}
	}
	return SurvivalConfigFor(mapName, players, opts)
}

// SurvivalConfigFor builds a Survival setup from the setup screen's rows:
// players[0] is the human and the rest are buddies, all on one team; the
// attacker takes the row after them, allied with nobody, in the first colour
// no survivor uses (red when free). The skirmish rule words keep their
// defaults for the caller to override.
func SurvivalConfigFor(mapName string, players []SkirmishPlayer, opts SurvivalOptions) SkirmishConfig {
	if len(players) > 1+SurvivalMaxBuddies {
		players = players[:1+SurvivalMaxBuddies]
	}
	opts.Enabled = true
	const team = 2
	cfg := SkirmishConfig{MapName: mapName, NumPlayers: len(players) + 1, Survival: opts}
	var used [SkirmishMaxPlayers]bool
	for i, p := range players {
		p.AllyGroup = team
		p.Controller = SkirmishControllerComputer
		if i == 0 {
			p.Controller = SkirmishControllerHuman
		}
		if p.Metal == 0 {
			p.Metal = SkirmishDefaultMetal
		}
		if p.Energy == 0 {
			p.Energy = SkirmishDefaultEnergy
		}
		cfg.Players[i] = p
		if p.Color >= 0 && p.Color < SkirmishMaxPlayers {
			used[p.Color] = true
		}
	}
	color := survivalAttackerColor
	if used[color] {
		for c := 0; c < SkirmishMaxPlayers; c++ {
			if !used[c] {
				color = c
				break
			}
		}
	}
	cfg.Players[len(players)] = SkirmishPlayer{Controller: SkirmishControllerComputer, AllyGroup: SkirmishDefaultAllyGroup, Side: 1, Color: color, Metal: SkirmishDefaultMetal, Energy: SkirmishDefaultEnergy}
	_ = cfg.Normalize()
	return cfg
}

// survivalAttacker is the attacker's slot, or -1 when the setup is not a
// Survival battle.
func (c SkirmishConfig) survivalAttacker() int {
	if !c.Survival.Enabled || c.NumPlayers < 2 || c.NumPlayers > 10 {
		return -1
	}
	return c.NumPlayers - 1
}

// IsSurvival reports whether the session is a Survival battle.
func (s *Session) IsSurvival() bool { return s != nil && s.Survival != nil }

// isSurvivalAttacker reports whether owner is the Survival attacker slot. The
// attacker is not a player in the result (DESIGN_SURVIVAL §8).
func (s *Session) isSurvivalAttacker(owner int) bool {
	return s != nil && s.Survival != nil && owner == int(s.Survival.attacker)
}

type survivalPhase uint8

// The director's phases are the published ones, so the HUD reads them as is.
const (
	survivalGrace    = survivalPhase(frame.SurvivalGrace)
	survivalWarning  = survivalPhase(frame.SurvivalWarning)
	survivalActive   = survivalPhase(frame.SurvivalActive)
	survivalDowntime = survivalPhase(frame.SurvivalDowntime)
)

// survivalClass is one movement class's static connected regions and the
// region that reaches the base (DESIGN_SURVIVAL §6.6).
type survivalClass struct {
	key     string
	profile movement.Profile
	regions survival.Regions
	base    int32 // 0: this class cannot reach the base
}

// survivalUnit is one live attacker unit and the target it was sent to.
type survivalUnit struct {
	h      pool.Handle
	wave   int
	target pool.Handle
}

// SurvivalStats are one player's Survival counters (DESIGN_SURVIVAL §8).
type SurvivalStats struct {
	Damage    int64 // priced damage this slot dealt to attacker units
	Destroyed int64 // wave cost of attacker units this slot killed
	Lost      int64 // wave cost of this slot's own units lost
}

type survivalState struct {
	attacker uint8
	team     []uint8    // the survivors, slot-ascending
	settled  [10]uint32 // each survivor's settlement deadline at the last income split
	accts    [10]survival.Account
	tuning   survival.Tuning
	opts     survival.Options
	pool     survival.Pool

	centreX, centreZ int32 // cells
	startClass       *survivalClass
	startRegion      int32
	deposits         int // extra deposits stamped at battle entry
	depositAt        [][2]int32
	depositWant      int // deposits a start usually has × survivors
	depositHave      int // deposits already near the start site
	classes          []*survivalClass

	phase        survivalPhase
	phaseEnd     uint32
	wave         int // the last planned wave's number
	plan         survival.Wave
	nextG        int // spawn cursor: group, then pick
	nextP        int
	lastSpawn    uint32
	waveUnits    []pool.Handle // the active wave's created units
	units        []survivalUnit
	nextRetarget uint32

	survived   int   // waves whose arrival the survivors outlasted
	wavePoints int64 // what those waves scored, bonuses included
	cleanLost  bool  // a finished survivor structure fell to the active wave
	stats      [10]SurvivalStats
	// removed is the health taken so far from each live attacker unit, the
	// running total its priced damage is credited from (§8). Lookup only;
	// never ranged.
	removed map[pool.Handle]int32

	walk    []*units.Unit
	history []SurvivalWaveRecord
	// info is the scenario record every survivor's manager holds
	// (ai.Manager.Survival); the director appends each warning to it.
	info *ai.SurvivalInfo
}

// SurvivalWaveRecord is one planned wave, as reports and tests read it.
type SurvivalWaveRecord struct {
	Number int                   `json:"number"`
	Tick   uint32                `json:"planned_tick"`
	Budget int64                 `json:"budget"`
	Groups []SurvivalGroupRecord `json:"groups"`
}

// SurvivalGroupRecord is one entry direction of a planned wave.
type SurvivalGroupRecord struct {
	From   string   `json:"from"`
	Domain string   `json:"domain"`
	Units  []string `json:"units"`
}

// SurvivalReport is the headless report's Survival block.
type SurvivalReport struct {
	Wave     int                   `json:"wave"`
	State    string                `json:"state"`
	Deposits int                   `json:"extra_deposits"`
	Want     int                   `json:"deposits_wanted"`
	Have     int                   `json:"deposits_near_start"`
	Outcome  *frame.SurvivalResult `json:"outcome"`
	Waves    []SurvivalWaveRecord  `json:"waves"`
}

// SurvivalReport returns the director's report, nil outside Survival.
func (s *Session) SurvivalReport(tick uint32) *SurvivalReport {
	if s == nil || s.Survival == nil {
		return nil
	}
	wave, state, _ := s.SurvivalStatus(tick)
	return &SurvivalReport{Wave: wave, State: state, Deposits: s.Survival.deposits, Want: s.Survival.depositWant, Have: s.Survival.depositHave, Outcome: s.survivalResult(tick), Waves: append([]SurvivalWaveRecord(nil), s.Survival.history...)}
}

// initSurvival builds the director after services are bound and before
// commanders are placed. It derives the wave pool from the bound
// construction rules' build products.
func (s *Session) initSurvival(cfg SkirmishConfig) error {
	attacker := cfg.survivalAttacker()
	if attacker < 0 {
		return nil
	}
	var rules construction.Rules
	if s.Build != nil {
		rules = s.Build.Rules
	}
	st := &survivalState{
		attacker: uint8(attacker),
		tuning:   survival.DefaultTuning(cfg.Survival.Pace),
		opts:     survival.Options{NoAir: cfg.Survival.NoAir, NoNaval: cfg.Survival.NoNaval},
		pool: survival.BuildPool(s.Catalog, func(menu *content.BuildMenuPage) []string {
			return construction.BuildProducts(rules, menu)
		}),
	}
	if len(st.pool.Units) == 0 {
		return fmt.Errorf("nanolathe: survival wave pool is empty: logical path %s, providers searched [catalog build menus], expected at least one armed mobile unit reachable from a commander", cfg.MapName)
	}
	st.phase = survivalGrace
	st.phaseEnd = st.tuning.FirstWaveDelay
	// The survivors are one side (DESIGN_SURVIVAL §4.3): shared sight, explored
	// map and radar, and income split evenly. Set before any coverage exists.
	var team []visibility.PlayerID
	for p := 0; p < cfg.NumPlayers; p++ {
		if p != attacker {
			st.team = append(st.team, uint8(p))
			team = append(team, visibility.PlayerID(p))
		}
	}
	if s.Vis != nil {
		s.Vis.SetVisionTeam(team)
	}
	s.Survival = st
	s.EnemyOwner = uint8(attacker)
	return nil
}

// survivalClassFor returns the static regions of a definition's movement
// class, labelling them on first use. Structures and features in place at
// that moment are ignored: the regions are terrain-only, because attackers
// fight through what the player builds.
func (s *Session) survivalClassFor(def *content.UnitDef) *survivalClass {
	st := s.Survival
	if st == nil || def == nil || s.World == nil {
		return nil
	}
	key := content.CanonicalKey(def.MovementClass)
	var profile movement.Profile
	if mc := s.movementClass(key); mc != nil {
		profile = movement.NewProfile(mc)
	} else {
		profile = movement.NewScratchProfile(def)
		key = "fbi:" + content.CanonicalKey(def.UnitName)
	}
	for _, c := range st.classes {
		if c.key == key {
			return c
		}
	}
	t := s.World
	c := &survivalClass{key: key, profile: profile}
	c.regions = survival.Label(t.CellW, t.CellH, func(x, z int32) bool {
		return profile.IsPassableFootprint(t, x, z)
	})
	st.classes = append(st.classes, c)
	return c
}

func (s *Session) movementClass(key string) *content.MovementClass {
	if key == "" || s.Catalog == nil {
		return nil
	}
	return s.Catalog.Movement[key]
}

// survivalBaseRegion settles which region of a class reaches the base: the
// region of the class's passable cell nearest the centre site, within reach
// cells. Ships use a wider reach, since they fight from the shore.
func (st *survivalState) baseRegion(c *survivalClass, reach int32) int32 {
	if c.base != 0 {
		return c.base
	}
	best, bestD := int32(0), int64(-1)
	for dz := -reach; dz <= reach; dz++ {
		for dx := -reach; dx <= reach; dx++ {
			id := c.regions.At(st.centreX+dx, st.centreZ+dz)
			if id == 0 {
				continue
			}
			d := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			if bestD < 0 || d < bestD {
				best, bestD = id, d
			}
		}
	}
	c.base = best
	return best
}

const (
	survivalLandReach  = 8
	survivalWaterReach = 40
	survivalSpawnReach = 48
)

func (s *Session) survivalReach(def *content.UnitDef) int32 {
	if survival.DomainOf(def, s.Catalog.Movement) == survival.Naval {
		return survivalWaterReach
	}
	return survivalLandReach
}

// placeSurvivalCommanders replaces the StartPos walk for a Survival battle
// (DESIGN_SURVIVAL §4.2): the human's commander at the centre site on its
// class's largest region, buddies on a ring around it. The attacker gets
// none.
// survivalChooseSite settles the start site (DESIGN_SURVIVAL §4.2) and labels
// every wave class's regions. It runs after the authored features are stamped
// and before the extra deposits and the commanders.
func (s *Session) survivalChooseSite(cfg SkirmishConfig) error {
	st := s.Survival
	var human *content.UnitDef
	for p := 0; p < cfg.NumPlayers; p++ {
		if p == int(st.attacker) {
			continue
		}
		def, err := skirmishCommander(s.Catalog, cfg.Players[p].Side, p)
		if err != nil {
			return err
		}
		if p == 0 {
			human = def
		}
	}
	cls := s.survivalClassFor(human)
	region := cls.regions.Largest()
	sx, sz := s.survivalRoomiestSite(human, cls, region)
	cx, cz, ok := cls.regions.Nearest(region, sx, sz)
	if !ok {
		return fmt.Errorf("nanolathe: survival has no start site: logical path %s, providers searched [terrain], expected a cell the commander can stand on", cfg.MapName)
	}
	st.centreX, st.centreZ = cx, cz
	st.startClass, st.startRegion = cls, region
	// Label every wave class now, at load, rather than when the first wave
	// is planned: a large map's flood fills are a visible stall mid-battle.
	for _, u := range st.pool.Units {
		if u.Domain != survival.Air {
			c := s.survivalClassFor(u.Def)
			st.baseRegion(c, s.survivalReach(u.Def))
		}
	}
	return nil
}

// placeSurvivalCommanders replaces the StartPos walk: the human's commander at
// the start site, buddies on a ring around it. The attacker gets none.
func (s *Session) placeSurvivalCommanders(cfg SkirmishConfig) error {
	st := s.Survival
	cx, cz, region := st.centreX, st.centreZ, st.startRegion
	buddies := cfg.NumPlayers - 2
	info := &ai.SurvivalInfo{CentreX: cx*16 + 8, CentreZ: cz*16 + 8, Attacker: st.attacker}
	for p := 0; p < cfg.NumPlayers; p++ {
		if p == int(st.attacker) {
			continue
		}
		def, _ := skirmishCommander(s.Catalog, cfg.Players[p].Side, p)
		x, z := cx, cz
		if p > 0 {
			angle := numeric.Angle(uint32(p-1) * 65536 / uint32(buddies))
			x = cx + int32(numeric.MulRound(numeric.Cos(angle), st.tuning.BuddyRing))
			z = cz + int32(numeric.MulRound(numeric.Sin(angle), st.tuning.BuddyRing))
		}
		c := s.survivalClassFor(def)
		wx, wy, wz, found := s.survivalFindCell(def, c, region, x, z, survivalSpawnReach)
		if !found {
			return fmt.Errorf("nanolathe: survival cannot place commander: logical path %s, providers searched [terrain], expected a free cell near (%d,%d) for slot %d", cfg.MapName, x, z, p)
		}
		h, err := s.Units.Create(def, uint8(p), wx, wy, wz)
		if err != nil {
			continue
		}
		info.Team = append(info.Team, uint8(p))
		info.Computer = append(info.Computer, cfg.Players[p].Controller == SkirmishControllerComputer)
		info.Starts = append(info.Starts, [2]int32{int32(wx >> 16), int32(wz >> 16)})
		if u := s.Units.Unit(h); u != nil {
			u.PlacementIdx = -1
			publishOne(s, u)
			if s.Movement != nil && s.Movement.Routes != nil {
				s.Movement.EnsureUnit(u)
			}
		}
	}
	// Every survivor's manager learns the scenario it plays in: the start
	// site, the team and where each member began. Only the Modern AI
	// controller reads it (a Modern buddy's survival brain); a Classic
	// buddy's step carries it dormant, and the attacker's manager gets none.
	st.info = info
	for _, p := range info.Team {
		if m := s.AI[p]; m != nil {
			m.Survival = info
		}
	}
	return nil
}

// Start-site search (DESIGN_SURVIVAL §4.2): candidates every siteStep cells
// within a third of the smaller map dimension of the centre; each scores the
// lattice anchors within siteWindow cells where the side's first factory could
// be placed.
const (
	siteStep   = 4
	siteSample = 2
	siteWindow = 12
)

// survivalRoomiestSite is the cell near the map centre with the most room to
// build: the candidate of the commander's region whose neighbourhood admits
// the most placements of the first factory on the commander's own menu, ties
// to the nearer candidate and then row-major. A map centre on a cramped
// plateau or a cliff no longer decides the start. With no factory on the menu
// it is the map centre.
func (s *Session) survivalRoomiestSite(commander *content.UnitDef, cls *survivalClass, region int32) (int32, int32) {
	w, h := s.World.CellW, s.World.CellH
	mx, mz := w/2, h/2
	factory := s.survivalFirstFactory(commander)
	if factory == nil {
		return mx, mz
	}
	r := min(w, h) / 3
	x0, z0 := max(mx-r-siteWindow, 0), max(mz-r-siteWindow, 0)
	x1, z1 := min(mx+r+siteWindow, w-1), min(mz+r+siteWindow, h-1)
	gw := (x1-x0)/siteSample + 1
	gh := (z1-z0)/siteSample + 1
	room := make([]bool, int(gw)*int(gh))
	for gz := int32(0); gz < gh; gz++ {
		for gx := int32(0); gx < gw; gx++ {
			cx, cz := x0+gx*siteSample, z0+gz*siteSample
			wx := numeric.Fixed((cx*16 + 8) << 16)
			wz := numeric.Fixed((cz*16 + 8) << 16)
			_, _, _, reason := s.checkSpawnPlacement(factory, wx, 0, wz)
			room[gz*gw+gx] = reason == ""
		}
	}
	bestX, bestZ, bestScore, bestD := mx, mz, -1, int64(0)
	for cz := mz - r; cz <= mz+r; cz += siteStep {
		for cx := mx - r; cx <= mx+r; cx += siteStep {
			if cls.regions.At(cx, cz) != region {
				continue
			}
			score := 0
			for gz := max((cz-siteWindow-z0)/siteSample, 0); gz <= min((cz+siteWindow-z0)/siteSample, gh-1); gz++ {
				for gx := max((cx-siteWindow-x0)/siteSample, 0); gx <= min((cx+siteWindow-x0)/siteSample, gw-1); gx++ {
					if room[gz*gw+gx] {
						score++
					}
				}
			}
			dx, dz := int64(cx-mx), int64(cz-mz)
			d := dx*dx + dz*dz
			if score > bestScore || (score == bestScore && d < bestD) {
				bestX, bestZ, bestScore, bestD = cx, cz, score, d
			}
		}
	}
	return bestX, bestZ
}

// survivalFirstFactory is the first immobile builder on a commander's build
// menu under the bound rules.
func (s *Session) survivalFirstFactory(commander *content.UnitDef) *content.UnitDef {
	if commander == nil || s.Catalog == nil {
		return nil
	}
	var rules construction.Rules
	if s.Build != nil {
		rules = s.Build.Rules
	}
	for _, key := range construction.BuildProducts(rules, s.Catalog.BuildMenus[content.CanonicalKey(commander.UnitName)]) {
		if def, ok := s.Catalog.Unit(key); ok && def != nil && def.Builder && def.BMCode == 0 {
			return def
		}
	}
	return nil
}

// survivalFindCell searches rings outward from (x, z) for a cell of region id
// (0: any passable cell of the class) where def passes the spawn command's
// placement checks, returning world coordinates.
func (s *Session) survivalFindCell(def *content.UnitDef, c *survivalClass, id, x, z, reach int32) (numeric.Fixed, numeric.Fixed, numeric.Fixed, bool) {
	for rad := int32(0); rad <= reach; rad++ {
		for cz := z - rad; cz <= z+rad; cz++ {
			for cx := x - rad; cx <= x+rad; cx++ {
				if cx != x-rad && cx != x+rad && cz != z-rad && cz != z+rad {
					continue
				}
				if c != nil {
					got := c.regions.At(cx, cz)
					if got == 0 || (id != 0 && got != id) {
						continue
					}
				} else if cx < 0 || cz < 0 || cx >= s.World.CellW || cz >= s.World.CellH {
					continue
				}
				wx := numeric.Fixed((cx*16 + 8) << 16)
				wz := numeric.Fixed((cz*16 + 8) << 16)
				wy := s.World.HeightAt(wx, wz)
				if wy < 0 {
					wy = 0
				}
				if px, py, pz, reason := s.checkSpawnPlacement(def, wx, wy, wz); reason == "" {
					return px, py, pz, true
				}
			}
		}
	}
	return 0, 0, 0, false
}

// stepSurvival is the wave director's once-per-tick step at the end of phase
// 1, after human commands (DESIGN_SURVIVAL §6.8).
func (s *Session) stepSurvival(tick uint32) {
	st := s.Survival
	if st == nil || s.result.Ended {
		return
	}
	s.survivalShareIncome()
	switch st.phase {
	case survivalGrace, survivalDowntime:
		if tick >= st.phaseEnd {
			s.survivalBeginWarning(tick)
		}
	case survivalWarning:
		if left := st.phaseEnd - tick; tick < st.phaseEnd && left%30 == 0 && left <= 5*30 {
			s.survivalCount(int(left / 30))
		}
		if tick >= st.phaseEnd {
			st.phase = survivalActive
			st.nextG, st.nextP = 0, 0
			st.lastSpawn = tick
			st.waveUnits = st.waveUnits[:0]
			st.cleanLost = false
			s.survivalCount(0)
			s.survivalSay(tick, fmt.Sprintf("Wave %d has arrived", st.wave))
		}
	case survivalActive:
		s.survivalSpawn(tick)
		if st.nextG < len(st.plan.Groups) {
			break // still arriving
		}
		// The next wave's clock runs from this wave's arrival: the downtime
		// after it is cleared, but never more than Straggle past arrival plus
		// that downtime. Units walking in from a far edge, or stuck, overlap
		// the next wave instead of stalling the battle.
		down := st.tuning.Downtime(st.plan.Budget, st.pool.Tier1Median)
		deadline := st.lastSpawn + down + st.tuning.Straggle
		dead := s.survivalWaveDead()
		if !dead && tick < deadline {
			break
		}
		ws := st.tuning.ScoreWave(st.plan.Budget, dead, !st.cleanLost, tick-st.lastSpawn, down+st.tuning.Straggle)
		st.survived++
		st.wavePoints += ws.Total()
		st.phase = survivalDowntime
		st.phaseEnd = min(tick+down, deadline)
		s.survivalReward()
		word := "survived"
		if dead {
			word = "cleared"
		}
		msg := fmt.Sprintf("Wave %d %s:", st.wave, word)
		if r := st.tuning.WaveReward; r > 0 {
			msg += fmt.Sprintf(" +%d metal and energy,", int(r))
		}
		msg += fmt.Sprintf(" score +%d", ws.Total())
		switch {
		case ws.Fast > 0 && ws.Clean > 0:
			msg += " (fast, clean)"
		case ws.Fast > 0:
			msg += " (fast)"
		case ws.Clean > 0:
			msg += " (clean)"
		}
		s.survivalSay(tick, msg)
	}
	if tick >= st.nextRetarget {
		st.nextRetarget = tick + st.tuning.RetargetEvery
		s.survivalRetarget(tick)
	}
}

func (s *Session) survivalBeginWarning(tick uint32) {
	st := s.Survival
	st.wave++
	st.plan = survival.Plan(st.wave, tick, &st.pool, st.tuning, st.opts, s.survivalEntry(), s.SimRNG())
	st.phase = survivalWarning
	st.phaseEnd = tick + st.tuning.WarningTime
	rec := SurvivalWaveRecord{Number: st.wave, Tick: tick, Budget: st.plan.Budget}
	for _, g := range st.plan.Groups {
		gr := SurvivalGroupRecord{From: compassWord(g.Angle), Domain: g.Domain.String()}
		for _, i := range g.Picks {
			gr.Units = append(gr.Units, st.pool.Units[i].Key)
		}
		rec.Groups = append(rec.Groups, gr)
	}
	st.history = append(st.history, rec)
	s.survivalPublishWarning(tick)
	var parts []string
	for _, g := range st.plan.Groups {
		word := compassWord(g.Angle)
		switch g.Domain {
		case survival.Air:
			word = "air from the " + word
		case survival.Naval:
			word = "naval from the " + word
		case survival.Hover:
			word = "hover from the " + word
		default:
			word = "the " + word
		}
		parts = append(parts, word)
	}
	s.survivalSay(tick, fmt.Sprintf("Wave %d incoming: %s", st.wave, strings.Join(parts, ", ")))
}

// survivalPublishWarning hands the survivors' managers what the warning
// announces (DESIGN_SURVIVAL §6.7): the wave, when it arrives, and each
// entry direction with its theme. Amphibious groups are announced as plain
// directions, so they are published as ground. It draws nothing and reads
// only the planned wave.
func (s *Session) survivalPublishWarning(tick uint32) {
	st := s.Survival
	if st.info == nil {
		return
	}
	w := ai.SurvivalWarning{Wave: int32(st.wave), Tick: tick, Arrive: st.phaseEnd}
	for _, g := range st.plan.Groups {
		ex, ez := s.survivalEntryCell(g.Angle)
		domain := "ground"
		switch g.Domain {
		case survival.Air:
			domain = "air"
		case survival.Naval:
			domain = "naval"
		case survival.Hover:
			domain = "hover"
		}
		w.Groups = append(w.Groups, ai.SurvivalApproach{Angle: g.Angle, X: ex*16 + 8, Z: ez*16 + 8, Domain: domain})
	}
	st.info.PublishWarning(w)
}

// compassWord names the direction a wave comes FROM, as seen on the map
// (north up). Angles follow the simulation trig: x by cosine, z by sine.
func compassWord(a uint16) string {
	words := [8]string{"east", "southeast", "south", "southwest", "west", "northwest", "north", "northeast"}
	return words[((uint32(a)+4096)>>13)&7]
}

// survivalEntryCell is the entry point for an angle: where the ray from the
// centre site leaves the map, moved EdgeInset cells inward.
func (s *Session) survivalEntryCell(angle uint16) (int32, int32) {
	st := s.Survival
	w, h := s.World.CellW, s.World.CellH
	c, sn := numeric.Cos(numeric.Angle(angle)), numeric.Sin(numeric.Angle(angle))
	x, z := st.centreX, st.centreZ
	for step := int32(1); ; step++ {
		nx := st.centreX + int32(numeric.MulRound(c, step))
		nz := st.centreZ + int32(numeric.MulRound(sn, step))
		in := st.tuning.EdgeInset
		if nx < in || nz < in || nx >= w-in || nz >= h-in {
			return x, z
		}
		x, z = nx, nz
	}
}

// survivalEntry answers the planner's reachability question with a cache per
// (angle, class) for the one planning call.
func (s *Session) survivalEntry() survival.Entry {
	st := s.Survival
	type key struct {
		angle uint16
		class string
	}
	cache := make(map[key]bool) // lookup only; never ranged
	return func(angle uint16, i int) bool {
		u := st.pool.Units[i]
		if u.Domain == survival.Air {
			return true
		}
		c := s.survivalClassFor(u.Def)
		k := key{angle, c.key}
		if v, ok := cache[k]; ok {
			return v
		}
		base := st.baseRegion(c, s.survivalReach(u.Def))
		ok := false
		if base != 0 {
			ex, ez := s.survivalEntryCell(angle)
			_, _, ok = c.regions.NearestWithin(base, ex, ez, survivalSpawnReach)
		}
		cache[k] = ok
		return ok
	}
}

// survivalSpawn creates up to SpawnPerTick of the wave's units
// (DESIGN_SURVIVAL §6.5).
func (s *Session) survivalSpawn(tick uint32) {
	st := s.Survival
	for made := 0; made < st.tuning.SpawnPerTick && st.nextG < len(st.plan.Groups); {
		g := st.plan.Groups[st.nextG]
		if st.nextP >= len(g.Picks) {
			st.nextG, st.nextP = st.nextG+1, 0
			continue
		}
		pu := st.pool.Units[g.Picks[st.nextP]]
		st.nextP++
		made++
		ex, ez := s.survivalEntryCell(g.Angle)
		var c *survivalClass
		var id int32
		if pu.Domain != survival.Air {
			c = s.survivalClassFor(pu.Def)
			id = st.baseRegion(c, s.survivalReach(pu.Def))
			if id == 0 {
				continue
			}
		}
		x, y, z, ok := s.survivalFindCell(pu.Def, c, id, ex, ez, survivalSpawnReach)
		if !ok {
			continue
		}
		h, err := s.Units.Create(pu.Def, st.attacker, x, y, z)
		if err != nil {
			continue // unit limit: the rest of the wave is dropped as it comes
		}
		u := s.Units.Unit(h)
		if u == nil {
			continue
		}
		if s.Movement != nil {
			s.Movement.EnsureUnit(u)
		}
		// Roam and fire at will, as the computer player sets its own units.
		u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingMoveShift) | 2<<units.StandingMoveShift
		u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | 2<<units.StandingFireShift
		st.waveUnits = append(st.waveUnits, h)
		delete(st.removed, h) // a reused handle starts a new unit's total
		su := survivalUnit{h: h, wave: st.wave}
		s.survivalSend(&su, u, tick)
		st.units = append(st.units, su)
		st.lastSpawn = tick
	}
}

// survivalWaveDead reports that every unit the active wave created is dead.
func (s *Session) survivalWaveDead() bool {
	for _, h := range s.Survival.waveUnits {
		if u := s.Units.Unit(h); u != nil && u.Alive {
			return false
		}
	}
	return true
}

// survivalRetarget sends idle attackers, and those whose target died, to the
// nearest human-team unit, structures first (DESIGN_SURVIVAL §6.7).
func (s *Session) survivalRetarget(tick uint32) {
	st := s.Survival
	kept := st.units[:0]
	for _, su := range st.units {
		u := s.Units.Unit(su.h)
		if u == nil || !u.Alive || u.Owner != st.attacker {
			continue
		}
		t := s.Units.Unit(su.target)
		q := orders.BindQueueBinding(u, s.orderBinding())
		if t == nil || !t.Alive || !q.HasIssuedWork() {
			s.survivalSend(&su, u, tick)
		}
		kept = append(kept, su)
	}
	st.units = kept
}

func (s *Session) orderBinding() *orders.QueueBinding {
	if s.Build == nil {
		return nil
	}
	return s.Build.OrderBinding
}

// survivalSend picks su's target and issues a patrol to it.
//
// TODO(question): what a single-waypoint patrol does on arrival — cycle on
// the waypoint or complete — is not recorded in DESIGN_UNITS_ORDERS_COB §3.5.
// Either way the retarget pass re-sends the unit once its queue holds no
// issued work or its target has died; observing it in a play-test settles
// whether that pass is doing the work.
func (s *Session) survivalSend(su *survivalUnit, u *units.Unit, tick uint32) {
	target := s.survivalTarget(u)
	if target == nil {
		su.target = 0
		return
	}
	su.target = target.Handle
	q := orders.BindQueueBinding(u, s.orderBinding())
	if q == nil {
		return
	}
	id := orders.Resolve(9, u, nil, &orders.ResolvePos{X: target.X, Y: target.Y, Z: target.Z})
	if id == 0 {
		return
	}
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	q.Push(id, orders.NewNodeForOrder(id, 0, target.X, target.Y, target.Z, tick, u.Handle, false))
}

// survivalTarget is the nearest living unit of the human team to u,
// structures before mobile units; ties go to pool order.
func (s *Session) survivalTarget(u *units.Unit) *units.Unit {
	st := s.Survival
	local := s.LocalOwner
	var best *units.Unit
	bestD, bestStruct := int64(-1), false
	st.walk = s.Units.AppendLiveSliced(st.walk[:0])
	for _, v := range st.walk {
		if v == nil || !v.Alive || v.Def == nil || v.Owner == st.attacker {
			continue
		}
		if v.Owner != local && (s.Econ == nil || !s.Econ.DeclaresAlliance(local, v.Owner)) {
			continue
		}
		isStruct := v.Def.BMCode == 0
		if best != nil && bestStruct && !isStruct {
			continue
		}
		dx := int64(v.X-u.X) >> 16
		dz := int64(v.Z-u.Z) >> 16
		d := dx*dx + dz*dz
		if best == nil || (isStruct && !bestStruct) || d < bestD {
			best, bestD, bestStruct = v, d, isStruct
		}
	}
	return best
}

// survivalNoteDamage credits the priced damage a survivor dealt to an
// attacker unit (DESIGN_SURVIVAL §8). It is bound to combat's HealthLost.
func (s *Session) survivalNoteDamage(victim, attacker *units.Unit, lost int32) {
	st := s.Survival
	if st == nil || victim == nil || attacker == nil || victim.Owner != st.attacker || victim.Def == nil || !st.onTeam(attacker.Owner) {
		return
	}
	if st.removed == nil {
		st.removed = make(map[pool.Handle]int32)
	}
	cost := st.pool.Cost(victim.Def)
	before := st.removed[victim.Handle]
	after := min(before+lost, max(victim.MaxHealth, 0))
	st.removed[victim.Handle] = after
	st.stats[attacker.Owner].Damage += survival.DamageValue(cost, victim.MaxHealth, after) - survival.DamageValue(cost, victim.MaxHealth, before)
}

// survivalNoteDeath files one death into the Survival counters. A finished
// survivor structure the attacker's weapons destroyed costs the active wave
// its clean bonus; one its owner reclaimed or self-destructed does not.
func (s *Session) survivalNoteDeath(u *units.Unit, cause combat.Cause) {
	st := s.Survival
	if st == nil || u == nil || u.Def == nil {
		return
	}
	v := st.pool.Cost(u.Def)
	if u.Owner == st.attacker {
		delete(st.removed, u.Handle)
		if side := int(u.LastDamageSide); side < 10 && side != int(st.attacker) {
			st.stats[side].Destroyed += v
		}
		return
	}
	if int(u.Owner) < 10 {
		st.stats[u.Owner].Lost += v
	}
	if st.phase == survivalActive && u.Def.BMCode == 0 && u.Remaining == 0 && u.LastDamageSide == st.attacker && cause == combat.CauseOrdinary {
		st.cleanLost = true
	}
}

// survivalScore is the team's score so far: every survivor's priced damage
// plus what the survived waves scored (DESIGN_SURVIVAL §8).
func (st *survivalState) survivalScore() int64 {
	score := st.wavePoints
	for _, p := range st.team {
		score += st.stats[p].Damage
	}
	return score
}

func (s *Session) survivalSay(tick uint32, text string) {
	if s.publication != nil && s.publication.events != nil {
		s.publication.events.EmitAnnounce(frame.Event{Tick: tick, StatusText: text, StatusClass: 4, AnnounceSlot: 10})
	}
}

func (s *Session) survivalCount(n int) {
	switch n {
	case 5:
		s.EmitCount5(0)
	case 4:
		s.EmitCount4(0)
	case 3:
		s.EmitCount3(0)
	case 2:
		s.EmitCount2(0)
	case 1:
		s.EmitCount1(0)
	case 0:
		s.EmitCount0(0)
	}
}

// survivalResult is the Survival line of the result (DESIGN_SURVIVAL §8).
func (s *Session) survivalResult(tick uint32) *frame.SurvivalResult {
	st := s.Survival
	if st == nil {
		return nil
	}
	r := &frame.SurvivalResult{
		Waves:      int32(st.survived),
		Reached:    int32(st.wave),
		TicksAlive: tick,
		WavePoints: st.wavePoints,
		Score:      st.survivalScore(),
	}
	for _, p := range st.team {
		c := st.stats[p]
		r.Damage += c.Damage
		r.Destroyed += c.Destroyed
		r.Lost += c.Lost
		r.SlotDamage[p] = c.Damage
	}
	return r
}

// SurvivalStatus is the director's state for the HUD and reports.
func (s *Session) SurvivalStatus(tick uint32) (wave int, state string, secondsLeft int) {
	st := s.Survival
	if st == nil {
		return 0, "", 0
	}
	left := 0
	if st.phaseEnd > tick {
		left = int((st.phaseEnd - tick + 29) / 30)
	}
	switch st.phase {
	case survivalGrace:
		return 1, "first wave in", left
	case survivalWarning:
		return st.wave, "incoming in", left
	case survivalActive:
		return st.wave, "in progress", 0
	default:
		return st.wave + 1, "next wave in", left
	}
}

// survivalFrameStatus is the committed HUD state for one tick.
func (s *Session) survivalFrameStatus(tick uint32) frame.SurvivalStatus {
	st := s.Survival
	if st == nil {
		return frame.SurvivalStatus{}
	}
	wave, _, left := s.SurvivalStatus(tick)
	return frame.SurvivalStatus{
		Active:      true,
		Wave:        int32(wave),
		Phase:       uint8(st.phase),
		SecondsLeft: int32(left),
		Attackers:   int32(s.Units.LiveCountForPlayer(int(st.attacker))),
		Score:       st.survivalScore(),
	}
}

// survivalShareIncome splits the survivors' income evenly (DESIGN_SURVIVAL
// §4.3). Once a tick, before any settlement, it finds each living survivor
// whose settlement ran since the last visit and divides that pass's
// production equally among the living survivors: the earner keeps 1/n and
// each teammate is credited 1/n, moved stock to stock and conserved exactly.
// Everyone still spends only their own stock, so a computer buddy cannot
// drain the human's. A share that does not fit in a teammate's storage goes
// back to the earner, whose next settlement clamps and counts any waste as
// usual. It draws nothing.
// survivalReward grants each living survivor the wave reward in metal and in
// energy, up to their storage (DESIGN_SURVIVAL §6.9). It is a grant, not
// production, so the income split never sees it.
func (s *Session) survivalReward() {
	st := s.Survival
	if s.Econ == nil || s.Units == nil || st.tuning.WaveReward <= 0 {
		return
	}
	for _, p := range st.team {
		if s.Units.LiveCountForPlayer(int(p)) == 0 {
			continue
		}
		pl := &s.Econ.Players[p]
		for r := range pl.Stock {
			pl.Stock[r] = max(pl.Stock[r], min(pl.Stock[r]+st.tuning.WaveReward, pl.Capacity[r]))
		}
	}
}

func (s *Session) survivalShareIncome() {
	st := s.Survival
	if s.Econ == nil || s.Units == nil || len(st.team) < 2 {
		return
	}
	var live [10]uint8
	var settled [10]bool
	n, any := 0, false
	for _, p := range st.team {
		if s.Units.LiveCountForPlayer(int(p)) == 0 {
			continue
		}
		pl := &s.Econ.Players[p]
		settled[n] = pl.UpdateTime != st.settled[p]
		any = any || settled[n]
		st.settled[p] = pl.UpdateTime
		live[n] = p
		n++
	}
	if n < 2 || !any {
		return
	}
	accts := st.accts[:n]
	for r := 0; r < 2; r++ {
		for i, p := range live[:n] {
			pl := &s.Econ.Players[p]
			accts[i] = survival.Account{Stock: pl.Stock[r], Capacity: pl.Capacity[r]}
			if settled[i] {
				accts[i].Earned = pl.PassProduced[r]
			}
		}
		survival.SplitIncome(accts)
		for i, p := range live[:n] {
			s.Econ.Players[p].Stock[r] = accts[i].Stock
		}
	}
}

// survivalTeamEconomy shows a survivor's income as its share of the team's
// (DESIGN_SURVIVAL §4.3): the sum of the living survivors' last-pass
// production divided among them. Presentation only.
func (s *Session) survivalTeamEconomy(v *frame.EconomyView) {
	st := s.Survival
	if st == nil || s.Econ == nil || s.Units == nil || v == nil || !st.onTeam(v.Player) {
		return
	}
	var metal, energy float32
	n := 0
	for _, p := range st.team {
		if s.Units.LiveCountForPlayer(int(p)) == 0 {
			continue
		}
		pl := &s.Econ.Players[p]
		metal += pl.PassProduced[economy.Metal]
		energy += pl.PassProduced[economy.Energy]
		n++
	}
	if n > 1 {
		v.MetalProduced = metal / float32(n)
		v.EnergyProduced = energy / float32(n)
	}
}

func (st *survivalState) onTeam(p uint8) bool {
	for _, m := range st.team {
		if m == p {
			return true
		}
	}
	return false
}
