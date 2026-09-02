package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

// traceFace is traceTriangle as the n-corner face the body raster consumes.
func traceFace(key int32, color uint8) screenPoly {
	p := walkPoly([][2]int32{{0, 0}, {4, 0}, {0, 4}}, []int32{key, key, key})
	p.color, p.candidate, p.piece, p.primitive, p.texture = color, uint32(color), 2, 7, "trace-texture"
	return p
}

func traceTriangle(key float64, color uint8) screenPoly {
	k := int32(key)
	p := walkPoly([][2]int32{{0, 0}, {4, 0}, {0, 4}}, []int32{k, k, k})
	p.color = color
	p.candidate, p.piece, p.primitive, p.texture = uint32(color), 2, 7, "trace-texture"
	return p
}

func renderTracedTriangles(t *testing.T, triangles ...screenPoly) ([]uint8, []RendererCandidate) {
	t.Helper()
	const width, height = 6, 6
	c := &Client{width: width, height: height, indexed: make([]uint8, width*height)}
	for i := range c.indexed {
		c.indexed[i] = 99
	}
	var got []RendererCandidate
	target := newModelTarget(width, height)
	target.tick = 41
	target.trace = newRendererTrace(width * height)
	target.winner = target.trace.winner
	target.trace.width, target.trace.height = width, height
	for i := range triangles {
		c.fillPolyTarget(target, &triangles[i], triangles[i].color, nil, 77)
	}
	target.commit(c.indexed, c.width, c.height)
	target.trace.resolve(target, c.indexed, width, height)
	target.trace.emit(func(v RendererCandidate) { got = append(got, v) }, nil)
	return append([]uint8(nil), c.indexed...), got
}

func TestP28RendererTraceLaterAdmittedCandidateWins(t *testing.T) {
	pixels, got := renderTracedTriangles(t, traceTriangle(2, 11), traceTriangle(5, 22))
	if len(got) == 0 {
		t.Fatal("trace did not observe any indexed candidates")
	}
	var first, later *RendererCandidate
	for i := range got {
		if got[i].X == 1 && got[i].Y == 1 {
			switch got[i].CandidateOrder {
			case 11:
				first = &got[i]
			case 22:
				later = &got[i]
			}
		}
	}
	if first == nil || later == nil {
		t.Fatalf("overlap trace missing candidates: first=%v later=%v", first, later)
	}
	if !first.Admitted || first.ActualWinner || first.Reason != RendererReasonDisplacedByLaterCandidate {
		t.Fatalf("early admitted candidate should lose later: %+v", *first)
	}
	if !later.Admitted || !later.ActualWinner || later.FinalPalette != 22 || later.Reason != RendererReasonLaterAdmittedCandidate {
		t.Fatalf("later candidate should be final indexed winner: %+v", *later)
	}
	if pixels[1*6+1] != 22 {
		t.Fatalf("pixel winner=%d, want 22", pixels[1*6+1])
	}
}

func TestP28RendererTraceClassifiesEachDisplacementBoundary(t *testing.T) {
	_, got := renderTracedTriangles(t, traceTriangle(5, 5), traceTriangle(7, 7), traceTriangle(7, 8))
	byOrder := make(map[uint32]*RendererCandidate)
	for i := range got {
		if got[i].X == 1 && got[i].Y == 1 {
			byOrder[got[i].CandidateOrder] = &got[i]
		}
	}
	first, middle, final := byOrder[5], byOrder[7], byOrder[8]
	if first == nil || middle == nil || final == nil {
		t.Fatalf("displacement chain missing candidates: %+v", got)
	}
	if first.ActualWinner || first.Reason != RendererReasonDisplacedByLaterCandidate {
		t.Fatalf("first candidate should be displaced by strict higher candidate: %+v", *first)
	}
	if middle.ActualWinner || middle.Reason != RendererReasonDisplacedByEqualHeightCandidate {
		t.Fatalf("middle candidate should be displaced by equal-height candidate: %+v", *middle)
	}
	if !final.ActualWinner || final.Reason != RendererReasonEqualHeightLaterCandidate || final.FinalPalette != 8 {
		t.Fatalf("final candidate should be equal-height winner: %+v", *final)
	}
}

func TestP28RendererTraceCaptureCapReportsDeterministicOverflow(t *testing.T) {
	makeTrace := func() *rendererTrace {
		r := newRendererTrace(1)
		r.width, r.height = 1, 1
		for i := 0; i < rendererTraceEventCap+3; i++ {
			event := r.add(RendererCandidate{X: 0, Y: 0, IncomingHeight: uint8(i)})
			if event >= 0 {
				r.admitted(0, event)
			}
		}
		return r
	}
	first, second := makeTrace(), makeTrace()
	if got, want := first.stats(), (RendererTraceStats{Capacity: rendererTraceEventCap, Captured: rendererTraceEventCap, Dropped: 3, Overflowed: true}); got != want {
		t.Fatalf("overflow stats=%+v, want %+v", got, want)
	}
	if got, want := second.stats(), first.stats(); got != want {
		t.Fatalf("overflow stats are not deterministic: first=%+v second=%+v", want, got)
	}
	if first.winner[0] != rendererTraceEventCap-1 || !first.incomplete[0] {
		t.Fatalf("overflow must preserve the last captured winner and mark its pixel incomplete: winner=%d incomplete=%v", first.winner[0], first.incomplete[0])
	}
	target := &modelTarget{covered: []bool{true}}
	first.resolve(target, []uint8{42}, 1, 1)
	for i, event := range first.events {
		if event.ActualWinner || event.FinalWriter != RendererWriterUnknown || event.FinalPaletteState != RendererValueUnavailable || event.FinalUnavailable != RendererUnavailableTraceOverflow {
			t.Fatalf("overflow event %d claims final provenance: %+v", i, event)
		}
	}
}

func TestP28RendererTraceResetClearsCaptureState(t *testing.T) {
	r := newRendererTrace(1)
	r.width, r.height, r.unit, r.tick = 1, 1, 9, 12
	event := r.add(RendererCandidate{X: 0, Y: 0})
	r.admitted(0, event)
	r.add(RendererCandidate{X: 0, Y: 0})
	r.reset()
	if got := r.stats(); got != (RendererTraceStats{Capacity: rendererTraceEventCap}) {
		t.Fatalf("reset stats=%+v, want empty capture with capacity", got)
	}
	if len(r.events) != 0 || len(r.predecessor) != 0 || r.winner[0] != -1 || r.incomplete[0] || r.unit != 0 || r.tick != 0 || r.width != 0 || r.height != 0 {
		t.Fatalf("reset left capture state: events=%d predecessors=%d winner=%d incomplete=%v unit=%d tick=%d size=%dx%d", len(r.events), len(r.predecessor), r.winner[0], r.incomplete[0], r.unit, r.tick, r.width, r.height)
	}
	event = r.add(RendererCandidate{X: 0, Y: 0})
	r.admitted(0, event)
	if event != 0 || !r.events[0].Admitted || r.winner[0] != 0 {
		t.Fatalf("reset did not restore capture lifecycle: event=%d admitted=%v winner=%d", event, r.events[0].Admitted, r.winner[0])
	}
}

func TestP28RendererTraceEqualHeightLaterCandidateWinsAndRejects(t *testing.T) {
	_, got := renderTracedTriangles(t, traceTriangle(4, 31), traceTriangle(4, 32), traceTriangle(3, 33))
	var equalFirst, equalLater, rejected *RendererCandidate
	for i := range got {
		if got[i].X != 1 || got[i].Y != 1 {
			continue
		}
		switch got[i].CandidateOrder {
		case 31:
			equalFirst = &got[i]
		case 32:
			equalLater = &got[i]
		case 33:
			rejected = &got[i]
		}
	}
	if equalFirst == nil || equalLater == nil || rejected == nil {
		t.Fatalf("tie/reject trace missing: %+v", got)
	}
	if equalFirst.ActualWinner || equalFirst.Reason != RendererReasonDisplacedByEqualHeightCandidate || !equalLater.ActualWinner || equalLater.Reason != RendererReasonEqualHeightLaterCandidate {
		t.Fatalf("equal-height tie must choose later candidate: first=%+v later=%+v", *equalFirst, *equalLater)
	}
	if rejected.Admitted || rejected.RejectReason != "height" || rejected.Reason != RendererReasonHeightRejected {
		t.Fatalf("lower candidate must carry height rejection: %+v", *rejected)
	}
}

func TestP28RendererTraceTexturePaletteAndShadeArePerPixel(t *testing.T) {
	c := &Client{width: 6, height: 6, indexed: make([]uint8, 36), pal: &palette.Tables{}}
	for row := range c.pal.Shade {
		for i := range c.pal.Shade[row] {
			c.pal.Shade[row][i] = byte(i)
		}
	}
	frame := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{17}, Transparent: []bool{false}}
	target := newModelTarget(6, 6)
	target.tick = 9
	target.trace = newRendererTrace(36)
	target.winner = target.trace.winner
	target.trace.width, target.trace.height = 6, 6
	face := traceFace(1, 17)
	face.frame, face.frameIndex, face.frameState = frame, 3, RendererValueAvailable
	face.useSHD = true
	face.attr[spanRow][0], face.attr[spanRow][1], face.attr[spanRow][2] = 7, 19, 31
	var got []RendererCandidate
	c.blitTexturedPolyTarget(target, &face, frame, nil, 123)
	target.commit(c.indexed, c.width, c.height)
	target.trace.resolve(target, c.indexed, 6, 6)
	target.trace.emit(func(v RendererCandidate) { got = append(got, v) }, nil)
	var winner *RendererCandidate
	for i := range got {
		if got[i].X == 1 && got[i].Y == 1 {
			winner = &got[i]
			break
		}
	}
	if winner == nil {
		t.Fatal("textured candidate missing")
	}
	if winner.Texture.FrameState != RendererValueAvailable || winner.Texture.Frame != 3 || winner.Texture.TexelState != RendererValueAvailable || winner.Texture.Texel != 17 {
		t.Fatalf("missing resolved texture provenance: %+v", winner.Texture)
	}
	if winner.ShadeRow == 0 || winner.CandidateIndex != 17 || winner.FinalPalette != 17 || !winner.ActualWinner {
		t.Fatalf("missing per-pixel palette/shade winner: %+v", *winner)
	}
}

func TestP28RendererTraceNanoframeEraseIsUnknown(t *testing.T) {
	c := &Client{width: 6, height: 6, indexed: make([]uint8, 36)}
	for i := range c.indexed {
		c.indexed[i] = 88
	}
	target := newModelTarget(6, 6)
	target.tick = 12
	target.trace = newRendererTrace(36)
	target.winner = target.trace.winner
	target.trace.width, target.trace.height = 6, 6
	face := traceFace(4, 33)
	reveal := presentationrender.NanoframeReveal{Below: presentationrender.NanoframeErase, Band: presentationrender.NanoframeErase, Above: presentationrender.NanoframeErase}
	c.fillPolyTarget(target, &face, face.color, &reveal, 55)
	target.commit(c.indexed, c.width, c.height)
	target.trace.resolve(target, c.indexed, 6, 6)
	var erased *RendererCandidate
	var got []RendererCandidate
	target.trace.emit(func(v RendererCandidate) { got = append(got, v) }, nil)
	for i := range got {
		if got[i].X == 1 && got[i].Y == 1 {
			erased = &got[i]
			break
		}
	}
	if erased == nil {
		t.Fatal("erased candidate missing")
	}
	if erased.Reason != RendererReasonNanoframeErase || erased.FinalWriter != RendererWriterUnknown || erased.FinalPaletteState != RendererValueUnavailable || erased.FinalUnavailable != RendererUnavailableNanoframeErase || erased.ActualWinner {
		t.Fatalf("erase must remain unknown/background-uninstrumented: %+v", *erased)
	}
	if got := c.indexed[1*6+1]; got != 88 {
		t.Fatalf("erase changed destination background: got %d", got)
	}
}

func TestP28RendererTraceOutlineOnlyAndOverwrite(t *testing.T) {
	target := newModelTarget(6, 6)
	target.trace = newRendererTrace(36)
	target.winner = target.trace.winner
	target.trace.width, target.trace.height = 6, 6
	c := &Client{width: 6, height: 6, indexed: make([]uint8, 36)}
	tri := traceTriangle(5, 20)
	c.fillPolyTarget(target, &tri, tri.color, nil, 91)
	target.trace.composite(1, 1, 200, 3, 4)
	target.trace.composite(4, 4, 201, 5, 6)
	target.commit(c.indexed, c.width, c.height)
	c.indexed[1*6+1] = 200
	c.indexed[4*6+4] = 201
	target.trace.resolve(target, c.indexed, 6, 6)
	var body, overwritten, only *RendererCandidate
	target.trace.emit(func(v RendererCandidate) {
		switch {
		case v.X == 1 && v.Y == 1 && v.Writer == RendererWriterModel:
			body = &v
		case v.X == 1 && v.Y == 1 && v.Writer == RendererWriterNanoframeOutline:
			overwritten = &v
		case v.X == 4 && v.Y == 4:
			only = &v
		}
	}, nil)
	if body == nil || overwritten == nil || only == nil {
		t.Fatalf("outline evidence missing: body=%v overwrite=%v only=%v", body, overwritten, only)
	}
	if body.ActualWinner || body.Reason != RendererReasonOutlineOverwrite || body.FinalWriter != RendererWriterNanoframeOutline || body.FinalPalette != 200 {
		t.Fatalf("body event should report outline overwrite: %+v", *body)
	}
	if !overwritten.ActualWinner || overwritten.Reason != RendererReasonOutlineOnly || overwritten.FinalWriter != RendererWriterNanoframeOutline || overwritten.Piece != 3 || overwritten.Primitive != 4 || overwritten.FinalPalette != 200 {
		t.Fatalf("outline winner malformed: %+v", *overwritten)
	}
	if !only.ActualWinner || only.Reason != RendererReasonOutlineOnly || only.FinalWriter != RendererWriterNanoframeOutline || only.Piece != 5 || only.Primitive != 6 || only.FinalPalette != 201 {
		t.Fatalf("outline-only winner malformed: %+v", *only)
	}
	if body.Scope != RendererScopeSubjectComposition || overwritten.Scope != RendererScopeSubjectComposition || only.Scope != RendererScopeSubjectComposition {
		t.Fatalf("trace scope overclaims full-frame provenance: body=%v overwrite=%v only=%v", body.Scope, overwritten.Scope, only.Scope)
	}
	if body.OutsideScope != RendererOutsideAll || overwritten.OutsideScope != RendererOutsideAll || only.OutsideScope != RendererOutsideAll {
		t.Fatalf("trace does not identify uninstrumented later passes: body=%v overwrite=%v only=%v", body.OutsideScope, overwritten.OutsideScope, only.OutsideScope)
	}
}

func TestP28RendererTraceNilSinkPreservesPixels(t *testing.T) {
	triangles := []screenPoly{traceTriangle(2, 11), traceTriangle(5, 22)}
	want, _ := renderTracedTriangles(t, triangles...)
	c := &Client{width: 6, height: 6, indexed: make([]uint8, 36)}
	for i := range c.indexed {
		c.indexed[i] = 99
	}
	target := newModelTarget(6, 6)
	for i := range triangles {
		c.fillPolyTarget(target, &triangles[i], triangles[i].color, nil, 77)
	}
	target.commit(c.indexed, c.width, c.height)
	if !reflect.DeepEqual(c.indexed, want) {
		t.Fatalf("nil-sink pixels changed: traced=%v nil=%v", want, c.indexed)
	}
}

func TestP28RendererTraceCapPreservesPixels(t *testing.T) {
	triangles := make([]screenPoly, rendererTraceEventCap+1)
	for i := range triangles {
		triangles[i] = traceTriangle(2, uint8(i))
	}
	traced, _ := renderTracedTriangles(t, triangles...)
	c := &Client{width: 6, height: 6, indexed: make([]uint8, 36)}
	for i := range c.indexed {
		c.indexed[i] = 99
	}
	target := newModelTarget(c.width, c.height)
	for i := range triangles {
		c.fillPolyTarget(target, &triangles[i], triangles[i].color, nil, 77)
	}
	target.commit(c.indexed, c.width, c.height)
	if !reflect.DeepEqual(c.indexed, traced) {
		t.Fatalf("capture cap changed indexed pixels: traced=%v nil=%v", traced, c.indexed)
	}
}
