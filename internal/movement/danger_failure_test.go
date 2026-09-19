package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Modern consumes the real empty-route publication before the primary pump.
// MoveState alone is not the production movement failure notification.
func TestModernDangerReceivesPublishedNoRoute(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "strict"
		if modern {
			name = "modern"
		}
		t.Run(name, func(t *testing.T) {
			sys, u, q, head, req := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 5})
			enemy := *u
			enemy.Handle, enemy.Owner = u.Handle+1, 1
			enemy.X += 100 << 16
			u.Flags = 2<<units.StandingMoveShift | 1<<units.StandingFireShift
			b := &orders.QueueBinding{
				Rules: orders.StrictRules{},
				Lookup: func(h pool.Handle) *units.Unit {
					if h == enemy.Handle {
						return &enemy
					}
					if h == u.Handle {
						return u
					}
					return nil
				},
				Hostility:          func(a, b *units.Unit) bool { return a.Owner != b.Owner },
				DangerVisible:      func(a, b *units.Unit) bool { return true },
				DangerStepFeasible: sys.DangerStepFeasible,
			}
			if modern {
				b.Rules = &orders.ModernRules{}
			}
			q.SetBinding(b)
			head.MoveState = orders.MoveEnRoute
			sys.publishFunc(req, nil, path.StatusRejected)
			if head.Satisfied&0x40 == 0 {
				t.Fatal("fixture did not publish the no-route event")
			}
			orders.ObserveDanger(u, &enemy, 10)
			orders.StepDangerResponse(u, 10)
			if (q.Head() != head) != modern {
				t.Fatalf("response=%v, want modern=%v", q.Head() != head, modern)
			}
			if modern && head.Satisfied != 0 {
				t.Fatal("suspended move retained consumed failure")
			}
		})
	}
}
