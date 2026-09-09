package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
)

func TestP28CollisionHistoryOnlyCommitsWhenEnabled(t *testing.T) {
	s := NewSystem(nil, Profile{}, nil)
	if got := s.CollisionHistory(1); got != nil {
		t.Fatalf("disabled trace returned %v", got)
	}
	s.EnableParityTrace()
	if s.Scheduler == nil {
		t.Fatal("movement scheduler unexpectedly nil")
	}
	if pending, result := s.Scheduler.TraceFor(1); pending != nil || result != nil {
		t.Fatalf("empty scheduler exposed trace: %v %v", pending, result)
	}
	// The scheduler trace surface is enabled by the same opt-in boundary; no
	// read is allowed to synthesize a request or collision record.
	s.pathProvider.Submit(path.Request{Unit: 1, Player: 0, Start: path.Cell{}, Goal: path.PointGoal(path.Cell{X: 1}, 0)})
	if pending, result := s.Scheduler.TraceFor(1); pending != nil || result != nil || !s.pathProvider.HasRequest(1) {
		t.Fatalf("provider request must remain outside scheduler trace: pending=%v result=%v provider=%v", pending, result, s.pathProvider.HasRequest(1))
	}
}

func TestP28CollisionTraceLimitAndReset(t *testing.T) {
	s := NewSystem(nil, Profile{}, nil)
	s.EnableParityTrace()
	s.collisionHistory = append(s.collisionHistory,
		collisionHistoryEntry{Tick: 1, Slot: 1},
		collisionHistoryEntry{Tick: 2, Slot: 2})
	s.SetParityTraceLimit(1)
	if len(s.collisionHistory) != 1 || s.collisionHistory[0].Tick != 2 {
		t.Fatalf("collision history was not bounded: %+v", s.collisionHistory)
	}
	s.ResetParityTrace()
	if len(s.collisionHistory) != 0 || s.ParityTraceDropped() {
		t.Fatalf("collision reset retained diagnostics: history=%v dropped=%v", s.collisionHistory, s.ParityTraceDropped())
	}
}
