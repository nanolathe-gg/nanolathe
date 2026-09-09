package airdiag

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestAirSecondMoveFlies is the control for TestAirMoveTickWindow. The takeoff
// preamble builds its cruisealt/2 climb marker only when the committed mover
// mode is 1 (grounded) [04 R-AIR-01 §6] step 4. A second Move issued to an
// already airborne aircraft therefore has no climb marker to be satisfied by,
// so the movement-side executor reaches its own phase 1 and installs a marker
// at the ordered destination. If this one flies while the first does not, the
// defect is the climb marker completing the first record, not the flight
// integrator.
func TestAirSecondMoveFlies(t *testing.T) {
	h := newHarness(t)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)

	// First move: it will complete on the climb marker without moving.
	first := u.X.Add(world.CellToWorld(40))
	if err := h.Order(u, 2, 0, first, goalHeight(h, first, u.Z), u.Z); err != nil {
		t.Fatalf("first move: %v", err)
	}
	h.Step(70)
	afterFirst := h.Observe(u)
	t.Logf("after first move: %s", afterFirst.Format())

	// Second move, now airborne.
	goalX := u.X.Add(world.CellToWorld(40))
	goalZ := u.Z
	if err := h.Order(u, 2, 0, goalX, goalHeight(h, goalX, goalZ), goalZ); err != nil {
		t.Fatalf("second move: %v", err)
	}
	startX, startZ := u.X, u.Z
	rows := h.Trace(u, 200)
	dump(t, rows, 10)

	last := rows[len(rows)-1]
	t.Logf("second move: start=(%.2f,%.2f) goal=(%.2f,%.2f) end=(%.2f,%.2f)",
		fx(startX), fx(startZ), fx(goalX), fx(goalZ), fx(last.X), fx(last.Z))
	if last.X == startX && last.Z == startZ {
		t.Errorf("the second move did not move the aircraft either")
	}
}

// TestAirSecondMoveTurns is the turning isolation, run on the second move so
// the climb-marker completion of the first is out of the way. The goal is
// placed behind the aircraft, so the command heading must swing about half a
// circle and the mover's heading must follow it at TurnRate per tick
// [04 §10.1] C30.
func TestAirSecondMoveTurns(t *testing.T) {
	h := newHarness(t)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)

	first := u.X.Add(world.CellToWorld(10))
	if err := h.Order(u, 2, 0, first, goalHeight(h, first, u.Z), u.Z); err != nil {
		t.Fatalf("first move: %v", err)
	}
	h.Step(70)

	startHeading := u.Move.Heading

	// The stored heading word is the reverse bearing: the mover's desired
	// heading is angleOf(unitX-goalX, unitZ-goalZ) and the velocity derived
	// from a heading is negated, so a unit at heading H travels toward
	// -(sin H, cos H) [04 R-MOV-01 §2]. The spawn heading 0x8000 therefore
	// travels toward +Z, and a goal at -Z demands the half-circle turn to 0.
	goalX := u.X
	goalZ := u.Z.Add(world.CellToWorld(-40))
	if err := h.Order(u, 2, 0, goalX, goalHeight(h, goalX, goalZ), goalZ); err != nil {
		t.Fatalf("second move: %v", err)
	}
	rows := h.Trace(u, 150)
	dump(t, rows, 5)

	var maxDelta int
	var sawCommandChange bool
	for _, r := range rows {
		d := int(int16(r.Heading - startHeading))
		if d < 0 {
			d = -d
		}
		if d > maxDelta {
			maxDelta = d
		}
		if r.CommandHead != startHeading {
			sawCommandChange = true
		}
	}
	t.Logf("start heading=%d turnrate=%d: max |heading delta|=%d, command heading ever changed=%v",
		startHeading, u.Def.TurnRate, maxDelta, sawCommandChange)
	if maxDelta == 0 {
		t.Errorf("the mover's heading never changed while the goal was behind it")
	}
}

func goalHeight(h *Harness, x, z numeric.Fixed) numeric.Fixed {
	if h.Session.World == nil {
		return 0
	}
	y := h.Session.World.HeightAt(x, z)
	if y == numeric.Fixed(-1) {
		return 0
	}
	return y
}
