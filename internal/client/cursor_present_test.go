package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// The placement reticle is anchored on the picked build point even when the
// presentation pointer arrives after recording. The stock GAF offset would
// move its visible centre down-right [03 R-FX-01 §5].
func TestPlacementReticleCenteredAfterLatePosition(t *testing.T) {
	c, err := New(Options{Width: 80, Height: 70})
	if err != nil {
		t.Fatal(err)
	}
	cs := snapshotCursor(99)
	cs.idx = render.CursorFindSite
	f := cs.Frame()
	f.Width, f.Height = 21, 23
	f.XOffset, f.YOffset = -15, -3
	f.Pixels = make([]byte, int(f.Width)*int(f.Height))
	f.Transparent = make([]bool, len(f.Pixels))
	for i := range f.Transparent {
		f.Transparent[i] = true
	}
	f.Pixels[11*int(f.Width)+10] = 99
	f.Transparent[11*int(f.Width)+10] = false
	c.SetCursors(cs)
	c.in.Mouse.SetPosition(30, 30)
	c.drawCursor()
	c.PositionPresentationCursor(&c.list, 40, 35)
	c.replayForTest()
	if got := c.indexed[35*c.width+40]; got != 99 {
		t.Fatalf("late positioned reticle centre = %d, want 99", got)
	}
	if got := c.indexed[30*c.width+30]; got != 0 {
		t.Fatalf("reticle remained at old pointer: %d", got)
	}
}

// Host late positioning must affect only the presented pointer, preserve the
// GAF hotspot [07 §8], and never move the pointer used by commands [I6].
func TestPresentationCursorMovesWithoutPublishingInput(t *testing.T) {
	c, err := New(Options{Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCursors(snapshotCursor(19))
	c.cursors.Frame().XOffset, c.cursors.Frame().YOffset = 1, 2
	c.in.UpdatePointerMotion(input.PointerEvent{X: 2, Y: 3})
	c.in.PublishPointer()
	before, _ := c.in.PointerSample()
	c.StartPreRecord(0, 0, false)
	c.JoinPreRecord()
	list, hit := c.TakePreRecord(c.PresentationDigest(), 0)
	if !hit {
		t.Fatal("unchanged presentation did not consume its pre-record")
	}
	c.PositionPresentationCursor(list, 6, 6)
	list.Replay(classicSink{c: c})
	if c.indexed[4*8+5] != 19 || c.indexed[1*8+1] == 19 {
		t.Fatal("cursor was not moved from its recorded location with its authored hotspot")
	}
	if after, _ := c.in.PointerSample(); !reflect.DeepEqual(after, before) {
		t.Fatal("presentation positioning changed command input")
	}
}

func TestPresentationCursorCaptureAndRestore(t *testing.T) {
	c, err := New(Options{Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	c.SetCursors(snapshotCursor(19))
	c.in.UpdatePointerMotion(input.PointerEvent{X: 2, Y: 3})
	c.in.PublishPointer()
	c.SetPointerCaptured(true)
	list := c.RecordModernFrame()
	c.PositionPresentationCursor(list, 6, 6)
	list.Replay(classicSink{c: c})
	for _, pixel := range c.indexed {
		if pixel == 19 {
			t.Fatal("captured pointer was drawn")
		}
	}
	c.SetPointerCaptured(false)
	list = c.RecordModernFrame()
	c.PositionPresentationCursor(list, 6, 6)
	list.Replay(classicSink{c: c})
	if c.indexed[3*8+2] != 19 || c.indexed[6*8+6] == 19 {
		t.Fatal("late positioning replaced the capture-release restore position")
	}
}
