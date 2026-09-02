package orders

// Deterministic visitor implementations shared by the typed attack and
// repair-patrol rows. The queue binding owns traversal order; these visitors
// deliberately keep their row-specific gate order instead of becoming one
// broad target predicate [04 R-ORD-01 §3, §4, §7][04 R-ORD-02 §4].

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func bindingFor(u *units.Unit) *QueueBinding {
	if q := QueueOfUnit(u); q != nil {
		return q.Binding()
	}
	return nil
}

func scanHostile(b *QueueBinding, actor, candidate *units.Unit) bool {
	if b == nil || actor == nil || candidate == nil {
		return false
	}
	if b.Hostility != nil {
		return b.Hostility(actor, candidate)
	}
	if b.World != nil && b.World.Hostile != nil {
		return b.World.Hostile(actor, candidate)
	}
	return false
}

func wholePlanarDistanceSquared(a, b *units.Unit) int64 {
	if a == nil || b == nil {
		return 0
	}
	dx := int64(int32(a.X.Raw()>>16) - int32(b.X.Raw()>>16))
	dz := int64(int32(a.Z.Raw()>>16) - int32(b.Z.Raw()>>16))
	return dx*dx + dz*dz
}

func withinPlanarRadius(a *units.Unit, x, z numeric.Fixed, radius int32) bool {
	if a == nil || radius < 0 {
		return false
	}
	dx := int64(int32(a.X.Raw()>>16) - int32(x.Raw()>>16))
	dz := int64(int32(a.Z.Raw()>>16) - int32(z.Raw()>>16))
	r := int64(radius)
	return dx*dx+dz*dz <= r*r
}

// The live-unit enumerator answers a stop question, not a continue one:
// QueueBinding.ForEachUnit ends the walk as soon as a visitor returns true and
// asks for the next slot when it returns false. Every visitor below is written
// in these two names rather than in bare booleans, because the two readings are
// indistinguishable at a glance and the wrong one silently truncates a scan to
// its first slot.
const (
	scanNext = false // this slot is not the answer; visit the next one
	scanStop = true  // end the walk here
)

// scanAttackUType visits the live unit pool in its supplied slot order. Every
// gate precedes the score draw: a rejected unit cannot consume simulation RNG.
// The score is d² - RNG(d²/2); equal scores replace the prior winner so later
// slots win ties [04 R-ORD-01 §3].
func scanAttackUType(u *units.Unit, definition uint32) *units.Unit {
	b := bindingFor(u)
	if b == nil {
		return nil
	}
	var best *units.Unit
	var bestScore int64
	forDraw := false
	b.ForEachUnit(func(h pool.Handle, candidate *units.Unit) bool {
		if h == 0 || candidate == nil || !candidate.Alive || candidate == u {
			return scanNext
		}
		if candidate.Def == nil || candidate.Def.UnitDefID != definition {
			return scanNext
		}
		if !scanHostile(b, u, candidate) {
			return scanNext
		}
		// `d²` is the whole-unit squared planar distance, formed at 64 bits
		// [04 R-ORD-01 §3], and the score is `d² − RNG(d²/2)` — subject to the
		// bound-below-2 rule, so a candidate on top of the scanner draws
		// nothing [04 R-ORD-01 §1].
		//
		// TODO(question): the section gives d²'s width (64-bit squares) but not
		// the width the draw's bound is passed at, and the simulation draw
		// helper takes 32 bits. The narrowing below is only reachable for
		// `d²/2` above 2^32, i.e. a separation past 92681 world units — beyond
		// the diagonal of any map the reference install ships — so no stock
		// scenario distinguishes the two. A trace of the bound's argument width
		// at this site would settle it.
		d2 := wholePlanarDistanceSquared(u, candidate)
		bound := uint32(d2 / 2)
		score := d2 - int64(drawBelow(u, bound))
		if !forDraw || score <= bestScore {
			best, bestScore, forDraw = candidate, score, true
		}
		return scanNext
	})
	return best
}

// scanRepairCandidates is the shared repair visitor from [04 R-ORD-02 §4].
// Both patrol rows apply the scanner-owner to candidate-owner diplomacy row;
// ground repeats that direction after its random pick, while VTOL does not.
func scanRepairCandidates(u *units.Unit, radius int32) []*units.Unit {
	b := bindingFor(u)
	if b == nil {
		return nil
	}
	var out []*units.Unit
	b.ForEachUnit(func(h pool.Handle, candidate *units.Unit) bool {
		if h == 0 || candidate == nil || !candidate.Alive || candidate == u {
			return scanNext
		}
		if scanHostile(b, u, candidate) {
			return scanNext
		}
		if candidate.Def == nil {
			return scanNext
		}
		if moverMode(candidate) != 1 {
			return scanNext
		}
		if health16(candidate) >= uint32(candidate.Def.MaxDamage) && candidate.Remaining == 0 {
			return scanNext
		}
		if candidate.LastDamageSide == u.Owner && candidate.LastDamageCause == 5 {
			return scanNext
		}
		if !withinPlanarRadius(u, candidate.X, candidate.Z, radius) {
			return scanNext
		}
		out = append(out, candidate)
		return scanNext
	})
	return out
}

func scanRadiusTarget(u *units.Unit, radius int32, hostileOnly bool) *units.Unit {
	b := bindingFor(u)
	if b == nil {
		return nil
	}
	var found *units.Unit
	b.ForEachUnit(func(h pool.Handle, candidate *units.Unit) bool {
		if h == 0 || candidate == nil || !candidate.Alive || candidate == u || candidate.Def == nil {
			return scanNext
		}
		if hostileOnly && !scanHostile(b, u, candidate) {
			return scanNext
		}
		if withinPlanarRadius(u, candidate.X, candidate.Z, radius) {
			found = candidate
			return scanStop
		}
		return scanNext
	})
	return found
}

func scanAirBasePads(u *units.Unit, radius int32) []*units.Unit {
	b := bindingFor(u)
	if b == nil || u == nil {
		return nil
	}
	var pads []*units.Unit
	b.ForEachUnit(func(h pool.Handle, candidate *units.Unit) bool {
		if h == 0 || candidate == nil || !candidate.Alive || candidate == u || candidate.Owner != u.Owner || candidate.Def == nil {
			return scanNext
		}
		if !candidate.Def.Builder || !candidate.Def.IsAirBase || !candidate.Activated {
			return scanNext
		}
		if !withinPlanarRadius(u, candidate.X, candidate.Z, radius) {
			return scanNext
		}
		pads = append(pads, candidate)
		return scanNext
	})
	return pads
}

func pickRepairCandidate(u *units.Unit, list []*units.Unit) *units.Unit {
	if len(list) == 0 {
		return nil
	}
	idx := drawBelow(u, uint32(len(list)))
	return list[int(idx)]
}

func pickCandidate(u *units.Unit, list []*units.Unit) *units.Unit {
	if len(list) == 0 {
		return nil
	}
	return list[int(drawBelow(u, uint32(len(list))))]
}

// scanFeatureLists samples the square lattice used by the patrol feature
// helper. The argument is a diameter: samples run from -diameter/2 through
// +diameter/2 in 48-world-unit steps. Each sample resolves independently, so
// one feature may appear more than once [04 R-ORD-01 §4, §7].
func scanFeatureLists(u *units.Unit, diameter int32) (energy, metal []FeatureView) {
	b := bindingFor(u)
	if b == nil || b.World == nil || b.World.LookupFeature == nil || u == nil || diameter < 0 {
		return nil, nil
	}
	half := diameter / 2
	cx := int32(u.X.Raw() >> 16)
	cz := int32(u.Z.Raw() >> 16)
	for xoff := -half; xoff <= half; xoff += 48 {
		for zoff := -half; zoff <= half; zoff += 48 {
			x := numeric.Fixed(int64(cx+xoff) << 16)
			z := numeric.Fixed(int64(cz+zoff) << 16)
			feature, ok := b.LookupFeature(world.WorldToCell(x), world.WorldToCell(z))
			if !ok || !feature.Reclaimable || !feature.Autoreclaimable {
				continue
			}
			feature.X = x
			feature.Z = z
			if b.World.TerrainHeight != nil {
				if y, heightOK := b.World.TerrainHeight(x, z); heightOK {
					feature.Y = y
				}
			}
			if feature.Energy > 0 {
				energy = append(energy, feature)
			}
			if feature.Metal > 0 {
				metal = append(metal, feature)
			}
		}
	}
	return energy, metal
}

func playerResources(u *units.Unit) (ResourceView, bool) {
	b := bindingFor(u)
	if b == nil || b.Resources == nil || u == nil {
		return ResourceView{}, false
	}
	return b.Resources(u.Owner)
}

func resourceAtLeastTwenty(stock, capacity float32) bool {
	return stock >= capacity/5
}

func resourceFits(stock, capacity float32, value int32) bool {
	return stock+float32(value) <= capacity
}

// chooseReclaimFeature is the patrol decision tree. Feature lists are built
// before the resource decision, and each non-empty list runs its own strict
// three-sample value tournament [04 R-ORD-01 §4, §7].
func chooseReclaimFeature(u *units.Unit, diameter int32) (FeatureView, bool) {
	resources, ok := playerResources(u)
	if !ok {
		return FeatureView{}, false
	}
	energyList, metalList := scanFeatureLists(u, diameter)
	var energy, metal FeatureView
	var hasEnergy, hasMetal bool
	if len(energyList) > 0 {
		energy, hasEnergy = pickFeatureTournament(u, energyList, false)
	}
	if len(metalList) > 0 {
		metal, hasMetal = pickFeatureTournament(u, metalList, true)
	}
	energyLow := !resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1])
	metalLow := !resourceAtLeastTwenty(resources.Stock[0], resources.Capacity[0])
	if hasMetal && metalLow {
		return metal, true
	}
	if !hasEnergy || !energyLow {
		if hasMetal && resourceFits(resources.Stock[0], resources.Capacity[0], metal.Metal) {
			return metal, true
		}
		if !hasMetal {
			return FeatureView{}, false
		}
		if hasEnergy && resourceFits(resources.Stock[1], resources.Capacity[1], energy.Energy) {
			return energy, true
		}
		return FeatureView{}, false
	}
	if hasEnergy {
		return energy, true
	}
	return FeatureView{}, false
}

func pickFeatureTournament(u *units.Unit, list []FeatureView, metal bool) (FeatureView, bool) {
	if len(list) == 0 {
		return FeatureView{}, false
	}
	best := 0
	var bestValue float32
	for i := 0; i < 3; i++ {
		idx := int(drawBelow(u, uint32(len(list))))
		value := float32(list[idx].Energy)
		if metal {
			value = float32(list[idx].Metal)
		}
		if value > bestValue {
			bestValue, best = value, idx
		}
	}
	return list[best], true
}

func spawnPatrolRepair(u *units.Unit, target *units.Unit, tick uint32) bool {
	if u == nil || target == nil {
		return false
	}
	id := Resolve(8, u, target, nil)
	if id == 0 {
		return false
	}
	q := QueueOfUnit(u)
	if q == nil {
		return false
	}
	q.PushHead(id, NewNodeForOrder(id, target.Handle, target.X, target.Y, target.Z, tick, u.Handle, false))
	return true
}

func spawnPatrolLanding(u *units.Unit, pad *units.Unit, tick uint32) bool {
	if u == nil || pad == nil {
		return false
	}
	id := Lookup("VTOL_Landing")
	if id == 0 {
		return false
	}
	q := QueueOfUnit(u)
	if q == nil {
		return false
	}
	q.PushHead(id, NewNodeForOrder(id, pad.Handle, pad.X, pad.Y, pad.Z, tick, u.Handle, false))
	return true
}

func spawnPatrolReclaim(u *units.Unit, feature FeatureView, air bool, tick uint32) bool {
	if u == nil {
		return false
	}
	name := "Reclaim"
	if air {
		name = "VTOL_Reclaim"
	}
	id := Lookup(name)
	if id == 0 {
		return false
	}
	q := QueueOfUnit(u)
	if q == nil {
		return false
	}
	q.PushHead(id, NewNodeForOrder(id, 0, feature.X, feature.Y, feature.Z, tick, u.Handle, false))
	return true
}
