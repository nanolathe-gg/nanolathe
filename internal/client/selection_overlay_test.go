package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/palette"
)

func TestSelectionDragUsesNormalizedInclusiveFramesAndPaletteMap(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[4] = 44
	c.pal.Logical[0] = 99
	c.SetSelectionDrag(SelectionDrag{Active: true, BoxMode: true, VisiblePanel: true, StartX: 132, StartY: 36, EndX: 130, EndY: 34})
	c.drawSelectionStage()

	// Endpoints are sorted per axis, and both endpoints are written. The
	// inset is [131,35]..[131,35], so the single inner pixel is deliberate.
	for _, p := range [][2]int{{130, 34}, {132, 34}, {130, 36}, {132, 36}} {
		if got := c.indexed[p[1]*c.width+p[0]]; got != 44 {
			t.Fatalf("outer edge at %v = %d, want mapped logical 4 -> 44", p, got)
		}
	}
	if got := c.indexed[35*c.width+131]; got != 99 {
		t.Fatalf("inner edge = %d, want mapped logical 0 -> 99", got)
	}
}

func TestSelectionDragUsesArmedAndOutsidePaletteEntries(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[6] = 66
	c.pal.Logical[0] = 100
	c.SetSelectionDrag(SelectionDrag{Active: true, BoxMode: true, BuildWake: true, VisiblePanel: true, StartX: 140, StartY: 40, EndX: 144, EndY: 44})
	c.drawSelectionStage()
	if got := c.indexed[40*c.width+140]; got != 66 {
		t.Fatalf("armed outer edge = %d, want mapped logical 6 -> 66", got)
	}

	for i := range c.indexed {
		c.indexed[i] = 0
	}
	c.pal.Logical[15] = 155
	c.SetSelectionDrag(SelectionDrag{Active: true, VisiblePanel: true, StartX: 140, StartY: 40, EndX: 144, EndY: 44})
	c.drawSelectionStage()
	if got := c.indexed[40*c.width+140]; got != 155 {
		t.Fatalf("outside-box outer edge = %d, want mapped logical 15 -> 155", got)
	}
}

func TestSelectionDragClipsToVisiblePanelAndClearsBetweenFrames(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[4] = 4
	c.pal.Logical[0] = 0
	c.SetSelectionDrag(SelectionDrag{Active: true, BoxMode: true, VisiblePanel: true, StartX: 120, StartY: 20, EndX: 132, EndY: 40})
	c.drawSelectionStage()
	if got := c.indexed[32*c.width+127]; got != 0 {
		t.Fatalf("pixel outside visible-panel clip changed to %d", got)
	}
	if got := c.indexed[32*c.width+128]; got != 0 {
		t.Fatalf("synthetic clip-boundary edge = %d, want untouched 0", got)
	}
	if got := c.indexed[40*c.width+128]; got != 4 {
		t.Fatalf("surviving original bottom edge = %d, want 4", got)
	}
	if got := c.indexed[32*c.width+132]; got != 4 {
		t.Fatalf("surviving original right edge = %d, want 4", got)
	}

	c.SetSelectionDrag(SelectionDrag{})
	c.composeIndexed(nil, false)
	if got := c.indexed[32*c.width+128]; got != 0 {
		t.Fatalf("stale drag pixel after inactive frame = %d, want cleared", got)
	}
}

func TestSelectionDragDegenerateInnerWritesNoInnerFrame(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[4] = 4
	c.pal.Logical[0] = 9
	c.SetSelectionDrag(SelectionDrag{Active: true, BoxMode: true, VisiblePanel: true, StartX: 140, StartY: 40, EndX: 141, EndY: 41})
	c.drawSelectionStage()
	for _, p := range [][2]int{{140, 40}, {141, 40}, {140, 41}, {141, 41}} {
		if got := c.indexed[p[1]*c.width+p[0]]; got != 4 {
			t.Fatalf("degenerate inner changed edge %v to %d, want outer 4", p, got)
		}
	}
}

func TestSelectionDragUnknownPanelModeDoesNotInventClip(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	for i := range c.indexed {
		c.indexed[i] = 77
	}
	c.SetSelectionDrag(SelectionDrag{Active: true, BoxMode: true, StartX: 140, StartY: 40, EndX: 144, EndY: 44})
	c.drawSelectionStage()
	if got := c.indexed[40*c.width+140]; got != 77 {
		t.Fatalf("unknown panel mode emitted pixel %d; clip behavior must remain unresolved", got)
	}
}
