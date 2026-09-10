package construction

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Both refund sites store once after the working-precision discount. A
// fractional existing accumulator exposes cancellation's integer-refund case
// just as it does reverse work's fractional refund [05 R-ECO-01 §3, §11].
func TestCancelRefundAccountAndFinalStore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		special bool
		mode    int
		want    float32
	}{
		{"ordinary ignores difficulty", false, 1, 3.1},
		{"easy", true, 0, 1.6},
		{"medium", true, 1, 2.2},
		{"hard", true, 2, 3.1},
		{"other selector", true, 3, 3.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, factory, product, node := factoryWithAttachedProduct(t)
			// trunc((1-.25)*4.9) = 3; the authored cost is fractional.
			product.Remaining, product.Def.BuildCostMetal = .25, 4.9
			product.Def.ActivateWhenBuilt = true
			s.ModeSelector = tc.mode
			s.IsSpecialSecondState = func(owner uint8) bool {
				if owner != factory.Owner {
					t.Fatalf("discount owner = %d", owner)
				}
				return tc.special
			}
			s.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production = .1
			s.Economy.Players[0].Mirror[economy.Metal].Production = .375
			activated := false
			p28FactoryLifecycleBinding(product).SetLifecycleSink(func(e cob.LifecycleEvent) {
				if e.Phase == "start" && e.Name == "Activate" {
					activated = true
					if product.Dying || product.Remaining != 0 || s.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production != tc.want {
						t.Fatal("activation must follow refund and completion, and precede the kill packet")
					}
				}
			})
			product.Activated = false
			s.handleCancelCurrent(factory, node, 100)
			if !activated {
				t.Fatal("completion activation not delivered")
			}
			got := s.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production
			if math.Float32bits(got) != math.Float32bits(tc.want) {
				t.Fatalf("builder refund = %.17g, want %.17g", got, tc.want)
			}
			if got := s.Economy.Players[0].Mirror[economy.Metal].Production; got != .375 {
				t.Fatalf("mirror changed to %v", got)
			}
			if got := s.Economy.UnitBuckets(product.Handle)[economy.Metal].Production; got != 0 {
				t.Fatalf("product received cancellation refund %v", got)
			}
			if product.Remaining != 0 || product.Flags&FlagCompleted == 0 || !product.Activated || !product.Dying || product.LastDamageCause != Kind9Cause || product.EngagementTarget != factory.Handle {
				t.Fatalf("completion/kill lifecycle changed: %+v", product)
			}
			if node.Param2 != 3 || node.Target != 0 || orders.QueueForUnit(factory).LenPrimary() != 0 {
				t.Fatalf("cancel changed count or retained node: %+v", node)
			}
		})
	}
}

func TestReverseRefundFinalStore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		refund  float32
		special bool
		mode    int
		want    float32
	}{
		{"ordinary", .3, false, 1, .4},
		{"easy", .3, true, 0, .25},
		{"medium fractional", .3, true, 1, .31},
		{"medium integer", 3, true, 1, 2.2},
		{"hard", .3, true, 2, .4},
		{"zero preserves guard", 0, true, 1, .1},
		{"negative preserves guard", -3, true, 1, .1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket := float32(.1)
			ReverseRefund(&bucket, tc.refund, tc.special, tc.mode)
			if math.Float32bits(bucket) != math.Float32bits(tc.want) {
				t.Fatalf("refund = %.17g, want %.17g", bucket, tc.want)
			}
		})
	}
}

// Reverse work selects the target account and owner, even with another builder
// supplied; cancellation's builder correction must not redirect it [05 R-WORK-01 §1].
func TestReverseRefundRetainsTargetAccount(t *testing.T) {
	s, builder, original, _ := factoryWithAttachedProduct(t)
	h, err := s.World.Create(original.Def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	target := s.World.Unit(h)
	target.Remaining = .5
	target.Def.BuildTime, target.Def.BuildCostMetal = 100, 12
	s.ModeSelector = 1
	s.IsSpecialSecondState = func(owner uint8) bool { return owner == target.Owner }
	s.Economy.UnitBuckets(target.Handle)[economy.Metal].Production = .1
	s.Economy.UnitBuckets(builder.Handle)[economy.Metal].Production = .375
	if !s.sharedStep(builder, target, -25, 10) {
		t.Fatal("reverse work refused")
	}
	if got := s.Economy.UnitBuckets(target.Handle)[economy.Metal].Production; math.Float32bits(got) != math.Float32bits(float32(2.2)) {
		t.Fatalf("target refund = %.17g, want %.17g", got, float32(2.2))
	}
	if got := s.Economy.UnitBuckets(builder.Handle)[economy.Metal].Production; got != .375 {
		t.Fatalf("builder changed to %v", got)
	}
}

// Gathering the builder before a later unit and the mirror changes float32
// accumulation order. Freed builders are absent from that gather, so their
// pending refund is not settled [05 R-ECO-01 §2, §5].
func TestCancelRefundSettlementUsesLiveBuilderSlot(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "live"
		if remove {
			name = "removed"
		}
		t.Run(name, func(t *testing.T) {
			s, factory, product, node := factoryWithAttachedProduct(t)
			product.Def.BuildCostMetal = 2 // refund 1
			later, err := s.World.Create(newProductDef("later", 1, 1, 0, 1), 0, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			s.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production = 16777216
			s.Economy.UnitBuckets(later)[economy.Metal].Production = -16777216
			s.Economy.Players[0].Mirror[economy.Metal].Production = .25
			s.handleCancelCurrent(factory, node, 100)
			if remove {
				s.World.Destroy(factory.Handle, units.DeathKilled)
				if !s.World.FinalizeDeath(factory.Handle, 100).Freed {
					t.Fatal("builder not freed")
				}
			}
			s.Economy.Settle(0, 101, s.World)
			want := float32(.25)
			if remove {
				want = -16777216
			}
			if got := s.Economy.Players[0].AIProduction[economy.Metal]; got != want {
				t.Fatalf("gathered production = %v, want %v", got, want)
			}
			archived := s.Economy.UnitArchived(factory.Handle)[economy.Metal].Production
			if (!remove && archived != 16777216) || (remove && archived != 0) {
				t.Fatalf("builder archived production = %v", archived)
			}
			if got := s.Economy.Players[0].ArchivedMirror[economy.Metal].Production; got != .25 {
				t.Fatalf("mirror archived = %v", got)
			}
		})
	}
}
