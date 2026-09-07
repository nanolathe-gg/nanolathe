package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestImpactStackRecordUsesCentralPresentationBranches(t *testing.T) {
	point := Vec3{X: numeric.FixedFromInt(7), Y: numeric.FixedFromInt(3), Z: numeric.FixedFromInt(9)}
	weapon := &content.WeaponDef{
		ShakeMagnitude: 5, ShakeDuration: 2,
		SoundHit: "hit", SoundWater: "water",
		ExplosionGaf: "land.gaf", ExplosionArt: "land",
		WaterExplosionGaf: "water.gaf", WaterExplosionArt: "water",
	}

	t.Run("land", func(t *testing.T) {
		var events []Event
		svc := &Service{Events: func(ev Event) { events = append(events, ev) }}
		svc.ImpactStackRecord(StackImpactRecord{
			Weapon: weapon, Point: point, SecondPoint: point, ShooterSide: 4,
		}, nil, nil, nil, 17)
		want := []EventKind{EventShake, EventHitSound, EventExplosion, EventProjectileImpact}
		if len(events) != len(want) {
			t.Fatalf("events = %#v, want %v [06 §12.2]", events, want)
		}
		for i, kind := range want {
			if events[i].Kind != kind || events[i].Source != 0 || events[i].Target != 0 || events[i].Position != point {
				t.Fatalf("event %d = %#v, want %v at null identities and stack point [06 §12.2]", i, events[i], kind)
			}
		}
		if events[2].Graphic != "land" || events[2].Bank != "land.gaf" || !events[2].HasCalculatedFlash {
			t.Fatalf("land stack effect = %#v, want central calculated land effect [06 R-WFX-01 §2]", events[2])
		}
	})

	t.Run("water", func(t *testing.T) {
		terrain := &world.Terrain{CellW: 1, CellH: 1, SeaLevel: 1, Plot: make([]world.PlotCell, 1)}
		var events []Event
		svc := &Service{Events: func(ev Event) { events = append(events, ev) }}
		svc.ImpactStackRecord(StackImpactRecord{Weapon: weapon, Point: point, SecondPoint: point}, nil, terrain, nil, 18)
		if len(events) != 4 || events[0].Kind != EventShake || events[1].Kind != EventWaterSound || events[2].Kind != EventWaterExplosion || events[3].Kind != EventProjectileImpact {
			t.Fatalf("water stack events = %#v, want shake/water-sound/water-effect/impact [06 R-WFX-01 §3]", events)
		}
		if events[2].Graphic != "water" || events[2].Bank != "water.gaf" {
			t.Fatalf("water stack effect = %#v, want water holder [06 R-WFX-01 §3]", events[2])
		}
	})

	t.Run("end-smoke-and-record-zero", func(t *testing.T) {
		var smoke []Event
		svc := &Service{Events: func(ev Event) { smoke = append(smoke, ev) }}
		svc.ImpactStackRecord(StackImpactRecord{Weapon: &content.WeaponDef{EndSmoke: true}, Point: point, SecondPoint: point}, nil, nil, nil, 19)
		if len(smoke) != 2 || smoke[0].Kind != EventEndSmoke || smoke[1].Kind != EventProjectileImpact {
			t.Fatalf("end-smoke stack events = %#v, want end-smoke then impact [06 R-WFX-01 §2]", smoke)
		}

		var zero []Event
		svc.Events = func(ev Event) { zero = append(zero, ev) }
		svc.ImpactStackRecord(StackImpactRecord{Weapon: &content.WeaponDef{}, Point: point, SecondPoint: point}, nil, nil, nil, 20)
		if len(zero) != 2 || zero[0].Kind != EventExplosion || !zero[0].HasCalculatedFlash || zero[0].CalculatedTable != impactFlashTable {
			t.Fatalf("record-zero stack events = %#v, want calculated table-zero flash [06 §12.2][06 R-WFX-01 §2]", zero)
		}
	})

	t.Run("opaque-water-does-not-retire-a-pooled-record", func(t *testing.T) {
		terrain := &world.Terrain{CellW: 1, CellH: 1, SeaLevel: 1, Plot: make([]world.PlotCell, 1)}
		svc := &Service{OpaqueLiquidMode: true}
		h, ok := svc.Reserve()
		if !ok {
			t.Fatal("reserve pooled control record")
		}
		svc.ImpactStackRecord(StackImpactRecord{Weapon: weapon, Point: point, SecondPoint: point}, nil, terrain, nil, 21)
		if !svc.Alive(h) || svc.Count() != 1 {
			t.Fatalf("stack impact changed pooled control record: alive=%v count=%d [06 R-WFX-01 §3]", svc.Alive(h), svc.Count())
		}
	})
}

func TestStackImpactRoutesOwnerAndFeatureDamage(t *testing.T) {
	def := &content.FeatureDef{Damage: 100, FootprintX: 1, FootprintZ: 1}
	def.CanonicalKey = "rock"
	sim := rng.SimulationFromState(1)
	svc, _, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	w := newCombatFixtureWorld(4, nil)
	var events []Event
	svc.Events = func(ev Event) { events = append(events, ev) }
	svc.ControlByte = func(owner uint8) uint8 {
		if owner == 4 {
			return ControlByteRemote
		}
		return ControlByteHuman
	}
	point := featureCellCentre(8, 8)
	record := StackImpactRecord{Weapon: blastWeapon(64, 7, 0), Point: point, SecondPoint: point, ShooterSide: 4}
	svc.ImpactStackRecord(record, w, terrain, nil, 1)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 0 {
		t.Fatalf("remote-owner stack damage = %d, want 0 [06 §9.1]", got)
	}
	if len(events) != 2 || events[0].Kind != EventExplosion || events[1].Kind != EventProjectileImpact {
		t.Fatalf("blocked routing suppressed presentation: %v", events)
	}
	record.ShooterSide = 3
	svc.ImpactStackRecord(record, w, terrain, nil, 2)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 7 {
		t.Fatalf("admitted stack feature damage = %d, want authored 7 [06 §13.1]", got)
	}
	terrain.LavaWorld = true
	terrain.SeaLevel = 11
	record.Weapon.LavaExplosionGaf, record.Weapon.LavaExplosionArt = "lava.gaf", "lava"
	events = nil
	svc.ImpactStackRecord(record, w, terrain, nil, 3)
	if len(events) != 2 || events[0].Kind != EventWaterExplosion || events[0].Graphic != "lava" || events[0].Bank != "lava.gaf" {
		t.Fatalf("lava stack presentation = %v [06 R-WFX-01 §3]", events)
	}
	svc.OpaqueLiquidMode = true
	events = nil
	before := terrain.PlotAt(8, 8).AnchorWord()
	svc.ImpactStackRecord(record, w, terrain, nil, 4)
	if len(events) != 0 || terrain.PlotAt(8, 8).AnchorWord() != before {
		t.Fatal("opaque null-direct stack impact emitted effects or feature damage [06 R-WFX-01 §3]")
	}
}
