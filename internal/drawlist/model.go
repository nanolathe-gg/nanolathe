package drawlist

import "github.com/nanolathe/nanolathe/formats"

// ModelVertex is one projected corner of an authored model polygon. Coordinates
// are composition-image pixels; Key is the signed integer source for the
// subject-local key comparison. A rasterizer interpolates it before narrowing
// to the stored byte. U and V are source texel coordinates. Shade is the physical SHD
// row selected for this corner when Face.Shaded is true.
//
// The packet deliberately keeps the authored polygon ring whole. A device may
// triangulate it for rasterization, but must not use that triangulation to
// reorder faces or replace the subject-local key comparison [03 R-REN-03A
// §2–§3].
type ModelVertex struct {
	X, Y  int32
	Key   int32
	U, V  int32
	Shade uint8
}

// ModelFace is one resolved model primitive. Texture is immutable after asset
// load; nil selects the physical flat Color. Shaded says that the face's source
// texel or flat color is resolved through the per-corner Shade rows.
type ModelFace struct {
	Vertices []ModelVertex
	Texture  *formats.GAFFrame
	Color    uint8
	Shaded   bool
}

// ModelFallbackReason says why modern mode omits a model subject. It is
// diagnostic data, never permission to substitute a CPU-composed image.
type ModelFallbackReason uint8

const (
	ModelFallbackNone ModelFallbackReason = iota
	ModelFallbackRevealOrOutline
	ModelFallbackWaterlineOrDigger
	ModelFallbackStaging
	ModelFallbackNoBodyCommit
)

// ModelReveal describes the resolved height bands of a construction nanoframe.
// Verdicts -2 and -1 mean erase and keep; other values are physical indices
// [03 §5.2]. The presentation producer owns selection of these values.
type ModelReveal struct {
	Line, Floor        uint8
	Below, Band, Above int16
}

type ModelWaterline uint8

const (
	ModelWaterlineNone ModelWaterline = iota
	ModelWaterlineErase
	ModelWaterlineBlue
)

// ModelChild is a separately composed subject in the carrier's key space.
// KeyDelta is compared at signed width before the resulting key byte is stored
// [03 R-REN-03A §4]. Geometry remains in framebuffer placement coordinates.
type ModelChild struct {
	Geometry *ModelGeometry
	KeyDelta int32
}

// ModelGeometry is the immutable, subject-local geometry input for the modern
// model path. Its slices are owned by the packet and never alias the recorder's
// scratch storage. Origin is the image pixel at model-local (0,0), and Anchor
// is that point on the framebuffer. The outer packet describes native output;
// Supersample optionally supplies a doubled local body raster for the resolve.
//
// Ineligible describes a subject modern mode intentionally omits. It lets a
// consumer report the reason without consulting a CPU image [03 R-REN-03A
// §4, §6–§8].
type ModelGeometry struct {
	Eligible bool
	Fallback ModelFallbackReason
	Faces    []ModelFace
	// Shadow is a separately projected silhouette, committed before this body.
	Shadow *ModelGeometry
	// Supersample is an optional doubled body raster in local image coordinates.
	Supersample  *ModelGeometry
	Reveal       *ModelReveal
	Outline      []ModelFace
	Waterline    ModelWaterline
	WaterlineKey uint8
	Digger       bool
	DiggerKey    uint8
	Children     []ModelChild

	Width, Height    int32
	OriginX, OriginY int32
	AnchorX, AnchorY int32
	Scale            int32
	KeyPlane         bool
}

// Clone returns a packet with independently owned face and vertex slices.
// Immutable loaded texture frames remain shared by pointer.
func (g *ModelGeometry) Clone() *ModelGeometry {
	if g == nil {
		return nil
	}
	out := *g
	out.Shadow = g.Shadow.Clone()
	out.Supersample = g.Supersample.Clone()
	out.Children = make([]ModelChild, len(g.Children))
	for i, ch := range g.Children {
		out.Children[i] = ModelChild{Geometry: ch.Geometry.Clone(), KeyDelta: ch.KeyDelta}
	}
	if g.Reveal != nil {
		reveal := *g.Reveal
		out.Reveal = &reveal
	}
	out.Outline = make([]ModelFace, len(g.Outline))
	for i, f := range g.Outline {
		out.Outline[i] = f
		out.Outline[i].Vertices = append([]ModelVertex(nil), f.Vertices...)
	}
	out.Faces = make([]ModelFace, len(g.Faces))
	for i := range g.Faces {
		out.Faces[i] = g.Faces[i]
		out.Faces[i].Vertices = append([]ModelVertex(nil), g.Faces[i].Vertices...)
	}
	return &out
}
