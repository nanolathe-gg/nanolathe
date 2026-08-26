package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	pathpkg "github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func TestPathActivationPreflightsGoalBeforeSubmission(t *testing.T) {
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
	s.Econ.Players[0].StatusHalfwordAt144 = 1
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(200)
	s.InitWindForSession(&crt, 0)
	if err := createAndBindServices(s); err != nil {
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
	if s.Movement.Scheduler.HasRequest(h) {
		t.Fatal("impassable goal submitted a scheduler request")
	}
	if q.Head() != nil {
		t.Fatal("impassable goal was not removed")
	}
	if badHead.MoveState != orders.MoveBlocked || badHead.PathStatus != uint32(pathpkg.StatusRejected) {
		t.Fatalf("bad order state=%d status=%#x, want blocked/rejected", badHead.MoveState, badHead.PathStatus)
	}

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
	// Resolution must happen before preflight, otherwise this request slips
	// through with the stale click and gets submitted.
	s.Step(2)
	if s.Movement.Scheduler.HasRequest(h) {
		t.Fatal("target order submitted using a stale passable goal")
	}
	if q.Head() != nil {
		t.Fatal("target order with impassable current position was not removed")
	}
	if targetHead.MoveState != orders.MoveBlocked || targetHead.PathStatus != uint32(pathpkg.StatusRejected) {
		t.Fatalf("target order state=%d status=%#x, want blocked/rejected", targetHead.MoveState, targetHead.PathStatus)
	}

	valid := orders.NewMoveNode(id, numeric.Fixed(10*16*65536), numeric.Fixed(10*16*65536), 1, h, false)
	q.Push(id, valid)
	validHead := q.Head()
	s.Step(3)
	if validHead == nil || validHead.MoveState != orders.MoveEnRoute {
		t.Fatalf("valid active head state=%v, want MoveEnRoute", validHead)
	}
	if !s.Movement.Scheduler.HasRequest(h) {
		route := s.Movement.Routes[h]
		if route == nil || !route.Active {
			t.Fatal("valid active head produced neither pending request nor route")
		}
	}
}
