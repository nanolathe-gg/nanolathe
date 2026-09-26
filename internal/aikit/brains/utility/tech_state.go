package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// The tech-2 transition (tech switch). A human moves to the second tier
// around minutes 10–20: a tech-2 factory, a couple of advanced
// constructors, then moho mines over the extractors, fusion plants and
// tech-2 units. Here the first tech-2 factory is wanted on a timeline set
// by income and game time, damped while the enemy army outnumbers ours;
// the rest follows from the ordinary scoring once advanced builders exist
// (their only extractor is the richer one, and upgrades are scored by the
// extra metal they bring).

// techState is the per-think picture of our tech progress.
type techState struct {
	t2Fac   int32 // own tech-2 factories (built or framed)
	advCons int32 // own advanced constructors (built or framed)
	want    int64 // permille: how much the first tech-2 factory is wanted now

}

// setupTech labels advanced constructors (a builder whose extractor out-
// produces the commander's, or that builds a power plant far beyond the
// commander's — a geothermal plant, which needs a vent, does not count) and
// tech-2 factories (a factory that makes one).
func (s *shared) setupTech(k *aikit.Kit, com *aikit.UnitInfo) {
	t := k.Table
	var baseMex int32
	if com != nil {
		for _, p := range com.Builds {
			if p.Role.Has(aikit.RoleExtractor) && (baseMex == 0 || p.MetalMake < baseMex) {
				baseMex = p.MetalMake
			}
		}
	}
	var bestE int32
	if com != nil {
		for _, p := range com.Builds {
			if p.Role.Has(aikit.RoleEnergy) && p.EnergyMake > bestE {
				bestE = p.EnergyMake
			}
		}
	}
	for i, u := range t.Units {
		if !u.Role.Has(aikit.RoleMobile) || !u.Role.Has(aikit.RoleBuilder) || u.Role.Has(aikit.RoleCommander) {
			continue
		}
		for _, p := range u.Builds {
			if (p.Role.Has(aikit.RoleExtractor) && baseMex > 0 && p.MetalMake > baseMex) ||
				(p.Role.Has(aikit.RoleEnergy) && !s.info[p.Index].geo && bestE > 0 && p.EnergyMake >= 25*bestE) {
				s.info[i].advCon = true
				break
			}
		}
	}
	for i, u := range t.Units {
		if !u.Role.Has(aikit.RoleFactory) {
			continue
		}
		for _, q := range u.Builds {
			if s.info[q.Index].advCon {
				s.info[i].t2 = true
				break
			}
		}
	}
}

// observeTech counts our tech progress, maps our extractors to their spots
// (for upgrades) and sets how much the first tech-2 factory is wanted.
func (s *shared) observeTech(b *core.Board) {
	o := b.O
	m := b.K.Map
	st := &s.tech
	st.t2Fac, st.advCons = 0, 0
	for i := range s.spotOwn {
		s.spotOwn[i] = -1
	}
	for i := range o.Own {
		u := &o.Own[i]
		si := &s.info[u.Info.Index]
		if si.t2 {
			st.t2Fac++
		}
		if si.advCon {
			st.advCons++
		}
		if u.Info.Role.Has(aikit.RoleExtractor) {
			if sp := s.spotOf(m, u); sp >= 0 {
				s.spotOwn[sp] = int32(i)
			}
		}
	}
	// Ready when income and game time both say so, and only while it is
	// safe: an enemy army estimate above ours holds it back (fully at 1.6×)
	// unless metal is piling up unspent.
	tt := int64(s.p.TechTime) * 1800
	ready := mul(lin(s.mInc, 15000, 28000), lin(int64(s.tick), tt*2/3, tt))
	safe := one - lin(s.armyRatio, 900, 1600)
	if s.part(gTech) {
		// The human timeline: a tech-2 factory by a median minute 12, held
		// back by pressure on the base or an enemy army estimate of 1.5–3×
		// ours rather than from 0.9× (econ_growth.go).
		tt = tt * 3 / 4
		ready = mul(lin(s.mInc, 12000, 24000), lin(int64(s.tick), tt*2/3, tt))
		safe = min64(one-lin(s.pressure, 200, 600), one-lin(s.armyRatio, 1500, 3000))
	}
	if s.mCap > 0 {
		safe = max64(safe, lin(s.mStock*1000/s.mCap, 600, 950))
	}
	st.want = mul(ready, safe)
}

// advConsWanted is how strongly one more advanced constructor is wanted:
// the first as soon as a tech-2 factory stands, more as income grows.
func (s *shared) advConsWanted() int64 {
	switch n := s.tech.advCons; {
	case n == 0:
		return 1500
	case n == 1:
		return lin(s.mInc, 25000, 40000) * 1200 / one
	case n == 2:
		return lin(s.mInc, 40000, 60000) * 1000 / one
	}
	return 0
}
