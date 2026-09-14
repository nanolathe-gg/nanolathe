package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"slices"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// retainedPacket is a plain cached-lane packet the way the recorder rebases
// one (§13.12): faces and a doubled lane, no reveal, outline or live lane,
// and a reusable key.
func retainedPacket(faces int, body uint64) *drawlist.ModelGeometry {
	g := benchCargoPacket(faces)
	g.Reveal, g.Outline, g.LiveFaces = nil, nil, nil
	g.Supersample.Reveal, g.Supersample.LiveFaces = nil, nil
	g.Cache = drawlist.ModelCacheKey{Body: body, Revision: 1}
	return g
}

// turnFaces poses faces the way a turned unit's rebuilt lane differs from
// the last one: every corner moves, the keys, SHD rows and heights change,
// the normal is another direction, and some rings (which and how many
// depends on seed) are wound the other way, so the faces culled under one
// pose are not those culled under another. The corner counts and textures — the topology
// the recorder authors from the model — are untouched. scale is the lane's
// raster scale.
func turnFaces(faces []drawlist.ModelFace, scale int32, seed int) {
	for i := range faces {
		f := &faces[i]
		dx, dy := int32((i+seed)%5)-2, int32((i+2*seed)%3)-1
		for j := range f.Vertices {
			v := &f.Vertices[j]
			v.X += dx * scale
			v.Y += dy * scale
			v.Key += int32((i + seed) % 7)
			v.Shade = uint8(int(v.Shade) + (i+seed)%3 - 1)
			v.Height += 1.5 * float32(seed)
		}
		if (i+seed)%(3+seed) == 0 {
			slices.Reverse(f.Vertices)
		}
		f.Normal = [3]float32{-0.2 * float32(seed), 0.6, 0.77}
	}
}

// turnGeometry is turnFaces over a packet's cached lanes — the native faces,
// the doubled lane's and the shadow's — with every key taking revision rev,
// which is what the recorder stamps on a rebuilt lane (§13.12).
func turnGeometry(g *drawlist.ModelGeometry, rev uint64, seed int) {
	turnFaces(g.Faces, 1, seed)
	if ss := g.Supersample; ss != nil {
		turnFaces(ss.Faces, 2, seed)
	}
	g.Cache.Revision = rev
	if g.Shadow != nil {
		turnGeometry(g.Shadow, rev, seed)
	}
}

// laneOutput is one append's whole output: the batch, the parameter bytes and
// the runs, which is everything the atlas passes read.
type laneOutput struct {
	verts  []ebiten.Vertex
	idx    []uint32
	params []byte
	runs   []modelDirectRun
}

func (d *modelDirectLane) output() laneOutput {
	var out laneOutput
	out.verts = slices.Clone(d.verts)
	out.idx = slices.Clone(d.idx)
	out.params = slices.Clone(d.params.buf[:d.params.count*modelQuadBytes])
	out.runs = slices.Clone(d.runs)
	return out
}

func (a laneOutput) equal(b laneOutput) error {
	if len(a.verts) != len(b.verts) {
		return fmt.Errorf("%d vertices, want %d", len(a.verts), len(b.verts))
	}
	for i := range a.verts {
		if a.verts[i] != b.verts[i] {
			return fmt.Errorf("vertex %d is %+v, want %+v", i, a.verts[i], b.verts[i])
		}
	}
	if !slices.Equal(a.idx, b.idx) {
		return fmt.Errorf("indices differ")
	}
	if !bytes.Equal(a.params, b.params) {
		return fmt.Errorf("parameter bytes differ (%d vs %d)", len(a.params), len(b.params))
	}
	if !slices.Equal(a.runs, b.runs) {
		return fmt.Errorf("runs %+v, want %+v", a.runs, b.runs)
	}
	return nil
}

// appendAt starts a frame, places g after `shift` texels of filler so its
// region lands somewhere else than last frame, and appends it.
func appendAt(r *Renderer, g *drawlist.ModelGeometry, shift int) {
	d := &r.modelDirect
	r.modelPrep.reset()
	d.resetFrame()
	r.modelStats = ModelStats{}
	d.retain.setSwitches(r.metalGlint, r.materialsEnabled)
	if shift > 0 {
		if _, ok := d.allocRegion(image.Rect(0, 0, shift, 3)); !ok {
			panic("filler refused")
		}
	}
	region, ok := d.allocRegion(modelWorldBounds(g))
	if !ok {
		panic("region refused")
	}
	r.appendPacket(g, region, false, 0, nil)
	if g.Shadow != nil {
		r.placeModelDirectShadow(g.Shadow)
	}
}

// A reusable key's first sighting primes, its second captures, and every
// later one replays: the replayed batch, parameter bytes and runs are
// byte-for-byte the cold append's at the same placement, wherever the
// region lands and whether or not the subject's lights, verdicts or cloak
// changed since the capture (model_retain.go).
func TestModelRetainReplayIsTheColdAppend(t *testing.T) {
	skipAfterDeviceLoop(t)
	pal := fixturePalette()
	r, err := NewChecked(&pal, 320, 240)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	r.metalGlint, r.materialsEnabled = true, true
	g := retainedPacket(60, 7)
	g.Shadow = retainedPacket(20, 7)
	g.Shadow.Cache.Lane = drawlist.ModelCacheLaneShadow
	g.Shadow.Supersample = nil
	d := &r.modelDirect
	appendAt(r, g, 0)
	if r.modelStats.DirectRetained != 0 || r.modelStats.DirectCaptured != 0 {
		t.Fatalf("first sighting retained %d captured %d, want a stub only", r.modelStats.DirectRetained, r.modelStats.DirectCaptured)
	}
	appendAt(r, g, 0)
	if r.modelStats.DirectCaptured != 2 || r.modelStats.DirectRetained != 0 {
		t.Fatalf("second sighting captured %d retained %d, want the body and the shadow captured", r.modelStats.DirectCaptured, r.modelStats.DirectRetained)
	}
	captured := d.output()
	// A third frame, the region shifted, the waterline and the cloak set: the
	// replay must be what a cold append at this placement produces.
	g.Waterline, g.WaterlineKey, g.Cloaked = drawlist.ModelWaterlineBlue, 40, true
	appendAt(r, g, 300)
	if r.modelStats.DirectRetained != 2 || r.modelStats.DirectCaptured != 0 {
		t.Fatalf("third sighting retained %d captured %d, want both lanes replayed", r.modelStats.DirectRetained, r.modelStats.DirectCaptured)
	}
	replayed := d.output()
	stats := r.modelStats
	if err := captured.equal(replayed); err == nil {
		t.Fatal("the shifted replay equals the capture; the placement did not move")
	}
	d.retain.clear()
	appendAt(r, g, 300)
	if r.modelStats.DirectRetained != 0 {
		t.Fatal("a cleared store replayed")
	}
	if err := replayed.equal(d.output()); err != nil {
		t.Fatalf("replay differs from the cold append: %v", err)
	}
	cold := r.modelStats
	if got, want := withoutRetainAccounting(stats), withoutRetainAccounting(cold); got != want {
		t.Fatalf("replay accounting %+v, want the cold append's %+v", got, want)
	}
	// A finish switch change drops the store; the next sighting primes anew.
	appendAt(r, g, 300)
	appendAt(r, g, 300)
	if r.modelStats.DirectRetained != 2 {
		t.Fatal("the store did not replay after re-priming")
	}
	r.metalGlint = false
	appendAt(r, g, 300)
	if r.modelStats.DirectRetained != 0 || r.modelStats.DirectCaptured != 0 {
		t.Fatal("a finish switch change kept the entries captured under the other finish")
	}
}

// withoutRetainAccounting is s with the store's own counters cleared, which
// is what a replay and a cold append of the same packet must agree on.
func withoutRetainAccounting(s ModelStats) ModelStats {
	s.DirectRetained, s.DirectWarm, s.DirectCaptured = 0, 0, 0
	s.DirectRetainedLive, s.DirectRetainedOutline = 0, 0
	s.DirectColdNoKey, s.DirectColdNoKeyOutline, s.DirectColdNoKeyLive = 0, 0, 0
	s.DirectColdGroup, s.DirectColdReflect, s.DirectColdPrimed = 0, 0, 0
	s.DirectColdShape, s.DirectColdParams, s.DirectRetainEvicted = 0, 0, 0
	return s
}

// A keyed packet's cached lane is replayed even when the packet carries a
// live lane and an outline this frame: both are per-frame and are appended
// cold after the cached lane in retail's order, so the batch is the cold
// append's whatever the live faces and outline were when the lane was
// captured. The doubled live faces here reach past the raster's box on the
// negative side, so the lane's even-rounded frame origin depends on them
// (slotFor) and a replay that kept the captured box would land elsewhere.
func TestModelRetainReplaysUnderPerFrameLanes(t *testing.T) {
	skipAfterDeviceLoop(t)
	pal := fixturePalette()
	r, err := NewChecked(&pal, 320, 240)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	r.metalGlint, r.materialsEnabled = true, true
	d := &r.modelDirect
	g := retainedPacket(40, 21)
	perFrame := func(shift int32) {
		g.LiveFaces = []drawlist.ModelFace{directFace(4+shift, 4, 20+shift, 16, 70, 30, 44)}
		g.Supersample.LiveFaces = []drawlist.ModelFace{directFace(-3-2*shift, 8, 40+2*shift, 32, 70, 30, 44)}
		g.Outline = []drawlist.ModelFace{{Color: 60, Vertices: []drawlist.ModelVertex{
			{X: 2 + shift, Y: 2, Key: 40}, {X: 48, Y: 2, Key: 40}, {X: 48, Y: 38 - shift, Key: 40},
		}}}
	}
	perFrame(0)
	appendAt(r, g, 0)
	appendAt(r, g, 0)
	if r.modelStats.DirectCaptured != 1 {
		t.Fatalf("second sighting captured %d, want the cached lane captured under a live lane and an outline", r.modelStats.DirectCaptured)
	}
	// A later frame with other live faces and another outline, elsewhere on
	// the atlas.
	perFrame(3)
	appendAt(r, g, 300)
	stats := r.modelStats
	if stats.DirectRetained != 1 || stats.DirectRetainedLive != 1 || stats.DirectRetainedOutline != 1 {
		t.Fatalf("third sighting retained %d (live %d, outline %d), want the cached lane replayed under both per-frame lanes", stats.DirectRetained, stats.DirectRetainedLive, stats.DirectRetainedOutline)
	}
	replayed := d.output()
	d.retain.clear()
	appendAt(r, g, 300)
	if r.modelStats.DirectRetained != 0 {
		t.Fatal("a cleared store replayed")
	}
	if err := replayed.equal(d.output()); err != nil {
		t.Fatalf("replay under per-frame lanes differs from the cold append: %v", err)
	}
	if got, want := withoutRetainAccounting(stats), withoutRetainAccounting(r.modelStats); got != want {
		t.Fatalf("replay accounting %+v, want the cold append's %+v", got, want)
	}
}

// A key miss whose body the index holds — the same object under a new
// revision, a turning unit — is appended warm: from this frame's corners,
// keys, rows, heights and normals, with the texture products of the last
// cold append. The warm append's batch, parameter bytes, runs and accounting
// are byte-for-byte a cold append's of the same packet, culling included; a
// returning key is captured through it; and a body whose topology changed —
// a face fewer, another texture, the texture page moved — is cold again
// (model_retain.go).
func TestModelRetainWarmAppendIsTheColdAppend(t *testing.T) {
	skipAfterDeviceLoop(t)
	pal := fixturePalette()
	r, err := NewChecked(&pal, 320, 240)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	r.metalGlint, r.materialsEnabled = true, true
	d := &r.modelDirect
	base := retainedPacket(60, 7)
	base.Shadow = retainedPacket(20, 7)
	base.Shadow.Cache.Lane = drawlist.ModelCacheLaneShadow
	base.Shadow.Supersample = nil
	pose := func(rev uint64, seed int) *drawlist.ModelGeometry {
		g := base.Clone()
		turnGeometry(g, rev, seed)
		return g
	}
	// The first textured face of the process opens the texture page in the
	// middle of its lane; a body is routed on the page its lane started with,
	// so that one lane's body serves nothing and is recorded again cold.
	appendAt(r, base, 0)
	d.retain.clear()
	// The first sighting is cold and records both lanes' bodies.
	appendAt(r, pose(1, 1), 0)
	first := r.modelStats
	if first.DirectWarm != 0 || first.DirectColdPrimed != 2 {
		t.Fatalf("first sighting warm %d, primed %d; want a cold stub for each lane", first.DirectWarm, first.DirectColdPrimed)
	}
	// A turned pose under new revisions, elsewhere on the atlas: both lanes
	// warm, neither cold nor replayed.
	turned := pose(2, 2)
	appendAt(r, turned, 300)
	stats := r.modelStats
	if stats.DirectWarm != 2 || stats.DirectColdPrimed != 0 || stats.DirectRetained != 0 || stats.DirectCaptured != 0 {
		t.Fatalf("turned pose warm %d, primed %d, retained %d, captured %d; want both lanes warm", stats.DirectWarm, stats.DirectColdPrimed, stats.DirectRetained, stats.DirectCaptured)
	}
	if stats.DirectCulled == first.DirectCulled {
		t.Fatalf("the turned pose culls %d faces, as the recorded one did; the poses must differ in winding", stats.DirectCulled)
	}
	warm := d.output()
	d.retain.clear()
	appendAt(r, turned, 300)
	if r.modelStats.DirectWarm != 0 {
		t.Fatal("a cleared store appended warm")
	}
	if err := warm.equal(d.output()); err != nil {
		t.Fatalf("warm append differs from the cold append: %v", err)
	}
	if got, want := withoutRetainAccounting(stats), withoutRetainAccounting(r.modelStats); got != want {
		t.Fatalf("warm accounting %+v, want the cold append's %+v", got, want)
	}
	// The cold append re-recorded the bodies and primed the keys: the next
	// sighting is captured through the warm append, the one after replays.
	appendAt(r, turned, 300)
	if got := r.modelStats; got.DirectWarm != 2 || got.DirectCaptured != 2 {
		t.Fatalf("returning key warm %d, captured %d; want both lanes captured through the warm append", got.DirectWarm, got.DirectCaptured)
	}
	appendAt(r, turned, 300)
	if got := r.modelStats; got.DirectRetained != 2 || got.DirectWarm != 0 {
		t.Fatalf("third sighting retained %d, warm %d; want both lanes replayed", got.DirectRetained, got.DirectWarm)
	}
	// A face fewer under the same body: the body lane is cold (and recorded
	// anew), the shadow lane still warm.
	fewer := pose(3, 3)
	fewer.Faces, fewer.Supersample.Faces = fewer.Faces[:59], fewer.Supersample.Faces[:59]
	appendAt(r, fewer, 0)
	if got := r.modelStats; got.DirectWarm != 1 || got.DirectColdPrimed != 1 || got.DirectFaces+got.DirectCulled != 59+20 {
		t.Fatalf("a face fewer: warm %d, primed %d, faces %d + culled %d; want the body cold and the shadow warm over 79 faces", got.DirectWarm, got.DirectColdPrimed, got.DirectFaces, got.DirectCulled)
	}
	next := pose(4, 4)
	next.Faces, next.Supersample.Faces = next.Faces[:59], next.Supersample.Faces[:59]
	appendAt(r, next, 0)
	if got := r.modelStats; got.DirectWarm != 2 {
		t.Fatalf("the re-recorded body did not serve the next revision of its topology (warm %d)", got.DirectWarm)
	}
	// Another texture on one face — an animated frame advanced, a damage
	// variant — is a topology the body does not describe.
	other := pose(5, 5)
	frame := &formats.GAFFrame{Width: 8, Height: 8, Pixels: make([]byte, 64)}
	other.Faces[2].Texture, other.Supersample.Faces[2].Texture = frame, frame
	appendAt(r, other, 0)
	if got := r.modelStats; got.DirectWarm != 1 || got.DirectColdPrimed != 1 {
		t.Fatalf("another texture: warm %d, primed %d; want the body cold and the shadow warm", got.DirectWarm, got.DirectColdPrimed)
	}
	// The texture page the run routing was decided against is gone: cold.
	r.textureAtlas.page = nil
	appendAt(r, pose(6, 6), 0)
	if got := r.modelStats; got.DirectWarm != 0 {
		t.Fatalf("a moved texture page still appended warm (%d)", got.DirectWarm)
	}
}

// A packet with a group delta is never retained, and a key whose packet
// changed shape is captured again rather than replayed.
func TestModelRetainSkipsPerFramePackets(t *testing.T) {
	skipAfterDeviceLoop(t)
	pal := fixturePalette()
	r, err := NewChecked(&pal, 320, 240)
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	d := &r.modelDirect
	child := retainedPacket(20, 5)
	for range 3 {
		r.modelPrep.reset()
		d.resetFrame()
		r.modelStats = ModelStats{}
		region, _ := d.allocRegion(modelWorldBounds(child))
		r.appendPacket(child, region, false, 12, nil)
	}
	if r.modelStats.DirectRetained != 0 || d.retain.count != 0 || r.modelStats.DirectColdGroup != 1 {
		t.Fatal("a group child was retained")
	}
	g := retainedPacket(20, 6)
	appendAt(r, g, 0)
	appendAt(r, g, 0)
	if r.modelStats.DirectCaptured != 1 {
		t.Fatal("the second sighting did not capture")
	}
	g.Faces = g.Faces[:10]
	g.Supersample.Faces = g.Supersample.Faces[:10]
	appendAt(r, g, 0)
	if r.modelStats.DirectRetained != 0 || r.modelStats.DirectCaptured != 1 || r.modelStats.DirectColdShape != 1 {
		t.Fatalf("a changed shape under an equal key replayed (retained %d, captured %d, shape %d)", r.modelStats.DirectRetained, r.modelStats.DirectCaptured, r.modelStats.DirectColdShape)
	}
	if r.modelStats.DirectFaces != 10 {
		t.Fatalf("the recapture appended %d faces, want 10", r.modelStats.DirectFaces)
	}
}

// The store is bounded: past the cap the least recently used key goes, and
// every entry's storage is recycled rather than reallocated.
func TestModelRetainStoreEvictsLeastRecentlyUsed(t *testing.T) {
	var s modelRetainStore
	key := func(i int) drawlist.ModelCacheKey { return drawlist.ModelCacheKey{Body: uint64(i + 1), Revision: 1} }
	for i := range modelRetainCap {
		s.insert(key(i))
	}
	if s.count != modelRetainCap || s.lookup(key(0)) == nil {
		t.Fatalf("store holds %d, want the cap with the first key present", s.count)
	}
	// Touching key 0 makes key 1 the oldest; its storage becomes the new
	// entry's.
	oldest := s.entries[key(1)]
	newest, evicted := s.insert(key(modelRetainCap))
	if !evicted {
		t.Fatal("an insert past the cap did not report its eviction")
	}
	if s.count != modelRetainCap {
		t.Fatalf("store holds %d past the cap", s.count)
	}
	if s.lookup(key(1)) != nil {
		t.Fatal("the least recently used key survived the insert")
	}
	if s.lookup(key(0)) == nil || s.lookup(key(modelRetainCap)) != newest {
		t.Fatal("a recently used key was evicted")
	}
	if newest != oldest {
		t.Fatal("the evicted entry's storage was not recycled into the new entry")
	}
	s.clear()
	if s.count != 0 || s.head != nil || s.tail != nil || len(s.entries) != 0 {
		t.Fatal("clear left entries behind")
	}
}

// checkModelRetainDevicePixels draws one list of retained-keyed subjects three
// times, the third with the atlas placement shifted by a filler, and reads
// the composite back: the replayed frame must be pixel-identical to a cold
// frame of the same list, and the store must have carried the subjects. A
// fourth frame turns every keyed subject — new revisions, new poses — and
// must be appended warm, pixel-identical to its own cold frame
// (model_retain.go).
func checkModelRetainDevicePixels() error {
	pal := fixturePalette()
	const w, h = 160, 120
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	build := func(shift, turned bool) *drawlist.List {
		var list drawlist.List
		list.RecordClear()
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: 7, Style: drawlist.FillSolid})
		if shift {
			// Off the canvas, packed first: every later region moves.
			filler := directSubject(0, -400, 300, 120, directFace(0, 0, 298, 118, 100, 10, 10))
			list.RecordModel(drawlist.Model{Geometry: filler})
		}
		a := retainedPacket(48, 11)
		a.AnchorX, a.AnchorY = 10, 10
		a.Shadow = retainedPacket(16, 11)
		a.Shadow.Supersample = nil
		a.Shadow.Cache.Lane = drawlist.ModelCacheLaneShadow
		a.Shadow.AnchorX, a.Shadow.AnchorY = 20, 20
		b := directSubject(90, 20, 40, 30,
			directFace(0, 0, 32, 24, 200, 20, 20),
			directFace(8, 8, 36, 28, 100, 10, 10))
		b.Faces[0].Shaded = true
		for i := range b.Faces[0].Vertices {
			b.Faces[0].Vertices[i].Shade = uint8(10 + 2*i)
		}
		b.Cache = drawlist.ModelCacheKey{Body: 12, Revision: 3}
		b.Waterline, b.WaterlineKey = drawlist.ModelWaterlineBlue, 15
		c := directSubject(30, 70, 40, 30, directFace(0, 0, 32, 24, 15, 5, 40))
		c.Cache = drawlist.ModelCacheKey{Body: 13, Revision: 1, HalfX: 1}
		c.Cloaked = true
		// A keyed nanoframe with a live lane: its cached lane replays while
		// the reveal, the outline and the live faces are this frame's, and
		// the shifted frame changes all three. The doubled live face reaches
		// past the raster's box on the negative side, so the lane's frame
		// origin depends on it (slotFor).
		e := retainedPacket(36, 14)
		e.AnchorX, e.AnchorY = 100, 60
		e.Reveal = &drawlist.ModelReveal{Line: 60, Floor: 30, Below: -1, Band: 250, Above: -2}
		e.Supersample.Reveal = e.Reveal
		liveShift := int32(0)
		if shift {
			liveShift = 3
			e.Reveal.Band, e.Reveal.Line = 251, 62
		}
		e.LiveFaces = []drawlist.ModelFace{directFace(4+liveShift, 4, 20+liveShift, 16, 70, 30, 44)}
		e.Supersample.LiveFaces = []drawlist.ModelFace{directFace(-3-2*liveShift, 8, 40+2*liveShift, 32, 70, 30, 44)}
		e.Outline = []drawlist.ModelFace{{Color: 60, Vertices: []drawlist.ModelVertex{
			{X: 2 + liveShift, Y: 2, Key: 40}, {X: 48, Y: 2, Key: 40}, {X: 48, Y: 38 - liveShift, Key: 40},
		}}}
		for i, g := range []*drawlist.ModelGeometry{a, b, c, e} {
			if turned {
				turnGeometry(g, 9, i+1)
			}
			list.RecordModel(drawlist.Model{Geometry: g})
		}
		list.RecordExpand()
		return &list
	}
	read := func(list *drawlist.List) ([]byte, error) {
		img := r.Execute(list, w, h)
		if img == nil {
			return nil, fmt.Errorf("retain fixture returned no image")
		}
		pixels := make([]byte, w*h*4)
		img.ReadPixels(pixels)
		return pixels, nil
	}
	if _, err := read(build(false, false)); err != nil {
		return err
	}
	if got := r.modelStats; got.DirectRetained != 0 || got.DirectWarm != 0 {
		return fmt.Errorf("first frame replayed %d packets, warm %d", got.DirectRetained, got.DirectWarm)
	}
	if _, err := read(build(false, false)); err != nil {
		return err
	}
	if got := r.modelStats.DirectCaptured; got != 5 {
		return fmt.Errorf("second frame captured %d packets, want 5 (four bodies and a shadow)", got)
	}
	replayed, err := read(build(true, false))
	if err != nil {
		return err
	}
	if got := r.modelStats; got.DirectRetained != 5 || got.DirectRetainedLive != 1 || got.DirectRetainedOutline != 1 {
		return fmt.Errorf("shifted frame replayed %d packets (live %d, outline %d), want 5 with the nanoframe under both per-frame lanes", got.DirectRetained, got.DirectRetainedLive, got.DirectRetainedOutline)
	}
	r.modelDirect.retain.clear()
	cold, err := read(build(true, false))
	if err != nil {
		return err
	}
	if got := r.modelStats.DirectRetained; got != 0 {
		return fmt.Errorf("the cleared store replayed %d packets", got)
	}
	if diff := differingPixels(replayed, cold); diff != 0 {
		return fmt.Errorf("replayed frame differs from the cold frame on %d pixels", diff)
	}
	if err := checkExactIndex("retain fixture field", cold, (2*w+2)*4, &pal, 7); err != nil {
		return err
	}
	// The cold frame recorded every keyed body; the turned frame's keys are
	// all new, and every one of its lanes goes warm.
	warm, err := read(build(true, true))
	if err != nil {
		return err
	}
	if got := r.modelStats; got.DirectWarm != 5 || got.DirectRetained != 0 {
		return fmt.Errorf("turned frame appended %d packets warm (replayed %d), want all 5 warm", got.DirectWarm, got.DirectRetained)
	}
	r.modelDirect.retain.clear()
	coldTurned, err := read(build(true, true))
	if err != nil {
		return err
	}
	if got := r.modelStats.DirectWarm; got != 0 {
		return fmt.Errorf("the cleared store appended %d packets warm", got)
	}
	if diff := differingPixels(warm, coldTurned); diff != 0 {
		return fmt.Errorf("warm frame differs from its cold frame on %d pixels", diff)
	}
	if bytes.Equal(coldTurned, cold) {
		return fmt.Errorf("the turned frame is the untouched one; the poses did not move")
	}
	return nil
}

// differingPixels counts the RGBA pixels on which two readbacks differ.
func differingPixels(a, b []byte) int {
	diff := 0
	for i := 0; i+4 <= len(a) && i+4 <= len(b); i += 4 {
		if !bytes.Equal(a[i:i+4], b[i:i+4]) {
			diff++
		}
	}
	return diff
}

// BenchmarkModelRetainAppendPacket measures one plain cached-lane packet's
// append cold — every face packed — against warm (a new revision every
// frame, as a turning unit's is: this frame's corners with the body's
// texture products, and a stub inserted for the key that never returns) and
// against replayed from the store.
func BenchmarkModelRetainAppendPacket(b *testing.B) {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 320, 240)
	if err != nil {
		b.Fatalf("renderer: %v", err)
	}
	r.metalGlint, r.materialsEnabled = true, true
	g := retainedPacket(160, 9)
	run := func(b *testing.B, retained, warm bool) {
		b.ReportAllocs()
		d := &r.modelDirect
		d.retain.clear()
		if retained {
			appendAt(r, g, 0)
			appendAt(r, g, 0)
		}
		if warm {
			appendAt(r, g, 0)
		}
		rev := g.Cache.Revision
		for b.Loop() {
			r.modelPrep.reset()
			d.resetFrame()
			if !retained && !warm {
				d.retain.clear()
			}
			if warm {
				rev++
				g.Cache.Revision = rev
			}
			region, ok := d.allocRegion(modelWorldBounds(g))
			if !ok {
				b.Fatal("the region was refused")
			}
			r.appendPacket(g, region, false, 0, nil)
		}
		if retained && r.modelStats.DirectRetained == 0 {
			b.Fatal("the retained loop did not replay")
		}
		if warm && r.modelStats.DirectWarm == 0 {
			b.Fatal("the warm loop did not append warm")
		}
	}
	b.Run("cold", func(b *testing.B) { run(b, false, false) })
	b.Run("warm", func(b *testing.B) { run(b, false, true) })
	b.Run("retained", func(b *testing.B) { run(b, true, false) })
}
