package gpurender

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

func TestQuadBatchHasRoomAtUint16Boundary(t *testing.T) {
	r := &Renderer{verts: make([]ebiten.Vertex, quadBatchVertexLimit-quadVertices)}
	if !r.quadBatchHasRoom() {
		t.Fatalf("quad batch with %d vertices rejected its final quad", len(r.verts))
	}
	r.verts = append(r.verts, make([]ebiten.Vertex, quadVertices)...)
	if r.quadBatchHasRoom() {
		t.Fatalf("quad batch with %d vertices accepted an index-wrapping quad", len(r.verts))
	}
}
