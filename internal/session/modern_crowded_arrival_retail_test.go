package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Exercise the captured rally crowd on its actual terrain through ordinary
// point-order installation, scheduled path rejection, movement and completion.
func TestRetailCapturedCrowdedRallyCompletesOnlyInModern(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s, _, _, _ := capturedDangerCrowd(t)
			s.SetGameplay(mode)
			// These five fresh-map trees are absent from the captured local
			// feature list. Restore their cleared collision footprints; decorative
			// ground marks in the capture do not block movement. Do not start new
			// removal animations or guess destruction successors.
			for _, at := range [][2]int32{{92, 151}, {95, 151}, {92, 154}, {95, 154}, {94, 155}} {
				cell := s.World.PlotAt(at[0], at[1])
				def, ok := s.World.FeatureDefAt(cell.Feature())
				if !ok || !def.Blocking || def.FootprintX != 1 || def.FootprintZ != 1 {
					t.Fatal("unexpected authored feature at captured clear cell")
				}
				cell.SetFeature(world.PlotFeatureNone)
				cell.SetOccupied(false)
				s.Features.ReleaseRemovedAt(int(at[0]), int(at[1]))
				s.World.NoteFootprintRestamp(at[0], at[1], 1, 1)
				s.World.BumpStaticObstacleRevision()
			}
			var mover *units.Unit
			s.Units.ForEachPlayerSliceLive(0, func(u *units.Unit) {
				if u.X == numeric.Fixed(98561210) && u.Z == numeric.Fixed(161477734) {
					mover = u // Flash137 in the diagnostic capture
				}
			})
			if mover == nil {
				t.Fatal("captured rally mover missing")
			}
			if !s.Movement.ProfileFor(mover.Handle).IsPassableFootprint(s.World, 91, 151) {
				t.Fatalf("captured goal not statically clear after feature restoration: %+v", s.Features.InstanceAt(92, 151))
			}
			q := orders.QueueOfUnit(mover)
			q.CancelAll()
			id := orders.Lookup("Move_Ground")
			q.Push(id, orders.NewNodeForOrder(id, 0, numeric.FixedFromInt(1476), numeric.FixedFromInt(85), numeric.FixedFromInt(2425), s.Clock.GlobalTick, mover.Handle, false))
			n := q.Head()
			x, z := mover.X, mover.Z
			stepRetail(s, 240)
			if mode == gameplay.Strict31 {
				if q.Head() != n || n.ID != id {
					t.Fatalf("Strict changed crowded move completion: %+v", q.Head())
				}
				return
			}
			if head := q.Head(); head == nil || head.ID != orders.Lookup("Standby") {
				cx, cz, blocked := s.Movement.CrowdedMoveBlocked(mover, n)
				t.Logf("final crowd query %d,%d=%v collision%+v", cx, cz, blocked, s.Movement.Collisions[mover.Handle])
				t.Fatalf("crowded rally did not become idle: %+v", head)
			}
			if s.Movement.HasPathRequest(mover.Handle) || s.Movement.HasGroundGoal(mover.Handle, n) {
				t.Fatal("completed rally retained path request or movement goal")
			}
			if dx, dz := (mover.X - x).Int(), (mover.Z - z).Int(); dx*dx+dz*dz > 16*16 {
				t.Fatalf("arrival displaced captured mover through crowd: (%d,%d)", dx, dz)
			}
		})
	}
}
