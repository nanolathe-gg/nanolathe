package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// zoneSectors is the edge of a target zone in sectors (4 × 128 = 512 wu):
// remembered enemies are binned into zones and each zone is one candidate
// target, valued by what is in it and defended by what can reach it.
const zoneSectors = 4

// zone is one candidate target area.
type zone struct {
	value  int64 // weighted target value
	vx, vz int64 // value-weighted position sums
	eco    int64 // economy share of value (extractors, builders, energy)
	fac    int64 // factory value (where the enemy commander usually works)
	// facSide is the side of the zone's most valuable factory (facTop):
	// the side whose commander is presumed at work there.
	facSide string
	facTop  int32
	com     bool  // the enemy commander was seen here recently
	x, z    int32 // value-weighted centre
	stat    force // static defenses whose reach covers the centre
	mob     force // mobile enemies near the centre, weighted by freshness and distance
	// Persistent across thinks: a failed attack makes the zone "cool" until
	// the tick, unless the attacker has grown well beyond the strength it
	// retreated with.
	coolUntil    uint32
	coolStrength int64
	// Naval view: the value ships can engage from known water, its centre,
	// the water access point they fight from and the enemy there.
	nval        int64
	nship       int64 // part of nval that is ships (torpedoes count against them)
	nvx, nvz    int64
	wx, wz      int32
	nstat, nmob force
}

// incident is an enemy force threatening own assets.
type incident struct {
	x, z   int32
	sx, sz int64 // weight sums for the centroid
	sw     int64
	enemy  force
	assets int64 // own asset value near the incident
	com    bool  // own commander nearby
	taken  int8  // responding squad id, 0 = none
	// naval: every identified raider is a ship (walkers cannot answer it
	// unless it is at home).
	naval bool
}

const maxIncidents = 4

// freshness weights a remembered mobile enemy by how long ago it was seen:
// fully for five seconds, then fading to a quarter at the one-minute
// memory limit — it still exists somewhere nearby.
func freshness(r *aikit.Remembered, tick uint32) int64 {
	if r.Building {
		return 1000
	}
	age := int64(tick - r.LastSeen)
	if age <= 150 {
		return 1000
	}
	w := 1000 - (age-150)*750/1650
	if w < 250 {
		w = 250
	}
	return w
}

// targetWeight is how much destroying a unit is worth to the army beyond
// its cost: economy multiplies (it denies future income), defenses are
// worth little by themselves (they are an obstacle, not a prize).
func targetWeight(info *aikit.UnitInfo) (value, eco int64) {
	v := int64(info.Value)
	r := info.Role
	switch {
	case r.Has(aikit.RoleCommander):
		return commanderValue, 0
	case r.Has(aikit.RoleExtractor):
		return v * 3, v * 3
	case r.Has(aikit.RoleBuilder):
		return v * 3, v * 3
	case r.Has(aikit.RoleFactory):
		return v * 2, v * 2
	case r.Any(aikit.RoleEnergy | aikit.RoleMetalMaker | aikit.RoleStorage):
		return v * 3 / 2, v * 3 / 2
	case r.Has(aikit.RoleRadar):
		return v * 2, 0
	case r.Has(aikit.RoleDefense):
		return v / 2, 0
	}
	return v, 0
}

// commanderValue is the target value of the enemy commander. Its authored
// cost is enormous, but it is worth a decisive win only when it can
// actually be killed, which the engagement prediction decides.
const commanderValue = 8000

// commanderDPSBonus stands in for the commander's manual super-weapon,
// which the unit table leaves out of sustained damage.
const commanderDPSBonus = 150

// buildPicture rebuilds the influence grids, the target zones and the
// global enemy force from the observation.
func (a *Army) buildPicture(b *core.Board) {
	o := b.O
	tick := b.Tick
	a.danger.Clear()
	a.static.Clear()
	a.aa.Clear()
	a.asset.Clear()
	// Fresh damage fades by a quarter a think; only the sectors holding
	// some (hurtCells) are walked.
	n := 0
	for _, i := range a.hurtCells {
		v := a.hurt.V[i] * 3 / 4
		a.hurt.V[i] = v
		if v != 0 {
			a.hurtCells[n] = i
			n++
		}
	}
	a.hurtCells = a.hurtCells[:n]
	for i := range a.zones {
		z := &a.zones[i]
		z.value, z.vx, z.vz, z.eco, z.fac, z.com = 0, 0, 0, 0, 0, false
		z.facSide, z.facTop = "", 0
		z.nval, z.nvx, z.nvz, z.nship = 0, 0, 0, 0
	}
	a.enemyMob = force{}
	var mx, mz, mw int64
	comSeen := false
	m := b.K.Map
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		w := freshness(r, tick)
		if info.DPS > 0 {
			dps := int32(int64(info.DPS) * w / 1000)
			if info.Role.Has(aikit.RoleCommander) {
				dps += commanderDPSBonus
			}
			if r.Building {
				a.addDisc(a.static, r.X, r.Z, info.Range+aikit.SectorWorld/2, dps)
				a.addDisc(a.danger, r.X, r.Z, info.Range+aikit.SectorWorld/2, dps)
			} else {
				a.addDisc(a.danger, r.X, r.Z, info.Range+info.Speed*3, dps)
				if !r.Building && info.Role.Any(aikit.RoleCombat|aikit.RoleCommander) {
					a.enemyMob.addEnemy(info, int64(info.HP), w)
					if !info.Role.Has(aikit.RoleCommander) {
						mx += int64(r.X) * w
						mz += int64(r.Z) * w
						mw += w
					}
				}
			}
		}
		if info.AirDPS > 0 && !(a.P.Air && info.Role.Has(aikit.RoleAir)) {
			a.addDisc(a.aa, r.X, r.Z, info.Range+aikit.SectorWorld/2, int32(int64(info.AirDPS)*w/1000))
		}
		v, eco := targetWeight(info)
		v = v * w / 1000
		eco = eco * w / 1000
		if v > 0 {
			zi := a.zoneOf(r.X, r.Z)
			z := &a.zones[zi]
			z.value += v
			z.eco += eco
			z.vx += int64(r.X) * v
			z.vz += int64(r.Z) * v
			if a.P.Naval && !info.Role.Has(aikit.RoleAir) && a.navalHittable(m, r) {
				z.nval += v
				if a.cls(info).kind == ukNaval {
					z.nship += v
				}
				z.nvx += int64(r.X) * v
				z.nvz += int64(r.Z) * v
			}
			if info.Role.Has(aikit.RoleFactory) {
				z.fac += int64(info.Value)
				if info.Value > z.facTop {
					z.facTop, z.facSide = info.Value, info.Side
				}
			}
			if info.Role.Has(aikit.RoleCommander) {
				comSeen = true
				if tick-r.LastSeen < 450 {
					z.com = true
				}
			}
		}
		if r.LastSeen == tick && !info.Role.Has(aikit.RoleAir) && info.Role.Has(aikit.RoleMobile) {
			a.known[m.Sector(r.X, r.Z)] = 1
		}
	}
	if mw > 0 {
		a.enemyMobX, a.enemyMobZ = int32(mx/mw), int32(mz/mw)
		a.enemyMobKnown = true
	} else {
		a.enemyMobKnown = false
	}
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if c.Info != nil {
			continue
		}
		a.addDisc(a.danger, c.X, c.Z, 2*aikit.SectorWorld, blipDPS)
	}
	// Own side: known ground, assets and fresh damage.
	a.seenNow = a.seenNow[:0]
	for i := range o.Own {
		u := &o.Own[i]
		info := u.Info
		um := a.unit(u)
		if u.Built && info.Role.Has(aikit.RoleMobile) && !info.Role.Has(aikit.RoleAir) {
			a.known[m.Sector(u.X, u.Z)] = 1
		}
		if um.movedTick == 0 || aikit.Dist2(u.X, u.Z, um.lastX, um.lastZ) > 48*48 {
			um.lastX, um.lastZ, um.movedTick = u.X, u.Z, tick
		}
		if u.Built && (!info.Role.Has(aikit.RoleMobile) || info.Role.Any(aikit.RoleBuilder|aikit.RoleCommander)) {
			a.addDisc(a.asset, u.X, u.Z, 3*aikit.SectorWorld, info.Value)
		}
		if u.Built && info.Role.Has(aikit.RoleMobile) && !info.Role.Has(aikit.RoleAir) {
			for si := range m.Starts {
				st := &m.Starts[si]
				if aikit.Dist2(u.X, u.Z, st[0], st[1]) <= startSeenRadius*startSeenRadius {
					a.startSeen[si] = tick
				}
			}
		}
		um.airDmg = 0
		if um.hp > 0 && u.HP < um.hp {
			a.addHurt(u.X, u.Z, (um.hp-u.HP)*4)
			um.hurtTick = tick
			um.airDmg = um.hp - u.HP
		}
		um.hp = u.HP
		um.seen, um.built = tick, u.Built
		a.seenNow = append(a.seenNow, u.H)
		if u.Built && info.Role.Has(aikit.RoleMobile) && a.zoneSeen != nil {
			a.zoneSeen[a.zoneOf(u.X, u.Z)] = tick
		}
	}
	// Units observed last think and gone now were destroyed. Only a unit
	// observed last think can still carry that think's tick.
	if a.seenTick != 0 {
		for _, h := range a.seenPrev {
			um := &a.units[h]
			if um.info != nil && um.built && um.seen == a.seenTick {
				a.lost[a.bucket(um.info)] += int64(um.info.Value)
				um.built = false
			}
		}
	}
	a.seenNow, a.seenPrev = a.seenPrev, a.seenNow
	a.seenTick = tick
	a.indexMemory(b)
	// An unseen enemy commander is presumed at work in its base: the zone
	// with the most factory value carries half its target value and half
	// its fighting strength. It is the commander of the side that built
	// those factories (a mirror game's enemy is of our own side).
	base := int32(-1)
	var com *aikit.UnitInfo
	if !comSeen {
		var bestFac int64
		for i := range a.zones {
			if f := a.zones[i].fac; f > bestFac {
				base, bestFac = int32(i), f
			}
		}
		if base >= 0 {
			a.zones[base].value += commanderValue / 2
			com = a.commanderOf(b.K.Table, a.zones[base].facSide)
		}
	}
	// Zone centres and the enemy that defends each.
	a.candidates = a.candidates[:0]
	a.navalTargets = 0
	for i := range a.zones {
		z := &a.zones[i]
		if z.value <= 0 {
			continue
		}
		z.x, z.z = int32(z.vx/z.value), int32(z.vz/z.value)
		if int32(i) == base {
			// The presumed commander's value has no position of its own.
			z.x, z.z = int32(z.vx/(z.value-commanderValue/2)), int32(z.vz/(z.value-commanderValue/2))
		}
		a.enemyAt(b, z.x, z.z, &z.stat, &z.mob)
		if int32(i) == base && com != nil {
			z.mob.addEnemy(com, int64(com.HP), 500)
		}
		if int32(i) == base && a.P.Naval && a.reachReady && z.nval > 0 {
			// The commander presumed at work among its factories is within
			// the fleet's guns when that base is: count it for the fleet.
			fake := aikit.Remembered{Info: com, X: z.x, Z: z.z, Building: true}
			if com != nil && a.navalHittable(m, &fake) {
				z.nval += commanderValue / 2
				z.nvx += int64(z.x) * (commanderValue / 2)
				z.nvz += int64(z.z) * (commanderValue / 2)
			}
		}
		if z.nval > 0 {
			// Ships fight from the known water nearest what they can hit.
			nx, nz := int32(z.nvx/z.nval), int32(z.nvz/z.nval)
			if wx, wz, ok := a.waterNear(m, nx, nz); ok {
				z.wx, z.wz = wx, wz
				a.enemyAt(b, wx, wz, &z.nstat, &z.nmob)
				a.navalTargets++
			} else {
				z.nval = 0
			}
		}
		a.candidates = append(a.candidates, int32(i))
	}
	a.findIncidents(b)
}

// startSeenRadius is how close an own unit must come to a start position
// to have looked at it.
const startSeenRadius = 450

// sideCom is a side's commander definition (nil: the side has none).
type sideCom struct {
	side string
	com  *aikit.UnitInfo
}

// commanderOf finds a side's commander definition in the unit table,
// remembered per side.
func (a *Army) commanderOf(t *aikit.Table, side string) *aikit.UnitInfo {
	for i := range a.coms {
		if a.coms[i].side == side {
			return a.coms[i].com
		}
	}
	var com *aikit.UnitInfo
	for _, u := range t.Units {
		if u.Side == side && u.Role.Has(aikit.RoleCommander) {
			com = u
			break
		}
	}
	a.coms = append(a.coms, sideCom{side: side, com: com})
	return com
}

func (a *Army) zoneOf(x, z int32) int32 {
	sx, sz := a.zoneXZ(x, z)
	return sz*a.zoneW + sx
}

// zoneXZ is the zone column and row of (x, z), clamped to the map.
func (a *Army) zoneXZ(x, z int32) (int32, int32) {
	sx, sz := x/(aikit.SectorWorld*zoneSectors), z/(aikit.SectorWorld*zoneSectors)
	if sx < 0 {
		sx = 0
	}
	if sz < 0 {
		sz = 0
	}
	if sx >= a.zoneW {
		sx = a.zoneW - 1
	}
	if sz >= a.zoneH {
		sz = a.zoneH - 1
	}
	return sx, sz
}

// Distances for the engagement prediction around a point.
const (
	staticSlack   = 150  // a static defense counts if the point is inside its range plus this
	nearMobile    = 700  // mobile enemies this close count fully
	reinforceDist = 1600 // mobile enemies this close count half (they can arrive in time)
)

// enemyAt estimates the enemy that would fight at (x, z): static defenses
// whose reach covers the point, mobile units nearby (freshness-weighted)
// and half of those close enough to reinforce, plus radar contacts.
func (a *Army) enemyAt(b *core.Board, x, z int32, stat, mob *force) {
	*stat, *mob = force{}, force{}
	a.memIndex(b)
	o := b.O
	tick := b.Tick
	for _, i := range a.memStat {
		r := &o.Memory[i]
		reach := int64(r.Info.Range + staticSlack)
		if aikit.Dist2(r.X, r.Z, x, z) <= reach*reach {
			stat.add(r.Info, int64(r.Info.HP), 1000)
		}
	}
	zx0, zz0 := a.zoneXZ(x-reinforceDist, z-reinforceDist)
	zx1, zz1 := a.zoneXZ(x+reinforceDist, z+reinforceDist)
	for zz := zz0; zz <= zz1; zz++ {
		for zx := zx0; zx <= zx1; zx++ {
			zi := zz*a.zoneW + zx
			for _, i := range a.mobIdx[a.mobHead[zi]:a.mobHead[zi+1]] {
				r := &o.Memory[i]
				d2 := aikit.Dist2(r.X, r.Z, x, z)
				w := freshness(r, tick)
				switch {
				case d2 <= nearMobile*nearMobile:
				case d2 <= reinforceDist*reinforceDist:
					w /= 2
				default:
					continue
				}
				mob.addEnemy(r.Info, int64(r.Info.HP), w)
			}
		}
	}
	for _, i := range a.blips {
		c := &o.Enemy[i]
		if aikit.Dist2(c.X, c.Z, x, z) <= nearMobile*nearMobile {
			mob.addGeneric(1000)
		}
	}
}

// localEnemy is the fight a squad is in right now: visible enemies around
// its centre at their current health, fresh remembered mobiles at half
// weight, static defenses whose reach covers it, and radar contacts.
func (a *Army) localEnemy(b *core.Board, x, z, radius int32, out *force, stat *force) {
	*out, *stat = force{}, force{}
	o := b.O
	r2 := int64(radius) * int64(radius)
	for i := range o.Enemy {
		c := &o.Enemy[i]
		d2 := aikit.Dist2(c.X, c.Z, x, z)
		if c.Info == nil {
			if d2 <= r2 {
				out.addGeneric(1000)
			}
			continue
		}
		if c.Info.DPS <= 0 || c.Info.Role.Has(aikit.RoleAir) || !c.Built {
			continue
		}
		if !c.Info.Role.Has(aikit.RoleMobile) {
			continue // statics are counted from memory with their reach
		}
		if d2 > r2 {
			continue
		}
		out.addEnemy(c.Info, int64(c.Info.HP)*int64(c.HPPct)/100, 1000)
	}
	tick := b.Tick
	for i := range o.Memory {
		r := &o.Memory[i]
		info := r.Info
		if info.DPS <= 0 || info.Role.Has(aikit.RoleAir) {
			continue
		}
		d2 := aikit.Dist2(r.X, r.Z, x, z)
		if r.Building {
			reach := int64(info.Range + staticSlack + 100)
			if d2 <= reach*reach {
				stat.add(info, int64(info.HP), 1000)
			}
			continue
		}
		if r.LastSeen == tick || tick-r.LastSeen > 150 || d2 > r2 {
			continue // visible ones were counted above; old ones are elsewhere
		}
		out.addEnemy(info, int64(info.HP), 500)
	}
}

// findIncidents groups visible armed enemies that threaten own assets
// (buildings, builders, the commander) into at most maxIncidents clusters.
func (a *Army) findIncidents(b *core.Board) {
	o := b.O
	a.incidents = a.incidents[:0]
	var comX, comZ int32
	hasCom := b.Commander >= 0
	if hasCom {
		comX, comZ = o.Own[b.Commander].X, o.Own[b.Commander].Z
	}
	for i := range o.Enemy {
		c := &o.Enemy[i]
		var info *aikit.UnitInfo = c.Info
		if info != nil && (info.DPS <= 0 || !info.Role.Has(aikit.RoleMobile)) {
			continue
		}
		if a.P.Air && info != nil && info.Role.Has(aikit.RoleAir) {
			continue // aircraft are the fighters' and the anti-air escort's
		}
		nearAsset := a.asset.At(c.X, c.Z) > 0
		nearHome := aikit.Dist2(c.X, c.Z, b.HomeX, b.HomeZ) < core.BaseRadius*core.BaseRadius
		nearCom := hasCom && aikit.Dist2(c.X, c.Z, comX, comZ) < 800*800
		if !nearAsset && !nearHome && !nearCom {
			continue
		}
		k := -1
		for j := range a.incidents {
			if aikit.Dist2(a.incidents[j].x, a.incidents[j].z, c.X, c.Z) < 700*700 {
				k = j
				break
			}
		}
		if k < 0 {
			if len(a.incidents) >= maxIncidents {
				continue
			}
			a.incidents = append(a.incidents, incident{x: c.X, z: c.Z, naval: a.P.Naval})
			k = len(a.incidents) - 1
		}
		in := &a.incidents[k]
		if info == nil || a.cls(info).kind != ukNaval {
			in.naval = false
		}
		if info == nil {
			in.enemy.addGeneric(1000)
		} else {
			in.enemy.addEnemy(info, int64(info.HP)*int64(c.HPPct)/100, 1000)
		}
		in.sx += int64(c.X)
		in.sz += int64(c.Z)
		in.sw++
		in.x, in.z = int32(in.sx/in.sw), int32(in.sz/in.sw)
		in.com = in.com || nearCom
	}
	for j := range a.incidents {
		in := &a.incidents[j]
		in.assets = int64(a.asset.At(in.x, in.z))
		if in.com {
			in.assets += 3000
		}
	}
	// Most valuable first (insertion sort: a handful of entries).
	for i := 1; i < len(a.incidents); i++ {
		for j := i; j > 0 && a.incidents[j].assets > a.incidents[j-1].assets; j-- {
			a.incidents[j], a.incidents[j-1] = a.incidents[j-1], a.incidents[j]
		}
	}
}
