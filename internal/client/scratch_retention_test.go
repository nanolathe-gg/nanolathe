package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func retentionFaces(faces, corners int) []drawlist.ModelFace {
	out := make([]drawlist.ModelFace, faces)
	for i := range out {
		out[i].Vertices = make([]drawlist.ModelVertex, corners)
		for j := range out[i].Vertices {
			out[i].Vertices[j] = drawlist.ModelVertex{X: int32(i + j), Y: int32(j), Key: 7}
		}
	}
	return out
}

func TestModelFaceShrinkDropsRetiredVertexReferences(t *testing.T) {
	dst, vertices := copyModelFaces(nil, nil, retentionFaces(4, 3), 0, 0)
	oldFaces := dst[:cap(dst)]
	oldVertex := &vertices[0]
	src := retentionFaces(1, 64)
	dst, vertices = copyModelFaces(dst, vertices, src, 2, 3)
	if &vertices[0] == oldVertex {
		t.Fatal("fixture did not replace vertex storage")
	}
	for _, face := range oldFaces[1:] {
		if face.Vertices != nil || face.Texture != nil {
			t.Fatal("removed face retains previous vertex storage")
		}
	}
	for i, v := range src[0].Vertices {
		want := v
		want.X += 2
		want.Y += 3
		if dst[0].Vertices[i] != want {
			t.Fatal("tail cleanup changed copied geometry")
		}
	}
	if !reflect.DeepEqual(src, retentionFaces(1, 64)) {
		t.Fatal("copy changed retained source geometry")
	}
}

func TestPacketReuseDropsOnlyObsoleteReferences(t *testing.T) {
	c := &Client{}
	c.modelScratch.active = true
	polys := make([]screenPoly, 4)
	for i := range polys {
		polys[i] = newScreenPoly(3)
	}
	old := c.borrowModelPacket(polys, 8, 8, 0, 0, 0, 0, 1, false, drawlist.ModelFallbackNone)
	oldFaces := old.Faces[:cap(old.Faces)]
	packet := c.modelScratch.packets[0]
	packet.supersample = drawlist.ModelGeometry{
		Outline: old.Faces,
		Reveal:  &drawlist.ModelReveal{},
		Shadow:  &drawlist.ModelGeometry{},
	}
	c.modelScratch.reset()
	if packet.supersample.Outline != nil || packet.supersample.Reveal != nil || packet.supersample.Shadow != nil {
		t.Fatal("frame reset retained doubled packet metadata")
	}
	large := []screenPoly{newScreenPoly(64)}
	large[0].x[0] = 37
	current := c.borrowModelPacket(large, 8, 8, 0, 0, 0, 0, 1, false, drawlist.ModelFallbackNone)
	for _, face := range oldFaces[1:] {
		if face.Vertices != nil {
			t.Fatal("shorter packet retained removed face vertices")
		}
	}
	// A different slot can grow in the same frame without clearing this one.
	c.borrowModelPacket([]screenPoly{newScreenPoly(256)}, 8, 8, 0, 0, 0, 0, 1, false, drawlist.ModelFallbackNone)
	if current.Faces[0].Vertices[0].X != 37 {
		t.Fatal("later packet growth changed current-frame geometry")
	}
}

func TestPolygonGrowthDropsUnusedLaneReferences(t *testing.T) {
	c := &Client{}
	c.modelScratch.active = true
	first := c.borrowPolys(4, 12)
	for i := 0; i < 4; i++ {
		first.next(3).x[0] = int32(i)
	}
	oldPolys := first.polys[:cap(first.polys)]
	c.modelScratch.reset()
	second := c.borrowPolys(4, 64)
	// Admission may emit fewer faces than the reserved count. Those unvisited
	// records must not keep the replaced lane allocation alive.
	second.next(64).x[0] = 43
	for _, p := range oldPolys[1:] {
		if p.x != nil || p.y != nil || p.x2 != nil || p.y2 != nil || p.oddHeight != nil {
			t.Fatal("unemitted polygon retains previous lane storage")
		}
		for _, lane := range p.attr {
			if lane != nil {
				t.Fatal("unemitted polygon retains previous attribute storage")
			}
		}
	}
	c.borrowPolys(1, 256).next(256)
	if second.polys[0].x[0] != 43 {
		t.Fatal("later polygon slot growth changed current-frame data")
	}
}

func BenchmarkScratchRetentionFaceCopy(b *testing.B) {
	large, small := retentionFaces(256, 4), retentionFaces(64, 4)
	dst, vertices := copyModelFaces(nil, nil, large, 0, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst, vertices = copyModelFaces(dst, vertices, small, 2, 3)
		dst, vertices = copyModelFaces(dst, vertices, large, 2, 3)
	}
}
