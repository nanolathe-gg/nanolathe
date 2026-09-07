package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/drawlist"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestModelGeometryPacketPreservesFaceOrderAndArity(t *testing.T) {
	first := newScreenPoly(3)
	first.x = []int32{2, 3, 4}
	first.y = []int32{5, 6, 7}
	first.attr[spanKey] = []int32{-10, 20, 300}
	first.attr[spanU] = []int32{0, 4, 8}
	first.attr[spanV] = []int32{1, 5, 9}
	first.attr[spanRow] = []int32{2, 3, 4}
	second := newScreenPoly(4)
	second.x = []int32{10, 11, 12, 13}
	second.y = []int32{14, 15, 16, 17}
	target := newModelImage(30, 31, 7, 8, 20, 21, true, 1)

	packet := modelGeometryPacket([]screenPoly{first, second}, target, 1, drawlist.ModelFallbackNone)
	if !packet.Eligible || packet.Fallback != drawlist.ModelFallbackNone {
		t.Fatalf("packet eligibility = %v/%v, want eligible/no fallback", packet.Eligible, packet.Fallback)
	}
	if got := len(packet.Faces); got != 2 {
		t.Fatalf("face count = %d, want 2", got)
	}
	if got := len(packet.Faces[0].Vertices); got != 3 {
		t.Fatalf("first face arity = %d, want 3", got)
	}
	if got := len(packet.Faces[1].Vertices); got != 4 {
		t.Fatalf("second face arity = %d, want 4", got)
	}
	vertex := packet.Faces[0].Vertices[2]
	if vertex.X != 4 || vertex.Y != 7 || vertex.Key != 300 || vertex.U != 8 || vertex.V != 9 || vertex.Shade != 4 {
		t.Fatalf("third first-face vertex = %+v, want original projected lanes", vertex)
	}
	first.x[0] = 99
	if got := packet.Faces[0].Vertices[0].X; got != 2 {
		t.Fatalf("packet aliases composition polygon: X=%d, want 2", got)
	}
}

func TestModelGeometryFallbacksKeepCPUOnlyStagesExplicit(t *testing.T) {
	c := &Client{}
	draw := &presentationrender.UnitDraw{WorldPos: [3]numeric.Fixed{0, numeric.Fixed(1 << 16), 0}}
	if got := c.geometryFallback(draw, nil, 0, 1); got != drawlist.ModelFallbackNone {
		t.Fatalf("ordinary body fallback = %v, want none", got)
	}
	if got := c.geometryFallback(draw, nil, 0, 2); got != drawlist.ModelFallbackSupersample {
		t.Fatalf("supersample fallback = %v, want supersample", got)
	}
	if got := c.geometryFallback(draw, &presentationrender.NanoframeReveal{}, 0, 1); got != drawlist.ModelFallbackRevealOrOutline {
		t.Fatalf("reveal fallback = %v, want reveal/outline", got)
	}

	pending := pendingModelCommit{m: composedModel{geometry: &drawlist.ModelGeometry{Eligible: true}}}
	packet := geometryForCommit(pending)
	if packet.Eligible || packet.Fallback != drawlist.ModelFallbackNoBodyCommit {
		t.Fatalf("trace/shadow-only packet = eligible=%v fallback=%v, want false/no-body", packet.Eligible, packet.Fallback)
	}
}
