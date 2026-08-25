package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestPathFailureRecovery_ImpasseGoalRecovers(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if h := os.Getenv("HOME"); h != "" {
			root = filepath.Join(h, "TotalAnnihilation")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "totala1.hpi")); err != nil {
		t.Skip("retail assets not present at ~/TotalAnnihilation — skip")
	}
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail %q: %v", root, err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Skipf("catalog compile: %v", err)
	}
	rng.SeedGlobal(100, 200)
	cfg := SkirmishConfig{MapName: "coast to coast", NumPlayers: 2}
	cfg.ApplyDefaults()
	sess, err := NewSkirmishWithFS(fs, cat, cfg)
	if err != nil {
		cfg2 := SkirmishConfig{MapName: "ashap plateau", NumPlayers: 2}
		cfg2.ApplyDefaults()
		sess, err = NewSkirmishWithFS(fs, cat, cfg2)
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
	sess.SetTraceEnabled(true)
	sess.ClearTrace()
	failed := false
	popped := false
	for i := 0; i < 200; i++ {
		now++
		sess.Step(now)
		evs := sess.TraceEvents()
		for _, ev := range evs {
			if ev.Kind == TracePathFailed && ev.Handle == comHandle {
				failed = true
			}
		}
		q2 := orders.QueueForUnit(sess.Units.Unit(comHandle))
		if q2 == nil || q2.LenPrimary() == 0 {
			popped = true
			break
		}
		if head := q2.Head(); head != nil && head.MoveState == orders.MoveBlocked {
			popped = true
			break
		}
		route := sess.Movement.Routes[comHandle]
		if route != nil && route.Active {
			t.Fatalf("bad goal should not produce active route, got active count %d", route.Count)
		}
	}
	if !popped {
		q2 := orders.QueueForUnit(sess.Units.Unit(comHandle))
		headInfo := "nil"
		if q2 != nil && q2.LenPrimary() > 0 {
			if h := q2.Head(); h != nil {
				headInfo = orders.DescriptorFor(h.ID).Name
			}
		}
		t.Fatalf("bad order not popped within 200 ticks head %s failedTrace %v", headInfo, failed)
	}
	sess.ClearTrace()
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
