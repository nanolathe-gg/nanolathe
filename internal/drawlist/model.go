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

// ModelFallbackReason says why a correct CPU-composed model body is not a P2
// GPU candidate. Consumers use it for honest fallback counts; it is not an
// instruction to alter the classic commit.
type ModelFallbackReason uint8

const (
	ModelFallbackNone ModelFallbackReason = iota
	ModelFallbackRevealOrOutline
	ModelFallbackSupersample
	ModelFallbackWaterlineOrDigger
	ModelFallbackStaging
	ModelFallbackNoBodyCommit
)

// ModelGeometry is the immutable, subject-local geometry input for the modern
// model path. Its slices are owned by the packet and never alias the recorder's
// scratch storage. Origin is the image pixel at model-local (0,0), and Anchor
// is that point on the framebuffer. Scale is the raster scale used while the
// faces were projected. P2 emits Scale==1 packets only; supersampled subjects
// carry an explicit fallback reason until their complete resolve is represented.
//
// Ineligible describes a correctly recorded CPU-composed subject for which this
// packet intentionally has no GPU body path. It lets a consumer report fallback
// rather than silently omitting the subject [03 R-REN-03A §4, §6–§8].
type ModelGeometry struct {
	Eligible bool
	Fallback ModelFallbackReason
	Faces    []ModelFace

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
	out.Faces = make([]ModelFace, len(g.Faces))
	for i := range g.Faces {
		out.Faces[i] = g.Faces[i]
		out.Faces[i].Vertices = append([]ModelVertex(nil), g.Faces[i].Vertices...)
	}
	return &out
}
