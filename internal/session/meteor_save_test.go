package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestMeteor_SaveContinuation(t *testing.T) {
	cat := &content.Catalog{
		Weapons: map[string]*content.WeaponDef{},
		Meteor: &content.MeteorDefaults{
			MeteorWeapon:   "meteor",
			MeteorRadius:   300,
			MeteorDensity:  2,
			MeteorDuration: 5,
			MeteorInterval: 60,
		},
		Units: map[string]*content.UnitDef{},
		Maps:  map[string]*content.MapHeader{},
		Sides: []*content.SideDef{{Commander: "armcom"}},
	}
	cat.Units[content.CanonicalKey("armcom")] = &content.UnitDef{UnitName: "armcom", MaxDamage: 100}
	w := &content.WeaponDef{ID: 0, Meteor: true, Name: "meteor"}
	w.CanonicalKey = content.CanonicalKey("meteor")
	cat.Weapons[w.CanonicalKey] = w

	sA := &Session{
		Catalog: cat,
		Combat:  &combat.Service{},
		World:   nil,
		Wind:    world.NewWind(100, 2000),
	}
	sA.Catalog = cat
	sA.Meteor = MeteorState{
		Enabled:       true,
		WeaponName:    "meteor",
		Weapon:        w,
		Radius:        300,
		Density:       2,
		DurationTicks: 150,
		IntervalTicks: 1800,
		PerHitDelay:   15,
		NextStrike:    15,
		Initialized:   true,
	}
	sA.SeedSessionRNG(111, 222)
	for tick := uint32(1); tick <= 50; tick++ {
		sA.authoritativeTick(tick)
	}
	// Capture
	st := sA.CaptureStateV1()
	if st == nil {
		t.Fatalf("CaptureStateV1 nil")
	}
	// Verify meteor snapshot captured
	if !st.Meteor.Enabled || st.Meteor.WeaponName != "meteor" {
		t.Fatalf("meteor not captured %+v", st.Meteor)
	}
	// Marshal/unmarshal round-trip
	data := save.MarshalStateV1(st)
	dec, err := save.UnmarshalStateV1(data, "", "")
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Restore into B
	sB := &Session{
		Catalog: cat,
		Combat:  &combat.Service{},
		World:   nil,
		Wind:    world.NewWind(100, 2000),
	}
	sB.Catalog = cat
	sB.Wind = world.NewWind(100, 2000)
	sB.Combat = &combat.Service{}
	if err := sB.RestoreStateV1(dec); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	if sB.Meteor.Enabled != sA.Meteor.Enabled || sB.Meteor.NextStrike != sA.Meteor.NextStrike || sB.Meteor.Active != sA.Meteor.Active {
		t.Fatalf("meteor restore mismatch A %+v B %+v", sA.Meteor, sB.Meteor)
	}
	// Continue both for 50 more ticks and verify determinism
	for tick := uint32(51); tick <= 100; tick++ {
		sA.authoritativeTick(tick)
		sB.authoritativeTick(tick)
	}
	if sA.Combat.Count() != sB.Combat.Count() {
		t.Fatalf("meteor continuation projectile counts differ %d vs %d after save", sA.Combat.Count(), sB.Combat.Count())
	}
	if sA.CrtRNG().Draws() != sB.CrtRNG().Draws() || sA.CrtRNG().State != sB.CrtRNG().State {
		t.Fatalf("CRT state diverged after save continuation")
	}
}
