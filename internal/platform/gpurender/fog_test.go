package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// The compiled fog pass is a lattice plus an atlas: the grid texture indexes
// cells against one screen origin taken from the ops, and every fog GAF frame
// lives at a fixed place in one atlas tile. These tests drive that arithmetic
// with no graphics device (C-G10); the pixel semantics themselves are locked by
// the classic/modern comparison of the M1-M8 capture matrix.

// fogOpAt builds one recorded fog op for grid cell (gx,gy) at camera (camX,camZ),
// exactly as render.BuildFogOpsWindowInto's producer places it, so the tests
// exercise the real screen arithmetic rather than a restatement of it.
func fogOpAt(gx, gy, camX, camZ int32, kind render.FogKind) render.FogOp {
	x0 := gx*render.FogTilePixels + render.FogTilePixels/2 - camX + camera.OriginX
	y0 := gy*render.FogTilePixels + render.FogTilePixels/2 - camZ + camera.OriginY
	return render.FogOp{
		GridX: gx, GridY: gy,
		ScreenX0: x0, ScreenY0: y0,
		ScreenX1: x0 + render.FogTilePixels, ScreenY1: y0 + render.FogTilePixels,
		Kind: kind, Variant: -1, Frame: -1,
	}
}

func TestFogPassShaderCompiles(t *testing.T) {
	if _, err := newFogPassShader(); err != nil {
		t.Fatalf("fog pass shader: %v", err)
	}
}

// The grid texel packs both channel operations into two bytes, so every encoded
// value must stay inside one. Channel one carries its atlas slot in the code, so
// its largest value is the dithered gray family's last slot.
func TestFogGridCodesFitOneByte(t *testing.T) {
	if got := fogCh1GrayDith + fogSlots - 1; got > 255 {
		t.Fatalf("largest channel-one code %d does not fit a byte", got)
	}
	if got := fogCh0Black + fogSlots - 1; got > 255 {
		t.Fatalf("largest channel-zero code %d does not fit a byte", got)
	}
	// The plain and dithered gray ranges must not overlap, or a dithered cell
	// would decode as a plain gray remap.
	if fogCh1GrayPlain+fogSlots > fogCh1GrayDith {
		t.Fatalf("plain gray range [%d,%d) overlaps the dithered range at %d",
			fogCh1GrayPlain, fogCh1GrayPlain+fogSlots, fogCh1GrayDith)
	}
	// The atlas holds both families; the black rows follow the four gray rows.
	if fogAtlasRows*fogAtlasCols != 2*fogSlots {
		t.Fatalf("atlas holds %d slots, the encoding names %d", fogAtlasRows*fogAtlasCols, 2*fogSlots)
	}
}

// The cell lattice comes from the ops, never from a camera pointer. Every op's
// rebased origin is the grid origin plus a multiple of 32, and the cell index
// the pass derives from a pixel must be the cell that produced the op.
func TestFogRegionLatticeFromOps(t *testing.T) {
	const camX, camZ = 401, 97 // an odd, unaligned camera: the hard case
	ops := []render.FogOp{
		fogOpAt(20, 7, camX, camZ, render.FogKindSolidDark),
		fogOpAt(14, 4, camX, camZ, render.FogKindGrayRemap),
		fogOpAt(23, 9, camX, camZ, render.FogKindPatterned),
	}
	region := fogRegionFor(ops, 640, 480)
	if !region.ok {
		t.Fatal("region rejected an on-screen op list")
	}
	// Cell (14,4) is the smallest in both axes, so it is grid cell (0,0).
	wantX := 14*render.FogTilePixels + render.FogTilePixels/2 - camX
	wantY := 4*render.FogTilePixels + render.FogTilePixels/2 - camZ
	if region.originX != int32(wantX) || region.originY != int32(wantY) {
		t.Fatalf("grid origin = (%d,%d), want (%d,%d)", region.originX, region.originY, wantX, wantY)
	}
	if region.cols != 23-14+1 || region.rows != 9-4+1 {
		t.Fatalf("grid extent = %dx%d, want %dx%d", region.cols, region.rows, 23-14+1, 9-4+1)
	}
	// The covered region must contain every fill and every fog GAF frame the
	// ops can produce: from the leftmost clamped origin through the rightmost
	// origin plus one tile, clipped to the framebuffer.
	if region.x0 > int32(wantX) || region.y0 > int32(wantY) {
		t.Fatalf("region top-left (%d,%d) excludes the first cell origin (%d,%d)",
			region.x0, region.y0, wantX, wantY)
	}
	lastX := 23*render.FogTilePixels + render.FogTilePixels/2 - camX + fogAtlasTile
	if int(region.x1) < lastX && region.x1 != 640 {
		t.Fatalf("region right edge %d excludes the last cell's frame reach %d", region.x1, lastX)
	}
	for i := range ops {
		rawX, rawY, _, _, _, _, ok := fogOpRect(&ops[i], 640, 480)
		if !ok {
			t.Fatalf("op %d clipped away", i)
		}
		if (rawX-region.originX)%render.FogTilePixels != 0 || (rawY-region.originY)%render.FogTilePixels != 0 {
			t.Fatalf("op %d origin (%d,%d) is off the lattice at (%d,%d)",
				i, rawX, rawY, region.originX, region.originY)
		}
		// The pass recovers the cell from a pixel inside it by one floor
		// division against the same lattice.
		col := int((rawX + 5 - region.originX) / render.FogTilePixels)
		row := int((rawY + 5 - region.originY) / render.FogTilePixels)
		if col != int(ops[i].GridX)-14 || row != int(ops[i].GridY)-4 {
			t.Fatalf("op %d recovers cell (%d,%d), want (%d,%d)",
				i, col, row, ops[i].GridX-14, ops[i].GridY-4)
		}
	}
}

// A cell that hangs off the left or top edge is the one case where the classic
// composer's clamp is visible: it clamps the cell rectangle to the framebuffer
// and hands the CLAMPED origin to the fog GAF blit, so the frame is anchored at
// the screen edge rather than at the cell origin. The grid keeps the unclamped
// origin, because that is what the 32-pixel fills are measured from.
func TestFogOpRectClampsAnchorButKeepsLattice(t *testing.T) {
	// Camera residue 5 puts the first visible column's origin at -5.
	op := fogOpAt(0, 0, 21, 21, render.FogKindSolidDark)
	rawX, rawY, x0, y0, x1, y1, ok := fogOpRect(&op, 640, 480)
	if !ok {
		t.Fatal("a cell overlapping the origin was rejected")
	}
	if rawX != -5 || rawY != -5 {
		t.Fatalf("unclamped origin = (%d,%d), want (-5,-5)", rawX, rawY)
	}
	if x0 != 0 || y0 != 0 {
		t.Fatalf("clamped anchor = (%d,%d), want (0,0)", x0, y0)
	}
	if x1 != 27 || y1 != 27 {
		t.Fatalf("clamped far edge = (%d,%d), want (27,27)", x1, y1)
	}
	// A cell entirely off the left edge is skipped, exactly as classicSink.Fog
	// skips a degenerate rectangle.
	off := fogOpAt(-1, 0, 21, 21, render.FogKindSolidDark)
	if _, _, _, _, _, _, ok := fogOpRect(&off, 640, 480); ok {
		t.Fatal("a cell entirely left of the framebuffer was not skipped")
	}
}

// The grid encoding is the whole contract between the op list and the shader:
// channel one in red, channel zero in green, an absent palette or GAF frame
// leaving the cell at zero so it keeps the underlying tile.
func TestFogGridEncoding(t *testing.T) {
	const camX, camZ = 400, 96 // aligned: cell (12,3) sits at screen (0,0)
	solid := fogOpAt(13, 3, camX, camZ, render.FogKindSolidDark)
	gray := fogOpAt(14, 3, camX, camZ, render.FogKindGrayRemap)
	pat := fogOpAt(15, 3, camX, camZ, render.FogKindPatterned)
	gafGray := fogOpAt(16, 3, camX, camZ, render.FogKindGAFCh1)
	gafGray.Variant, gafGray.Frame = 2, 5
	gafDith := fogOpAt(17, 3, camX, camZ, render.FogKindGAFCh1)
	gafDith.Variant, gafDith.Frame, gafDith.Patterned = 1, 3, true
	gafBlack := fogOpAt(19, 3, camX, camZ, render.FogKindGAFCh0)
	gafBlack.Variant, gafBlack.Frame = 0, 13
	missing := fogOpAt(18, 3, camX, camZ, render.FogKindGAFCh1)
	missing.Variant, missing.Frame = 3, 9 // never marked present below
	ops := []render.FogOp{solid, gray, pat, gafGray, gafDith, gafBlack, missing}

	var f fogPass
	f.slotPresent[0*fogSlots+2*fogAtlasCols+5] = true  // Gray3 frame 5
	f.slotPresent[0*fogSlots+1*fogAtlasCols+3] = true  // Gray2 frame 3
	f.slotPresent[1*fogSlots+0*fogAtlasCols+13] = true // Black1 frame 13

	region := fogRegionFor(ops, 640, 480)
	if !region.ok {
		t.Fatal("region rejected the op list")
	}
	f.encodeGrid(region, ops, 640, 480, true)
	imgW, _ := f.gridImageSize(region)
	at := func(gx int32) (byte, byte) {
		col := int(gx - 13)
		i := (0*imgW + col) * 4
		return f.gridBuf[i], f.gridBuf[i+1]
	}

	if c1, c0 := at(13); c1 != fogCh1None || c0 != fogCh0Solid {
		t.Fatalf("solid cell = (%d,%d), want (0,%d)", c1, c0, fogCh0Solid)
	}
	if c1, c0 := at(19); c1 != fogCh1None || c0 != byte(fogCh0Black+0*fogAtlasCols+13) {
		t.Fatalf("black GAF cell = (%d,%d), want (0,%d)", c1, c0, fogCh0Black+13)
	}
	if c1, c0 := at(14); c1 != fogCh1GrayFill || c0 != fogCh0None {
		t.Fatalf("gray fill cell = (%d,%d), want (%d,0)", c1, c0, fogCh1GrayFill)
	}
	if c1, c0 := at(15); c1 != fogCh1PatFill || c0 != fogCh0None {
		t.Fatalf("patterned cell = (%d,%d), want (%d,0)", c1, c0, fogCh1PatFill)
	}
	if c1, _ := at(16); c1 != byte(fogCh1GrayPlain+2*fogAtlasCols+5) {
		t.Fatalf("plain gray GAF cell = %d, want %d", c1, fogCh1GrayPlain+2*fogAtlasCols+5)
	}
	if c1, _ := at(17); c1 != byte(fogCh1GrayDith+1*fogAtlasCols+3) {
		t.Fatalf("dithered gray GAF cell = %d, want %d", c1, fogCh1GrayDith+1*fogAtlasCols+3)
	}
	if c1, c0 := at(18); c1 != fogCh1None || c0 != fogCh0None {
		t.Fatalf("cell with a missing GAF frame = (%d,%d), want (0,0)", c1, c0)
	}

	// Without a gray table the gray-reading operations are skipped and nothing
	// else changes, exactly as fogFillGray and blitFogGAF(fogBlitGray) return
	// early when the palette is absent [03 §3.3].
	f.encodeGrid(region, ops, 640, 480, false)
	if c1, _ := at(14); c1 != fogCh1None {
		t.Fatalf("gray fill without a gray table = %d, want 0", c1)
	}
	if c1, _ := at(16); c1 != fogCh1None {
		t.Fatalf("plain gray GAF without a gray table = %d, want 0", c1)
	}
	if c1, _ := at(15); c1 != fogCh1PatFill {
		t.Fatalf("dithered fill without a gray table = %d, want %d", c1, fogCh1PatFill)
	}
	if c1, _ := at(17); c1 != byte(fogCh1GrayDith+1*fogAtlasCols+3) {
		t.Fatalf("dithered GAF without a gray table = %d, want %d", c1, fogCh1GrayDith+1*fogAtlasCols+3)
	}
	if _, c0 := at(19); c0 != byte(fogCh0Black+13) {
		t.Fatalf("black GAF without a gray table = %d, want %d", c0, fogCh0Black+13)
	}
}

// The grid texture grows with the visible cell range and is never recreated when
// the range shrinks, so a panning camera reuses one texture (§11.2 allocation
// policy).
// The fog grid is rebuilt from the op list every frame, so its encode must reuse
// its buffer (docs/DESIGN_GPU_RENDERER.md §11.2 "Allocation policy").
func TestFogGridEncodeIsAllocationFree(t *testing.T) {
	const camX, camZ = 400, 96
	ops := []render.FogOp{
		fogOpAt(13, 3, camX, camZ, render.FogKindSolidDark),
		fogOpAt(14, 3, camX, camZ, render.FogKindGrayRemap),
		fogOpAt(15, 3, camX, camZ, render.FogKindPatterned),
		fogOpAt(16, 4, camX, camZ, render.FogKindGAFCh1),
		fogOpAt(17, 5, camX, camZ, render.FogKindGAFCh0),
	}
	var f fogPass
	f.slotPresent[0*fogSlots] = true
	f.slotPresent[1*fogSlots] = true
	encode := func() {
		region := fogRegionFor(ops, 640, 480)
		f.encodeGrid(region, ops, 640, 480, true)
	}
	encode()
	if got := testing.AllocsPerRun(8, encode); got != 0 {
		t.Fatalf("fog grid encode allocated %v objects per frame, want none", got)
	}
}

func TestFogGridImageGrowsOnly(t *testing.T) {
	var f fogPass
	f.gridW, f.gridH = 40, 30
	w, h := f.gridImageSize(fogRegion{cols: 12, rows: 9})
	if w != 40 || h != 30 {
		t.Fatalf("shrinking range resized the grid to %dx%d", w, h)
	}
	w, h = f.gridImageSize(fogRegion{cols: 64, rows: 12})
	if w != 64 || h != 30 {
		t.Fatalf("growing range gave %dx%d, want 64x30", w, h)
	}
}

// A frame is placed in its atlas tile at the position the retail anchor puts it
// relative to the cell origin, and one that reaches past the 2x2 cell
// neighbourhood the pass visits does not fit.
func TestFogFrameTilePlacement(t *testing.T) {
	fits := &formats.GAFFrame{Width: 33, Height: 19, XOffset: 0, YOffset: -13}
	ox, oy, ok := fogFrameTilePlacement(fits)
	if !ok || ox != 0 || oy != 13 {
		t.Fatalf("33x19 at offset (0,-13): (%d,%d,%v), want (0,13,true)", ox, oy, ok)
	}
	before := &formats.GAFFrame{Width: 16, Height: 16, XOffset: 4}
	if _, _, ok := fogFrameTilePlacement(before); ok {
		t.Fatal("a frame starting before the cell origin was accepted")
	}
	past := &formats.GAFFrame{Width: fogAtlasTile + 1, Height: 16}
	if _, _, ok := fogFrameTilePlacement(past); ok {
		t.Fatal("a frame reaching past the cell neighbourhood was accepted")
	}
	degenerate := &formats.GAFFrame{Width: 0, Height: 16}
	if _, _, ok := fogFrameTilePlacement(degenerate); ok {
		t.Fatal("a degenerate frame was accepted")
	}
}

// I14: the atlas tile is a claim about how far the shipped fog art reaches past
// a cell origin, so it is measured against the reference install rather than
// trusted. A frame that did not fit would be dropped with a diagnostic, which is
// visible fog art going missing.
func TestFogAtlasFitsRetail(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount %s: %v", root, err)
	}
	gaf, err := formats.LoadGAFFile(fs, "anims/fog.gaf")
	if err != nil {
		t.Fatalf("load anims/fog.gaf: %v", err)
	}
	maxX, maxY, frames := 0, 0, 0
	for _, prefix := range []string{"Gray", "Black"} {
		for variant := 1; variant <= fogVariants; variant++ {
			name := fmt.Sprintf("%s%d", prefix, variant)
			entry, ok := gaf.Find(name)
			if !ok {
				t.Fatalf("anims/fog.gaf has no entry %s", name)
			}
			for i, ref := range entry.Frames {
				if ref.Frame == nil {
					continue
				}
				ox, oy, ok := fogFrameTilePlacement(ref.Frame)
				if !ok {
					t.Fatalf("%s frame %d (%dx%d at offset (%d,%d)) does not fit a %d-pixel tile",
						name, i, ref.Frame.Width, ref.Frame.Height, ox, oy, fogAtlasTile)
				}
				frames++
				maxX = maxInt(maxX, ox+int(ref.Frame.Width))
				maxY = maxInt(maxY, oy+int(ref.Frame.Height))
			}
		}
	}
	if frames == 0 {
		t.Fatal("anims/fog.gaf decoded no fog frames")
	}
	t.Logf("%d fog frames reach at most %d x %d past a cell origin (tile %d)", frames, maxX, maxY, fogAtlasTile)
}

// checkFogDevicePixels drives the compiled fog pass on a real device and
// compares its result against the classic byte writers' rule for every pixel of
// a small surface (C-G7). It is written to the shape the package's existing
// device fixtures use so the hidden Ebitengine loop in TestMain can call it;
// TestFogDeviceFixture explains why this package cannot start that loop itself.
//
// The scene is one solid fill, then a fog command with one cell per kind: a
// solid fill, a gray remap, a dithered checker, and a black-family fog GAF whose
// authored offset makes it overhang into the next cell.
func checkFogDevicePixels() error {
	pal := fixturePalette()
	for i := 0; i < 256; i++ {
		pal.Gray[i] = byte(255 - i)
	}
	const w, h = 128, 64
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	// A 16x16 opaque block anchored one cell to the right of its own cell, so
	// the pass has to reach it from the neighbouring cell.
	frame := &formats.GAFFrame{Width: 16, Height: 16, XOffset: -16, YOffset: 0,
		Pixels: make([]byte, 16*16), Transparent: make([]bool, 16*16)}
	for i := range frame.Pixels {
		frame.Pixels[i] = 3
	}
	entry := &formats.GAFEntry{Name: "Black1", Frames: []formats.GAFFrameRef{{Frame: frame}}}

	// Camera (16,16) puts cell (0,0)'s rebased origin at (0,0).
	const camX, camZ = 16, 16
	solid := fogOpAt(0, 0, camX, camZ, render.FogKindSolidDark)
	gray := fogOpAt(1, 0, camX, camZ, render.FogKindGrayRemap)
	pat := fogOpAt(2, 0, camX, camZ, render.FogKindPatterned)
	black := fogOpAt(0, 1, camX, camZ, render.FogKindGAFCh0)
	black.Variant, black.Frame = 0, 0
	ops := []render.FogOp{solid, gray, pat, black}

	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 100, Style: drawlist.FillSolid})
	list.RecordFog(drawlist.Fog{Ops: ops, Black: [4]*formats.GAFEntry{entry}})
	list.RecordExpand()
	img := r.Execute(&list, w, h)
	if img == nil {
		return fmt.Errorf("fog device fixture returned no image")
	}
	if r.fog.draws != 1 {
		return fmt.Errorf("fog issued %d device draws for %d ops, want 1", r.fog.draws, len(ops))
	}
	// Clear, fill and fog all land in one phase: the fills are opaque and the fog
	// reads them, and a phase draws its opaque batch before its destination batch.
	// That one phase costs three destination switches — the opaque batch into the
	// composed surface, the read-surface copy, the destination batch back into the
	// composite — and the expansion one more
	// (docs/DESIGN_GPU_RENDERER.md §11.5).
	stats := r.ModelStats()
	if stats.Phases != 1 {
		return fmt.Errorf("fog fixture compiled %d phases, want 1", stats.Phases)
	}
	if stats.Passes != 4 {
		return fmt.Errorf("fog fixture issued %d destination switches for %d phases, want 4",
			stats.Passes, stats.Phases)
	}
	pixels := make([]byte, w*h*4)
	img.ReadPixels(pixels)
	parity := (int32(camX) + int32(camZ)) & 1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			want := byte(100)
			switch {
			case x < 32 && y < 32:
				want = render.FogDarkPaletteIndex
			case x >= 32 && x < 64 && y < 32:
				want = pal.Gray[100]
			case x >= 64 && x < 96 && y < 32:
				if (int32(x)+int32(y)+parity)&1 == 1 {
					want = render.FogDarkPaletteIndex
				}
			case x >= 16 && x < 32 && y >= 32 && y < 48:
				want = 3 // the overhanging black-family frame
			}
			if got := pixels[(y*w+x)*4]; got != want {
				return fmt.Errorf("fog device pixel (%d,%d): index %d, want %d", x, y, got, want)
			}
		}
	}
	return nil
}

// TestFogDeviceFixture is opt-in because ordinary tests must not require a
// graphics device (C-G10).
//
// Ebitengine rejects RunGame from a testing worker goroutine, so the device loop
// starts in TestMain on the process main goroutine, and the fixture game's Draw
// calls checkFogDevicePixels there alongside the other device checks. This test
// reports that loop's result.
func TestFogDeviceFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device fog fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}
