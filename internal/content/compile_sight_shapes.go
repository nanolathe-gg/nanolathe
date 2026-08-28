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

// sightShapeCandidates retains the resource-candidate ordering used by the
// content catalog. Retail binds only the plural file; the singular
// anims/vismask.gaf file is a distinct cursor set and is not a sight table
// [03 §3.2].
//
// The table is the `vismask` entry in the plural file. Its ten authored frames
// provide the shape count and geometry; there is no synthesized replacement
// [03 §3.2][fmt gaf].
var sightShapeCandidates = []string{
	"anims/vismasks.gaf",
}

// CompileSightShapes compiles the visibility-mask GAF into the shape table
// [03 §3.2].
//
// The plural file and its `vismask` entry are required inputs to this compiler.
// A missing or malformed resource is reported with its logical path and VFS
// provenance; the singular-file cursor set is never a fallback [03 §3.2].
func CompileSightShapes(fs vfs.FSOps) (*SightShapes, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	out := &SightShapes{}
	out.CanonicalKey = CanonicalKey("vismask")
	name := sightShapeCandidates[0]
	g, err := formats.LoadGAFFile(fs, name)
	if err != nil {
		return nil, requiredContentError(fs, name, "the retail visibility-mask GAF entry vismask", err)
	}
	if g == nil {
		return nil, requiredContentError(fs, name, "the retail visibility-mask GAF entry vismask", fmt.Errorf("GAF decoder returned nil"))
	}
	if info, statErr := fs.Stat(name); statErr == nil {
		out.Provenance = ProvenanceFrom(info)
	}
	entry, ok := g.Find("vismask")
	if !ok || entry == nil {
		return nil, requiredContentError(fs, name, "the retail visibility-mask GAF entry vismask", fmt.Errorf("GAF entry %q not found", "vismask"))
	}
	if len(entry.Frames) != 10 {
		return nil, requiredContentError(fs, name, "the retail visibility-mask GAF entry vismask with ten frames", fmt.Errorf("GAF entry has %d frames", len(entry.Frames)))
	}
	out.Shapes = make([]SightShape, 0, len(entry.Frames))
	for i, ref := range entry.Frames {
		fr := ref.Frame
		if fr == nil || fr.Width == 0 || fr.Height == 0 {
			return nil, requiredContentError(fs, name, "the retail visibility-mask GAF entry vismask with valid frames", fmt.Errorf("GAF frame %d has zero dimensions", i))
		}
		shape := SightShape{
			W:       int32(fr.Width),
			H:       int32(fr.Height),
			AnchorX: int32(fr.XOffset),
			AnchorY: int32(fr.YOffset),
			Opaque:  make([]bool, int(fr.Width)*int(fr.Height)),
		}
		for pixel := range shape.Opaque {
			// Transparent is parallel to Pixels and already accounts for
			// the frame's transparent palette index [fmt gaf].
			if pixel < len(fr.Transparent) {
				shape.Opaque[pixel] = !fr.Transparent[pixel]
			}
		}
		out.Shapes = append(out.Shapes, shape)
	}
	return out, nil
}
