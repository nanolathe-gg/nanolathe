// Package core is the shared chassis every prototype brain is built on: a
// blackboard rebuilt from each observation, persistent per-unit tasks and
// squads, and four replaceable decision policies — economy (what builders
// build), production (what factories make), army (what combat units do) and
// strategy (posture: how much to invest in economy versus army, when to
// attack). Comparing brains that differ in one policy isolates that
// policy's effect.
//
// Everything here is deterministic integer code that reads only the
// observation, so a brain built on the chassis can think asynchronously.
package core

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Posture is the strategy policy's output, read by the other policies.
type Posture struct {
	// EcoShare 0..100: the share of spending the economy policy should aim
	// at economy (extractors, energy, factories) rather than army.
	EcoShare int32
	// Aggression 0..100: how readily the army attacks rather than defends.
	Aggression int32
	// AttackValue is the army value (metal-equivalent) at which to launch an
	// attack; 0 lets the army policy decide.
	AttackValue int32
	// Tech asks the economy to build the next tier of factory.
	Tech bool
	// Label names the current posture for visualization.
	Label string
}

// SpotState is what the brain believes about a metal spot.
type SpotState int8

const (
	SpotFree SpotState = iota
	SpotOurs
	SpotPlanned // a builder of ours is on its way
	SpotEnemy
	// SpotHeld is a spot where one of our placements met a building we
	// have not seen (HoldSpot): most likely an enemy extractor out of
	// sight. It stays held until an own unit has looked at the spot and
	// no enemy building is remembered there, or the hold lapses.
	SpotHeld
)

// Task is a persistent per-unit record: when the unit was last given
// work. It lives in Board.tasks indexed by handle and starts over when the
// slot holds a different unit.
type Task struct {
	Since uint32
	Def   *aikit.UnitInfo // the unit the task belongs to (slot reuse guard)
	Gen   uint32          // and its instance (aikit.OwnUnit.Gen)
}

// spotClaim is a builder sent to a metal spot: when, and which builder
// (handle and instance; 0 when the caller did not say).
type spotClaim struct {
	tick uint32
	h    pool.Handle
	gen  uint32
}

// claimTicks is how long a claim on a metal spot lasts at most.
const claimTicks = 1800

// Holds (HoldSpot). A hold lapses after holdTicks; before that it ends
// once an own unit has stood within holdLookR of the spot (close enough
// to see a building on it) with no enemy building remembered within
// holdBuildingR, but not in the first holdLookMin ticks: the builder
// whose placement failed may be standing beside the spot, and a building
// it cannot see from there must not be retried every think.
const (
	holdTicks     = 9000 // five minutes
	holdLookR     = 200
	holdLookMin   = 900
	holdBuildingR = 48
)

// Board is the blackboard: derived facts for one think plus the state that
// persists between thinks.
type Board struct {
	K    *aikit.Kit
	O    *aikit.Obs
	Tick uint32
	Me   uint8

	// Own units by role: indices into O.Own, rebuilt every think.
	Commander   int32
	Builders    []int32 // built mobile builders, including the commander
	Factories   []int32 // built factories
	Combat      []int32 // built mobile armed non-builders
	Defenses    []int32
	Frames      []int32 // own nanoframes (under construction)
	Eco         []int32 // extractors, energy, makers, storage (built)
	Radars      int32
	Extractors  int32
	Energies    int32
	Constructor int32 // mobile builders other than the commander

	// Economy.
	Metal, Energy aikit.Res
	BuildPower    int32
	ArmyValue     int32
	ArmyStrength  int64

	// Places.
	HomeX, HomeZ   int32
	EnemyX, EnemyZ int32 // best current guess of the enemy's base
	EnemyKnown     bool  // based on sighted buildings rather than a start position
	RallyX, RallyZ int32

	// Enemy picture from contacts and memory.
	EnemyGround, EnemyAir, EnemyNaval, EnemyDefense int64 // strength
	EnemyArmyValue                                  int32
	EnemyBuildings                                  int32
	NearHomeThreat                                  int64 // enemy strength within the base radius
	ThreatX, ThreatZ                                int32 // centroid of the nearest threat to home

	// Influence grids (sector resolution), rebuilt every think.
	Threat     *aikit.Grid // enemy DPS reach
	EnemyValue *aikit.Grid // enemy value (targets)
	OwnPower   *aikit.Grid // own armed strength

	// Spots.
	Spots []SpotState

	Posture Posture

	// Persistent state.
	tasks   []Task
	claims  []spotClaim // spot → the builder sent there (tick 0 = none)
	held    []uint32    // spot → tick its hold began (0 = none; HoldSpot)
	homeSet bool
	// startCleared marks start positions an own unit has looked at without
	// finding the enemy there.
	startCleared []bool
	unitIndex    []int32 // handle → index in O.Own for this think
	// indexed lists the handles unitIndex holds this think, so the next
	// think resets those instead of every slot.
	indexed []pool.Handle
}

// BaseRadius is the distance from home counted as "the base".
const BaseRadius = 1100

// TaskOf returns the task of an own unit; a slot that holds a different
// unit (another definition or instance) starts over.
func (b *Board) TaskOf(i int32) *Task {
	u := &b.O.Own[i]
	h := int(u.H)
	for len(b.tasks) <= h {
		b.tasks = append(b.tasks, Task{})
	}
	t := &b.tasks[h]
	if t.Def != u.Info || t.Gen != u.Gen {
		*t = Task{Def: u.Info, Gen: u.Gen}
	}
	return t
}

// Index returns the O.Own index of a handle this think, or -1.
func (b *Board) Index(h pool.Handle) int32 {
	if int(h) < len(b.unitIndex) {
		return b.unitIndex[h]
	}
	return -1
}

// Update rebuilds the derived facts from a fresh observation.
func (b *Board) Update(k *aikit.Kit, o *aikit.Obs) {
	b.K, b.O, b.Tick, b.Me = k, o, o.Tick, o.Me
	b.Metal, b.Energy = o.Metal, o.Energy
	m := k.Map
	if b.Threat == nil {
		b.Threat = aikit.NewGrid(m)
		b.EnemyValue = aikit.NewGrid(m)
		b.OwnPower = aikit.NewGrid(m)
		b.Spots = make([]SpotState, len(m.Spots))
		b.claims = make([]spotClaim, len(m.Spots))
		b.held = make([]uint32, len(m.Spots))
	}
	b.Commander = -1
	b.Builders = b.Builders[:0]
	b.Factories = b.Factories[:0]
	b.Combat = b.Combat[:0]
	b.Defenses = b.Defenses[:0]
	b.Frames = b.Frames[:0]
	b.Eco = b.Eco[:0]
	b.Radars, b.Extractors, b.Energies, b.Constructor = 0, 0, 0, 0
	b.BuildPower, b.ArmyValue, b.ArmyStrength = 0, 0, 0
	for _, h := range b.indexed {
		b.unitIndex[h] = -1
	}
	b.indexed = b.indexed[:0]
	b.OwnPower.Clear()
	for i := range o.Own {
		u := &o.Own[i]
		h := int(u.H)
		for len(b.unitIndex) <= h {
			b.unitIndex = append(b.unitIndex, -1)
		}
		b.unitIndex[h] = int32(i)
		b.indexed = append(b.indexed, u.H)
		r := u.Info.Role
		if !u.Built {
			b.Frames = append(b.Frames, int32(i))
			continue
		}
		switch {
		case r.Has(aikit.RoleCommander):
			b.Commander = int32(i)
			b.Builders = append(b.Builders, int32(i))
			b.BuildPower += u.Info.BuildPower
		case r.Has(aikit.RoleBuilder):
			b.Builders = append(b.Builders, int32(i))
			b.BuildPower += u.Info.BuildPower
			b.Constructor++
		case r.Has(aikit.RoleFactory):
			b.Factories = append(b.Factories, int32(i))
			b.BuildPower += u.Info.BuildPower
		case r.Has(aikit.RoleCombat):
			b.Combat = append(b.Combat, int32(i))
			b.ArmyValue += u.Info.Value
			b.ArmyStrength += u.Info.Strength()
			b.OwnPower.AddDisc(u.X, u.Z, u.Info.Range+aikit.SectorWorld, int32(u.Info.Strength()/64+1))
		case r.Has(aikit.RoleDefense):
			b.Defenses = append(b.Defenses, int32(i))
			b.OwnPower.AddDisc(u.X, u.Z, u.Info.Range, int32(u.Info.Strength()/64+1))
		}
		if r.Any(aikit.RoleExtractor | aikit.RoleEnergy | aikit.RoleMetalMaker | aikit.RoleStorage) {
			b.Eco = append(b.Eco, int32(i))
		}
		if r.Has(aikit.RoleExtractor) {
			b.Extractors++
		}
		if r.Has(aikit.RoleEnergy) {
			b.Energies++
		}
		if r.Has(aikit.RoleRadar) {
			b.Radars++
		}
	}
	if !b.homeSet && b.Commander >= 0 {
		c := &o.Own[b.Commander]
		b.HomeX, b.HomeZ = c.X, c.Z
		b.homeSet = true
	}
	b.updateEnemy()
	b.updateSpots()
	// Rally toward the enemy: a quarter of the way, but at least 700 world
	// units out (clear of the factories' pads) and at most half way.
	d := aikit.Dist(b.HomeX, b.HomeZ, b.EnemyX, b.EnemyZ)
	r := d / 4
	if r < 700 {
		r = 700
	}
	if r > d/2 {
		r = d / 2
	}
	if d > 0 {
		b.RallyX = b.HomeX + int32(int64(b.EnemyX-b.HomeX)*int64(r)/int64(d))
		b.RallyZ = b.HomeZ + int32(int64(b.EnemyZ-b.HomeZ)*int64(r)/int64(d))
	} else {
		b.RallyX, b.RallyZ = b.HomeX, b.HomeZ
	}
}

// updateEnemy rebuilds the enemy picture and the threat/value grids from
// contacts and memory. Memory already includes every current contact that
// was identified, so the walk is over memory plus radar-only blips.
func (b *Board) updateEnemy() {
	o := b.O
	b.Threat.Clear()
	b.EnemyValue.Clear()
	b.EnemyGround, b.EnemyAir, b.EnemyNaval, b.EnemyDefense = 0, 0, 0, 0
	b.EnemyArmyValue, b.EnemyBuildings = 0, 0
	b.NearHomeThreat = 0
	var bx, bz, bn int64
	var tx, tz, tw int64
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		s := info.Strength()
		if info.DPS > 0 {
			reach := info.Range + aikit.SectorWorld
			if info.Role.Has(aikit.RoleMobile) {
				reach += info.Speed * 2
			}
			b.Threat.AddDisc(r.X, r.Z, reach, int32(s/64+1))
		}
		b.EnemyValue.AddDisc(r.X, r.Z, 0, info.Value)
		switch {
		case info.Role.Has(aikit.RoleDefense):
			b.EnemyDefense += s
		case info.Role.Has(aikit.RoleAir) && info.Role.Has(aikit.RoleCombat):
			b.EnemyAir += s
			b.EnemyArmyValue += info.Value
		case info.Role.Has(aikit.RoleNaval) && info.Role.Has(aikit.RoleCombat):
			b.EnemyNaval += s
			b.EnemyArmyValue += info.Value
		case info.Role.Has(aikit.RoleCommander):
			b.EnemyGround += s // its fighting strength counts, its build cost is not an army
		case info.Role.Has(aikit.RoleCombat):
			b.EnemyGround += s
			b.EnemyArmyValue += info.Value
		}
		if r.Building {
			b.EnemyBuildings++
			bx += int64(r.X)
			bz += int64(r.Z)
			bn++
		}
		if info.DPS > 0 && aikit.Dist2(r.X, r.Z, b.HomeX, b.HomeZ) < BaseRadius*BaseRadius {
			if info.Role.Has(aikit.RoleMobile) {
				b.NearHomeThreat += s
				w := s/64 + 1
				tx += int64(r.X) * w
				tz += int64(r.Z) * w
				tw += w
			}
		}
	}
	// Radar blips of unknown type count as a modest ground threat.
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info != nil {
			continue
		}
		b.Threat.AddDisc(c.X, c.Z, 3*aikit.SectorWorld, 40)
		if aikit.Dist2(c.X, c.Z, b.HomeX, b.HomeZ) < BaseRadius*BaseRadius {
			b.NearHomeThreat += 2000
			tx += int64(c.X) * 32
			tz += int64(c.Z) * 32
			tw += 32
		}
	}
	if tw > 0 {
		b.ThreatX, b.ThreatZ = int32(tx/tw), int32(tz/tw)
	}
	if bn > 0 {
		b.EnemyX, b.EnemyZ = int32(bx/bn), int32(bz/bn)
		b.EnemyKnown = true
		return
	}
	b.EnemyKnown = false
	// No enemy building seen: the nearest start position nobody of ours has
	// looked at yet. A start is cleared once an own unit has stood near it
	// (no enemy building has been seen anywhere, so it held none). With the
	// start assignment public, only an opponent's start is a candidate
	// (MapInfo.StartEnemy).
	m := b.K.Map
	for len(b.startCleared) < len(m.Starts) {
		b.startCleared = append(b.startCleared, false)
	}
	best := int64(-1)
	var far int64 = -1
	var farX, farZ int32
	for si, s := range m.Starts {
		d := aikit.Dist2(s[0], s[1], b.HomeX, b.HomeZ)
		if d < 400*400 || !m.MaybeEnemyStart(si) {
			continue // our own start, or one no opponent took
		}
		if d > far {
			far, farX, farZ = d, s[0], s[1]
		}
		if !b.startCleared[si] {
			for i := range o.Own {
				u := &o.Own[i]
				if u.Info.Role.Has(aikit.RoleMobile) && aikit.Dist2(u.X, u.Z, s[0], s[1]) < 350*350 {
					b.startCleared[si] = true
					break
				}
			}
		}
		if b.startCleared[si] {
			continue
		}
		if best < 0 || d < best {
			best = d
			b.EnemyX, b.EnemyZ = s[0], s[1]
		}
	}
	if best < 0 {
		if far >= 0 {
			b.EnemyX, b.EnemyZ = farX, farZ
		} else {
			b.EnemyX, b.EnemyZ = m.WorldW-b.HomeX, m.WorldH-b.HomeZ
		}
	}
}

// updateSpots labels each metal spot free, ours, planned, held or enemy.
// A claim is released when its builder is gone (dead, or its slot holds
// another unit), or after claimTicks; a hold as HoldSpot says. Ours and
// enemy (an extractor standing there, or remembered) override both.
func (b *Board) updateSpots() {
	m := b.K.Map
	o := b.O
	for i := range b.Spots {
		b.Spots[i] = SpotFree
		c := &b.claims[i]
		if c.tick == 0 {
			continue
		}
		if b.Tick-c.tick >= claimTicks || (c.h != 0 && !b.alive(c.h, c.gen)) {
			*c = spotClaim{}
			continue
		}
		b.Spots[i] = SpotPlanned
	}
	for i, t := range b.held {
		if t == 0 {
			continue
		}
		if b.Tick-t >= holdTicks || (b.Tick-t >= holdLookMin && b.lookedAt(i)) {
			b.held[i] = 0
			continue
		}
		b.Spots[i] = SpotHeld
	}
	mark := func(x, z int32, s SpotState) {
		for i := range m.Spots {
			sp := &m.Spots[i]
			if aikit.Dist2(sp.X, sp.Z, x, z) <= 40*40 {
				b.Spots[i] = s
				if s == SpotOurs {
					b.claims[i] = spotClaim{}
					b.held[i] = 0
				}
				return
			}
		}
	}
	for i := range o.Own {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleExtractor) {
			mark(u.X, u.Z, SpotOurs)
		}
	}
	for i := range o.Memory {
		r := &o.Memory[i]
		if r.Info.Role.Has(aikit.RoleExtractor) {
			mark(r.X, r.Z, SpotEnemy)
		}
	}
}

// lookedAt reports whether an own unit stands within holdLookR of spot i
// while no enemy building is remembered on it: had a building stood
// there, that unit would have seen it.
func (b *Board) lookedAt(i int) bool {
	sp := &b.K.Map.Spots[i]
	o := b.O
	for j := range o.Memory {
		r := &o.Memory[j]
		if r.Building && aikit.Dist2(r.X, r.Z, sp.X, sp.Z) <= holdBuildingR*holdBuildingR {
			return false
		}
	}
	for j := range o.Own {
		u := &o.Own[j]
		if u.Built && aikit.Dist2(u.X, u.Z, sp.X, sp.Z) <= holdLookR*holdLookR {
			return true
		}
	}
	return false
}

// HoldSpot records that a placement at spot i met a building we have not
// seen: the spot reads SpotHeld (not free, not planned) until an own unit
// has looked at it and no enemy building is remembered there, or for
// holdTicks at most. A remembered enemy extractor or one of ours on the
// spot overrides the hold as usual.
func (b *Board) HoldSpot(i int32) {
	if i < 0 || int(i) >= len(b.held) {
		return
	}
	t := b.Tick
	if t == 0 {
		t = 1 // tick 0 marks no hold
	}
	b.held[i] = t
	b.claims[i] = spotClaim{}
	b.Spots[i] = SpotHeld
}

// PlanSpot records that a builder was sent to a spot, without saying
// which: the claim lasts claimTicks. ClaimSpot names the builder, so the
// claim is released as soon as that builder dies.
func (b *Board) PlanSpot(i int32) { b.ClaimSpot(i, 0) }

// ClaimSpot records that builder (an own unit's handle this think) was
// sent to spot i. The spot stays planned while the builder lives, at most
// claimTicks.
func (b *Board) ClaimSpot(i int32, builder pool.Handle) {
	if i < 0 || int(i) >= len(b.claims) {
		return
	}
	c := spotClaim{tick: b.Tick}
	if j := b.Index(builder); builder != 0 && j >= 0 {
		c.h, c.gen = builder, b.O.Own[j].Gen
	}
	if c.tick == 0 {
		c.tick = 1 // tick 0 marks no claim
	}
	b.claims[i] = c
	b.Spots[i] = SpotPlanned
}

// alive reports whether the own unit h of instance gen is in this think's
// observation.
func (b *Board) alive(h pool.Handle, gen uint32) bool {
	j := b.Index(h)
	return j >= 0 && b.O.Own[j].Gen == gen
}

// NearestFreeSpot returns the free spot nearest (x, z) whose threat is at
// most maxThreat and that is within maxDist, preferring richer spots by
// discounting distance with metal. -1 when none.
func (b *Board) NearestFreeSpot(x, z, maxDist, maxThreat int32, water bool) int32 {
	m := b.K.Map
	best := int32(-1)
	var bestScore int64
	for i := range m.Spots {
		if b.Spots[i] != SpotFree {
			continue
		}
		sp := &m.Spots[i]
		if sp.Water && !water {
			continue
		}
		d := aikit.Dist(sp.X, sp.Z, x, z)
		if d > maxDist {
			continue
		}
		if b.Threat.At(sp.X, sp.Z) > maxThreat {
			continue
		}
		// Score: distance penalised, metal rewarded.
		score := int64(d)*100/int64(sp.Metal+1) + int64(d)
		if best < 0 || score < bestScore {
			best, bestScore = int32(i), score
		}
	}
	return best
}

// IdleBuilders appends builders with no productive order and no live task.
func (b *Board) IdleBuilders(dst []int32) []int32 {
	for _, i := range b.Builders {
		u := &b.O.Own[i]
		if u.Order == aikit.OrderIdle {
			dst = append(dst, i)
		}
	}
	return dst
}

// BestProduct returns the product of builder with role want that maximizes
// score (nil when none). Ties keep the first in authored order.
func BestProduct(builder *aikit.UnitInfo, want aikit.Role, score func(p *aikit.UnitInfo) int64) *aikit.UnitInfo {
	var best *aikit.UnitInfo
	var bestScore int64
	for _, p := range builder.Builds {
		if !p.Role.Has(want) {
			continue
		}
		s := score(p)
		if best == nil || s > bestScore {
			best, bestScore = p, s
		}
	}
	return best
}

// ConstructorScore ranks a mobile builder as an economy constructor: it
// must build extractors or energy (a minelayer does not), then build power
// per cost decides. Non-positive means unsuitable.
func ConstructorScore(q *aikit.UnitInfo) int64 {
	var eco int64
	for _, p := range q.Builds {
		if p.Role.Any(aikit.RoleExtractor | aikit.RoleEnergy | aikit.RoleFactory) {
			eco++
		}
	}
	if eco == 0 {
		return -1
	}
	return eco*100 + int64(q.BuildPower)*1000/int64(q.Value+1)
}

// CountOwn counts own units (built or not) of a definition.
func (b *Board) CountOwn(info *aikit.UnitInfo) int32 {
	var n int32
	for i := range b.O.Own {
		if b.O.Own[i].Info == info {
			n++
		}
	}
	return n
}

// CountRole counts own units (built or not) with every bit of role.
func (b *Board) CountRole(role aikit.Role) int32 {
	var n int32
	for i := range b.O.Own {
		if b.O.Own[i].Info.Role.Has(role) {
			n++
		}
	}
	return n
}

// Handles collects the handles of the given own-unit indices into dst.
func (b *Board) Handles(dst []pool.Handle, idx []int32) []pool.Handle {
	for _, i := range idx {
		dst = append(dst, b.O.Own[i].H)
	}
	return dst
}

// Centroid of the given own units.
func (b *Board) Centroid(idx []int32) (int32, int32) {
	if len(idx) == 0 {
		return b.HomeX, b.HomeZ
	}
	var x, z int64
	for _, i := range idx {
		x += int64(b.O.Own[i].X)
		z += int64(b.O.Own[i].Z)
	}
	return int32(x / int64(len(idx))), int32(z / int64(len(idx)))
}

// Minutes is the game time in whole minutes.
func (b *Board) Minutes() int32 { return int32(b.Tick / 1800) }
