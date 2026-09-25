package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Device outline rings (model_outline.go) must draw exactly the CPU walk's
// endpoints: the same pixels [03 R-COMP-01 §3] and the same keys, the walk's
// float32 mix truncated, which the device computes as the truncated exact
// rational wherever the two provably agree.

// The validator's float is the walk's float: chainAt's mix and outlineEdgeKey
// are the same float32 operations, fused or not alike.
func TestOutlineEdgeKeyIsTheWalk(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 9))
	for range 20000 {
		a, b := rng.Int32N(1200)-400, rng.Int32N(1200)-400
		dy := 1 + rng.Int32N(300)
		m := rng.Int32N(dy)
		v := []drawlist.ModelVertex{{X: 0, Y: 0, Key: a}, {X: 7, Y: dy, Key: b}}
		got, ok := chainAt(v, 0, 1, 1, m)
		if !ok || got.Key != outlineEdgeKey(a, b, m, dy) {
			t.Fatalf("edge %d→%d over %d rows, row %d: the walk's key %v, the validator's %v", a, b, dy, m, got.Key, outlineEdgeKey(a, b, m, dy))
		}
	}
}

// Wherever outlineEdgeExact accepts an edge, the device's truncated exact
// rational is the walk's truncated float on every row; the grid includes
// edges where the rational is a whole number on some rows and the float lands
// below it, which the check must refuse.
func TestOutlineEdgeExactMatchesTheWalk(t *testing.T) {
	refused := 0
	for dy := int32(1); dy <= 40; dy++ {
		for a := int32(-60); a <= 120; a += 3 {
			for b := int32(-60); b <= 240; b++ {
				ok := outlineEdgeExact(a, b, dy)
				if !ok {
					refused++
					continue
				}
				for m := int32(0); m < dy; m++ {
					device := (a*dy + (b-a)*m) / dy
					if walk := int32(outlineEdgeKey(a, b, m, dy)); walk != device {
						t.Fatalf("edge %d→%d over %d rows, row %d: accepted, but the walk truncates to %d and the device to %d", a, b, dy, m, walk, device)
					}
				}
			}
		}
	}
	if refused == 0 {
		t.Fatal("no edge of the grid was refused; the whole-number rows are not being checked")
	}
	// The worked case: keys −1 to 49 over 25 rows are exactly 1 on the first
	// row, and the fused float lands just below it.
	if int32(outlineEdgeKey(-1, 49, 1, 25)) != 1 && outlineEdgeExact(-1, 49, 25) {
		t.Fatal("an edge whose float truncates below its whole-number key was accepted")
	}
}

// The device ring, ported: on every native pixel of its box, whether the pixel
// is one of its row's endpoints and with which key, against the CPU walk's
// endpoints over random rings of three and four corners — folded, thin, facing
// either way, reaching past the packet's box.
func TestOutlineRingMatchesTheWalk(t *testing.T) {
	var r Renderer
	rng := rand.New(rand.NewPCG(21, 23))
	drawn, walked := 0, 0
	for range 3000 {
		n := 3 + rng.IntN(2)
		v := make([]drawlist.ModelVertex, n)
		for i := range v {
			v[i] = drawlist.ModelVertex{X: rng.Int32N(48) - 8, Y: rng.Int32N(40) - 6, Key: rng.Int32N(400) - 150}
		}
		g := &drawlist.ModelGeometry{Width: 36, Height: 30}
		f := &drawlist.ModelFace{Vertices: v, Color: 9}
		r.modelDirect.params.reset()
		ring, ok := r.describeOutlineRing(g, f)
		want := walkOutlineEndpoints(g, v)
		if !ok {
			walked++
			continue
		}
		got := map[[2]int32]int32{}
		if ring.entry != 0 {
			drawn++
			e := decodeQuadEntry(r.modelDirect.params.buf, int(ring.entry)-1)
			for y := ring.y0; y < ring.y1; y++ {
				for x := ring.x0; x < ring.x1; x++ {
					if key, hit := e.outlineKey(x+modelQuadLocalBias, y+modelQuadLocalBias); hit {
						got[[2]int32{x, y}] = key
					}
				}
			}
		}
		if len(got) != len(want) {
			t.Fatalf("ring %v: the device draws %d endpoints, the walk %d (%v, %v)", v, len(got), len(want), got, want)
		}
		for p, key := range want {
			if got[p] != key {
				t.Fatalf("ring %v: endpoint %v has key %d on the device, %d in the walk", v, p, got[p], key)
			}
		}
	}
	if drawn < 1000 || walked == 0 {
		t.Fatalf("%d rings drawn on the device and %d walked; the cases do not cover both paths", drawn, walked)
	}
}

// walkOutlineEndpoints is the CPU walk's endpoint set of one ring, clipped to
// the packet's box, with each endpoint's truncated key.
func walkOutlineEndpoints(g *drawlist.ModelGeometry, v []drawlist.ModelVertex) map[[2]int32]int32 {
	out := map[[2]int32]int32{}
	top, bottom := 0, 0
	for j := range v {
		if v[j].Y < v[top].Y {
			top = j
		}
		if v[j].Y > v[bottom].Y {
			bottom = j
		}
	}
	for y := v[top].Y; y < v[bottom].Y; y++ {
		left, a := chainAt(v, top, bottom, -1, y)
		right, b := chainAt(v, top, bottom, 1, y)
		if !a || !b || right.X <= left.X {
			continue
		}
		for _, p := range [2]modelGPUVertex{left, right} {
			if p.X < 0 || p.X >= float32(g.Width) || p.Y < 0 || p.Y >= float32(g.Height) {
				continue
			}
			out[[2]int32{int32(p.X), int32(p.Y)}] = int32(p.Key)
		}
	}
	return out
}

// outlineKey is modelOutlineKey on a decoded key entry, without the group
// delta: whether the biased native pixel (x, y) is an endpoint of its row, and
// its key.
func (e quadEntry) outlineKey(x, y int32) (int32, bool) {
	left, right := e.walk(y)
	if left.x == -1 || right.x <= left.x {
		return 0, false
	}
	var at quadEdgeAt
	switch x {
	case left.x:
		at = left
	case right.x:
		at = right
	default:
		return 0, false
	}
	dy := at.yt - at.yf
	return (at.from*dy + (at.to-at.from)*(y-at.yf)) / dy, true
}

// checkModelOutlineDevicePixels draws one list of nanoframe outlines twice,
// once with every ring the device can take drawn on the device and once with
// every ring walked on the CPU, and compares both planes of every atlas page
// used, texel for texel, and the composites (model_outline.go). The rings are
// triangles and quads, folded, thin, facing away, reaching past the packet's
// box, tying the body's key, under a live face that ties theirs, a five-corner
// ring and a ring whose float key truncates below a whole number (both walked
// in either run), a group child's rings under a raised and a saturating key
// delta with a carried child's solo image reusing them, and the same packets
// at twice the size, one with a doubled lane.
func checkModelOutlineDevicePixels() error {
	pal := fixturePalette()
	const w, h = 200, 150
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	list := modelOutlineFixtureList(w, h)
	read := func(walk bool) (composite []byte, planes [][]byte, stats ModelStats) {
		r.modelDirect.walkOutlines = walk
		img := r.Execute(list, w, h)
		composite = make([]byte, w*h*4)
		img.ReadPixels(composite)
		for p := range r.modelDirect.pages {
			pg := &r.modelDirect.pages[p]
			if pg.usedRows == 0 {
				continue
			}
			for _, plane := range [2]*ebiten.Image{pg.key, pg.colour} {
				sub := plane.SubImage(image.Rect(0, 0, modelDirectAtlasW, int(pg.usedRows))).(*ebiten.Image)
				px := make([]byte, modelDirectAtlasW*int(pg.usedRows)*4)
				sub.ReadPixels(px)
				planes = append(planes, px)
			}
		}
		return composite, planes, r.modelStats
	}
	device, devicePlanes, deviceStats := read(false)
	walked, walkedPlanes, walkedStats := read(true)
	r.modelDirect.walkOutlines = false
	if deviceStats.DirectOutlineRings == 0 || walkedStats.DirectOutlineRings != 0 {
		return fmt.Errorf("outline fixture: %d rings on the device, %d when walking; want some, then none", deviceStats.DirectOutlineRings, walkedStats.DirectOutlineRings)
	}
	// The five-corner ring and the inexact-key ring, in the plain packets and
	// their doubled copies, walk in both runs.
	if deviceStats.DirectOutlineWalked < 4 {
		return fmt.Errorf("outline fixture: %d rings walked on the device run, want the five-corner and inexact-key rings", deviceStats.DirectOutlineWalked)
	}
	if deviceStats.DirectCargoImages == 0 {
		return fmt.Errorf("outline fixture: no carried child composed alone; the solo reuse is not exercised")
	}
	if len(devicePlanes) != len(walkedPlanes) {
		return fmt.Errorf("outline fixture: %d planes on the device run, %d walking", len(devicePlanes), len(walkedPlanes))
	}
	for i := range devicePlanes {
		if diff := differingPixels(devicePlanes[i], walkedPlanes[i]); diff != 0 {
			return fmt.Errorf("outline fixture: atlas plane %d differs from the walk on %d texels", i, diff)
		}
	}
	if !bytes.Equal(device, walked) {
		return fmt.Errorf("outline fixture: the composite differs from the walk on %d pixels", differingPixels(device, walked))
	}
	// The rings must show: somewhere the composite is not the field.
	if bytes.Count(device, []byte{7, 7, 7, 255}) == w*h {
		return fmt.Errorf("outline fixture drew nothing")
	}
	return nil
}

// modelOutlineFixtureList is the outline fixture's recording.
func modelOutlineFixtureList(w, h int32) *drawlist.List {
	ring := func(color uint8, key int32, xy ...int32) drawlist.ModelFace {
		f := drawlist.ModelFace{Color: color}
		for i := 0; i+1 < len(xy); i += 2 {
			f.Vertices = append(f.Vertices, drawlist.ModelVertex{X: xy[i], Y: xy[i+1], Key: key + int32(i)})
		}
		return f
	}
	// keyed builds a ring whose corners carry the given keys.
	keyed := func(color uint8, xy []int32, keys ...int32) drawlist.ModelFace {
		f := drawlist.ModelFace{Color: color}
		for i := range keys {
			f.Vertices = append(f.Vertices, drawlist.ModelVertex{X: xy[2*i], Y: xy[2*i+1], Key: keys[i]})
		}
		return f
	}
	outlines := func() []drawlist.ModelFace {
		return []drawlist.ModelFace{
			ring(60, 40, 2, 2, 20, 4, 8, 18),                               // triangle
			ring(61, 38, 22, 2, 36, 3, 34, 16, 21, 14),                     // convex quad
			ring(62, 38, 2, 20, 4, 34, 18, 32, 16, 21),                     // facing away
			ring(63, 30, 22, 18, 34, 30, 20, 26, 36, 21),                   // bow-tie: both chains fold
			ring(64, 45, 5, 22, 6, 34, 7, 23),                              // thin: rows with the right column not past the left
			ring(65, 35, -6, 30, 12, 26, 40, 44, 10, 38),                   // reaches past the packet's box
			ring(66, 40, 24, 30, 30, 28, 36, 34, 30, 40, 24, 36),           // five corners: walked
			keyed(67, []int32{8, 26, 30, 26, 14, 51}, -1, -1, 49),          // a key that truncates below a whole number: walked
			keyed(68, []int32{3, 3, 12, 3, 12, 12, 3, 12}, 40, 40, 40, 40), // ties the body's key
		}
	}
	body := func(scale int32) []drawlist.ModelFace {
		return []drawlist.ModelFace{
			directFace(0, 0, 14*scale, 14*scale, 90, 40, 40),
			directFace(16*scale, 0, 36*scale, 18*scale, 91, 10, 70),
		}
	}
	scaled := func(fs []drawlist.ModelFace, s int32) []drawlist.ModelFace {
		out := make([]drawlist.ModelFace, len(fs))
		for i, f := range fs {
			out[i] = f
			out[i].Vertices = append([]drawlist.ModelVertex(nil), f.Vertices...)
			for j := range out[i].Vertices {
				out[i].Vertices[j].X *= s
				out[i].Vertices[j].Y *= s
			}
		}
		return out
	}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: 7, Style: drawlist.FillSolid})
	// Plain packets at native size and at twice it.
	for i, s := range []int32{1, 2} {
		g := directSubject(4+int32(i)*48, 4, 38*s, 40*s, body(s)...)
		g.Outline = scaled(outlines(), s)
		g.Reveal = &drawlist.ModelReveal{Floor: 20, Line: 60, Below: -1, Band: 250, Above: -2}
		// A live face over the tied ring's corner ties its key and wins.
		g.LiveFaces = []drawlist.ModelFace{directFace(2*s, 2*s, 6*s, 6*s, 92, 40, 40)}
		list.RecordModel(drawlist.Model{Geometry: g})
	}
	// A packet with a doubled lane: the outline still comes from the native
	// packet.
	d := directSubject(150, 4, 38, 40, body(1)...)
	d.Outline = outlines()
	ss := directSubject(0, 0, 76, 80, scaled(body(1), 2)...)
	ss.Scale = 2
	d.Supersample = ss
	list.RecordModel(drawlist.Model{Geometry: d})
	// A carrier whose nanoframe children sit under key deltas, one raising
	// and one saturating at the byte's floor; the first casts a shadow, so its
	// solo image reuses the rings its group composition planned.
	carrier := directSubject(4, 96, 80, 44, directFace(0, 0, 78, 42, 93, 30, 30))
	up := directSubject(4, 96, 38, 40, body(1)...)
	up.Outline = outlines()
	up.Reveal = &drawlist.ModelReveal{Floor: 20, Line: 60, Below: -1, Band: 250, Above: -2}
	up.Shadow = &drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Silhouette: true, SilhouetteClip: 20,
		Width: up.Width, Height: up.Height, AnchorX: 120, AnchorY: up.AnchorY, Scale: 1,
	}
	down := directSubject(44, 96, 38, 40, body(1)...)
	down.Outline = outlines()
	down.Reveal = up.Reveal
	carrier.Children = []drawlist.ModelChild{{Geometry: up, KeyDelta: 25}, {Geometry: down, KeyDelta: -60}}
	list.RecordModel(drawlist.Model{Geometry: up, ShadowOnly: true})
	list.RecordModel(drawlist.Model{Geometry: carrier})
	list.RecordExpand()
	return &list
}
