package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// moveRetentionFixture is a computer player's cancapture unit running a
// plain move with an assignment queued behind it, under the given rules,
// with the binding's Modern AI predicate answering modernAI for its owner.
func moveRetentionFixture(t *testing.T, rules Rules, modernAI bool) (*Queue, *units.Unit, []*Node) {
	t.Helper()
	w := newOrdersFixtureWorld(8, &content.Catalog{})
	h, err := w.Create(&content.UnitDef{UnitName: "retreatcom", CanMove: true, CanCapture: true, MaxDamage: 100}, 3, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Rules: rules, SimRNG: &rng.Simulation{}, ModernAIPlayer: func(owner uint8) bool {
		return modernAI && owner == 3
	}})
	move := &Node{ID: Lookup("Move_Ground"), Owner: h, Phase: 1}
	next := &Node{ID: Lookup("Move_Ground"), Owner: h}
	q.SetPrimary([]*Node{move, next})
	return q, u, []*Node{move, next}
}

// Nanolathe Modern policy "Modern AI move retention"
// (DESIGN_UNITS_ORDERS_COB): under Modern the construction throttle's damage
// purge spares a Modern AI player's queue whose running record is a move,
// while Strict purges it as retail does [08 R-AI-01 §11] and a Classic
// player is purged under Modern as before. The decision draws nothing.
func TestModernAIMoveSurvivesTheDamagePurge(t *testing.T) {
	cases := []struct {
		name     string
		rules    Rules
		modernAI bool
		keeps    bool
	}{
		{"modern/Modern AI player", &ModernRules{}, true, true},
		{"modern/Classic player", &ModernRules{}, false, false},
		{"strict/Modern AI player", StrictRules{}, true, false},
		{"community/Modern AI player", &CommunityRules{}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, u, records := moveRetentionFixture(t, c.rules, c.modernAI)
			random := *q.Binding().SimRNG
			PurgeOrdersOnDamage(u)
			primary := q.Primary()
			if c.keeps {
				if len(primary) != 2 || primary[0] != records[0] || primary[1] != records[1] || records[0].Phase != 1 {
					t.Fatalf("the Modern AI player's move was purged: %v", primary)
				}
			} else if len(primary) != 0 {
				t.Fatalf("the move survived the purge: %d records left", len(primary))
			}
			if *q.Binding().SimRNG != random {
				t.Fatal("the purge decision drew randomness")
			}
		})
	}
}

// The retention reads the running record past the temporary control records,
// as protected work does, and covers the air move; any other running record
// is purged, and a binding with no predicate plays every owner as Classic.
func TestModernAIMoveRetentionReadsTheRunningRecord(t *testing.T) {
	for _, c := range []struct {
		name  string
		chain []string
		keeps bool
	}{
		{"air move", []string{"VTOL_Move"}, true},
		{"move behind a stance record", []string{"Standing_MoveOrder", "Move_Ground"}, true},
		{"move behind a stun", []string{"Paralyze", "Move_Ground"}, true},
		{"attack ahead of a move", []string{"Attack_NoMove", "Move_Ground"}, false},
		{"reclaim", []string{"ReclaimUnit"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			q, u, _ := moveRetentionFixture(t, &ModernRules{}, true)
			var chain []*Node
			for _, name := range c.chain {
				chain = append(chain, &Node{ID: Lookup(name), Owner: u.Handle})
			}
			q.SetPrimary(chain)
			if got := rulesOfUnit(u).KeepsMoveOnDamage(u); got != c.keeps {
				t.Fatalf("KeepsMoveOnDamage = %v, want %v", got, c.keeps)
			}
		})
	}
	q, u, _ := moveRetentionFixture(t, &ModernRules{}, true)
	q.Binding().ModernAIPlayer = nil
	if rulesOfUnit(u).KeepsMoveOnDamage(u) {
		t.Fatal("a binding without the predicate kept a move")
	}
}
