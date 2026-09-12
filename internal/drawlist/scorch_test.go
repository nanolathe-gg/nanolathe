package drawlist

import "testing"

type scorchCapture struct {
	Sink
	marks []ScorchMark
}

func (s *scorchCapture) ScorchMarks(v ScorchMarks) { s.marks = append(s.marks, v.Marks...) }

func TestScorchMarkCloneOwnsBorrowedMarks(t *testing.T) {
	marks := []ScorchMark{{X: 7, Age: 90}}
	var list List
	list.RecordScorchMarks(ScorchMarks{Marks: marks})
	clone := list.Clone()
	marks[0].X = 99
	list.Reset()
	var got scorchCapture
	clone.Replay(&got)
	if len(got.marks) != 1 || got.marks[0].X != 7 {
		t.Fatalf("clone borrowed later marks: %+v", got.marks)
	}
	var empty scorchCapture
	list.Replay(&empty)
	if len(empty.marks) != 0 {
		t.Fatal("reset retained live wake records")
	}
}

func TestScorchOptionalClassicSink(t *testing.T) {
	var list List
	list.RecordScorchMarks(ScorchMarks{Marks: []ScorchMark{{Radius: 12}}})
	// A sink without the optional extension ignores the command entirely.
	list.Replay(struct{ Sink }{})
}
