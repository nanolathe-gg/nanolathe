package aikit

import (
	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Brain is a computer player's decision maker. Think must depend only on the
// observation, the brain's own state and Kit.Rand; it must not keep pointers
// into the observation, read simulation objects, or draw from any other
// random source.
type Brain interface {
	Name() string
	Init(k *Kit)
	Think(k *Kit, o *Obs)
}

// Explainer is optional: a brain that can describe its current reasoning
// for visualization. Explain runs on the simulation thread between thinks
// and may allocate; production never calls it.
type Explainer interface {
	Explain(x *Explain)
}

// Reporter is optional: a brain that publishes its own end-of-match
// metrics (for example its model's prediction error) to the arena. It runs
// host-side after the match and never influences play.
type Reporter interface {
	Report(add func(name string, value int64))
}

// Explain is a brain's self-description at one moment.
type Explain struct {
	Mode   string
	Notes  []string
	Goals  []Goal
	Squads []SquadView
	Grids  []NamedGrid
}

// Goal is one scored option the brain weighed.
type Goal struct {
	Label  string
	Score  int32
	Chosen bool
	X, Z   int32
}

// SquadView is one group the brain is operating.
type SquadView struct {
	ID               int32
	Task             string
	X, Z             int32 // centroid
	TargetX, TargetZ int32
	Units            int32
	Strength         int64
	Enemy            int64 // estimated opposing strength at the target
}

// NamedGrid is a sector grid snapshot (for example an influence map).
type NamedGrid struct {
	Name   string
	W, H   int32
	Values []int32
}

// Probe is host-side instrumentation (timing, tracing). The session never
// reads a clock; the arena supplies one here [I6]. ThinkBegin/ThinkEnd run
// on a worker goroutine when the persona is asynchronous. A synchronous
// persona's first think, which runs behind the preparation (Host), is not
// probed, so a synchronous persona's Probe methods are only ever called on
// the simulation thread (PartProbe's PrepBegin/PrepEnd run on the worker).
type Probe interface {
	StepBegin(player uint8, tick uint32)
	StepEnd(player uint8, tick uint32)
	ThinkBegin(player uint8, tick uint32)
	ThinkEnd(player uint8, tick uint32)
}

// StepPart names a part of a host step on the simulation thread (PartProbe).
type StepPart uint8

const (
	PartBegin   StepPart = iota // the first eligible tick: unit table, generator, hand-off
	PartJoin                    // waiting for a worker that has not finished
	PartApply                   // applying a due batch
	PartObserve                 // building the observation
	PartThink                   // a think on the simulation thread, or handing one to the worker
	PartUpkeep                  // the retail step's engine upkeep
	NumStepParts
)

// PartProbe is optional Probe instrumentation that attributes a step's time.
// Part runs on the simulation thread as each part of the step finishes: the
// time since the previous Part (or StepBegin) belongs to part. PartJoin is
// reported only for a join that found the worker still busy, so its count is
// the number of waits. PrepBegin and PrepEnd bracket the preparation (the map
// analysis and the brain's Init) on the worker goroutine.
type PartProbe interface {
	Part(player uint8, tick uint32, part StepPart)
	PrepBegin(player uint8, tick uint32)
	PrepEnd(player uint8, tick uint32)
}

// Kit is what a brain may use during Init and Think: immutable tables and
// map knowledge, its persona, and the command emitter. Init runs on the
// host's preparation goroutine (Host), beside the simulation: it may read
// only the kit's tables, map and persona and draw from Rand. Commands are
// applied only from a think; one issued during Init is dropped.
type Kit struct {
	Me      uint8
	Side    string
	Table   *Table
	Map     *MapInfo
	Persona Persona
	Tick    uint32
	// Last is the outcome of the previous batch (applied, dropped by the APM
	// limit, stale actor or target, failed placement).
	Last ApplyStats
	// Budget is how many actions this think's batch can spend when it is
	// applied (the persona's APM bucket projected over the reaction window),
	// or -1 when the persona has no action limit. Commands beyond it are
	// dropped in emission order, so layers should ration against it.
	Budget int32
	// Rand is this player's private generator, seeded from the battle seed
	// and the player slot. Draw from it only in Init and Think (a core
	// policy's Init or Plan): never in Explain or Report, which run only
	// under instrumentation, so a draw there would make the game depend on
	// whether it was being watched.
	Rand *Rand

	out    *batch
	ownIdx []int32 // handle → index in the current observation's Own
	obs    *Obs
	tags   []int32
	inst   []*units.Unit // handle → the unit observed in that slot (observer.inst)
}

func (k *Kit) push(c Command, actors []pool.Handle) {
	b := k.out
	if b == nil {
		return // outside a think (a brain's Init): nothing to apply it with
	}
	c.first = int32(len(b.actors))
	for _, h := range actors {
		if int(h) >= len(k.ownIdx) || k.ownIdx[h] < 0 {
			continue
		}
		b.actors = append(b.actors, h)
		b.inst = append(b.inst, k.inst[h])
	}
	c.count = int32(len(b.actors)) - c.first
	if c.count == 0 {
		return
	}
	// The command acts on the target as observed; a different unit in the
	// same slot by the time the batch applies makes it stale (cmd.go).
	if c.Target != 0 && int(c.Target) < len(k.inst) {
		c.target = k.inst[c.Target]
	}
	b.cmds = append(b.cmds, c)
}

// Move orders actors to (x, z).
func (k *Kit) Move(actors []pool.Handle, x, z int32, queued bool) {
	k.push(Command{Kind: CmdMove, X: x, Z: z, Queued: queued, Spot: -1}, actors)
}

// Patrol orders actors to patrol to (x, z): they engage what they meet.
func (k *Kit) Patrol(actors []pool.Handle, x, z int32, queued bool) {
	k.push(Command{Kind: CmdPatrol, X: x, Z: z, Queued: queued, Spot: -1}, actors)
}

// Attack orders actors to attack a unit the observation contains.
func (k *Kit) Attack(actors []pool.Handle, target pool.Handle, queued bool) {
	k.push(Command{Kind: CmdAttack, Target: target, Queued: queued, Spot: -1}, actors)
}

// AttackPos orders actors to attack the ground at (x, z).
func (k *Kit) AttackPos(actors []pool.Handle, x, z int32, queued bool) {
	k.push(Command{Kind: CmdAttackPos, X: x, Z: z, Queued: queued, Spot: -1}, actors)
}

// Guard orders actors to guard (follow and assist) a friendly unit.
func (k *Kit) Guard(actors []pool.Handle, target pool.Handle, queued bool) {
	k.push(Command{Kind: CmdGuard, Target: target, Queued: queued, Spot: -1}, actors)
}

// Repair orders actors to help build or repair a friendly unit.
func (k *Kit) Repair(actors []pool.Handle, target pool.Handle, queued bool) {
	k.push(Command{Kind: CmdRepair, Target: target, Queued: queued, Spot: -1}, actors)
}

// Reclaim orders actors to reclaim a unit.
func (k *Kit) Reclaim(actors []pool.Handle, target pool.Handle, queued bool) {
	k.push(Command{Kind: CmdReclaim, Target: target, Queued: queued, Spot: -1}, actors)
}

// Stop clears actors' unprotected orders.
func (k *Kit) Stop(actors []pool.Handle) {
	k.push(Command{Kind: CmdStop, Spot: -1}, actors)
}

// Build orders a mobile builder to construct product. With spot >= 0 the
// site is that metal spot; otherwise the executor searches outward from
// (x, z), keeping spacing free cells between buildings.
func (k *Kit) Build(builder pool.Handle, product *UnitInfo, x, z, spot, spacing int32, queued bool) {
	var one [1]pool.Handle
	one[0] = builder
	k.push(Command{Kind: CmdBuild, Product: product, X: x, Z: z, Spot: spot, Spacing: spacing, Queued: queued}, one[:])
}

// Produce queues count units of product at a factory.
func (k *Kit) Produce(factory pool.Handle, product *UnitInfo, count int32) {
	var one [1]pool.Handle
	one[0] = factory
	k.push(Command{Kind: CmdProduce, Product: product, Count: count, Spot: -1}, one[:])
}

// Emitted is the number of commands emitted so far this think.
func (k *Kit) Emitted() int {
	if k.out == nil {
		return 0
	}
	return len(k.out.cmds)
}

// SetTag stores a brain-owned label on an own unit; it is carried in later
// observations until the unit dies.
func (k *Kit) SetTag(h pool.Handle, tag int32) {
	if int(h) < len(k.tags) {
		k.tags[h] = tag
	}
}

// Host runs one computer player's brain behind the ai.Planner seam.
//
// Preparation. The map analysis and the brain's Init cost from a few
// milliseconds to well over a hundred on a large map, and every player
// would pay it on the same first tick. So the first eligible tick (begin)
// takes only what must come from the live simulation — the unit table, the
// commander, the void cells — and hands the rest to a goroutine. A think
// that falls due before the preparation has been joined runs behind it on
// the same chain, and the chain is joined when that think's batch is due,
// exactly when a synchronous think's commands would take effect. Neither
// the preparation nor the think reads anything the simulation writes, so
// the game is the same whichever core does the work and however late it
// finishes; the simulation thread only waits when the chain is late. The
// parts of the map analysis that do not depend on the player's start are
// computed once per battle and shared by its hosts (mapShared).
//
// Saves. A save keeps the battle seed the generator was seeded from and the
// generator's position (Generator); a host built after the load seeds from
// the same battle seed, so its brain's Init draws the same style and opening
// variation, then continues the generator from the saved position. The
// brain's memory is not saved: it starts again from observation.
type Host struct {
	m       *ai.Manager
	brain   Brain
	persona Persona
	kit     Kit
	obs     Obs
	ob      observer
	ex      executor
	b       batch
	rand    Rand

	nextThink uint32
	inited    bool // begin ran: the table, executor and generator exist
	ready     bool // the preparation has been joined: Kit.Map and the brain are set up
	// flight is closed when the last job started off the simulation thread
	// (the preparation, a think behind it, an asynchronous think) has
	// finished; nil when none is outstanding. Jobs run one after another.
	flight chan struct{}

	// Probe is optional host instrumentation; nil costs one test.
	Probe Probe
	// Thinks counts completed thinks.
	Thinks uint32
}

// NewHost binds brain and persona to a manager. It does not touch the
// simulation; preparation starts on the first eligible tick.
func NewHost(m *ai.Manager, brain Brain, persona Persona) *Host {
	persona.normalize()
	return &Host{m: m, brain: brain, persona: persona}
}

// Brain returns the bound brain.
func (h *Host) Brain() Brain { return h.brain }

// Persona returns the bound persona.
func (h *Host) Persona() Persona { return h.persona }

// Observation returns the most recent observation (host-side readers only).
func (h *Host) Observation() *Obs { return &h.obs }

// Stats returns cumulative command outcomes.
func (h *Host) Stats() ApplyStats { return h.ex.stats }

// Kit returns the brain's kit (host-side readers only).
func (h *Host) Kit() *Kit { return &h.kit }

// Close waits for any work in flight. A host keeps no goroutine between
// jobs, so an unclosed host leaks nothing once its last job ends.
func (h *Host) Close() { h.Join() }

// Generator reports the private generator's position for a save, after
// waiting for work in flight (a think or the preparation may be drawing);
// false before the host has begun, when there is no generator yet. Waiting
// changes no game: a joined think's batch still applies at its own deadline.
// Call it between ticks, as a save does.
func (h *Host) Generator() (uint64, bool) {
	h.Join()
	if !h.inited {
		return 0, false
	}
	return h.rand.Position(), true
}

// onPhase is the first think tick at or after t. Players' thinks are
// staggered so their cost does not land on the same tick: each slot thinks
// on the ticks congruent to its own phase, however late its host started (a
// host built after a load starts late).
func (h *Host) onPhase(t uint32) uint32 {
	every := h.persona.ThinkEvery
	phase := uint32(h.m.Player) * 7 % every
	return t + (phase+every-t%every)%every
}

// begin runs on the first eligible tick: the parts of initialization that
// read the live simulation, then the preparation on a goroutine.
func (h *Host) begin(tick uint32, w *units.World) {
	m := h.m
	h.inited = true
	table := tableFor(m)
	// The extractor footprint and own anchor come from this owner's units:
	// the commander's cheapest extractor and the commander's position.
	var ownX, ownZ int32
	var side string
	var fx, fz int32 = 2, 2
	for _, u := range w.AppendLiveSliced(nil) {
		if u.Owner != m.Player || u.Def == nil || !u.Def.Commander {
			continue
		}
		ownX, ownZ = int32(int64(u.X)>>16), int32(int64(u.Z)>>16)
		info := table.Of(u.Def)
		if info == nil {
			continue
		}
		side = info.Side
		var best *UnitInfo
		for _, p := range info.Builds {
			if p.Role.Has(RoleExtractor) && (best == nil || p.Metal < best.Metal) {
				best = p
			}
		}
		if best != nil {
			fx, fz = best.FootX, best.FootZ
		}
		break
	}
	h.ex = executor{m: m, table: table, obs: &h.obs}
	h.rand = PlayerRand(m.BattleSeed, m.Player)
	h.kit = Kit{Me: m.Player, Side: side, Table: table, Persona: h.persona, Rand: &h.rand}
	h.kit.obs = &h.obs
	h.nextThink = h.onPhase(tick)
	// The feature word is the one part of a plot cell the analysis needs
	// that a battle rewrites, so it is read here; heights and metal are
	// fixed at map load and the preparation reads them itself. The parts of
	// the analysis that do not depend on this player's start are shared by
	// the battle's hosts (mapShared), so only the first preparation to reach
	// each one pays for it.
	t, starts := m.Terrain, m.StartPositions
	void := terrainVoid(t)
	shared := sharedAnalysis(m)
	// A restored battle's first controller continues its generator where
	// the save left it, after Init has drawn the style again from the seed
	// (ai.Manager.ResumeGenerator). It is taken here, on the simulation
	// thread, so a controller built after a later switch starts afresh.
	resume := m.ResumeGenerator
	m.ResumeGenerator = nil
	startEnemy := startEnemies(m)
	pp, _ := h.Probe.(PartProbe)
	player := m.Player
	h.start(func() {
		if pp != nil {
			pp.PrepBegin(player, tick)
			defer pp.PrepEnd(player, tick)
		}
		mapInfo := analyzeMap(shared, t, void, starts, fx, fz, ownX, ownZ)
		mapInfo.StartEnemy = startEnemy
		h.ex.mapInfo = mapInfo
		h.kit.Map = mapInfo
		h.brain.Init(&h.kit)
		if resume != nil {
			h.rand.SetPosition(*resume)
		}
	})
}

// startEnemies marks each start position an opponent took, when the battle
// published the start assignment (ai.Manager.StartOwners); nil otherwise.
// It reads the alliance table, so it runs on the simulation thread.
func startEnemies(m *ai.Manager) []bool {
	if m.StartOwners == nil || len(m.StartOwners) != len(m.StartPositions) {
		return nil
	}
	enemy := make([]bool, len(m.StartOwners))
	for i, p := range m.StartOwners {
		if p < 0 || p >= 10 || uint8(p) == m.Player {
			continue
		}
		enemy[i] = m.IsAlliance == nil || !m.IsAlliance(m.Player, uint8(p))
	}
	return enemy
}

// start runs job on a goroutine once the job in flight has finished.
func (h *Host) start(job func()) {
	prev := h.flight
	done := make(chan struct{})
	h.flight = done
	go func() {
		if prev != nil {
			<-prev
		}
		job()
		close(done)
	}()
}

// think runs one think with the batch parameters the simulation thread
// fixed when it took the observation.
func (h *Host) think(tick uint32, budget int32, probe bool) {
	h.kit.out = &h.b
	h.kit.Tick = tick
	h.kit.Budget = budget
	probe = probe && h.Probe != nil
	if probe {
		h.Probe.ThinkBegin(h.m.Player, h.obs.Tick)
	}
	h.brain.Think(&h.kit, &h.obs)
	h.Thinks++
	if probe {
		h.Probe.ThinkEnd(h.m.Player, h.obs.Tick)
	}
}

// Join waits for work in flight off the simulation thread (host-side
// readers that want a quiescent brain, e.g. the recorder calling Explain).
func (h *Host) Join() {
	if h.flight == nil {
		return
	}
	<-h.flight
	h.flight = nil
	h.ready = h.inited // the preparation heads every chain
}

// joinStep is Join on the simulation thread inside a step: a join that finds
// the worker still busy is reported to the part probe as a wait.
func (h *Host) joinStep(pp PartProbe, tick uint32) {
	if h.flight == nil {
		return
	}
	if pp != nil {
		select {
		case <-h.flight:
		default:
			h.Join()
			pp.Part(h.m.Player, tick, PartJoin)
			return
		}
	}
	h.Join()
}

// Step is the per-tick entry: apply a due batch, start a due think, then
// the engine upkeep the retail step would have run.
func (h *Host) Step(tick uint32, w *units.World, econ *economy.Service) {
	m := h.m
	eligible, decide := m.StepGates(econ)
	if !eligible {
		return
	}
	var pp PartProbe
	if h.Probe != nil {
		h.Probe.StepBegin(m.Player, tick)
		pp, _ = h.Probe.(PartProbe)
	}
	if decide && w != nil {
		if !h.inited {
			h.begin(tick, w)
			if pp != nil {
				pp.Part(m.Player, tick, PartBegin)
			}
		}
		if h.b.pending && tick >= h.b.due {
			h.joinStep(pp, tick)
			h.applyBatch(tick, w)
			if pp != nil {
				pp.Part(m.Player, tick, PartApply)
			}
		}
		if !h.b.pending && tick >= h.nextThink {
			h.buildObs(tick, w, econ)
			h.b.reset()
			h.b.due = tick + h.persona.Reaction
			h.b.pending = true
			budget := h.ex.projectBudget(tick, &h.persona)
			h.nextThink = h.onPhase(tick + 1)
			if pp != nil {
				pp.Part(m.Player, tick, PartObserve)
			}
			switch {
			case h.persona.Reaction == 0:
				// The batch applies now, so its think cannot wait.
				h.joinStep(pp, tick)
				h.think(tick, budget, true)
				h.applyBatch(tick, w)
			case h.persona.Async:
				h.start(func() { h.think(tick, budget, true) })
			case !h.ready:
				h.start(func() { h.think(tick, budget, false) })
			default:
				h.think(tick, budget, true)
			}
			if pp != nil {
				pp.Part(m.Player, tick, PartThink)
			}
		}
	}
	m.EngineUpkeep(tick, w)
	if h.Probe != nil {
		if pp != nil {
			pp.Part(m.Player, tick, PartUpkeep)
		}
		h.Probe.StepEnd(m.Player, tick)
	}
}

func (h *Host) applyBatch(tick uint32, w *units.World) {
	before := h.ex.stats
	h.ex.apply(&h.b, tick, w, &h.persona)
	after := h.ex.stats
	h.kit.Last = ApplyStats{
		Applied:    after.Applied - before.Applied,
		DroppedAPM: after.DroppedAPM - before.DroppedAPM,
		Stale:      after.Stale - before.Stale,
		Failed:     after.Failed - before.Failed,
	}
	for i := range after.Reasons {
		h.kit.Last.Reasons[i] = after.Reasons[i] - before.Reasons[i]
	}
	h.b.pending = false
}

// buildObs fills h.obs from the live world on the simulation thread. It is
// the only place a brain's information comes from.
func (h *Host) buildObs(tick uint32, w *units.World, econ *economy.Service) {
	m := h.m
	me := m.Player
	o := &h.obs
	ob := &h.ob
	o.Tick, o.Me = tick, me
	if int(me) < len(econ.Players) {
		p := &econ.Players[me]
		o.Metal = Res{Stock: int32(p.Stock[economy.Metal]), Cap: int32(p.Capacity[economy.Metal]), Income: int32(p.AIProduction[economy.Metal]), Expense: int32(p.AIConsumption[economy.Metal])}
		o.Energy = Res{Stock: int32(p.Stock[economy.Energy]), Cap: int32(p.Capacity[economy.Energy]), Income: int32(p.AIProduction[economy.Energy]), Expense: int32(p.AIConsumption[economy.Energy])}
	}
	var hostile [10]bool
	for i := 0; i < 10 && i < len(econ.Players); i++ {
		p := &econ.Players[i]
		o.Allied[i] = false
		if uint8(i) == me || !p.Exists || p.IsObserver {
			continue
		}
		allied := m.IsAlliance != nil && m.IsAlliance(me, uint8(i))
		o.Allied[i] = allied
		hostile[i] = !allied
	}
	o.WindPermille = int32(econ.WindScalar() * 1000)
	if lim, ok := m.Strategic.UnitLimit(); ok {
		o.UnitLimit = int32(lim)
	}
	o.UnitCount = int32(w.LiveCountForPlayer(int(me)))

	ob.walk = w.AppendLiveSliced(ob.walk[:0])
	maxH := 0
	for _, u := range ob.walk {
		if int(u.Handle) > maxH {
			maxH = int(u.Handle)
		}
	}
	ob.ensure(maxH + 1)
	for len(h.kit.ownIdx) < maxH+1 {
		h.kit.ownIdx = append(h.kit.ownIdx, -1)
	}
	h.kit.tags = ob.tags
	h.kit.inst = ob.inst
	for _, u := range ob.walk {
		ob.identify(u)
	}

	// Pass 1: own units and sensor coverage, and the jammers of every other
	// player the owner is not allied with.
	classes := orderClassTable()
	for i := range o.Own {
		h.kit.ownIdx[o.Own[i].H] = -1
	}
	o.Own = o.Own[:0]
	ob.sensors = ob.sensors[:0]
	ob.jammers = ob.jammers[:0]
	table := h.kit.Table
	for _, u := range ob.walk {
		if u.Owner != me {
			if u.Owner < 10 && hostile[u.Owner] {
				ob.addJammer(u)
			}
			continue
		}
		if u.Def == nil || u.Dying {
			continue
		}
		info := table.Of(u.Def)
		if info == nil {
			continue
		}
		hd := u.Handle
		gen := ob.gen[hd]
		if ob.tagGen[hd] != gen {
			ob.tags[hd] = 0 // a new unit in a recycled slot starts untagged
			ob.tagGen[hd] = gen
		}
		built, progress := buildProgress(u)
		class, qlen, targetKey := queueState(u, classes)
		var target *UnitInfo
		if targetKey != "" {
			target = table.Lookup(targetKey)
		}
		h.kit.ownIdx[hd] = int32(len(o.Own))
		o.Own = append(o.Own, OwnUnit{
			H: hd, Gen: gen, Info: info,
			X: fixedToWorld(int64(u.X)), Y: fixedToWorld(int64(u.Y)), Z: fixedToWorld(int64(u.Z)),
			HP: u.Health, MaxHP: u.MaxHealth, Built: built, Progress: progress,
			Order: class, QueueLen: qlen, Tag: ob.tags[hd], Target: target,
		})
		ob.addSensor(u)
	}
	// Pass 2: enemy contacts, and the allied units in sight. An allied unit
	// is tested with the same sight predicate as an enemy: a skirmish
	// ally's units only where the owner's own line of sight falls, a
	// Survival teammate's wherever the team's shared sight does
	// (docs/DESIGN_SURVIVAL.md §4.3). Allies do not carry radar blips: a
	// friendly unit out of sight is simply not listed.
	o.Enemy = o.Enemy[:0]
	o.Allies = o.Allies[:0]
	var sea numeric.Fixed
	if t := m.Terrain; t != nil {
		sea = t.SeaLevelWorld()
	}
	for _, u := range ob.walk {
		if u.Owner >= 10 || u.Def == nil || u.Dying {
			continue
		}
		if o.Allied[u.Owner] {
			if !h.persona.Omniscient && (m.UnitVisible == nil || !m.UnitVisible(me, u)) {
				continue
			}
			info := table.Of(u.Def)
			if info == nil {
				continue
			}
			built, progress := buildProgress(u)
			o.Allies = append(o.Allies, AllyUnit{
				H: u.Handle, Gen: ob.gen[u.Handle], Info: info, Owner: u.Owner,
				X: fixedToWorld(int64(u.X)), Z: fixedToWorld(int64(u.Z)),
				HP: u.Health, MaxHP: u.MaxHealth, Built: built, Progress: progress,
			})
			continue
		}
		if !hostile[u.Owner] {
			continue
		}
		visible := h.persona.Omniscient || (m.UnitVisible != nil && m.UnitVisible(me, u))
		x, z := fixedToWorld(int64(u.X)), fixedToWorld(int64(u.Z))
		if visible {
			pct := int32(100)
			if u.MaxHealth > 0 {
				pct = u.Health * 100 / u.MaxHealth
			}
			o.Enemy = append(o.Enemy, Contact{H: u.Handle, Gen: ob.gen[u.Handle], Info: table.Of(u.Def), Owner: u.Owner, X: x, Z: z, HPPct: pct, Visible: true, Built: u.Remaining == 0})
			continue
		}
		if ob.blip(u, sea) {
			o.Enemy = append(o.Enemy, Contact{Owner: u.Owner, X: x, Z: z, HPPct: 100})
		}
	}
	h.updateMemory(tick)
}

// memoryMobileTTL drops a remembered mobile unit after 60 s unseen.
const memoryMobileTTL = 1800

func (h *Host) updateMemory(tick uint32) {
	o := &h.obs
	ob := &h.ob
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info == nil {
			continue // a blip is not remembered and refreshes no record
		}
		idx := ob.memIndex[c.H]
		if idx >= 0 && int(idx) < len(o.Memory) && o.Memory[idx].H == c.H {
			r := &o.Memory[idx]
			// A different unit in the slot (Gen) takes the record over.
			r.Gen = c.Gen
			if r.Info != c.Info {
				r.Info = c.Info
				r.Building = !c.Info.Role.Has(RoleMobile)
			}
			r.X, r.Z, r.LastSeen, r.Owner = c.X, c.Z, tick, c.Owner
			continue
		}
		ob.memIndex[c.H] = int32(len(o.Memory))
		o.Memory = append(o.Memory, Remembered{H: c.H, Gen: c.Gen, Info: c.Info, Owner: c.Owner, X: c.X, Z: c.Z, LastSeen: tick, Building: !c.Info.Role.Has(RoleMobile)})
	}
	// Forget what we can now see is gone: a remembered position inside the
	// owner's current sight that produced no contact this pass. Mobile
	// memories also age out.
	probe := h.m.RallyProbeKnown
	for i := 0; i < len(o.Memory); {
		r := &o.Memory[i]
		if r.LastSeen == tick {
			i++
			continue
		}
		drop := !r.Building && tick-r.LastSeen > memoryMobileTTL
		if !drop && probe != nil {
			x := numeric.Fixed(int64(r.X) << 16)
			z := numeric.Fixed(int64(r.Z) << 16)
			var y numeric.Fixed
			if t := h.m.Terrain; t != nil {
				y = t.HeightAt(x, z)
			}
			drop = probe(h.m.Player, x, y, z)
		}
		if !drop {
			i++
			continue
		}
		last := len(o.Memory) - 1
		ob.memIndex[r.H] = -1
		if i != last {
			o.Memory[i] = o.Memory[last]
			ob.memIndex[o.Memory[i].H] = int32(i)
		}
		o.Memory = o.Memory[:last]
	}
}

// HostPlanner is the ai.Planner that runs the host a manager carries in Ext
// and falls back to the base mode's step for a manager without one. Every
// rule set that binds it derives from Modern, so the fallback is Modern's
// step, ai.ModernPlanner (the retail step with Modern wave air targets), not
// the bare retail step: a computer player without a brain plays exactly as it
// would under Modern itself. It is zero size, so a rule set may cache it
// (docs/DESIGN_GAMEPLAY_RULES.md §3).
type HostPlanner struct{}

// fallback is the step a manager without a host runs.
var fallback ai.ModernPlanner

// Step implements ai.Planner.
func (HostPlanner) Step(m *ai.Manager, tick uint32, w *units.World, econ *economy.Service) {
	switch ext := m.Ext.(type) {
	case *Host:
		if ext != nil {
			ext.Step(tick, w, econ)
			return
		}
	case *RetailTimer:
		if ext != nil && ext.Probe != nil {
			ext.Probe.StepBegin(m.Player, tick)
			fallback.Step(m, tick, w, econ)
			ext.Probe.StepEnd(m.Player, tick)
			return
		}
	}
	fallback.Step(m, tick, w, econ)
}

// ControlsModernAI implements ai.ModernAIStep: a manager the arena gave a
// brain (a Host in Ext) is a Modern AI player, and one without plays the
// fallback step, as a Classic player would.
func (HostPlanner) ControlsModernAI(m *ai.Manager) bool {
	h, ok := m.Ext.(*Host)
	return ok && h != nil
}

// RetailTimer lets host instrumentation time the fallback step (the
// computer player without a brain) through HostPlanner without changing it.
type RetailTimer struct{ Probe Probe }

// StepLazy is the body of a registered brain's zero-size planner: it creates
// the manager's host on first use with newHost, then steps it.
func StepLazy(m *ai.Manager, tick uint32, w *units.World, econ *economy.Service, newHost func(*ai.Manager) *Host) {
	h, ok := m.Ext.(*Host)
	if !ok || h == nil {
		h = newHost(m)
		m.Ext = h
	}
	h.Step(tick, w, econ)
}
