package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

var deviceFixtureResult error
var deviceFixtureLoop bool

// deviceFixtureLoopRan reports whether TestMain already ran, and finished, the
// Ebitengine device loop. Ebitengine rejects NewImage after RunGame returns, so
// a fixture that builds a renderer must either run inside that loop or skip.
func deviceFixtureLoopRan() bool { return deviceFixtureLoop }

// skipAfterDeviceLoop skips a fixture that builds a renderer once that loop has
// finished. The ordinary (non-device) run covers these cases, and the device
// run exists for the fixtures inside the loop.
func skipAfterDeviceLoop(t *testing.T) {
	t.Helper()
	if deviceFixtureLoopRan() {
		t.Skip("a renderer cannot allocate images after the device loop; covered by the ordinary test run")
	}
}

// TestMain owns the optional device loop so Ebiten starts on the process main
// goroutine. macOS graphics backends reject RunGame from a testing worker
// goroutine; ordinary tests still do no device work unless explicitly opted in.
func TestMain(m *testing.M) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") == "1" {
		deviceFixtureLoop = true
		game := &modelFixtureGame{}
		ebiten.SetWindowVisible(false)
		ebiten.SetWindowSize(80, 48)
		deviceFixtureResult = ebiten.RunGame(game)
		if deviceFixtureResult == nil {
			deviceFixtureResult = game.err
		}
	}
	os.Exit(m.Run())
}

// TestModelDeviceFixtures is opt-in because ordinary tests must not require a
// graphics device. It runs all ownership cases in one hidden Ebitengine loop,
// so the assertions inspect expanded/index pixels from the real backend.
func TestModelDeviceFixtures(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device model fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}

type modelFixtureGame struct {
	done bool
	err  error
}

func (g *modelFixtureGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}

func (g *modelFixtureGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	defer func() { g.done = true }()
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		g.err = fmt.Errorf("compile fixture shaders: %w", err)
		return
	}
	list := fixtureModelList()
	img := r.Execute(&list, 80, 48)
	if img == nil {
		g.err = fmt.Errorf("fixture renderer returned no image")
		return
	}
	var pixels = make([]byte, 80*48*4)
	img.ReadPixels(pixels)
	// The composite is colour now, so a check names the classic index and the
	// helper expands it through PAL (§13.4). An opaque family must land on it
	// exactly; the ALP-blended shadow commits are inside the §13.2 bound.
	check := func(name string, x, y int, want byte) {
		if err := checkExactIndex(fmt.Sprintf("%s at (%d,%d)", name, x, y),
			pixels, (y*80+x)*4, &pal, want); err != nil && g.err == nil {
			g.err = err
		}
	}
	checkBlended := func(name string, x, y int, want byte) {
		if err := checkBlendedIndex(fmt.Sprintf("%s at (%d,%d) [ALP floor]", name, x, y),
			pixels, (y*80+x)*4, &pal, want); err != nil && g.err == nil {
			g.err = err
		}
	}
	check("equal key later face", 1, 2, 4)
	check("equal key later live face", 9, 2, 9)
	check("live transparent index erases cached color", 11, 2, 7)
	check("keyed live-only body", 41, 2, 27)
	check("transparent texture leaves background", 6, 2, 7)
	check("wrapped gradient wins", 13, 2, 5)
	check("wrapped gradient loses", 16, 2, 2)
	check("keyless painter order", 22, 2, 9)
	check("folded positive span", 31, 3, 6)
	check("folded negative lobe", 27, 1, 7)
	check("keyed sprite transparent skip", 36, 0, 7)
	textured := false
	for y := 0; y < 12; y++ {
		for x := 0; x < 16; x++ {
			left, right := pixels[(y*80+44+x)*4], pixels[(y*80+64+x)*4]
			if left >= 32 && left <= 95 {
				textured = true
			}
			if left != right && g.err == nil {
				g.err = fmt.Errorf("cyclic textured quad at (%d,%d): index %d, rotated index %d", x, y, left, right)
			}
		}
	}
	if !textured && g.err == nil {
		g.err = fmt.Errorf("cyclic textured quad did not draw an authored gradient texel")
	}
	// One blend of the silhouette over the background: floor((200 + 7)/2) = 103.
	// Two blends would give 151, far outside the bound [03 R-REN-03D §4–§5].
	checkBlended("shadow ALP blend", 1, 16, 103)
	checkBlended("overlapping shadow faces blend once", 3, 16, 103)
	check("body punches shadow", 5, 16, 13)
	check("shadow bounds", 10, 16, 7)
	check("reveal keeps lower body", 1, 27, 15)
	check("reveal erases previous lower face", 5, 27, 7)
	check("outline left endpoint", 4, 27, 21)
	check("outline right endpoint beyond body span", 12, 27, 21)
	check("outline does not draw horizontal polygon border", 5, 26, 7)
	check("reveal band color", 15, 27, 18)
	check("waterline blue inclusive threshold", 23, 27, 17)
	check("waterline leaves higher key", 27, 27, 15)
	check("waterline erases inclusive threshold", 33, 27, 7)
	check("waterline erase leaves higher key", 37, 27, 15)
	check("digger erases inclusive threshold", 43, 27, 7)
	check("digger leaves higher key", 47, 27, 15)
	check("keyless subject skips clipping", 53, 27, 15)
	check("staging later child sees wrapped stored key", 1, 37, 25)
	check("staging compares before wrapping", 3, 37, 24)
	check("negative shifted child loses", 5, 37, 23)
	check("equal shifted child wins", 7, 37, 26)
	check("erased carrier retains key ownership", 13, 37, 7)
	checkBlended("child shadow-only blend", 59, 37, 103)
	check("child shadow-only punches own body", 61, 37, 7)
	check("child shadow-only omits body commit", 63, 37, 7)
	check("structure live lane follows ALP resolve", 68, 37, 47)
	check("structure resolve takes top-left key", 69, 37, 7)
	check("structure resolve includes transparent background", 72, 37, 46)
	check("untriangulated upper lobe", 17, 16, 19)
	check("untriangulated pinch leaves prior face", 18, 17, 18)
	check("untriangulated lower lobe", 17, 18, 19)
	stats := r.ModelStats()
	if stats.UntriangulatedFaces != 1 && g.err == nil {
		g.err = fmt.Errorf("untriangulated faces=%d, want 1", stats.UntriangulatedFaces)
	}
	if stats.StructureResolves != 2 && g.err == nil {
		g.err = fmt.Errorf("fixture structure resolves=%d, want 2", stats.StructureResolves)
	}
	if stats.ComposedGroups != 3 && g.err == nil {
		g.err = fmt.Errorf("fixture composed groups = %d, want 3", stats.ComposedGroups)
	}
	if stats.Shadows != 2 && g.err == nil {
		g.err = fmt.Errorf("fixture GPU shadow count = %d, want 2", stats.Shadows)
	}
	if stats.GPU != 26 && g.err == nil {
		g.err = fmt.Errorf("fixture GPU count = %d, want 26", stats.GPU)
	}
	if stats.Skipped != 1 && g.err == nil {
		g.err = fmt.Errorf("fixture omitted count = %d, want 1", stats.Skipped)
	}
	if stats.ShadowsOmitted != 2 && g.err == nil {
		g.err = fmt.Errorf("fixture shadow omission count = %d, want 2", stats.ShadowsOmitted)
	}
	if stats.StagedGroups != 1 && g.err == nil {
		g.err = fmt.Errorf("fixture staged group count = %d, want 1", stats.StagedGroups)
	}
	if stats.WaterlineOrDiggerOmitted != 1 && g.err == nil {
		g.err = fmt.Errorf("fixture waterline omission count = %d, want 1", stats.WaterlineOrDiggerOmitted)
	}
	if g.err == nil {
		g.err = checkConstantShadeRows()
	}
	if g.err == nil {
		g.err = checkTexturedQuadInteriorMatchesStrips()
	}
	if g.err == nil {
		g.err = checkModelSlotFrames()
	}
	if g.err == nil {
		g.err = checkModelSlotNeighbourIndependence()
	}
	if g.err == nil {
		g.err = checkFeatureShadowDevicePixels()
	}
	if g.err == nil {
		g.err = checkMinimapSurfaceDevicePixels()
	}
	if g.err == nil {
		g.err = checkFogDevicePixels()
	}
	if g.err == nil {
		g.err = checkRowScaleDevicePixels()
	}
	if g.err == nil {
		g.err = checkTerrainDevicePixels()
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
}

func (g *modelFixtureGame) Layout(int, int) (int, int) { return 80, 48 }

// fixturePalette is the grey ramp every device fixture composes against:
// PAL[i] = (i,i,i), so a readback pixel names the classic index it stands for,
// and ALP is its own builder's arithmetic [03 §4.3.4], so the classic table and
// the Enhanced blend agree exactly (see composite_test.go). The authored ALP
// entries after the fill are the source-side lookups the structure supersample
// resolve makes, which stay exact table fetches (C-G4).
func fixturePalette() palette.Tables {
	var p palette.Tables
	for i := 0; i < 256; i++ {
		p.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
		p.Logical[i] = byte(i)
		p.Gray[i], p.Blue[i] = byte(i), byte(i)
		for row := 0; row < 32; row++ {
			p.Light[row*256+i] = byte(i)
			p.Shade[row][i] = byte(i)
		}
	}
	fixtureALP(&p)
	p.Alpha[31*256+32] = 41
	p.Alpha[33*256+34] = 42
	p.Alpha[41*256+42] = 43
	p.Alpha[31*256+1] = 45
	p.Alpha[45*256+1] = 46
	p.Blue[15] = 17
	return p
}

// fixtureShadowIndex is the silhouette index both shadow subjects paint. It sits
// far from the fixture background so the difference between blending the
// silhouette once and blending it twice is far outside §13.4's bound: over
// background 7 one blend gives 103 and two would give 151
// [03 R-REN-03D §4–§5].
const fixtureShadowIndex = uint8(200)

func fixtureModelList() drawlist.List {
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{1}, Transparent: []bool{true}}
	// The same transparent-marked index-1 texel is also sent through the keyed
	// sprite path. Its green admission flag must leave the background untouched.
	list.RecordSprite(drawlist.Sprite{Frame: texture, X: 36, Y: 0, Kind: drawlist.BlitKeyed})
	// An authored touching ring retains both lobes and the prior face at its pinch.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true, fixtureFace(16, 15, 4, 4, 10, 18), fixturePinchedRing(16, 15))})
	// Equal key: the later color 4 owns the tie.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		fixtureFace(0, 0, 4, 4, 20, 3), fixtureFace(0, 0, 4, 4, 20, 4))})
	// Live faces run after cached resolve/outline. Equal keys therefore admit
	// the later colour, including index 1 which erases at the composition edge.
	live := fixtureGeometry(0, true, fixtureFace(9, 0, 3, 4, 20, 8))
	live.LiveFaces = []drawlist.ModelFace{fixtureFace(9, 0, 1, 4, 20, 9), fixtureFace(10, 0, 2, 4, 20, 1)}
	list.RecordModel(drawlist.Model{Geometry: live})
	liveOnly := fixtureGeometry(0, true)
	liveOnly.LiveFaces = []drawlist.ModelFace{fixtureFace(39, 0, 4, 4, 20, 27)}
	list.RecordModel(drawlist.Model{Geometry: liveOnly})
	// Texture index 1 owns the key but is the model composition transparent
	// index, so the prior background remains visible.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		fixtureFace(5, 0, 4, 4, 20, 3), drawlist.ModelFace{
			Vertices: []drawlist.ModelVertex{
				{X: 5, Y: 0, Key: 30, U: 0, V: 0}, {X: 9, Y: 0, Key: 30, U: 0, V: 0},
				{X: 9, Y: 4, Key: 30, U: 0, V: 0}, {X: 5, Y: 4, Key: 30, U: 0, V: 0},
			}, Texture: texture,
		})})
	// The interpolated key is 253 at pixel center x=13.5 and 3 at x=16.5.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		fixtureFace(12, 0, 8, 4, 100, 2), drawlist.ModelFace{
			Vertices: []drawlist.ModelVertex{
				{X: 12, Y: 0, Key: 250}, {X: 20, Y: 0, Key: 266},
				{X: 20, Y: 4, Key: 266}, {X: 12, Y: 4, Key: 250},
			}, Color: 5,
		})})
	// Keyless subjects use later painter order even when its key is smaller.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, false,
		fixtureFace(22, 0, 4, 4, 200, 8), fixtureFace(22, 0, 4, 4, 100, 9))})
	// Authored crossed ring: only its positive row strips paint.
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true, drawlist.ModelFace{Vertices: []drawlist.ModelVertex{
		{X: 26, Y: 0, Key: 20}, {X: 34, Y: 4, Key: 20}, {X: 32, Y: 6, Key: 20}, {X: 28, Y: 0, Key: 20},
	}, Color: 6})})
	// Unsupported geometry has an explicit fallback and no model source. It
	// contributes diagnostics without claiming a successful body image.
	list.RecordModel(drawlist.Model{Geometry: &drawlist.ModelGeometry{Fallback: drawlist.ModelFallbackWaterlineOrDigger}, ShadowOmissions: 2, GroupOmission: true})
	// A non-parallelogram has different conventional triangle mappings under a
	// cyclic corner rotation. The two-chain row mapper must instead produce the
	// same device pixels in the two translated regions [03 R-RAST-01 §1].
	gradient := &formats.GAFFrame{Width: 8, Height: 8, Pixels: fixtureGradientTexture()}
	quad := []drawlist.ModelVertex{
		{X: 46, Y: 1, Key: 20, U: 0, V: 0},
		{X: 58, Y: 3, Key: 20, U: 7, V: 0},
		{X: 55, Y: 10, Key: 20, U: 7, V: 7},
		{X: 44, Y: 8, Key: 20, U: 0, V: 7},
	}
	rotated := make([]drawlist.ModelVertex, len(quad))
	for i := range quad {
		rotated[i] = quad[(i+1)%len(quad)]
		rotated[i].X += 20
	}
	list.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true,
		drawlist.ModelFace{Vertices: quad, Texture: gradient},
		drawlist.ModelFace{Vertices: rotated, Texture: gradient},
	)})
	body := fixtureGeometry(0, true, fixtureFace(4, 14, 4, 6, 50, 13))
	body.Shadow = fixtureGeometry(0, true,
		fixtureFace(0, 14, 7, 6, 25, fixtureShadowIndex), fixtureFace(2, 14, 7, 6, 35, fixtureShadowIndex))
	list.RecordModel(drawlist.Model{Geometry: body})
	reveal := fixtureGeometry(0, true, fixtureFace(0, 26, 12, 6, 10, 15), fixtureFace(4, 26, 8, 6, 30, 16), fixtureFace(14, 26, 4, 6, 20, 15))
	reveal.Reveal = &drawlist.ModelReveal{Floor: 20, Line: 30, Below: -1, Band: 18, Above: -2}
	reveal.Outline = []drawlist.ModelFace{fixtureFace(4, 26, 8, 6, 30, 21), fixtureFace(0, 26, 12, 6, 5, 22)}
	list.RecordModel(drawlist.Model{Geometry: reveal})
	blue := fixtureGeometry(0, true, fixtureFace(22, 26, 4, 6, 20, 15), fixtureFace(26, 26, 4, 6, 30, 15))
	blue.Waterline, blue.WaterlineKey = drawlist.ModelWaterlineBlue, 20
	list.RecordModel(drawlist.Model{Geometry: blue})
	erase := fixtureGeometry(0, true, fixtureFace(32, 26, 4, 6, 20, 15), fixtureFace(36, 26, 4, 6, 30, 15))
	erase.Waterline, erase.WaterlineKey = drawlist.ModelWaterlineErase, 20
	list.RecordModel(drawlist.Model{Geometry: erase})
	digger := fixtureGeometry(0, true, fixtureFace(42, 26, 4, 6, 125, 15), fixtureFace(46, 26, 4, 6, 126, 15))
	digger.Digger, digger.DiggerKey = true, 125
	list.RecordModel(drawlist.Model{Geometry: digger})
	keyless := fixtureGeometry(0, false, fixtureFace(52, 26, 4, 6, 10, 15))
	keyless.Waterline, keyless.WaterlineKey, keyless.Digger, keyless.DiggerKey = drawlist.ModelWaterlineBlue, 20, true, 125
	list.RecordModel(drawlist.Model{Geometry: keyless})
	carrier := fixtureGeometry(0, true, fixtureFace(0, 36, 8, 6, 200, 23))
	carrier.Children = []drawlist.ModelChild{
		{Geometry: fixtureGeometry(0, true, fixtureFace(0, 36, 4, 6, 250, 24)), KeyDelta: 10},
		{Geometry: fixtureGeometry(0, true, fixtureFace(0, 36, 2, 6, 5, 25))},
		{Geometry: fixtureGeometry(0, true, fixtureFace(4, 36, 4, 6, 10, 24)), KeyDelta: -20},
		{Geometry: fixtureGeometry(0, true, fixtureFace(2, 36, 2, 6, 100, 1))},
		{Geometry: fixtureGeometry(0, true, fixtureFace(6, 36, 2, 6, 200, 26))},
	}
	list.RecordModel(drawlist.Model{Geometry: carrier})
	erasedCarrier := fixtureGeometry(0, true, fixtureFace(12, 36, 4, 6, 200, 23))
	erasedCarrier.Reveal = &drawlist.ModelReveal{Above: -2, Below: -2, Band: -2}
	erasedCarrier.Children = []drawlist.ModelChild{{Geometry: fixtureGeometry(0, true, fixtureFace(12, 36, 4, 6, 100, 24))}}
	list.RecordModel(drawlist.Model{Geometry: erasedCarrier})
	shadowOnly := fixtureGeometry(0, true, fixtureFace(60, 36, 4, 6, 50, 19))
	shadowOnly.Shadow = fixtureGeometry(0, true, fixtureFace(58, 36, 4, 6, 25, fixtureShadowIndex))
	list.RecordModel(drawlist.Model{Geometry: shadowOnly, ShadowOnly: true})
	resolved := fixtureGeometry(68, true, fixtureFace(0, 0, 2, 1, 50, 31))
	resolved.AnchorY, resolved.Width, resolved.Height = 37, 2, 1
	ss := fixtureGeometry(0, true)
	ss.Scale, ss.Width, ss.Height = 2, 4, 2
	for _, x := range []int32{0, 2} {
		ss.Faces = append(ss.Faces, fixtureFace(x, 0, 1, 1, 70, 31), fixtureFace(x+1, 0, 1, 1, 200, 32), fixtureFace(x, 1, 1, 1, 210, 33), fixtureFace(x+1, 1, 1, 1, 220, 34))
	}
	resolved.Supersample = ss
	resolved.LiveFaces = []drawlist.ModelFace{fixtureFace(0, 0, 1, 1, 220, 47)}
	resolved.Waterline, resolved.WaterlineKey = drawlist.ModelWaterlineErase, 100
	resolved.Children = []drawlist.ModelChild{{Geometry: fixtureGeometry(0, true, fixtureFace(69, 37, 1, 1, 100, 44))}}
	list.RecordModel(drawlist.Model{Geometry: resolved})
	edge := fixtureGeometry(72, true, fixtureFace(0, 0, 1, 1, 50, 31))
	edge.AnchorY, edge.Width, edge.Height = 37, 1, 1
	edge.Supersample = fixtureGeometry(0, true, fixtureFace(0, 0, 1, 1, 50, 31))
	edge.Supersample.Scale, edge.Supersample.Width, edge.Supersample.Height = 2, 2, 2
	list.RecordModel(drawlist.Model{Geometry: edge})
	list.RecordExpand()
	return list
}

func fixtureGradientTexture() []byte {
	pixels := make([]byte, 8*8)
	for v := 0; v < 8; v++ {
		for u := 0; u < 8; u++ {
			pixels[v*8+u] = uint8(32 + v*8 + u)
		}
	}
	return pixels
}

func fixtureGeometry(anchor int32, keyPlane bool, faces ...drawlist.ModelFace) *drawlist.ModelGeometry {
	return &drawlist.ModelGeometry{Eligible: true, Faces: faces, AnchorX: anchor, AnchorY: 0, Scale: 1, KeyPlane: keyPlane}
}

func fixtureFace(x, y, w, h, key int32, color uint8) drawlist.ModelFace {
	return drawlist.ModelFace{Color: color, Vertices: []drawlist.ModelVertex{
		{X: x, Y: y, Key: key}, {X: x + w, Y: y, Key: key}, {X: x + w, Y: y + h, Key: key}, {X: x, Y: y + h, Key: key},
	}}
}

// A flat shade row must stay constant across a slanted primitive; interpolator
// residue at an integer boundary must not select an adjacent SHD row.
func checkConstantShadeRows() error {
	p := fixturePalette()
	for row := range p.Shade {
		p.Shade[row][100] = uint8(100 + row)
	}
	r, err := NewChecked(&p, 80, 48)
	if err != nil {
		return err
	}
	var l drawlist.List
	l.RecordClear()
	l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
	for row := int32(0); row < 32; row++ {
		x, y := (row%8)*10, (row/8)*12
		f := drawlist.ModelFace{Color: 100, Shaded: true, Vertices: []drawlist.ModelVertex{{X: x + 1, Y: y + 1, Shade: uint8(row), Key: 50}, {X: x + 9, Y: y + 3, Shade: uint8(row), Key: 50}, {X: x + 5, Y: y + 10, Shade: uint8(row), Key: 50}}}
		l.RecordModel(drawlist.Model{Geometry: fixtureGeometry(0, true, f)})
	}
	l.RecordExpand()
	out := r.Execute(&l, 80, 48)
	pixels := make([]byte, 80*48*4)
	out.ReadPixels(pixels)
	var seen [32]bool
	for y := 0; y < 48; y++ {
		for x := 0; x < 80; x++ {
			idx := pixels[(y*80+x)*4]
			if idx == 7 {
				continue
			}
			row := y/12*8 + x/10
			seen[row] = true
			if idx != byte(100+row) {
				return fmt.Errorf("constant shade row %d at (%d,%d): index %d, want %d", row, x, y, idx, 100+row)
			}
		}
	}
	for row, ok := range seen {
		if !ok {
			return fmt.Errorf("constant shade row %d painted nothing", row)
		}
	}
	return nil
}
