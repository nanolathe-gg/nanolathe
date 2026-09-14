package gpurender

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

const (
	featureDeviceWidth      = 80
	featureDeviceHeight     = 20
	featureDeviceBackground = byte(201)
	featureDeviceShadow     = byte(37)
	featureDeviceBody       = byte(41)
	// The ALP builder's own target for each pair over the background:
	// floor((src + dst)/2) [03 §4.3.4]. The Enhanced composite evaluates that in
	// RGB instead of looking it up, so these are the classic indices the blended
	// pixels are measured against (§13.4).
	featureDeviceShadowALP = byte((int(featureDeviceShadow) + int(featureDeviceBackground)) / 2)
	featureDeviceBodyALP   = byte((int(featureDeviceBody) + int(featureDeviceBackground)) / 2)
)

// Feature shadows reuse the existing keyed and ALP-tinted GPU stages. The
// producer records the final top-left, while drawTint accepts an anchor; both
// routes must therefore submit identical destination/source geometry despite
// nonzero authored offsets [03 §5.3.1][R-REN-03D §4].
func TestFeatureShadowRoutesPreserveAuthoredGeometry(t *testing.T) {
	// The same check runs inside the device fixture loop, which is where a
	// renderer can still allocate images.
	skipAfterDeviceLoop(t)
	if err := checkFeatureShadowRouteGeometry(); err != nil {
		t.Fatal(err)
	}
}

// checkFeatureShadowRouteGeometry compiles both feature-shadow routes and
// compares the geometry they submit. It needs a renderer, so it runs either as
// an ordinary test or inside the device fixture loop, never after it.
func checkFeatureShadowRouteGeometry() error {
	p := &palette.Tables{}
	p.Alpha[37*256+201] = 88
	r, err := NewChecked(p, 16, 12)
	if err != nil {
		return fmt.Errorf("compile GPU shaders: %w", err)
	}
	f := &formats.GAFFrame{
		Width: 2, Height: 1, XOffset: 2, YOffset: 1,
		ColorKey: 9, Pixels: []byte{37, 9}, Transparent: []bool{false, true},
	}
	for _, tc := range []struct {
		name  string
		trans bool
		class int
	}{
		{name: "opaque keyed", class: schedOpaque},
		{name: "translucent ALP", trans: true, class: schedOpaque},
	} {
		r.sched.resetFrame(16, 12)
		r.Sprite(drawlist.Sprite{Frame: f, X: 3, Y: 4, Kind: drawlist.BlitFeatureShadow, Trans: tc.trans})
		verts := r.sched.classVerts(tc.class)
		if len(verts) != 4 {
			return fmt.Errorf("%s: compiled %d vertices, want 4", tc.name, len(verts))
		}
		// The frame's scene atlas region is the source; both routes must place
		// the same destination rectangle over the same region texels.
		e := r.sceneFrameFor(f)
		ax, ay := e.x, e.y
		want := [4]ebitenVertexPoint{
			{3, 4, float32(ax), float32(ay)},
			{5, 4, float32(ax + 2), float32(ay)},
			{3, 5, float32(ax), float32(ay + 1)},
			{5, 5, float32(ax + 2), float32(ay + 1)},
		}
		for i, v := range verts {
			got := ebitenVertexPoint{v.DstX, v.DstY, v.SrcX, v.SrcY}
			if got != want[i] {
				return fmt.Errorf("%s: vertex %d = %+v, want %+v", tc.name, i, got, want[i])
			}
		}
	}
	return nil
}

type ebitenVertexPoint struct {
	dstX, dstY float32
	srcX, srcY float32
}

// checkFeatureShadowDevicePixels runs inside modelFixtureGame's opt-in
// main-thread Ebitengine loop. It verifies the authored feature commands after
// actual shader execution and readback. The first four blocks are static opaque
// shadow, static translucent shadow, static translucent body and an opaque live
// event shadow; the fifth stays empty to represent the producer's disabled
// feature-shadow gate. The producer tests above the GPU package lock that this
// gate remains independent when Shading is off [03 §5.3][03 R-RAST-01 §6]
// [R-REN-03D §4].
func checkFeatureShadowDevicePixels() error {
	if err := checkFeatureShadowRouteGeometry(); err != nil {
		return err
	}
	var p palette.Tables
	for i := 0; i < 256; i++ {
		p.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
	}
	fixtureALP(&p)
	r, err := NewChecked(&p, featureDeviceWidth, featureDeviceHeight)
	if err != nil {
		return fmt.Errorf("compile feature-shadow fixture shaders: %w", err)
	}
	frame := func(index byte) *formats.GAFFrame {
		pixels := make([]byte, 8*8)
		transparent := make([]bool, 8*8)
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				i := y*8 + x
				pixels[i] = index
				transparent[i] = x == 7
			}
		}
		return &formats.GAFFrame{
			Width: 8, Height: 8, XOffset: 2, YOffset: 1,
			ColorKey: 9, Pixels: pixels, Transparent: transparent,
		}
	}
	shadow, body := frame(featureDeviceShadow), frame(featureDeviceBody)
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{
		Rect:  drawlist.Rect{W: featureDeviceWidth, H: featureDeviceHeight},
		Index: featureDeviceBackground,
		Style: drawlist.FillSolid,
	})
	// These are the exact records produced after the client has applied the
	// independent option and static/live dispatcher decisions.
	list.RecordSprite(drawlist.Sprite{Frame: shadow, X: 4, Y: 6, Kind: drawlist.BlitFeatureShadow})
	list.RecordSprite(drawlist.Sprite{Frame: shadow, X: 20, Y: 6, Kind: drawlist.BlitFeatureShadow, Trans: true})
	list.RecordSprite(drawlist.Sprite{Frame: body, X: 36, Y: 6, Kind: drawlist.BlitFeatureNormal, Trans: true})
	list.RecordSprite(drawlist.Sprite{Frame: shadow, X: 52, Y: 6, Kind: drawlist.BlitFeatureShadow})
	list.RecordExpand()
	img := r.Execute(&list, featureDeviceWidth, featureDeviceHeight)
	if img == nil {
		return fmt.Errorf("feature-shadow fixture renderer returned no image")
	}
	pixels := make([]byte, featureDeviceWidth*featureDeviceHeight*4)
	img.ReadPixels(pixels)
	// The composite is colour; the palette is the grey ramp, so a check names the
	// classic index and the helpers expand it through PAL (§13.4). Only the two
	// translucent blocks are blended; every other pixel must be exact.
	checks := []struct {
		name    string
		x, y    int
		wanted  byte
		blended bool
	}{
		{name: "opaque keyed source", x: 4, y: 6, wanted: featureDeviceShadow},
		{name: "opaque keyed transparent skip", x: 11, y: 6, wanted: featureDeviceBackground},
		{name: "translucent shadow ALP", x: 20, y: 6, wanted: featureDeviceShadowALP, blended: true},
		{name: "translucent shadow offset cancellation", x: 18, y: 5, wanted: featureDeviceBackground},
		{name: "translucent shadow transparent skip", x: 27, y: 6, wanted: featureDeviceBackground},
		{name: "static body ALP", x: 36, y: 6, wanted: featureDeviceBodyALP, blended: true},
		{name: "live event opaque", x: 52, y: 6, wanted: featureDeviceShadow},
		{name: "feature option disabled omission", x: 68, y: 6, wanted: featureDeviceBackground},
	}
	for _, c := range checks {
		at := (c.y*featureDeviceWidth + c.x) * 4
		name := fmt.Sprintf("%s at (%d,%d)", c.name, c.x, c.y)
		var err error
		if c.blended {
			err = checkBlendedIndex(name+" [ALP floor]", pixels, at, &p, c.wanted)
		} else {
			err = checkExactIndex(name, pixels, at, &p, c.wanted)
		}
		if err != nil {
			return err
		}
	}
	if path := os.Getenv("NANOLATHE_FEATURE_SHADOW_SHOT"); path != "" {
		if err := writeFeatureShadowDeviceShot(path, pixels); err != nil {
			return err
		}
	}
	// The device loop's call sites live in a file this unit does not own, so the
	// scheduler's record-order fixture is chained here rather than added there.
	return checkTintedOverlapDevicePixels()
}

const (
	overlapWidth  = 8
	overlapHeight = 2
	overlapBack   = byte(200)
)

// overlapALP is the ALP builder's own arithmetic, the value the classic table
// holds for this fixture's grey palette and the value the Enhanced blend forms
// in RGB [03 §4.3.4](§13.2 ALP row). The expected chain below is computed from
// it rather than from a captured image.
func overlapALP(src, dst byte) byte {
	return byte((int(src) + int(dst)) / 2)
}

// checkTintedOverlapDevicePixels is the scheduler's record-order fixture
// (docs/DESIGN_GPU_RENDERER.md §11.2 "The scheduler", C-G3). Two overlapping
// ALP-tinted puffs must blend one after the other, an opaque write over one of
// them must land after that blend, and a third puff over both must composite over
// what they left [03 R-COMP-01 §2][03 R-FX-02 §3]. Every one of those commands
// shares a 32-pixel grid cell, so the scheduler has to open a phase per
// dependency; a batch that merged them would show the background under the second
// puff.
//
// Every pixel here is covered by a blended command, so §13.4's bound is what
// applies — but this fixture locks ORDER, and an ordering mistake moves a pixel
// by tens of units, so it is asserted per pixel rather than as a mean: the chain
// values are 104, 104, 89, 121, 121, 110, 146, 146 and each of the mistakes the
// fixture is here to catch moves one of them well outside the bound.
func checkTintedOverlapDevicePixels() error {
	var p palette.Tables
	for i := 0; i < 256; i++ {
		p.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
	}
	fixtureALP(&p)
	r, err := NewChecked(&p, overlapWidth, overlapHeight)
	if err != nil {
		return fmt.Errorf("compile tinted-overlap fixture shaders: %w", err)
	}
	puff := func(index byte, w int) *formats.GAFFrame {
		pixels := make([]byte, w*overlapHeight)
		transparent := make([]bool, w*overlapHeight)
		for i := range pixels {
			pixels[i] = index
		}
		return &formats.GAFFrame{Width: uint16(w), Height: overlapHeight,
			Pixels: pixels, Transparent: transparent}
	}
	const (
		s1 = byte(31)
		s2 = byte(57)
		s3 = byte(93)
		q  = byte(150)
	)
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: overlapWidth, H: overlapHeight}, Index: overlapBack})
	list.RecordSprite(drawlist.Sprite{Frame: puff(s1, 4), X: 0, Y: 0, Kind: drawlist.BlitTinted})
	list.RecordSprite(drawlist.Sprite{Frame: puff(s2, 4), X: 2, Y: 0, Kind: drawlist.BlitTinted})
	list.RecordSprite(drawlist.Sprite{Frame: puff(q, 2), X: 3, Y: 0, Kind: drawlist.BlitKeyed})
	list.RecordSprite(drawlist.Sprite{Frame: puff(s3, 8), X: 0, Y: 0, Kind: drawlist.BlitTinted})
	list.RecordExpand()
	img := r.Execute(&list, overlapWidth, overlapHeight)
	if img == nil {
		return fmt.Errorf("tinted-overlap fixture renderer returned no image")
	}
	// The classic sink's chain, computed byte by byte in record order.
	want := make([]byte, overlapWidth)
	for x := range want {
		v := overlapBack
		if x < 4 {
			v = overlapALP(s1, v)
		}
		if x >= 2 && x < 6 {
			v = overlapALP(s2, v)
		}
		if x >= 3 && x < 5 {
			v = q
		}
		want[x] = overlapALP(s3, v)
	}
	pixels := make([]byte, overlapWidth*overlapHeight*4)
	img.ReadPixels(pixels)
	var stats compositeStats
	for y := 0; y < overlapHeight; y++ {
		for x := 0; x < overlapWidth; x++ {
			at := (y*overlapWidth + x) * 4
			stats.add(pixels, at, &p, want[x])
			if err := checkBlendedIndex(
				fmt.Sprintf("tinted overlap at (%d,%d) [ALP floor, record-order chain %v]", x, y, want),
				pixels, at, &p, want[x]); err != nil {
				return err
			}
		}
	}
	return stats.check("tinted overlap [ALP floor]")
}

func writeFeatureShadowDeviceShot(path string, pixels []byte) error {
	const scale = 8
	out := image.NewRGBA(image.Rect(0, 0, featureDeviceWidth*scale, featureDeviceHeight*scale))
	for y := 0; y < featureDeviceHeight; y++ {
		for x := 0; x < featureDeviceWidth; x++ {
			i := (y*featureDeviceWidth + x) * 4
			c := color.RGBA{R: pixels[i], G: pixels[i+1], B: pixels[i+2], A: pixels[i+3]}
			for yy := 0; yy < scale; yy++ {
				for xx := 0; xx < scale; xx++ {
					out.SetRGBA(x*scale+xx, y*scale+yy, c)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create feature-shadow device shot: %w", err)
	}
	if err := png.Encode(f, out); err != nil {
		f.Close()
		return fmt.Errorf("encode feature-shadow device shot: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close feature-shadow device shot: %w", err)
	}
	return nil
}
