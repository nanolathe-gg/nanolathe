package economy

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

var ownerIterationSink int

// benchmarkSettlementFixture fills every participating player's fixed slice
// before timing starts. NewSliced's argument is the definition count AND the
// fixed per-player slice capacity, so this fixture gives every active player
// exactly 200 slots; the two-player case fills two slices and the ten-player
// case fills all ten. The warm pass allocates the ledger buckets before the
// timer, so allocation results describe traversal rather than fixture setup.
func benchmarkSettlementFixture(b *testing.B, players int) (*Service, *units.World) {
	b.Helper()
	const perPlayerSlots = 200
	w := units.NewSliced(perPlayerSlots, nil)
	s := &Service{}
	def := economyFixtureDef(&content.UnitDef{
		UnitName:      fmt.Sprintf("settlement-bench-%d", players),
		Limit:         -1,
		MaxDamage:     1,
		EnergyMake:    1,
		MetalMake:     1,
		EnergyStorage: 100000,
		MetalStorage:  100000,
	})
	for player := 0; player < players; player++ {
		p := settlingPlayer(s, player)
		p.Stock[Energy] = 1000
		p.Stock[Metal] = 1000
		for i := 0; i < perPlayerSlots; i++ {
			if _, err := w.Create(def, uint8(player), 0, 0, 0); err != nil {
				b.Fatalf("create player %d unit %d: %v", player, i, err)
			}
		}
	}
	for player := 0; player < players; player++ {
		s.Settle(player, 0, w)
	}
	return s, w
}

// legacyOwnerIteration is EC-P1's former whole-world snapshot and owner
// filter. It stays in this benchmark only so both implementations run on the
// same prebuilt live fixture; production code must use ForEachUnitOrdered.
func legacyOwnerIteration(w *units.World, player int) int {
	count := 0
	for _, u := range w.IterSliced() {
		if u != nil && u.Alive && int(u.Owner) == player {
			count++
		}
	}
	return count
}

func BenchmarkSettlementOwnerIteration(b *testing.B) {
	for _, players := range []int{2, 10} {
		b.Run(fmt.Sprintf("%d-players/direct", players), func(b *testing.B) {
			_, w := benchmarkSettlementFixture(b, players)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ownerIterationSink = 0
				for player := 0; player < players; player++ {
					ForEachUnitOrdered(w, player, func(*units.Unit) { ownerIterationSink++ })
				}
			}
		})
		b.Run(fmt.Sprintf("%d-players/legacy-whole-world-snapshot", players), func(b *testing.B) {
			_, w := benchmarkSettlementFixture(b, players)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ownerIterationSink = 0
				for player := 0; player < players; player++ {
					ownerIterationSink += legacyOwnerIteration(w, player)
				}
			}
		})
	}
}

func BenchmarkSettlementFullCapacity(b *testing.B) {
	for _, players := range []int{2, 10} {
		b.Run(fmt.Sprintf("%d-players", players), func(b *testing.B) {
			s, w := benchmarkSettlementFixture(b, players)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for player := 0; player < players; player++ {
					s.Settle(player, uint32(i), w)
				}
			}
		})
	}
}

// BenchmarkOrdinaryPlayerTick keeps every active deadline in the future and
// calls TickPlayer for every active player, representing one ordinary player
// phase that does not settle. Its fixture is also built before timing; this
// confirms the ordinary player path has no owner-world scan or allocation
// between settlement deadlines.
func BenchmarkOrdinaryPlayerTick(b *testing.B) {
	for _, players := range []int{2, 10} {
		b.Run(fmt.Sprintf("%d-players", players), func(b *testing.B) {
			s, w := benchmarkSettlementFixture(b, players)
			for player := 0; player < players; player++ {
				s.Players[player].UpdateTime = ^uint32(0)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for player := 0; player < players; player++ {
					s.TickPlayer(player, uint32(i), w, nil)
				}
			}
		})
	}
}
