package movement

import "github.com/nanolathe-gg/nanolathe/internal/units"

// LearnedTerrain is the Modern learned-terrain grid
// (docs/DESIGN_MOVEMENT_PATH.md "Modern learned terrain"): one word per
// mapping block, one bit per player slot, in exactly the frame the route
// search's passability read indexes the mapping word grid — the unsheared
// ground block with the quarter-footprint offset [04 R-PATH-01 §2]. A set bit
// says "this owner has touched this block", never "this block is blocked": the
// search that finds it reads the class layer's stamped value as it would for a
// mapped block, so the answer tracks later terrain and feature changes.
//
// It is deliberately not the visibility publisher's mapping word grid. That
// grid's other readers — the unit visibility sample, the known-site placement
// gate, the aircraft landing accept and the explored-terrain presentation —
// index it by the height-sheared tile, so a bit written at the search's
// unsheared index would announce a different piece of ground as explored to
// every one of them [04 R-PATH-01 §2][03 R-LAYER §1].
//
// The grid is derived runtime state with no retail save field. It is absent
// after a load and is relearned one rejection at a time.
type LearnedTerrain struct {
	w, h  int32
	words []uint16
}

// Known reports whether the player has learned the block.
func (l *LearnedTerrain) Known(bx, bz int32, player uint8) bool {
	if l == nil || bx < 0 || bz < 0 || bx >= l.w || bz >= l.h {
		return false
	}
	return airMappingBitSet(l.words[bz*l.w+bx], player)
}

// learn sets the player's bit and reports whether it was absent.
func (l *LearnedTerrain) learn(bx, bz int32, player uint8) bool {
	if l == nil || player > 9 || bx < 0 || bz < 0 || bx >= l.w || bz >= l.h {
		return false
	}
	word := &l.words[bz*l.w+bx]
	bit := uint16(1) << player
	if *word&bit != 0 {
		return false
	}
	*word |= bit
	return true
}

// learnRejectedFootprint teaches the mover's owner every mapping block the
// route search consults for an anchor inside the rejected footprint rectangle —
// the block of the proposed anchor itself, and those of the cells beside it
// that the footprint would have covered — and that the search still reads as
// unexplored for that owner. A block the owner has mapped is already read from
// the stamped layer, and with no mapping grid bound every block is, so neither
// has anything to teach [04 R-PATH-01 §2]. It reports whether any bit was new.
func (s *System) learnRejectedFootprint(u *units.Unit, anchor Cell, footX, footZ int16) bool {
	if s == nil || s.Terrain == nil || u == nil || u.Owner > 9 {
		return false
	}
	mapping := s.mappingWordSource(u.Handle)
	if mapping == nil {
		return false
	}
	fx, fz := max(int32(footX), 1), max(int32(footZ), 1)
	taught := false
	for dz := int32(0); dz < fz; dz++ {
		for dx := int32(0); dx < fx; dx++ {
			bx, bz := mappingTile(anchor.X+dx, anchor.Z+dz, footX, footZ)
			if word, ok := mapping(bx, bz); !ok || airMappingBitSet(word, u.Owner) {
				continue
			}
			if s.learned == nil {
				w, h := s.Terrain.CellW>>1, s.Terrain.CellH>>1
				if w <= 0 || h <= 0 {
					return false
				}
				s.learned = &LearnedTerrain{w: w, h: h, words: make([]uint16, int(w)*int(h))}
			}
			taught = s.learned.learn(bx, bz, u.Owner) || taught
		}
	}
	return taught
}

// passableLearned is the Modern search read: the retail four-step read, with a
// block the requester's owner has learned answered from the stamped layer
// instead of as unexplored [04 R-PATH-01 §2].
func (l *ClassLayer) passableLearned(x, z int32, footX, footZ int16, player uint8, learned *LearnedTerrain) uint8 {
	v := l.Passable(x, z, footX, footZ, player)
	if v == LayerUnmapped {
		if bx, bz := mappingTile(x, z, footX, footZ); learned.Known(bx, bz, player) {
			return l.Value(x, z)
		}
	}
	return v
}
