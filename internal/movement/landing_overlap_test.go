package movement

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Landing checks both occupant words, ignoring self in either plane. The
// mapping early accept still precedes that walk [04 R-AIR-01 §6a].
func TestLandingOccupancyChecksBothPlanes(t *testing.T) {
	for _, plane := range []Plane{PlaneGround, PlaneAir} {
		for _, self := range []bool{false, true} {
			s, _, u := airFixture(t)
			c := handleRow(s.Collisions, u.Handle)
			s.Grid.ClearPlane(PlaneGround, c.CachedAnchor, c.FootPrintX, c.FootPrintZ, c.ID)
			id := c.ID
			if !self {
				id++
			}
			s.Grid.StampPlane(plane, c.CachedAnchor, c.FootPrintX, c.FootPrintZ, id)
			mapped := uint16(1 << u.Owner)
			orders.QueueForUnit(u).Binding().World = &orders.WorldQueryAdapter{
				MappingWord: func(_, _ int32) (uint16, bool) { return mapped, true },
			}
			if got := s.landable(u, u.X, u.Z); got != self {
				t.Errorf("plane=%d self=%v: mapped landing=%v, want %v", plane, self, got, self)
			}
			mapped = 0
			if !s.landable(u, u.X, u.Z) {
				t.Errorf("plane=%d self=%v: unmapped landing lost its early accept", plane, self)
			}
		}
	}
}

// An overlapping airborne pair must search apart before descending. Otherwise
// the later touchdown is refused with request mode 1 and accepted mirror 2,
// and repair patrol sees an airborne mirror while the integrator stays stopped
// [04 R-AIR-01 §6a][04 R-COLL-01 §1][04 R-ORD-01 §7][04 §10.1].
func TestLandingPairCanResumeConstructionPatrol(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, overlap := range []bool{false, true} {
			t.Run(fmt.Sprintf("modern=%v/overlap=%v", modern, overlap), func(t *testing.T) {
				s, w, first := airFixture(t)
				first.Def.Builder, first.Def.CanReclamate = true, true
				first.Y += numeric.Fixed(first.Def.CruiseAlt) << 16
				secondX := first.X + world.CellToWorld(4)
				if overlap {
					secondX = first.X
				}
				h, err := w.Create(first.Def, first.Owner, secondX, first.Y, first.Z)
				if err != nil {
					t.Fatal(err)
				}
				pair := []*units.Unit{first, w.Unit(h)}
				binding := orders.QueueForUnit(first).Binding()
				binding.Movement = &orders.MovementGoalAdapter{InstallAir: s.InstallAirGoal, Release: s.ReleaseGoalPayload}
				if modern {
					s.Rules = &ModernRules{}
					binding.Rules = &orders.ModernRules{}
				}
				for _, u := range pair {
					s.EnsureUnit(u)
					orders.QueueForUnit(u).SetBinding(binding)
					s.SetMoverMode(u, 2)
				}
				runMovementTick(s, 1, w)
				for _, u := range pair {
					pushAirOrder(t, u, "VTOL_LandIfCan", 0, 0)
				}
				tick := uint32(1)
				for tick < 600 {
					tick++
					runLandingTick(s, tick, w)
					if first.Move.Mode == 1 && pair[1].Move.Mode == 1 {
						break
					}
				}
				for _, u := range pair {
					if u.Move.Mode != 1 || u.Move.ModeMirror != 1 {
						t.Errorf("aircraft %d did not land freely: request=%d mirror=%d position=(%d,%d)", u.Handle, u.Move.Mode, u.Move.ModeMirror, u.X.Int(), u.Z.Int())
					}
					q := orders.QueueForUnit(u)
					q.PurgeUnprotected()
					q.DropLeadingAutoOps()
					pushAirOrder(t, u, "VTOL_RepairPatrol", world.CellToWorld(26), world.CellToWorld(24))
				}
				start := [2]Vec3{{X: first.X, Y: first.Y, Z: first.Z}, {X: pair[1].X, Y: pair[1].Y, Z: pair[1].Z}}
				for end := tick + 180; tick < end; {
					tick++
					runLandingTick(s, tick, w)
				}
				for i, u := range pair {
					if u.Y <= start[i].Y || numeric.Abs(u.X-start[i].X)+numeric.Abs(u.Z-start[i].Z) < numeric.Fixed(32<<16) {
						t.Errorf("aircraft %d stayed grounded on construction patrol: request=%d mirror=%d head=%s position=(%d,%d,%d)", u.Handle, u.Move.Mode, u.Move.ModeMirror, headDescriptorName(u), u.X.Int(), u.Y.Int(), u.Z.Int())
					}
				}
			})
		}
	}
}
