package utility

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Model extensions behind the naval, air and tech switches. With every
// switch off none of this runs and the brain is the earlier baseline.

// setupN computes the static tables the switches need (setup, sim thread).
func (s *shared) setupN(k *aikit.Kit, com *aikit.UnitInfo) {
	t := k.Table
	for i, u := range t.Units {
		si := &s.info[i]
		si.combatN = u.Role.Has(aikit.RoleCombat) && !u.Role.Any(aikit.RoleKamikaze|aikit.RoleTransport)
		if !u.Role.Has(aikit.RoleMobile) || !u.Role.Has(aikit.RoleBuilder) {
			continue
		}
		for _, p := range u.Builds {
			if p.Role.Has(aikit.RoleExtractor) {
				si.mexes = append(si.mexes, p)
			}
		}
		sort.SliceStable(si.mexes, func(a, b int) bool { return si.mexes[a].Value < si.mexes[b].Value })
	}
	for i, u := range t.Units {
		if !u.Role.Has(aikit.RoleFactory) {
			continue
		}
		si := &s.info[i]
		air, any := true, false
		for _, q := range u.Builds {
			if !s.info[q.Index].combatN {
				continue
			}
			any = true
			if !q.Role.Has(aikit.RoleAir) {
				air = false
			}
		}
		si.airFac = any && air
	}
	if s.p.Naval != 0 || s.p.Air != 0 {
		s.analyzeTerrain(k, com)
		if s.p.ReachMix != 0 && s.terr.ready {
			s.setupReachMix(k)
		}
	}
	if s.p.Tech != 0 {
		s.setupTech(k, com)
	}
	if s.p.Naval != 0 && s.p.Fleet != 0 {
		s.setupFleet(k)
	}
	for i, u := range t.Units {
		if u.Role.Has(aikit.RoleFactory) && (!s.terr.ready || s.terr.inTree[i]) {
			s.factories = append(s.factories, int32(i))
		}
	}
	if s.p.Naval != 0 && s.p.TidalField != 0 {
		s.setupWaterFields(k, com)
	}
	s.spotOwn = make([]int32, len(k.Map.Spots))
	// Constructor types in our tree, and the standing water work each would
	// have at the naval base: three spots' worth of room, six where the tide
	// is strong enough that tidal power and floating makers are the economy.
	s.consRoom = make([]int32, len(t.Units))
	for i, u := range t.Units {
		si := &s.info[i]
		if !si.canEco || !u.Role.Has(aikit.RoleMobile) || (s.terr.ready && !s.terr.inTree[i]) {
			continue
		}
		s.consDefs = append(s.consDefs, int32(i))
		if !s.terr.ready {
			continue
		}
		for _, p := range u.Builds {
			if !s.info[p.Index].water || !p.Role.Any(aikit.RoleEnergy|aikit.RoleMetalMaker) || !s.terr.siteOK[p.Index] {
				continue
			}
			if c := si.cls; c < 0 || s.terr.cls[c].siteOK[p.Index] {
				si.waterWork = 3
				if k.Map.TidalPermille >= 15000 {
					si.waterWork = 6
				}
				break
			}
		}
	}
}

// effN is eff with torpedo damage counted only as far as the enemy fields
// ships, so a submarine is not rated against a land base, and without
// paralyzer damage, which stuns but never kills.
func (s *shared) effN(q *aikit.UnitInfo, aaNeed int64) int64 {
	v := s.info[q.Index].costMeq
	if v < 1 {
		v = 1
	}
	dps := int64(q.SurfaceDPS()-q.StunDPS) + int64(q.WaterDPS)*s.navalShare/one + int64(q.AirDPS)*aaNeed/one
	if dps < 0 {
		dps = 0
	}
	return dps * int64(q.HP) * 1000000 / (v * (v + int64(s.p.VRef)))
}

// observeN refreshes the per-think parts of the extended model.
func (s *shared) observeN(b *core.Board) {
	en := b.EnemyGround + b.EnemyNaval + b.EnemyAir
	s.navalShare = b.EnemyNaval * one / (en + 1)
	if s.terr.ready {
		s.selectReach(b)
	}
	// Reach-weighted factory quality, and the reach of a basic land army.
	s.landReach = one
	if s.reach != nil {
		s.landReach = 0
	}
	for _, fi := range s.factories {
		f := s.k.Table.Units[fi]
		si := &s.info[fi]
		si.qualityR = 0
		var bestReach int64
		for _, q := range f.Builds {
			if !s.info[q.Index].combatN {
				continue
			}
			if s.p.Naval == 0 && !s.info[q.Index].combat {
				continue
			}
			r := s.reachOf(q)
			if r > bestReach {
				bestReach = r
			}
			if e := s.effN(q, 0) * r / one; e > si.qualityR {
				si.qualityR = e
			}
		}
		if s.reach != nil && f.Depth == 1 && !si.water && !si.airFac && bestReach > s.landReach {
			s.landReach = bestReach
		}
	}
	// Factory capacity that serves the army: build power weighted by how
	// far each factory's units reach the enemy.
	s.bpFacUseful = 0
	for _, fi := range b.Factories {
		f := &b.O.Own[fi]
		if s.fstateOf(f).dead {
			continue
		}
		s.bpFacUseful += int64(f.Info.BuildPower) * s.factoryReach(f.Info) / one
	}
	if s.p.Tech != 0 {
		s.observeTech(b)
	}
}

// factoryReach is the best reach among a factory's combat products
// (permille, floor 150: whatever it makes still defends home).
func (s *shared) factoryReach(f *aikit.UnitInfo) int64 {
	if s.reach == nil {
		return one
	}
	var best int64 = 150
	for _, q := range f.Builds {
		if s.info[q.Index].combatN {
			if r := s.reachOf(q); r > best {
				best = r
			}
		}
	}
	return best
}
