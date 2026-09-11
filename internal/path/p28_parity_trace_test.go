package path

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func TestCurrentRequestAvailableWithoutHistoricalTrace(t *testing.T) {
	s := newTestScheduler(func(Request, int32, int) WorkResult {
		return WorkResult{Pops: 100}
	}, nil)
	s.SetUnitLimit(1)
	s.Submit(Request{Unit: 1, Goal: PointGoal(Cell{X: 9}, 0), Activation: 7})
	s.Tick(100)
	r := s.CurrentRequest(1)
	if r == nil || r.Activation != 7 || s.TraceEnabled() || s.CurrentRequest(2) != nil {
		t.Fatalf("current request without tracing=%+v", r)
	}
	r.Start.X = 123
	if s.CurrentRequest(1).Start.X == 123 {
		t.Fatal("request copy aliases scheduler record")
	}
	if pending, history := s.TraceFor(1); pending != nil || history != nil {
		t.Fatal("current request enabled history")
	}
}

func TestP28TraceIsOptInAndRepeatReadPure(t *testing.T) {
	search := func(r Request, _ int32, _ int) WorkResult {
		return WorkResult{Points: []Point{{X: 3, Z: 4}}, Done: true}
	}
	s := newTestScheduler(search, nil)
	s.SetUnitLimit(1)
	if s.TraceEnabled() {
		t.Fatal("scheduler tracing enabled by default")
	}
	s.Submit(Request{Unit: pool.Handle(1), Player: 0, Start: Cell{X: 1, Z: 2}, Goal: PointGoal(Cell{X: 3, Z: 4}, 0), Activation: 9})
	s.Tick(1)
	if pending, result := s.TraceFor(1); pending != nil || result != nil {
		t.Fatal("unconfigured scheduler exposed trace state")
	}
	s.EnableTrace()
	s.Submit(Request{Unit: pool.Handle(1), Player: 0, Start: Cell{X: 1, Z: 2}, Goal: PointGoal(Cell{X: 3, Z: 4}, 0), Activation: 10})
	s.Tick(2)
	pending, first := s.TraceFor(1)
	if pending != nil || first == nil || !first.Done || len(first.Points) != 1 {
		t.Fatalf("trace=%+v pending=%+v", first, pending)
	}
	if first.Request.Goal.Kind != 1 || first.Request.Goal.Center != (Cell{X: 3, Z: 4}) || first.Request.Activation != 10 {
		t.Fatalf("request trace was not normalized: %+v", first.Request)
	}
	secondPending, second := s.TraceFor(1)
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(pending, secondPending) {
		t.Fatal("repeated trace read changed the copied result")
	}
	first.Points[0].X = 99
	_, third := s.TraceFor(1)
	if third.Points[0].X == 99 {
		t.Fatal("trace result aliases scheduler storage")
	}
}

func TestP28TraceLimitAndReset(t *testing.T) {
	s := newTestScheduler(func(r Request, _ int32, _ int) WorkResult {
		return WorkResult{Points: []Point{{X: int32(r.Unit)}}, Done: true}
	}, nil)
	s.SetUnitLimit(1)
	s.EnableTrace()
	s.SetTraceLimit(1)
	s.Submit(Request{Unit: 1, Player: 0, Goal: PointGoal(Cell{}, 0)})
	s.Submit(Request{Unit: 2, Player: 0, Goal: PointGoal(Cell{}, 0)})
	s.Tick(1)
	if !s.TraceDropped() {
		t.Fatal("trace cap did not report dropped record")
	}
	s.ResetTrace()
	if s.TraceDropped() {
		t.Fatal("reset retained trace drop state")
	}
	if _, result := s.TraceFor(1); result != nil {
		t.Fatal("reset retained trace record")
	}
}

func TestP28TraceCaptureDoesNotChangeDispatchOrder(t *testing.T) {
	run := func(enabled bool) []pool.Handle {
		var order []pool.Handle
		s := newTestScheduler(func(r Request, _ int32, _ int) WorkResult {
			return WorkResult{Points: []Point{{X: int32(r.Unit)}}, Done: true}
		}, func(r Request, _ []Point, _ Status) {
			order = append(order, r.Unit)
		})
		s.SetUnitLimit(1)
		if enabled {
			s.EnableTrace()
		}
		for _, unit := range []pool.Handle{7, 2, 5} {
			s.Submit(Request{Unit: unit, Player: 0, Goal: PointGoal(Cell{}, 0)})
		}
		s.Tick(1)
		return order
	}
	without, with := run(false), run(true)
	if !reflect.DeepEqual(without, with) {
		t.Fatalf("trace changed dispatch order: disabled=%v enabled=%v", without, with)
	}
}
