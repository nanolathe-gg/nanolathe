package drawlist

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Rect is an inclusive-origin, exclusive-extent screen rectangle in whole
// pixels: the pixels covered are [X, X+W) by [Y, Y+H). It is the recorded
// form of the clip and blit rectangles the byte writers compute.
type Rect struct {
	X, Y, W, H int32
}

// BlitKind selects which GAF blitter family a Sprite replays as
// (docs/DESIGN_GPU_RENDERER.md §2.1). The kinds include the keyed, the
// ALP-tinted, the LHT-lit and the scaled blitters [03 R-COMP-01 §2]
// [03 R-FX-02 §3].
type BlitKind uint8

const (
	// BlitKeyed is the plain keyed blit: every source byte equal to the
	// transparent key is skipped, all others are written [03 R-RAST-01 §6].
	BlitKeyed BlitKind = iota
	// BlitTinted folds the source through the ALP alpha table against the
	// destination pixel — a destination-reading blit [03 R-COMP-01 §2].
	BlitTinted
	// BlitLit folds the source through one LHT light-table row against the
	// destination pixel; the row is Sprite.LightRow [03 §4.3].
	BlitLit
	// BlitScaled samples Sprite.Src into Sprite.Dst, the scaled GAF blit.
	BlitScaled
	// BlitFeatureNormal is the 2D feature GAF sprite copy (trees, rocks and
	// sprite-form wrecks): Sprite.Trans selects the ALP-tinted primitive for a
	// static feature whose definition sets animtrans; otherwise the non-key source
	// indices copy opaquely at the already-offset top-left. Live event cursors
	// always take the opaque route [03 R-RAST-01 §6][fmt gaf].
	BlitFeatureNormal
	// BlitFeatureShadow is the feature GAF sprite's shadow pass. Sprite.Trans
	// selects ALP[source*256+destination]; when clear, every non-key source byte
	// is copied opaquely. It is emitted before BlitFeatureNormal for the same
	// feature [03 §5.3.1][R-REN-03D §4].
	BlitFeatureShadow
)

// SpriteLightingKind identifies actual effect producers for Enhanced surface
// lighting. It does not alter the sprite's existing blit or glow (§19, §22.4).
type SpriteLightingKind uint8

const (
	SpriteLightingNone SpriteLightingKind = iota
	SpriteLightingExplosion
	SpriteLightingSmoke
	// SpriteLightingFire is a burning feature's flame strip art: the standing
	// flame family, which the producer has already admitted through the
	// one-point coverage gate (§31).
	SpriteLightingFire
	// SpriteLightingProjectile is a bright projectile body sprite — a plasma
	// shell or flare — never its ground shadow and never muzzle-flash effect
	// art, which the explosion path already carries (§31).
	SpriteLightingProjectile
	// SpriteLightingSpark is a flame-stream TRAIL particle: the flame a burning
	// debris piece or a flame-stream segment drags behind it. It stands for a
	// spark in flight rather than a burning place, so it takes its reach from
	// its own art and never the standing fire's wide floor (§31.7).
	SpriteLightingSpark
)

// Emitter reports whether the kind is a light SOURCE rather than a receiver.
// Smoke is a receiver and is never promoted (§23.1 BL1, §31).
func (k SpriteLightingKind) Emitter() bool {
	return k == SpriteLightingExplosion || k == SpriteLightingFire ||
		k == SpriteLightingProjectile || k == SpriteLightingSpark
}

// Sprite records one GAF-frame blit (docs/DESIGN_GPU_RENDERER.md §2.1). The
// frame reference is immutable after load; every carried byte is physical
// [C-G2].
type Sprite struct {
	// Frame is the resolved GAF frame; its Pixels are physical indices already
	// [C-G2]. It is nil for a PCX blit, where PCX carries the source instead.
	Frame *formats.GAFFrame
	// PCX is the resolved PCX image for the opaque frontend-background blit, used
	// when Frame is nil [fmt pcx][07 "Retail palette contract"]. It is an additive
	// discriminator (WU-1.7b): Sprite cannot carry a PCX in Frame, so the executor
	// routes a Sprite whose PCX is non-nil to the PCX byte writer regardless of
	// Kind, honoring HasClip/Clip. PCX pixels are physical indices already [C-G2].
	PCX *formats.PCX
	// X, Y is the destination top-left in screen pixels.
	X, Y int32
	// Clip is the destination clip rectangle; HasClip is false when the blit is
	// unclipped.
	Clip    Rect
	HasClip bool
	// Kind selects the blitter family [03 R-COMP-01 §2].
	Kind BlitKind
	// Anchored selects, for a BlitKeyed sprite, whether the frame's authored
	// XOffset/YOffset are subtracted before the pixels are written (WU-1.7b). It
	// is the additive discriminator between the two keyed placement contracts: an
	// Anchored keyed blit is UIBlitAnchor — the frame-anchor placement the effect,
	// projectile and battle-shell art use [fmt gaf][07 §6] — while a non-anchored
	// keyed blit is UIBlit, the plain placement whose rectangle is the contract
	// (the software cursor and .GUI controls) [07 §4]. It is ignored for the other
	// kinds and for a PCX blit.
	Anchored bool
	// LightRow is the LHT row used when Kind is BlitLit [03 §4.3].
	LightRow uint8
	// Src and Dst are the source and destination rectangles used when Kind is
	// BlitScaled; both are ignored for the other kinds.
	Src, Dst Rect
	// Key is the transparent index the keyed path skips [03 R-RAST-01 §6].
	Key uint8
	// Trans is the authored translucent flag for the feature GAF blitter, used
	// when Kind is BlitFeatureNormal or BlitFeatureShadow (WU-1.7c). It is the
	// per-feature ShadTrans (shadow pass) or AnimTrans (normal pass) value the
	// direct blit call carried. For either static feature frame it selects the
	// ALP-tinted rather than opaque keyed primitive [03 R-RAST-01 §6]
	// [03 §5.3.1][R-REN-03D §4].
	Trans bool
	// Emissive marks a sprite whose bright texels are a light source for the
	// Enhanced glow layer: effect, projectile and strip art
	// (docs/DESIGN_GPU_RENDERER.md §19). It changes nothing about how the sprite
	// itself is written; the classic executor and every parity fixture ignore it.
	Emissive     bool
	LightingKind SpriteLightingKind
	// WorldHeight is absolute height in recording view-scale pixels;
	// LightingScale is recording pixels per world pixel. Neither includes
	// supersampling or a subsequent executor world transform.
	WorldHeight, LightingScale float32
	// LightingGround is the terrain height UNDER the source, in the same units
	// as WorldHeight. The ground pass attenuates by the source's height above
	// it rather than above the sea datum, which is the receiver height §31.3
	// had to do without. Zero leaves the datum measurement every producer that
	// does not carry one keeps (§31.7).
	LightingGround float32
	// LightingSize is an explosion sequence's maximum authored extent in native
	// world pixels, independent of the current animation frame or Distortion.
	// Zero retains frame-sized fallback for sources without sequence metadata.
	LightingSize float32
	// LightingTime is committed ticks plus the presentation fraction, wrapped
	// to a bounded window, for a source whose emission flickers (§31). It is
	// zero for every other sprite and classic ignores it.
	LightingTime float32
	// LightingAge is explosion age in committed ticks plus presentation fraction,
	// independent of Distortion. Presence distinguishes frame zero from missing
	// timing in detached sources (GPU design §31.6).
	LightingAge    float32
	HasLightingAge bool
	// LightingFade is the source's own remaining emission, 1 while it has life
	// to spare and falling to 0 as its art retires. It lets a terrain pool
	// follow the art out instead of switching off with the particle (§31.7).
	// Presence distinguishes a spent source from one that carries no fade at
	// all, exactly as HasLightingAge does above.
	LightingFade    float32
	HasLightingFade bool
	// ReflectionHeight is the projectile body anchor above sea in recording
	// pixels. Ground shadows never ReflectWater (GPU design §26).
	ReflectWater     bool
	ReflectionHeight float32
	// BlastAge is elapsed simulation ticks plus presentation fraction; BlastSize
	// is the maximum authored animation extent in world pixels (GPU design §25).
	// Zero size disables refraction; classic ignores both.
	BlastAge, BlastSize float32
	// Authored weapon scalars copied at explosion admission; no source-handle
	// lookup or damage inference from shared artwork (GPU design §25.2).
	HasBlastProfile                bool
	BlastAreaOfEffect, BlastDamage int32
	// HeatSource and HeatTime drive the modern burning-feature shimmer (GPU design §27).
	// Time is committed ticks plus presentation fraction; classic ignores both.
	HeatSource bool
	// SubmergedGround lets the Enhanced GPU water pass include fully
	// submerged, short nonblocking feature art in its terrain input. Classic
	// retains the recorded order and ignores this presentation-only hint.
	SubmergedGround bool
	HeatTime        float32
	// Pal is the palette a BlitLit sprite resolves its LHT row against; nil for
	// every other kind (WU-1.8). Carrying it on the record makes the lit glyph
	// blit self-contained under deferred replay: the classic sink reads Pal here
	// instead of a client scratch field, so a list holding several lit blits with
	// differing palettes replays each against the palette its caller installed
	// [03 §4.3.1]. It is a pointer to immutable-after-load palette tables, so the
	// struct stays comparable [C-G2].
	Pal *palette.Tables
}

// Glyphs records one run of FNT text (docs/DESIGN_GPU_RENDERER.md §2.1). The
// classic executor replays it as the FNT blitter [03 §7.1].
type Glyphs struct {
	// Font is the resolved FNT reference, immutable after load.
	Font *formats.FNT
	// Text is the string to lay out from the baseline origin.
	Text string
	// X, Y is the baseline origin in screen pixels.
	X, Y int32
	// Color is the physical palette index the glyph pixels are written with,
	// already resolved through the logical map [C-G2][03 §4.3].
	Color uint8
	// MaxWidth is the retail control width the FNT rasterizer truncates the run
	// to before it clips to the framebuffer [07 §7]. Its zero value is the
	// health-bar control-group digit's contract, whose direct call passed
	// max-width 0 (no truncation) [03 R-FX-01 §6A]; the frontend text paths carry
	// the authored gadget width here instead.
	MaxWidth int32
	// Clip confines the rasterized glyph pixels after the retail width
	// truncation. HasClip is false for every existing call site.
	Clip    Rect
	HasClip bool
}

// FillStyle selects which rectangle writer a Fill replays as
// (docs/DESIGN_GPU_RENDERER.md §2.1).
type FillStyle uint8

const (
	// FillSolid writes Index into every pixel of the rect (fillIndexedRect).
	FillSolid FillStyle = iota
	// FillOutline writes Index along the rect's edge only (frameIndexedRect).
	FillOutline
	// FillLitRect folds the rect through one LHT row against the destination
	// (the UI light rect): the level in Fill.Level selects the LHT row directly
	// and each pixel is rewritten in place [03 §4.3.1].
	FillLitRect
	// FillShadeRect folds the rect through the signed full-screen fade table (the
	// UI shade rect): a negative Fill.Level addresses SHD row level+32 (clamped
	// at -32), a non-negative level uses the brighten-only LHT row
	// [03 R-COMP-02 §5].
	FillShadeRect
	// FillSolidInclusive writes Index into every pixel of the rect through the
	// INCLUSIVE-bounds solid filler (fillRectInclusive), whose framebuffer
	// clamp-and-recheck clipping differs from FillSolid's fillIndexedRect; the
	// health bar's two rectangles use it [03 R-FX-01 §6][R-P0-19-P]. Rect
	// carries the covered pixels in the family's standard extent form, so the
	// inclusive span [left..right] x [top..bottom] the byte writer fills is
	// exactly [X, X+W) x [Y, Y+H): the executor recovers right = X+W-1,
	// bottom = Y+H-1.
	FillSolidInclusive
	// FillFrameInclusive writes Index along the rect's edge only through the
	// clipped inclusive-frame writer (drawIndexedFrameInclusive), which clips
	// each edge independently against Fill.Clip rather than intersecting first;
	// the drag-selection rectangle uses it [R-SEL-02A]. Rect and Clip both carry
	// inclusive spans in extent form, recovered as for FillSolidInclusive.
	FillFrameInclusive
)

// Fill records one indexed rectangle (docs/DESIGN_GPU_RENDERER.md §2.1). Index
// is a physical palette byte [C-G2].
type Fill struct {
	// Nano marks a visibility-admitted construction particle for Enhanced glow
	// and local lighting (GPU design §23.5). Classic ignores this metadata.
	Nano bool
	// NanoRamp is the optional team ramp; its mean colours broad lighting
	// without following the particle shimmer (GPU design §23.6).
	NanoRamp [7]uint8
	NanoTeam bool
	// NanoSubmerged suppresses Enhanced emission beneath the wet sea surface;
	// the original particle core remains unchanged (GPU design §23.5).
	NanoSubmerged bool
	// WorldHeight and LightingScale use record pixels, as on Sprite.
	WorldHeight, LightingScale float32
	// Rect is the destination rectangle in screen pixels.
	Rect Rect
	// Index is the physical fill/edge palette byte [C-G2].
	Index uint8
	// Style selects the writer [03 §4.3].
	Style FillStyle
	// Row is the LHT/SHD table row used when Style is FillLitRect or
	// FillShadeRect [03 §4.3].
	Row uint8
	// Level is the signed light/shade level used when Style is FillLitRect or
	// FillShadeRect. Unlike Row it can be negative — the shade rect selects an SHD
	// row for a negative level and an LHT row otherwise — so it carries the raw
	// level the executor resolves to a table row exactly as the byte writer did
	// [03 §4.3.1][03 R-COMP-02 §5]. Other styles ignore it (its zero value).
	Level int32
	// Clip also carries the recorded viewport for Nano lighting preparation.
	// Otherwise it is the inclusive clip rectangle used only when Style is
	// FillFrameInclusive: drawIndexedFrameInclusive clips each edge against it
	// independently [R-SEL-02A]. Like Rect it is carried in extent form, so the
	// inclusive clip bounds are recovered as [X, X+W-1] x [Y, Y+H-1]. Other
	// styles ignore it (its zero value).
	Clip Rect
	// Pal is the palette a FillLitRect or FillShadeRect resolves its LHT/SHD row
	// against; nil for the destination-independent styles (WU-1.8). Carrying it on
	// the record makes the UI light/shade rect self-contained under deferred
	// replay: the classic sink reads Pal here instead of a client scratch field,
	// so a list holding several lit/shade fills with differing palettes replays
	// each against the palette its caller installed [03 §4.3.1][03 R-COMP-02 §5].
	// It is a pointer to immutable-after-load palette tables, so the struct stays
	// comparable [C-G2].
	Pal *palette.Tables
}

// Line records one indexed line (docs/DESIGN_GPU_RENDERER.md §2.1), replayed as
// drawIndexedLine. Index is a physical palette byte [C-G2].
type Line struct {
	X0, Y0, X1, Y1 int32
	Index          uint8
	// Emissive marks a line that is a light source for the Enhanced glow layer:
	// the beam and segment strokes of the projectile renderer, never the
	// selection quad or a path (docs/DESIGN_GPU_RENDERER.md §19). The classic
	// executor ignores it.
	Emissive bool
	// Reflection heights follow the recorded endpoints, in pixels above sea.
	// Enhanced clips interpolation below the plane (GPU design §26).
	ReflectWater                         bool
	ReflectionHeight0, ReflectionHeight1 float32
	// WorldHeight0/WorldHeight1 are the endpoints' ABSOLUTE heights in recording
	// view-scale pixels, as Sprite.WorldHeight is — not the above-sea values the
	// reflection fields carry. An emissive stroke needs a physical height to
	// light from (§31); classic ignores both. LightingScale is recording pixels
	// per world pixel, as on Sprite.
	WorldHeight0, WorldHeight1, LightingScale float32
}

// Point is one packed (x, y, operand) triple. The Index field is a physical
// palette byte for a plain point and, for a destination-reading kind, the op's
// source operand instead: an LHT row for PointLit [C-G2][03 §4.3.1].
type Point struct {
	X, Y  int32
	Index uint8
}

// PointKind selects how a Points batch resolves each point against the
// destination pixel (docs/DESIGN_GPU_RENDERER.md §2.1). It records the SOURCE
// and OP, never a destination-resolved byte, so both executors resolve the same
// op against whatever the destination holds at replay time.
type PointKind uint8

const (
	// PointPlain writes Point.Index straight into the destination — the
	// destination-independent single-pixel write [03 §5.5].
	PointPlain PointKind = iota
	// PointLit folds the destination pixel through one PALETTE.LHT row: the
	// destination byte becomes LHT[Point.Index][destination], where Point.Index
	// is the LHT row (level). This is the explosion/muzzle-flash ground halo and
	// the calculated flash disc, both brightenings of the pixel already on
	// screen [03 §4.3.1][03 R-FX-01 §4]. It reads the destination, so the record
	// carries the level, not the resolved byte.
	PointLit
)

// Points records a batch of single-pixel writes (docs/DESIGN_GPU_RENDERER.md
// §2.1): nanolathe particles, flash discs and 2x2 sprinkle fills [03 §5.5]
// [03 R-FX-01 §3]. Kind selects the per-point resolution; each point's Index is
// physical for PointPlain and an LHT row for PointLit [C-G2][03 §4.3.1].
type Points struct {
	// Kind selects how each point resolves against the destination.
	Kind PointKind
	// Points is the batch of (x, y, operand) triples.
	Points []Point
}

// FlashTransparentRow marks a generated disc texel that brightens nothing. The
// generator writes the transparent key outside the disc [06 R-WFX-01 §2], and
// the ramp inside it is exactly LHT rows 0..31 [03 §4.3.1][03 R-FX-01 §4], so
// one byte per texel carries both and no row can collide with the sentinel.
const FlashTransparentRow uint8 = 0xFF

// Flash records one calculated explosion disc as a single lit-disc command
// (docs/DESIGN_GPU_RENDERER.md §13.11). It is the same brightening the
// PointLit batch of [03 R-FX-01 §4] carries — every opaque texel folds the
// pixel under it through its own LHT row — expressed as one command instead of
// one point per covered screen pixel, so an executor that can magnify a texture
// need not be handed a hundred thousand points.
//
// It is part of the Sink contract every executor satisfies: the classic sink
// implements it by expanding the command back into the very points the classic
// recording lane emits (Expand), so a modern-lane list replayed through the
// classic executor composes the same bytes.
type Flash struct {
	// Table and Frame name the generated frame, already clamped by the recorder
	// [06 R-WFX-01 §2]. The pair is an identity, not a lookup: the frames are
	// generated once per battle and never change, so an executor may cache
	// whatever it builds from Rows under it.
	Table, Frame int32
	// Side is the generated frame's side in source pixels and Offset the
	// centring offset the blit subtracts on both axes (both are the frame's own
	// H) [06 R-WFX-01 §2].
	Side, Offset int32
	// Rows is Side*Side source texels row-major: an LHT row 0..31, or
	// FlashTransparentRow outside the disc. It is immutable after the tables are
	// generated, so it is shared rather than copied [I6].
	Rows []uint8
	// X, Y is the impact point in screen pixels, the anchor Offset centres the
	// disc on.
	X, Y int32
	// Scale is the presentation view scale the disc is magnified by
	// (DESIGN_GPU_RENDERER §14.2): source pixel c covers the screen span
	// [Scale.Project(c-Offset), Scale.Project(c-Offset+1)).
	Scale camera.ViewScale
	// Clip is the gate the recorder resolved: the recording extent intersected
	// with the terrain rectangle in screen space, because the halo must never
	// brighten unit, effect or HUD pixels [03 §4.3.1]. It is a rectangle rather
	// than a predicate so an executor can clip a quad to it.
	Clip Rect
}

// Halo records one flat LHT disc — the light an explosion or muzzle flash puts
// on the ground around it [03 §4.3.1][F-P0-036] — as a single lit-disc command
// (docs/DESIGN_GPU_RENDERER.md §13.11), the same relationship Flash has to its
// PointLit batch.
type Halo struct {
	// X, Y is the disc centre in screen pixels.
	X, Y int32
	// Radius is the disc radius in screen pixels, already taken through the view
	// scale by the recorder (DESIGN_GPU_RENDERER §14.2). A pixel at offset
	// (dx, dy) is inside when dx*dx + dy*dy <= Radius*Radius.
	Radius int32
	// Row is the LHT row every covered pixel is folded through, already clamped
	// to 0..31 as LightLookup clamps it [03 §4.3.1].
	Row uint8
	// Clip is the same terrain gate Flash carries.
	Clip Rect
}

// Contains reports whether the rectangle covers the pixel. The lit-disc
// families carry their gate as a rectangle, so both executors and the tests that
// compare them ask the same question (§13.11).
func (r Rect) Contains(x, y int32) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

// Expand walks the disc's covered screen pixels in the order the recorder's own
// point loop emits them, calling emit with each pixel's LHT row. It is the
// definition of the command: the classic sink replays a Flash by running the
// byte writer over exactly this walk, and a test locks it against the points the
// classic recording lane emits for the same disc [03 R-FX-01 §4].
func (f Flash) Expand(emit func(x, y int32, row uint8)) {
	if f.Side <= 0 || len(f.Rows) < int(f.Side*f.Side) || emit == nil {
		return
	}
	for row := int32(0); row < f.Side; row++ {
		base := row * f.Side
		py0 := f.Y + f.Scale.Project(row-f.Offset)
		py1 := f.Y + f.Scale.Project(row-f.Offset+1)
		for col := int32(0); col < f.Side; col++ {
			level := f.Rows[base+col]
			if level == FlashTransparentRow {
				continue
			}
			px0 := f.X + f.Scale.Project(col-f.Offset)
			px1 := f.X + f.Scale.Project(col-f.Offset+1)
			for py := py0; py < py1; py++ {
				for px := px0; px < px1; px++ {
					if !f.Clip.Contains(px, py) {
						continue
					}
					emit(px, py, level)
				}
			}
		}
	}
}

// Expand walks the halo's covered screen pixels in the recorder's own order,
// calling emit with the disc's single LHT row [03 §4.3.1].
func (h Halo) Expand(emit func(x, y int32, row uint8)) {
	if h.Radius <= 0 || emit == nil {
		return
	}
	r2 := h.Radius * h.Radius
	for dy := -h.Radius; dy <= h.Radius; dy++ {
		py := h.Y + dy
		for dx := -h.Radius; dx <= h.Radius; dx++ {
			if dx*dx+dy*dy > r2 {
				continue
			}
			px := h.X + dx
			if !h.Clip.Contains(px, py) {
				continue
			}
			emit(px, py, h.Row)
		}
	}
}

// Model records one model subject — a unit, feature or projectile
// (docs/DESIGN_GPU_RENDERER.md §2.1). Classic is the classic executor's owned
// replay operand, and Geometry is modern mode's only body input [I6].
type Model struct {
	// ShadowOnly preserves a carried child's earlier framebuffer shadow commit;
	// its body is composed by the later carrier command [03 R-REN-03A §4].
	ShadowOnly bool
	// Classic owns the completed classic planes and placement for this command.
	// It may be nil for geometry-only modern recording.
	Classic *ClassicModel
	// Geometry is the durable, device-neutral polygon packet. It never requires
	// a client, camera, pool, or CPU image.
	Geometry *ModelGeometry
	// ShadowOmissions is the number of requested model-shadow stages omitted by
	// modern mode. It is a count because one staged carrier group may contain
	// several shadow-casting subjects.
	ShadowOmissions int
}

// Fog records the already-clipped fog op list the client built with
// render.BuildFogOpsWindowWithArtInto (docs/DESIGN_GPU_RENDERER.md §2.1). Carrying
// render.FogOp directly introduces no import cycle: internal/render does not
// import internal/drawlist [03 §3.3].
type Fog struct {
	Ops []render.FogOp
	// Gray and Black are the resolved fog GAF variant families (anims/fog.gaf,
	// Gray1-4 and Black1-4). A GAF fog op names its family entry by op.Variant and
	// its frame by op.Frame; an executor resolves entry.Frames[op.Frame].Frame to
	// the drawn frame. These are carried on the record so a destination-reading
	// executor that cannot reach the client's fog cache (the GPU executor) resolves
	// exactly the frame the classic sink resolves from client state [03 §3.3]. They
	// are immutable-after-load GAF entries, so carrying the pointers introduces no
	// ordering and no per-frame copy [I6]. The classic sink ignores them and reads
	// its own cache, so this is purely additive.
	Gray  [4]*formats.GAFEntry
	Black [4]*formats.GAFEntry
}

// Surface records one indexed byte surface blit — the minimap or radar image —
// replayed as UIBlitIndexed (docs/DESIGN_GPU_RENDERER.md §2.1). Pixels are
// physical indices [C-G2].
type Surface struct {
	// Pixels is the source surface, SrcW*SrcH physical bytes row-major.
	Pixels     []byte
	SrcW, SrcH int32
	// Dst is the destination rectangle in screen pixels.
	Dst Rect
	// Clip confines the scaled destination after sampling coordinates have been
	// derived from Dst. HasClip is false for an unclipped surface.
	Clip    Rect
	HasClip bool
	// Identity and Revision describe immutable presentation content. Identity
	// zero preserves a dynamic upload for callers without a durable source.
	Identity uint64
	Revision uint64
}

// Terrain records one terrain blit (docs/DESIGN_GPU_RENDERER.md §2.1). Terrain
// is immutable after load. OriginX/OriginY and DstW/DstH capture the projection
// values used by the GPU executor, while the classic executor still reads Cam.
// A live record requires the source camera to remain unchanged; Clone captures it.
//
// A tile at world pixel (px, pz) lands at (px-OriginX, pz-OriginY); OriginX and
// OriginY fold the camera scroll and the orthographic beam offset [03 §2.5].
type Terrain struct {
	Water            WaterSurface
	Terrain          *world.Terrain
	OriginX, OriginY int32
	DstW, DstH       int32
	// Cam is the live camera the classic executor reads for per-tile projection.
	// Clone replaces this pointer with an owned camera value.
	Cam *camera.Camera
	// Scale is the presentation view scale this record was projected at,
	// with zero read as native (DESIGN_GPU_RENDERER §14.2).
	// A tile's screen rectangle is Scale.Px(32)
	// on a side: 32 or 64. It is additive: an executor that ignores it
	// draws the native view.
	Scale camera.ViewScale
	// Detail is the detail tile set of DESIGN_GPU_RENDERER §14.3 — one index
	// tile per Terrain.TileSet entry, in the same order, each already at the
	// screen tile size of the record's Scale and stored at 64x64, so an
	// executor copies it one-to-one. It is nil at the native scale and when
	// no detail art is installed, and then the executor resamples the 32x32
	// tile by nearest sampling through the scale's inverse. The tiles are
	// immutable after load, so Clone copies the slice header and shares the
	// tiles [I6].
	Detail [][DetailTilePixels]byte
}

// DetailTilePixels is the pixel count of one detail tile slot, 64x64, the
// largest tile any scale needs (DESIGN_GPU_RENDERER §14.3).
const DetailTilePixels = 64 * 64

// Cursor records the software cursor blit (docs/DESIGN_GPU_RENDERER.md §2.1),
// replayed as drawCursor [07 §8]. The GAF frame reference is immutable after
// load.
type Cursor struct {
	Frame      *formats.GAFFrame
	HotX, HotY int32
}

// family identifies the command struct one ordering tag points at. It is
// private: order is expressed only through Replay, never by family value.
type family uint8

const (
	familyClear family = iota
	familyTerrain
	familySprite
	familyGlyphs
	familyFill
	familyLine
	familyPoints
	familyModel
	familyFog
	familySurface
	familyCursor
	familyExpand
	familyTrails
	familyWorld
	familyMarkers
	familyFlash
	familyHalo
	familySurfaceWakes
	familyScorchMarks
	familyLens
)

// tag is one ordering entry: which family, and which element of that family's
// backing slice. familyClear and familyExpand carry no element (idx is unused).
type tag struct {
	fam family
	idx int
}

// List is one frame's draw commands in record order (C-G1). It keeps a
// per-family backing slice plus an ordering index of tags, so Replay can visit
// commands across families in exact record order (C-G3) while Reset reuses
// backing arrays. Capacity grows when the recorded workload grows.
type List struct {
	order        []tag
	terrain      []Terrain
	sprite       []Sprite
	lightSources []Sprite
	glyphs       []Glyphs
	fill         []Fill
	line         []Line
	points       []Points
	model        []Model
	fog          []Fog
	trails       []Trails
	surface      []Surface
	cursor       []Cursor
	world        []WorldSpace
	markers      []Markers
	flash        []Flash
	halo         []Halo
	surfaceWakes []SurfaceWakes
	scorchMarks  []ScorchMarks
	lens         []Lens

	classicImages    []*ClassicModelImage
	classicImageNext int
}

// RecordClear appends the frame-clear marker in record order. It carries no
// data; it fixes where the indexed surface is zeroed, which is the first command
// of every committed frame (WU-1.8). Recording the clear rather than doing it
// inline is what lets the whole frame — clear included — replay once through the
// sink after a record-only pass [C-G1].
func (l *List) RecordClear() {
	l.order = append(l.order, tag{familyClear, 0})
}

// RecordTerrain appends one terrain command in record order.
func (l *List) RecordTerrain(c Terrain) {
	l.order = append(l.order, tag{familyTerrain, len(l.terrain)})
	l.terrain = append(l.terrain, c)
}

// RecordSprite appends one sprite command in record order.
func (l *List) RecordSprite(c Sprite) {
	if c.LightingKind.Emitter() {
		l.RecordLightSource(c)
	}
	l.order = append(l.order, tag{familySprite, len(l.sprite)})
	l.sprite = append(l.sprite, c)
}

// RecordGlyphs appends one glyph-run command in record order.
func (l *List) RecordGlyphs(c Glyphs) {
	l.order = append(l.order, tag{familyGlyphs, len(l.glyphs)})
	l.glyphs = append(l.glyphs, c)
}

// RecordFill appends one fill command in record order.
func (l *List) RecordFill(c Fill) {
	l.order = append(l.order, tag{familyFill, len(l.fill)})
	l.fill = append(l.fill, c)
}

// VisitNanoSources borrows the admitted particle fills before model preparation.
// Metadata travels with the fill through list cloning and reset.
func (l *List) VisitNanoSources(visit func(Fill)) {
	for _, f := range l.fill {
		if f.Nano && f.Style == FillSolid {
			visit(f)
		}
	}
}

// RecordLine appends one line command in record order.
func (l *List) RecordLine(c Line) {
	l.order = append(l.order, tag{familyLine, len(l.line)})
	l.line = append(l.line, c)
}

// RecordPoints appends one points command in record order.
func (l *List) RecordPoints(c Points) {
	l.order = append(l.order, tag{familyPoints, len(l.points)})
	l.points = append(l.points, c)
}

// RecordModel appends one model command in record order.
func (l *List) RecordModel(c Model) {
	l.order = append(l.order, tag{familyModel, len(l.model)})
	l.model = append(l.model, c)
}

// ModelCommands copies the recorded model commands in model-family order for
// diagnostic consumers such as static previews. Both payloads are cloned, so
// callers can retain and replay the result after source-list reset. Executors
// preserve global order by using Replay.
func (l *List) ModelCommands() []Model {
	out := make([]Model, len(l.model))
	for i, m := range l.model {
		out[i] = m
		out[i].Classic = m.Classic.Clone()
		out[i].Geometry = m.Geometry.Clone()
	}
	return out
}

// VisitModels borrows commands for synchronous read-only preparation. The visitor
// must not mutate geometry or retain it beyond the recorded list's lifetime.
func (l *List) VisitModels(visit func(Model)) {
	for _, m := range l.model {
		visit(m)
	}
}

// RecordLightSource retains one named-art emitter before composite leaves are
// expanded. It is metadata only, ignored by every replay sink (GPU design §23).
func (l *List) RecordLightSource(s Sprite) { l.lightSources = append(l.lightSources, s) }

// RecordedWorldExtent is the record-space extent the world region was clipped
// to, or (0, 0) when the list carries no world region. Source gathering runs
// before replay, so a preparation pass that has to cull against the recorded
// viewport reads it here rather than from the executor's framebuffer size,
// which is smaller than the record extent below a rest factor (§16.3).
func (l *List) RecordedWorldExtent() (int32, int32) {
	for _, w := range l.world {
		if w.Begin {
			return w.RecordW, w.RecordH
		}
	}
	return 0, 0
}

// VisitLightSources borrows complete emitter art, once per recorded event.
func (l *List) VisitLightSources(visit func(Sprite)) {
	for _, s := range l.lightSources {
		visit(s)
	}
}

// VisitSprites borrows commands for synchronous read-only preparation. The
// visitor must not mutate frames or retain them beyond the list's lifetime.
func (l *List) VisitSprites(visit func(Sprite)) {
	for _, s := range l.sprite {
		visit(s)
	}
}

// VisitLines borrows commands for synchronous read-only preparation.
func (l *List) VisitLines(visit func(Line)) {
	for _, line := range l.line {
		visit(line)
	}
}

// RecordFlash appends one calculated explosion disc in record order (§13.11).
func (l *List) RecordFlash(c Flash) {
	l.order = append(l.order, tag{familyFlash, len(l.flash)})
	l.flash = append(l.flash, c)
}

// RecordHalo appends one flat LHT ground disc in record order (§13.11).
func (l *List) RecordHalo(c Halo) {
	l.order = append(l.order, tag{familyHalo, len(l.halo)})
	l.halo = append(l.halo, c)
}

// RecordFog appends one fog command in record order.
func (l *List) RecordFog(c Fog) {
	l.order = append(l.order, tag{familyFog, len(l.fog)})
	l.fog = append(l.fog, c)
}

// RecordSurface appends one surface command in record order.
func (l *List) RecordSurface(c Surface) {
	l.order = append(l.order, tag{familySurface, len(l.surface)})
	l.surface = append(l.surface, c)
}

// RecordCursor appends one cursor command in record order.
func (l *List) RecordCursor(c Cursor) {
	l.order = append(l.order, tag{familyCursor, len(l.cursor)})
	l.cursor = append(l.cursor, c)
}

// PositionCursor places recorded software cursors at the latest logical host
// position while preserving their resolved art and authored hotspot [07 §8].
// The window calls this after joining its recorder and before replay; it does
// not alter picking, other commands, or the client's published pointer sample.
func (l *List) PositionCursor(x, y int) {
	for i := range l.cursor {
		cu := &l.cursor[i]
		if cu.Frame != nil {
			hx, hy := render.CursorHotspot(cu.Frame, x, y)
			cu.HotX, cu.HotY = int32(hx), int32(hy)
		}
	}
}

// RecordExpand appends the index-to-RGBA expansion marker in record order.
// It carries no data; it fixes where the expansion pass runs (C-G8).
func (l *List) RecordExpand() {
	l.order = append(l.order, tag{familyExpand, 0})
}

// Reset truncates every backing slice to zero length WITHOUT freeing capacity,
// so a re-recorded frame that fits reuses the arrays and allocates nothing.
func (l *List) Reset() {
	l.classicImageNext = 0
	l.order = l.order[:0]
	l.terrain = l.terrain[:0]
	l.sprite = l.sprite[:0]
	l.lightSources = l.lightSources[:0]
	l.glyphs = l.glyphs[:0]
	l.fill = l.fill[:0]
	l.line = l.line[:0]
	l.points = l.points[:0]
	l.model = l.model[:0]
	l.fog = l.fog[:0]
	l.trails = l.trails[:0]
	l.surface = l.surface[:0]
	l.cursor = l.cursor[:0]
	l.world = l.world[:0]
	l.markers = l.markers[:0]
	l.flash = l.flash[:0]
	l.halo = l.halo[:0]
	l.lens = l.lens[:0]
	clear(l.surfaceWakes)
	l.surfaceWakes = l.surfaceWakes[:0]
	clear(l.scorchMarks)
	l.scorchMarks = l.scorchMarks[:0]
}

// Replay visits the recorded commands in exact record order and calls the
// matching Sink method for each (C-G3, I1). It never reads the committed frame
// and mutates no simulation state [I6].
func (l *List) Replay(s Sink) {
	// The trail family is optional: an executor that cannot present it (the
	// Original executor) simply lacks the hook.
	trails, _ := s.(TrailSink)
	lenses, _ := s.(LensSink)
	wakes, _ := s.(SurfaceWakeSink)
	scorch, _ := s.(ScorchSink)
	// The world boundary and the strategic marker layer are optional in exactly
	// the same way: an executor without free zoom needs neither
	// (docs/DESIGN_GPU_RENDERER.md §16.3).
	world, _ := s.(WorldSink)
	markers, _ := s.(MarkerSink)
	for _, t := range l.order {
		switch t.fam {
		case familyClear:
			s.Clear()
		case familyTerrain:
			s.Terrain(l.terrain[t.idx])
		case familySprite:
			s.Sprite(l.sprite[t.idx])
		case familyGlyphs:
			s.Glyphs(l.glyphs[t.idx])
		case familyFill:
			s.Fill(l.fill[t.idx])
		case familyLine:
			s.Line(l.line[t.idx])
		case familyPoints:
			s.Points(l.points[t.idx])
		case familyModel:
			s.Model(l.model[t.idx])
		case familyFog:
			s.Fog(l.fog[t.idx])
		case familySurface:
			s.Surface(l.surface[t.idx])
		case familyCursor:
			s.Cursor(l.cursor[t.idx])
		case familyExpand:
			s.Expand()
		case familyTrails:
			if trails != nil {
				trails.Trails(l.trails[t.idx])
			}
		case familyScorchMarks:
			if scorch != nil {
				scorch.ScorchMarks(l.scorchMarks[t.idx])
			}
		case familySurfaceWakes:
			if wakes != nil {
				wakes.SurfaceWakes(l.surfaceWakes[t.idx])
			}
		case familyWorld:
			if world != nil {
				world.World(l.world[t.idx])
			}
		case familyMarkers:
			if markers != nil {
				markers.Markers(l.markers[t.idx])
			}
		case familyFlash:
			s.Flash(l.flash[t.idx])
		case familyHalo:
			s.Halo(l.halo[t.idx])
		case familyLens:
			if lenses != nil {
				lenses.Lens(l.lens[t.idx])
			}
		}
	}
}

// Clone copies the list's command slices, geometry packets and mutable pixel
// buffers so resetting or recording into the source list cannot overwrite them.
// Immutable loaded resources (GAF/PCX frames, palettes and terrain) are shared.
// Terrain cameras are copied. Model classic packets own their planes, so Clone
// is a self-contained later-frame replay artifact [docs/DESIGN_GPU_RENDERER.md
// C-G5].
func (l *List) Clone() List {
	var c List
	c.order = append([]tag(nil), l.order...)
	// The Terrain records copy by value, which carries the Detail slice HEADER:
	// the detail tiles are immutable after load, so the clone shares them the
	// way it shares the world terrain itself (DESIGN_GPU_RENDERER §14.3) [I6].
	c.terrain = append([]Terrain(nil), l.terrain...)
	for i, terrain := range l.terrain {
		if terrain.Cam != nil {
			captured := *terrain.Cam
			c.terrain[i].Cam = &captured
		}
	}
	c.sprite = append([]Sprite(nil), l.sprite...)
	c.lightSources = append([]Sprite(nil), l.lightSources...)
	c.glyphs = append([]Glyphs(nil), l.glyphs...)
	c.fill = append([]Fill(nil), l.fill...)
	c.line = append([]Line(nil), l.line...)
	c.model = make([]Model, len(l.model))
	for i, m := range l.model {
		c.model[i] = m
		c.model[i].Classic = m.Classic.Clone()
		c.model[i].Geometry = m.Geometry.Clone()
	}
	c.cursor = append([]Cursor(nil), l.cursor...)
	// Points records sub-slice the client's reusable point arena; give each its
	// own array so the copy survives the next frame's arena reuse.
	c.points = make([]Points, len(l.points))
	for i, p := range l.points {
		c.points[i] = Points{Kind: p.Kind, Points: append([]Point(nil), p.Points...)}
	}
	// Fog op lists alias the client's reused fogOps buffer; copy each. The Gray and
	// Black variant families are immutable-after-load GAF entries, so their pointer
	// arrays are carried by value (shared, like GAF/PCX frames above) [I6].
	c.fog = make([]Fog, len(l.fog))
	for i, f := range l.fog {
		c.fog[i] = Fog{Ops: append([]render.FogOp(nil), f.Ops...), Gray: f.Gray, Black: f.Black}
	}
	// Surface pixels may point at a caller buffer that is reused per frame; copy.
	c.surface = make([]Surface, len(l.surface))
	for i, sf := range l.surface {
		c.surface[i] = Surface{
			Pixels:   append([]byte(nil), sf.Pixels...),
			SrcW:     sf.SrcW,
			SrcH:     sf.SrcH,
			Dst:      sf.Dst,
			Clip:     sf.Clip,
			HasClip:  sf.HasClip,
			Identity: sf.Identity,
			Revision: sf.Revision,
		}
	}
	// The world boundary markers are plain values.
	c.world = append([]WorldSpace(nil), l.world...)
	// Marker batches borrow the client's reusable marker arena; copy each.
	// Their immutable icon atlases remain shared across frames and retained lists
	// so replay preserves resource identity without copying art (GPU design §18.5).
	c.markers = make([]Markers, len(l.markers))
	for i, mk := range l.markers {
		c.markers[i] = Markers{Marks: append([]Marker(nil), mk.Marks...)}
	}
	// The lit discs are plain values. Their Rows slice is the generated frame's
	// own texels, immutable after the tables are built, so the clone shares it the
	// way it shares GAF frames [I6].
	c.flash = append([]Flash(nil), l.flash...)
	c.halo = append([]Halo(nil), l.halo...)
	c.lens = append([]Lens(nil), l.lens...)
	c.surfaceWakes = make([]SurfaceWakes, len(l.surfaceWakes))
	for i, wakes := range l.surfaceWakes {
		c.surfaceWakes[i] = SurfaceWakes{Marks: append([]SurfaceWake(nil), wakes.Marks...)}
	}
	c.scorchMarks = make([]ScorchMarks, len(l.scorchMarks))
	for i, batch := range l.scorchMarks {
		c.scorchMarks[i] = ScorchMarks{Marks: append([]ScorchMark(nil), batch.Marks...)}
	}
	// Trail batches borrow the client's reusable mark arena; copy each.
	c.trails = make([]Trails, len(l.trails))
	for i, tr := range l.trails {
		c.trails[i] = Trails{Marks: append([]Trail(nil), tr.Marks...)}
	}
	return c
}
