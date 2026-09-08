package gpurender

import (
	"bytes"
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/render"
)

// The fog composite for the modern executor (docs/DESIGN_GPU_RENDERER.md C-G7,
// §11.2 "Fog as one pass").
//
// The recorded op list becomes two device draws instead of one per fog cell:
//
//  1. one copy of the pre-fog offscreen into destScratch over the fog region;
//  2. one draw of that region through the fog pass shader, which reads the
//     snapshot, a per-cell grid texture rebuilt this frame from the ops, the fog
//     GAF atlas built once per family identity, and the GRAY TABLE.
//
// Fog is a destination-reading family — the gray fills and the plain gray fog
// GAF remap the pixels already on the surface through the GRAY TABLE
// [03 §3.3][R-RR16-A §1] — so it needs the pre-fog destination. Reading it from
// a snapshot image while writing the offscreen keeps the pass free of any
// read-after-write hazard (C-G4). Everything else the classic byte writers do
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
	// placed inside a fogAtlasTile square at its authored offset. atlasGray and
	// atlasBlack are the family identity it was built for; slotPresent records
	// which slots hold a drawable frame, which is the same admission
	// classicSink.Fog makes per op (entry present, frame index in range, frame
	// non-nil) [03 §3.3].
	atlas       *ebiten.Image
	atlasGray   [4]*formats.GAFEntry
	atlasBlack  [4]*formats.GAFEntry
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

	// draws counts the device draws the most recent Fog command issued, so the
	// per-frame device-call budget of §11.4 can be asserted.
	draws int
}

// fogRegion is the device-free result of walking one frame's op list: the
// screen lattice the cell grid is indexed against, the grid's used extent, and
// the region of the framebuffer the pass has to cover.
type fogRegion struct {
	// originX and originY are the UNCLAMPED rebased screen position of grid
	// cell (0,0); every op's rebased origin is this plus a multiple of 32, so
	// the shader recovers a cell index from a pixel by one floor division.
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
	r.fog.draws = 0
	if r.offscreen == nil || r.destScratch == nil {
		return
	}
	// Fog reads everything drawn so far, so it is a phase boundary: the compiled
	// phases are submitted before the pass runs (docs/DESIGN_GPU_RENDERER.md
	// §11.2 "Fog as one pass").
	r.submitSchedule()
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
	r.fog.ensureAtlas(fg.Gray, fg.Black)

	w, h := int32(r.w), int32(r.h)
	// The gray-reading operations are skipped outright when the palette carries
	// no gray table, exactly as fogFillGray and blitFogGAF(fogBlitGray) return
	// early when the palette is absent [03 §3.3].
	grayReady := r.tables.gray != nil

	region := fogRegionFor(fg.Ops, w, h)
	if !region.ok {
		return
	}
	r.fog.encodeGrid(region, fg.Ops, w, h, grayReady)
	if !r.fog.uploadGrid(region) {
		return
	}

	// The pre-fog destination the gray operations read (C-G7).
	r.snapshotRect(int(region.x0), int(region.y0), int(region.x1), int(region.y1))
	r.fog.draws++

	// A nil gray table means no operation samples source 3; bind the grid there
	// so the shader's sampler still has an image behind it.
	grayTable := r.tables.gray
	if grayTable == nil {
		grayTable = r.fog.grid
	}
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendFogQuad(region, float32(fogParityOps(fg.Ops)))
	// The pass writes every pixel of the region, unfogged ones with the
	// snapshot value it read, so a copy is the correct blend (C-G4). The reused
	// options value keeps the frame free of a per-draw allocation
	// (§11.2 "Allocation policy").
	r.sceneOpts.Blend = ebiten.BlendCopy
	r.sceneOpts.Images = [4]*ebiten.Image{r.destScratch, r.fog.grid, r.fog.atlas, grayTable}
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.fog.shader, &r.sceneOpts)
	r.sceneOpts.Images = [4]*ebiten.Image{}
	r.fog.draws++
	r.frameDraws++
}

// appendFogQuad appends the fog pass quad. The lattice origin and the checker
// parity ride the vertex custom attributes rather than a uniform map, so a
// steady-state frame builds no per-draw uniform (§11.2 "Allocation policy").
// All four vertices carry the same values, so the interpolated attribute is
// constant across the region. Source coordinates equal destination coordinates,
// so the snapshot is sampled 1:1 under each fragment.
func (r *Renderer) appendFogQuad(region fogRegion, parity float32) {
	x0, y0 := float32(region.x0), float32(region.y0)
	x1, y1 := float32(region.x1), float32(region.y1)
	ox, oy := float32(region.originX), float32(region.originY)
	base := uint16(len(r.verts))
	r.verts = append(r.verts,
		ebiten.Vertex{DstX: x0, DstY: y0, SrcX: x0, SrcY: y0, Custom0: ox, Custom1: oy, Custom2: parity},
		ebiten.Vertex{DstX: x1, DstY: y0, SrcX: x1, SrcY: y0, Custom0: ox, Custom1: oy, Custom2: parity},
		ebiten.Vertex{DstX: x0, DstY: y1, SrcX: x0, SrcY: y1, Custom0: ox, Custom1: oy, Custom2: parity},
		ebiten.Vertex{DstX: x1, DstY: y1, SrcX: x1, SrcY: y1, Custom0: ox, Custom1: oy, Custom2: parity},
	)
	r.idx = append(r.idx, base, base+1, base+2, base+1, base+2, base+3)
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
// producer places a cell at rebased origin gx*32 + 16 - camX (render.FogScreenRect
// with camera.OriginX cancelling against the composer's rebase), so every op's
// rebased origin differs from every other by a multiple of 32 and the smallest
// of them names cell (0,0) [03 §3.3].
//
// The covered region is the union of [clamped origin, clamped origin +
// fogAtlasTile) over the surviving ops, clipped to the framebuffer. That is a
// superset of every pixel the byte writers touch: a fill stays inside the cell's
// own 32 pixels, and a fog GAF frame is anchored at the clamped origin and fits
// inside a fogAtlasTile square by construction of the atlas.
func fogRegionFor(ops []render.FogOp, w, h int32) fogRegion {
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
	out.cols = int((maxRawX-minRawX)/fogCellPixels) + 1
	out.rows = int((maxRawY-minRawY)/fogCellPixels) + 1
	out.x0 = maxInt32(minAX, 0)
	out.y0 = maxInt32(minAY, 0)
	out.x1 = minInt32(maxAX+fogAtlasTile, w)
	out.y1 = minInt32(maxAY+fogAtlasTile, h)
	if out.x0 >= out.x1 || out.y0 >= out.y1 {
		out.ok = false
	}
	return out
}

// fogParityOps derives the fog checker parity (camX + camZ) & 1 from an op's
// screen rectangle, so the pass needs no camera pointer. The producer places a
// cell at ScreenX0 = gx*32 + 16 - camX + OriginX and ScreenY0 = gy*32 + 16 -
// camZ + OriginY (render.FogScreenRect); every term but -camX / -camZ is even,
// so (ScreenX0 + ScreenY0) & 1 == (camX + camZ) & 1 for every op — the same
// value fogFillChecker and blitFogGAF compute from the camera [03 §3.3].
func fogParityOps(ops []render.FogOp) int32 {
	if len(ops) == 0 {
		return 0
	}
	return (ops[0].ScreenX0 + ops[0].ScreenY0) & 1
}

// encodeGrid fills the reused grid buffer from the op list. A cell carries at
// most one channel-one and one channel-zero operation, so one texel describes
// it; a visible cell, an operation the palette makes impossible and an
// operation whose GAF frame is missing all stay zero, which is the cell keeping
// the underlying tile exactly as classicSink.Fog's skip does [03 §3.3].
func (f *fogPass) encodeGrid(region fogRegion, ops []render.FogOp, w, h int32, grayReady bool) {
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
		col := int((rawX - region.originX) / fogCellPixels)
		row := int((rawY - region.originY) / fogCellPixels)
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

// ensureAtlas builds the fog GAF atlas for one (Gray, Black) family identity and
// keeps it for that identity's lifetime. The families are immutable after load,
// so identity is the eight entry pointers [03 §3.3].
//
// Every frame is stored in its own fogAtlasTile square at the position it lands
// at relative to the CELL ORIGIN: retail subtracts the frame's signed
// XOffset/YOffset before clipping, so the fog quadrant geometry lives entirely
// in those anchors [03 §3.3][R-RR16-A §3][fmt gaf]. Baking them here is what
// lets the pass address a frame from the cell index alone.
func (f *fogPass) ensureAtlas(gray, black [4]*formats.GAFEntry) {
	if f.atlasReady && f.atlasGray == gray && f.atlasBlack == black {
		return
	}
	f.atlasReady = true
	f.atlasGray, f.atlasBlack = gray, black
	f.oversized, f.oversizedNote = 0, ""
	for i := range f.slotPresent {
		f.slotPresent[i] = false
	}

	buf := make([]byte, fogAtlasW*fogAtlasH*4)
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
				fr := entry.Frames[frame].Frame
				if fr == nil {
					continue
				}
				ox, oy, ok := fogFrameTilePlacement(fr)
				if !ok {
					if int(fr.Width) <= 0 || int(fr.Height) <= 0 {
						// blitFogGAF returns without drawing a degenerate frame.
						continue
					}
					f.oversized++
					if f.oversizedNote == "" {
						f.oversizedNote = fmt.Sprintf(
							"nanolathe: fog GAF frame reaches past the %d-pixel cell neighbourhood: entry %s%d frame %d, %dx%d at offset (%d,%d)",
							fogAtlasTile, names[family], variant+1, frame, fr.Width, fr.Height, ox, oy)
					}
					continue
				}
				writeFogAtlasTile(buf, family*fogVariants+variant, frame, ox, oy, fr)
				f.slotPresent[family*fogSlots+variant*fogAtlasCols+frame] = true
			}
		}
	}
	f.atlas = ebiten.NewImage(fogAtlasW, fogAtlasH)
	f.atlas.WritePixels(buf)
}

// fogFrameTilePlacement returns where one fog GAF frame lands inside its
// fogAtlasTile square, measured from the cell origin, and whether it fits.
// Retail subtracts the frame's signed XOffset/YOffset from the destination
// before clipping, so the placement is (-XOffset, -YOffset)
// [03 §3.3][R-RR16-A §3][fmt gaf]. A frame that starts before the cell origin or
// reaches past the tile does not fit the 2×2 cell neighbourhood the pass visits.
func fogFrameTilePlacement(fr *formats.GAFFrame) (ox, oy int, ok bool) {
	ox, oy = -int(fr.XOffset), -int(fr.YOffset)
	fw, fh := int(fr.Width), int(fr.Height)
	if fw <= 0 || fh <= 0 {
		return ox, oy, false
	}
	if ox < 0 || oy < 0 || ox+fw > fogAtlasTile || oy+fh > fogAtlasTile {
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
func writeFogAtlasTile(buf []byte, row, col, ox, oy int, fr *formats.GAFFrame) {
	fw, fh := int(fr.Width), int(fr.Height)
	np, nt := len(fr.Pixels), len(fr.Transparent)
	baseX := col*fogAtlasTile + ox
	baseY := row*fogAtlasTile + oy
	for y := 0; y < fh; y++ {
		for x := 0; x < fw; x++ {
			src := y*fw + x
			// Key pixels leave the destination alone; the decoder records the
			// key match in Transparent [fmt gaf].
			opaque := src < np && (src >= nt || !fr.Transparent[src])
			at := ((baseY+y)*fogAtlasW + baseX + x) * 4
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
