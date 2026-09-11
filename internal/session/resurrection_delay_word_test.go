package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// A wrapped worker word still owns a nonzero wait before consuming the corpse;
// the live session must use the same boundary as the delay helper
// [05 R-WORK-01 §7 "the delay"].
func TestResurrectionWrappedWorkerRetainsCorpseDuringDelay(t *testing.T) {
	s := wreckOrientationSession(t)
	corpse := s.Features.PlaceAt(12, 12, s.Catalog.Features["wreckvictim_dead"])
	if corpse == nil {
		t.Fatal("corpse placement refused")
	}
	def := s.Catalog.Units["wreckcon"]
	def.WorkerTime = -1
	s.Catalog.Units["wreckvictim"].BuildTime = 30000
	h, err := s.Units.Create(def, 0, world.CellToWorld(11), 0, world.CellToWorld(12))
	if err != nil {
		t.Fatal(err)
	}
	builder := s.Units.Unit(h)
	s.Movement.EnsureUnit(builder)
	builder.InBuildStance = true
	q := orders.QueueForUnit(builder)
	s.bindExistingOrderQueue(builder)
	q.Push(orders.Lookup("Resurrect"), orders.Node{Owner: h, Phase: 3, GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(12)})
	n := q.Primary()[0]
	q.Pump(builder, 100)
	// Word 65535 yields quantum 2184 and delay four; the first phase-4 visit
	// decrements it and schedules the next tick without consuming the corpse.
	if s.Features.InstanceAt(12, 12) != corpse {
		t.Fatal("resurrection consumed the corpse before its wait completed")
	}
	if n.Phase != 4 || n.Param2 != 3 || n.Deadline != 101 {
		t.Fatalf("phase/delay/deadline = %d/%d/%d, want 4/3/101", n.Phase, n.Param2, n.Deadline)
	}
}
