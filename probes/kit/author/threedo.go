package author

// 3DO writer — layout per research/formats/3do.md. Coordinates are 16.16
// fixed point; a piece's origin is a pure translation from its parent.

// Vec is a model-space point in world units (converted to 16.16 on write).
type Vec struct{ X, Y, Z float64 }

// Prim is one flat-coloured primitive (no texture).
type Prim struct {
	Color   uint8
	Indexes []uint16 // into the owning piece's vertex array
}

// Piece is a 3DO object.
type Piece struct {
	Name string
	// Offset is the translation from the parent origin (source-space, as
	// authored: Y up, −Z the model's front per [fmt 3do]).
	Offset   Vec
	Vertices []Vec
	Prims    []Prim
	// Selection is the root's OffsetToSelectionPrimitive value. Stock roots
	// author 0 (primitive 0 is the flat X/Z plate) or -1; children store -1.
	Selection int32
	Children  []*Piece
}

// Box appends a closed axis-aligned box to the piece (six flat quads) and
// returns nothing; the box spans [min,max] in piece space. Winding follows
// the retail authored order (counter-clockwise seen from outside, [fmt 3do]
// "Unknowns and caveats").
func (p *Piece) Box(min, max Vec, color uint8) {
	base := uint16(len(p.Vertices))
	p.Vertices = append(p.Vertices,
		Vec{min.X, min.Y, min.Z}, // 0
		Vec{max.X, min.Y, min.Z}, // 1
		Vec{max.X, max.Y, min.Z}, // 2
		Vec{min.X, max.Y, min.Z}, // 3
		Vec{min.X, min.Y, max.Z}, // 4
		Vec{max.X, min.Y, max.Z}, // 5
		Vec{max.X, max.Y, max.Z}, // 6
		Vec{min.X, max.Y, max.Z}, // 7
	)
	quad := func(a, b, c, d uint16) {
		p.Prims = append(p.Prims, Prim{Color: color, Indexes: []uint16{base + a, base + b, base + c, base + d}})
	}
	quad(0, 3, 2, 1) // −Z face (front)
	quad(4, 5, 6, 7) // +Z face
	quad(0, 4, 7, 3) // −X face
	quad(1, 2, 6, 5) // +X face
	quad(3, 7, 6, 2) // +Y face (top)
	quad(0, 1, 5, 4) // −Y face (bottom)
}

// Plate appends a flat X/Z quad at height y — the ground/selection plate
// every stock unit root carries as primitive 0.
func (p *Piece) Plate(halfX, halfZ, y float64, color uint8) {
	base := uint16(len(p.Vertices))
	p.Vertices = append(p.Vertices,
		Vec{-halfX, y, -halfZ}, Vec{halfX, y, -halfZ}, Vec{halfX, y, halfZ}, Vec{-halfX, y, halfZ})
	p.Prims = append(p.Prims, Prim{Color: color, Indexes: []uint16{base, base + 3, base + 2, base + 1}})
}

func fixed(v float64) int32 { return int32(v * 65536) }

// ThreeDOBytes serialises a model whose root is root. Object records are
// 52 bytes (13 × i32); primitive records 32 bytes; vertices 12 bytes; all
// offsets absolute. Sibling lists link the children of one parent.
func ThreeDOBytes(root *Piece) []byte {
	var b buf
	// Pass 1: reserve every object record (depth-first, children after
	// their parent, siblings consecutive) and remember where each sits.
	type slot struct {
		p   *Piece
		at  uint32
		sib int // index of the next sibling slot, -1 = none
		chd int // index of the first child slot, -1 = none
	}
	var slots []slot
	var reserve func(p *Piece) int
	reserve = func(p *Piece) int {
		idx := len(slots)
		slots = append(slots, slot{p: p, at: b.off(), sib: -1, chd: -1})
		for i := 0; i < 13; i++ {
			b.i32(0)
		}
		prev := -1
		for _, c := range p.Children {
			ci := reserve(c)
			if prev < 0 {
				slots[idx].chd = ci
			} else {
				slots[prev].sib = ci
			}
			prev = ci
		}
		return idx
	}
	reserve(root)

	// Pass 2: emit names, vertices, index arrays and primitives, then fill
	// each object record.
	for i := range slots {
		s := &slots[i]
		p := s.p
		nameAt := b.off()
		b.cstr(p.Name)
		b.pad(4)
		vertAt := b.off()
		for _, v := range p.Vertices {
			b.i32(fixed(v.X))
			b.i32(fixed(v.Y))
			b.i32(fixed(v.Z))
		}
		idxAts := make([]uint32, len(p.Prims))
		for pi, pr := range p.Prims {
			idxAts[pi] = b.off()
			for _, ix := range pr.Indexes {
				check(int(ix) < len(p.Vertices), "3do: vertex index out of range in %q", p.Name)
				b.u16(ix)
			}
			b.pad(4)
		}
		primAt := b.off()
		for pi, pr := range p.Prims {
			b.u32(uint32(pr.Color))       // +0x00 ColorIndex
			b.i32(int32(len(pr.Indexes))) // +0x04 NumberOfVertexIndexes
			b.i32(0)                      // +0x08 Always_0
			b.u32(idxAts[pi])             // +0x0C OffsetToVertexIndexArray
			b.i32(0)                      // +0x10 OffsetToTextureName (0 = none)
			b.i32(0)                      // +0x14 Unknown_1
			b.i32(0)                      // +0x18 Unknown_2
			b.i32(1)                      // +0x1C IsColored (bit 0 set: flat colour)
		}
		var sibAt, chdAt uint32
		if s.sib >= 0 {
			sibAt = slots[s.sib].at
		}
		if s.chd >= 0 {
			chdAt = slots[s.chd].at
		}
		rec := s.at
		b.patchU32(rec+0x00, 1)                         // VersionSignature
		b.patchU32(rec+0x04, uint32(len(p.Vertices)))   // NumberOfVertexes
		b.patchU32(rec+0x08, uint32(len(p.Prims)))      // NumberOfPrimitives
		b.patchU32(rec+0x0C, uint32(p.Selection))       // OffsetToSelectionPrimitive
		b.patchU32(rec+0x10, uint32(fixed(p.Offset.X))) // XFromParent
		b.patchU32(rec+0x14, uint32(fixed(p.Offset.Y))) // YFromParent
		b.patchU32(rec+0x18, uint32(fixed(p.Offset.Z))) // ZFromParent
		b.patchU32(rec+0x1C, nameAt)                    // OffsetToObjectName
		b.patchU32(rec+0x20, 0)                         // Always_0
		b.patchU32(rec+0x24, vertAt)                    // OffsetToVertexArray
		b.patchU32(rec+0x28, primAt)                    // OffsetToPrimitiveArray
		b.patchU32(rec+0x2C, sibAt)                     // OffsetToSiblingObject
		b.patchU32(rec+0x30, chdAt)                     // OffsetToChildObject
	}
	return b.Bytes()
}
