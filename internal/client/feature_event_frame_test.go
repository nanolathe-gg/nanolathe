package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

// A cell carrying a live event record blits that record's own cursor frames;
// a cell without one blits the definition's rest cursor [03 R-RAST-01 §6]
// [05 R-FEAT-01 §10] pass 3. The visit index walks the same max(delay, 1)
// cadence the simulation timed the record with, so the frame on screen is the
// frame the record is on.
//
// The defect this locks: a reclaimed tree stayed on its idle frame for the
// whole reclaim animation and then popped straight to its `featurereclamate`
// successor, because the committed feature view carried only `seqname` and the
// draw path had no way to reach `seqnamereclamate` at all.
func TestFeatureFrameFollowsTheEventCursorNotTheRestCursor(t *testing.T) {
	c := newFeatureSequenceClient(t)
	// The fixture's burn entry is three frames with authored delays 3, 0 and 2,
	// so its widths by visit are 20, 20, 20, 5, 16, 16 and it lasts six visits.
	widthByVisit := []int32{20, 20, 20, 5, 16, 16}
	for visit, want := range widthByVisit {
		view := frame.FeatureView{
			Filename: "trees", SeqName: "treedie",
			EventSeqName: "treeburn", EventSeqVisit: int32(visit),
		}
		got := c.featureFrameFor(view, false)
		if got == nil {
			t.Fatalf("visit %d resolved no frame", visit)
		}
		if int32(got.Width) != want {
			t.Fatalf("visit %d drew a %d-wide frame, want %d: the event cursor is not the one being read", visit, got.Width, want)
		}
	}
	// Past the end the cursor sits on the last frame — the visit that retires
	// the record.
	if got := c.featureFrameFor(frame.FeatureView{
		Filename: "trees", SeqName: "treedie", EventSeqName: "treeburn", EventSeqVisit: 99,
	}, false); got == nil || got.Width != 16 {
		t.Fatalf("a visit past the end resolved %v, want the last frame", got)
	}
	// With no event record the rest cursor is in charge again.
	rest := c.featureFrameFor(frame.FeatureView{Filename: "trees", SeqName: "treedie"}, false)
	if rest == nil || rest.Width != 8 {
		t.Fatalf("the resting feature drew %v, want the 8-wide rest frame", rest)
	}
	// A definition naming no shadow twin for its event sequence draws no
	// shadow rather than falling back to the rest shadow, which would leave a
	// standing tree's silhouette under its own reclaim animation.
	if shadow := c.featureFrameFor(frame.FeatureView{
		Filename: "trees", SeqName: "treedie", SeqNameShad: "treedie",
		EventSeqName: "treeburn", EventSeqVisit: 0,
	}, true); shadow != nil {
		t.Fatal("an event record with no shadow sequence still drew the rest shadow")
	}
}
