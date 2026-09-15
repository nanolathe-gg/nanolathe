package gpurender

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// worldRegion is the boundary marker a test opens its world region with.
func worldRegion(z camera.Zoom, step camera.ViewScale, recW, recH int32) drawlist.WorldSpace {
	return drawlist.WorldSpace{
		Begin: true, Zoom: z, Step: step, RecordW: recW, RecordH: recH,
		Viewport: drawlist.Rect{X: camera.OriginX, Y: camera.OriginY, W: 64, H: 64},
	}
}

// A world region on a rest step is the identity: the transform is disarmed and
// the compiled geometry is exactly what the same command compiled to before the
// region existed. That is what keeps the §6 parity gate exact at 1x and 2x
// (DESIGN_GPU_RENDERER §16.3).
func TestWorldRegionIsTheIdentityOnARestStep(t *testing.T) {
	r, _ := schedulerFixture(t)
	for _, tc := range []struct {
		z    camera.Zoom
		step camera.ViewScale
	}{
		{camera.ZoomUnit, camera.ViewScaleNative},
		{camera.ZoomMax, camera.ViewScaleDetail},
	} {
		r.sched.resetFrame(64, 64)
		r.worldW, r.worldH = 64, 64
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 4, Y: 6, W: 10, H: 8}, Index: 3})
		plain := append([]float32(nil), quadCorners(r)...)

		r.sched.resetFrame(64, 64)
		r.worldW, r.worldH = 64, 64
		r.World(worldRegion(tc.z, tc.step, 64, 64))
		if r.sched.worldOn {
			t.Fatalf("%s on step %s armed the transform", tc.z, tc.step)
		}
		if r.clipW() != 64 || r.clipH() != 64 {
			t.Fatalf("%s: clip extent %dx%d, want the framebuffer", tc.z, r.clipW(), r.clipH())
		}
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 4, Y: 6, W: 10, H: 8}, Index: 3})
		got := quadCorners(r)
		if len(got) != len(plain) {
			t.Fatalf("%s: %d compiled corners, want %d", tc.z, len(got), len(plain))
		}
		for i := range got {
			if got[i] != plain[i] {
				t.Fatalf("%s: corner %d = %v, want %v (the region must be the identity)", tc.z, i, got[i], plain[i])
			}
		}
	}
}

// Off a rest step the region scales every world command by the live factor over
// the record step, about the surface origin — one affine transform, applied at
// the scheduler's intake so the placed rectangle and the appended vertices agree
// (§16.3).
func TestWorldRegionScalesAWorldQuad(t *testing.T) {
	r, frame := schedulerFixture(t)
	// 1.5x recorded at the 2x step: the recording shrinks by three quarters.
	r.sched.resetFrame(64, 64)
	r.worldW, r.worldH = 64, 64
	r.World(worldRegion(camera.ZoomUnit*3/2, camera.ViewScaleDetail, 86, 86))
	if !r.sched.worldOn {
		t.Fatal("1.5x recorded at the 2x step left the transform disarmed")
	}
	if r.clipW() != 86 || r.clipH() != 86 {
		t.Fatalf("clip extent %dx%d, want the record extent 86x86", r.clipW(), r.clipH())
	}
	frame.Width, frame.Height = 8, 8
	frame.Pixels = make([]byte, 64)
	frame.Transparent = make([]bool, 64)
	r.Sprite(drawlist.Sprite{Frame: frame, X: 16, Y: 24, Kind: drawlist.BlitKeyed})
	got := quadCorners(r)
	if len(got) != 4 {
		t.Fatalf("compiled %d corners, want one quad", len(got))
	}
	// The recorded rectangle is (16,24)..(24,32); three quarters of it is
	// (12,18)..(18,24).
	want := []float32{12, 18, 18, 24}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("scaled quad = %v, want %v", got, want)
		}
	}

	// Closing the region disarms the transform, so the chrome that follows keeps
	// its authored pixel size.
	r.World(drawlist.WorldSpace{Begin: false})
	if r.sched.worldOn || r.clipW() != 64 || r.clipH() != 64 {
		t.Fatalf("closing the region left worldOn=%v clip=%dx%d", r.sched.worldOn, r.clipW(), r.clipH())
	}
}

// A world primitive that shrinks below one screen pixel keeps one, so the
// selection quad's lines and a dotted path's dots cannot fall between two pixel
// centres and vanish at an arbitrary subset of factors (§16.3).
//
// It keeps EXACTLY one, not at least one. A span of exactly 1.0 contains exactly
// one pixel centre wherever it starts, so the primitive is one pixel wide at
// every factor; a span the rule rounded up to more than that would cover one
// centre at some factors and two at others, and a one-pixel line would flicker
// between one and two pixels as the view eased. Anything already wider than a
// screen pixel is left alone, so the terrain's tiles and every sprite still tile
// the plane exactly.
func TestSubPixelWorldPrimitivesKeepExactlyOnePixel(t *testing.T) {
	r, _ := schedulerFixture(t)
	for _, z := range []camera.Zoom{camera.ZoomUnit / 4, camera.ZoomUnit * 7 / 10,
		camera.ZoomUnit * 71 / 100, camera.ZoomUnit * 99 / 100} {
		r.sched.resetFrame(64, 64)
		r.worldW, r.worldH = 64, 64
		r.World(worldRegion(z, camera.ViewScaleNative, 1024, 1024))
		r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 8, Y: 8, W: 1, H: 1}, Index: 5})
		got := quadCorners(r)
		if len(got) != 4 {
			t.Fatalf("%s: compiled %d corners, want one quad", z, len(got))
		}
		if got[2]-got[0] != 1 || got[3]-got[1] != 1 {
			t.Fatalf("%s: a one-pixel world fill compiled to the span %vx%v, want exactly 1x1",
				z, got[2]-got[0], got[3]-got[1])
		}
	}

	// A primitive already wider than a screen pixel is not touched: the rule is a
	// floor, not a thickening.
	r.sched.resetFrame(64, 64)
	r.worldW, r.worldH = 64, 64
	r.World(worldRegion(camera.ZoomUnit*7/10, camera.ViewScaleNative, 1024, 1024))
	r.Fill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: 10, H: 10}, Index: 5})
	got := quadCorners(r)
	if len(got) != 4 {
		t.Fatalf("compiled %d corners, want one quad", len(got))
	}
	k := float32(camera.ZoomUnit*7/10) / float32(camera.ZoomUnit)
	if want := 10 * k; got[2] != want || got[3] != want {
		t.Fatalf("a ten-pixel world fill compiled to %v, want the scaled %v", got, want)
	}
}

// quadCorners returns the opaque batch's single quad as x0,y0,x1,y1.
func quadCorners(r *Renderer) []float32 {
	verts := r.sched.classVerts(schedOpaque)
	if len(verts) < 4 {
		return nil
	}
	return []float32{verts[0].DstX, verts[0].DstY, verts[3].DstX, verts[3].DstY}
}

// litCoverage counts, per screen pixel, how many compiled destination quads
// cover it: a lit point brightens the destination, so covering a pixel twice
// brightens it twice.
func litCoverage(r *Renderer, w, h int) map[[2]int]int {
	verts := r.sched.classVerts(schedDest)
	cover := map[[2]int]int{}
	for i := 0; i+4 <= len(verts); i += 4 {
		x0, y0, x1, y1 := verts[i].DstX, verts[i].DstY, verts[i].DstX, verts[i].DstY
		for _, v := range verts[i+1 : i+4] {
			x0, y0 = min(x0, v.DstX), min(y0, v.DstY)
			x1, y1 = max(x1, v.DstX), max(y1, v.DstY)
		}
		for y := int(y0); y < int(y1) && y < h; y++ {
			for x := int(x0); x < int(x1) && x < w; x++ {
				cover[[2]int{x, y}]++
			}
		}
	}
	return cover
}

// Under the world transform the lit-point layer is resampled to one record
// point per screen pixel, so the explosion halo brightens each pixel exactly
// once (§16.3 "Lit points"). Scaling the points quad by quad let two or three
// record points land on one screen pixel and brighten it two or three times,
// which drew the halo as a lattice at fractional zoom.
func TestLitPointsBrightenEachScreenPixelOnceUnderTheTransform(t *testing.T) {
	r, _ := schedulerFixture(t)
	r.sched.resetFrame(64, 64)
	r.worldW, r.worldH = 64, 64
	// 1.5x is the 2x step shrunk by 0.75: four record pixels to three screen ones.
	r.World(worldRegion(camera.ZoomUnit*3/2, camera.ViewScaleDetail, 64, 64))
	if !r.sched.worldOn {
		t.Fatal("the fixture did not arm the transform")
	}
	var pts []drawlist.Point
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			pts = append(pts, drawlist.Point{X: int32(x), Y: int32(y), Index: 20})
		}
	}
	r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: pts})
	if !r.sched.worldOn {
		t.Fatal("the lit-point path left the transform disarmed")
	}
	cover := litCoverage(r, 64, 64)
	// The 16x16 record block is 12x12 screen pixels, each covered exactly once.
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			if n := cover[[2]int{x, y}]; n != 1 {
				t.Fatalf("screen pixel (%d,%d) is lit %d times, want once", x, y, n)
			}
		}
	}
	for p, n := range cover {
		if (p[0] >= 12 || p[1] >= 12) && n > 0 {
			t.Fatalf("screen pixel %v outside the shrunk block is lit", p)
		}
	}
}

// The resample is the inverse of nearest sampling, at every factor and not only
// the dyadic ones the fixtures above use (§16.3 "Lit points"). The rule it has
// to satisfy is a relationship, not a count: a screen pixel is lit exactly when
// the record point nearest sampling would read for its centre — floor((s+.5)/k)
// — is one of the batch's points. Selecting the destination pixel with
// floor(x*k) broke that at a non-dyadic factor: the point the filter keeps for
// screen pixel s can floor to s-1, so s was claimed by nobody and the halo drew
// with unlit columns (0.9 lost 60 of 179 columns, 0.8 lost 40, 0.7 lost 23).
// 0.75 and 0.5 are here because they are the factors that already passed, and
// must keep passing unchanged.
func TestLitPointResampleInvertsNearestSamplingAtEveryFactor(t *testing.T) {
	const block = 40
	for _, k := range []float32{.9, .8, .7, .75, .5, 1} {
		r, _ := schedulerFixture(t)
		r.sched.resetFrame(64, 64)
		r.worldW, r.worldH = 64, 64
		r.World(drawlist.WorldSpace{Begin: true, Factor: k, Step: camera.ViewScaleNative, RecordW: 128, RecordH: 128})
		if r.sched.worldOn != (k != 1) {
			t.Fatalf("factor %v: transform armed %v", k, r.sched.worldOn)
		}
		var pts []drawlist.Point
		for y := 0; y < block; y++ {
			for x := 0; x < block; x++ {
				pts = append(pts, drawlist.Point{X: int32(x), Y: int32(y), Index: 20})
			}
		}
		r.Points(drawlist.Points{Kind: drawlist.PointLit, Points: pts})
		cover := litCoverage(r, 64, 64)
		for sy := 0; sy < 64; sy++ {
			for sx := 0; sx < 64; sx++ {
				rx := math.Floor((float64(sx) + .5) / float64(k))
				ry := math.Floor((float64(sy) + .5) / float64(k))
				want := 0
				if rx >= 0 && ry >= 0 && rx < block && ry < block {
					want = 1
				}
				if got := cover[[2]int{sx, sy}]; got != want {
					t.Fatalf("factor %v: screen pixel (%d,%d) lit %d times, want %d (its nearest record point is %v,%v)", k, sx, sy, got, want, rx, ry)
				}
			}
		}
	}
}
