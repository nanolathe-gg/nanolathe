package drawlist

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Rect is an inclusive-origin, exclusive-extent screen rectangle in whole
// pixels: the pixels covered are [X, X+W) by [Y, Y+H). It is the recorded
// form of the clip and blit rectangles the byte writers compute.
type Rect struct {
	X, Y, W, H int32
}

// BlitKind selects which GAF blitter family a Sprite replays as
// (docs/DESIGN_GPU_RENDERER.md §2.1). The four kinds are the keyed, the
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
)

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
	// Clip is the inclusive clip rectangle used only when Style is
	// FillFrameInclusive: drawIndexedFrameInclusive clips each edge against it
	// independently [R-SEL-02A]. Like Rect it is carried in extent form, so the
	// inclusive clip bounds are recovered as [X, X+W-1] x [Y, Y+H-1]. Other
	// styles ignore it (its zero value).
	Clip Rect
}

// Line records one indexed line (docs/DESIGN_GPU_RENDERER.md §2.1), replayed as
// drawIndexedLine. Index is a physical palette byte [C-G2].
type Line struct {
	X0, Y0, X1, Y1 int32
	Index          uint8
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

// Model records one composed model subject — a unit, feature or projectile
// (docs/DESIGN_GPU_RENDERER.md §2.1). internal/drawlist cannot import
// internal/client (cycle), so the finished composition image cannot ride the
// record directly; instead Ref indexes a per-frame, client-side table of
// finished composed subjects that the classic executor resolves. Like
// Terrain.Cam it is same-frame replay only — the WU-1.3..WU-1.6 transition
// executes each record inline, so the index is never held across a frame
// boundary [I6]. C-G5 states modern mode obtains a subject's image from the
// classic rasterizer until Phase 3 anyway; a later unit revisits how the GPU
// executor obtains the image [03 R-REN-03A].
type Model struct {
	// Ref indexes the client-side table of finished composed subjects for this
	// frame. Replaying this command runs that subject's shadow, its one body
	// blit and its trace, in that order.
	Ref int
}

// Fog records the already-clipped fog op list the client built with
// render.BuildFogOpsWindowInto (docs/DESIGN_GPU_RENDERER.md §2.1). Carrying
// render.FogOp directly introduces no import cycle: internal/render does not
// import internal/drawlist [03 §3.3].
type Fog struct {
	Ops []render.FogOp
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
}

// Terrain records one terrain blit (docs/DESIGN_GPU_RENDERER.md §2.1). It
// carries the immutable-after-load *world.Terrain plus the camera-derived
// screen origin and destination window as values, never a live camera pointer,
// so the list stays durable across the frame boundary [I6].
//
// A tile at world pixel (px, pz) lands at (px-OriginX, pz-OriginY); OriginX and
// OriginY fold the camera scroll and the orthographic beam offset the classic
// blitter applies [03 §2.5].
//
// The modern (GPU) executor derives per-tile projection from OriginX/OriginY and
// the DstW/DstH window, not from Cam; whether the half-height shear and any
// per-tile parameters must also ride the record for that executor is not settled
// until WU-1.x builds it. The classic blitter reads them from *world.Terrain and
// the camera, both reproducible from these fields.
type Terrain struct {
	Terrain          *world.Terrain
	OriginX, OriginY int32
	DstW, DstH       int32
	// Cam is the live camera the classic executor reads for per-tile projection.
	// It is same-frame replay only: the WU-1.3..WU-1.6 transition executes each
	// record inline, so the pointer is never held across a frame boundary [I6].
	// Carrying it resolves WU-1.1's TODO(question) for the classic executor; a
	// later unit revisits the record's completeness for the GPU executor, which
	// uses OriginX/OriginY and the window instead of this pointer.
	Cam *camera.Camera
}

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
	familyTerrain family = iota
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
)

// tag is one ordering entry: which family, and which element of that family's
// backing slice. familyExpand carries no element (idx is unused).
type tag struct {
	fam family
	idx int
}

// List is one frame's draw commands in record order (C-G1). It keeps a
// per-family backing slice plus an ordering index of tags, so Replay can visit
// commands across families in exact record order (C-G3) while Reset reuses
// every array. A steady-state frame allocates nothing after warm-up.
type List struct {
	order   []tag
	terrain []Terrain
	sprite  []Sprite
	glyphs  []Glyphs
	fill    []Fill
	line    []Line
	points  []Points
	model   []Model
	fog     []Fog
	surface []Surface
	cursor  []Cursor
}

// RecordTerrain appends one terrain command in record order.
func (l *List) RecordTerrain(c Terrain) {
	l.order = append(l.order, tag{familyTerrain, len(l.terrain)})
	l.terrain = append(l.terrain, c)
}

// RecordSprite appends one sprite command in record order.
func (l *List) RecordSprite(c Sprite) {
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

// RecordExpand appends the index-to-RGBA expansion marker in record order.
// It carries no data; it fixes where the expansion pass runs (C-G8).
func (l *List) RecordExpand() {
	l.order = append(l.order, tag{familyExpand, 0})
}

// Reset truncates every backing slice to zero length WITHOUT freeing capacity,
// so a re-recorded frame that fits reuses the arrays and allocates nothing.
func (l *List) Reset() {
	l.order = l.order[:0]
	l.terrain = l.terrain[:0]
	l.sprite = l.sprite[:0]
	l.glyphs = l.glyphs[:0]
	l.fill = l.fill[:0]
	l.line = l.line[:0]
	l.points = l.points[:0]
	l.model = l.model[:0]
	l.fog = l.fog[:0]
	l.surface = l.surface[:0]
	l.cursor = l.cursor[:0]
}

// Replay visits the recorded commands in exact record order and calls the
// matching Sink method for each (C-G3, I1). It never reads the committed frame
// and mutates no simulation state [I6].
func (l *List) Replay(s Sink) {
	for _, t := range l.order {
		switch t.fam {
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
		}
	}
}
