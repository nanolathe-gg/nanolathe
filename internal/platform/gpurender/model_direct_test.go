package gpurender

import (
	"fmt"
	"image"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
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

// A carried child that casts a shadow composes a second time in a region of
// its own: a shadow is cut from its subject's FINISHED image
// [03 R-REN-03D §1], and the group region's texels at the child's box are the
// carrier's wherever the carrier reaches over it. The solo region must
// therefore alias no texel of the group's, and the shadow commits must read it
// (§22).
func TestModelDirectCarriedShadowChildComposesAlone(t *testing.T) {
	var d modelDirectLane
	d.resetFrame()
	carrier := directSubject(10, 10, 40, 20, directFace(0, 0, 36, 16, 200, 60, 60))
	cargo := directSubject(10, 10, 16, 16, directFace(0, 0, 8, 16, 100, 10, 10))
	cargo.Shadow = &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Silhouette: true, SilhouetteClip: 20,
		Width: 16, Height: 16, AnchorX: 40, AnchorY: 10, Scale: 1,
	}
	plain := directSubject(10, 10, 8, 16, directFace(0, 0, 8, 16, 90, 20, 20))
	carrier.Children = []drawlist.ModelChild{{Geometry: cargo, KeyDelta: 50}, {Geometry: plain}}

	group := d.assignGroupRegions(carrier)
	if !group.ok {
		t.Fatal("the group region was refused")
	}
	solo, carried := d.solo[cargo]
	if !carried || !solo.ok {
		t.Fatalf("the carried child's solo region is %+v, carried=%v; want a placed region", solo, carried)
	}
	if _, spent := d.solo[plain]; spent {
		t.Fatal("a carried child that casts no shadow spent a region of its own")
	}
	// Allocated boxes, margin included: every texel the region walls off.
	box := func(r modelDirectRegion) image.Rectangle {
		return image.Rect(int(r.x)-modelDirectMargin, int(r.y)-modelDirectMargin,
			int(r.x)+2*r.bounds.Dx()+modelDirectMargin, int(r.y)+2*r.bounds.Dy()+modelDirectMargin)
	}
	if solo.page == group.page && box(solo).Overlaps(box(group)) {
		t.Fatalf("the solo region %v aliases the group region %v on page %d", box(solo), box(group), solo.page)
	}
	if solo.bounds != modelWorldBounds(cargo) {
		t.Fatalf("the solo region covers %v, want the child's own box %v", solo.bounds, modelWorldBounds(cargo))
	}
	// The child keeps its entry INSIDE the group region — that is where its
	// faces compose, under the carrier's clip — while its shadow reads the
	// solo one.
	inGroup, ok := d.regions[cargo]
	if !ok || !inGroup.ok || inGroup.page != group.page || !box(inGroup).In(box(group)) {
		t.Fatalf("the child's group entry is %+v, want a sub-rectangle of the group region", inGroup)
	}
	got, usable := d.shadowSource(cargo)
	if !usable || got != solo {
		t.Fatalf("the child's shadow reads %+v (usable=%v), want its solo region %+v", got, usable, solo)
	}
	if got, usable := d.shadowSource(carrier); !usable || got != group {
		t.Fatalf("the carrier's shadow reads %+v (usable=%v), want its own region %+v", got, usable, group)
	}
	// A solo region the atlas cannot hold omits the shadow rather than cutting
	// it from texels that are not the child's.
	d.solo[cargo] = modelDirectRegion{}
	if _, usable := d.shadowSource(cargo); usable {
		t.Fatal("a child whose solo region was refused still offered a shadow source")
	}
}

// The page-overflow fallback draws the whole GROUP in one painter order: a
// carried unit's faces join its carrier's, sorted on the key the group
// composition would give them (the child's delta applied), and a child the lane
// cannot merge is counted as skipped rather than silently dropped
// [03 R-REN-03A §4] (§22).
func TestModelDirectFallbackOrdersTheWholeGroup(t *testing.T) {
	var d modelDirectLane
	carrier := directSubject(10, 10, 40, 20, directFace(0, 0, 36, 16, 200, 30, 30))
	carrier.LiveFaces = []drawlist.ModelFace{directFace(0, 0, 4, 4, 201, 40, 40)}
	// The cargo's own keys are below the carrier's; its delta puts it above.
	cargo := directSubject(10, 10, 16, 16, directFace(0, 0, 8, 16, 100, 10, 10))
	nested := directSubject(10, 10, 8, 8, directFace(0, 0, 8, 8, 90, 20, 20))
	nested.Children = []drawlist.ModelChild{{Geometry: directSubject(10, 10, 4, 4)}}
	carrier.Children = []drawlist.ModelChild{{Geometry: cargo, KeyDelta: 50}, {Geometry: nested}}

	if skipped := d.groupPainterOrder(carrier); skipped != 1 {
		t.Fatalf("skipped %d children, want 1 (the one with children of its own)", skipped)
	}
	want := []modelDirectFace{
		{key: 30 * 256, index: 0, child: -1},  // the carrier's cached face
		{key: 40 * 256, index: ^0, child: -1}, // its live face
		{key: 60 * 256, index: 0, child: 0},   // the cargo, at key 10 + 50
	}
	if len(d.order) != len(want) {
		t.Fatalf("the group's painter order has %d faces, want %d: %+v", len(d.order), len(want), d.order)
	}
	for i, w := range want {
		if d.order[i] != w {
			t.Fatalf("painter order %d is %+v, want %+v", i, d.order[i], w)
		}
	}
	if owner := modelDirectFaceOwner(carrier, d.order[2].child); owner != cargo {
		t.Fatal("the cargo's face does not resolve to the cargo packet")
	}
	ox, oy := modelDirectFaceOrigin(cargo)
	if ox != 10 || oy != 10 {
		t.Fatalf("the cargo's origin is (%v,%v), want its own anchor (10,10)", ox, oy)
	}
}

// benchCargoTexture is the one texture frame every authored cargo packet
// binds: a loaded texture is immutable and pointer-stable across frames, and
// the retained store's body index reads that identity (model_retain.go).
var benchCargoTexture = func() *formats.GAFFrame {
	texture := &formats.GAFFrame{Width: 16, Height: 16, Pixels: make([]byte, 16*16)}
	for i := range texture.Pixels {
		texture.Pixels[i] = byte(32 + i%64)
	}
	return texture
}()

// benchCargoPacket authors one carried unit the way the recorder hands one
// over: a body of flat and textured quads with normals, shades and a material,
// a nanoframe outline and reveal, and the doubled lane the recorder builds for
// the 2× atlas (§17.3, §22).
func benchCargoPacket(faces int) *drawlist.ModelGeometry {
	texture := benchCargoTexture
	build := func(scale int32) []drawlist.ModelFace {
		out := make([]drawlist.ModelFace, 0, faces)
		for i := range faces {
			x, y := int32(i%12)*4*scale, int32(i/12)*3*scale
			key := int32(20 + i%60)
			f := drawlist.ModelFace{
				Color: uint8(32 + i%40), Shaded: true, Normal: [3]float32{0.3, -0.5, 0.81},
				Material: uint8(1 + i%3),
				Vertices: []drawlist.ModelVertex{
					{X: x, Y: y, Key: key, U: 0, V: 0, Shade: 15, Height: float32(key)},
					{X: x + 4*scale, Y: y, Key: key + 2, U: 15, V: 0, Shade: 16, Height: float32(key + 2)},
					{X: x + 4*scale, Y: y + 3*scale, Key: key + 3, U: 15, V: 15, Shade: 17, Height: float32(key + 3)},
					{X: x, Y: y + 3*scale, Key: key + 1, U: 0, V: 15, Shade: 16, Height: float32(key + 1)},
				},
			}
			if i%2 == 0 {
				f.Texture = texture
			}
			out = append(out, f)
		}
		return out
	}
	g := &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Scale: 1,
		Width: 52, Height: 42, AnchorX: 100, AnchorY: 80,
		Faces:  build(1),
		Reveal: &drawlist.ModelReveal{Line: 60, Floor: 30, Below: -1, Band: 250, Above: -2},
		Outline: []drawlist.ModelFace{{Color: 60, Vertices: []drawlist.ModelVertex{
			{X: 2, Y: 2, Key: 40}, {X: 48, Y: 2, Key: 40}, {X: 48, Y: 38, Key: 40}, {X: 2, Y: 38, Key: 40},
		}}},
	}
	g.LiveFaces = g.Faces[:8]
	ss := &drawlist.ModelGeometry{Eligible: true, KeyPlane: true, Scale: 2, Width: 104, Height: 84, Faces: build(2), Reveal: g.Reveal}
	ss.LiveFaces = ss.Faces[:8]
	g.Supersample = ss
	return g
}

// BenchmarkModelDirectAppendPacket measures what a carried child's second
// composition costs: the group append alone, then the group append followed by
// the solo one through the full path (everything a colour needs) and through
// the coverage-only path the shadow commits actually read (§22).
func BenchmarkModelDirectAppendPacket(b *testing.B) {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 320, 240)
	if err != nil {
		b.Fatalf("renderer: %v", err)
	}
	g := benchCargoPacket(160)
	d := &r.modelDirect
	run := func(b *testing.B, solo, coverage bool) {
		b.ReportAllocs()
		for b.Loop() {
			// Execute rewinds the preparation arena every frame; a benchmark
			// that does not measures its unbounded growth instead.
			r.modelPrep.reset()
			d.resetFrame()
			region, ok := d.allocRegion(modelWorldBounds(g))
			if !ok {
				b.Fatal("the region was refused")
			}
			r.appendPacket(g, region, false, 0, nil)
			if !solo {
				continue
			}
			d.soloPass = coverage
			r.appendPacket(g, region, false, 0, nil)
			d.soloPass = false
		}
	}
	b.Run("group", func(b *testing.B) { run(b, false, false) })
	b.Run("group+solo-full", func(b *testing.B) { run(b, true, false) })
	b.Run("group+solo-coverage", func(b *testing.B) { run(b, true, true) })
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
//   - K: a carrier whose cargo casts a silhouette shadow. The carrier's face
//     covers the cargo's whole box, so a silhouette cut from the GROUP region
//     would shadow every carrier texel over that box, and the clip key would
//     compare against the group plane's SHIFTED keys; cut from the cargo's own
//     finished image it covers the cargo alone and clips on the cargo's own
//     keys [03 R-REN-03A §4][03 R-REN-03D §1].
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
	const w, h, field = 80, 144, 7
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
	// K's carrier covers world x 8..44; its cargo's BOX is x 8..24 but its
	// faces reach only x 8..20: key 10 to x 12, key 30 to x 16, and a textured
	// face of the transparent index to x 20. The silhouette sits at x 48, clear
	// of the carrier's own body, so what it shadows is read back with nothing
	// over it.
	kc := directSubject(8, 118, 40, 18, directFace(0, 0, 36, 16, 200, 60, 60))
	// The third face is textured with the composition transparent index, which
	// is a hole in the finished image and so a hole in its shadow: the solo
	// image keeps every face's texel, not a flat stand-in.
	hole := &formats.GAFFrame{Width: 4, Height: 4, Pixels: []byte{
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	}}
	cargo := directSubject(8, 118, 16, 16,
		directFace(0, 0, 4, 16, 100, 10, 10),
		directFace(4, 0, 8, 16, 110, 30, 30),
		drawlist.ModelFace{Texture: hole, Vertices: []drawlist.ModelVertex{
			{X: 8, Y: 0, Key: 30, U: 0, V: 0}, {X: 12, Y: 0, Key: 30, U: 3, V: 0},
			{X: 12, Y: 16, Key: 30, U: 3, V: 3}, {X: 8, Y: 16, Key: 30, U: 0, V: 3},
		}})
	cargo.Shadow = &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Silhouette: true, SilhouetteClip: 20,
		Width: cargo.Width, Height: cargo.Height, AnchorX: 48, AnchorY: cargo.AnchorY, Scale: 1,
	}
	kc.Children = []drawlist.ModelChild{{Geometry: cargo, KeyDelta: 50}}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: field, Style: drawlist.FillSolid})
	bodies, wantPages := 11, 1
	if secondPage {
		// A subject 2,044 pixels tall and 2,046 wide, off the canvas, takes a
		// page-wide shelf of 4,092 of the first page's 4,096 rows; packed
		// tallest first it goes on before the rest, which then open the
		// second page.
		filler := directSubject(0, -2100, 2046, 2044, directFace(0, 0, 2044, 2040, 100, 10, 10))
		list.RecordModel(drawlist.Model{Geometry: filler})
		bodies, wantPages = 12, 2
	}
	for _, g := range []*drawlist.ModelGeometry{a, b, c, d, e, f, g, hc, i, j} {
		list.RecordModel(drawlist.Model{Geometry: g})
	}
	// A carried child's shadow-only command runs before its carrier composes
	// the group [03 R-REN-03A §4].
	list.RecordModel(drawlist.Model{Geometry: cargo, ShadowOnly: true})
	list.RecordModel(drawlist.Model{Geometry: kc})
	list.RecordExpand()
	img := r.Execute(&list, w, h)
	if img == nil {
		return fmt.Errorf("direct lane fixture returned no image")
	}
	if got := r.modelStats.DirectSubjects; got != bodies+2 {
		return fmt.Errorf("direct subjects %d, want %d (the bodies and two carried children)", got, bodies+2)
	}
	if got := r.modelStats.DirectCargoImages; got != 1 {
		return fmt.Errorf("cargo images %d, want 1 (only the carried child that casts a shadow composes alone)", got)
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
	if got := r.modelStats.Silhouettes; got != 2 {
		return fmt.Errorf("silhouette shadows %d, want 2 (J's body and K's cargo)", got)
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
		{"K the cargo's low face ties the carrier on the shifted key and wins as the later lane", 10, 126, 100},
		{"K the cargo's high face over the carrier", 14, 126, 110},
		{"K the carrier beside its cargo", 42, 126, 200},
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
	// K: the cargo's silhouette is cut from the cargo's OWN image. Its high
	// face casts the half-blend; its low face, at and below the clip key its
	// own plane holds, casts nothing (the group plane's shifted key 80 would
	// not clip it); and the part of the cargo's box the cargo does not cover —
	// which the carrier does — casts nothing at all.
	if got, want := at(54, 126), field/2; got < want-3 || got > want+3 {
		return fmt.Errorf("the cargo's silhouette reads %d, want about %d (half of index 0 over %d)", got, want, field)
	}
	for _, c := range []struct {
		name string
		x, y int
	}{
		{"K the cargo's own clip key erases its low face from the silhouette", 50, 126},
		{"K a transparent texel of the cargo's texture is a hole in its silhouette", 58, 126},
		{"K the carrier's texels over the cargo's box cast no cargo shadow", 62, 126},
	} {
		if err := exact(c.name, c.x, c.y, field); err != nil {
			return err
		}
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

// checkModelDirectFallbackCloak fills both atlas pages so a cloaked subject
// takes the fallback, which must draw its ALP half-colour rather than an
// opaque body [03 R-COMP-01 §2] (drawModelDirectFallback).
func checkModelDirectFallbackCloak() error {
	pal := fixturePalette()
	const w, h, field = 80, 48, 40
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	for _, cloak := range []bool{false, true} {
		var list drawlist.List
		list.RecordClear()
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: field, Style: drawlist.FillSolid})
		for range modelDirectMaxPages {
			filler := directSubject(0, -2100, 2046, 2044, directFace(0, 0, 2044, 2040, 100, 10, 10))
			list.RecordModel(drawlist.Model{Geometry: filler})
		}
		g := directSubject(8, 8, 22, 18, directFace(0, 0, 20, 16, 200, 10, 10))
		g.Cloaked = cloak
		list.RecordModel(drawlist.Model{Geometry: g})
		list.RecordExpand()
		img := r.Execute(&list, w, h)
		if img == nil {
			return fmt.Errorf("fallback cloak fixture returned no image")
		}
		if r.modelStats.DirectOverflow != 1 || r.modelStats.DirectPages != modelDirectMaxPages {
			return fmt.Errorf("fallback cloak fixture: overflow %d on %d pages, want the subject overflowed past both pages", r.modelStats.DirectOverflow, r.modelStats.DirectPages)
		}
		pix := make([]byte, w*h*4)
		img.ReadPixels(pix)
		want := 200
		if cloak {
			want = (200 + field) / 2
		}
		if got := int(pix[(12*w+12)*4]); got < want-1 || got > want+1 {
			return fmt.Errorf("fallback cloak %v: pixel %d, want %d", cloak, got, want)
		}
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
