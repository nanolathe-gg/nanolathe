package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
)

type snapshotUIStage struct {
	fill   uint8
	called int
}

func (s *snapshotUIStage) DrawUI(c *Client, _ UIFrame) {
	s.called++
	for i := range c.indexed {
		c.indexed[i] = s.fill
	}
}

func snapshotCursor(index uint8) *Cursors {
	entry := &formats.GAFEntry{FrameCount: 1, Frames: []formats.GAFFrameRef{{
		Value: 1,
		Frame: &formats.GAFFrame{
			Width:       1,
			Height:      1,
			Pixels:      []byte{index},
			Transparent: []bool{false},
		},
	}}}
	cs := &Cursors{idx: 1}
	cs.play.Bind(entry, 0, true)
	return cs
}

func snapshotPalette() *palette.Tables {
	p := &palette.Tables{}
	for i := 0; i < 256; i++ {
		logical := uint8(255 - i)
		p.Logical[i] = logical
		p.Base[logical] = [4]byte{byte(i), byte(i + 1), byte(i + 2), byte(i + 3)}
	}
	// Exercise the existing present-time opaque-alpha rule through the same
	// installed table route: convertIndexedToRGBA changes reserved zero to 255.
	p.Base[p.Logical[19]][3] = 0
	return p
}

func wantSnapshotRGBA(c *Client, indexed []uint8) []byte {
	got := make([]byte, len(indexed)*4)
	for i, idx := range indexed {
		physical := c.logical[idx]
		copy(got[i*4:i*4+4], c.base[physical][:])
		if got[i*4+3] == 0 {
			got[i*4+3] = 255
		}
	}
	return got
}

func TestP28ComposeFrameSnapshotCopiesOneCommittedComposition(t *testing.T) {
	buf := frame.NewBuffer()
	write := buf.BeginWrite()
	write.Paused = true
	if err := buf.Publish(17); err != nil {
		t.Fatal(err)
	}
	steps := 0
	c, err := New(Options{Buffer: buf, Width: 3, Height: 2, Step: func(float64) { steps++ }})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(snapshotPalette())
	stage := &snapshotUIStage{fill: 7}
	c.SetUIStage(stage)
	// Bind the package-local software cursor directly: SetCursors additionally
	// changes the platform cursor mode, which is unrelated to pixel ordering.
	c.cursors = snapshotCursor(19)
	c.in.Mouse.SetPosition(1, 0)

	snapshot := c.ComposeFrameSnapshot()
	if stage.called != 1 {
		t.Fatalf("UI composition calls = %d, want exactly one", stage.called)
	}
	if steps != 0 {
		t.Fatalf("diagnostic composition advanced injected simulation step %d times", steps)
	}
	if !snapshot.Committed || snapshot.Tick != 17 {
		t.Fatalf("committed identity = (%v,%d), want (true,17)", snapshot.Committed, snapshot.Tick)
	}
	if snapshot.Width != 3 || snapshot.Height != 2 || len(snapshot.Indexed) != 6 || len(snapshot.RGBA) != 24 {
		t.Fatalf("snapshot dimensions = %dx%d indexed=%d rgba=%d", snapshot.Width, snapshot.Height, len(snapshot.Indexed), len(snapshot.RGBA))
	}
	wantIndexed := []byte{7, 19, 7, 7, 7, 7}
	if !bytes.Equal(snapshot.Indexed, wantIndexed) {
		t.Fatalf("indexed order/cursor = %v, want %v", snapshot.Indexed, wantIndexed)
	}
	if want := wantSnapshotRGBA(c, snapshot.Indexed); !bytes.Equal(snapshot.RGBA, want) {
		t.Fatalf("RGBA does not correspond to same-call indexed plane:\n got %v\nwant %v", snapshot.RGBA, want)
	}
	if !write.Paused || write.Tick != 17 {
		t.Fatalf("composition mutated committed frame: tick=%d paused=%v", write.Tick, write.Paused)
	}
}

func TestP28ComposeFrameSnapshotIsolatedBothDirections(t *testing.T) {
	c, err := New(Options{Width: 2, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(snapshotPalette())
	stage := &snapshotUIStage{fill: 7}
	c.SetUIStage(stage)

	first := c.ComposeFrameSnapshot()
	firstIndexed := append([]byte(nil), first.Indexed...)
	firstRGBA := append([]byte(nil), first.RGBA...)

	first.Indexed[0] = 201
	first.RGBA[0] = 202
	if c.indexed[0] != 7 || c.rgba[0] != firstRGBA[0] {
		t.Fatalf("caller mutation reached client planes: indexed=%d rgba=%d", c.indexed[0], c.rgba[0])
	}
	mutatedFirstIndexed := append([]byte(nil), first.Indexed...)
	mutatedFirstRGBA := append([]byte(nil), first.RGBA...)

	stage.fill = 11
	second := c.ComposeFrameSnapshot()
	if !bytes.Equal(first.Indexed, mutatedFirstIndexed) || !bytes.Equal(first.RGBA, mutatedFirstRGBA) {
		t.Fatal("later composition altered prior caller-owned evidence")
	}
	if !bytes.Equal(second.Indexed, []byte{11, 11}) {
		t.Fatalf("later composition indexed = %v, want [11 11]", second.Indexed)
	}
	second.Indexed[0] = 203
	second.RGBA[0] = 204
	if first.Indexed[1] != firstIndexed[1] || first.RGBA[1] != firstRGBA[1] || c.indexed[0] != 11 {
		t.Fatal("one returned snapshot aliases another")
	}
}

func TestP28ComposeFrameSnapshotEmptyStableAndTraceNeutral(t *testing.T) {
	c, err := New(Options{Width: 2, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	withoutTrace := c.ComposeFrameSnapshot()
	if withoutTrace.Committed || withoutTrace.Tick != 0 || withoutTrace.Width != 2 || withoutTrace.Height != 2 {
		t.Fatalf("empty snapshot metadata = %+v", withoutTrace)
	}
	if len(withoutTrace.Indexed) != 4 || len(withoutTrace.RGBA) != 16 {
		t.Fatalf("empty snapshot dimensions indexed=%d rgba=%d", len(withoutTrace.Indexed), len(withoutTrace.RGBA))
	}
	c.SetRendererTraceSink(func(RendererCandidate) {})
	withTrace := c.ComposeFrameSnapshot()
	if !bytes.Equal(withTrace.Indexed, withoutTrace.Indexed) || !bytes.Equal(withTrace.RGBA, withoutTrace.RGBA) {
		t.Fatal("enabling renderer tracing changed copied pixels")
	}
	repeated := c.ComposeFrameSnapshot()
	if !bytes.Equal(repeated.Indexed, withTrace.Indexed) || !bytes.Equal(repeated.RGBA, withTrace.RGBA) {
		t.Fatal("repeated empty composition produced unstable bytes")
	}
	if got := (*Client)(nil).ComposeFrameSnapshot(); got.Width != 0 || got.Height != 0 || got.Committed || len(got.Indexed) != 0 || len(got.RGBA) != 0 {
		t.Fatalf("nil client snapshot = %+v, want deterministic zero value", got)
	}
}
