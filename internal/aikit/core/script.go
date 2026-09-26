package core

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// ---------------------------------------------------------------------------
// ScriptStrategy: posture as a fixed function of game time.

// ScriptStrategy shifts from economy to army on a fixed clock.
type ScriptStrategy struct{}

func (ScriptStrategy) Init(*Board) {}

func (ScriptStrategy) Plan(b *Board) {
	min := b.Minutes()
	eco := 70 - min*3
	if eco < 35 {
		eco = 35
	}
	b.Posture = Posture{EcoShare: eco, Aggression: 50, AttackValue: 700 + 180*min, Label: "scripted clock"}
}

// ---------------------------------------------------------------------------
// ScriptEconomy: an ordered rule list per idle builder.

// ScriptEconomy assigns each idle builder the first rule that applies:
// help a nanoframe, first factory, energy when short, the nearest safe metal
// spot, more factories when metal piles up, base defense under threat, a
// radar, a metal maker on surplus energy, otherwise assist a factory.
type ScriptEconomy struct {
	idle []int32
}

func (e *ScriptEconomy) Init(*Board) {}

// Energy efficiency: energy per second per metal-equivalent cost.
func energyScore(p *aikit.UnitInfo) int64 {
	if p.WindGen > 0 || p.TidalGen > 0 {
		return 0 // variable output; the scripted economy avoids it
	}
	return int64(p.EnergyMake) * 1000 / int64(p.Value+1)
}

func factoryScore(p *aikit.UnitInfo) int64 {
	var combat int64
	for _, q := range p.Builds {
		if q.Role.Has(aikit.RoleCombat) && !q.Role.Has(aikit.RoleNaval) {
			combat++
		}
	}
	if p.Role.Has(aikit.RoleNaval) || combat == 0 {
		return -1
	}
	return combat*1000 - int64(p.Value)
}

func (e *ScriptEconomy) Plan(b *Board) {
	o := b.O
	e.idle = b.IdleBuilders(e.idle[:0])
	if len(e.idle) == 0 {
		return
	}
	min := b.Minutes()
	var factoryFrames, energyFrames int32
	for _, i := range b.Frames {
		r := o.Own[i].Info.Role
		if r.Has(aikit.RoleFactory) {
			factoryFrames++
		}
		if r.Has(aikit.RoleEnergy) {
			energyFrames++
		}
	}
	plannedFactory, plannedEnergy, plannedDefense, plannedRadar := int32(0), int32(0), int32(0), int32(0)
	metalIn, energyIn := b.Metal.Income, b.Energy.Income
	for _, i := range e.idle {
		u := &o.Own[i]
		info := u.Info
		isCom := info.Role.Has(aikit.RoleCommander)
		task := b.TaskOf(i)
		// A builder given work in the last five seconds that is idle again
		// probably had it refused; wait before trying again rather than
		// spending an action every think.
		if task.Since != 0 && b.Tick-task.Since < 150 {
			continue
		}
		if b.K.Budget >= 0 && int32(b.K.Emitted()) >= b.K.Budget/2 {
			break // leave half of the action budget for the other layers
		}
		// 1. A nanoframe of a building nearby that is not progressing: help.
		if f := e.frameToHelp(b, u); f >= 0 {
			var one [1]pool.Handle
			one[0] = u.H
			b.K.Repair(one[:], o.Own[f].H, false)
			task.Since = b.Tick
			continue
		}
		factories := int32(len(b.Factories)) + factoryFrames + plannedFactory
		// 2. First factory once a little metal is flowing.
		if factories == 0 && (b.Extractors >= 2 || b.Tick > 2700) {
			if p := BestProduct(info, aikit.RoleFactory, factoryScore); p != nil && factoryScore(p) > 0 {
				e.build(b, u, p, b.HomeX, b.HomeZ, -1, 3)
				plannedFactory++
				continue
			}
		}
		// 3. Energy when short.
		needE := energyIn*10 < metalIn*150 || b.Energy.Stock*5 < b.Energy.Cap
		if needE && energyFrames+plannedEnergy < 1+int32(len(b.Builders))/3 {
			if p := BestProduct(info, aikit.RoleEnergy, energyScore); p != nil && energyScore(p) > 0 {
				e.build(b, u, p, b.HomeX, b.HomeZ, -1, 1)
				plannedEnergy++
				continue
			}
		}
		// 4. Nearest safe metal spot.
		reach := int32(3000)
		if isCom {
			reach = 900
		}
		if mex := BestProduct(info, aikit.RoleExtractor, func(p *aikit.UnitInfo) int64 { return -int64(p.Value) }); mex != nil {
			if s := b.NearestFreeSpot(u.X, u.Z, reach, 8, false); s >= 0 {
				sp := &b.K.Map.Spots[s]
				e.build(b, u, mex, sp.X, sp.Z, s, 0)
				b.ClaimSpot(s, u.H)
				continue
			}
		}
		// 5. More factories when metal piles up.
		if b.Metal.Stock*10 > b.Metal.Cap*6 && factories < 1+min/5 && factories < 4 {
			if p := BestProduct(info, aikit.RoleFactory, factoryScore); p != nil && factoryScore(p) > 0 {
				e.build(b, u, p, b.HomeX, b.HomeZ, -1, 3)
				plannedFactory++
				continue
			}
		}
		// 6. Base defense under threat.
		if b.NearHomeThreat > 0 && int32(len(b.Defenses))+plannedDefense < 2+min/4 {
			if p := BestProduct(info, aikit.RoleDefense, func(p *aikit.UnitInfo) int64 {
				if p.Depth > 1 || p.Role.Has(aikit.RoleAntiAir) && p.DPS == p.AirDPS {
					return -1
				}
				return p.Strength() * 100 / int64(p.Value+1)
			}); p != nil {
				x := (b.HomeX*2 + b.ThreatX) / 3
				z := (b.HomeZ*2 + b.ThreatZ) / 3
				e.build(b, u, p, x, z, -1, 1)
				plannedDefense++
				continue
			}
		}
		// 7. One radar after three minutes.
		if b.Radars+plannedRadar == 0 && min >= 3 {
			if p := BestProduct(info, aikit.RoleRadar, func(p *aikit.UnitInfo) int64 {
				if p.Role.Has(aikit.RoleMobile) {
					return -1
				}
				return -int64(p.Value)
			}); p != nil {
				e.build(b, u, p, b.RallyX, b.RallyZ, -1, 1)
				plannedRadar++
				continue
			}
		}
		// 8. Metal maker on surplus energy.
		if b.Energy.Stock*10 > b.Energy.Cap*9 && energyIn > metalIn*25+100 {
			if p := BestProduct(info, aikit.RoleMetalMaker, func(p *aikit.UnitInfo) int64 { return -int64(p.Value) }); p != nil {
				e.build(b, u, p, b.HomeX, b.HomeZ, -1, 1)
				continue
			}
		}
		// 9. Extra energy is never wasted.
		if energyFrames+plannedEnergy == 0 {
			if p := BestProduct(info, aikit.RoleEnergy, energyScore); p != nil && energyScore(p) > 0 {
				e.build(b, u, p, b.HomeX, b.HomeZ, -1, 1)
				plannedEnergy++
				continue
			}
		}
		// 10. Assist the nearest factory.
		if f := nearestOf(b, b.Factories, u.X, u.Z); f >= 0 {
			var one [1]pool.Handle
			one[0] = u.H
			b.K.Guard(one[:], o.Own[f].H, false)
			task.Since = b.Tick
		}
	}
}

func (e *ScriptEconomy) build(b *Board, u *aikit.OwnUnit, p *aikit.UnitInfo, x, z, spot, spacing int32) {
	b.K.Build(u.H, p, x, z, spot, spacing, false)
	b.TaskOf(b.Index(u.H)).Since = b.Tick
}

// frameToHelp picks an own building nanoframe within 700 of u.
func (e *ScriptEconomy) frameToHelp(b *Board, u *aikit.OwnUnit) int32 {
	best := int32(-1)
	var bestD int64
	for _, i := range b.Frames {
		f := &b.O.Own[i]
		if f.Info.Role.Has(aikit.RoleMobile) {
			continue
		}
		d := aikit.Dist2(f.X, f.Z, u.X, u.Z)
		if d > 700*700 {
			continue
		}
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// nearFactoryPad reports whether a point sits on or in front of an own
// factory, where an idle unit blocks production.
func nearFactoryPad(b *Board, x, z int32) bool {
	for _, i := range b.Factories {
		f := &b.O.Own[i]
		if aikit.Dist2(f.X, f.Z+f.Info.FootZ*8, x, z) < 200*200 {
			return true
		}
	}
	return false
}

func nearestOf(b *Board, idx []int32, x, z int32) int32 {
	best := int32(-1)
	var bestD int64
	for _, i := range idx {
		u := &b.O.Own[i]
		d := aikit.Dist2(u.X, u.Z, x, z)
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// ScriptProduction: constructors first, one scout, then a rotating mix.

// ScriptProduction keeps each factory's queue two deep: constructors up to a
// clock-based target, one scout, then the three most cost-efficient combat
// products in rotation, with anti-air once enemy aircraft are seen.
type ScriptProduction struct {
	turn int32
}

func (p *ScriptProduction) Init(*Board) {}

func combatEfficiency(q *aikit.UnitInfo) int64 {
	if !q.Role.Has(aikit.RoleCombat) || q.Role.Has(aikit.RoleScout) || q.Role.Has(aikit.RoleKamikaze) {
		return -1
	}
	return q.Strength() * 100 / int64(q.Value+1)
}

func (p *ScriptProduction) Plan(b *Board) {
	o := b.O
	min := b.Minutes()
	wantCons := 2 + min/4
	if wantCons > 6 {
		wantCons = 6
	}
	cons := b.Constructor
	for _, i := range b.Frames {
		if o.Own[i].Info.Role.Has(aikit.RoleBuilder) {
			cons++
		}
	}
	scouts := b.CountRole(aikit.RoleScout)
	var aa int32
	for _, i := range b.Combat {
		if o.Own[i].Info.Role.Has(aikit.RoleAntiAir) {
			aa++
		}
	}
	for _, fi := range b.Factories {
		f := &o.Own[fi]
		if f.QueueLen >= 2 {
			continue
		}
		info := f.Info
		if cons < wantCons {
			if q := BestProduct(info, aikit.RoleBuilder, ConstructorScore); q != nil && ConstructorScore(q) > 0 {
				b.K.Produce(f.H, q, 1)
				cons++
				continue
			}
		}
		if scouts == 0 && min >= 1 {
			if q := BestProduct(info, aikit.RoleScout, func(q *aikit.UnitInfo) int64 { return int64(q.Speed)*10 - int64(q.Value) }); q != nil {
				b.K.Produce(f.H, q, 1)
				scouts++
				continue
			}
		}
		if b.EnemyAir > 0 && aa*4 < int32(len(b.Combat)) {
			if q := BestProduct(info, aikit.RoleAntiAir, func(q *aikit.UnitInfo) int64 {
				if !q.Role.Has(aikit.RoleCombat) {
					return -1
				}
				return int64(q.AirDPS) * 1000 / int64(q.Value+1)
			}); q != nil {
				b.K.Produce(f.H, q, 1)
				aa++
				continue
			}
		}
		// Rotate over the three most efficient combat products.
		var top [3]*aikit.UnitInfo
		var topS [3]int64
		for _, q := range info.Builds {
			s := combatEfficiency(q)
			if s < 0 {
				continue
			}
			for k := 0; k < 3; k++ {
				if top[k] == nil || s > topS[k] {
					copy(top[k+1:], top[k:2])
					copy(topS[k+1:], topS[k:2])
					top[k], topS[k] = q, s
					break
				}
			}
		}
		n := int32(0)
		for n < 3 && top[n] != nil {
			n++
		}
		if n == 0 {
			continue
		}
		p.turn++
		b.K.Produce(f.H, top[p.turn%n], 2)
	}
}

// ---------------------------------------------------------------------------
// WaveArmy: gather at the rally point, attack at a threshold, retreat when
// the wave is spent, and turn everyone around when the base is threatened.

const (
	tagRally   int32 = 0
	tagAttack  int32 = 1
	tagDefend  int32 = 2
	tagScout   int32 = 3
	tagRetreat int32 = 4
)

// WaveArmy is the threshold-wave baseline.
type WaveArmy struct {
	launch       int64
	attackX      int32
	attackZ      int32
	waves        int32
	scoutTarget  int32
	buf          []pool.Handle
	rally        []int32
	attack       []int32
	defend       []int32
	lastDecision string
}

func (a *WaveArmy) Init(*Board) {}

func (a *WaveArmy) Plan(b *Board) {
	o := b.O
	k := b.K
	a.rally, a.attack, a.defend = a.rally[:0], a.attack[:0], a.defend[:0]
	var rallyValue int32
	var attackStrength int64
	for _, i := range b.Combat {
		u := &o.Own[i]
		if u.Info.Role.Has(aikit.RoleScout) {
			a.scout(b, u)
			continue
		}
		switch u.Tag {
		case tagAttack:
			a.attack = append(a.attack, i)
			attackStrength += u.Info.Strength()
		case tagDefend:
			a.defend = append(a.defend, i)
		default:
			a.rally = append(a.rally, i)
			rallyValue += u.Info.Value
		}
	}
	// Defense first.
	if b.NearHomeThreat > 0 {
		a.buf = a.buf[:0]
		for _, i := range a.rally {
			a.buf = append(a.buf, o.Own[i].H)
			k.SetTag(o.Own[i].H, tagDefend)
		}
		for _, i := range a.defend {
			if o.Own[i].Order == aikit.OrderIdle {
				a.buf = append(a.buf, o.Own[i].H)
			}
		}
		if len(a.buf) > 0 {
			k.Patrol(a.buf, b.ThreatX, b.ThreatZ, false)
		}
		a.lastDecision = "defend base"
		a.rally = a.rally[:0]
	} else if len(a.defend) > 0 {
		a.buf = b.Handles(a.buf[:0], a.defend)
		for _, h := range a.buf {
			k.SetTag(h, tagRally)
		}
		k.Move(a.buf, b.RallyX, b.RallyZ, false)
	}
	// Launch a wave.
	threshold := b.Posture.AttackValue
	if threshold <= 0 {
		threshold = 1000
	}
	if len(a.rally) > 0 && rallyValue >= threshold {
		a.buf = b.Handles(a.buf[:0], a.rally)
		for _, h := range a.buf {
			k.SetTag(h, tagAttack)
		}
		a.attackX, a.attackZ = a.target(b)
		k.Patrol(a.buf, a.attackX, a.attackZ, false)
		a.launch = attackStrength
		for _, i := range a.rally {
			a.launch += o.Own[i].Info.Strength()
		}
		a.waves++
		a.lastDecision = fmt.Sprintf("wave %d launched at value %d", a.waves, rallyValue)
		a.rally = a.rally[:0]
	} else {
		// Gather idle stragglers at the rally point.
		a.buf = a.buf[:0]
		for _, i := range a.rally {
			u := &o.Own[i]
			if u.Order == aikit.OrderIdle && (aikit.Dist2(u.X, u.Z, b.RallyX, b.RallyZ) > 350*350 || nearFactoryPad(b, u.X, u.Z)) {
				a.buf = append(a.buf, u.H)
			}
		}
		if len(a.buf) > 0 {
			k.Move(a.buf, b.RallyX, b.RallyZ, false)
		}
	}
	// Maintain the wave: retreat when spent, re-target when idle.
	if len(a.attack) > 0 {
		if a.launch > 0 && attackStrength*100 < a.launch*30 {
			a.buf = b.Handles(a.buf[:0], a.attack)
			for _, h := range a.buf {
				k.SetTag(h, tagRally)
			}
			k.Move(a.buf, b.RallyX, b.RallyZ, false)
			a.lastDecision = "wave spent: retreat"
			a.launch = 0
		} else {
			a.buf = a.buf[:0]
			for _, i := range a.attack {
				if o.Own[i].Order == aikit.OrderIdle {
					a.buf = append(a.buf, o.Own[i].H)
				}
			}
			if len(a.buf) > 0 {
				a.attackX, a.attackZ = a.target(b)
				k.Patrol(a.buf, a.attackX, a.attackZ, false)
			}
		}
	}
}

// target is the nearest remembered enemy building to the rally point, else
// the enemy base guess.
func (a *WaveArmy) target(b *Board) (int32, int32) {
	best := int64(-1)
	x, z := b.EnemyX, b.EnemyZ
	for i := range b.O.Memory {
		r := &b.O.Memory[i]
		if !r.Building {
			continue
		}
		d := aikit.Dist2(r.X, r.Z, b.RallyX, b.RallyZ)
		if best < 0 || d < best {
			best, x, z = d, r.X, r.Z
		}
	}
	return x, z
}

// scout walks start positions and far metal spots in turn.
func (a *WaveArmy) scout(b *Board, u *aikit.OwnUnit) {
	if u.Order != aikit.OrderIdle {
		return
	}
	m := b.K.Map
	n := int32(len(m.Starts)) + int32(len(m.Spots))
	if n == 0 {
		return
	}
	a.scoutTarget = (a.scoutTarget + 1) % n
	var x, z int32
	if a.scoutTarget < int32(len(m.Starts)) {
		x, z = m.Starts[a.scoutTarget][0], m.Starts[a.scoutTarget][1]
	} else {
		sp := &m.Spots[a.scoutTarget-int32(len(m.Starts))]
		x, z = sp.X, sp.Z
	}
	var one [1]pool.Handle
	one[0] = u.H
	b.K.Move(one[:], x, z, false)
}

func (a *WaveArmy) Explain(b *Board, x *aikit.Explain) {
	var rallyValue int32
	for _, i := range a.rally {
		rallyValue += b.O.Own[i].Info.Value
	}
	x.Goals = append(x.Goals, aikit.Goal{Label: fmt.Sprintf("launch wave at army value %d (have %d)", b.Posture.AttackValue, rallyValue), Score: rallyValue, X: b.RallyX, Z: b.RallyZ})
	if a.lastDecision != "" {
		x.Notes = append(x.Notes, a.lastDecision)
	}
	if len(a.attack) > 0 {
		cx, cz := b.Centroid(a.attack)
		var s int64
		for _, i := range a.attack {
			s += b.O.Own[i].Info.Strength()
		}
		x.Squads = append(x.Squads, aikit.SquadView{ID: 1, Task: "attack", X: cx, Z: cz, TargetX: a.attackX, TargetZ: a.attackZ, Units: int32(len(a.attack)), Strength: s, Enemy: int64(b.Threat.At(a.attackX, a.attackZ))})
	}
	if len(a.rally) > 0 {
		cx, cz := b.Centroid(a.rally)
		x.Squads = append(x.Squads, aikit.SquadView{ID: 0, Task: "rally", X: cx, Z: cz, TargetX: b.RallyX, TargetZ: b.RallyZ, Units: int32(len(a.rally))})
	}
}
