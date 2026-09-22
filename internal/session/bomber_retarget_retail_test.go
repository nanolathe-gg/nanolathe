package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A replacement during bomb release must purge the cancelled run's seek before
// the next player order starts [04 R-MOV-03 §6][04 R-AIR-01 §16]. Exercise the
// actual command boundary, stock bomber script, flight and projectile pipeline.
func TestRetailBomberRetargetAfterRelease(t *testing.T) {
	f := loadRetailFixture(t)
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(mode)
			stepRetail(s, 2)
			for i := range s.AI[1].Deadlines {
				s.AI[1].Deadlines[i] = ^uint32(0)
			}
			u := placeCompleteRetailUnit(t, s, "ARMTHUND", 0, numeric.FixedFromInt(1800), numeric.FixedFromInt(1800))
			u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingFireShift) | 2<<units.StandingFireShift
			pos := orders.ResolvePos{X: numeric.FixedFromInt(2200), Z: numeric.FixedFromInt(1800)}
			issue := func() {
				t.Helper()
				if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{u.Handle}, Code: 3, Position: pos}}); err != nil {
					t.Fatal(err)
				}
			}
			issue()
			firstRelease := uint32(0)
			for i := 0; i < 3000; i++ {
				previousReload := u.Slots[0].Reload
				s.Step(s.Clock.ScaledAnchor + 1)
				q := orders.QueueForUnit(u)
				if firstRelease != 0 {
					n := q.Head()
					if n == nil || n.ID != orders.Lookup("AirStrike") || n.GoalX != pos.X || n.GoalZ != pos.Z {
						t.Fatalf("replacement blocked after release: head=%+v", n)
					}
				}
				// A new admitted bomb replenishes the reload countdown.
				if u.Slots[0].Reload <= previousReload || u.Slots[0].Target.Kind != units.TargetGround {
					continue
				}
				if firstRelease == 0 {
					firstRelease = s.Clock.GlobalTick
					t.Logf("first release tick=%d position=(%d,%d) target=(%d,%d)", s.Clock.GlobalTick, u.X.Int(), u.Z.Int(), pos.X.Int(), pos.Z.Int())
					pos.X -= numeric.FixedFromInt(100)
					issue()
					continue
				}
				if u.Slots[0].Target.X != pos.X || u.Slots[0].Target.Z != pos.Z {
					t.Fatal("subsequent bomb retained old target")
				}
				t.Logf("second release tick=%d", s.Clock.GlobalTick)
				return
			}
			t.Fatalf("bomber never completed both releases: first release tick=%d head=%+v", firstRelease, orders.QueueForUnit(u).Head())
		})
	}
}
