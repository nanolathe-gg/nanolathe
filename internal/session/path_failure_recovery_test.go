package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestPathFailureRecovery_ImpasseGoalKeepsPollingUntilReplaced is read-only
// against the catalog — it only feeds it to NewSkirmishWithProgress, it never
// writes into it, so it shares the process-wide compile
// [internal/testsupport/retailcat].
func TestPathFailureRecovery_ImpasseGoalKeepsPollingUntilReplaced(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	rng.SeedGlobal(100, 200)
	cfg := SkirmishConfig{MapName: "coast to coast", NumPlayers: 2}
	cfg.ApplyDefaults()
	sess, err := NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		cfg2 := SkirmishConfig{MapName: "ashap plateau", NumPlayers: 2}
		cfg2.ApplyDefaults()
		sess, err = NewSkirmishWithProgress(fs, cat, cfg2, nil)
		if err != nil {
			t.Skipf("NewSkirmish coast/ashap: %v", err)
		}
	}
	if err := sess.ValidateComposition(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	now := sess.Clock.ScaledAnchor
	for i := 0; i < 10 && sess.State != StateBattle; i++ {
		now++
		sess.Step(now)
	}
	if sess.State != StateBattle {
		t.Fatalf("not in battle after 10 steps state %v", sess.State)
	}
	var comHandle pool.Handle
	var comX, comZ numeric.Fixed
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if u.Owner != 0 {
			continue
		}
		if u.Def != nil && u.Def.Commander {
			comHandle = u.Handle
			comX = u.X
			comZ = u.Z
			break
		}
	}
	if comHandle == 0 {
		t.Fatalf("no commander for player 0")
	}
	if sess.Movement == nil || sess.World == nil {
		t.Fatalf("movement/world nil")
	}
	sess.Movement.EnsureUnit(sess.Units.Unit(comHandle))
	comCellX := world.WorldToCell(comX)
	comCellZ := world.WorldToCell(comZ)
	var waterCell path.Cell
	foundWater := false
	for cz := int32(0); cz < sess.World.CellH && !foundWater; cz++ {
		for cx := int32(0); cx < sess.World.CellW && !foundWater; cx++ {
			cell := path.Cell{X: cx, Z: cz}
			if sess.Movement.IsGoalCellPassable(comHandle, cell) {
				continue
			}
			dx := cx - comCellX
			dz := cz - comCellZ
			d2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			if d2 < 36 || d2 > 10000 {
				continue
			}
			waterCell = cell
			foundWater = true
		}
	}
	if !foundWater {
		t.Skip("no impassable cell found for profile — skip")
	}
	goalX := world.CellToWorld(waterCell.X) + numeric.Fixed(524288)
	goalZ := world.CellToWorld(waterCell.Z) + numeric.Fixed(524288)
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("Move_Ground not found")
	}
	q := orders.QueueForUnit(sess.Units.Unit(comHandle))
	if q == nil {
		t.Fatalf("queue nil")
	}
	node := orders.NewMoveNode(id, goalX, goalZ, sess.Clock.GlobalTick, comHandle, false)
	q.Push(id, node)
	badHead := q.Head()
	for i := 0; i < 200; i++ {
		now++
		sess.Step(now)
		q2 := orders.QueueForUnit(sess.Units.Unit(comHandle))
		if q2 == nil || q2.Head() != badHead {
			t.Fatal("failed publication consumed or replaced the move order")
		}
		route := sess.Movement.Routes[comHandle]
		if route != nil && route.Active {
			t.Fatalf("bad goal should not produce active route, got active count %d", route.Count)
		}
	}
	if !sess.Movement.HasPathFailure(comHandle) {
		t.Fatal("impassable goal never recorded a rejected publication")
	}
	// A later user replacement, not an invented retry ceiling, ends the failed
	// move and allows a new reachable command [04 R-MOV-01 §7].
	q.CancelAll()
	sess.Movement.DeactivateMove(comHandle)
	beforeX := sess.Units.Unit(comHandle).X
	beforeZ := sess.Units.Unit(comHandle).Z
	var landCell path.Cell
	foundLand := false
	for cz := int32(0); cz < sess.World.CellH && !foundLand; cz++ {
		for cx := int32(0); cx < sess.World.CellW && !foundLand; cx++ {
			cell := path.Cell{X: cx, Z: cz}
			if !sess.Movement.IsGoalCellPassable(comHandle, cell) {
				continue
			}
			dx := cx - comCellX
			dz := cz - comCellZ
			d2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			if d2 < 25 || d2 > 2500 {
				continue
			}
			if cell == waterCell {
				continue
			}
			landCell = cell
			foundLand = true
		}
	}
	if !foundLand {
		t.Skip("no passable land cell found")
	}
	goalX2 := world.CellToWorld(landCell.X) + numeric.Fixed(524288)
	goalZ2 := world.CellToWorld(landCell.Z) + numeric.Fixed(524288)
	q3 := orders.QueueForUnit(sess.Units.Unit(comHandle))
	node2 := orders.NewMoveNode(id, goalX2, goalZ2, sess.Clock.GlobalTick, comHandle, false)
	q3.Push(id, node2)
	routeActiveSeen := false
	moved := false
	for i := 0; i < 200; i++ {
		now++
		sess.Step(now)
		if r := sess.Movement.Routes[comHandle]; r != nil && r.Active && r.Count > 0 {
			routeActiveSeen = true
		}
		u2 := sess.Units.Unit(comHandle)
		if u2 != nil && (u2.X != beforeX || u2.Z != beforeZ) {
			moved = true
		}
		if routeActiveSeen && moved {
			break
		}
	}
	if !routeActiveSeen {
		t.Fatalf("follow-up good order did not produce Route.Active=true")
	}
	if !moved {
		t.Fatalf("follow-up good order produced route but position did not change before %v after %v", beforeX, sess.Units.Unit(comHandle).X)
	}
	_ = path.StatusRejected
}
