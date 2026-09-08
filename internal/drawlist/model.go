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

// ClassicModelImage is one owned indexed composition plane for the classic
// executor. Color, Coverage, and Key are mutable working bytes produced while
// recording; they are copied into a Model command so later recording cannot
// alter an already-recorded subject. Key is nil for the painter-order path.
// The placement scalars locate the model-local origin on the destination.
type ClassicModelImage struct {
	Color    []byte
	Coverage []bool
	Key      []byte

	Width, Height    int32
	OriginX, OriginY int32
	AnchorX, AnchorY int32
	Transparent      uint8
}

// Clone returns an image whose mutable planes do not alias the source.
func (i *ClassicModelImage) Clone() *ClassicModelImage {
	if i == nil {
		return nil
	}
	out := *i
	out.Color = append([]byte(nil), i.Color...)
	out.Coverage = append([]bool(nil), i.Coverage...)
	out.Key = append([]byte(nil), i.Key...)
	return &out
}

// ClassicModel is the durable classic replay operand. Shadow has already been
// projected and punched against Body during recording, so replay requires no
// UnitDraw, camera, client model cache, or per-frame lookup. Replay applies
// Shadow, then the single Body blit, then Observer. Observer is optional
// diagnostics-only state: it never affects pixels and is permitted to retain
// its own immutable recording-time evidence.
type ClassicModel struct {
	Shadow *ClassicModelImage
	Body   *ClassicModelImage
	// Trace is the observer-only raster plane. It is separate from Body when a
	// supersampled raster was resolved before the body blit.
	Trace    *ClassicModelImage
	Observer func(trace *ClassicModelImage, indexed []byte, width, height int)
}

// Clone copies every classic pixel plane. Observer is intentionally shared: it
// has no pixel-writing capability and exists only to publish renderer evidence.
func (m *ClassicModel) Clone() *ClassicModel {
	if m == nil {
		return nil
	}
	return &ClassicModel{Shadow: m.Shadow.Clone(), Body: m.Body.Clone(), Trace: m.Trace.Clone(), Observer: m.Observer}
}

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

// CopyClassicImage freezes composition scratch in storage owned by this list.
// Each call gets distinct planes until Reset; retained lists use Clone, which
// copies these planes independently of this reusable recording storage [C-G5].
func (l *List) CopyClassicImage(src ClassicModelImage) *ClassicModelImage {
	if l.classicImageNext == len(l.classicImages) {
		l.classicImages = append(l.classicImages, &ClassicModelImage{})
	}
	dst := l.classicImages[l.classicImageNext]
	l.classicImageNext++
	color := append(dst.Color[:0], src.Color...)
	coverage := append(dst.Coverage[:0], src.Coverage...)
	key := append(dst.Key[:0], src.Key...)
	if src.Key == nil {
		key = nil
	}
	*dst = src
	dst.Color, dst.Coverage, dst.Key = color, coverage, key
	return dst
}
