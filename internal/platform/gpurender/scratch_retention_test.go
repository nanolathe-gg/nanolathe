package gpurender

import (
	"image"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Frame storage may grow while earlier subjects still need its slices. Only
// the submission boundary retires those references (§11.5 "CPU", I6).
func TestPreparationRetiresReferencesAfterFrame(t *testing.T) {
	var scratch modelPrepScratch
	face := &drawlist.ModelFace{Vertices: []drawlist.ModelVertex{{X: 9}}}
	firstVertices := scratch.vertices.take(4)
	firstVertices[0].X = 17
	firstStrips := scratch.strips.take(1)
	firstStrips[0].Vertices = firstVertices
	firstPrepared := scratch.prepared.take(1)
	firstPrepared[0] = preparedModelFace{face: face, strips: firstStrips}
	page := modelPage{subjects: []modelPageSubject{{faces: firstPrepared, outline: firstStrips}}}
	oldSubjects := page.subjects

	// Force every pointer-bearing arena, and the numeric one they reference,
	// onto a new backing array without retiring the earlier subject.
	lastVertices := scratch.vertices.take(32)
	lastVertices[0].X = 23
	lastStrips := scratch.strips.take(8)
	lastStrips[0].Vertices = lastVertices
	lastPrepared := scratch.prepared.take(8)
	lastPrepared[0] = preparedModelFace{face: face, strips: lastStrips, vertices: lastVertices}
	if page.subjects[0].faces[0].face != face || page.subjects[0].outline[0].Vertices[0].X != 17 {
		t.Fatal("arena growth changed an earlier current-frame subject")
	}
	stripCap, preparedCap, vertexCap := cap(scratch.strips.buf), cap(scratch.prepared.buf), cap(scratch.vertices.buf)
	scratch.reset()
	page.beginFrame(modelPageMaxRows)
	if oldSubjects[0].faces != nil || oldSubjects[0].outline != nil {
		t.Fatal("retired page still references an earlier arena generation")
	}
	for _, f := range scratch.strips.buf {
		if f.Vertices != nil || f.Texture != nil {
			t.Fatal("retired strip still references frame data")
		}
	}
	for _, f := range scratch.prepared.buf {
		if f.face != nil || f.strips != nil || f.vertices != nil || f.indices != nil {
			t.Fatal("retired prepared face still references frame data")
		}
	}
	if cap(scratch.strips.buf) != stripCap || cap(scratch.prepared.buf) != preparedCap || cap(scratch.vertices.buf) != vertexCap {
		t.Fatal("retirement discarded warm capacity")
	}
	if lastVertices[0].X != 23 {
		t.Fatal("retirement cleared numeric storage")
	}
	// A much shorter frame cannot leave the first frame's unused pointer tail.
	scratch.strips.take(1)[0].Vertices = scratch.vertices.take(4)
	scratch.prepared.take(1)[0].face = face
	scratch.reset()
	if n := testing.AllocsPerRun(20, func() {
		scratch.strips.take(1)
		scratch.prepared.take(1)
		scratch.vertices.take(4)
		scratch.reset()
	}); n != 0 {
		t.Fatalf("warm scratch retirement allocated %g objects", n)
	}
}

func TestUnusedOverflowPageRetiresPreviousSubject(t *testing.T) {
	r := &Renderer{}
	previous := []modelPageSubject{{faces: []preparedModelFace{{face: &drawlist.ModelFace{}}}}}
	r.modelAtlas.overflow[0].subjects = previous
	// No models means no overflow reuse and no device work. Opening the next
	// frame must still release the old subject's preparation references.
	r.prepareModelSlots(&drawlist.List{})
	if len(r.modelAtlas.overflow[0].subjects) != 0 || previous[0].faces != nil {
		t.Fatal("unused overflow page retained previous preparation")
	}
}

// CPU preparation only: no device, scene allocation, or readback enters the
// measurement. Large outlines exercise the pointer-bearing strip storage.
func BenchmarkScratchRetentionPreparation(b *testing.B) {
	r := &Renderer{}
	g := fixtureGeometry(0, true, fixtureFace(0, 0, 48, 64, 20, 3))
	g.Outline = []drawlist.ModelFace{fixtureFace(0, 0, 48, 64, 20, 5)}
	prepare := func() {
		for i := 0; i < 64; i++ {
			out := r.modelPrep.prepared.take(len(g.Faces))
			for j := range g.Faces {
				r.prepareModelFace(&out[j], &g.Faces[j], image.Point{}, false)
			}
			r.prepareModelOutline(g, false)
		}
		r.modelPrep.reset()
	}
	// An arena can grow more than once during its first frame, rewinding the
	// offset each time. Warm subsequent frames until the whole workload fits.
	for i := 0; i < 3; i++ {
		prepare()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prepare()
	}
}
