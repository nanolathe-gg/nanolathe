package movement

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// GroupDestination is one actor's destination for an ordinary group move,
// rewritten in place by AssignGroupDestinations.
type GroupDestination struct {
	H    pool.Handle
	X, Z numeric.Fixed
}

// groupSlotRadius bounds the ring search for a free destination footprint, in
// cells. Nanolathe Modern policy tuning (DESIGN_INTERFACE_HUD_INPUT "Modern
// group destination slots").
const groupSlotRadius = 12

// AssignGroupDestinations gives each ground actor of an ordinary group move a
// destination footprint no other actor of the group holds, when the bound
// rules ask for it (DESIGN_INTERFACE_HUD_INPUT "Modern group destination
// slots"). Each actor keeps its own formation goal where that footprint is
// free; otherwise it takes the nearest free one within groupSlotRadius cells,
// and keeps its goal when there is none. Actors claim in travel order — the
// one farthest along the centroid-to-click direction first — so an actor that
// arrives first is not moved off its own goal by one behind it. "Free" means
// passable for the actor's movement class as its owner knows the ground (the
// route search's view: unexplored ground is passable, learned ground is read)
// and not held by a stationary unit outside the group.
//
// w is passed by the command boundary, which runs before the movement sweep
// binds its world. Integer arithmetic only; no RNG; no map is ranged.
func (s *System) AssignGroupDestinations(w *units.World, dests []GroupDestination, clickX, clickZ numeric.Fixed) {
	if s == nil || w == nil || s.Terrain == nil || len(dests) < 2 || !s.rules().GroupDestinationSlots(s) {
		return
	}
	var cx, cz int64
	for _, d := range dests {
		u := w.Unit(d.H)
		if u == nil {
			return
		}
		cx += int64(u.X) >> 16
		cz += int64(u.Z) >> 16
	}
	cx /= int64(len(dests))
	cz /= int64(len(dests))
	dx := int64(clickX)>>16 - cx
	dz := int64(clickZ)>>16 - cz
	type claim struct {
		i          int
		depth, lat int64
	}
	order := make([]claim, len(dests))
	for i, d := range dests {
		u := w.Unit(d.H)
		ux, uz := int64(u.X)>>16, int64(u.Z)>>16
		order[i] = claim{i: i, depth: ux*dx + uz*dz, lat: uz*dx - ux*dz}
	}
	sort.SliceStable(order, func(a, b int) bool {
		if order[a].depth != order[b].depth {
			return order[a].depth > order[b].depth
		}
		return order[a].lat < order[b].lat
	})
	member := make(map[int]bool, len(dests))
	for _, d := range dests {
		member[int(d.H)] = true
	}
	claimed := make(map[Cell]bool, 4*len(dests))
	learned := s.rules().LearnedTerrain(s)
	for _, c := range order {
		d := &dests[c.i]
		u := w.Unit(d.H)
		fx, fz := int32(1), int32(1)
		if coll := handleRow(s.Collisions, d.H); coll != nil {
			fx, fz = max(int32(coll.FootPrintX), 1), max(int32(coll.FootPrintZ), 1)
		}
		profile := s.ProfileFor(d.H)
		layer := s.existingLayer(d.H)
		mapping := s.slotMappingWord(u)
		tx, tz := goalCellForWorld(d.X, fx), goalCellForWorld(d.Z, fz)
		free := func(ax, az int32) bool {
			if !s.slotPassable(layer, mapping, learned, profile, u.Owner, ax, az, fx, fz) {
				return false
			}
			for z := az; z < az+fz; z++ {
				for x := ax; x < ax+fx; x++ {
					cell := Cell{X: x, Z: z}
					if claimed[cell] {
						return false
					}
					if s.Grid == nil {
						continue
					}
					if occ, held := s.Grid.OccupantAt(cell); held && occ > 0 && !member[occ] {
						if o := handleRow(s.Collisions, pool.Handle(occ)); o == nil || o.Speed == 0 {
							return false
						}
					}
				}
			}
			return true
		}
		bx, bz, found := tx, tz, false
		for r := int32(0); r <= groupSlotRadius && !found; r++ {
			best := int64(-1)
			for z := tz - r; z <= tz+r; z++ {
				for x := tx - r; x <= tx+r; x++ {
					if max(absInt32(x-tx), absInt32(z-tz)) != r {
						continue
					}
					ddx, ddz := int64(x-tx), int64(z-tz)
					dist := ddx*ddx + ddz*ddz
					if (best < 0 || dist < best) && free(x, z) {
						best, bx, bz = dist, x, z
					}
				}
			}
			found = best >= 0
		}
		if !found {
			continue
		}
		for z := bz; z < bz+fz; z++ {
			for x := bx; x < bx+fx; x++ {
				claimed[Cell{X: x, Z: z}] = true
			}
		}
		if bx != tx || bz != tz {
			// The footprint centre maps back to the anchor under the commit's
			// quantisation [04 R-COLL-01 §1].
			d.X = numeric.Fixed(int64(bx)<<20 + int64(fx)<<19)
			d.Z = numeric.Fixed(int64(bz)<<20 + int64(fz)<<19)
		}
	}
}

// existingLayer is the actor's class layer if a search has already allocated
// it. A destination query never allocates one: allocation seeds occupant-age
// clocks from the bound world, which the command boundary precedes.
func (s *System) existingLayer(h pool.Handle) *ClassLayer {
	if s == nil || s.layerRegistry == nil {
		return nil
	}
	name := s.classKeyFor(h)
	if name == "" {
		name = scratchLayerKey(s.ProfileFor(h))
	}
	return s.layerRegistry.byName[name]
}

// slotMappingWord is the owner's mapping-word view the route search binds.
func (s *System) slotMappingWord(u *units.Unit) MappingWordSource {
	if u == nil {
		return nil
	}
	if b := airBinding(u); b != nil && b.World != nil && b.World.MappingWord != nil {
		return b.World.MappingWord
	}
	return nil
}

// slotPassable answers whether an anchor is passable as the owner knows the
// ground: the class layer's search read when the layer exists, otherwise the
// same mapping-word gate over the static footprint test.
func (s *System) slotPassable(layer *ClassLayer, mapping MappingWordSource, learned *LearnedTerrain, p Profile, owner uint8, ax, az, fx, fz int32) bool {
	if layer != nil && (layer.mapping != nil || mapping == nil) {
		if learned != nil {
			return layer.passableLearned(ax, az, int16(fx), int16(fz), owner, learned) != LayerBlocked
		}
		return layer.Passable(ax, az, int16(fx), int16(fz), owner) != LayerBlocked
	}
	if ax < 0 || az < 0 || ax+fx > s.Terrain.CellW || az+fz > s.Terrain.CellH {
		return false
	}
	if mapping != nil {
		bx, bz := mappingTile(ax, az, int16(fx), int16(fz))
		if word, ok := mapping(bx, bz); ok && !airMappingBitSet(word, owner) {
			if learned == nil || !learned.Known(bx, bz, owner) {
				return true
			}
		}
	}
	return p.IsPassableFootprint(s.Terrain, ax, az)
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
