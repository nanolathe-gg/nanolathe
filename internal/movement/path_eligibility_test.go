package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"testing"
)

// The divisor is a count; admission addresses actual player slots
// [04 R-PATH-01 §6]. Sparse and dense layouts buy the same per-player share.
func TestPathEligibilityUsesSlotsSeparatelyFromCount(t *testing.T) {
	for _, owner := range []uint8{1, 3} {
		terrain := syntheticTerrainForIntegrate()
		sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
		w := newMovementFixtureWorld(10)
		sys.BindWorld(w)
		sys.ConfigurePath(2, 10, func(player int) bool { return player == 0 || player == int(owner) })
		if sys.pathProvider.PlayerCount() != 2 || !sys.pathProvider.Eligible(int(owner)) {
			t.Fatal("participant count or slot eligibility changed")
		}
		if owner == 3 && sys.pathProvider.Eligible(1) {
			t.Fatal("empty slot 1 admitted")
		}
		h, err := w.Create(wiringDef(), owner, world.CellToWorld(2), 0, world.CellToWorld(2))
		if err != nil {
			t.Fatal(err)
		}
		sys.EnsureUnit(w.Unit(h))
		sys.BeginTick(1)
		sys.SubmitMove(h, owner, path.Cell{X: 2, Z: 2}, path.Cell{X: 9, Z: 9})
		for tick := uint32(60); tick < 70 && sys.HasPathRequest(h); tick++ {
			sys.Scheduler.Tick(tick)
		}
		if route := handleRow(sys.Routes, h); route == nil || route.Count == 0 || route.Status != 0 {
			t.Fatalf("owner %d route = %+v", owner, route)
		}
	}
}
