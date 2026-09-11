package gpurender

import (
	"bytes"
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// The fog composite for the modern executor (docs/DESIGN_GPU_RENDERER.md C-G7,
// §11.2 "Fog as one pass").
//
// The recorded op list becomes one device draw instead of one per fog cell: one
// draw over the fog region through the fog pass shader, which reads the phase's
// read surface, a per-cell grid texture rebuilt this frame from the ops, the fog
// GAF atlas built once per family identity, and the GRAY TABLE.
//
// Fog is a destination-reading family — the gray fills and the plain gray fog
// GAF remap the pixels already on the surface through the GRAY TABLE
// [03 §3.3][R-RR16-A §1] — so it needs the pre-fog destination. It is therefore
// an ordinary destination-reading command of the scheduler, with its own shader
// and the fog region as its rectangle: the phase rules place it after everything
// already drawn under that region and before everything later that overwrites it,
// and the phase's read surface is the pre-fog destination, free of any
// read-after-write hazard (C-G4). It is no longer a barrier
// (docs/DESIGN_GPU_RENDERER.md §11.5). Everything else the classic byte writers do
// sequentially — the later cell that reads an earlier cell's fog write — the
// shader reproduces by carrying a running index through the 2×2 block of cells
// that can reach a pixel, in the op list's own row-major order.
//
// Every operation reproduces its classic byte writer's rebase, clip and
// per-pixel value from internal/client (classicSink.Fog, fogFillSolid,
// fogFillGray, fogFillChecker, blitFogGAF) exactly
// [03 §3.3][R-RR16-A §1][R-RR16-A §2][R-RR16-A §8].

// fogPass holds the compiled fog pass state (docs/DESIGN_GPU_RENDERER.md
// §11.2): the per-frame cell grid texture, the fog GAF atlas and the one-pass
// shader. It is lazily initialised by Fog so renderer.go need not change with
// it, and every buffer it owns is reused between frames (§11.2 "Allocation
// policy").
type fogPass struct {
	shader    *ebiten.Shader
	shaderErr error
	compiled  bool

	// atlas packs every frame of all four variants of both families, each frame
	// placed inside a fogAtlasTile(scale) square at its authored offset.
	// atlasGray, atlasBlack and atlasScale are the identity it was built for;
	// slotPresent records which slots hold a drawable frame, which is the same
	// admission classicSink.Fog makes per op (entry present, frame index in
	// range, frame non-nil) [03 §3.3].
	//
	// The scale is part of that identity because the detail view draws each cell
	// from the frame's 2× variant, one blit into the scaled cell (§14.2): the
	// variant's pixels, its size and its authored offsets are all doubled, so it
	// needs its own atlas geometry.
	atlas       *ebiten.Image
	atlasGray   [4]*formats.GAFEntry
	atlasBlack  [4]*formats.GAFEntry
	atlasScale  camera.ViewScale
	atlasReady  bool
	slotPresent [fogAtlasRows * fogAtlasCols]bool
	// oversized counts frames that do not fit a fogAtlasTile square measured
	// from the cell origin, and oversizedNote names the first of them. Such a
	// frame reaches further than the 2×2 cell neighbourhood the pass visits, so
	// it is left out of the atlas and its cells keep the underlying tile. This
	// is a reported skip, never a silent clip.
	oversized     int
	oversizedNote string

	// grid holds one texel per fog cell of the visible cell range: red is the
	// channel-one operation, green the channel-zero operation. gridBuf is the
	// reused upload buffer; the image grows but never shrinks.
	grid    *ebiten.Image
	gridW   int
	gridH   int
	gridBuf []byte
	// gridSent is the last bytes uploaded into the grid texture. A frame whose
	// fog did not change re-encodes the same bytes, and Ebitengine's Metal
	// driver builds a staging texture per WritePixels, so the upload is skipped
	// when they compare equal [DESIGN_GPU_RENDERER.md §11.2].
	gridSent []byte

	// draws counts the device draws the most recent Fog command compiled, so the
	// per-frame device-call budget of §11.4 can be asserted. Since §11.5 the fog
	// composite is one scheduled draw with no snapshot copy of its own.
	draws int
}

// fogRegion is the device-free result of walking one frame's op list: the
// screen lattice the cell grid is indexed against, the grid's used extent, and
// the region of the framebuffer the pass has to cover.
type fogRegion struct {
	// originX and originY are the UNCLAMPED rebased screen position of grid
	// cell (0,0); every op's rebased origin is this plus a multiple of the cell
	// edge 32·s, so the shader recovers a cell index from a pixel by one floor
	// division.
	originX, originY int32
	cols, rows       int
	// The framebuffer region the pass draws, a superset of every pixel the
	// classic writers touch for these ops.
	x0, y0, x1, y1 int32
	ok             bool
}

// Fog replays one clipped fog op list into the indexed offscreen (C-G7). It runs
// the same per-op rebase and clip classicSink.Fog runs, encodes each op into the
// cell grid, and then computes every fog pixel in one pass whose per-pixel value
// is the byte writer's [03 §3.3].
func (r *Renderer) Fog(fg drawlist.Fog) {
	if r == nil {
		return
	}
	// The glow layer resolves under the fog, so the grey composite dims it and
	// the black one hides it (§19). It is a barrier of its own; a frame with no
	// fog command resolves when the world region closes instead.
	r.resolveGlow()
	r.fog.draws = 0
	if r.surfaces[0] == nil {
		return
	}
	if len(fg.Ops) == 0 {
		return
	}
	if !r.fog.compiled {
		r.fog.compiled = true
		r.fog.shader, r.fog.shaderErr = newFogPassShader()
	}
	if r.fog.shader == nil {
		// Without a compiled pass there is nothing to draw; leave the composed
		// surface as it is rather than guessing a fog colour (I9).
		return
	}
	if r.tables.atlas == nil {
		// The fog dark fills and the black-family frames resolve their colour
		// through PAL; with no palette installed there is nothing to draw and
		// nothing to guess (I9).
		return
	}
	// The view scale the ops were projected at (§14.2). Every op of one frame is
	// built from one camera, so the first op names the whole list's scale.
	scale := fogOpsScale(fg.Ops)
	r.fog.ensureAtlas(fg.Gray, fg.Black, scale)

	w, h := int32(r.clipW()), int32(r.clipH())
	// The gray remap no longer reads the GRAY TABLE: in the true-colour composite
	// it is the desaturation the table was built from, the luminance
	// floor((r+g+b)/3) of the colour already there [03 §4.3.3](§13.2 GRAY row,
	// §13.3). The classic byte writer's "no gray table, no gray fill" gate
	// therefore becomes the palette gate above.
	const grayReady = true

	region := fogRegionFor(fg.Ops, w, h, scale)
	if !region.ok {
		return
	}
	r.fog.encodeGrid(region, fg.Ops, w, h, scale, grayReady)
	if !r.fog.uploadGrid(region) {
		return
	}

	// Source slot 0 is the read copy of the region, left unbound here and filled
	// at submission (§11.5, §13.3): fog is the one family that still reads the
	// pixels it rewrites, because the gray remap is a desaturation no
	// fixed-function blend expresses. The pass writes every pixel of the region,
	// unfogged ones with the colour it read there, and always returns an opaque
	// fragment, so source-over stores the colour unchanged.
	imgs := [4]*ebiten.Image{1: r.fog.grid, 2: r.fog.atlas, 3: r.tables.atlas}
	// The fog composite is the ONE family the generic world transform cannot
	// carry (docs/DESIGN_GPU_RENDERER.md §16.3 "Fog"). Its pass reads the
	// pre-fog copy of the composite 1:1 under each fragment, so its source
	// coordinates have to equal its destination coordinates in SCREEN space,
	// and its shader recovers a fog cell from the fragment's own position, so
	// it has to be told which record pixel that position is. The region is
	// therefore transformed here, the generic transform is held off for the one
	// command, and the record-per-screen factor rides the colour lane the fog
	// quad never used.
	k := r.sched.worldScale
	if !r.sched.worldOn {
		k = 1
	}
	sx0, sy0, sx1, sy1 := r.sched.txRect(int(region.x0), int(region.y0), int(region.x1), int(region.y1))
	sx0, sy0 = maxInt(sx0, 0), maxInt(sy0, 0)
	sx1, sy1 = minInt(sx1, r.w), minInt(sy1, r.h)
	worldOn := r.sched.worldOn
	r.sched.worldOn = false
	defer func() { r.sched.worldOn = worldOn }()
	if !r.sched.beginBlended(schedDest, sx0, sy0, sx1, sy1,
		imgs, r.fog.shader, blendComposite, 0) {
		return
	}
	// The lattice origin, the checker parity and the view scale ride the vertex
	// custom attributes rather than a uniform map, so a steady-state frame builds
	// no per-draw uniform (§11.2 "Allocation policy"). All four vertices carry the
	// same values, so the interpolated attribute is constant across the region.
	// Source coordinates equal destination coordinates, so the read surface is
	// sampled 1:1 under each fragment.
	x0, y0 := float32(sx0), float32(sy0)
	x1, y1 := float32(sx1), float32(sy1)
	r.sched.quad(schedDest, x0, y0, x1, y1, x0, y0, x1, y1,
		[4]float32{k, 0, 0, 0},
		[4]float32{float32(region.originX), float32(region.originY),
			float32(fogParityOps(fg.Ops, scale)), float32(scale.Float())})
	r.fog.draws++
}

// fogOpRect returns one op's rebased screen rectangle exactly as
// classicSink.Fog computes it: rebased from the retail viewport origin (128,32)
// to the full-window shell origin (0,0), then clamped to the framebuffer
// [03 §2.5][03 §3.3]. rawX and rawY are the UNCLAMPED rebased origin the
// 32-pixel fill is measured from; x0 and y0 are the clamped origin the fog GAF
// blit is anchored at. ok is false for the degenerate rectangles classicSink.Fog
// skips.
//
// The modern executor always rebases: the op list is only ever recorded with a
// live camera (drawFog gates on c.cam != nil), which is classicSink.Fog's
// `if c.cam != nil` branch.
func fogOpRect(op *render.FogOp, w, h int32) (rawX, rawY, x0, y0, x1, y1 int32, ok bool) {
	rawX = op.ScreenX0 - camera.OriginX
	rawY = op.ScreenY0 - camera.OriginY
	x1 = op.ScreenX1 - camera.OriginX
	y1 = op.ScreenY1 - camera.OriginY
	x0, y0 = rawX, rawY
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	if x0 >= x1 || y0 >= y1 {
		return rawX, rawY, x0, y0, x1, y1, false
	}
	return rawX, rawY, x0, y0, x1, y1, true
}

// fogRegionFor walks the op list once for the cell lattice, the grid extent and
// the region the pass must cover.
//
// The lattice comes from the ops themselves, never from a camera pointer: the
// producer places a cell at rebased origin (gx*32 + 16 - camX)·s
// (render.FogScreenRect with camera.OriginX cancelling against the composer's
// rebase), so every op's rebased origin differs from every other by a multiple
// of the cell edge 32·s and the smallest of them names cell (0,0)
// [03 §3.3](§14.2).
//
// The covered region is the union of [clamped origin, clamped origin +
// fogAtlasTile(s)) over the surviving ops, clipped to the framebuffer. That is a
// superset of every pixel the byte writers touch: a fill stays inside the cell's
// own 32·s pixels, and a fog GAF frame is anchored at the clamped origin and
// fits inside a fogAtlasTile(s) square by construction of the atlas.
func fogRegionFor(ops []render.FogOp, w, h int32, scale camera.ViewScale) fogRegion {
	cell := scale.Px(fogCellPixels)
	tile := int32(fogAtlasTile(scale))
	var out fogRegion
	var minRawX, minRawY, maxRawX, maxRawY int32
	var minAX, minAY, maxAX, maxAY int32
	for i := range ops {
		rawX, rawY, x0, y0, _, _, ok := fogOpRect(&ops[i], w, h)
		if !ok {
			continue
		}
		if !out.ok {
			out.ok = true
			minRawX, maxRawX, minRawY, maxRawY = rawX, rawX, rawY, rawY
			minAX, maxAX, minAY, maxAY = x0, x0, y0, y0
			continue
		}
		minRawX, maxRawX = minInt32(minRawX, rawX), maxInt32(maxRawX, rawX)
		minRawY, maxRawY = minInt32(minRawY, rawY), maxInt32(maxRawY, rawY)
		minAX, maxAX = minInt32(minAX, x0), maxInt32(maxAX, x0)
		minAY, maxAY = minInt32(minAY, y0), maxInt32(maxAY, y0)
	}
	if !out.ok {
		return out
	}
	out.originX, out.originY = minRawX, minRawY
	out.cols = int((maxRawX-minRawX)/cell) + 1
	out.rows = int((maxRawY-minRawY)/cell) + 1
	out.x0 = maxInt32(minAX, 0)
	out.y0 = maxInt32(minAY, 0)
	out.x1 = minInt32(maxAX+tile, w)
	out.y1 = minInt32(maxAY+tile, h)
	if out.x0 >= out.x1 || out.y0 >= out.y1 {
		out.ok = false
	}
	return out
}

// fogOpsScale is the view scale one frame's fog ops were projected at. Every op
// of a frame comes from one camera, so the first op names the list's scale; an
// empty list is the native scale (render.FogOp.ViewScale)(§14.2).
func fogOpsScale(ops []render.FogOp) camera.ViewScale {
	if len(ops) == 0 {
		return camera.ViewScaleNative
	}
	return ops[0].ViewScale()
}

// fogParityOps derives the fog checker parity (camX + camZ) & 1 from an op's
// grid cell and screen rectangle, so the pass needs no camera pointer. The
// producer places a cell at ScreenX0 = (gx*32 + 16 - camX)·s + OriginX and
// ScreenY0 = (gy*32 + 16 - camZ)·s + OriginY (render.FogScreenRect), and the
// scaled term is exactly divisible, so
//
//	camX = gx*32 + 16 - (ScreenX0 - OriginX)/s
//
// recovers the camera the byte writers read, and likewise camZ. That is the
// value fogFillChecker and blitFogGAF compute from the camera [03 §3.3].
//
// The scale term matters: at s = 2 every rebased origin is even, so the parity
// cannot be read off the screen rectangle alone the way it can at s = 1, where
// this expression reduces to (ScreenX0 + ScreenY0) & 1 because 32, 16 and the
// beam offsets are all even.
func fogParityOps(ops []render.FogOp, scale camera.ViewScale) int32 {
	if len(ops) == 0 {
		return 0
	}
	op := &ops[0]
	camX := op.GridX*render.FogTilePixels + render.FogTilePixels/2 - scale.Inverse(op.ScreenX0-camera.OriginX)
	camZ := op.GridY*render.FogTilePixels + render.FogTilePixels/2 - scale.Inverse(op.ScreenY0-camera.OriginY)
	return (camX + camZ) & 1
}

// encodeGrid fills the reused grid buffer from the op list. A cell carries at
// most one channel-one and one channel-zero operation, so one texel describes
// it; a visible cell, an operation the palette makes impossible and an
// operation whose GAF frame is missing all stay zero, which is the cell keeping
// the underlying tile exactly as classicSink.Fog's skip does [03 §3.3].
func (f *fogPass) encodeGrid(region fogRegion, ops []render.FogOp, w, h int32, scale camera.ViewScale, grayReady bool) {
	cell := scale.Px(fogCellPixels)
	imgW, imgH := f.gridImageSize(region)
	need := imgW * imgH * 4
	if cap(f.gridBuf) < need {
		f.gridBuf = make([]byte, need)
	}
	f.gridBuf = f.gridBuf[:need]
	for i := 0; i < imgW*imgH; i++ {
		f.gridBuf[i*4+0] = 0
		f.gridBuf[i*4+1] = 0
		f.gridBuf[i*4+2] = 0
		// Alpha stays opaque so the stored red and green survive premultiplied
		// sampling and decode back exactly (C-G4).
		f.gridBuf[i*4+3] = 255
	}
	for i := range ops {
		op := &ops[i]
		rawX, rawY, _, _, _, _, ok := fogOpRect(op, w, h)
		if !ok {
			continue
		}
		col := int((rawX - region.originX) / cell)
		row := int((rawY - region.originY) / cell)
		if col < 0 || row < 0 || col >= imgW || row >= imgH {
			continue
		}
		at := (row*imgW + col) * 4
		switch op.Kind {
		case render.FogKindSolidDark:
			// lo==15: fill the clipped cell with the fog dark index
			// (fogFillSolid) [03 §3.3].
			f.gridBuf[at+1] = fogCh0Solid
		case render.FogKindGrayRemap:
			// hi==15: remap the destination through the GRAY TABLE
			// (fogFillGray) [03 §3.3][R-RR16-A §1].
			if grayReady {
				f.gridBuf[at+0] = fogCh1GrayFill
			}
		case render.FogKindPatterned:
			// hi==15 dithered: the dark index at (x+y+parity)&1==1
			// (fogFillChecker) [03 §3.3][R-RR16-A §2].
			f.gridBuf[at+0] = fogCh1PatFill
		case render.FogKindGAFCh1:
			// hi 1..14: gray-family fog GAF, a masked GRAY TABLE remap of the
			// destination, or the masked checker under the dither option
			// [03 §3.3][R-RR16-A §1][R-RR16-A §8].
			slot, present := f.slotFor(0, op)
			if !present {
				continue
			}
			if op.Patterned {
				f.gridBuf[at+0] = byte(fogCh1GrayDith + slot)
			} else if grayReady {
				f.gridBuf[at+0] = byte(fogCh1GrayPlain + slot)
			}
		case render.FogKindGAFCh0:
			// lo 1..14: black-family fog GAF, always a plain keyed copy of the
			// frame (blitFogGAF fogBlitBlack) [03 §3.3][R-RR16-A §8].
			slot, present := f.slotFor(1, op)
			if !present {
				continue
			}
			f.gridBuf[at+1] = byte(fogCh0Black + slot)
		default:
			// Visible cells produce no ops; nothing to draw.
			continue
		}
	}
}

// slotFor resolves the atlas slot for one fog GAF op within a family (0 gray, 1
// black), matching the entry/frame bounds classicSink.Fog checks: a valid
// variant (0..3) and frame index, a non-nil entry, and op.Frame within
// entry.Frames. Anything else, including a frame the atlas could not hold,
// leaves the cell with the underlying tile [03 §3.3][R-RR16-A §6].
func (f *fogPass) slotFor(family int, op *render.FogOp) (int, bool) {
	if op.Variant < 0 || op.Variant >= fogVariants || op.Frame < 0 || op.Frame >= fogAtlasCols {
		return 0, false
	}
	slot := op.Variant*fogAtlasCols + op.Frame
	if !f.slotPresent[family*fogSlots+slot] {
		return 0, false
	}
	return slot, true
}

// gridImageSize returns the grid texture size for this frame: the used cell
// range, never smaller than the image already allocated, so the texture grows
// with the visible range but is not recreated when it shrinks.
func (f *fogPass) gridImageSize(region fogRegion) (int, int) {
	return maxInt(region.cols, f.gridW), maxInt(region.rows, f.gridH)
}

// uploadGrid writes the encoded grid into its texture, growing it when the
// visible cell range grows.
func (f *fogPass) uploadGrid(region fogRegion) bool {
	imgW, imgH := f.gridImageSize(region)
	if imgW <= 0 || imgH <= 0 {
		return false
	}
	if f.grid == nil || f.gridW != imgW || f.gridH != imgH {
		f.grid = ebiten.NewImage(imgW, imgH)
		f.gridW, f.gridH = imgW, imgH
		f.gridSent = f.gridSent[:0]
	}
	if bytes.Equal(f.gridSent, f.gridBuf) {
		return true
	}
	f.grid.WritePixels(f.gridBuf)
	f.gridSent = append(f.gridSent[:0], f.gridBuf...)
	return true
}

// ensureAtlas builds the fog GAF atlas for one (Gray, Black, scale) identity and
// keeps it for that identity's lifetime. The families are immutable after load,
// so identity is the eight entry pointers and the view scale [03 §3.3](§14.2).
//
// Every frame is stored in its own fogAtlasTile(scale) square at the position it
// lands at relative to the CELL ORIGIN: retail subtracts the frame's signed
// XOffset/YOffset before clipping, so the fog quadrant geometry lives entirely
// in those anchors [03 §3.3][R-RR16-A §3][fmt gaf]. Baking them here is what
// lets the pass address a frame from the cell index alone.
//
// At the detail scale the frame stored is the one the classic sink draws there:
// the client resolves a world-space sprite through viewFrame, and no remaster
// covers anims/fog.gaf, so a fog frame resolves to its nearest-doubled variant —
// doubled pixels, doubled size, doubled authored offsets (§14.3). Building the
// atlas from that variant is the same one-blit-per-cell the classic sink makes.
func (f *fogPass) ensureAtlas(gray, black [4]*formats.GAFEntry, scale camera.ViewScale) {
	scale = scale.Norm()
	if f.atlasReady && f.atlasGray == gray && f.atlasBlack == black && f.atlasScale == scale {
		return
	}
	f.atlasReady = true
	f.atlasGray, f.atlasBlack, f.atlasScale = gray, black, scale
	f.oversized, f.oversizedNote = 0, ""
	for i := range f.slotPresent {
		f.slotPresent[i] = false
	}

	tile := fogAtlasTile(scale)
	atlasW, atlasH := fogAtlasCols*tile, fogAtlasRows*tile
	buf := make([]byte, atlasW*atlasH*4)
	for i := 3; i < len(buf); i += 4 {
		buf[i] = 255
	}
	families := [2][4]*formats.GAFEntry{gray, black}
	names := [2]string{"Gray", "Black"}
	for family := 0; family < 2; family++ {
		for variant := 0; variant < fogVariants; variant++ {
			entry := families[family][variant]
			if entry == nil {
				continue
			}
			for frame := 0; frame < fogAtlasCols && frame < len(entry.Frames); frame++ {
				fr := fogViewFrame(entry.Frames[frame].Frame, scale)
				if fr == nil {
					continue
				}
				ox, oy, ok := fogFrameTilePlacement(fr, tile)
				if !ok {
					if int(fr.Width) <= 0 || int(fr.Height) <= 0 {
						// blitFogGAF returns without drawing a degenerate frame.
						continue
					}
					f.oversized++
					if f.oversizedNote == "" {
						f.oversizedNote = fmt.Sprintf(
							"nanolathe: fog GAF frame reaches past the %d-pixel cell neighbourhood: entry %s%d frame %d, %dx%d at offset (%d,%d)",
							tile, names[family], variant+1, frame, fr.Width, fr.Height, ox, oy)
					}
					continue
				}
				writeFogAtlasTile(buf, atlasW, tile, family*fogVariants+variant, frame, ox, oy, fr)
				f.slotPresent[family*fogSlots+variant*fogAtlasCols+frame] = true
			}
		}
	}
	f.atlas = ebiten.NewImage(atlasW, atlasH)
	f.atlas.WritePixels(buf)
}

// fogViewFrame is the frame the classic sink draws for one fog cell at this view
// scale: the frame itself at the native scale, and its nearest-resampled
// variant at a magnified one — doubled at 2x, 3/2 at 1.5x — which is what the
// client's viewFrame resolves for art no remaster covers (§14.3).
// anims/fog.gaf is not a feature bank, so it is never covered.
func fogViewFrame(fr *formats.GAFFrame, scale camera.ViewScale) *formats.GAFFrame {
	if fr == nil || scale.Native() {
		return fr
	}
	return fr.Resampled(int(scale.Norm()), 2)
}

// fogAtlasTile is the square the atlas reserves for one fog GAF frame at one
// view scale: twice the cell edge, the reach of the 2×2 cell neighbourhood the
// pass visits (fog_shaders.go).
func fogAtlasTile(scale camera.ViewScale) int {
	return int(scale.Px(fogAtlasNativeTile))
}

// fogFrameTilePlacement returns where one fog GAF frame lands inside its
// tile-sized square, measured from the cell origin, and whether it fits.
// Retail subtracts the frame's signed XOffset/YOffset from the destination
// before clipping, so the placement is (-XOffset, -YOffset)
// [03 §3.3][R-RR16-A §3][fmt gaf]. A frame that starts before the cell origin or
// reaches past the tile does not fit the 2×2 cell neighbourhood the pass visits.
func fogFrameTilePlacement(fr *formats.GAFFrame, tile int) (ox, oy int, ok bool) {
	ox, oy = -int(fr.XOffset), -int(fr.YOffset)
	fw, fh := int(fr.Width), int(fr.Height)
	if fw <= 0 || fh <= 0 {
		return ox, oy, false
	}
	if ox < 0 || oy < 0 || ox+fw > tile || oy+fh > tile {
		return ox, oy, false
	}
	return ox, oy, true
}

// writeFogAtlasTile copies one decoded frame into its atlas tile: index in red,
// opacity flag in green, alpha opaque, the same encoding buildGAFFrameImage uses
// so the pass reads a frame the way every other keyed blitter does (C-G4). The
// fog art is a mask — every shipped frame is built from the colour key and the
// cloud index — so what reaches the screen is decided by the family, not by
// these source indices [R-RR16-A §3][R-RR16-A §8].
func writeFogAtlasTile(buf []byte, atlasW, tile, row, col, ox, oy int, fr *formats.GAFFrame) {
	fw, fh := int(fr.Width), int(fr.Height)
	np, nt := len(fr.Pixels), len(fr.Transparent)
	baseX := col*tile + ox
	baseY := row*tile + oy
	for y := 0; y < fh; y++ {
		for x := 0; x < fw; x++ {
			src := y*fw + x
			// Key pixels leave the destination alone; the decoder records the
			// key match in Transparent [fmt gaf].
			opaque := src < np && (src >= nt || !fr.Transparent[src])
			at := ((baseY+y)*atlasW + baseX + x) * 4
			if src < np {
				buf[at+0] = fr.Pixels[src]
			}
			if opaque {
				buf[at+1] = 255
			}
		}
	}
}

func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
