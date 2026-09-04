package frame

import "testing"

// The message column draws the record F3 last jumped to in colour-map entry 10
// and every other line in entry 15, installing the pair per line
// [07 R-CAM-01 §14 "the jumped-to line is the highlighted line"]. Swapping the
// two entries still highlights *a* line, so the failure mode is silent; the
// pairing is what this pins.
func TestMessageLineJumpedTakesTheHighlightEntry(t *testing.T) {
	var line MessageLine
	if got := line.LogicalColor(); got != 15 {
		t.Fatalf("ordinary line colour = %d, want colour-map entry 15", got)
	}
	line.Jumped = true
	if got := line.LogicalColor(); got != 10 {
		t.Fatalf("jumped-to line colour = %d, want colour-map entry 10", got)
	}
	// Visited is F3's other bit and decides nothing about colour.
	line = MessageLine{Visited: true}
	if got := line.LogicalColor(); got != 15 {
		t.Fatalf("visited-but-not-jumped line colour = %d, want colour-map entry 15", got)
	}
}
