package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// groupSlotFixture is a flat 128x128-cell map with a sixteen-unit row along X
// (32 world units apart) whose two end units lie beyond retail's formation
// cutoff, plus an optional unselected stationary unit.
func groupSlotFixture(t *testing.T, rules movement.Rules, idleAt *orders.ResolvePos) (*Session, []pool.Handle, pool.Handle) {
	t.Helper()
	var row []orders.ResolvePos
	for i := 0; i < 16; i++ {
		row = append(row, orders.ResolvePos{X: numeric.Fixed((200 + 32*i) << 16), Z: 400 << 16})
	}
	positions := row
	if idleAt != nil {
		positions = append(append([]orders.ResolvePos(nil), row...), *idleAt)
	}
	s, handles := groupSlotWorld(t, positions)
	s.Movement = movement.NewSystem(completedOccupancySessionTerrain(128, 128), movement.Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255}, movement.NewOccupancyGrid())
	s.Movement.Rules = rules
	var idle pool.Handle
	if idleAt != nil {
		idle = handles[len(handles)-1]
		handles = handles[:len(handles)-1]
		// Stamp the idle unit so its footprint is held and stationary.
		s.Movement.BindWorld(s.Units)
		s.Movement.EnsureUnit(s.Units.Unit(idle))
	}
	return s, handles, idle
}

// groupSlotWorld is groupMoveFixture with room for seventeen units.
func groupSlotWorld(t *testing.T, positions []orders.ResolvePos) (*Session, []pool.Handle) {
	t.Helper()
	def := &content.UnitDef{UnitName: "mover", BMCode: 1, CanMove: true, CanPatrol: true, MaxDamage: 100}
	def.CanonicalKey = "mover"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"mover": def}}
	w := newSessionFixtureWorld(32, cat)
	s := &Session{Units: w, Catalog: cat, Econ: &economy.Service{}, rngSim: rng.NewSimulation(7), rngCrt: rng.NewCRT(9), rngInitialized: true}
	var handles []pool.Handle
	for _, p := range positions {
		h, err := w.Create(def, 0, p.X, p.Y, p.Z)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, h)
	}
	return s, handles
}

func goalCell(v numeric.Fixed) int32 { return int32(int64(v) >> 20) }

// Strict keeps retail's broadcast: the row's two end units are outliers and
// both take the clicked point [04 R-STANCE-01 §5]. Modern gives every actor
// its own destination cell, keeps the retail offset of every actor inside the
// cutoff, and clamps the outliers onto their own bearing instead
// (DESIGN_INTERFACE_HUD_INPUT "Modern group destination slots"). Neither
// consumes RNG or resources.
func TestGroupDestinationSlotsStrictAndModern(t *testing.T) {
	click := orders.ResolvePos{X: 1400 << 16, Y: 10 << 16, Z: 600 << 16}
	for _, tc := range []struct {
		name  string
		rules movement.Rules
	}{{"strict", movement.StrictRules{}}, {"community", movement.CommunityRules{}}, {"modern", &movement.ModernRules{}}} {
		t.Run(tc.name, func(t *testing.T) {
			s, h, _ := groupSlotFixture(t, tc.rules, nil)
			sim, crt := s.rngSim, s.rngCrt
			stock := s.Econ.Players[0].Stock
			s.applyHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Handles: h, Position: click}}, 1)
			count := int32(16)
			seen := map[[2]int32]int{}
			for i, handle := range h {
				n := orders.QueueForUnit(s.Units.Unit(handle)).Head()
				if n == nil {
					t.Fatalf("actor %d has no move", i)
				}
				if n.GoalY != click.Y {
					t.Fatalf("actor %d goal Y = %d, want the clicked %d", i, n.GoalY, click.Y)
				}
				u := s.Units.Unit(handle)
				retail := humanFormationGoal(click, u, s.centroidFor(h), count)
				outlier := i == 0 || i == 15
				switch {
				case tc.name != "modern" && (n.GoalX != retail.X || n.GoalZ != retail.Z):
					t.Fatalf("%s actor %d goal (%d,%d), want retail (%d,%d)", tc.name, i, n.GoalX, n.GoalZ, retail.X, retail.Z)
				case tc.name == "modern" && !outlier && (n.GoalX != retail.X || n.GoalZ != retail.Z):
					t.Fatalf("modern actor %d inside the cutoff moved from its retail goal (%d,%d) to (%d,%d)", i, retail.X, retail.Z, n.GoalX, n.GoalZ)
				case tc.name == "modern" && outlier && n.GoalX == click.X && n.GoalZ == click.Z:
					t.Fatalf("modern outlier %d collapsed onto the clicked point", i)
				}
				seen[[2]int32{goalCell(n.GoalX), goalCell(n.GoalZ)}]++
			}
			shared := 0
			for _, k := range seen {
				if k > 1 {
					shared += k
				}
			}
			if tc.name == "modern" && shared != 0 {
				t.Fatalf("modern: %d actors share a destination cell", shared)
			}
			if tc.name != "modern" && shared == 0 {
				t.Fatal("retail broadcast should put both outliers on the clicked point")
			}
			if s.rngSim != sim || s.rngCrt != crt || s.Econ.Players[0].Stock != stock {
				t.Fatal("group destinations consumed RNG or resources")
			}
		})
	}
}

// A stationary unit outside the selection holding an actor's destination
// moves that actor to the nearest free cell; Strict keeps the held goal.
func TestGroupDestinationSlotsAvoidHeldGround(t *testing.T) {
	click := orders.ResolvePos{X: 1400 << 16, Y: 10 << 16, Z: 600 << 16}
	idle := click // the middle actors' offsets are ±16, so the click itself is free; hold actor 8's goal
	idle.X += 16 << 16
	for _, modern := range []bool{false, true} {
		var rules movement.Rules = movement.StrictRules{}
		if modern {
			rules = &movement.ModernRules{}
		}
		s, h, blocker := groupSlotFixture(t, rules, &idle)
		held := s.Units.Unit(blocker)
		s.applyHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Handles: h, Position: click}}, 1)
		onHeld := 0
		for _, handle := range h {
			n := orders.QueueForUnit(s.Units.Unit(handle)).Head()
			if goalCell(n.GoalX) == goalCell(held.X) && goalCell(n.GoalZ) == goalCell(held.Z) {
				onHeld++
			}
		}
		if modern && onHeld != 0 {
			t.Fatalf("modern: %d actors kept a destination the idle unit holds", onHeld)
		}
		if !modern && onHeld == 0 {
			t.Fatal("strict: the retail goal onto the held cell should be kept")
		}
	}
}

// centroidFor is the broadcast's centroid over the actors (test helper).
func (s *Session) centroidFor(h []pool.Handle) orders.ResolvePos {
	c, _ := s.humanOrderCentroid(h, 0)
	return c
}
