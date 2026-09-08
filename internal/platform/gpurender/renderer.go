package gpurender

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Renderer is the modern (GPU) executor (docs/DESIGN_GPU_RENDERER.md §2.3). It
// owns the palette-table textures, the indexed offscreen the frame composes into
// (index in the red channel), the expanded RGBA surface presented at the end, and
// the compiled expansion shader. It implements drawlist.Sink, so Execute replays
// a recorded List straight through it. Every device resource lives here, never on
// the client [I6].
type Renderer struct {
	modelPrep modelPrepScratch
	tables    tables
	expand    *ebiten.Shader
	// solid writes one constant palette index per fragment (the fills, the line
	// and the plain point batch); atlas copies an index out of a source atlas's
	// red channel (the terrain tile pass). Both draw into the indexed offscreen
	// with BlendCopy (C-G4).
	solid *ebiten.Shader
	atlas *ebiten.Shader
	// gafKeyed copies a frame's opaque index texels into the offscreen, skipping
	// its transparent ones under the source-over blend (the keyed sprite, feature
	// GAF and cursor blits). indexScaled reproduces the byte writers' integer
	// source mapping for the scaled GAF blit and the indexed surface blit (C-G4).
	gafKeyed    *ebiten.Shader
	indexScaled *ebiten.Shader
	// The fog composite's destination-reading passes (C-G7): fogGray builds the
	// gray-remapped destination layer, fogCheck writes the dithered checker,
	// fogGrayMask is the plain gray fog GAF (masked Gray[dst]) and fogPatMask the
	// dithered gray fog GAF. Solid fog fills reuse solid, gray fills copy the gray
	// layer through atlas, and black fog GAF reuses gafKeyed.
	fogGray     *ebiten.Shader
	fogCheck    *ebiten.Shader
	fogGrayMask *ebiten.Shader
	fogPatMask  *ebiten.Shader
	// The remaining dest-reading and text families (WU-2.6). litBlit folds a
	// keyed source through one LHT row (BlitLit, source-through, no snapshot).
	// tint and destTable read the destination through ALP / SHD / LHT and so run
	// over a pre-command snapshot of the offscreen (destScratch): tint is the
	// translucent strip and static-feature-shadow blit, and destTable is the
	// shared light/shade rect and lit point pass. glyph is the keyed FNT text
	// blit (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4).
	litBlit               *ebiten.Shader
	tint                  *ebiten.Shader
	destTable             *ebiten.Shader
	glyph                 *ebiten.Shader
	modelKey              *ebiten.Shader
	modelBody             *ebiten.Shader
	modelCommit           *ebiten.Shader
	modelShadowCommit     *ebiten.Shader
	modelClip             *ebiten.Shader
	modelPack, modelChild *ebiten.Shader
	modelResolve          *ebiten.Shader

	// offscreen is the indexed frame surface, RGBA8 with the palette index in the
	// red channel (C-G4). output is the expanded RGBA surface Execute returns.
	// grayScratch is the fog composite's per-frame gray-remapped snapshot of the
	// offscreen: the fog pass reads it while writing the offscreen, so a gray fill
	// or gray fog GAF never samples a pixel a fog write already changed (C-G7).
	// All three are recreated when the frame size changes.
	offscreen   *ebiten.Image
	output      *ebiten.Image
	grayScratch *ebiten.Image
	// destScratch is the dest-reading families' per-command snapshot of the
	// offscreen: before a tinted/shadow/lit-rect/shade-rect/lit-point command (or
	// a point batch) writes the offscreen, the command's covered rect is copied
	// from the offscreen into destScratch, and the shading pass reads destScratch
	// while writing the offscreen — the fog snapshot pattern (C-G7), so a
	// dest-reading write never samples a pixel it just changed. It is full-surface
	// and aligned 1:1 with the offscreen, recreated when the frame size changes.
	destScratch *ebiten.Image
	// modelKey is the subject-local maximum byte-key plane. modelCoord is a
	// same-sized coordinate source used to address the key/table/texture inputs
	// from one Kage source space while a face carries UVs separately.
	modelKeyImage                 *ebiten.Image
	modelColor                    *ebiten.Image
	modelProcessed                *ebiten.Image
	modelStage, modelStageScratch *ebiten.Image
	modelCoord                    *ebiten.Image
	modelRasterOrigin             image.Point
	modelScratch                  [4]*ebiten.Image
	modelCache                    modelImageCache
	textureAtlas                  modelTextureAtlas
	stageAtlas                    [3]*ebiten.Image
	w, h                          int

	modelSuperColor, modelSuperKey, modelSuperCoord *ebiten.Image
	modelSuperW, modelSuperH                        int

	// tileAtlases caches one tile-index atlas per *world.Terrain identity, built
	// on first Terrain draw and reused for the map's lifetime (C-G4,
	// docs/DESIGN_GPU_RENDERER.md §2.3). Keyed by pointer, never ranged in a way
	// that reaches output, so it introduces no ordering [I1].
	tileAtlases map[*world.Terrain]*tileAtlas

	// gafImages caches one index texture per *formats.GAFFrame identity, built on
	// first use and reused for the frame's lifetime: index in red, opacity flag in
	// green, alpha opaque (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). pcxImages does
	// the same for each *formats.PCX frontend background. Both are keyed by pointer
	// and never ranged in a way that reaches output, so they introduce no ordering
	// [I1].
	gafImages map[*formats.GAFFrame]*ebiten.Image
	pcxImages map[*formats.PCX]*ebiten.Image

	// fntAtlases caches one glyph atlas per *formats.FNT identity, built on first
	// Glyphs draw and reused for the font's lifetime (docs/DESIGN_GPU_RENDERER.md
	// §2.3). Each atlas packs every present glyph in a horizontal strip with the
	// set-bit flag in green; keyed by pointer and never ranged in a way that
	// reaches output, so it introduces no ordering [I1].
	fntAtlases map[*formats.FNT]*fntAtlas

	// surfaceDynamic serves zero-identity commands. surfaceCache is a bounded
	// presentation cache for durable Surface identities; replay order never
	// depends on its lookup or eviction order [I1].
	surfaceDynamic surfaceUpload
	surfaceCache   [4]surfaceUpload
	surfaceClock   uint64
	surfaceWrites  uint64 // focused device-fixture diagnostic; never output state

	// verts and idx are reusable geometry scratch so a steady-state frame's draws
	// allocate nothing after warm-up.
	verts []ebiten.Vertex
	idx   []uint16

	modelStats ModelStats
}

type surfaceUpload struct {
	identity uint64
	revision uint64
	used     uint64
	img      *ebiten.Image
	w, h     int
	pixels   []byte
}

// New builds a renderer from the installed palette tables, uploading every table
// texture once (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4, C-G8). w and h are the
// initial frame size; pass 0 for either to defer surface allocation until the
// first Execute (the size is re-checked every Execute regardless).
//
// The expansion shader is compiled here. If it fails to compile the renderer is
// still returned with a nil shader; Expand then no-ops rather than panicking, and
// the compile error is reported by NewChecked for callers that want it.
func New(pal *palette.Tables, w, h int) *Renderer {
	r, _ := NewChecked(pal, w, h)
	return r
}

// NewChecked is New but also returns the first shader compilation error. Each
// drawing family guards its own resources; callers report the initialization
// error before attempting a frame.
func NewChecked(pal *palette.Tables, w, h int) (*Renderer, error) {
	shader, err := newExpandShader()
	solid, solidErr := newSolidShader()
	atlas, atlasErr := newAtlasShader()
	gafKeyed, gafErr := newGAFKeyedShader()
	indexScaled, scaledErr := newIndexScaledShader()
	fogGray, fogGrayErr := newFogGrayShader()
	fogCheck, fogCheckErr := newFogCheckerShader()
	fogGrayMask, fogGrayMaskErr := newFogGrayMaskShader()
	fogPatMask, fogPatMaskErr := newFogPatternMaskShader()
	litBlit, litBlitErr := newLitBlitShader()
	tint, tintErr := newTintShader()
	destTable, destTableErr := newDestTableShader()
	glyph, glyphErr := newGlyphShader()
	modelKey, modelKeyErr := newModelKeyShader()
	modelBody, modelBodyErr := newModelBodyShader()
	modelCommit, modelCommitErr := newModelCommitShader()
	modelShadowCommit, modelShadowCommitErr := newModelShadowCommitShader()
	modelClip, modelClipErr := newModelClipShader()
	modelPack, modelPackErr := newModelPackShader()
	modelChild, modelChildErr := newModelChildShader()
	modelResolve, modelResolveErr := newModelResolveShader()
	r := &Renderer{
		tables:            uploadTables(pal),
		expand:            shader,
		solid:             solid,
		atlas:             atlas,
		gafKeyed:          gafKeyed,
		indexScaled:       indexScaled,
		fogGray:           fogGray,
		fogCheck:          fogCheck,
		fogGrayMask:       fogGrayMask,
		fogPatMask:        fogPatMask,
		litBlit:           litBlit,
		tint:              tint,
		destTable:         destTable,
		glyph:             glyph,
		modelKey:          modelKey,
		modelBody:         modelBody,
		modelCommit:       modelCommit,
		modelShadowCommit: modelShadowCommit,
		modelClip:         modelClip,
		modelPack:         modelPack,
		modelChild:        modelChild,
		modelResolve:      modelResolve,
		tileAtlases:       make(map[*world.Terrain]*tileAtlas),
		gafImages:         make(map[*formats.GAFFrame]*ebiten.Image),
		pcxImages:         make(map[*formats.PCX]*ebiten.Image),
		fntAtlases:        make(map[*formats.FNT]*fntAtlas),
	}
	if w > 0 && h > 0 {
		r.ensureSize(w, h)
	}
	// Report the first compile error so a caller that wants it (NewChecked) can
	// surface it; each drawing family guards on its own nil shader and no-ops
	// rather than panicking, exactly as Expand does.
	if err == nil {
		err = solidErr
	}
	if err == nil {
		err = atlasErr
	}
	if err == nil {
		err = gafErr
	}
	if err == nil {
		err = scaledErr
	}
	if err == nil {
		err = fogGrayErr
	}
	if err == nil {
		err = fogCheckErr
	}
	if err == nil {
		err = fogGrayMaskErr
	}
	if err == nil {
		err = fogPatMaskErr
	}
	if err == nil {
		err = litBlitErr
	}
	if err == nil {
		err = tintErr
	}
	if err == nil {
		err = destTableErr
	}
	if err == nil {
		err = glyphErr
	}
	if err == nil {
		err = modelKeyErr
	}
	if err == nil {
		err = modelBodyErr
	}
	if err == nil {
		err = modelCommitErr
	}
	if err == nil {
		err = modelShadowCommitErr
	}
	if err == nil {
		err = modelClipErr
	}
	if err == nil {
		err = modelPackErr
	}
	if err == nil {
		err = modelChildErr
	}
	if err == nil {
		err = modelResolveErr
	}
	return r, err
}

// ensureSize allocates or reallocates the indexed offscreen and the expanded
// output when the frame size changes.
func (r *Renderer) ensureSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	if r.offscreen != nil && r.w == w && r.h == h {
		return
	}
	r.offscreen = ebiten.NewImage(w, h)
	r.output = ebiten.NewImage(w, h)
	r.grayScratch = ebiten.NewImage(w, h)
	r.destScratch = ebiten.NewImage(w, h)
	r.w, r.h = w, h
}

// Execute replays the recorded frame list through this renderer and returns the
// expanded RGBA surface for the adapter to present (docs/DESIGN_GPU_RENDERER.md
// §2.3). The list is visited in exact record order (C-G3), ending with the
// palette expansion after every indexed composite.
//
// It returns nil when the surface cannot be sized (a degenerate w/h) or when the
// list is nil, so the adapter can fall back to leaving the screen untouched.
func (r *Renderer) Execute(list *drawlist.List, w, h int) *ebiten.Image {
	if r == nil || list == nil {
		return nil
	}
	r.ensureSize(w, h)
	if r.offscreen == nil {
		return nil
	}
	r.modelStats = ModelStats{UnsupportedFace: -1, CacheBytes: r.modelCache.bytes}
	r.modelPrep.reset()
	r.modelPrep.active = true
	defer func() { r.modelPrep.reset(); r.modelPrep.active = false }()
	pins := r.prepareModelPages(list)
	list.Replay(r)
	for _, e := range pins {
		e.pins--
	}
	r.trimModelCache()
	return r.output
}

// index0Color is the offscreen's cleared value: index 0 in the red channel with
// opaque alpha, so premultiplied sampling recovers red exactly (C-G4).
var index0Color = color.RGBA{R: 0, G: 0, B: 0, A: 255}

// Clear fills the indexed offscreen with palette index 0 — the first command of
// every committed frame (C-G1). Index 0 rides the red channel; alpha is opaque so
// the stored red survives premultiplication and decodes back to 0 (C-G4).
func (r *Renderer) Clear() {
	if r.offscreen == nil {
		return
	}
	r.offscreen.Fill(index0Color)
}

// Expand runs the index→RGBA expansion pass: the indexed offscreen through
// PALETTE.PAL into the output surface, the single colour pass of the composite
// (docs/DESIGN_GPU_RENDERER.md C-G8). It is a triangle draw rather than a rect
// draw because the two source images differ in size (the offscreen is the screen
// size, PAL is 256×1) and only DrawTrianglesShader permits that in pixel mode.
//
// The quad maps the output 1:1 to the offscreen, so each output pixel samples its
// own offscreen texel with nearest filtering (C-G4). BlendCopy overwrites the
// output outright — no blend arithmetic on the result (C-G4).
func (r *Renderer) Expand() {
	if r.expand == nil || r.offscreen == nil || r.output == nil || r.tables.pal == nil {
		// Without a compiled shader or an installed palette there is nothing to
		// expand; leave the output as-is rather than guessing a colour (I9).
		return
	}
	fw, fh := float32(r.w), float32(r.h)
	vertices := []ebiten.Vertex{
		{DstX: 0, DstY: 0, SrcX: 0, SrcY: 0, ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1},
		{DstX: fw, DstY: 0, SrcX: fw, SrcY: 0, ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1},
		{DstX: 0, DstY: fh, SrcX: 0, SrcY: fh, ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1},
		{DstX: fw, DstY: fh, SrcX: fw, SrcY: fh, ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1},
	}
	indices := []uint16{0, 1, 2, 1, 2, 3}
	opts := &ebiten.DrawTrianglesShaderOptions{
		Blend: ebiten.BlendCopy,
		Images: [4]*ebiten.Image{
			r.offscreen,  // source 0: the indexed frame, index in red
			r.tables.pal, // source 1: PALETTE.PAL colours
			nil,
			nil,
		},
	}
	r.output.DrawTrianglesShader(vertices, indices, r.expand, opts)
}

// Every drawing family is now implemented — Terrain, Fill, Line and Points in
// terrain.go and draw.go (WU-2.3); the keyed Sprite families, Surface and Cursor
// in sprites.go (WU-2.4); Fog in fog.go (WU-2.5); Glyphs, the BlitLit/BlitTinted/
// BlitFeatureShadow Sprite kinds, the FillLitRect/FillShadeRect Fill styles and
// the PointLit Points kind in text.go and deststage.go (WU-2.6); and the composed
// Model in models.go (WU-2.7).

// Glyphs is implemented in text.go: the keyed FNT text blit over the indexed
// offscreen (C-G4).

// Model is implemented in models.go: the shadow (ALP) and body (keyed) commit of
// GPU-rasterized, cached local composition images (DESIGN_GPU_RENDERER §11).

// Fog is implemented in fog.go: the fog composite over the indexed offscreen
// (C-G7).

// staticSinkCheck fails to compile if *Renderer stops satisfying drawlist.Sink.
var _ drawlist.Sink = (*Renderer)(nil)
