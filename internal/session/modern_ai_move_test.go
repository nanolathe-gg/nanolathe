package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The composed damage path: a hit on a computer player's cancapture unit arms
// the construction throttle with its one draw and then purges its orders
// [08 R-AI-01 §11]. Under Modern a Modern AI player's running move survives
// the purge; under Strict it is purged as retail does, and a Classic player
// is purged in both. Every case draws and spends the same
// (DESIGN_UNITS_ORDERS_COB "Modern AI move retention").
func TestModernAIMoveRetentionThroughTheDamagePath(t *testing.T) {
	type outcome struct {
		kept  bool
		sim   rng.Simulation
		crt   rng.CRT
		stock [2]float32
	}
	run := func(t *testing.T, mode gameplay.Mode, c ai.Controller) outcome {
		t.Helper()
		s := strictNewSessionWithUnits(t, 0, 73, 23)
		s.SetGameplay(mode)
		s.AI[1] = &ai.Manager{Player: 1, RNG: s.SimRNG()}
		if err := s.setAIController(s.AI[1], c); err != nil {
			t.Fatal(err)
		}
		def := *s.Catalog.Units["armcom"]
		def.CanCapture = true
		var made [2]*units.Unit
		for i := range made {
			x, z := numeric.FixedFromInt(int64(200+64*i)), numeric.FixedFromInt(200)
			h, err := s.Units.Create(&def, uint8(i), x, s.World.HeightAt(x, z), z)
			if err != nil {
				t.Fatal(err)
			}
			s.CompleteUnit(h)
			made[i] = s.Units.Unit(h)
			s.bindOrderQueue(made[i])
		}
		attacker, victim := made[0], made[1]
		q := orders.QueueOfUnit(victim)
		moveID := orders.Lookup("Move_Ground")
		q.Push(moveID, orders.NewNodeForOrder(moveID, 0, numeric.FixedFromInt(600), 0, numeric.FixedFromInt(600), 1, victim.Handle, false))
		move := q.Head()
		draws := s.SimRNG().Draws()
		s.Combat.AcceptDamage(s.Units, 1, combat.DamageInput{Victim: victim.Handle, Attacker: attacker.Handle, Nominal: 1, Kind: combat.KindOrdinary})
		if s.SimRNG().Draws() != draws+1 {
			t.Fatalf("the hit drew %d times, want the throttle's one draw", s.SimRNG().Draws()-draws)
		}
		return outcome{kept: move != nil && q.Head() == move, sim: *s.SimRNG(), crt: *s.CrtRNG(), stock: s.Econ.Players[1].Stock}
	}
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			m, c := run(t, mode, ai.ControllerModern), run(t, mode, ai.ControllerClassic)
			if m.kept != (mode == gameplay.Modern) {
				t.Fatalf("the Modern AI player's move kept=%v under %s", m.kept, mode)
			}
			if c.kept {
				t.Fatalf("the Classic player's move survived under %s", mode)
			}
			if m.sim != c.sim || m.crt != c.crt || m.stock != c.stock {
				t.Fatalf("the retention changed draws or resources: %+v vs %+v", m, c)
			}
		})
	}
}
