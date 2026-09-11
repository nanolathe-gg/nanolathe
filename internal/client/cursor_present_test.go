package client

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/input"
)

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
