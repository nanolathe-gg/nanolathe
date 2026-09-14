package drawlist

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

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
	// Height is model-relative height in recording view-scale pixels. It is
	// independent of Key and is unchanged by the doubled raster (§22.4).
	Height float32
}

// ModelFace is one resolved model primitive. Texture is immutable after asset
// load; nil selects the physical flat Color. Shaded says that the face's source
// texel or flat color is resolved through the per-corner Shade rows.
type ModelFace struct {
	Vertices []ModelVertex
	Texture  *formats.GAFFrame
	Color    uint8
	Shaded   bool
	// Normal is the outward unit normal in world X, world Z, height axes,
	// used only by Enhanced surface lighting (DESIGN_GPU_RENDERER §22.4).
	Normal [3]float32
	// Material is a curated Enhanced art annotation (GPU design §29).
	// Zero leaves the existing lighting unchanged.
	Material uint8
}

// Authored presentation finishes, not retail material metadata (GPU design §29).
const (
	ModelMaterialDefault uint8 = iota
	ModelMaterialMetal
	ModelMaterialPaint
)

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
	// Blit is the nearest-neighbour view scale the blit applies: zero or
	// native draws one framebuffer pixel per image pixel; a magnified scale
	// draws each image pixel over the block its Project span covers about the
	// anchor — 2x2 at 2x, alternately one and two wide at 1.5x. Original at a
	// magnified scale rasterizes the model at its native size and scales it
	// here, so the classic frame is a pure nearest upscale of the native one
	// (DESIGN_GPU_RENDERER §14.2).
	Blit camera.ViewScale
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
	// Cloaked selects the body ALP blit after visibility admission [03 R-RAST-01 §7].
	Cloaked bool
	Shadow  *ClassicModelImage
	Body    *ClassicModelImage
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
	return &ClassicModel{Cloaked: m.Cloaked, Shadow: m.Shadow.Clone(), Body: m.Body.Clone(), Trace: m.Trace.Clone(), Observer: m.Observer}
}

// ModelChild is a separately composed subject in the carrier's key space.
// KeyDelta is compared at signed width before the resulting key byte is stored
// [03 R-REN-03A §4]. Geometry remains in framebuffer placement coordinates.
type ModelChild struct {
	Geometry *ModelGeometry
	KeyDelta int32
}

// ModelCacheLane names which of one retained object's rasters a packet carries.
// A body and its shadow are two independently projected rasters of the same
// retained subject, each with a revision of its own, so the lane is what keeps a
// shadow from ever answering to a body's slot key
// (docs/DESIGN_GPU_RENDERER.md §13.12 "Shadows — contract P4").
type ModelCacheLane uint8

const (
	// ModelCacheLaneBody is the retained cached-lane composition.
	ModelCacheLaneBody ModelCacheLane = iota
	// ModelCacheLaneShadow is the retained quarter-sheared silhouette
	// projection [03 R-REN-03D §2].
	ModelCacheLaneShadow
)

// ModelCacheKey names the retained lane a packet was rebased from, so an
// executor can recognise a subject whose raster inputs have not changed since
// the last frame and keep the raster it already holds
// (docs/DESIGN_GPU_RENDERER.md §13.12).
//
// Body is a serial the recorder assigns once per retained body object, Lane says
// which of that object's rasters this packet is, and Revision changes every time
// the recorder stores new geometry for that lane, so an equal triple means
// literally the same retained faces. HalfX/HalfY are the frame's half-pixel
// offset, which the rebase adds to the DOUBLED lane's corners alone (§17) and
// which the packet's own origin therefore does not imply.
//
// A zero Body means "not reusable" and is the value of every packet whose raster
// inputs the recorder cannot prove stable: the direct lanes, children, a shadow
// whose subject has no retained body, and any retained body carrying a reveal,
// an outline or a live lane this frame.
type ModelCacheKey struct {
	Body         uint64
	Revision     uint64
	HalfX, HalfY int32
	Lane         ModelCacheLane
}

// Reusable reports whether the key identifies a retained body at all.
func (k ModelCacheKey) Reusable() bool { return k.Body != 0 }

// ModelGeometry is the immutable, subject-local geometry input for the modern
// model path. Recorded slices remain valid until the next recording pass;
// Clone owns independent slices for retained consumers. Origin is the image
// pixel at model-local (0,0), and Anchor
// is that point on the framebuffer. The outer packet describes native output;
// Supersample optionally supplies the subject's doubled raster — its cached and
// live faces, outline and reveal at twice the scale, placed with the subject's
// half-pixel offset — which the executor rasterizes instead of the native faces
// and resolves two-to-one with fractional coverage (DESIGN_GPU_RENDERER §17).
//
// Ineligible describes a subject modern mode intentionally omits. It lets a
// consumer report the reason without consulting a CPU image [03 R-REN-03A
// §4, §6–§8].
type ModelGeometry struct {
	// Cloaked selects the final body blend, never a retained-raster input
	// [03 R-RAST-01 §7]. A staged carrier owns its combined body blend.
	Cloaked bool
	// WreckHeat carries modern-only cooling operands refreshed at recording.
	// Emission is preweighted RGB; strength affects only distortion amplitude.
	// These are presentation design choices (GPU design §28), never sim inputs.
	WreckEmission                                    [3]float32
	WreckHeatStrength, WreckHeatTime, WreckHeatScale float32

	Eligible bool
	Fallback ModelFallbackReason
	Faces    []ModelFace
	// LiveFaces are the current unshaded DontCache lane. Faces remain the
	// retained cached lane, including the optional structure supersample.
	LiveFaces []ModelFace
	// Shadow is a separately projected silhouette, committed before this body.
	Shadow *ModelGeometry
	// Supersample is the optional doubled raster in local image coordinates,
	// carrying its own Faces, LiveFaces, Outline and Reveal (§17).
	Supersample  *ModelGeometry
	Reveal       *ModelReveal
	Outline      []ModelFace
	Waterline    ModelWaterline
	WaterlineKey uint8
	Digger       bool
	DiggerKey    uint8
	Children     []ModelChild
	// Silhouette marks a shadow packet that is the finished body's own
	// silhouette rather than a projection of its own, which is retail's
	// Digger and mobile shadow [03 R-REN-03D §1]: the executor reads the body's
	// raster at this packet's placement, so the packet carries no faces, only
	// the body's box and the shadow anchor. SilhouetteClip erases the
	// silhouette at and below that height key; zero is no clip.
	Silhouette     bool
	SilhouetteClip uint8

	Width, Height    int32
	OriginX, OriginY int32
	AnchorX, AnchorY int32
	Scale            int32
	KeyPlane         bool
	// WorldHeight is this packet's current absolute origin height in recording
	// view-scale pixels. Retained corners carry relative heights; placement
	// refreshes this value, including when the retained body does not rebuild.
	WorldHeight float32
	// AircraftShadowHeight and AircraftShadowScale are Enhanced placement inputs
	// (GPU design §34). Height is clearance above the higher of terrain and sea
	// under the aircraft, in recording pixels; zero keeps the ordinary shadow.
	AircraftShadowHeight, AircraftShadowScale float32
	// ReflectWater admits this body over ordinary water. ReflectionSea is the
	// absolute sea plane in recording-scale pixels; individual corners are
	// clipped against it by the Enhanced executor (GPU design §26).
	ReflectWater  bool
	ReflectionSea float32
	// Cache identifies the retained cached-lane raster this packet carries, or
	// is zero when the packet is not reusable across frames (§13.12).
	Cache ModelCacheKey
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
	out.LiveFaces = make([]ModelFace, len(g.LiveFaces))
	for i := range g.LiveFaces {
		out.LiveFaces[i] = g.LiveFaces[i]
		out.LiveFaces[i].Vertices = append([]ModelVertex(nil), g.LiveFaces[i].Vertices...)
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
