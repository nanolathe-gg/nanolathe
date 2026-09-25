package gpurender

import (
	"fmt"
	"github.com/hajimehoshi/ebiten/v2"
	"image"
	"image/color"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// If the last child cannot be placed, no earlier child image was rasterized
// either. Their shadows must not read apparently valid empty atlas regions.
func TestModelGroupAllocationFailureInvalidatesEarlierChildren(t *testing.T) {
	var d modelDirectLane
	d.resetFrame()
	d.page = modelDirectMaxPages - 1
	d.packX, d.packY = modelDirectAtlasW-120, modelDirectAtlasH-44
	parent := directSubject(0, 0, 40, 20)
	child := directSubject(0, 0, 4, 4)
	child.Reveal = &drawlist.ModelReveal{Above: -2, Below: -2, Band: -2}
	child.Shadow = &drawlist.ModelGeometry{Silhouette: true}
	later := directSubject(0, 0, 8, 4)
	parent.Children = []drawlist.ModelChild{{Geometry: child}, {Geometry: later}}
	if region := d.assignGroupRegions(parent); region.ok {
		t.Fatal("group unexpectedly fit after its second child ran out of atlas space")
	}
	for _, g := range []*drawlist.ModelGeometry{parent, child, later} {
		if d.regions[g].ok {
			t.Fatal("failed group retained a valid unwritten raster")
		}
	}
	if _, ok := d.shadowSource(child); ok {
		t.Fatal("failed group's earlier child still provides a shadow image")
	}
}

// Reflected children keep their own finished alpha, and compare against the
// group's surviving key too. An erased high face must not reflect the plate
// underneath at the child's height, even when its key ties the plate.
func checkModelGroupReflectionPixels() error {
	shader, err := newReflectionSourceShader()
	if err != nil {
		return err
	}
	defer shader.Deallocate()
	own := image.NewRGBA(image.Rect(0, 0, 16, 16))
	keys := image.NewRGBA(own.Bounds())
	group := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 4; y++ {
		for x := 0; x < 12; x++ {
			if x >= 4 {
				own.SetRGBA(x, y, color.RGBA{100, 100, 100, 255})
			}
			keys.SetRGBA(x, y, color.RGBA{30, 0, 0, 255})
			key := uint8(30)
			if x >= 4 && x < 8 {
				key = 40
			}
			group.SetRGBA(x+8, y+4, color.RGBA{key, 0, 0, 255})
		}
	}
	src, ownKey, groupKey := ebiten.NewImageFromImage(own), ebiten.NewImageFromImage(keys), ebiten.NewImageFromImage(group)
	defer src.Deallocate()
	defer ownKey.Deallocate()
	defer groupKey.Deallocate()
	dst := ebiten.NewImage(12, 4)
	defer dst.Deallocate()
	var v [4]ebiten.Vertex
	for i, xy := range [4][2]float32{{0, 0}, {12, 0}, {12, 4}, {0, 4}} {
		v[i] = ebiten.Vertex{DstX: xy[0], DstY: xy[1], SrcX: xy[0], SrcY: xy[1], ColorB: 8, ColorA: 4, Custom0: 10, Custom1: 30, Custom3: -1}
	}
	dst.DrawTrianglesShader(v[:], []uint16{0, 1, 2, 0, 2, 3}, shader, &ebiten.DrawTrianglesShaderOptions{
		Images:   [4]*ebiten.Image{src, ownKey, groupKey},
		Uniforms: map[string]any{"Metadata": float32(0), "RecordScale": float32(1), "Surface": []float32{0, 0, 1, 0}},
	})
	pix := make([]byte, 12*4*4)
	dst.ReadPixels(pix)
	for _, sample := range []struct {
		x    int
		want byte
	}{{2, 0}, {6, 0}, {10, 255}} {
		if got := pix[(12+sample.x)*4+3]; got != sample.want {
			return fmt.Errorf("construction reflection at %d alpha=%d want=%d", sample.x, got, sample.want)
		}
	}
	return nil
}

func TestModelGroupReflectionKeepsWorldPlacement(t *testing.T) {
	r := &Renderer{}
	r.reflections.active = &drawlist.ModelGeometry{ReflectWater: true, WorldHeight: 10}
	r.reflections.region = modelDirectRegion{x: 200, y: 300, page: 1, bounds: image.Rect(30, 40, 50, 60), ok: true}
	r.modelDirect.groupReflection = modelDirectRegion{x: 20, y: 40, page: 0, bounds: image.Rect(10, 20, 60, 70), ok: true}
	face := directFace(0, 0, 8, 8, 100, 30, 30)
	r.reflectModelFace(&face, 200, 300, 2, 0, 0, 0, 1, 1)
	if len(r.reflections.runs) != 1 || len(r.reflections.verts) != 4 {
		t.Fatal("missing reflected child")
	}
	run, v := r.reflections.runs[0], r.reflections.verts[0]
	if run.page != 1 || run.occlusionPage != 0 || v.SrcX != 200 || v.SrcY != 300 || v.DstX != 30 || v.DstY != 50 || v.ColorB != -140 || v.ColorA != -220 || v.Custom3 != -1 {
		t.Fatalf("reflection source/group mapping changed placement: run=%+v vertex=%+v", run, v)
	}
}

// modelGroupWaveFixture is a merge list over two 256-texel pages that takes
// every branch of the wave rule, and the index of each wave's first slot.
// Every rectangle it reads or writes lies inside the pages.
func modelGroupWaveFixture() ([]modelGroupMerge, []int) {
	region := func(page, x, y int32, bounds image.Rectangle) modelDirectRegion {
		return modelDirectRegion{x: x, y: y, page: page, bounds: bounds, ok: true}
	}
	// aligned gives parent and child the same bw × bh bounds, so the box is
	// both grown by the margin: the parent's rectangle lies at (px, py) on
	// page pp, the child's at (cx, cy) on page cp, each 2(bw+2) × 2(bh+2).
	aligned := func(pp, px, py, cp, cx, cy int32, bw, bh int, key bool) modelGroupMerge {
		b := image.Rect(0, 0, bw, bh)
		return modelGroupMerge{parent: region(pp, px+2, py+2, b), child: region(cp, cx+2, cy+2, b), key: key}
	}
	carrier := region(0, 150, 196, image.Rect(0, 0, 40, 16))
	return []modelGroupMerge{
		// Wave 0: twelve independent 204 × 8 merges, parents in the top rows of
		// both pages and children further down, their page pairs interleaved
		// so the evaluation splits into runs. The eleventh passes the
		// 2048-texel shelf and opens a second shelf of the same wave.
		aligned(0, 2, 2, 0, 2, 130, 100, 2, true),
		aligned(1, 2, 2, 0, 2, 140, 100, 2, true),
		aligned(0, 2, 12, 1, 2, 130, 100, 2, false),
		aligned(0, 2, 22, 1, 2, 140, 100, 2, true),
		aligned(1, 2, 12, 1, 2, 150, 100, 2, true),
		aligned(0, 2, 32, 0, 2, 150, 100, 2, true),
		aligned(1, 2, 22, 0, 2, 160, 100, 2, false),
		aligned(1, 2, 32, 0, 2, 170, 100, 2, true),
		aligned(0, 2, 42, 1, 2, 160, 100, 2, true),
		aligned(1, 2, 42, 1, 2, 170, 100, 2, true),
		aligned(0, 2, 52, 0, 2, 180, 100, 2, true),
		aligned(1, 2, 52, 1, 2, 180, 100, 2, true),
		// Boxes that do not meet: no slot and no stats.
		{parent: region(0, 222, 2, image.Rect(0, 0, 4, 4)), child: region(1, 222, 2, image.Rect(50, 50, 54, 54)), key: true},
		// Write after read: this parent's rectangle covers children the first
		// merges only read, so it stays in wave 0.
		{parent: region(0, 86, 122, image.Rect(10, 10, 40, 30)), child: region(1, 222, 102, image.Rect(18, 14, 32, 24)), key: true},
		// Read after write: this child's rectangle overlaps the first merge's
		// parent, so it starts wave 1.
		aligned(1, 2, 200, 0, 60, 4, 20, 4, true),
		// One carrier's three children in turn, the second and third only
		// partly inside it: each reads what the one before wrote, so the second
		// starts wave 2 and the third, which carries no key, wave 3.
		{parent: carrier, child: region(1, 150, 216, image.Rect(0, 0, 40, 16)), key: true},
		{parent: carrier, child: region(0, 20, 220, image.Rect(8, 4, 30, 12)), key: true},
		{parent: carrier, child: region(1, 100, 150, image.Rect(20, 0, 44, 10)), key: false},
		// Independent of the carrier: joins its last wave.
		aligned(1, 60, 100, 1, 60, 60, 10, 10, true),
	}, []int{0, 13, 15, 16}
}

// referenceModelGroupMerges is the sequential merge on the CPU, the contract
// the waves must reproduce: each merge in turn evaluates its whole box from the
// planes as the earlier merges left them, then writes its colour back to the
// parent's rectangle, and its key when it carries one.
func referenceModelGroupMerges(colour, key *[2][]byte, size int, merges []modelGroupMerge) (merged, pixels int, err error) {
	for n, merge := range merges {
		p, c := merge.parent, merge.child
		box := c.bounds.Inset(-modelDirectMargin / 2).Intersect(p.bounds.Inset(-modelDirectMargin / 2))
		if box.Empty() {
			continue
		}
		w, h := 2*box.Dx(), 2*box.Dy()
		px, py := int(p.x)+2*(box.Min.X-p.bounds.Min.X), int(p.y)+2*(box.Min.Y-p.bounds.Min.Y)
		cx, cy := int(c.x)+2*(box.Min.X-c.bounds.Min.X), int(c.y)+2*(box.Min.Y-c.bounds.Min.Y)
		page := image.Rect(0, 0, size, size)
		if !image.Rect(px, py, px+w, py+h).In(page) || !image.Rect(cx, cy, cx+w, cy+h).In(page) {
			return 0, 0, fmt.Errorf("group merge fixture %d leaves the page", n)
		}
		outColour, outKey := make([]byte, w*h*4), make([]byte, w*h*4)
		for y := range h {
			for x := range w {
				pi, ci, o := ((py+y)*size+px+x)*4, ((cy+y)*size+cx+x)*4, (y*w+x)*4
				priorColour, priorKey := colour[p.page][pi:pi+4], key[p.page][pi:pi+4]
				childColour, childKey := colour[c.page][ci:ci+4], key[c.page][ci:ci+4]
				if childColour[3] > 0 && priorKey[0] <= childKey[0] {
					copy(outColour[o:], childColour)
					copy(outKey[o:], childKey)
				} else {
					copy(outColour[o:], priorColour)
					copy(outKey[o:], priorKey)
				}
			}
		}
		for y := range h {
			row, out := ((py+y)*size+px)*4, y*w*4
			copy(colour[p.page][row:row+w*4], outColour[out:out+w*4])
			if merge.key {
				copy(key[p.page][row:row+w*4], outKey[out:out+w*4])
			}
		}
		merged++
		pixels += w * h
	}
	return merged, pixels, nil
}

// The waves must give every texel of both planes of both pages exactly what
// merging one child at a time gives, for independent merges spread over both
// pages, a read after a write, a carrier's consecutive children, a write after
// a read, merges without a key, boxes that do not meet and a wrapped shelf.
// The pages hold arbitrary valid premultiplied texels, and keys drawn from a
// narrow range so ties are common. The second frame reuses the scratch pair.
func checkModelGroupMergeWaves() error {
	const size = 256
	shader, err := ebiten.NewShader([]byte(modelGroupMergeShaderSource))
	if err != nil {
		return err
	}
	defer shader.Deallocate()
	r := &Renderer{}
	d := &r.modelDirect
	d.groups.shader = shader
	defer func() {
		for _, img := range []*ebiten.Image{d.groups.colour, d.groups.key, d.pages[0].colour, d.pages[0].key, d.pages[1].colour, d.pages[1].key} {
			if img != nil {
				img.Deallocate()
			}
		}
	}()
	var colour, key [2][]byte
	seed := uint64(0x5eed)
	next := func() uint32 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return uint32(seed >> 32)
	}
	opts := &ebiten.NewImageOptions{Unmanaged: true}
	for p := range colour {
		colour[p], key[p] = make([]byte, size*size*4), make([]byte, size*size*4)
		for i := 0; i < size*size*4; i += 4 {
			// A colour texel is transparent black or opaque; a key texel is opaque.
			if v := next(); v%4 != 0 {
				colour[p][i], colour[p][i+1], colour[p][i+2], colour[p][i+3] = byte(v>>8), byte(v>>16), byte(v>>24), 255
			}
			v := next()
			key[p][i], key[p][i+1], key[p][i+2], key[p][i+3] = 100+byte(v%6), byte(v>>8), byte(v>>16), 255
		}
		pg := &d.pages[p]
		pg.colour = ebiten.NewImageWithOptions(image.Rect(0, 0, size, size), opts)
		pg.key = ebiten.NewImageWithOptions(image.Rect(0, 0, size, size), opts)
		pg.colour.WritePixels(colour[p])
		pg.key.WritePixels(key[p])
	}
	merges, waves := modelGroupWaveFixture()
	got := make([]byte, size*size*4)
	for frame := range 2 {
		r.modelStats, r.lastDest = ModelStats{}, nil
		d.groups.merges = append(d.groups.merges[:0], merges...)
		r.mergeModelGroups()
		merged, pixels, err := referenceModelGroupMerges(&colour, &key, size, merges)
		if err != nil {
			return err
		}
		for p := range colour {
			for plane, img := range [2]*ebiten.Image{d.pages[p].colour, d.pages[p].key} {
				want := colour[p]
				if plane == 1 {
					want = key[p]
				}
				img.ReadPixels(got)
				for i := range got {
					if got[i] != want[i] {
						t := i / 4
						return fmt.Errorf("group merge waves frame %d page %d plane %d texel (%d,%d): got %v want %v",
							frame, p, plane, t%size, t/size, got[t*4:t*4+4], want[t*4:t*4+4])
					}
				}
			}
		}
		// Four waves: six passes each for the two that write both planes of
		// both pages, four for one page, five where one page takes no key.
		// Merging one child at a time took four per keyed merge and two per
		// other, 66 here.
		s, b := r.modelStats, d.groups.colour.Bounds()
		if !slices.Equal(d.groups.waves, waves) || s.DirectGroupMerges != merged || s.DirectGroupPixels != pixels ||
			s.DirectPasses != 21 || s.Passes != s.DirectPasses || s.DirectGroupScratchBytes != b.Dx()*b.Dy()*8 {
			return fmt.Errorf("group merge waves frame %d: waves=%v want %v, stats merges=%d/%d pixels=%d/%d passes=%d/%d direct scratch=%d over %v",
				frame, d.groups.waves, waves, s.DirectGroupMerges, merged, s.DirectGroupPixels, pixels, s.DirectPasses, s.Passes, s.DirectGroupScratchBytes, b)
		}
	}
	return nil
}

// The wave rule on its own: a merge starts a wave when it reads what an
// earlier merge of the wave writes, never merely for writing what one read,
// and when the scratch would pass its bounds.
func TestModelGroupMergeWaves(t *testing.T) {
	var m modelGroupMergeLane
	merges, waves := modelGroupWaveFixture()
	m.merges = merges
	m.planWaves()
	if len(m.slots) != len(merges)-1 || !slices.Equal(m.waves, waves) {
		t.Fatalf("fixture waves=%v slots=%d, want %v and %d", m.waves, len(m.slots), waves, len(merges)-1)
	}
	for _, want := range []struct{ slot, x, y int }{{9, 1836, 0}, {10, 0, 8}, {12, 408, 8}, {13, 0, 0}, {14, 44, 0}} {
		if s := m.slots[want.slot]; s.sx != want.x || s.sy != want.y {
			t.Fatalf("slot %d at (%d,%d), want (%d,%d)", want.slot, s.sx, s.sy, want.x, want.y)
		}
	}

	merge := func(page, x, y int32, bw, bh int, key bool) modelGroupMerge {
		b := image.Rect(0, 0, bw, bh)
		return modelGroupMerge{
			parent: modelDirectRegion{x: x + 2, y: y + 2, page: page, bounds: b, ok: true},
			child:  modelDirectRegion{x: 2, y: 2, page: 1 - page, bounds: b, ok: true}, key: key,
		}
	}
	for _, c := range []struct {
		name   string
		merges []modelGroupMerge
		waves  []int
	}{
		// A merge without a key still writes its colour.
		{"read after a colour-only write", []modelGroupMerge{merge(0, 0, 0, 10, 10, false), merge(0, 12, 12, 10, 10, true)}, []int{0, 1}},
		{"touching rectangles", []modelGroupMerge{merge(0, 0, 0, 10, 10, true), merge(0, 24, 0, 10, 10, true), merge(0, 0, 24, 10, 10, true)}, []int{0}},
		// 1024-square slots, four to the 2048 square: the fifth starts a wave.
		{"height cap", []modelGroupMerge{
			merge(0, 0, 0, 510, 510, true), merge(0, 1024, 0, 510, 510, true), merge(0, 2048, 0, 510, 510, true),
			merge(0, 3072, 0, 510, 510, true), merge(0, 0, 1024, 510, 510, true),
		}, []int{0, 4}},
		// A slot wider than the scratch has a wave to itself.
		{"oversized", []modelGroupMerge{merge(0, 0, 0, 10, 10, true), merge(0, 0, 100, 1100, 4, true), merge(0, 100, 0, 10, 10, true)}, []int{0, 1, 2}},
		{"oversized first", []modelGroupMerge{merge(0, 0, 100, 1100, 4, true), merge(0, 100, 0, 10, 10, true)}, []int{0, 1}},
	} {
		m.merges = c.merges
		m.planWaves()
		if !slices.Equal(m.waves, c.waves) {
			t.Fatalf("%s: waves=%v want %v", c.name, m.waves, c.waves)
		}
		for i, s := range m.slots {
			if s.sx+s.w > modelGroupScratchW && s.sx != 0 || s.sy+s.h > modelGroupScratchH && s.sy != 0 {
				t.Fatalf("%s: slot %d at (%d,%d) %dx%d passes the scratch", c.name, i, s.sx, s.sy, s.w, s.h)
			}
		}
	}
}
