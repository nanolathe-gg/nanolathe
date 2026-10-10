//go:build retail

package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Target loss must preserve the physical return and keep borrowing paused until
// actual waypoint arrival: DESIGN_UNITS_ORDERS_COB "Modern patrol work".
func TestModernConstructorPatrolTargetLossRecoversAtWaypointRetail(t *testing.T) {
	for _, key := range []string{"armcv", "armca"} {
		for stance := uint32(0); stance < 3; stance++ {
			t.Run(fmt.Sprintf("%s/stance%d", key, stance), func(t *testing.T) {
				f := newConstructorPatrolRuntime(t, key, stance, nil)
				// Keep the first route leg in the locally exercised corridor. Initial
				// scene staging precedes acquisition; both retained route identities stay.
				f.route[0].GoalX = numeric.FixedFromInt(1000)
				f.route[0].GoalZ = numeric.FixedFromInt(584)
				f.route[0].GoalY = f.s.World.HeightAt(f.route[0].GoalX, f.route[0].GoalZ)
				f.goals[0] = constructorPatrolPoint{f.route[0].GoalX, f.route[0].GoalY, f.route[0].GoalZ}
				target := f.lateNanoframe(160)
				f.waitForBorrow(target)
				var back *orders.Node
				for _, n := range f.q.Primary() {
					if n.IsPatrolReturn() {
						back = n
						break
					}
				}
				if back == nil {
					t.Fatal("borrowed job has no saved return")
				}
				anchor := constructorPatrolPoint{back.GoalX, back.GoalY, back.GoalZ}
				f.s.Units.Destroy(target.Handle, units.DeathUnknown)
				for i := 0; i < 120 && !f.q.PatrolWorkPaused(); i++ {
					f.step()
				}
				if !f.q.PatrolWorkPaused() {
					t.Fatalf("target destruction did not pause patrol work: helper alive=%v dying=%v hp=%d target alive=%v dying=%v remaining=%g head=%s", f.helper.Alive, f.helper.Dying, f.helper.Health, target.Alive, target.Dying, target.Remaining, constructorPatrolName(f.q.Head()))
				}
				returned, recovered := false, false
				for i := 0; i < 3000; i++ {
					before := f.q.Head()
					f.step()
					f.assertRetained()
					head := f.q.Head()
					if (before == back || head == back) && constructorPatrolPosition(f.helper).distanceSquared(anchor) <= 24*24 {
						returned = true
					}
					if !f.q.PatrolWorkPaused() {
						atWaypoint := false
						for _, p := range f.goals {
							if constructorPatrolPosition(f.helper).distanceSquared(p) <= 48*48 {
								atWaypoint = true
							}
						}
						if !returned || !atWaypoint {
							t.Fatalf("pause cleared before physical return and waypoint: returned=%v atWaypoint=%v position=(%d,%d)", returned, atWaypoint, f.helper.X.Floor(), f.helper.Z.Floor())
						}
						recovered = true
						break
					}
					if head != nil && head.Target != 0 {
						t.Fatalf("borrowed before waypoint recovery: %s target=%d", constructorPatrolName(head), head.Target)
					}
				}
				if !recovered {
					t.Fatalf("never reached a patrol waypoint after invalid target; position=(%d,%d) goal=(%d,%d) alive=%v dying=%v hp=%d head=%s return=%v", f.helper.X.Floor(), f.helper.Z.Floor(), int64(back.GoalX)/int64(numeric.FixedFromInt(1)), int64(back.GoalZ)/int64(numeric.FixedFromInt(1)), f.helper.Alive, f.helper.Dying, f.helper.Health, constructorPatrolName(f.q.Head()), returned)
				}
			})
		}
	}
}
