package frame

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestMessageRingCaptionBudgetAndExpiry(t *testing.T) {
	r := NewMessageRing()
	r.Configure(3, 0)
	r.Append("a", 1, 4, 10, 0)
	r.Append("b", 1, 5, 10, 0)
	r.Append("c", 1, 6, 10, 0) // third append drops the oldest of two visible lines
	if r.Producer != 3 || r.Display != 1 {
		t.Fatalf("indices after fullness = producer %d display %d, want 3,1", r.Producer, r.Display)
	}
	lines := r.Visible()
	if len(lines) != 2 || lines[0].Text != "b" || lines[1].Text != "c" {
		t.Fatalf("visible lines = %#v, want b,c", lines)
	}
	r.Expire(31) // strict stored+(scroll+1)*30 < currentTick
	if got := r.Visible(); len(got) != 0 {
		t.Fatalf("expired lines = %#v, want empty", got)
	}
}

func TestMessageRingStorageIndicesWrapAtThirty(t *testing.T) {
	r := NewMessageRing()
	r.Configure(30, 0)
	for i := 0; i < 31; i++ {
		if !r.Append("x", 1, 0, 10, uint32(i)) {
			t.Fatalf("append %d rejected", i)
		}
	}
	if r.Producer != 1 || r.Display != 2 {
		t.Fatalf("indices after 30-slot wrap = producer %d display %d, want 1,2", r.Producer, r.Display)
	}
	if got := len(r.Visible()); got != 29 {
		t.Fatalf("visible count after wrap = %d, want textlines-1 = 29", got)
	}
}

// TestSpeedAnnouncementBothForms locks [07 R-CAM-01 §3]'s two text forms. The
// `%c` is a space for a zero or negative offset, so the negative form carries
// three spaces before its sign — which `%+d` cannot produce.
func TestSpeedAnnouncementBothForms(t *testing.T) {
	cases := []struct {
		speed int
		want  string
	}{
		{10, "Game Speed Normal"},
		{11, "Game Speed  +1"},
		{20, "Game Speed  +10"},
		{8, "Game Speed   -2"},
		{1, "Game Speed   -9"},
	}
	for _, c := range cases {
		if got := SpeedAnnouncement(c.speed); got != c.want {
			t.Fatalf("SpeedAnnouncement(%d) = %q, want %q", c.speed, got, c.want)
		}
	}
}

// TestMessageRingClearAndVisitedWalk locks the F12 and F3 ring mechanics
// [07 R-CAM-01 §2].
func TestMessageRingClearAndVisitedWalk(t *testing.T) {
	r := NewMessageRing()
	r.Append("dead", 1, 4, 10, 0)
	r.Append("live", 1, 5, 10, 0)
	alive := func(h pool.Handle) bool { return h == 5 }

	r.ClearVisited()
	got, ok := r.NextUnvisitedSource(alive)
	if !ok || got != 5 {
		t.Fatalf("first walk returned %d (%v), want the live source 5", got, ok)
	}
	if _, ok := r.NextUnvisitedSource(alive); ok {
		t.Fatalf("the walk offered the same record twice without a clear")
	}
	r.ClearVisited()
	if _, ok := r.NextUnvisitedSource(alive); !ok {
		t.Fatalf("clearing the visited bits did not restart the walk")
	}

	r.Clear()
	if r.Producer != 0 || r.Display != 0 || len(r.Visible()) != 0 {
		t.Fatalf("Clear left producer %d display %d and %d lines", r.Producer, r.Display, len(r.Visible()))
	}
}
