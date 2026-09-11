package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Taking a small slot from an evicted large one must leave the rest available
// without overlapping reservations or losing alignment (§13.12 P3).
func TestModelSlotFreeRegionPreservesRemainder(t *testing.T) {
	a := modelSlotAtlas{freeRects: []image.Rectangle{image.Rect(0, 0, 100, 80)}}
	var used []image.Rectangle
	for _, size := range []image.Point{{40, 20}, {60, 20}, {100, 60}} {
		r, ok := a.takeFreeRegion(size.X, size.Y)
		if !ok || r.Size() != size {
			t.Fatalf("reservation %v: got %v, available=%v", size, r, a.freeRects)
		}
		for _, prior := range used {
			if r.Overlaps(prior) {
				t.Fatalf("reservations overlap: %v and %v", r, prior)
			}
		}
		used = append(used, r)
	}
	if len(a.freeRects) != 0 {
		t.Fatalf("full original area was consumed, but free regions remain: %v", a.freeRects)
	}
	// Return the bottom and top-right occupants first. They form an L, whose
	// bounding box must not free the still-owned top-left reservation.
	a.releaseRegion(used[2])
	a.releaseRegion(used[1])
	if _, ok := a.takeFreeRegion(100, 80); ok {
		t.Fatal("joined an L-shaped free region across a live reservation")
	}
	a.releaseRegion(used[0])
	if r, ok := a.takeFreeRegion(100, 80); !ok || r != image.Rect(0, 0, 100, 80) {
		t.Fatalf("released occupants did not restore their original region: %v, %v", r, ok)
	}
}

// Preparation runs for every subject of every frame, so it must reuse its
// scratch rather than allocate per face, strip or outline endpoint
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"]. This needs no device:
// only the CPU preparation is measured.
func TestModelPreparationReusesFrameScratch(t *testing.T) {
	r := &Renderer{}
	g := fixtureGeometry(0, true,
		fixtureFace(0, 0, 6, 6, 20, 3),
		drawlist.ModelFace{Color: 4, Vertices: []drawlist.ModelVertex{
			{X: 1, Y: 1, Key: 30}, {X: 9, Y: 3, Key: 31}, {X: 5, Y: 9, Key: 32},
		}},
		fixturePinchedRing(2, 2),
		// A textured quad takes the parameter-image path, which must also grow
		// its packed bytes once and reuse them from then on.
		drawlist.ModelFace{Texture: &formats.GAFFrame{Width: 8, Height: 8}, Vertices: []drawlist.ModelVertex{
			{X: 2, Y: 1, U: 0, V: 0, Key: 30}, {X: 10, Y: 3, U: 7, V: 0, Key: 30},
			{X: 7, Y: 9, U: 7, V: 7, Key: 30}, {X: 0, Y: 7, U: 0, V: 7, Key: 30},
		}},
	)
	g.Outline = []drawlist.ModelFace{fixtureFace(0, 0, 6, 6, 20, 5)}
	prepare := func() {
		r.modelPrep.reset()
		r.modelAtlas.quads.reset()
		out := r.modelPrep.prepared.take(len(g.Faces))
		for i := range g.Faces {
			f := &g.Faces[i]
			r.prepareModelFace(&out[i], f, image.Point{}, polygonCrosses(f.Vertices))
		}
		r.prepareModelOutline(g, false)
	}
	prepare()
	if n := testing.AllocsPerRun(20, prepare); n != 0 {
		t.Fatalf("steady-state model preparation allocated %v objects per frame", n)
	}
}

// The preparation arenas hand out subslices of one retained backing array, so a
// slice taken before a growth must still be the caller's own storage afterwards
// [DESIGN_GPU_RENDERER.md §11.5 "CPU"]. A frame's prepared faces are read after
// later subjects have been prepared, so aliasing or copy-on-grow would corrupt
// them.
func TestFrameArenaKeepsEarlierSlicesValidAcrossGrowth(t *testing.T) {
	var a frameArena[int]
	first := a.take(4)
	for i := range first {
		first[i] = 100 + i
	}
	// Force several growths, filling each new slice with a distinct pattern.
	var later [][]int
	for n := 8; n <= 4096; n *= 2 {
		v := a.take(n)
		for i := range v {
			v[i] = n + i
		}
		later = append(later, v)
	}
	for i := range first {
		if first[i] != 100+i {
			t.Fatalf("slice taken before the growth was overwritten at %d: %d", i, first[i])
		}
	}
	for _, v := range later {
		for i := range v {
			if v[i] != len(v)+i {
				t.Fatalf("slice of length %d was overwritten at %d: %d", len(v), i, v[i])
			}
		}
	}
	// take must never hand the same element to two callers in one frame.
	if len(first) > 0 && len(later) > 0 && &first[0] == &later[0][0] {
		t.Fatal("two takes in one frame share an element")
	}
	// A steady-state frame rewinds rather than reallocating.
	frame := func() {
		a.reset()
		a.take(4)
		a.take(4096)
	}
	frame()
	before := cap(a.buf)
	if n := testing.AllocsPerRun(20, frame); n != 0 {
		t.Fatalf("steady-state arena use allocated %v objects per frame", n)
	}
	if cap(a.buf) != before {
		t.Fatalf("arena backing changed size in steady state: %d, want %d", cap(a.buf), before)
	}
}

func TestModelPadCountGrowsCoarsely(t *testing.T) {
	for _, c := range [][2]int{{0, 16}, {1, 16}, {16, 16}, {17, 32}, {4095, 4096}, {4096, 4096}, {4097, 8192}, {9000, 12288}} {
		if got := modelPadCount(c[0]); got != c[1] {
			t.Fatalf("modelPadCount(%d) = %d, want %d", c[0], got, c[1])
		}
	}
	// Padding never asks for more than the reserved slack past a list's end.
	for _, n := range []int{1, 100, 4095, 4096, 20000} {
		if modelPadCount(n)-n > modelVertexPadSlack {
			t.Fatalf("modelPadCount(%d) overshoots the reserved slack", n)
		}
	}
}

// The slot atlas replaces one scratch surface set per subject, so a page's
// stages must cost the same number of device draws whatever the frame holds.
// Runs inside the existing opt-in device loop.
func checkModelSlotFrames() error {
	pal := fixturePalette()
	a, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	b, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	source := fixtureModelList()
	models := source.ModelCommands()
	frame := func(w, h int, dx, dy int32, copies int) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: int32(w), H: int32(h)}, Index: 7, Style: drawlist.FillSolid})
		for c := 0; c < copies; c++ {
			for _, original := range models {
				cmd := original
				cmd.Geometry = cmd.Geometry.Clone()
				moveModelFixture(cmd.Geometry, dx, dy)
				l.RecordModel(cmd)
			}
		}
		l.RecordExpand()
		return l
	}
	var first ModelStats
	var firstPasses int
	for i := 0; i < 5; i++ {
		w, h := 80, 48
		if i > 1 {
			w, h = 128, 80
		}
		dx, dy := int32(0), int32(0)
		if i == 2 {
			dx, dy = 17, 11
		}
		if i == 3 {
			dx, dy = -5, -4
		}
		l := frame(w, h, dx, dy, 1)
		// b replays the same frame twice: no per-frame page state may leak
		// from one Execute into the next.
		x, y := make([]byte, w*h*4), make([]byte, w*h*4)
		a.Execute(&l, w, h).ReadPixels(x)
		b.Execute(&l, w, h)
		b.Execute(&l, w, h).ReadPixels(y)
		if !bytes.Equal(x, y) {
			return fmt.Errorf("slot atlas frame %d differs when the frame is repeated", i)
		}
		s := a.ModelStats()
		if s.SlotOverflows != 0 || s.SlotPages == 0 || s.RasterPixels == 0 {
			return fmt.Errorf("frame %d slot accounting: %+v", i, s)
		}
		// The stage is ordered by destination, so its device destination
		// switches are a property of which stages the frame needs and never of
		// the number of subjects or pages
		// [DESIGN_GPU_RENDERER.md §11.5 "Model slot passes"]. This fixture frame
		// exercises every stage: key, colour, reveal, both resolves, outline and
		// waterline/Digger clipping.
		if p := a.modelStagePasses(); p > modelStageMaxPasses {
			return fmt.Errorf("frame %d model stage passes=%d, want at most %d", i, p, modelStageMaxPasses)
		}
		if i == 0 {
			first, firstPasses = s, a.modelStagePasses()
		} else if s.RasterDraws != first.RasterDraws {
			return fmt.Errorf("frame %d raster draws=%d, want the frame-0 count %d", i, s.RasterDraws, first.RasterDraws)
		} else if p := a.modelStagePasses(); p != firstPasses {
			return fmt.Errorf("frame %d model stage passes=%d, want the frame-0 count %d", i, p, firstPasses)
		}
	}
	// A frame that cannot fit the shared pages sends its remaining subjects
	// through the per-subject fallback route, which must produce the same
	// pixels as the atlas it could not fit into.
	small, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	small.modelPageLimit = 6
	l := frame(80, 48, 0, 0, 1)
	x, y := make([]byte, 80*48*4), make([]byte, 80*48*4)
	a.Execute(&l, 80, 48).ReadPixels(x)
	small.Execute(&l, 80, 48).ReadPixels(y)
	if s := small.ModelStats(); s.SlotOverflows == 0 {
		return fmt.Errorf("a six-row atlas did not exercise the fallback route: %+v", s)
	}
	if !bytes.Equal(x, y) {
		return fmt.Errorf("the per-subject fallback route differs from the slot atlas")
	}
	// Four independent copies of every subject cost the same atlas passes.
	crowd := frame(128, 80, 0, 0, 4)
	a.Execute(&crowd, 128, 80)
	if s := a.ModelStats(); s.RasterDraws != first.RasterDraws || s.GPU != 4*first.GPU {
		return fmt.Errorf("crowded frame raster draws=%d bodies=%d, want %d draws and %d bodies", s.RasterDraws, s.GPU, first.RasterDraws, 4*first.GPU)
	}
	if p := a.modelStagePasses(); p != firstPasses {
		return fmt.Errorf("crowded frame model stage passes=%d, want the single-copy count %d", p, firstPasses)
	}
	// The attached-unit staging is ordered by destination too, so four copies of
	// every group cost the frame the same device destination switches as one: the
	// staging atlas gives each group a disjoint region and merges child k of every
	// group in a single pass (docs/DESIGN_GPU_RENDERER.md §13.3, model_stage.go).
	if s := a.ModelStats(); s.Passes != first.Passes {
		return fmt.Errorf("crowded frame destination switches=%d for %d composed groups, want the single-copy count %d for %d",
			s.Passes, s.ComposedGroups, first.Passes, first.ComposedGroups)
	}
	return nil
}

// modelStageMaxPasses is the contract of the destination-ordered slot stage:
// the clear of each plane, the key work, the colour work, the reveal into the
// scratch, the outline and live keys, the copy back with the outline and live
// colours, final clipping, and the coverage resolve that closes the stage
// [DESIGN_GPU_RENDERER.md §11.5 "Model slot passes", §17].
//
// A frame that clips costs one more than a frame that does not: persistent
// slots cannot exchange the two page planes at the end of the clipping stage —
// that would move every resident raster to the plane the resolved colours are
// on — so the clip copies each clipped rectangle back instead (§13.12).
const modelStageMaxPasses = 9

func moveModelFixture(g *drawlist.ModelGeometry, dx, dy int32) {
	if g == nil {
		return
	}
	g.AnchorX += dx
	g.AnchorY += dy
	moveModelFixture(g.Shadow, dx, dy)
	for _, child := range g.Children {
		moveModelFixture(child.Geometry, dx, dy)
	}
}

// checkModelSlotNeighbourIndependence holds the slot atlas to its own premise:
// slots are disjoint, so a subject's pixels are a function of its packet alone
// [DESIGN_GPU_RENDERER.md §11.2]. Subjects placed off the framebuffer reserve
// and rasterize slots — moving every later subject to a different page origin,
// with a different parity, on a page of a different height — but commit
// nothing, so the finished frame must not change by one byte. A subject whose
// faces reached outside its slot, or a resolve that read its doubled block from
// the page's parity instead of its own slot's, would move with its neighbours
// and show up here.
func checkModelSlotNeighbourIndependence() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	source := fixtureModelList()
	models := source.ModelCommands()
	record := func(l *drawlist.List, dx, dy int32) {
		for _, original := range models {
			cmd := original
			cmd.Geometry = cmd.Geometry.Clone()
			moveModelFixture(cmd.Geometry, dx, dy)
			l.RecordModel(cmd)
		}
	}
	build := func(fillers int) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
		for i := 0; i < fillers; i++ {
			// Far outside the framebuffer: reserved and rasterized, committed
			// nowhere. The odd offsets keep the fillers from coinciding.
			record(&l, int32(4000+37*i), int32(4000+29*i))
		}
		record(&l, 0, 0)
		l.RecordExpand()
		return l
	}
	alone := build(0)
	want := make([]byte, 80*48*4)
	r.Execute(&alone, 80, 48).ReadPixels(want)
	for _, fillers := range []int{1, 2, 3} {
		crowded := build(fillers)
		got := make([]byte, 80*48*4)
		r.Execute(&crowded, 80, 48).ReadPixels(got)
		if !bytes.Equal(want, got) {
			return fmt.Errorf("committed pixels changed with %d uncommitted neighbour sets on the page", fillers)
		}
	}
	return nil
}

// The key of a persistent slot must name every raster input, and must refuse
// every lane the recorder rebuilds each frame (§13.12). This needs no device.
func TestModelSlotKeyRefusesRebuiltLanes(t *testing.T) {
	r := &Renderer{}
	base := func() *drawlist.ModelGeometry {
		g := fixtureGeometry(0, true, fixtureFace(0, 0, 6, 6, 20, 3))
		g.Width, g.Height = 8, 8
		g.Cache = drawlist.ModelCacheKey{Body: 7, Revision: 2}
		return g
	}
	if _, ok := r.modelSlotKeyFor(base()); !ok {
		t.Fatal("a retained cached lane alone in its slot was refused")
	}
	for name, spoil := range map[string]func(*drawlist.ModelGeometry){
		"no recorder identity": func(g *drawlist.ModelGeometry) { g.Cache = drawlist.ModelCacheKey{} },
		"reveal":               func(g *drawlist.ModelGeometry) { g.Reveal = &drawlist.ModelReveal{} },
		"outline":              func(g *drawlist.ModelGeometry) { g.Outline = g.Faces },
		"live lane":            func(g *drawlist.ModelGeometry) { g.LiveFaces = g.Faces },
		"children":             func(g *drawlist.ModelGeometry) { g.Children = []drawlist.ModelChild{{}} },
		"doubled live lane": func(g *drawlist.ModelGeometry) {
			ss := g.Clone()
			ss.LiveFaces = ss.Faces
			g.Supersample = ss
		},
	} {
		g := base()
		spoil(g)
		if _, ok := r.modelSlotKeyFor(g); ok {
			t.Fatalf("%s did not disqualify the slot from being kept", name)
		}
	}
	// Every scalar the raster stages read off the packet separates two keys.
	for name, change := range map[string]func(*drawlist.ModelGeometry){
		"width":        func(g *drawlist.ModelGeometry) { g.Width++ },
		"height":       func(g *drawlist.ModelGeometry) { g.Height++ },
		"origin x":     func(g *drawlist.ModelGeometry) { g.OriginX++ },
		"origin y":     func(g *drawlist.ModelGeometry) { g.OriginY++ },
		"key plane":    func(g *drawlist.ModelGeometry) { g.KeyPlane = !g.KeyPlane },
		"waterline":    func(g *drawlist.ModelGeometry) { g.Waterline = drawlist.ModelWaterlineBlue },
		"waterlineKey": func(g *drawlist.ModelGeometry) { g.WaterlineKey++ },
		"digger":       func(g *drawlist.ModelGeometry) { g.Digger = !g.Digger },
		"diggerKey":    func(g *drawlist.ModelGeometry) { g.DiggerKey++ },
		"revision":     func(g *drawlist.ModelGeometry) { g.Cache.Revision++ },
		"body":         func(g *drawlist.ModelGeometry) { g.Cache.Body++ },
		// The shadow lane of one retained object shares its serial and keeps a
		// revision of its own, so nothing but the lane separates a shadow slot
		// from a body slot whose geometry fields happen to agree
		// (§13.12 "Shadows — contract P4").
		"lane": func(g *drawlist.ModelGeometry) { g.Cache.Lane = drawlist.ModelCacheLaneShadow },
	} {
		want, _ := r.modelSlotKeyFor(base())
		g := base()
		change(g)
		got, ok := r.modelSlotKeyFor(g)
		if !ok || got == want {
			t.Fatalf("%s did not change the slot key", name)
		}
	}
	// The half-pixel offset moves the DOUBLED corners alone (§17), so it must not
	// split a packet with no doubled lane into one slot per offset.
	plain, doubled := base(), base()
	plain.Cache.HalfX, plain.Cache.HalfY = 1, 1
	if got, _ := r.modelSlotKeyFor(plain); got != mustModelSlotKey(t, r, base()) {
		t.Fatal("the half-pixel offset split a native subject's key")
	}
	ss := doubled.Clone()
	ss.Scale, ss.Width, ss.Height = 2, 16, 16
	doubled.Supersample = ss
	shifted := doubled.Clone()
	shifted.Cache.HalfX = 1
	if mustModelSlotKey(t, r, doubled) == mustModelSlotKey(t, r, shifted) {
		t.Fatal("the half-pixel offset did not separate two doubled rasters")
	}
}

func mustModelSlotKey(t *testing.T, r *Renderer, g *drawlist.ModelGeometry) modelSlotKey {
	t.Helper()
	k, ok := r.modelSlotKeyFor(g)
	if !ok {
		t.Fatal("packet was refused a slot key")
	}
	return k
}

// checkModelSlotResidency holds persistent slots to their contract: a subject
// whose raster inputs have not changed keeps its slot and is not drawn again,
// and the pixels are the ones a fresh raster produces (§13.12). Runs inside the
// existing opt-in device loop.
func checkModelSlotResidency() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	build := func(revision uint64) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
		for i := 0; i < 3; i++ {
			g := fixtureGeometry(int32(4+22*i), true, fixtureFace(1, 1, 12, 12, 30+int32(i), uint8(40+8*i)))
			g.Width, g.Height = 16, 16
			rev := uint64(1)
			if i == 0 {
				rev = revision
			}
			g.Cache = drawlist.ModelCacheKey{Body: uint64(i + 1), Revision: rev}
			l.RecordModel(drawlist.Model{Geometry: g})
		}
		l.RecordExpand()
		return l
	}
	pixels := func(l *drawlist.List) []byte {
		out := make([]byte, 80*48*4)
		r.Execute(l, 80, 48).ReadPixels(out)
		return out
	}
	first := build(1)
	want := pixels(&first)
	if s := r.ModelStats(); s.SlotsRasterized != 3 || s.SlotsReused != 0 {
		return fmt.Errorf("cold frame rasterized %d and reused %d slots, want 3 and 0", s.SlotsRasterized, s.SlotsReused)
	}
	again := build(1)
	if got := pixels(&again); !bytes.Equal(want, got) {
		return fmt.Errorf("a frame of reused slots differs from the frame that rasterized them")
	}
	s := r.ModelStats()
	if s.SlotsReused != 3 || s.SlotsRasterized != 0 {
		return fmt.Errorf("repeated frame reused %d and rasterized %d slots, want 3 and 0", s.SlotsReused, s.SlotsRasterized)
	}
	if s.RasterPixels != 0 || s.SlotPages != 0 {
		return fmt.Errorf("repeated frame reported %d raster pixels over %d pages, want none", s.RasterPixels, s.SlotPages)
	}
	// New geometry for one body is a new raster: only that subject is drawn
	// again, and the frame is still the same pixels because the fixture stores
	// the same faces under the new revision.
	bumped := build(2)
	if got := pixels(&bumped); !bytes.Equal(want, got) {
		return fmt.Errorf("a bumped revision changed the finished frame")
	}
	if s := r.ModelStats(); s.SlotsRasterized != 1 || s.SlotsReused != 2 {
		return fmt.Errorf("bumped revision rasterized %d and reused %d slots, want 1 and 2", s.SlotsRasterized, s.SlotsReused)
	}
	// A new subject at the front cannot evict subjects later in this list.
	// This page holds exactly two of the wide reservations. The third must
	// use overflow while both existing subjects keep their pixels (§13.12 P3).
	r.modelPageLimit = 20
	wide := func(extra bool) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		add := func(id uint64, x int32) {
			g := fixtureGeometry(x, true, fixtureFace(1, 1, 12, 12, 30, uint8(40+id)))
			g.Width, g.Height = 1000, 16
			g.Cache = drawlist.ModelCacheKey{Body: id, Revision: 1}
			l.RecordModel(drawlist.Model{Geometry: g})
		}
		if extra {
			add(3, 48)
		}
		add(1, 4)
		add(2, 26)
		l.RecordExpand()
		return l
	}
	cold := wide(false)
	pixels(&cold)
	crowded := wide(true)
	got := pixels(&crowded)
	if s := r.ModelStats(); s.SlotsReused != 2 || s.SlotOverflows != 1 {
		return fmt.Errorf("early miss evicted a later hit: reused %d, overflow %d", s.SlotsReused, s.SlotOverflows)
	}
	// Rebuild the same list cold: residency must not change its visible pixels.
	r.modelAtlas.rebuild = true
	if want := pixels(&crowded); !bytes.Equal(got, want) {
		return fmt.Errorf("protecting later resident subjects changed crowded-frame pixels")
	}
	return nil
}

// checkModelSlotShadowResidency holds the retained shadow lane to the same
// contract as the body (§13.12 "Shadows — contract P4"): a shadow whose
// projection has not changed keeps its slot and is not drawn again, the finished
// frame is the one a fresh raster produced, and a new shadow revision rasterizes
// exactly that shadow. Runs inside the existing opt-in device loop.
func checkModelSlotShadowResidency() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	build := func(shadowRevision uint64) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
		for i := 0; i < 2; i++ {
			g := fixtureGeometry(int32(6+34*i), true, fixtureFace(1, 1, 12, 12, 30+int32(i), uint8(40+8*i)))
			g.Width, g.Height = 16, 16
			g.Cache = drawlist.ModelCacheKey{Body: uint64(i + 1), Revision: 1}
			// The silhouette is offset from its body the way the shear places
			// it, so part of it is punched by the body and part darkens the
			// fill [03 R-REN-03D §3][03 R-REN-03D §5].
			shadow := fixtureGeometry(int32(11+34*i), true, fixtureFace(2, 4, 12, 10, 20, 0))
			shadow.Width, shadow.Height = 20, 20
			shadow.AnchorY = 6
			revision := uint64(1)
			if i == 0 {
				revision = shadowRevision
			}
			shadow.Cache = drawlist.ModelCacheKey{
				Body: uint64(i + 1), Revision: revision, Lane: drawlist.ModelCacheLaneShadow,
			}
			g.Shadow = shadow
			l.RecordModel(drawlist.Model{Geometry: g})
		}
		l.RecordExpand()
		return l
	}
	pixels := func(l *drawlist.List) []byte {
		out := make([]byte, 80*48*4)
		r.Execute(l, 80, 48).ReadPixels(out)
		return out
	}
	first := build(1)
	want := pixels(&first)
	s := r.ModelStats()
	if s.Shadows != 2 || s.ShadowsOmitted != 0 {
		return fmt.Errorf("cold frame committed %d shadows and omitted %d, want 2 and 0", s.Shadows, s.ShadowsOmitted)
	}
	if s.SlotsRasterized != 4 || s.ShadowSlotsRasterized != 2 || s.SlotsReused != 0 {
		return fmt.Errorf("cold frame rasterized %d subjects (%d shadows) and reused %d, want 4, 2 and 0",
			s.SlotsRasterized, s.ShadowSlotsRasterized, s.SlotsReused)
	}
	again := build(1)
	if got := pixels(&again); !bytes.Equal(want, got) {
		return fmt.Errorf("a frame of reused shadow slots differs from the frame that rasterized them")
	}
	s = r.ModelStats()
	if s.SlotsReused != 4 || s.ShadowSlotsReused != 2 || s.SlotsRasterized != 0 {
		return fmt.Errorf("repeated frame reused %d subjects (%d shadows) and rasterized %d, want 4, 2 and 0",
			s.SlotsReused, s.ShadowSlotsReused, s.SlotsRasterized)
	}
	// A new projection for one subject is a new raster of its shadow alone: its
	// body and the other subject keep their slots, and the fixture stores the
	// same faces under the new revision, so the frame is unchanged.
	bumped := build(2)
	if got := pixels(&bumped); !bytes.Equal(want, got) {
		return fmt.Errorf("a bumped shadow revision changed the finished frame")
	}
	s = r.ModelStats()
	if s.SlotsRasterized != 1 || s.ShadowSlotsRasterized != 1 || s.SlotsReused != 3 {
		return fmt.Errorf("bumped shadow rasterized %d subjects (%d shadows) and reused %d, want 1, 1 and 3",
			s.SlotsRasterized, s.ShadowSlotsRasterized, s.SlotsReused)
	}
	return nil
}

// bleedFixture builds one subject for checkModelSlotNeighbourBleed. anchor
// places its commit; width/height are the declared composition box; color is
// the flat index its one face paints. The knobs are the shapes the slot atlas
// packs side by side in a real frame and that
// checkModelSlotNeighbourIndependence does not build: odd box widths, a
// supersampled subject beside a native one, a keyless subject beside a keyed
// one, a packet whose corners reach outside its declared box, and a clipped
// subject.
type bleedFixture struct {
	anchor        int32
	w, h          int32
	color         uint8
	keyed         bool
	doubled       bool
	outsideCorner bool
	clipped       bool
	textured      bool
	shadow        bool
}

// bleedTexture is the gradient the textured cases map, so a shifted slot shows
// up as a mapped texel changing rather than a flat colour staying put.
var bleedTexture = &formats.GAFFrame{Width: 8, Height: 8, Pixels: fixtureGradientTexture()}

func (f bleedFixture) geometry() *drawlist.ModelGeometry {
	face := fixtureFace(1, 1, f.w-2, f.h-2, 40, f.color)
	if f.textured {
		face.Texture = bleedTexture
		for i, uv := range [4][2]int32{{0, 0}, {7, 0}, {7, 7}, {0, 7}} {
			face.Vertices[i].U, face.Vertices[i].V = uv[0], uv[1]
		}
	}
	if f.outsideCorner {
		// A corner past the declared box: the placement widens the reservation
		// but the commit still samples the box alone.
		face.Vertices[1].X += 3
		face.Vertices[2].X += 3
	}
	g := &drawlist.ModelGeometry{
		Eligible: true, Scale: 1, KeyPlane: f.keyed,
		Faces: []drawlist.ModelFace{face},
		Width: f.w, Height: f.h,
		AnchorX: f.anchor, AnchorY: 2,
	}
	if f.clipped && f.keyed {
		g.Waterline, g.WaterlineKey = drawlist.ModelWaterlineErase, 200
	}
	if f.shadow {
		// A silhouette of its own, committed a few pixels down-left, whose punch
		// reads the body slot at page coordinates [03 R-REN-03D §4].
		sf := fixtureFace(1, 1, f.w-2, f.h-2, 40, 9)
		g.Shadow = &drawlist.ModelGeometry{
			Eligible: true, Scale: 1, KeyPlane: true,
			Faces: []drawlist.ModelFace{sf},
			Width: f.w, Height: f.h,
			AnchorX: f.anchor - 3, AnchorY: 5,
		}
	}
	if f.doubled {
		ss := &drawlist.ModelGeometry{
			Eligible: true, Scale: 2, KeyPlane: f.keyed,
			Width: 2 * f.w, Height: 2 * f.h,
		}
		double := face
		double.Vertices = append([]drawlist.ModelVertex(nil), face.Vertices...)
		for i := range double.Vertices {
			double.Vertices[i].X *= 2
			double.Vertices[i].Y *= 2
		}
		ss.Faces = []drawlist.ModelFace{double}
		g.Supersample = ss
	}
	return g
}

// checkModelSlotNeighbourBleed holds the slot atlas to its premise on the
// shapes a real frame packs: a committed subject's pixels are a function of its
// own packet, never of what the packer happened to place beside it
// [DESIGN_GPU_RENDERER.md §11.2, §13.12]. The subject is recorded first, so it
// takes the shelf position ahead of the neighbour, and the neighbour is
// committed off the framebuffer so only its RESERVATION and RASTER are in play.
// Changing the neighbour's colour, shape or scale must not move one byte of the
// finished frame.
func checkModelSlotNeighbourBleed() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	neighbours := []bleedFixture{
		{anchor: 4000, w: 12, h: 12, color: 200, keyed: true},
		{anchor: 4000, w: 13, h: 11, color: 90, keyed: false},
		{anchor: 4000, w: 12, h: 12, color: 250, keyed: true, doubled: true},
		{anchor: 4000, w: 15, h: 13, color: 160, keyed: true, outsideCorner: true},
	}
	subjects := map[string]bleedFixture{
		"native keyed":           {anchor: 6, w: 12, h: 12, color: 60, keyed: true},
		"native odd box":         {anchor: 6, w: 13, h: 11, color: 70, keyed: true},
		"native keyless":         {anchor: 6, w: 12, h: 12, color: 80, keyed: false},
		"doubled keyed":          {anchor: 6, w: 12, h: 12, color: 100, keyed: true, doubled: true},
		"doubled odd box":        {anchor: 6, w: 13, h: 11, color: 110, keyed: true, doubled: true},
		"corner outside the box": {anchor: 6, w: 12, h: 12, color: 120, keyed: true, outsideCorner: true},
		"doubled corner outside": {anchor: 6, w: 12, h: 12, color: 130, keyed: true, doubled: true, outsideCorner: true},
		"clipped keyed":          {anchor: 6, w: 12, h: 12, color: 140, keyed: true, clipped: true},
		"clipped doubled":        {anchor: 6, w: 12, h: 12, color: 150, keyed: true, doubled: true, clipped: true},
		"textured keyed":         {anchor: 6, w: 12, h: 12, color: 60, keyed: true, textured: true},
		"textured odd box":       {anchor: 6, w: 13, h: 11, color: 60, keyed: true, textured: true},
		"textured doubled":       {anchor: 6, w: 12, h: 12, color: 60, keyed: true, textured: true, doubled: true},
		"textured doubled odd":   {anchor: 6, w: 13, h: 11, color: 60, keyed: true, textured: true, doubled: true},
	}
	// A filler recorded BEFORE the subject moves its slot to a different page
	// origin, with a different parity and a different row. The finished frame
	// must not change: the raster and its commit are subject-local, so nothing
	// they compute may depend on where the packer put the slot (§13.12).
	movers := []bleedFixture{
		{anchor: 4000, w: 12, h: 12, color: 200, keyed: true},
		{anchor: 4000, w: 13, h: 11, color: 200, keyed: true},
		{anchor: 4000, w: 31, h: 29, color: 200, keyed: true},
		{anchor: 4000, w: 64, h: 40, color: 200, keyed: true, doubled: true},
	}
	frame := func(subject bleedFixture, before, after *bleedFixture) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
		if before != nil {
			l.RecordModel(drawlist.Model{Geometry: before.geometry()})
		}
		l.RecordModel(drawlist.Model{Geometry: subject.geometry()})
		if after != nil {
			l.RecordModel(drawlist.Model{Geometry: after.geometry()})
		}
		l.RecordExpand()
		return l
	}
	for name, subject := range subjects {
		alone := frame(subject, nil, nil)
		want := make([]byte, 80*48*4)
		r.Execute(&alone, 80, 48).ReadPixels(want)
		compare := func(l *drawlist.List, what string) error {
			got := make([]byte, 80*48*4)
			r.Execute(l, 80, 48).ReadPixels(got)
			if bytes.Equal(want, got) {
				return nil
			}
			n, first := 0, ""
			for p := 0; p < 80*48; p++ {
				if want[p*4] != got[p*4] || want[p*4+1] != got[p*4+1] || want[p*4+2] != got[p*4+2] {
					if n == 0 {
						first = fmt.Sprintf("(%d,%d) %d,%d,%d -> %d,%d,%d", p%80, p/80,
							want[p*4], want[p*4+1], want[p*4+2], got[p*4], got[p*4+1], got[p*4+2])
					}
					n++
				}
			}
			return fmt.Errorf("%q changed by %s: %d pixels, first %s", name, what, n, first)
		}
		for i := range neighbours {
			with := frame(subject, nil, &neighbours[i])
			if err := compare(&with, fmt.Sprintf("neighbour %d", i)); err != nil {
				return err
			}
		}
		for i := range movers {
			moved := frame(subject, &movers[i], nil)
			if err := compare(&moved, fmt.Sprintf("mover %d", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkModelSlotPageSizeIndependence holds the slot page to the property every
// packer change depends on: a committed subject's pixels are a function of its
// own packet, never of how big the page it happens to share is
// [DESIGN_GPU_RENDERER.md §11.2, §13.12].
//
// The fillers are committed far off the framebuffer, so they contribute no
// pixel of their own; all they do is reserve enough rows to grow the page's
// planes past the size at which Ebitengine would place them on its internal
// texture atlas. A page on that atlas is a REGION of a larger texture, and the
// model passes address it by whole page pixels, so its placement reaches the
// arithmetic and the finished frame moves with the page's height. Allocating
// the planes unmanaged is what makes them their own textures at every size.
func checkModelSlotPageSizeIndependence() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	subject := bleedFixture{anchor: 6, w: 13, h: 11, color: 60, keyed: true, textured: true, shadow: true}
	build := func(fillers int) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 80, H: 48}, Index: 7, Style: drawlist.FillSolid})
		l.RecordModel(drawlist.Model{Geometry: subject.geometry()})
		for i := 0; i < fillers; i++ {
			f := bleedFixture{anchor: 4000, w: 500, h: 500, color: uint8(200 + i%16), keyed: true}
			l.RecordModel(drawlist.Model{Geometry: f.geometry()})
		}
		l.RecordExpand()
		return l
	}
	small := build(0)
	want := make([]byte, 80*48*4)
	r.Execute(&small, 80, 48).ReadPixels(want)
	// Four fillers per shelf row at 504 reserved pixels each, so twenty rows of
	// them carry the page past two thousand rows.
	for _, fillers := range []int{4, 12, 20} {
		grown := build(fillers)
		got := make([]byte, 80*48*4)
		r.Execute(&grown, 80, 48).ReadPixels(got)
		if bytes.Equal(want, got) {
			continue
		}
		n := 0
		for p := 0; p < 80*48; p++ {
			if want[p*4] != got[p*4] || want[p*4+1] != got[p*4+1] || want[p*4+2] != got[p*4+2] {
				n++
			}
		}
		return fmt.Errorf("committed pixels changed when %d uncommitted fillers grew the page: %d pixels", fillers, n)
	}
	return nil
}
