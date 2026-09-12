package drawlist

import "testing"

type surfaceWakeCapture struct {
	Sink
	marks []SurfaceWake
}

func (s *surfaceWakeCapture) SurfaceWakes(v SurfaceWakes) { s.marks = append(s.marks, v.Marks...) }

func TestSurfaceWakeCloneOwnsBorrowedMarks(t *testing.T) {
	marks := []SurfaceWake{{X: 7, Alpha: .2}}
	var list List
	list.RecordSurfaceWakes(SurfaceWakes{Marks: marks})
	clone := list.Clone()
	marks[0].X = 99
	list.Reset()
	var got surfaceWakeCapture
	clone.Replay(&got)
	if len(got.marks) != 1 || got.marks[0].X != 7 {
		t.Fatalf("clone borrowed later marks: %+v", got.marks)
	}
	var empty surfaceWakeCapture
	list.Replay(&empty)
	if len(empty.marks) != 0 {
		t.Fatal("reset retained live wake records")
	}
}
