package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The shelf packer places regions at even origins, wraps rows, opens the
// next page when a page is full and refuses what no page can hold, so a
// native pixel's 2×2 block never straddles regions and an overflowing subject
// takes the fallback rather than an alias (§22).
func TestModelDirectAllocPacksEvenAndOverflows(t *testing.T) {
	var d modelDirectLane
	x, y, page, ok := d.alloc(33, 17)
	if !ok || x != 0 || y != 0 || page != 0 {
		t.Fatalf("first region at (%d,%d) page %d ok=%v, want (0,0) on page 0", x, y, page, ok)
	}
	x, y, _, ok = d.alloc(10, 10)
	if !ok || x != 34 || y != 0 {
		t.Fatalf("second region at (%d,%d) ok=%v, want (34,0): the first rounded up to an even width", x, y, ok)
	}
	if d.pages[0].usedRows != 18 {
		t.Fatalf("used rows %d, want 18 (the taller region rounded up)", d.pages[0].usedRows)
	}
	// A region wider than what remains wraps to the next shelf.
	x, y, _, ok = d.alloc(modelDirectAtlasW-20, 8)
	if !ok || x != 0 || y != 18 {
		t.Fatalf("wrapped region at (%d,%d) ok=%v, want (0,18)", x, y, ok)
	}
	if _, _, _, ok = d.alloc(modelDirectAtlasW+2, 8); ok {
		t.Fatal("a region wider than the atlas was placed")
	}
	// A region past the page's height opens the next page at its origin.
	d.packY = modelDirectAtlasH - 4
	d.packX, d.packRow = 0, 0
	x, y, page, ok = d.alloc(8, 8)
	if !ok || x != 0 || y != 0 || page != 1 {
		t.Fatalf("region past the page at (%d,%d) page %d ok=%v, want (0,0) on page 1", x, y, page, ok)
	}
	if d.pages[0].usedRows != 26 || d.pages[1].usedRows != 8 {
		t.Fatalf("used rows %d/%d, want 26 on page 0 and 8 on page 1", d.pages[0].usedRows, d.pages[1].usedRows)
	}
	// Past the last page the region is refused.
	d.packY = modelDirectAtlasH - 4
	d.packX, d.packRow = 0, 0
	if _, _, _, ok = d.alloc(8, 8); ok {
		t.Fatal("a region past the last page was placed")
	}
}

// directFace builds one flat, keyed quad in subject-local pixels.
func directFace(x0, y0, x1, y1 int32, color uint8, key0, key1 int32) drawlist.ModelFace {
	return drawlist.ModelFace{Color: color, Vertices: []drawlist.ModelVertex{
		{X: x0, Y: y0, Key: key0}, {X: x1, Y: y0, Key: key1}, {X: x1, Y: y1, Key: key1}, {X: x0, Y: y1, Key: key0},
	}}
}

// directSubject builds one eligible packet at a framebuffer anchor.
func directSubject(ax, ay, w, h int32, faces ...drawlist.ModelFace) *drawlist.ModelGeometry {
	return &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Faces: faces,
		Width: w, Height: h, AnchorX: ax, AnchorY: ay, Scale: 1,
	}
}

// checkModelDirectDevicePixels draws eight subjects through the lane and
// reads the composite back (§22):
//
//   - A: two overlapping faces, the higher key drawn FIRST — the key plane
//     gives the overlap to the higher key whatever the order.
//   - B: two overlapping faces of equal key — the later-drawn wins the tie.
//   - C: one face whose key rises across it, with a reveal — below the floor
//     kept, in the band the band index written flat, at the line erased.
//   - D: a body with a shadow reaching past it — beside the body the shadow is
//     the ALP half-blend; under it the body.
//   - E, F: a rising key under the waterline — erased at and below the
//     waterline key, or tinted through the blue table [03 R-REN-03A §8].
//   - G: a Digger's rising key — erased at and below the Digger key.
//   - H: a carrier with a carried child fifty keys above it — the child's
//     reveal reads the child's OWN key (its band, not its "above"), and the
//     carrier's waterline erase reads the SHIFTED key (the child's low face
//     survives a clip its own key would not) [03 R-REN-03A §4].
//   - J: a body whose shadow is its own silhouette, placed twelve pixels
//     right with a clip at key 20 — the low half casts nothing, the high half
//     the half-blend of index 0, and no region or faces were spent on it.
//   - I: an outline ring whose endpoint key sits between the two keys of
//     the body's texels under one pixel — the endpoint is tested once against
//     the pixel's nearest key and draws whole, not as half a pixel; an
//     endpoint under a higher body key is rejected.
//   - Coverage: the far-edge fattening half-covers the pixel past a face's
//     right edge, which resolves at half alpha over the field.
func checkModelDirectDevicePixels() error {
	if err := checkModelDirectDevicePixelsOn(false); err != nil {
		return err
	}
	// The same subjects behind a filler that takes all but a few rows of the
	// first page: every subject then draws and commits from the second page,
	// and the picture is the same.
	return checkModelDirectDevicePixelsOn(true)
}

func checkModelDirectDevicePixelsOn(secondPage bool) error {
	pal := fixturePalette()
	const w, h, field = 80, 122, 7
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	// Packets carry retail's two-pixel margin past their faces
	// [03 R-REN-03A §1], which is where the fattened far edge resolves.
	a := directSubject(8, 8, 26, 18,
		directFace(8, 0, 24, 16, 200, 20, 20),
		directFace(0, 0, 16, 16, 100, 10, 10))
	b := directSubject(44, 8, 26, 18,
		directFace(0, 0, 16, 16, 100, 10, 10),
		directFace(8, 0, 24, 16, 200, 10, 10))
	c := directSubject(8, 30, 34, 18, directFace(0, 0, 32, 16, 100, 5, 40))
	c.Reveal = &drawlist.ModelReveal{Line: 30, Floor: 10, Below: -1, Band: 250, Above: -2}
	d := directSubject(44, 30, 10, 10, directFace(0, 0, 8, 8, 100, 10, 10))
	d.Shadow = directSubject(44, 30, 18, 10, directFace(0, 0, 16, 8, 30, 10, 10))
	e := directSubject(8, 52, 34, 18, directFace(0, 0, 32, 16, 100, 5, 40))
	e.Waterline, e.WaterlineKey = drawlist.ModelWaterlineErase, 20
	// The fixture palette's blue table is the identity except entry 15 → 17.
	f := directSubject(44, 52, 34, 18, directFace(0, 0, 32, 16, 15, 5, 40))
	f.Waterline, f.WaterlineKey = drawlist.ModelWaterlineBlue, 20
	g := directSubject(8, 74, 34, 18, directFace(0, 0, 32, 16, 100, 110, 140))
	g.Digger, g.DiggerKey = true, 125
	hc := directSubject(44, 74, 38, 18, directFace(0, 0, 36, 16, 200, 52, 52))
	hc.Waterline, hc.WaterlineKey = drawlist.ModelWaterlineErase, 50
	child := directSubject(44, 74, 34, 18,
		directFace(0, 0, 16, 16, 100, 20, 20),
		directFace(16, 0, 32, 16, 120, 5, 5))
	child.Reveal = &drawlist.ModelReveal{Line: 30, Floor: 10, Below: -1, Band: 250, Above: -2}
	hc.Children = []drawlist.ModelChild{{Geometry: child, KeyDelta: 50}}
	// I's body key rises 5 → 40 over 64 texels: under pixel 16 the texels
	// hold 22 and 23, under pixel 18 they hold 24 and 25. The ring's key is
	// 22 everywhere.
	i := directSubject(8, 96, 34, 18, directFace(0, 0, 32, 16, 100, 5, 40))
	i.Outline = []drawlist.ModelFace{{Color: 60, Vertices: []drawlist.ModelVertex{
		{X: 16, Y: 0, Key: 22}, {X: 18, Y: 0, Key: 22}, {X: 18, Y: 16, Key: 22}, {X: 16, Y: 16, Key: 22},
	}}}
	j := directSubject(52, 96, 12, 12, directFace(0, 0, 8, 8, 100, 5, 40))
	j.Shadow = &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Silhouette: true, SilhouetteClip: 20,
		Width: j.Width, Height: j.Height, AnchorX: j.AnchorX + 12, AnchorY: j.AnchorY, Scale: 1,
	}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: field, Style: drawlist.FillSolid})
	subjects, wantPages := 10, 1
	if secondPage {
		// A subject 2,044 pixels tall and 2,046 wide, off the canvas, takes a
		// page-wide shelf of 4,092 of the first page's 4,096 rows; packed
		// tallest first it goes on before the rest, which then open the
		// second page.
		filler := directSubject(0, -2100, 2046, 2044, directFace(0, 0, 2044, 2040, 100, 10, 10))
		list.RecordModel(drawlist.Model{Geometry: filler})
		subjects, wantPages = 11, 2
	}
	for _, g := range []*drawlist.ModelGeometry{a, b, c, d, e, f, g, hc, i, j} {
		list.RecordModel(drawlist.Model{Geometry: g})
	}
	list.RecordExpand()
	img := r.Execute(&list, w, h)
	if img == nil {
		return fmt.Errorf("direct lane fixture returned no image")
	}
	if got := r.modelStats.DirectSubjects; got != subjects+1 {
		return fmt.Errorf("direct subjects %d, want %d (the bodies and one carried child)", got, subjects+1)
	}
	if got := r.modelStats.DirectPages; got != wantPages {
		return fmt.Errorf("direct pages %d, want %d", got, wantPages)
	}
	if got := r.modelStats.DirectOverflow; got != 0 {
		return fmt.Errorf("direct overflow %d, want none", got)
	}
	if got := r.modelStats.DirectShadows; got != 1 {
		return fmt.Errorf("direct shadows %d, want 1 (the silhouette takes no region)", got)
	}
	if got := r.modelStats.Silhouettes; got != 1 {
		return fmt.Errorf("silhouette shadows %d, want 1", got)
	}
	pixels := make([]byte, w*h*4)
	img.ReadPixels(pixels)
	exact := func(name string, x, y int, want byte) error {
		return checkExactIndex(fmt.Sprintf("%s at (%d,%d)", name, x, y), pixels, (y*w+x)*4, &pal, want)
	}
	at := func(x, y int) int { return int(pixels[(y*w+x)*4]) }
	// A: left part 100, overlap 200 (higher key, drawn first), right part 200.
	for _, c := range []struct {
		name string
		x, y int
		want byte
	}{
		{"A lower face alone", 10, 12, 100}, {"A overlap goes to the higher key", 18, 12, 200}, {"A higher face alone", 28, 12, 200},
		{"B first face alone", 46, 12, 100}, {"B tie goes to the later face", 54, 12, 200}, {"B later face alone", 64, 12, 200},
		{"C below the floor keeps the face", 10, 38, 100}, {"C the band writes its index flat", 24, 38, 250},
		{"D body over its own shadow", 47, 34, 100},
		{"E above the waterline keeps the face", 32, 60, 100},
		{"F above the waterline keeps the face", 68, 60, 15}, {"F at and below the waterline tints through the blue table", 54, 60, 17},
		{"G above the Digger key keeps the face", 32, 82, 100},
		{"H the child's reveal bands on its own key", 52, 82, 250},
		{"H the carrier's clip on the shifted key keeps the child's low face", 68, 82, 120},
		{"H the carrier beside the child", 78, 82, 200},
		{"I the endpoint draws whole against the pixel's key", 24, 104, 60},
		{"I the body beside the endpoint", 20, 104, 100},
		{"I an endpoint under a higher body key is rejected", 26, 104, 100},
		{"field beside every subject", 2, 2, field},
	} {
		if err := exact(c.name, c.x, c.y, c.want); err != nil {
			return err
		}
	}
	// C at and above the line is erased to the field; E and G at and below
	// their clip keys likewise.
	for _, c := range []struct {
		name string
		x, y int
	}{{"C at the line erases", 38, 38}, {"E at and below the waterline erases", 18, 60}, {"G at and below the Digger key erases", 18, 82}} {
		if err := exact(c.name, c.x, c.y, field); err != nil {
			return err
		}
	}
	// D beside the body: the ALP half-blend of the shadow index over the field.
	if got, want := at(56, 34), (30+field)/2; got < want-3 || got > want+3 {
		return fmt.Errorf("shadow beside the body reads %d, want about %d (half of %d over %d)", got, want, 30, field)
	}
	// J: the silhouette's high half is the half-blend of index 0 over the
	// field; its low half, at and below the clip key, casts nothing.
	if got, want := at(70, 100), field/2; got < want-3 || got > want+3 {
		return fmt.Errorf("silhouette shadow reads %d, want about %d (half of index 0 over %d)", got, want, field)
	}
	if err := exact("J the clipped half of the silhouette casts nothing", 66, 100, field); err != nil {
		return err
	}
	// Coverage: the pixel past A's right edge is half covered by the fattened
	// far edge and resolves at half alpha over the field.
	if got, want := at(32, 12), (200+field)/2; got < want-4 || got > want+4 {
		return fmt.Errorf("half-covered edge pixel reads %d, want about %d", got, want)
	}
	if err := exact("two past the edge is the field", 33, 12, field); err != nil {
		return err
	}
	return nil
}

// TestModelDirectDeviceFixture is opt-in because ordinary tests must not
// require a graphics device (C-G10); the hidden loop in TestMain runs the check.
func TestModelDirectDeviceFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device direct lane fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}
