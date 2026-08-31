//go:build retail

package main

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestBattleMoveAndBuildReachGoal runs the real battle input path end to end:
// select commander, right-click move far away, step the authoritative loop,
// and watch where the order takes the commander.
func TestBattleMoveAndBuildReachGoal(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		if h, err := os.UserHomeDir(); err == nil {
			root = h + "/TotalAnnihilation"
		}
	}
	opts := Options{Root: root, Map: "coast to coast", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Movement == nil || sess.World == nil {
		t.Fatal("movement/world services not bound")
	}
	cam := &camera.Camera{
		ViewW: 640, ViewH: 480,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	centerBattleStartCamera(sess, b.cam)

	var com *units.Unit
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != sess.LocalOwner {
			continue
		}
		if u.Def != nil && u.Def.Commander {
			com = u
			break
		}
	}
	if com == nil {
		t.Fatal("no local commander")
	}
	replaceSelectionForTest(t, b, com)
	t.Logf("commander at world %d,%d cell %d,%d", com.X.Raw(), com.Z.Raw(),
		world.WorldToCell(com.X), world.WorldToCell(com.Z))

	// Right-click a point 200px south-east of the commander.
	csx, csy := b.cam.WorldToScreen(com.X, com.Y, com.Z)
	csx -= camera.OriginX
	csy -= camera.OriginY
	tx, ty := csx+200, csy+200
	gx, gy, gz := b.cursorWorld(tx, ty)
	t.Logf("click at screen %d,%d -> world goal %d,%d,%d cell %d,%d", tx, ty,
		gx.Raw(), gy.Raw(), gz.Raw(), world.WorldToCell(gx), world.WorldToCell(gz))

	b.orderSelected(2, tx, ty, false)
	now := sess.Clock.ScaledAnchor
	var lastX, lastZ numeric.Fixed
	lastX, lastZ = com.X, com.Z
	lastMove := now
	for i := 0; i < 2000; i++ {
		now++
		sess.Step(now)
		u := sess.Units.Unit(com.Handle)
		if u == nil {
			break
		}
		if u.X != lastX || u.Z != lastZ {
			lastMove = now
			lastX, lastZ = u.X, u.Z
		}
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			t.Logf("move completed at tick %d: pos %d,%d (goal %d,%d)", now, u.X.Raw(), u.Z.Raw(), gx.Raw(), gz.Raw())
			// Phase 2: mobile build — the same walk machinery must carry the
			// commander to the build site (previously the walk order completed
			// far short of the site and build orders froze).
			menu, ok := cat.BuildMenus[content.CanonicalKey(com.Def.UnitName)]
			if !ok || menu == nil || len(menu.Buttons) == 0 {
				t.Logf("no commander build menu; skipping build phase")
				return
			}
			product := menu.Buttons[0]
			pdef, ok := cat.Unit(product)
			if !ok {
				t.Logf("no def for product %s; skipping build phase", product)
				return
			}
			// Pick a valid placement site 12-20 cells away so the builder must
			// walk (beyond nano range) to reach it. Placement is validated the
			// same way the production dispatch validates it.
			comCellX := world.WorldToCell(com.X)
			comCellZ := world.WorldToCell(com.Z)
			footX, footZ := footprintCellsForCatalog(b.cat, pdef)
			var site path.Cell
			found := false
			for cz := int32(0); cz < sess.World.CellH && !found; cz++ {
				for cx := int32(0); cx < sess.World.CellW && !found; cx++ {
					c := path.Cell{X: cx, Z: cz}
					dx := cx - comCellX
					dz := cz - comCellZ
					d2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
					if d2 < 144 || d2 > 400 {
						continue
					}
					if !sess.Movement.IsGoalCellPassable(com.Handle, c) {
						continue
					}
					if _, err := b.checkProductPlacement(c.X-footX/2, c.Z-footZ/2, pdef, footX, footZ, uint16(com.Handle)); err == nil {
						site = c
						found = true
					}
				}
			}
			if !found {
				t.Logf("no valid build site found; skipping build phase")
				return
			}
			bx := world.CellToWorld(site.X) + numeric.Fixed(524288)
			bz := world.CellToWorld(site.Z) + numeric.Fixed(524288)
			_ = pdef
			validated, err := b.checkProductPlacement(site.X-footX/2, site.Z-footZ/2, pdef, footX, footZ, uint16(com.Handle))
			if err != nil {
				t.Fatalf("revalidate build site: %v", err)
			}
			if err := b.DispatchMobileBuild(product, bx, numeric.Fixed(int64(validated.SiteHeight)<<16), bz, false); err != nil {
				t.Fatalf("dispatch build: %v", err)
			}
			walked := false
			now++
			for i := 0; i < 2000; i++ {
				now++
				sess.Step(now)
				ub := sess.Units.Unit(com.Handle)
				if ub == nil {
					t.Fatalf("commander died during build walk")
				}
				if !walked && (ub.X != lastX || ub.Z != lastZ) {
					walked = true
				}
				qb := orders.QueueForUnit(ub)
				if qb == nil || qb.LenPrimary() == 0 {
					t.Logf("build order completed at tick %d: pos %d,%d site %d,%d walked=%v", now, ub.X.Raw(), ub.Z.Raw(), bx.Raw(), bz.Raw(), walked)
					if !walked {
						t.Fatalf("build walk did not move the builder to the site")
					}
					return
				}
			}
			ub := sess.Units.Unit(com.Handle)
			hb := orders.QueueForUnit(ub).Head()
			t.Fatalf("builder froze: pos %d,%d site %d,%d head %s", ub.X.Raw(), ub.Z.Raw(), bx.Raw(), bz.Raw(), orders.DescriptorFor(hb.ID).Name)
		}
	}
	u := sess.Units.Unit(com.Handle)
	head := orders.QueueForUnit(u).Head()
	var tileX, tileZ int64
	if c := sess.Movement.Collisions[com.Handle]; c != nil {
		tileX = int64(c.CachedAnchor.X)
		tileZ = int64(c.CachedAnchor.Z)
	}
	t.Logf("FROZEN after %d ticks: last move %d pos %d,%d tile %d,%d goal world %d,%d cell %d,%d head %s",
		now, lastMove, u.X.Raw(), u.Z.Raw(), tileX, tileZ, gx.Raw(), gz.Raw(),
		world.WorldToCell(gx), world.WorldToCell(gz), orders.DescriptorFor(head.ID).Name)
	r := sess.Movement.Routes[com.Handle]
	if r != nil {
		t.Logf("route active=%v count=%d status=%d pts=%v", r.Active, r.Count, r.Status, r.Points[:r.Count])
	}
	t.Fatalf("commander did not reach goal: pos %d,%d", u.X.Raw(), u.Z.Raw())
}
