package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/palette"
)

func TestSelectionDragUsesNormalizedInclusiveFramesAndPaletteMap(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[15] = 44
	c.pal.Logical[0] = 99
	c.SetSelectionDrag(SelectionDrag{Active: true, VisiblePanel: true, StartX: 132, StartY: 36, EndX: 130, EndY: 34})
	c.drawSelectionStage()

	// Endpoints are sorted per axis, and both endpoints are written. The
	// inset is [131,35]..[131,35], so the single inner pixel is deliberate.
	for _, p := range [][2]int{{130, 34}, {132, 34}, {130, 36}, {132, 36}} {
		if got := c.indexed[p[1]*c.width+p[0]]; got != 44 {
			t.Fatalf("outer edge at %v = %d, want mapped logical 15 -> 44", p, got)
		}
	}
	if got := c.indexed[35*c.width+131]; got != 99 {
		t.Fatalf("inner edge = %d, want mapped logical 0 -> 99", got)
	}
}

// TestSelectionDragOuterEntryFollowsTheArmedLatch locks the three outer
// entries and which state selects each: an ordinary drag is white (logical 15),
// an armed MOBILEBUILD latch is 4, and 6 only with latch-flag bit 0x40 set
// [07 R-P0-11 §1 "The drawing."][07 §6 "Frame composition passes"]. A build
// that read the box-selection flag instead painted every drag in entry 4.
func TestSelectionDragOuterEntryFollowsTheArmedLatch(t *testing.T) {
	cases := []struct {
		name  string
		drag  SelectionDrag
		outer int
		want  uint8
	}{
		{"ordinary drag", SelectionDrag{}, 15, 155},
		{"mobilebuild armed", SelectionDrag{MobileBuildLatch: true}, 4, 44},
		{"mobilebuild armed, latch bit 0x40", SelectionDrag{MobileBuildLatch: true, SpecialLatchFlag: true}, 6, 66},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
			c.pal.Logical[tc.outer] = tc.want
			c.pal.Logical[0] = 100
			d := tc.drag
			d.Active, d.VisiblePanel = true, true
			d.StartX, d.StartY, d.EndX, d.EndY = 140, 40, 144, 44
			c.SetSelectionDrag(d)
			c.drawSelectionStage()
			if got := c.indexed[40*c.width+140]; got != tc.want {
				t.Fatalf("outer edge = %d, want mapped logical %d -> %d", got, tc.outer, tc.want)
			}
			if got := c.indexed[41*c.width+141]; got != 100 {
				t.Fatalf("inner edge = %d, want mapped logical 0 -> 100", got)
			}
		})
	}
}

func TestSelectionDragClipsToVisiblePanelAndClearsBetweenFrames(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[15] = 4
	c.pal.Logical[0] = 0
	c.SetSelectionDrag(SelectionDrag{Active: true, VisiblePanel: true, StartX: 120, StartY: 20, EndX: 132, EndY: 40})
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
	c.pal.Logical[15] = 4
	c.pal.Logical[0] = 9
	c.SetSelectionDrag(SelectionDrag{Active: true, VisiblePanel: true, StartX: 140, StartY: 40, EndX: 141, EndY: 41})
	c.drawSelectionStage()
	for _, p := range [][2]int{{140, 40}, {141, 40}, {140, 41}, {141, 41}} {
		if got := c.indexed[p[1]*c.width+p[0]]; got != 4 {
			t.Fatalf("degenerate inner changed edge %v to %d, want outer 4", p, got)
		}
	}
}

// The rail's state does not gate the overlay. The viewport subrect this clip
// comes from has one writer, which hard-codes left 128 and top 32 and never
// consults the rail [03 §4.1]; both readings of the clip in [R-SEL-02A] draw
// the rectangle and disagree only about its left edge. This test previously
// asserted the opposite — that a drag with VisiblePanel clear emitted nothing —
// which locked a third arm neither reading offers and made a drag started while
// the rail was mid-slide invisible for the whole gesture (WU-19-160).
func TestSelectionDragDrawsWithThePanelAwayFromItsVisibleDetent(t *testing.T) {
	c := &Client{width: 200, height: 100, indexed: make([]uint8, 200*100), pal: &palette.Tables{}}
	c.pal.Logical[15] = 4
	for i := range c.indexed {
		c.indexed[i] = 77
	}
	c.SetSelectionDrag(SelectionDrag{Active: true, StartX: 140, StartY: 40, EndX: 144, EndY: 44})
	c.drawSelectionStage()
	if got := c.indexed[40*c.width+140]; got != 4 {
		t.Fatalf("drag with the panel off its detent emitted pixel %d, want outer entry 15 -> physical 4", got)
	}
	// The clip is unchanged too: a pixel left of column 128 stays untouched.
	c.SetSelectionDrag(SelectionDrag{Active: true, StartX: 120, StartY: 40, EndX: 124, EndY: 44})
	c.drawSelectionStage()
	if got := c.indexed[40*c.width+120]; got != 77 {
		t.Fatalf("drag left of the viewport clip wrote pixel %d, want the clip to reject it", got)
	}
}
