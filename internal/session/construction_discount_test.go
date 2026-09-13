package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// Exercise session composition and the actual cancel/decay producers, not
// manually installed refund hooks [05 R-ECO-01 §11].
func TestComposedConstructionRefundDifficulty(t *testing.T) {
	for selector, want := range []float32{1.5, 2.1, 3} {
		for _, cancel := range []bool{false, true} {
			s := strictNewSessionWithUnits(t, 2, 7, 11)
			s.Skirmish.Difficulty = selector
			s.Mission.Difficulty = selector
			if err := createAndBindServices(s); err != nil {
				t.Fatal(err)
			}
			builder, product := s.Units.Iter()[0], s.Units.Iter()[1]
			bd, pd := *builder.Def, *product.Def
			builder.Def, product.Def = &bd, &pd
			// Target is computer-owned; use that same owner for the factory
			// cancellation, whose account differs from reverse work's account.
			builder.Owner = product.Owner
			product.Remaining, product.Health = 0.5, 50
			pd.BuildTime, pd.BuildCostMetal, pd.BuildCostEnergy = 100, 12, 44
			s.bindOrderQueue(builder)
			s.bindOrderQueue(product)
			account, other := product.Handle, builder.Handle
			if cancel {
				pd.BuildCostMetal = 6 // trunc((1-.5)*6) = 3
				q := orders.QueueForUnit(builder)
				q.Push(orders.Lookup("BuildingBuild"), orders.Node{Target: product.Handle, Param2: 1, Phase: 3, DynamicGate: construction.InterruptCancel})
				q.Head().BindTarget(product.Handle)
				if !s.Build.DeliverCancelNotice(builder, q.Head(), 100) {
					t.Fatal("cancel producer refused")
				}
				account, other = builder.Handle, product.Handle
			} else {
				q := orders.QueueForUnit(product)
				q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: 2, Deadline: 99, DynamicGate: 0x8001})
				s.Build.StepUnit(construction.TickContext{Tick: 100}, product.Handle)
			}
			if got := s.Econ.UnitBuckets(account)[economy.Metal].Production; got != want {
				t.Fatalf("selector %d cancel=%t: refund=%v want %v", selector, cancel, got, want)
			}
			if got := s.Econ.UnitBuckets(other)[economy.Metal].Production; got != 0 {
				t.Fatalf("refund reached wrong account: %v", got)
			}
		}
	}
}
