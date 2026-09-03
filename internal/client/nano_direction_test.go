package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func nanoFixed(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }

// nanoEvent is one committed strip-6 segment with a six-word box.
func nanoEvent(atSource bool) frame.EffectView {
	return frame.EffectView{
		Kind: frame.EventKindNanolathe.String(), Strip: 6, StartTick: 5,
		X: nanoFixed(100), Y: nanoFixed(0), Z: nanoFixed(100),
		TargetX: nanoFixed(400), TargetY: nanoFixed(0), TargetZ: nanoFixed(400),
		NanolatheGeometryKnown:  true,
		NanolatheTargetBoxKnown: true,
		NanolatheBoxAtSource:    atSource,
		NanolatheTargetMin:      [3]numeric.Fixed{nanoFixed(100), nanoFixed(0), nanoFixed(100)},
		NanolatheTargetMax:      [3]numeric.Fixed{nanoFixed(111), nanoFixed(11), nanoFixed(111)},
	}
}

// Feature reclaim sprays FROM the feature box INTO the builder's nano piece,
// while build, repair and resurrection spray the other way [05 R-WORK-01 §8].
// One submission routine serves both, differing only in which argument becomes
// the degenerate box, so the committed flag has to decide which end carries the
// extent.
//
// The defect this locks: the client read the box as the destination in both
// directions. A feature reclaim's emitter was then born at the tree's own box
// corner and aimed into the tree's own box, so every particle travelled a few
// world units inside the tree and none of the spray ever reached the
// commander — a reclaim with no visible nanolathe at all.
func TestReversedNanoDirectionPutsTheBoxAtTheSourceEnd(t *testing.T) {
	// Each box axis spans 11 world units, so the 4/11..7/11 narrowing gives an
	// origin 4 units in and an extent of 3.
	const originOffset, extent = 4, 3

	forward := &Client{}
	cur := &frame.Frame{Tick: 5}
	cur.Effects = append(cur.Effects, nanoEvent(false))
	forward.tickNanolathe(cur)
	if len(forward.nano.Records) != 1 {
		t.Fatalf("forward direction admitted %d records, want 1", len(forward.nano.Records))
	}
	fwd := forward.nano.Records[0]
	for a := 0; a < 3; a++ {
		if fwd.SrcExtent[a] != 0 {
			t.Fatalf("forward axis %d: source extent %v, want the degenerate nano piece", a, fwd.SrcExtent[a])
		}
		if fwd.DstExtent[a] != nanoFixed(extent) {
			t.Fatalf("forward axis %d: destination extent %v, want the narrowed box %v", a, fwd.DstExtent[a], nanoFixed(extent))
		}
	}

	reverse := &Client{}
	cur = &frame.Frame{Tick: 5}
	cur.Effects = append(cur.Effects, nanoEvent(true))
	reverse.tickNanolathe(cur)
	if len(reverse.nano.Records) != 1 {
		t.Fatalf("reversed direction admitted %d records, want 1", len(reverse.nano.Records))
	}
	rev := reverse.nano.Records[0]
	for a := 0; a < 3; a++ {
		if rev.SrcExtent[a] != nanoFixed(extent) {
			t.Fatalf("reversed axis %d: source extent %v, want the narrowed box %v", a, rev.SrcExtent[a], nanoFixed(extent))
		}
		if rev.DstExtent[a] != 0 {
			t.Fatalf("reversed axis %d: destination extent %v, want the degenerate nano piece", a, rev.DstExtent[a])
		}
	}
	// The destination is the builder's nano piece, four hundred world units
	// away — not a point inside the feature's own box.
	if rev.DstOrigin[0] != nanoFixed(400) || rev.DstOrigin[2] != nanoFixed(400) {
		t.Fatalf("reversed destination %v, want the published nano piece at 400,400", rev.DstOrigin)
	}
	if rev.SrcOrigin[0] != nanoFixed(100+originOffset) {
		t.Fatalf("reversed source origin %v, want the box narrowed to %v", rev.SrcOrigin[0], nanoFixed(100+originOffset))
	}
}
