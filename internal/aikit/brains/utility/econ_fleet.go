package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// Fleet composition (fleet switch, README §13.2).
//
// The production layer rates combat units by square-law efficiency,
// DPS·HP / (V·(V+v_ref)), which favours the cheapest hull: on pillopeens
// the fleet was 68 patrol boats. Human fleets in the stock unit set (the
// 1v1 recordings of the original game with ships, 58 player-games) are
// about one light boat in seven; the rest are destroyers, submarines and
// tech-2 hulls. A fleet fights coastal towers and other fleets that focus
// fire, where a light hull dies before it pays for itself; destroyers
// also carry sonar and depth charges against submarines.
//
// With the switch, while the enemy fields a navy (fleetWar), naval combat
// products are rated with a heavier blend (v_ref × fleetVRef: at the
// default 600 a destroyer out-rates a patrol boat), and light boats — a
// quarter of the value of the heaviest hull their shipyard makes, or less
// — are kept to a few for scouting and raiding: two, plus one per six
// ships of the fleet.

const (
	fleetNaval    = 100 // permille of the enemy's seen mobile strength afloat from which the fleet switch acts
	fleetVRef     = 4   // naval efficiency blend: v_ref times this
	lightHullDiv  = 4   // light boat: value at most the heaviest hull's / this
	lightBase     = 2   // light boats always allowed
	lightPerFleet = 6   // one more light boat per this many ships
)

// setupFleet labels naval combat products and light boats (setup).
func (s *shared) setupFleet(k *aikit.Kit) {
	t := k.Table
	for i, u := range t.Units {
		si := &s.info[i]
		si.hull = u.Role.Has(aikit.RoleCombat) && u.Role.Has(aikit.RoleMobile) &&
			!u.Role.Any(aikit.RoleKamikaze|aikit.RoleTransport|aikit.RoleAir|aikit.RoleHover) &&
			(u.Role.Has(aikit.RoleNaval) || si.water)
		if si.hull {
			s.hullDefs = append(s.hullDefs, int32(i))
		}
	}
	for _, f := range t.Units {
		if !f.Role.Has(aikit.RoleFactory) {
			continue
		}
		var heavy int32
		for _, q := range f.Builds {
			if s.info[q.Index].hull && q.Value > heavy {
				heavy = q.Value
			}
		}
		for _, q := range f.Builds {
			if s.info[q.Index].hull && q.Value*lightHullDiv <= heavy {
				s.info[q.Index].light = true
			}
		}
	}
	for _, i := range s.hullDefs {
		if s.info[i].light {
			s.lightDefs = append(s.lightDefs, i)
		}
	}
}

// effFleet is effN with the heavier blend for naval hulls (fleet switch).
func (s *shared) effFleet(q *aikit.UnitInfo, aaNeed int64) int64 {
	if !s.fleetWar() || !s.info[q.Index].hull {
		return s.effN(q, aaNeed)
	}
	v := s.info[q.Index].costMeq
	if v < 1 {
		v = 1
	}
	dps := int64(q.SurfaceDPS()-q.StunDPS) + int64(q.WaterDPS)*s.navalShare/one + int64(q.AirDPS)*aaNeed/one
	if dps < 0 {
		dps = 0
	}
	return dps * int64(q.HP) * 1000000 / (v * (v + int64(s.p.VRef)*fleetVRef))
}

// fleetWar reports that the fleet switch acts: against an enemy navy (at
// least fleetNaval of the enemy's seen mobile strength). Against coasts
// alone the square law's cheap boats do better: patrol boats carry about
// twice a destroyer's damage and one and a half times its hit points per
// unit of value, and on the water pool against a brain without ships the
// heavier fleet scored 74% of points where the patrol boats scored 78–80%
// (sail away 4-4-0 against 7-1-0; E4, README §13.8).
func (s *shared) fleetWar() bool {
	return s.p.Fleet != 0 && s.navalShare >= fleetNaval
}

// lightFull reports whether the fleet holds as many light boats as it
// keeps (own, framed or queued this think).
func (s *shared) lightFull() bool {
	var fleet, light int64
	for _, i := range s.hullDefs {
		fleet += int64(s.count[i])
	}
	for _, i := range s.lightDefs {
		light += int64(s.count[i])
	}
	return light >= lightBase+fleet/lightPerFleet
}
