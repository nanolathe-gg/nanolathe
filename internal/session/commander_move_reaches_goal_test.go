package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestCommanderMoveReachesGoalCell(t *testing.T) {
	// Read-only: this test only walks a commander with NewSkirmishWithFS and
	// never writes back into the catalog, so it shares the process-wide
	// compile [internal/testsupport/retailcat].
	cat, fs := retailcat.Shared(t)
	rng.SeedGlobal(100, 200)
	cfg := SkirmishConfig{MapName: "coast to coast", NumPlayers: 2}
	cfg.ApplyDefaults()
	sess, err := NewSkirmishWithFS(fs, cat, cfg)
	if err != nil {
		t.Skipf("skirmish: %v", err)
	}
	now := sess.Clock.ScaledAnchor
	for i := 0; i < 10 && sess.State != StateBattle; i++ {
		now++
		sess.Step(now)
	}
	if sess.State != StateBattle {
		t.Fatalf("not in battle: %v", sess.State)
	}
	var comHandle pool.Handle
	var comDef *content.UnitDef
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || u.Owner != 0 {
			continue
		}
		if u.Def != nil && u.Def.Commander {
			comHandle = u.Handle
			comDef = u.Def
			break
		}
	}
	if comHandle == 0 {
		t.Fatalf("no commander")
	}
	t.Logf("commander: %s footprint %dx%d sight %d maxvel %d turn %d accel %d brake %d",
		comDef.UnitName, comDef.FootprintX, comDef.FootprintZ, comDef.SightDistance,
		comDef.MaxVelocity, comDef.TurnRate, comDef.Acceleration, comDef.BrakeRate)
	sess.Movement.EnsureUnit(sess.Units.Unit(comHandle))
	comCellX := world.WorldToCell(sess.Units.Unit(comHandle).X)
	comCellZ := world.WorldToCell(sess.Units.Unit(comHandle).Z)
	var goalCell path.Cell
	found := false
	for cz := int32(0); cz < sess.World.CellH && !found; cz++ {
		for cx := int32(0); cx < sess.World.CellW && !found; cx++ {
			c := path.Cell{X: cx, Z: cz}
			dx := cx - comCellX
			dz := cz - comCellZ
			d2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			if d2 < 500 || d2 > 2500 {
				continue
			}
			if sess.Movement.IsGoalCellPassable(comHandle, c) {
				goalCell = c
				found = true
			}
		}
	}
	if !found {
		t.Skip("no passable goal found")
	}
	goalX := world.CellToWorld(goalCell.X) + numeric.Fixed(524288)
	goalZ := world.CellToWorld(goalCell.Z) + numeric.Fixed(524288)
	id := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(sess.Units.Unit(comHandle))
	node := orders.NewMoveNode(id, goalX, goalZ, sess.Clock.GlobalTick, comHandle, false)
	q.Push(id, node)
	startX, startZ := sess.Units.Unit(comHandle).X, sess.Units.Unit(comHandle).Z
	lastX, lastZ := startX, startZ
	lastMoveTick := int(now)
	frozenTicks := 0
	goalCellX := (int64(goalX) + 0x80000) >> 20
	goalCellZ := (int64(goalZ) + 0x80000) >> 20
	const maxTicks = 3000
	for i := 0; i < maxTicks; i++ {
		now++
		sess.Step(now)
		u := sess.Units.Unit(comHandle)
		r := sess.Movement.Routes[comHandle]
		moved := u.X != lastX || u.Z != lastZ
		if moved {
			lastMoveTick = int(now)
			lastX, lastZ = u.X, u.Z
			frozenTicks = 0
		} else {
			frozenTicks++
		}
		report := false
		if i < 30 || moved && frozenTicks == 0 && (now%120 == 0) {
			report = true
		}
		if frozenTicks == 30 || frozenTicks == 60 {
			report = true
		}
		if !report {
			continue
		}
		var tileX, tileZ int64
		if c := sess.Movement.Collisions[comHandle]; c != nil {
			tileX = int64(c.CachedAnchor.X)
			tileZ = int64(c.CachedAnchor.Z)
		} else {
			tileX = (int64(u.X) + 0x80000) >> 20
			tileZ = (int64(u.Z) + 0x80000) >> 20
		}
		tdx := tileX - goalCellX
		tdz := tileZ - goalCellZ
		arriveD2 := tdx*tdx + tdz*tdz
		routeDesc := "nil"
		if r != nil {
			routeDesc = fmt.Sprintf("active=%v count=%d status=%d pts=%v", r.Active, r.Count, r.Status, r.Points[:r.Count])
		}
		st := sess.Movement.Steers[comHandle]
		speed := int32(-1)
		blocked := false
		if st != nil {
			speed = st.Speed
		}
		if c := sess.Movement.Collisions[comHandle]; c != nil {
			blocked = c.Blocked
		}
		t.Logf("tick %d pos %d,%d tile %d,%d goal %d,%d arriveD2 %d speed %d blocked %v route %s",
			now, u.X.Raw(), u.Z.Raw(), tileX, tileZ, goalCellX, goalCellZ, arriveD2, speed, blocked, routeDesc)
	}
	u := sess.Units.Unit(comHandle)
	qf := orders.QueueForUnit(u)
	if qf == nil || qf.LenPrimary() == 0 {
		t.Logf("order completed at tick %d: pos %d,%d goal cell %d,%d", now, u.X.Raw(), u.Z.Raw(), goalCellX, goalCellZ)
		return
	}
	head := qf.Head()
	var tileX, tileZ int64
	if c := sess.Movement.Collisions[comHandle]; c != nil {
		tileX = int64(c.CachedAnchor.X)
		tileZ = int64(c.CachedAnchor.Z)
	}
	t.Logf("FROZEN: last move tick %d (tick now %d), pos %d,%d tile %d,%d goal cell %d,%d head %s state %d route %+v",
		lastMoveTick, now, u.X.Raw(), u.Z.Raw(), tileX, tileZ, goalCellX, goalCellZ,
		orders.DescriptorFor(head.ID).Name, head.MoveState, sess.Movement.Routes[comHandle])
	t.Fatalf("commander froze after moving to %d,%d (from %d,%d)", u.X.Raw(), u.Z.Raw(), startX.Raw(), startZ.Raw())
}
