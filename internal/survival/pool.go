package survival

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Domain is how a wave unit travels (DESIGN_SURVIVAL §5).
type Domain uint8

const (
	Ground Domain = iota
	Amphibious
	Hover
	Naval
	Air
	DomainCount
)

func (d Domain) String() string {
	switch d {
	case Ground:
		return "ground"
	case Amphibious:
		return "amphibious"
	case Hover:
		return "hover"
	case Naval:
		return "naval"
	case Air:
		return "air"
	}
	return "unknown"
}

// maxTierDepth bounds the tier walk for a malformed or cyclic build tree.
const maxTierDepth = 16

// Unit is one wave-pool entry.
type Unit struct {
	Key    string // canonical catalog key
	Def    *content.UnitDef
	Tier   int
	Domain Domain
	Cost   int64 // metal + energy / R (§5)
}

// Pool is the per-battle wave pool, ordered by key.
type Pool struct {
	Units       []Unit
	MaxTier     int
	Tier1Median int64 // median cost of the tier-1 units, the budget's unit

	// RatioE and RatioM are the median unit's (energy, metal) pair: R is
	// RatioE/RatioM, kept as a pair so every cost stays integer arithmetic.
	RatioE, RatioM int64
}

// TierMedian is the median cost of a tier's pool units, 0 for an empty tier.
func (p *Pool) TierMedian(tier int) int64 {
	var costs []int64
	for _, u := range p.Units {
		if u.Tier == tier {
			costs = append(costs, u.Cost)
		}
	}
	if len(costs) == 0 {
		return 0
	}
	sort.Slice(costs, func(i, j int) bool { return costs[i] < costs[j] })
	return costs[len(costs)/2]
}

// Cost is a definition's wave cost, metal + energy / R (DESIGN_SURVIVAL §5).
// It prices any unit, so the player's losses use the same scale as the
// attacker's.
func (p *Pool) Cost(def *content.UnitDef) int64 {
	if def == nil {
		return 0
	}
	c := int64(def.BuildCostMetal)
	if p.RatioE > 0 {
		c += int64(def.BuildCostEnergy) * p.RatioM / p.RatioE
	}
	if c < 1 {
		c = 1
	}
	return c
}

// Products lists what a builder's menu offers under the bound rules
// (construction.BuildProducts in production).
type Products func(menu *content.BuildMenuPage) []string

// Tiers derives every reachable unit's tech tier from the build tree
// (DESIGN_SURVIVAL §5): each side's commander is tier 0; a product entered
// through an immobile builder (a factory) costs one tier, one entered through a
// mobile builder costs nothing; a unit's tier is the cheapest route from any
// commander. Tiers are walked in ascending order and each tier's builders in
// discovery order, so the result never depends on map iteration.
func Tiers(cat *content.Catalog, products Products) map[string]int {
	tier := make(map[string]int) // lookup only; never ranged
	if cat == nil {
		return tier
	}
	var frontier []string
	for _, side := range cat.Sides {
		if side == nil || side.Commander == "" {
			continue
		}
		frontier = append(frontier, content.CanonicalKey(side.Commander))
	}
	for t := 0; t <= maxTierDepth && len(frontier) > 0; t++ {
		var next []string
		queue := frontier
		for i := 0; i < len(queue); i++ {
			key := queue[i]
			if _, seen := tier[key]; seen {
				continue
			}
			def, ok := cat.Unit(key)
			if !ok || def == nil {
				continue
			}
			tier[key] = t
			if !def.Builder {
				continue
			}
			for _, p := range products(cat.BuildMenus[key]) {
				pk := content.CanonicalKey(p)
				if _, seen := tier[pk]; seen {
					continue
				}
				if mobile(def) {
					queue = append(queue, pk)
				} else {
					next = append(next, pk)
				}
			}
		}
		frontier = next
	}
	return tier
}

// DomainOf classifies a definition's travel domain. Naval is a floater or a
// movement class that requires water depth; everything else that is neither
// flying nor hovering is ground or amphibious.
func DomainOf(def *content.UnitDef, classes map[string]*content.MovementClass) Domain {
	switch {
	case def.CanFly:
		return Air
	case def.CanHover:
		return Hover
	case def.Amphibious:
		return Amphibious
	case def.Floater:
		return Naval
	}
	if mc := classes[content.CanonicalKey(def.MovementClass)]; mc != nil && mc.MinWaterDepth > 0 {
		return Naval
	}
	if def.MovementClass == "" && def.MinWaterDepth > 0 {
		return Naval
	}
	return Ground
}

// mobile reports a unit that moves. Stock factories author canmove, so the
// structure test is bmcode, as the spawn command's placement uses it.
func mobile(def *content.UnitDef) bool {
	return def != nil && def.CanMove && def.BMCode != 0
}

// eligible is the wave-pool filter: a mobile, non-builder, non-commander unit
// with a positive metal cost and at least one weapon that can hurt a base —
// not an interceptor and not anti-air only. Anti-nukes and pure anti-air
// units would spend a wave's budget on nothing.
func eligible(def *content.UnitDef) bool {
	if !mobile(def) || def.Builder || def.Commander || def.BuildCostMetal <= 0 {
		return false
	}
	for _, w := range [3]*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
		if w != nil && w.ID != 0 && !w.Interceptor && !w.ToAirWeapon {
			return true
		}
	}
	return false
}

// BuildPool derives the wave pool from the catalog (DESIGN_SURVIVAL §5).
func BuildPool(cat *content.Catalog, products Products) Pool {
	var pool Pool
	if cat == nil {
		return pool
	}
	tiers := Tiers(cat, products)
	for _, key := range cat.SortedUnitKeys() {
		t, ok := tiers[key]
		if !ok || t < 1 {
			continue
		}
		def, _ := cat.Unit(key)
		if !eligible(def) {
			continue
		}
		pool.Units = append(pool.Units, Unit{Key: key, Def: def, Tier: t, Domain: DomainOf(def, cat.Movement)})
		if t > pool.MaxTier {
			pool.MaxTier = t
		}
	}
	// R is the median energy-to-metal ratio over the pool, kept as the median
	// unit's own (energy, metal) pair so the cost stays integer arithmetic.
	type pair struct{ e, m int64 }
	var ratios []pair
	for _, u := range pool.Units {
		e, m := int64(u.Def.BuildCostEnergy), int64(u.Def.BuildCostMetal)
		if e > 0 && m > 0 {
			ratios = append(ratios, pair{e, m})
		}
	}
	sort.SliceStable(ratios, func(i, j int) bool { return ratios[i].e*ratios[j].m < ratios[j].e*ratios[i].m })
	if len(ratios) > 0 {
		med := ratios[len(ratios)/2]
		pool.RatioE, pool.RatioM = med.e, med.m
	}
	var tier1 []int64
	for i := range pool.Units {
		u := &pool.Units[i]
		u.Cost = pool.Cost(u.Def)
		if u.Tier == 1 {
			tier1 = append(tier1, u.Cost)
		}
	}
	sort.Slice(tier1, func(i, j int) bool { return tier1[i] < tier1[j] })
	if len(tier1) > 0 {
		pool.Tier1Median = tier1[len(tier1)/2]
	}
	return pool
}
