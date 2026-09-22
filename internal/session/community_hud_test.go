package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func TestCommunityHUDPublicationFallbacksAndLinks(t *testing.T) {
	s := newLoopTestSession(t, 0)
	def := *s.Catalog.Units["armcom"]
	def.TransportCapacity = 3
	def.Weapon1Def = &content.WeaponDef{Stockpile: true}
	def.Weapon2Def = &content.WeaponDef{ReloadTime: 0, ReloadBar: true}
	def.Weapon3Def = nil
	h, err := s.Units.Create(&def, 0, 100<<16, 0, 100<<16)
	if err != nil {
		t.Fatal(err)
	}
	childH, err := s.Units.Create(&def, 0, 110<<16, 0, 100<<16)
	if err != nil {
		t.Fatal(err)
	}
	u, child := s.Units.Unit(h), s.Units.Unit(childH)
	u.Slots[0].Ammo = 257 // The display sums the source byte, not the widened counter.
	u.Slots[1].Weapon = &content.WeaponDef{ReloadTime: 90, Stockpile: true}
	u.Slots[1].Reload = 35
	u.Slots[2].Weapon = &content.WeaponDef{Stockpile: true}
	u.Slots[2].Ammo = 4
	orders.QueueForUnit(u).CoalesceTail(orders.Lookup("BuildWeapon"), orders.Node{Param2: 7})
	u.Attachment.Cargo = []pool.Handle{childH, h}
	child.Attachment.Carrier = h
	got := s.publishCommunityHUD(u)
	if got.StockpileCount != 5 || got.StockpileQueued != 7 || got.TransportCount != 1 || got.TransportCapacity != 3 {
		t.Fatalf("published counters %+v", got)
	}
	if w := got.Weapons[1]; !w.Tagged || !w.Stockpile || w.ReloadTime != 90 || w.Reload != 35 {
		t.Fatalf("independent authored/live fallbacks %+v", w)
	}
	// Publication owns a value copy; later queue and attachment changes cannot
	// mutate the already-published operands.
	orders.QueueForUnit(u).Secondary()[0].Param2 = 2
	child.Attachment.Carrier = 0
	if got.StockpileQueued != 7 || got.TransportCount != 1 {
		t.Fatal("publication retained mutable state")
	}
}
