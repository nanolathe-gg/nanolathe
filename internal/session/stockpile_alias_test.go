package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestStockpileAliasNamesSlotZeroAndRefusesThroughTheGuard locks both halves of
// the HUD order alias [06 §11.1][06 R-WPN-05 §2]: it supplies build type ZERO —
// slot 0, where shipped stockpile weapons live — rather than searching for a
// slot the alias never names, and it refuses the click through the enqueue
// guard when slot 0 holds weapon record 0 or a weapon without `stockpile`.
func TestStockpileAliasNamesSlotZeroAndRefusesThroughTheGuard(t *testing.T) {
	for _, tc := range []struct {
		name   string
		arm    func(*units.Unit)
		queued bool
	}{
		{"weapon record 0 in slot 0", func(*units.Unit) {}, false},
		{"slot 0 armed without stockpile", func(u *units.Unit) {
			u.SlotAt(0).Weapon = &content.WeaponDef{ID: 2, ReloadTime: 30}
		}, false},
		// The search this replaced would have found slot 1 and enqueued
		// against it; the alias names slot 0 and nothing else.
		{"stockpile weapon in a later slot only", func(u *units.Unit) {
			u.SlotAt(1).Weapon = &content.WeaponDef{ID: 3, ReloadTime: 30, Stockpile: true}
		}, false},
		{"stockpile weapon in slot 0", func(u *units.Unit) {
			u.SlotAt(0).Weapon = &content.WeaponDef{ID: 1, ReloadTime: 30, Stockpile: true}
		}, true},
	} {
		cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
		def := &content.UnitDef{UnitName: "silo", MaxDamage: 100}
		def.CanonicalKey = "silo"
		cat.Units[def.CanonicalKey] = def
		w := newSessionFixtureWorld(8, cat)
		h, err := w.Create(def, 0, 0, 0, 0)
		if err != nil {
			t.Fatalf("%s: create: %v", tc.name, err)
		}
		u := w.Unit(h)
		tc.arm(u)

		s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
		if err := s.EnqueueHumanCommand(HumanCommand{
			Kind:      HumanStockpile,
			Stockpile: HumanStockpileCommand{Unit: h},
		}); err != nil {
			t.Fatalf("%s: enqueue: %v", tc.name, err)
		}
		s.applyHumanCommands(1)

		q := orders.QueueForUnit(u)
		queued := q != nil && q.LenSecondary() > 0
		if queued != tc.queued {
			t.Fatalf("%s: node queued = %v, want %v [06 R-WPN-05 §2]", tc.name, queued, tc.queued)
		}
		if !queued {
			continue
		}
		n := q.Secondary()[0]
		if orders.DescriptorFor(n.ID).Name != "BuildWeapon" {
			t.Fatalf("%s: secondary head is %q, want BuildWeapon", tc.name, orders.DescriptorFor(n.ID).Name)
		}
		if n.Param1 != 0 {
			t.Fatalf("%s: node slot = %d, want the alias's 0 [06 §11.1]", tc.name, n.Param1)
		}
	}
}
