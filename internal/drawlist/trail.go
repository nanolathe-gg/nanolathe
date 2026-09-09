package drawlist

// Trails is one frame's batch of Enhanced ground marks: footprint ovals and
// track segments that darken the terrain under them and fade with age
// (docs/DESIGN_GPU_RENDERER.md §15). Retail leaves no marks, so this is a
// Nanolathe presentation feature: the Original executor never draws it, and
// the family rides the optional TrailSink hook rather than the Sink contract
// every executor must satisfy.
//
// The whole frame's marks travel as one record because every mark is a
// multiply of the destination, and multiplies commute: an executor may draw
// the batch as one command whatever the marks overlap, instead of chaining
// abutting segments through successive phases.
type Trails struct {
	Marks []Trail
}

// Trail is one mark. Geometry is in screen pixels at the recorded view scale;
// the two half-vectors are in 1/256 pixel so a short mark still carries its
// direction. The recorder supplies both so an executor needs no normalisation.
type Trail struct {
	// X, Y is the mark's centre.
	X, Y int32
	// AxisX, AxisY is the half-length vector along the unit's travel.
	AxisX, AxisY int32
	// CrossX, CrossY is the half-width vector across it.
	CrossX, CrossY int32
	Shape          TrailShape
	// Strength is the share of the terrain colour the mark removes at its
	// centre, 0..255 for 0..1, with the age fade already applied.
	Strength uint8
}

// TrailShape selects the coverage the executor evaluates inside the mark's
// quad.
type TrailShape uint8

const (
	// TrailFootprint is an oval with a soft edge all round.
	TrailFootprint TrailShape = iota
	// TrailTrack is a segment with hard ends, so consecutive segments join,
	// and soft sides.
	TrailTrack
)

// TrailSink is the optional executor hook for the trail family. Replay calls
// it on a Sink that implements it and skips the record otherwise.
type TrailSink interface {
	Trails(Trails)
}

// RecordTrails appends one trail batch in record order. The marks slice is
// borrowed until Reset; Clone copies it.
func (l *List) RecordTrails(c Trails) {
	l.order = append(l.order, tag{familyTrails, len(l.trails)})
	l.trails = append(l.trails, c)
}
