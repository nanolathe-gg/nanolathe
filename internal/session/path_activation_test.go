package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// goalReachedSearch reports that the unit's goal reached the path search: it
// is either still queued as a live request, or the search already ran to a
// published route within the same tick.
//
// A still-pending request is not the contract. The scheduler runs once per
// tick, first, and keeps popping until an iteration's step charge reaches 100
// [04 §7.3 R-PATH-01 §6]; a short search therefore reconstructs and publishes
// in the very call that admitted it, and the request is released before the
// tick ends. "Requests are full-or-empty… budget exhaustion leaves the heap and
// request active for later ticks" [04 §7.3] — the surviving request is the
// budget-exhaustion case, not the normal one. Asserting HasPathRequest alone
// tests how long the search took.
func goalReachedSearch(s *Session, h pool.Handle) bool {
	if s.Movement.HasPathRequest(h) {
		return true
	}
	route := s.Movement.Routes[h]
	return route != nil && route.Active
}

func TestPathActivationSubmitsGoalForSearchValidation(t *testing.T) {
	rng.SeedGlobal(100, 200)
	cat := minimalCatalogForStrict()
	for _, def := range cat.Units {
		def.CanMove = true
		def.MaxVelocity = 65536
		def.TurnRate = 500
		def.FootprintX = 1
		def.FootprintZ = 1
	}
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission()}
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatalf("newSlicedWorld: %v", err)
	}
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	s.Econ.SeedDeadlines(0)
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind services: %v", err)
	}
	s.RegisterAll()
	s.State = StateBattle
	s.Clock.ScaledAnchor = 0
	def := cat.Units["armcom"]
	h, err := s.Units.Create(def, 0, numeric.Fixed(5*16*65536), 0, numeric.Fixed(5*16*65536))
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	u := s.Units.Unit(h)
	publishOne(s, u)
	s.Movement.EnsureUnit(u)
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("Move_Ground descriptor missing")
	}
	q := orders.QueueForUnit(u)
	bad := orders.NewMoveNode(id, numeric.Fixed(100*16*65536), numeric.Fixed(100*16*65536), 0, h, false)
	q.Push(id, bad)
	badHead := q.Head()
	if badHead == nil {
		t.Fatal("bad order was not queued")
	}
	s.Step(1)
	if !goalReachedSearch(s, h) {
		t.Fatal("goal was not submitted for path search validation")
	}
	if q.Head() != badHead || badHead.MoveState != orders.MoveEnRoute {
		t.Fatal("submission consumed or blocked the active order before search")
	}
	q.CancelAll()
	s.Movement.DeactivateMove(h)

	targetH, err := s.Units.Create(def, 1, numeric.Fixed(100*16*65536), 0, numeric.Fixed(100*16*65536))
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	targetOrder := orders.NewNodeForOrder(id, targetH, numeric.Fixed(10*16*65536), 0, numeric.Fixed(10*16*65536), 1, h, false)
	q.Push(id, targetOrder)
	targetHead := q.Head()
	if targetHead == nil {
		t.Fatal("target order was not queued")
	}
	// The stored click is passable, but the target's current position is not.
	// Resolution still happens before submission; search owns the rejection.
	s.Step(2)
	if !goalReachedSearch(s, h) {
		t.Fatal("resolved target position was not submitted")
	}
	if q.Head() != targetHead || targetHead.MoveState != orders.MoveEnRoute {
		t.Fatal("target order was consumed before search validation")
	}
	q.CancelAll()
	s.Movement.DeactivateMove(h)

	valid := orders.NewMoveNode(id, numeric.Fixed(10*16*65536), numeric.Fixed(10*16*65536), 1, h, false)
	q.Push(id, valid)
	validHead := q.Head()
	s.Step(3)
	if validHead == nil || validHead.MoveState != orders.MoveEnRoute {
		t.Fatalf("valid active head state=%v, want MoveEnRoute", validHead)
	}
	if !s.Movement.HasPathRequest(h) {
		route := s.Movement.Routes[h]
		if route == nil || !route.Active {
			t.Fatal("valid active head produced neither pending request nor route")
		}
	}
}
