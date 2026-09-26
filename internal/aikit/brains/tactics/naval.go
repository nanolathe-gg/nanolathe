package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Naval and amphibious forces.
//
// Water is where the fleet's regions are (reach.go: the region maps of our
// ships' movement classes); before the first ship, the water metal spots.
// Ships only ever target what stands within reach of known water. When
// walkers cannot reach the enemy base, hovercraft and amphibious units form
// their own assault squad.

// navalReach is how far from known water a land target still counts as
// coastal: within the gun range of an ordinary warship.
const navalReach = 520

// waterSearch is the radius (sectors) searched for a water access point
// around a naval target.
const waterSearch = 6

// markWater records that a sector holds water a ship can use.
func (a *Army) markWater(m *aikit.MapInfo, x, z int32) {
	a.water[m.Sector(x, z)] = 1
}

// updateWaterDist is a multi-source breadth-first walk (8-neighbour) from
// every known water sector; wdist is the sector distance, capped at 255.
func (a *Army) updateWaterDist(m *aikit.MapInfo) {
	w, h := m.SectorW, m.SectorH
	q := a.bfs[:0]
	for i := range a.wdist {
		if a.water[i] != 0 {
			a.wdist[i] = 0
			q = append(q, int32(i))
		} else {
			a.wdist[i] = 255
		}
	}
	for head := 0; head < len(q); head++ {
		n := q[head]
		d := a.wdist[n]
		if d >= 254 {
			continue
		}
		x, z := n%w, n/w
		for k := 0; k < 8; k++ {
			nx, nz := x+nbrDX[k], z+nbrDZ[k]
			if nx < 0 || nz < 0 || nx >= w || nz >= h {
				continue
			}
			nb := nz*w + nx
			if a.wdist[nb] > d+1 {
				a.wdist[nb] = d + 1
				q = append(q, nb)
			}
		}
	}
	a.bfs = q[:0]
	a.homeWaterSet = false
}

// navalHittable reports whether a remembered enemy can be engaged from
// known water: it floats, or it stands within gun range of the shore.
func (a *Army) navalHittable(m *aikit.MapInfo, r *aikit.Remembered) bool {
	info := r.Info
	if a.cls(info).kind == ukNaval || (r.Building && info.Def != nil && info.Def.MinWaterDepth > 0) {
		return true
	}
	d := int32(a.wdist[m.Sector(r.X, r.Z)])
	return d*aikit.SectorWorld <= navalReach
}

// waterNear finds the known water sector nearest to (x, z) within
// waterSearch sectors and returns its centre.
func (a *Army) waterNear(m *aikit.MapInfo, x, z int32) (int32, int32, bool) {
	cx, cz := x/aikit.SectorWorld, z/aikit.SectorWorld
	best := int32(-1)
	var bestD int32
	for dz := -int32(waterSearch); dz <= waterSearch; dz++ {
		sz := cz + dz
		if sz < 0 || sz >= m.SectorH {
			continue
		}
		for dx := -int32(waterSearch); dx <= waterSearch; dx++ {
			sx := cx + dx
			if sx < 0 || sx >= m.SectorW {
				continue
			}
			i := sz*m.SectorW + sx
			if a.water[i] == 0 {
				continue
			}
			d := dx*dx + dz*dz
			if best < 0 || d < bestD {
				best, bestD = i, d
			}
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	wx, wz := m.SectorCentre(best)
	return wx, wz, true
}

// homeWaterPoint is the known water sector nearest home (cached until the
// water knowledge changes).
func (a *Army) homeWaterPoint(b *core.Board) (int32, int32, bool) {
	if a.homeWaterSet {
		return a.homeWX, a.homeWZ, a.homeWOK
	}
	a.homeWaterSet = true
	m := b.K.Map
	best := int64(-1)
	for i := range a.water {
		if a.water[i] == 0 {
			continue
		}
		x, z := m.SectorCentre(int32(i))
		d := aikit.Dist2(x, z, b.HomeX, b.HomeZ)
		if best < 0 || d < best {
			best, a.homeWX, a.homeWZ = d, x, z
		}
	}
	a.homeWOK = best >= 0
	return a.homeWX, a.homeWZ, a.homeWOK
}

// navalGather is where the fleet waits: the known water within 1500 wu of
// home water nearest the enemy (between the base and the threat), or next to
// a naval constructor working away from home so it is not left alone.
func (a *Army) navalGather(b *core.Board) (int32, int32) {
	if b.Tick < a.navalGatherTick+150 && a.navalGatherSet {
		return a.ngx, a.ngz
	}
	a.navalGatherTick = b.Tick
	a.navalGatherSet = true
	hx, hz, ok := a.homeWaterPoint(b)
	if !ok {
		a.ngx, a.ngz = b.HomeX, b.HomeZ
		return a.ngx, a.ngz
	}
	m := b.K.Map
	ex, ez := b.EnemyX, b.EnemyZ
	bx, bz := hx, hz
	var bestD int64 = -1
	// Where the fleet may wait: near home water; with region tables and the
	// enemy base located, forward at sea toward it (no nearer than 1600 wu
	// or 35 % of the way back from it), where it both blocks the enemy's
	// water and strikes without sailing across the map after each fight.
	lim := int64(1500)
	var keep int64 = -1
	danger := int32(gatherDanger)
	if a.reachReady && b.EnemyKnown {
		lim = 1 << 20
		keep = int64(aikit.Dist(hx, hz, ex, ez)) * 35 / 100
		if keep < 1600 {
			keep = 1600
		}
		danger = gatherDanger * 2
	}
	for i := range a.water {
		if a.water[i] == 0 {
			continue
		}
		x, z := m.SectorCentre(int32(i))
		if aikit.Dist2(x, z, hx, hz) > lim*lim || a.danger.At(x, z) > danger || a.nearFactory(b, x, z) {
			continue
		}
		d := aikit.Dist2(x, z, ex, ez)
		if keep >= 0 && d < keep*keep {
			continue
		}
		if bestD < 0 || d < bestD {
			bx, bz, bestD = x, z, d
		}
	}
	if bestD < 0 {
		// Only the water by our own yards is known: wait out at sea, away
		// from home past the yard, not on its pad.
		dx, dz := int64(hx-b.HomeX), int64(hz-b.HomeZ)
		if dx == 0 && dz == 0 {
			dx, dz = int64(ex-hx), int64(ez-hz)
		}
		if d := aikit.ISqrt64(dx*dx + dz*dz); d > 0 {
			bx = clampTo(hx+int32(dx*600/d), m.WorldW)
			bz = clampTo(hz+int32(dz*600/d), m.WorldH)
		}
	}
	// A naval constructor working far out: keep the fleet with it.
	o := b.O
	var far int64 = 800 * 800
	for _, i := range b.Builders {
		u := &o.Own[i]
		if a.cls(u.Info).kind != ukNaval {
			continue
		}
		d := aikit.Dist2(u.X, u.Z, bx, bz)
		threatened := !a.reachReady || a.danger.At(u.X, u.Z) > 0
		if threatened && d > far && aikit.Dist2(u.X, u.Z, b.HomeX, b.HomeZ) < 3000*3000 && a.danger.At(u.X, u.Z) <= gatherDanger*3 {
			far = d
			a.ngx, a.ngz = u.X, u.Z
		}
	}
	if far > 800*800 {
		return a.ngx, a.ngz
	}
	a.ngx, a.ngz = bx, bz
	return a.ngx, a.ngz
}

// blind reports a fleet or amphibious squad taking damage it cannot account
// for: the recent damage rate well above what the known enemy around it
// deals and enough to matter (1.5% of the present hit points per second).
// Ships see less far than coastal guns reach; staying means dying unseen.
func (a *Army) blind(s *squad, known int64) bool {
	if !a.P.Naval || (s.id != sqNaval && s.id != sqAmph) {
		return false
	}
	// A large fleet clearly winning the fight it can see (torpedo launchers
	// and submarines show only to its sonar ships) pushes on to twice the
	// rate.
	lim := int64(15)
	if s.ratio >= 4000 && s.present.value >= 5000 {
		lim = 30
	}
	return s.dmgRate > 20 && s.dmgRate*2 > known*3 && s.dmgRate*1000 > s.total.hp*lim
}

// padClear is how far a waiting squad keeps from an own factory: ships or
// aircraft parked on a pad stop its production.
const padClear = 450

// nearFactory reports whether (x, z) is within padClear of an own factory.
func (a *Army) nearFactory(b *core.Board, x, z int32) bool {
	o := b.O
	for _, fi := range b.Factories {
		f := &o.Own[fi]
		if aikit.Dist2(f.X, f.Z, x, z) <= padClear*padClear {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Reach.

// reachable reports whether squad s may target a point: most of it can get
// within range of it (reach.go). Without reach tables (no terrain, as in
// unit tests) every point is taken on trust.
func (a *Army) reachable(b *core.Board, s *squad, x, z int32) bool {
	if !a.P.Naval || !a.reachReady {
		return true
	}
	return a.reaches(b, s, x, z)
}

// landCut reports whether walkers are known not to reach the enemy base.
func (a *Army) landCut(b *core.Board) bool {
	return a.P.Naval && a.cut
}

// fleetPicture sums the enemy ships and hovercraft (what contests the sea),
// freshness-weighted, and keeps a slowly fading estimate of the largest
// fleet seen: the host forgets ships a minute after they leave sight, but
// a fleet waiting at its base is still there when ours arrives.
func (a *Army) fleetPicture(b *core.Board) {
	o := b.O
	a.enemyFleet = force{}
	var x, z, w int64
	for i := range o.Memory {
		r := &o.Memory[i]
		if r.Building || r.Info.DPS <= 0 {
			continue
		}
		if k := a.cls(r.Info).kind; k != ukNaval && k != ukHover {
			continue
		}
		f := freshness(r, b.Tick)
		a.enemyFleet.addEnemy(r.Info, int64(r.Info.HP), f)
		x += int64(r.X) * f
		z += int64(r.Z) * f
		w += f
	}
	if a.enemyFleet.strength() >= a.fleetMem.strength() {
		a.fleetMem = a.enemyFleet
		f := &a.fleetMemK
		*f = a.enemyFleet
		f.dps, f.hp, f.aa, f.rngW, f.value = f.dps*1000, f.hp*1000, f.aa*1000, f.rngW*1000, f.value*1000
		if w > 0 {
			a.fleetX, a.fleetZ = int32(x/w), int32(z/w)
		}
		return
	}
	a.fadeFleet(int64(a.dt))
}

// fadeFleet fades the remembered fleet by dt ticks with the time constant
// fleetMemTicks. The fade keeps parts per million of a memory held in
// thousandths: in permille of whole units the per-think step truncated
// (15 ticks: 1000 − 1 rather than 1000 − 1.67, a time constant of 15000
// ticks, not 9000) and the amounts stopped fading once small.
func (a *Army) fadeFleet(dt int64) {
	const ppm = 1000000
	keep := int64(ppm) - dt*ppm/fleetMemTicks
	if keep < 0 {
		keep = 0
	}
	f := &a.fleetMemK
	f.dps, f.hp, f.aa = f.dps*keep/ppm, f.hp*keep/ppm, f.aa*keep/ppm
	f.rngW, f.value = f.rngW*keep/ppm, f.value*keep/ppm
	g := &a.fleetMem
	g.dps, g.hp, g.aa, g.rngW, g.value = f.dps/1000, f.hp/1000, f.aa/1000, f.rngW/1000, f.value/1000
}

// fleetMemTicks is how long a seen enemy fleet is expected to still exist.
const fleetMemTicks = 9000

// seaHurtTicks is the time constant of the memory of where ships were hurt.
const seaHurtTicks = 5400

// seaHurtPicture fades the memory of where our ships took damage and adds
// this think's hits (hit points per second): coastal guns and submarines
// the fleet could not see are still there when it comes back.
func (a *Army) seaHurtPicture(b *core.Board) {
	if a.seaTick == 0 {
		a.seaTick = b.Tick
	}
	if fade := int64(b.Tick - a.seaTick); fade >= memFadeTicks {
		a.seaTick = b.Tick
		fadeGrid(a.seaHurt, a.seaHurtK, fade, seaHurtTicks, nil)
	}
	dt := int64(a.dt)
	if dt <= 0 {
		return
	}
	o := b.O
	for i := range o.Own {
		u := &o.Own[i]
		if !u.Built || a.cls(u.Info).kind != ukNaval {
			continue
		}
		if d := a.units[u.H].airDmg; d > 0 {
			a.addDisc(a.seaHurt, u.X, u.Z, 4*aikit.SectorWorld, int32(int64(d)*30/dt))
		}
	}
}

// seaHurtReserve counts remembered hits at a naval target as an unseen
// defender of that damage rate (twenty seconds of it as hit points).
func (a *Army) seaHurtReserve(x, z int32, en *force) {
	v := int64(a.seaHurt.At(x, z))
	if v <= 0 {
		return
	}
	en.dps += v
	en.hp += v * 20
	en.rngW += v * 600
}

// fleetReserve adds the part of the remembered enemy fleet that is not in
// sight now (what is in sight is already counted around the target):
// fully near where it was last seen or near the enemy base (where it
// returns), half elsewhere (it can come).
func (a *Army) fleetReserve(b *core.Board, x, z int32, en *force) {
	f := a.fleetMem
	cur := &a.enemyFleet
	f.dps -= cur.dps
	f.hp -= cur.hp
	f.rngW -= cur.rngW
	if f.dps <= 0 || f.hp <= 0 {
		return
	}
	if f.rngW < 0 {
		f.rngW = 0
	}
	if aikit.Dist2(x, z, a.fleetX, a.fleetZ) > 2500*2500 && aikit.Dist2(x, z, b.EnemyX, b.EnemyZ) > 2500*2500 {
		f.dps /= 2
		f.hp /= 2
		f.rngW /= 2
	}
	en.dps += f.dps
	en.hp += f.hp
	en.rngW += f.rngW
}

// navalExploreValue is the fleet value that may sail to find the enemy
// coast (two destroyers): scouting by fleet is a fight on the enemy's terms.
const navalExploreValue = 1800

// navalMargin is added to the engage margin at sea: submarines are unseen
// without sonar and coastal guns outrange a ship's sight.
const navalMargin = 250

// navalExplore picks where a fleet with nothing it can engage sails: the
// enemy base once buildings have shown where it is, otherwise the nearest
// start position nobody has looked at lately. Air scouts and radar find
// the rest.
func (a *Army) navalExplore(b *core.Board, s *squad) (int32, int32, bool) {
	if s.total.value < navalExploreValue {
		return 0, 0, false
	}
	if b.EnemyKnown {
		return b.EnemyX, b.EnemyZ, true
	}
	m := b.K.Map
	best := -1
	var bestD int64
	for i := range m.Starts {
		st := &m.Starts[i]
		if aikit.Dist2(st[0], st[1], b.HomeX, b.HomeZ) < 800*800 || !m.MaybeEnemyStart(i) {
			continue
		}
		if a.startSeen[i] != 0 && b.Tick-a.startSeen[i] < startStale {
			continue
		}
		d := aikit.Dist2(st[0], st[1], s.cx, s.cz)
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return m.Starts[best][0], m.Starts[best][1], true
}
