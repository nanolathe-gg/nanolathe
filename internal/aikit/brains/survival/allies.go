package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// The team's other members, as the observation's allied units show them
// (aikit.Obs.Allies; the survivors share sight, docs/DESIGN_SURVIVAL.md
// §4.3). Each think the survival layer reads from them:
//
//   - the allied towers on each bearing, which cover that lane as the
//     survivor's own would, so it is not towered twice (nextTower);
//   - how far the allied buildings reach on each bearing, so the ring
//     stands in front of the whole team's base, not inside the human's;
//   - keep-out boxes around allied buildings and over allied factories'
//     exit lanes, which tower and wall sites avoid;
//   - damaged allied buildings, repaired like its own (repairTarget), and
//     allied buildings under fire, which the army is turned toward;
//   - the metal spots allied extractors stand on, held from its economy;
//   - the allied commanders, which its own commander keeps clear of while
//     attackers are near it: a commander's death explosion kills any
//     commander beside it, and the human's commander ends the battle.

// keepOut is a box of world units, x0 <= x < x1 and z0 <= z < z1: round
// an allied building, or over an allied factory's exit lane (lane).
type keepOut struct {
	x0, z0, x1, z1 int32
	lane           bool
}

// allyView is the per-think picture of the team's other members.
type allyView struct {
	have   [numSectors]int64 // allied tower value standing or framed, by sector
	towers [numSectors]int32
	boxes  []keepOut
	coms   [][2]int32 // the allied commanders' positions
	ext    [][2]int32 // the allied extractors' positions, framed or built
	// hit is the allied building under fire this survivor weighs most (its
	// value times the share of its hit points lost), for the army.
	hit        bool
	hitX, hitZ int32
}

// Allied geometry, world units.
const (
	// allyCoverMin is how far from the site an allied tower must stand to
	// cover its bearing's lane: out on the ring's ground (towerRoom, less a
	// margin). A tower nearer guards the human's core, which the ring
	// stands in front of, not the lane the ring would hold.
	allyCoverMin = towerRoom - 160
	// allyMargin is the clearance a tower or wall site keeps around an
	// allied building's footprint.
	allyMargin = 48
	// allyLane is the depth of the exit lane kept clear in front of an
	// allied factory (units leave toward +Z, as the executor keeps an own
	// factory's lane).
	allyLane = 160
	// allyHitNear is how near an armed attacker must be to a damaged allied
	// building for it to count as under fire.
	allyHitNear = 480
	// blastKeep is how near another survivor's commander this survivor's
	// commander may stand while attackers are near it: beyond a commander's
	// death explosion (stock: 950 wu across), with a margin.
	blastKeep = 560
)

// observeAllies reads the allied units in sight. It runs after the own
// buildings have drawn each sector's perimeter and before the sector
// weights, and extends the perimeter with the allied buildings.
func (st *state) observeAllies(b *core.Board) {
	a := &st.ally
	a.have = [numSectors]int64{}
	a.towers = [numSectors]int32{}
	a.boxes = a.boxes[:0]
	a.coms = a.coms[:0]
	a.ext = a.ext[:0]
	a.hit = false
	var hitV int64
	o := b.O
	for i := range o.Allies {
		u := &o.Allies[i]
		role := u.Info.Role
		if role.Has(aikit.RoleCommander) {
			a.coms = append(a.coms, [2]int32{u.X, u.Z})
		}
		if role.Has(aikit.RoleMobile) {
			continue
		}
		if role.Has(aikit.RoleExtractor) {
			a.ext = append(a.ext, [2]int32{u.X, u.Z})
		}
		hx, hz := u.Info.FootX*8+allyMargin, u.Info.FootZ*8+allyMargin
		a.boxes = append(a.boxes, keepOut{x0: u.X - hx, z0: u.Z - hz, x1: u.X + hx, z1: u.Z + hz})
		if role.Has(aikit.RoleFactory) {
			a.boxes = append(a.boxes, keepOut{x0: u.X - hx, z0: u.Z, x1: u.X + hx, z1: u.Z + u.Info.FootZ*8 + allyLane, lane: true})
		}
		vx, vz := int64(u.X-st.cx), int64(u.Z-st.cz)
		if d2 := vx*vx + vz*vz; d2 >= allyCoverMin*allyCoverMin {
			s := sectorOf(vx, vz)
			d := st.tab.of(u.Info)
			switch {
			case d.tower || d.aa:
				a.have[s] += int64(u.Info.Value)
				a.towers[s]++
			case role.Any(aikit.RoleFactory | aikit.RoleEnergy | aikit.RoleMetalMaker | aikit.RoleStorage | aikit.RoleRadar):
				if r := int32(aikit.ISqrt64(d2)) + perimeterAhead; r > st.radius[s] {
					st.radius[s] = min(r, maxPerimeter)
				}
			}
		}
		if u.Built && u.MaxHP > 0 && u.HP < u.MaxHP && armedNear(o, u.X, u.Z, allyHitNear) {
			if v := int64(u.Info.Value) * int64(u.MaxHP-u.HP) / int64(u.MaxHP); !a.hit || v > hitV {
				a.hit, a.hitX, a.hitZ, hitV = true, u.X, u.Z, v
			}
		}
	}
}

// armedNear reports whether an armed contact, or a blip, is within r of
// (x, z).
func armedNear(o *aikit.Obs, x, z, r int32) bool {
	r2 := int64(r) * int64(r)
	for i := range o.Enemy {
		c := &o.Enemy[i]
		if (c.Info == nil || c.Info.DPS > 0) && aikit.Dist2(c.X, c.Z, x, z) <= r2 {
			return true
		}
	}
	return false
}

// clearOfAllies reports whether (x, z) is outside every allied building's
// keep-out box and every allied factory's lane.
func (st *state) clearOfAllies(x, z int32) bool {
	for i := range st.ally.boxes {
		k := &st.ally.boxes[i]
		if x >= k.x0 && x < k.x1 && z >= k.z0 && z < k.z1 {
			return false
		}
	}
	return true
}

// outOfLanes reports whether (x, z) is outside every allied factory's exit
// lane.
func (st *state) outOfLanes(x, z int32) bool {
	for i := range st.ally.boxes {
		k := &st.ally.boxes[i]
		if k.lane && x >= k.x0 && x < k.x1 && z >= k.z0 && z < k.z1 {
			return false
		}
	}
	return true
}

// allyCover is the allied tower value this survivor credits to sector s
// against its own deficit there: the allied towers on that bearing, up to
// the deficit, so a lane an ally already towers draws none of this
// survivor's towers and its share goes to the other lanes.
func (st *state) allyCover(s int, deficit int64) int64 {
	if deficit <= 0 {
		return 0
	}
	return min(st.ally.have[s], deficit)
}

// nearAllyCommander reports whether another survivor's commander stands
// within r of (x, z).
func (st *state) nearAllyCommander(x, z, r int32) bool {
	r2 := int64(r) * int64(r)
	for _, c := range st.ally.coms {
		if aikit.Dist2(c[0], c[1], x, z) < r2 {
			return true
		}
	}
	return false
}

// allyAt finds an allied unit by handle and instance; nil when it is gone
// or out of sight.
func allyAt(o *aikit.Obs, h pool.Handle, gen uint32) *aikit.AllyUnit {
	for i := range o.Allies {
		if u := &o.Allies[i]; u.H == h && u.Gen == gen {
			return u
		}
	}
	return nil
}
