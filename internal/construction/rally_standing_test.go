package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// standingFixture builds a live builder/product pair owned by one player whose
// economy row carries the given control byte.
func standingFixture(t *testing.T, controlByte uint8) (*Service, *units.Unit, *units.Unit) {
	t.Helper()
	facDef := newProductDef("armfac", 1, 1, 100, 100)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	prodDef.BMCode = 1
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		facDef.CanonicalKey:  facDef,
		prodDef.CanonicalKey: prodDef,
	}}
	w := newConstructionFixtureWorld(4, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	econ := &economy.Service{}
	econ.Players[0].Exists = true
	econ.Players[0].ControllerState = controlByte
	svc := NewService(nil, cat, w, econ)
	return svc, w.Unit(fh), w.Unit(ph)
}

// The post-build merge of [04 §3.5] / [04 §3.8] / [04 R-FAC-02 §4]:
// standing-move bits 18-19 and standing-fire bits 20-21 copy from builder to
// product under the alive / death-latch guard, and the control group rides
// the same block only when the product's owner row is occupied AND its control
// byte is exactly the human seat value — 1, not 2 [05 R-SHARE-01 §1].
func TestRallyInheritanceMergesStandingFields(t *testing.T) {
	for _, tc := range []struct {
		name         string
		control      uint8
		change       func(*Service, *units.Unit, *units.Unit, *orders.Node)
		wantStanding bool
		wantGroup    uint8
	}{
		{name: "human", control: 1, wantStanding: true, wantGroup: 7},
		{name: "computer", control: 2, wantStanding: true, wantGroup: 3},
		{name: "remote peer", control: 3, wantStanding: true, wantGroup: 3},
		{name: "gate reads product owner", control: 1, wantStanding: true, wantGroup: 7,
			change: func(s *Service, b, _ *units.Unit, _ *orders.Node) {
				b.Owner = 1
				s.Economy.Players[1].Exists = true
				s.Economy.Players[1].ControllerState = 2
			}},
		{name: "unoccupied", control: 1, wantStanding: true, wantGroup: 3,
			change: func(s *Service, _, p *units.Unit, _ *orders.Node) { s.Economy.Players[p.Owner].Exists = false }},
		{name: "builder dying", control: 1, wantGroup: 3,
			change: func(_ *Service, b, _ *units.Unit, _ *orders.Node) { b.Dying = true }},
		{name: "product dying", control: 1, wantGroup: 3,
			change: func(_ *Service, _, p *units.Unit, _ *orders.Node) { p.Dying = true }},
		{name: "builder no longer alive", control: 1, wantGroup: 3,
			change: func(_ *Service, b, _ *units.Unit, _ *orders.Node) { b.Alive = false }},
		{name: "product no longer alive", control: 1, wantGroup: 3,
			change: func(_ *Service, _, p *units.Unit, _ *orders.Node) { p.Alive = false }},
		{name: "no mover", control: 1, wantGroup: 3,
			change: func(_ *Service, _, p *units.Unit, _ *orders.Node) { p.Def.BMCode = 0 }},
		{name: "no producer", control: 1, wantGroup: 3,
			change: func(_ *Service, _, _ *units.Unit, n *orders.Node) { n.Target = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, factory, product := standingFixture(t, tc.control)
			factory.Flags |= StandingMoveMask | StandingFireMask
			product.Flags &^= StandingMoveMask | StandingFireMask
			factory.Group, product.Group = 7, 3
			factory.Kills, product.Kills = 11, 2
			product.Remaining = 0
			node := &orders.Node{Target: factory.Handle, Phase: uint8(State2)}
			if tc.change != nil {
				tc.change(svc, factory, product, node)
			}

			if code := svc.handleGetBuiltOrder(product, node, 0, 10); code != 5 {
				t.Fatalf("GetBuilt result = %d, want complete", code)
			}

			var wantFlags uint32
			if tc.wantStanding {
				wantFlags = StandingMoveMask | StandingFireMask
			}
			if got := product.Flags & (StandingMoveMask | StandingFireMask); got != wantFlags {
				t.Errorf("standing bits = %#x, want %#x", got, wantFlags)
			}
			if product.Group != tc.wantGroup {
				t.Errorf("control group = %d, want %d", product.Group, tc.wantGroup)
			}
			if factory.Kills != 11 || product.Kills != 2 {
				t.Errorf("builder/product kills = %d/%d, want unchanged 11/2", factory.Kills, product.Kills)
			}
		})
	}
}

// The current group copies once at the completed GetBuilt visit, after its
// ordinary deadline admits the handler. Allocation's stance copy and the
// synchronous completion transition do not copy it [04 §3.8][04 R-FAC-02 §4].
func TestGetBuiltInheritsCurrentGroupAtHandoff(t *testing.T) {
	for _, tc := range []struct {
		name  string
		group uint8
	}{{"reassigned", 5}, {"cleared", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			svc, factory, product := standingFixture(t, controlByteHuman)
			factory.Group, product.Group = 7, 3
			factory.Kills, product.Kills = 11, 2
			product.Remaining = 0.5
			svc.copyStandingFlags(factory, product)
			if product.Group != 3 {
				t.Fatalf("initial stance copy changed group to %d", product.Group)
			}
			q := svc.queueForUnit(product)
			q.Push(orders.Lookup("GetBuilt"), orders.Node{Target: factory.Handle, Phase: uint8(State1), Deadline: -1})
			q.Head().DynamicGate = 0
			q.Pump(product, 10)
			if q.Head() == nil || q.Head().Phase != uint8(State2) || q.Head().Deadline != 40 || product.Group != 3 {
				t.Fatalf("unfinished visit: head=%+v group=%d", q.Head(), product.Group)
			}

			svc.applyCompletionPosture(product)
			factory.Group = tc.group
			q.Pump(product, 39)
			if product.Group != 3 || q.Head() == nil || q.Head().ID != orders.Lookup("GetBuilt") {
				t.Fatalf("inherited before due handoff: group=%d head=%+v", product.Group, q.Head())
			}
			q.Pump(product, 40)
			if product.Group != tc.group {
				t.Errorf("handoff group = %d, want current builder group %d", product.Group, tc.group)
			}
			for _, node := range q.Primary() {
				if node.ID == orders.Lookup("GetBuilt") {
					t.Fatal("completed GetBuilt remained in the queue")
				}
			}

			factory.Group = 9
			q.Pump(product, 41)
			if product.Group != tc.group {
				t.Errorf("group changed after handoff: got %d, want %d", product.Group, tc.group)
			}
			if factory.Kills != 11 || product.Kills != 2 {
				t.Errorf("builder/product kills = %d/%d, want unchanged 11/2", factory.Kills, product.Kills)
			}
		})
	}
}
