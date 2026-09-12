package drawlist

// These are authored presentation choices (GPU design §29), not retail behavior.
const (
	ScorchFadeStartTicks = 90
	ScorchLifeTicks      = 450
	ScorchMarkLimit      = 256
)

// ScorchMark carries a dry ground mark in recording pixels and committed tick
// age plus the optional presentation fraction. Replay owns no live history.
type ScorchMark struct {
	X, Y, Radius, Age float32
	Variant           uint32
}
type ScorchMarks struct{ Marks []ScorchMark }
type ScorchSink interface{ ScorchMarks(ScorchMarks) }

// RecordScorchMarks borrows the marks until Reset. Clone owns its copy.
func (l *List) RecordScorchMarks(c ScorchMarks) {
	l.order = append(l.order, tag{familyScorchMarks, len(l.scorchMarks)})
	l.scorchMarks = append(l.scorchMarks, c)
}
