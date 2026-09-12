package gpurender

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

func fogTestLeaf(index byte, x, y, w, h int) *formats.GAFFrame {
	f := &formats.GAFFrame{Width: uint16(w), Height: uint16(h), XOffset: int16(-x), YOffset: int16(-y), Pixels: make([]byte, w*h)}
	for i := range f.Pixels {
		f.Pixels[i] = index
	}
	return f
}

func fogTestComposite(children ...*formats.GAFFrame) *formats.GAFFrame {
	// Neither the parent's size nor its anchor can clip or move the children.
	return &formats.GAFFrame{Width: 1, Height: 1, XOffset: 40, YOffset: 50, Subframes: children}
}

func fogTestCommand(fr *formats.GAFFrame, kind render.FogKind) drawlist.Fog {
	op := fogOpAt(0, 0, 16, 16, kind)
	op.Frame, op.Variant = 0, 0
	entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: fr}}}
	return drawlist.Fog{Ops: []render.FogOp{op}, Gray: [4]*formats.GAFEntry{entry}, Black: [4]*formats.GAFEntry{entry}}
}

func TestFogOrderedShaderCompiles(t *testing.T) {
	if _, err := ebiten.NewShader([]byte(fogOrderedShaderSource)); err != nil {
		t.Fatal(err)
	}
}

func TestFogOrderedRepeatedGrayUsesSeparateSnapshots(t *testing.T) {
	r, _ := schedulerFixture(t)
	f := fogTestComposite(fogTestLeaf(7, 4, 5, 2, 1), fogTestLeaf(8, 4, 5, 2, 1))
	f.Subframes[1].AlternateBlitter = 1
	r.sched.resetFrame(64, 64)
	r.Fog(fogTestCommand(f, render.FogKindGAFCh1))
	// Modern desaturation is idempotent; a pixel-only check cannot distinguish
	// one application from two. Separate ordered snapshots lock the traversal
	// contract; the classic fixture uses a non-idempotent authored gray table.
	if r.sched.nphase != 2 {
		t.Fatalf("overlapping gray children used %d phases, want separate destination snapshots", r.sched.nphase)
	}
	for i := 0; i < 2; i++ {
		batch := &r.sched.phases[i].batch[schedDest]
		if len(batch.runs) != 1 || batch.runs[0].readSlot != 0 || batch.runs[0].shader != r.fog.orderedShader {
			t.Fatalf("phase %d lost its gray destination operation", i)
		}
		if len(batch.verts) != 4 || batch.verts[0].DstX != 4 || batch.verts[0].DstY != 5 || batch.verts[3].DstX != 6 || batch.verts[3].DstY != 6 {
			t.Fatalf("phase %d clipped or moved child to parent canvas: %v", i, batch.verts)
		}
	}
}

func TestFogOrderedRawGateBeforeEveryRecursion(t *testing.T) {
	for _, mode := range []fogLeafMode{fogLeafGray, fogLeafChecker} {
		first, second := fogTestLeaf(7, 0, 0, 1, 1), fogTestLeaf(8, 0, 0, 1, 1)
		second.AlternateBlitter = 1
		f := fogTestComposite(first, second)
		collect := func() []byte {
			var got []byte
			walkFogLeaves(f, mode, func(leaf *formats.GAFFrame, gotMode fogLeafMode) {
				if gotMode != mode {
					t.Fatal("gray/checker child changed consumer")
				}
				got = append(got, leaf.Pixels[0])
			})
			return got
		}
		if got := collect(); len(got) != 2 || got[0] != 7 || got[1] != 8 {
			t.Fatalf("ordered children = %v", got)
		}
		second.Compressed = 1
		if got := collect(); len(got) != 1 || got[0] != 7 {
			t.Fatalf("compressed child bypassed gate: %v", got)
		}
		f.Compressed = 1
		if got := collect(); len(got) != 0 {
			t.Fatalf("compressed parent recursed: %v", got)
		}
	}
}

func TestFogOrderedBlackPropagatesAlternateChildTint(t *testing.T) {
	first := fogTestLeaf(10, 0, 0, 1, 1)
	alternate := fogTestComposite(fogTestLeaf(20, 0, 0, 1, 1), fogTestLeaf(30, 0, 0, 1, 1))
	alternate.AlternateBlitter = 1
	last := fogTestLeaf(40, 0, 0, 1, 1)
	var values []byte
	var modes []fogLeafMode
	walkFogLeaves(fogTestComposite(first, alternate, last), fogLeafBlack, func(f *formats.GAFFrame, mode fogLeafMode) {
		values, modes = append(values, f.Pixels[0]), append(modes, mode)
	})
	if fmt.Sprint(values) != "[10 20 30 40]" || fmt.Sprint(modes) != fmt.Sprint([]fogLeafMode{fogLeafBlack, fogLeafTint, fogLeafTint, fogLeafBlack}) {
		t.Fatalf("black traversal: values %v modes %v", values, modes)
	}
}

func TestFogOrderedZoomClipsBeforeSamplingAtlasNeighbours(t *testing.T) {
	for _, k := range []float32{0.25, 0.375, 0.75, 1, 1.25} {
		x0, y0, x1, y1 := fogRemapScreenRect(5, 7, 19, 23, k)
		for sy := 0; sy < 32; sy++ {
			for sx := 0; sx < 32; sx++ {
				x, y := int(math.Floor(float64(sx)/float64(k))), int(math.Floor(float64(sy)/float64(k)))
				want := x >= 5 && x < 19 && y >= 7 && y < 23
				got := sx >= x0 && sx < x1 && sy >= y0 && sy < y1
				if got != want {
					t.Fatalf("zoom %g screen (%d,%d) samples (%d,%d), admitted %t", k, sx, sy, x, y, got)
				}
			}
		}
	}
}

func TestFogOrderedSelectionPreservesStockFastPath(t *testing.T) {
	leaf := fogTestLeaf(7, 0, 0, 16, 16)
	fg := fogTestCommand(leaf, render.FogKindGAFCh1)
	if fogNeedsOrdered(fg, camera.ViewScaleNative) {
		t.Fatal("ordinary stock geometry lost atlas path")
	}
	composite := fogTestComposite(leaf)
	fg = fogTestCommand(composite, render.FogKindGAFCh1)
	if !fogNeedsOrdered(fg, camera.ViewScaleNative) {
		t.Fatal("selected composite remained in atlas")
	}
	composite.Compressed = 1
	if fogNeedsOrdered(fg, camera.ViewScaleNative) {
		t.Fatal("compressed gray parent bypassed gate")
	}
	leaf.XOffset = 4
	if !fogNeedsOrdered(fogTestCommand(leaf, render.FogKindGAFCh0), camera.ViewScaleNative) {
		t.Fatal("leaf extending before cell origin remained in atlas")
	}
}

// checkFogOrderedDevicePixels runs in the existing fog device fixture. Its
// source is authored here; stock captures cannot cover alternate fog children.
func checkFogOrderedDevicePixels() error {
	const w, h = 64, 48
	pal := fixturePalette()
	pal.Base[100] = [4]byte{30, 60, 90, 255}
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	// Two ALP children in a tinted subtree follow an opaque child. They must
	// yield ((40+120)/2+200)/2 = 140; reversing them yields 120.
	alternate := fogTestComposite(fogTestLeaf(120, 4, 4, 12, 12), fogTestLeaf(200, 4, 4, 12, 12))
	alternate.AlternateBlitter = 1
	black := fogTestComposite(fogTestLeaf(40, 4, 4, 12, 12), alternate)
	// The two gray children overlap beyond their parent bounds, with a key
	// hole in the first. A compressed child must leave the third region alone.
	grayLeaf := fogTestLeaf(7, 24, 4, 12, 12)
	grayLeaf.Transparent = make([]bool, len(grayLeaf.Pixels))
	grayLeaf.Transparent[0] = true
	compressed := fogTestLeaf(7, 44, 4, 12, 12)
	compressed.Compressed = 1
	gray := fogTestComposite(grayLeaf, fogTestLeaf(8, 28, 4, 8, 12), compressed)
	checker := fogTestCommand(fogTestComposite(fogTestLeaf(7, 24, 24, 12, 12)), render.FogKindGAFCh1)
	checker.Ops[0].Patterned = true
	gated := fogTestComposite(fogTestLeaf(7, 44, 24, 12, 12))
	gated.Compressed = 1
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 100})
	list.RecordFog(fogTestCommand(black, render.FogKindGAFCh0))
	list.RecordFog(fogTestCommand(gray, render.FogKindGAFCh1))
	list.RecordFog(checker)
	list.RecordFog(fogTestCommand(gated, render.FogKindGAFCh1))
	list.RecordExpand()
	img := r.Execute(&list, w, h)
	pixels := make([]byte, w*h*4)
	img.ReadPixels(pixels)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			want := [4]byte{30, 60, 90, 255}
			switch {
			case x >= 4 && x < 16 && y >= 4 && y < 16:
				want = [4]byte{140, 140, 140, 255}
			case x >= 24 && x < 36 && y >= 4 && y < 16 && !(x == 24 && y == 4):
				want = [4]byte{60, 60, 60, 255}
			case x >= 24 && x < 36 && y >= 24 && y < 36 && (x+y)&1 != 0:
				want = [4]byte{0, 0, 0, 255}
			}
			at := (y*w + x) * 4
			if got := [4]byte(pixels[at : at+4]); got != want {
				return fmt.Errorf("ordered fog pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
	if path := os.Getenv("NANOLATHE_FOG_COMPOSITE_CAPTURE"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		err = png.Encode(file, &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	return nil
}
