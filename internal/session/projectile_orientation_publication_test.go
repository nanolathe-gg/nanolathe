package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestPublishedProjectileOrientationKeepsIndependentWords verifies that the
// committed frame carries the maintained model angle words without collapsing
// base roll, ordinary pitch, and meteor pitch [06 §6.1][06 §6.5][03 §5.2].
func TestPublishedProjectileOrientationKeepsIndependentWords(t *testing.T) {
	w := &content.WeaponDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "propeller-view"},
		ID:               203,
		Meteor:           true,
		Propeller:        true,
	}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{w.CanonicalKey: w}}
	cat.RebuildWeaponIndex()

	var svc combat.Service
	h, ok := svc.Reserve()
	if !ok {
		t.Fatal("reserve projectile")
	}
	p := &svc.Records[int(h)-1]
	p.WeaponID = w.ID
	p.Yaw = numeric.Angle(0x1234)
	p.Pitch = numeric.Angle(0x2345)
	p.Roll = numeric.Angle(0x3456)
	p.PropellerYaw = numeric.Angle(0x5678)
	p.MeteorPitch = numeric.Angle(0x4567)
	p.ExpiryTick = 99

	s := &Session{Catalog: cat, Combat: &svc, Snapshot: &frame.Buffer{}}
	s.publishSnapshot(1)
	got := s.Snapshot.Current()
	if got == nil || len(got.Projectiles) != 1 {
		t.Fatalf("published frame = %#v", got)
	}
	v := got.Projectiles[0]
	if v.Roll != 0x3456 || v.PropellerRoll != 0x5678 || v.MeteorPitch != 0x4567 {
		t.Fatalf("published orientation = %#04x/%#04x/%#04x, want 0x3456/0x5678/0x4567", v.Roll, v.PropellerRoll, v.MeteorPitch)
	}
	if v.Pitch != 0x2345 || v.Yaw != combat.RetailYaw(p.Yaw) {
		t.Fatalf("published yaw/pitch = %#04x/%#04x, want %#04x/0x2345", v.Yaw, v.Pitch, combat.RetailYaw(p.Yaw))
	}
	if !v.Propeller || !v.Meteor || v.ExpiryTick != 99 {
		t.Fatalf("published propeller/meteor/deadline = %v/%v/%d, want true/true/99", v.Propeller, v.Meteor, v.ExpiryTick)
	}
}
