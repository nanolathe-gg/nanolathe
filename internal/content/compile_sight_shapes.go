package content

import (
	"fmt"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// SightShape is one authored visibility-mask shape [03 §3.2].
//
// The sprite-mask raster does not synthesize a circle: it indexes an authored
// GAF by the quantized sight radius, and the selected frame supplies "width,
// height, anchor offsets, a transparent palette sentinel, and row-major mask
// bytes" [03 §3.2]. Opaque cells are covered; transparent cells are skipped.
type SightShape struct {
	W, H             int32  // frame dimensions in coverage tiles
	AnchorX, AnchorY int32  // the observer's cell within the frame
	Opaque           []bool // row-major, len == W*H; true means the cell is covered
}

// Covers reports whether the shape covers the cell at (x, y) within the frame.
func (s *SightShape) Covers(x, y int32) bool {
	if s == nil || x < 0 || y < 0 || x >= s.W || y >= s.H {
		return false
	}
	return s.Opaque[y*s.W+x]
}

// SightShapes is the compiled visibility-mask table [03 §3.2].
//
// Shape k covers a radius of k+5 tiles: the -5 in the quantization
// `floor(radius/32) - 5` is an INDEX bias that the frame geometry undoes.
// Reading the index as a radius shrinks every unit's sight by five tiles.
type SightShapes struct {
	DefinitionHeader
	Shapes []SightShape
}

// Count returns the number of authored shapes, which is the clamp bound for
// the sprite-mask quantization [03 §3.2].
func (s *SightShapes) Count() int {
	if s == nil {
		return 0
	}
	return len(s.Shapes)
}

// Shape returns shape i, or nil when out of range.
func (s *SightShapes) Shape(i int) *SightShape {
	if s == nil || i < 0 || i >= len(s.Shapes) {
		return nil
	}
	return &s.Shapes[i]
}

// sightShapeCandidates are the shipped visibility-mask resources, in the order
// they are tried [03 §3.2].
//
// TODO(question): the reference install ships both, with identical frame
// geometry and different opacity — vismasks.gaf's frames are solid squares and
// vismask.gaf's are circular. Which one retail binds is recorded as an open
// residual in [03 §3.2]; the plural spelling matches the handle name, so it is
// tried first. This slice is the one place to flip that decision.
var sightShapeCandidates = []string{
	"anims/vismasks.gaf",
	"anims/vismask.gaf",
}

// CompileSightShapes compiles the visibility-mask GAF into the shape table
// [03 §3.2].
//
// A missing resource is not fatal: fixtures without an anims/ directory
// compile to an empty table, and phase 5 falls back to publishing nothing
// rather than inventing a shape.
func CompileSightShapes(fs vfs.FSOps) (*SightShapes, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	out := &SightShapes{}
	out.CanonicalKey = CanonicalKey("vismask")
	for _, name := range sightShapeCandidates {
		g, err := formats.LoadGAFFile(fs, name)
		if err != nil || g == nil || len(g.Entries) == 0 {
			continue
		}
		if info, statErr := fs.Stat(name); statErr == nil {
			out.Provenance = ProvenanceFrom(info)
		}
		entry := g.Entries[0]
		out.Shapes = make([]SightShape, 0, len(entry.Frames))
		for _, ref := range entry.Frames {
			fr := ref.Frame
			if fr == nil || fr.Width == 0 || fr.Height == 0 {
				continue
			}
			shape := SightShape{
				W:       int32(fr.Width),
				H:       int32(fr.Height),
				AnchorX: int32(fr.XOffset),
				AnchorY: int32(fr.YOffset),
				Opaque:  make([]bool, int(fr.Width)*int(fr.Height)),
			}
			for i := range shape.Opaque {
				// Transparent is parallel to Pixels and already accounts for
				// the frame's transparent palette index [fmt gaf].
				if i < len(fr.Transparent) {
					shape.Opaque[i] = !fr.Transparent[i]
				}
			}
			out.Shapes = append(out.Shapes, shape)
		}
		if len(out.Shapes) > 0 {
			return out, nil
		}
	}
	return out, nil
}
