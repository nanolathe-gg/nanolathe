package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestBroadcastForwardsSpacingIntoTheArgumentWord locks [08 R-AI-01 §19]: the
// broadcast helper does not read its trailing pair; each member's submission
// carries it into the order node's argument word — the same slot the
// construction task fills with the product type index for a MobileBuild. The
// wave task's gather broadcast passes 160 and the regroup and explore
// broadcasts pass 0, and the ground move handler reads that word as
// `argument + 4`, its phase-0 arrival radius [04 R-ORD-01 §4]. There is no
// per-member coordinate transform: every member is submitted at the supplied
// point.
func TestBroadcastForwardsSpacingIntoTheArgumentWord(t *testing.T) {
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "attacker"}, UnitName: "attacker", CanAttack: true, CanMove: true, BMCode: true, MaxDamage: 100}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"attacker": def}}

	x := numeric.FixedFromInt(300)
	z := numeric.FixedFromInt(400)
	for _, tt := range []struct {
		name    string
		spacing int32
	}{
		{"wave gather", 0xa0},
		{"regroup and explore", 0},
	} {
		w := newAIFixtureWorld(4, cat)
		a, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		b, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range []pool.Handle{a, b} {
			w.Unit(h).Group = 8
			w.Unit(h).Flags |= units.ArmedStatus
		}
		m := &Manager{Player: 0}
		m.broadcastGroupOrder(w, 8, 2, 0, nil, x, 0, z, 12, tt.spacing)
		for _, h := range []pool.Handle{a, b} {
			q := orders.QueueOfUnit(w.Unit(h))
			if q == nil || len(q.Primary()) != 1 {
				t.Fatalf("%s: member %d received %v", tt.name, h, q)
			}
			n := q.Primary()[0]
			if int32(n.Param1) != tt.spacing {
				t.Fatalf("%s: member %d argument word = %d, want %d", tt.name, h, int32(n.Param1), tt.spacing)
			}
			if n.GoalX != x || n.GoalZ != z {
				t.Fatalf("%s: member %d was transformed to (%d,%d), want the supplied point (%d,%d)", tt.name, h, n.GoalX, n.GoalZ, x, z)
			}
		}
	}
}
