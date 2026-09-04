package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
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

// The unit-reclaim/capture family is the other half of the same submission
// routine [05 R-WORK-01 §8]: the box is the TARGET UNIT's — its position plus
// the six signed extents of its definition's bounding record [02 R-CAT-01 §7]
// — and it sits at the source end, with the builder's nano piece as the
// degenerate destination.
//
// The defect this locks: the producer swapped the point pair but published no
// box and no NanolatheBoxAtSource flag, so the client re-derived bounds from
// the model and read them as the DESTINATION. Source and destination then both
// lay inside the victim, and a capture or a unit reclaim drew no spray that
// ever left the thing being reclaimed.
func TestUnitReclaimNanoSpraysFromTheTargetUnitsBox(t *testing.T) {
	// The Z footprint is deliberately twice the X footprint so an axis swap
	// cannot pass, and the model top is nonzero so a dropped Y extent cannot.
	const footX, footZ, modelTop = 2, 4, 40
	def := &content.UnitDef{FootprintX: footX, FootprintZ: footZ, ModelTopFixed: modelTop << 16}

	// The bounding record itself: ±(footprint << 20)/2 on X and Z, zero to the
	// model-top walk on Y. Y has no lower walk in retail [02 R-CAT-01 §7], so a
	// negative minimum here would be invented geometry, and the symmetric X/Z
	// halves are world-space — the model/world Z mirror of [03 R-RAST-01 §2]
	// has no purchase on them.
	lo, hi := def.BoundingExtents()
	wantLo := [3]int32{-(footX << 19), 0, -(footZ << 19)}
	wantHi := [3]int32{footX << 19, modelTop << 16, footZ << 19}
	if lo != wantLo || hi != wantHi {
		t.Fatalf("bounding extents = %v/%v, want %v/%v", lo, hi, wantLo, wantHi)
	}

	target := &units.Unit{Def: def, X: nanoFixed(500), Y: nanoFixed(10), Z: nanoFixed(600)}
	boxMin, boxMax := target.NanolatheBox()
	nanoPiece := [3]numeric.Fixed{nanoFixed(900), nanoFixed(20), nanoFixed(600)}

	// Shaped exactly as the two reversed producers publish it.
	e := frame.EffectView{
		Kind: frame.EventKindNanolathe.String(), Strip: 6, StartTick: 5,
		X: boxMin[0], Y: boxMin[1], Z: boxMin[2],
		TargetX: nanoPiece[0], TargetY: nanoPiece[1], TargetZ: nanoPiece[2],
		NanolatheGeometryKnown:  true,
		NanolatheTargetBoxKnown: true,
		NanolatheBoxAtSource:    true,
		NanolatheTargetMin:      boxMin,
		NanolatheTargetMax:      boxMax,
	}
	c := &Client{}
	cur := &frame.Frame{Tick: 5}
	cur.Effects = append(cur.Effects, e)
	c.tickNanolathe(cur)
	if len(c.nano.Records) != 1 {
		t.Fatalf("admitted %d records, want 1", len(c.nano.Records))
	}
	r := c.nano.Records[0]

	for a := 0; a < 3; a++ {
		if r.DstExtent[a] != 0 {
			t.Fatalf("axis %d: destination extent %v, want the degenerate nano piece", a, r.DstExtent[a])
		}
		if r.SrcExtent[a] <= 0 {
			t.Fatalf("axis %d: source extent %v, want the target's own box span", a, r.SrcExtent[a])
		}
		if r.SrcOrigin[a] < boxMin[a] || r.SrcOrigin[a] > boxMax[a] {
			t.Fatalf("axis %d: source origin %v outside the target box [%v, %v]",
				a, r.SrcOrigin[a], boxMin[a], boxMax[a])
		}
	}
	// The footprint asymmetry survives the narrowing, which is the axis-swap
	// guard: the Z footprint is twice the X footprint. The 4/11..7/11 span
	// truncates per axis [I3], so the doubling is exact to within one raw
	// 16.16 step, not to the bit.
	if d := 2*r.SrcExtent[0] - r.SrcExtent[2]; d < 0 || d > 1 {
		t.Fatalf("source extents X=%v Z=%v, want Z twice X for a %dx%d footprint",
			r.SrcExtent[0], r.SrcExtent[2], footX, footZ)
	}
	// And the spray leaves the victim: the destination is the builder's nano
	// piece four hundred world units away, not a second point inside the box.
	if r.DstOrigin != nanoPiece {
		t.Fatalf("destination %v, want the builder's nano piece %v", r.DstOrigin, nanoPiece)
	}
	if r.DstOrigin[0]-r.SrcOrigin[0] <= boxMax[0]-boxMin[0] {
		t.Fatalf("spray span %v does not exceed the target's own box width %v",
			r.DstOrigin[0]-r.SrcOrigin[0], boxMax[0]-boxMin[0])
	}
}

// The ordinary work direction — build, repair, help-build/assist, and a
// resurrection whose target has already resolved into a unit — is the mirror
// of the unit-reclaim/capture family above: the box is still the TARGET
// UNIT's own [02 R-CAT-01 §7], but it now sits at the DESTINATION end, with
// the builder's nano piece as the degenerate source
// (NanolatheBoxAtSource=false) [05 R-WORK-01 §8].
//
// The defect this locks: the forward producers published no box at all, so
// the client fell back to deriving the destination from the target's real
// model geometry — the wrong shape, since the published record is
// footprint-derived in X/Z, not the model's silhouette [02 R-CAT-01 §7]. A
// unit under repair or construction could then spray into a box shaped like
// its rendered mesh instead of its definition's bounding record.
func TestForwardNanoSpraysIntoTheTargetUnitsBox(t *testing.T) {
	// Same asymmetric footprint guard as the reversed-unit test: Z is twice X,
	// and the model top is nonzero.
	const footX, footZ, modelTop = 2, 4, 40
	def := &content.UnitDef{FootprintX: footX, FootprintZ: footZ, ModelTopFixed: modelTop << 16}

	target := &units.Unit{Def: def, X: nanoFixed(500), Y: nanoFixed(10), Z: nanoFixed(600)}
	boxMin, boxMax := target.NanolatheBox()
	nanoPiece := [3]numeric.Fixed{nanoFixed(900), nanoFixed(20), nanoFixed(600)}

	// Shaped exactly as the forward producers publish it: the box rides as the
	// TARGET box (destination), and NanolatheBoxAtSource is false.
	e := frame.EffectView{
		Kind: frame.EventKindNanolathe.String(), Strip: 6, StartTick: 5,
		X: nanoPiece[0], Y: nanoPiece[1], Z: nanoPiece[2],
		TargetX: target.X, TargetY: target.Y, TargetZ: target.Z,
		NanolatheGeometryKnown:  true,
		NanolatheTargetBoxKnown: true,
		NanolatheBoxAtSource:    false,
		NanolatheTargetMin:      boxMin,
		NanolatheTargetMax:      boxMax,
	}
	c := &Client{}
	cur := &frame.Frame{Tick: 5}
	cur.Effects = append(cur.Effects, e)
	c.tickNanolathe(cur)
	if len(c.nano.Records) != 1 {
		t.Fatalf("admitted %d records, want 1", len(c.nano.Records))
	}
	r := c.nano.Records[0]

	for a := 0; a < 3; a++ {
		if r.SrcExtent[a] != 0 {
			t.Fatalf("axis %d: source extent %v, want the degenerate nano piece", a, r.SrcExtent[a])
		}
		if r.DstExtent[a] <= 0 {
			t.Fatalf("axis %d: destination extent %v, want the target's own box span", a, r.DstExtent[a])
		}
		if r.DstOrigin[a] < boxMin[a] || r.DstOrigin[a] > boxMax[a] {
			t.Fatalf("axis %d: destination origin %v outside the target box [%v, %v]",
				a, r.DstOrigin[a], boxMin[a], boxMax[a])
		}
	}
	// The footprint asymmetry survives the narrowing: Z is twice X, to within
	// one raw 16.16 step of the truncation [I3].
	if d := 2*r.DstExtent[0] - r.DstExtent[2]; d < 0 || d > 1 {
		t.Fatalf("destination extents X=%v Z=%v, want Z twice X for a %dx%d footprint",
			r.DstExtent[0], r.DstExtent[2], footX, footZ)
	}
	// The source is the builder's nano piece, not a second point inside the
	// target's own box.
	if r.SrcOrigin != nanoPiece {
		t.Fatalf("source %v, want the builder's nano piece %v", r.SrcOrigin, nanoPiece)
	}
	if r.SrcOrigin[0]-r.DstOrigin[0] <= boxMax[0]-boxMin[0] {
		t.Fatalf("spray span %v does not exceed the target's own box width %v",
			r.SrcOrigin[0]-r.DstOrigin[0], boxMax[0]-boxMin[0])
	}
}
