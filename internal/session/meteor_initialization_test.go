package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func meteorInitSession(t *testing.T, global, schema string, defaults *content.MeteorDefaults) *Session {
	t.Helper()
	ota, err := formats.LoadOTA([]byte("[GlobalHeader] {\n" + global + "\n[Schema 0] {\n" + schema + "\n}\n}"))
	if err != nil {
		t.Fatalf("LoadOTA: %v", err)
	}
	weapons := map[string]*content.WeaponDef{}
	for id, name := range []string{"noweapon", "custom", "meteor"} {
		w := &content.WeaponDef{ID: int32(id), Name: name, Meteor: name != "noweapon"}
		w.CanonicalKey = content.CanonicalKey(name)
		weapons[w.CanonicalKey] = w
	}
	return &Session{
		Catalog: &content.Catalog{Meteor: defaults, Weapons: weapons},
		Mission: &mission.Mission{OTA: ota, Schema: mission.Schema{Name: "Schema 0"}},
	}
}

func validMeteorDefault() *content.MeteorDefaults {
	return &content.MeteorDefaults{
		DefaultPresent: true,
		DefaultValid:   true,
		MeteorWeapon:   "meteor",
		MeteorRadius:   300,
		MeteorDensity:  2,
		MeteorDuration: 5,
		MeteorInterval: 60,
	}
}

func TestInitMeteorUsesSelectedSchemaInsteadOfGlobals(t *testing.T) {
	s := meteorInitSession(t,
		"MeteorWeapon=global; MeteorRadius=0;",
		"MeteorWeapon=custom; MeteorRadius=17; MeteorDensity=3; MeteorDuration=4; MeteorInterval=5;",
		&content.MeteorDefaults{DefaultPresent: true, DefaultValid: false},
	)
	if err := s.initMeteor(); err != nil {
		t.Fatalf("initMeteor selected schema: %v", err)
	}
	if !s.Meteor.Enabled || s.Meteor.WeaponName != "custom" || s.Meteor.Radius != 17 || s.Meteor.PerHitDelay != 10 || s.Meteor.DurationTicks != 120 || s.Meteor.IntervalTicks != 150 {
		t.Fatalf("selected-schema meteor = %+v", s.Meteor)
	}
}

func TestInitMeteorCompleteSchemaBypassesInvalidDefault(t *testing.T) {
	s := meteorInitSession(t, "", "MeteorWeapon=custom; MeteorRadius=7; MeteorDensity=2; MeteorDuration=3; MeteorInterval=4;", &content.MeteorDefaults{DefaultPresent: true, DefaultValid: false})
	if err := s.initMeteor(); err != nil {
		t.Fatalf("complete selected schema should bypass invalid default: %v", err)
	}
	if s.Meteor.WeaponName != "custom" || s.Meteor.Radius != 7 {
		t.Fatalf("custom schema was changed: %+v", s.Meteor)
	}
}

func TestInitMeteorZeroReplacesWholeRecord(t *testing.T) {
	s := meteorInitSession(t, "", "MeteorWeapon=custom; MeteorRadius=7; MeteorDensity=3; MeteorDuration=4; MeteorInterval=0;", validMeteorDefault())
	if err := s.initMeteor(); err != nil {
		t.Fatalf("initMeteor default replacement: %v", err)
	}
	if !s.Meteor.Enabled || s.Meteor.WeaponName != "meteor" || s.Meteor.Radius != 300 || s.Meteor.PerHitDelay != 15 || s.Meteor.DurationTicks != 150 || s.Meteor.IntervalTicks != 1800 {
		t.Fatalf("whole default replacement = %+v", s.Meteor)
	}
}

func TestInitMeteorSelectedEmptyDefaultUsesRecordZero(t *testing.T) {
	defaults := validMeteorDefault()
	defaults.MeteorWeapon = ""
	s := meteorInitSession(t, "", "MeteorWeapon=custom; MeteorRadius=0; MeteorDensity=3; MeteorDuration=4; MeteorInterval=5;", defaults)
	if err := s.initMeteor(); err != nil {
		t.Fatalf("initMeteor selected empty default: %v", err)
	}
	if !s.Meteor.Enabled || s.Meteor.WeaponName != "" || s.Meteor.Weapon == nil || s.Meteor.Weapon.ID != 0 {
		t.Fatalf("selected empty default must retain enablement and use ID 0: %+v", s.Meteor)
	}
}

func TestInitMeteorOriginalEmptyNameRemainsDisabled(t *testing.T) {
	s := meteorInitSession(t, "", "MeteorRadius=7; MeteorDensity=3; MeteorDuration=4; MeteorInterval=5;", validMeteorDefault())
	if err := s.initMeteor(); err != nil {
		t.Fatalf("initMeteor empty weapon: %v", err)
	}
	if s.Meteor.Enabled || s.Meteor.WeaponName != "meteor" || s.Meteor.Radius != 300 {
		t.Fatalf("empty original weapon did not remain disabled: %+v", s.Meteor)
	}
	// The phase still consumes its due scheduling draws, but Enabled prevents
	// the default-supplied weapon from producing a meteor.
	s.World = &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	for i := range s.World.Plot {
		s.World.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	s.Combat = &combat.Service{}
	s.SeedSessionRNG(1, 2)
	s.phaseMeteorShower(15)
	if got := s.CrtRNG().Draws(); got != 4 || s.Combat.Count() != 0 {
		t.Fatalf("disabled default phase draws=%d projectiles=%d", got, s.Combat.Count())
	}
}

func TestInitMeteorAbsentDefaultLeavesIncomingRecord(t *testing.T) {
	s := meteorInitSession(t, "", "MeteorWeapon=custom; MeteorRadius=0; MeteorDensity=2; MeteorDuration=3; MeteorInterval=4;", &content.MeteorDefaults{})
	if err := s.initMeteor(); err != nil {
		t.Fatalf("missing Default should not fail: %v", err)
	}
	if !s.Meteor.Enabled || s.Meteor.WeaponName != "custom" || s.Meteor.Radius != 0 || s.Meteor.PerHitDelay != 15 || s.Meteor.DurationTicks != 90 || s.Meteor.IntervalTicks != 120 {
		t.Fatalf("missing Default changed incoming record: %+v", s.Meteor)
	}
}

func TestInitMeteorEmptyWeaponWithoutDefaultUsesDirectHostZeroPolicy(t *testing.T) {
	s := meteorInitSession(t, "", "MeteorRadius=7; MeteorDensity=2; MeteorDuration=3; MeteorInterval=4;", &content.MeteorDefaults{})
	if err := s.initMeteor(); err != nil {
		t.Fatalf("missing Default with empty weapon: %v", err)
	}
	if s.Meteor.Enabled || s.Meteor.Radius != 0 || s.Meteor.PerHitDelay != 0 || s.Meteor.DurationTicks != 0 || s.Meteor.IntervalTicks != 0 {
		t.Fatalf("empty/no-default host policy = %+v", s.Meteor)
	}
}

func TestInitMeteorRejectsInvalidSelectedDefaultWithoutPartialState(t *testing.T) {
	defaults := map[string]*content.MeteorDefaults{
		"missing weapon": {
			DefaultPresent: true,
			MeteorRadius:   300,
			MeteorDensity:  2,
			MeteorDuration: 5,
			MeteorInterval: 60,
		},
		"zero numeric": {
			DefaultPresent: true,
			MeteorWeapon:   "meteor",
			MeteorRadius:   0,
			MeteorDensity:  2,
			MeteorDuration: 5,
			MeteorInterval: 60,
		},
	}
	for name, defaults := range defaults {
		t.Run(name, func(t *testing.T) {
			s := meteorInitSession(t, "", "MeteorWeapon=custom; MeteorRadius=0; MeteorDensity=2; MeteorDuration=3; MeteorInterval=4;", defaults)
			if err := s.initMeteor(); err == nil || err.Error() != "Hey, hoser!  The default meteor shower data was bogus!" {
				t.Fatalf("initMeteor fatal = %v", err)
			}
			if s.Meteor.Initialized || s.Meteor.WeaponName != "" || s.Meteor.Radius != 0 {
				t.Fatalf("fatal default installed partial storm: %+v", s.Meteor)
			}
		})
	}
}

func TestMeteorPhaseFallbackFailsBeforeSchedulingInvalidDefault(t *testing.T) {
	s := meteorInitSession(t, "", "MeteorWeapon=custom; MeteorRadius=0; MeteorDensity=2; MeteorDuration=3; MeteorInterval=4;", &content.MeteorDefaults{DefaultPresent: true})
	deferred := func() (got any) {
		defer func() { got = recover() }()
		s.phaseMeteorShower(1)
		return nil
	}()
	if err, ok := deferred.(error); !ok || err.Error() != "Hey, hoser!  The default meteor shower data was bogus!" {
		t.Fatalf("phase fallback fatal = %#v", deferred)
	}
	if s.Meteor.Initialized {
		t.Fatalf("phase fallback marked a fatal storm initialized: %+v", s.Meteor)
	}
}
