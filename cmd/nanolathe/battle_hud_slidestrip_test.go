package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
)

// The slide strip's three lines are literal formats around translated keys:
// `%s : %02d:%02d:%02d`, `%s : %d  (Max %d)` and `%s %s` — no colon after the
// key, two spaces before `(Max` [07 R-HUD-04 §4].
func TestSlideStripLineFormats(t *testing.T) {
	if got, want := slideStripTimeText(30*(3600+62)), "Game Time : 01:01:02"; got != want {
		t.Fatalf("time line = %q, want %q", got, want)
	}
	if got, want := slideStripUnitsText(7, 500), "Total Units : 7  (Max 500)"; got != want {
		t.Fatalf("units line = %q, want %q", got, want)
	}
	if got, want := "Game Speed "+slideStripSpeedText(frame.StripReadout{RequestedSpeed: 10, ActiveSpeed: 10}), "Game Speed Normal"; got != want {
		t.Fatalf("speed line = %q, want %q", got, want)
	}
	if got, want := "Game Speed "+slideStripSpeedText(frame.StripReadout{RequestedSpeed: 13, ActiveSpeed: 8}), "Game Speed +3 (-2)"; got != want {
		t.Fatalf("speed line = %q, want %q", got, want)
	}
}

func nineSliceEntry(t *testing.T, frames int, w, h uint16) *formats.GAFEntry {
	t.Helper()
	entry := &formats.GAFEntry{}
	for i := 0; i < frames; i++ {
		f := &formats.GAFFrame{Width: w, Height: h}
		f.Pixels = make([]byte, int(w)*int(h))
		f.Transparent = make([]bool, int(w)*int(h))
		entry.Frames = append(entry.Frames, formats.GAFFrameRef{Frame: f})
	}
	return entry
}

type stamp struct{ frame, x, y int }

func collectStamps(entry *formats.GAFEntry, w, h int) []stamp {
	var out []stamp
	nineSliceFill(entry, 0, 0, w, h, func(f *formats.GAFFrame, x, y int) {
		for i := range entry.Frames {
			if entry.Frames[i].Frame == f {
				out = append(out, stamp{i, x, y})
			}
		}
	})
	return out
}

// The tile fill's frame choice: band 0/3/6 by row, column 0/1/2 by position,
// overflowing tiles pulled flush to the far edge, and the two edge tests of
// differing strictness — a row ending exactly on the bottom edge is a middle
// band while a column ending exactly on the right edge is the right column
// [07 R-WGT-02 "tile fill"].
func TestNineSliceFillFrameChoice(t *testing.T) {
	entry := nineSliceEntry(t, 9, 10, 10)
	// 25 x 20: columns at x 0 (left), 10 (middle), 20 -> flush 15 (right);
	// rows at y 0 (top) and y 10, which ends exactly on the bottom edge and so
	// stays a middle band.
	want := []stamp{{0, 0, 0}, {1, 10, 0}, {2, 15, 0}, {3, 0, 10}, {4, 10, 10}, {5, 15, 10}}
	got := collectStamps(entry, 25, 20)
	if len(got) != len(want) {
		t.Fatalf("stamps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stamp %d = %v, want %v (all %v)", i, got[i], want[i], got)
		}
	}
	// 20 x 25: the third row overflows and is the bottom band, flush at y 15;
	// the second column ends exactly on the right edge and is the right column.
	want = []stamp{{0, 0, 0}, {2, 10, 0}, {3, 0, 10}, {5, 10, 10}, {6, 0, 15}, {8, 10, 15}}
	got = collectStamps(entry, 20, 25)
	if len(got) != len(want) {
		t.Fatalf("stamps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stamp %d = %v, want %v (all %v)", i, got[i], want[i], got)
		}
	}
	// A single-frame entry is stamped once at the origin, never tiled.
	single := collectStamps(nineSliceEntry(t, 1, 10, 10), 40, 40)
	if len(single) != 1 || single[0] != (stamp{0, 0, 0}) {
		t.Fatalf("single-frame fill = %v, want one stamp at the origin", single)
	}
}
