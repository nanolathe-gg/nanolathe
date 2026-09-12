package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These tests lock the approved Enhanced presentation policy, not retail heat.
func TestWreckHeatComesFromSuccessfulDeathPlacement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tick    uint32
		cause   combat.Cause
		blocked bool
		known   bool
	}{
		{name: "tick zero", cause: combat.CauseOrdinary, known: true},
		{name: "later death", tick: 71, cause: combat.CauseOrdinary, known: true},
		{name: "feature conversion", tick: 71, cause: combat.CauseFeatureConversion},
		{name: "rejected placement", tick: 71, cause: combat.CauseOrdinary, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newLoopTestSession(t, 0)
			s.Clock.GlobalTick = tc.tick
			wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "heatwreck"}, FootprintX: 1, FootprintZ: 1, Damage: 100, Object: "heatwreck", Reclaimable: true}
			s.Catalog.Features["heatwreck"] = wreck
			def := s.Catalog.Units["armcom"]
			def.Corpse = "heatwreck"
			def.MaxDamage = 100
			h, err := s.Units.Create(def, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
			if err != nil {
				t.Fatal(err)
			}
			u := s.Units.Unit(h)
			u.Health, u.MaxHealth, u.PriorSample = -10, 100, 80
			u.Remaining = 0
			u.SetScript(cob.NewVM(makeKilledProg(1)))
			if tc.blocked {
				blocker := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "obstruction"}, FootprintX: 1, FootprintZ: 1, Indestructible: true}
				if s.Features.PlaceAt(10, 10, blocker) == nil {
					t.Fatal("place blocking feature")
				}
			}
			s.Units.Destroy(h, units.DeathKilled)
			u.LastDamageCause = uint8(tc.cause)
			s.Units.FinalizeDeath(h, tc.tick)
			s.publishSnapshot(tc.tick)
			views := s.Snapshot.Current().Features
			if len(views) != 1 || views[0].WreckHeatKnown != tc.known {
				t.Fatalf("death feature views = %+v, want known=%v", views, tc.known)
			}
			if tc.known {
				if views[0].WreckBornTick != tc.tick {
					t.Fatalf("birth tick=%d, want %d", views[0].WreckBornTick, tc.tick)
				}
				s.publishSnapshot(tc.tick + 1)
				if got := s.Snapshot.Current().Features[0]; !got.WreckHeatKnown || got.WreckBornTick != tc.tick {
					t.Fatalf("later publication reset birth: %+v", got)
				}
			}
		})
	}
}

func wreckPublicationFixture(t *testing.T) (*Session, *features.Instance) {
	t.Helper()
	s := &Session{Clock: &clock.State{GlobalTick: 20}, Snapshot: frame.NewBuffer()}
	s.SeedSessionRNG(7, 11)
	s.Clock.GlobalTick = 20
	s.Features = features.NewService(minimalTerrain(), s.SimRNG(), nil, nil)
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "heatwreck"}, Object: "heatwreck", FootprintX: 1, FootprintZ: 1, Damage: 100, Reclaimable: true}
	inst := s.Features.PlaceAt(4, 4, def)
	if inst == nil {
		t.Fatal("place feature")
	}
	return s, inst
}

func TestWreckHeatRetiresOnReplacementReclaimAndRestore(t *testing.T) {
	for _, operation := range []string{"replacement", "reclaim", "restore", "service removal"} {
		t.Run(operation, func(t *testing.T) {
			s, inst := wreckPublicationFixture(t)
			s.noteWreckBirth(inst)
			s.publishSnapshot(20)
			prior := s.Snapshot.Current()
			switch operation {
			case "replacement":
				if s.Features.PlaceAt(4, 4, inst.Def) == nil {
					t.Fatal("replace feature")
				}
			case "reclaim":
				s.Features.RemoveFeatureAt(4, 4, features.CauseReclaim)
			case "restore":
				image, err := s.Features.RetailFeatureImage()
				if err != nil || len(image.ThreeD) != 1 {
					t.Fatalf("save feature: %+v, %v", image, err)
				}
				s.Features.ResetForRestore()
				record := image.ThreeD[0]
				if _, err := s.Features.RestoreAt(int(record.X), int(record.Z), inst.Def, 2, record.Data); err != nil {
					t.Fatal(err)
				}
			case "service removal":
				s.Features = nil
			}
			s.publishSnapshot(21)
			for _, view := range s.Snapshot.Current().Features {
				if view.WreckHeatKnown || view.WreckBornTick != 0 {
					t.Fatalf("replacement inherited heat: %+v", view)
				}
			}
			if len(s.publication.wrecks.births) != 0 || len(s.publication.wrecks.instances) != 0 {
				t.Fatal("retained removed wreck metadata")
			}
			for _, retained := range s.publication.wrecks.instances[:cap(s.publication.wrecks.instances)] {
				if retained != nil {
					t.Fatal("retained removed feature in scratch capacity")
				}
			}
			if !prior.Features[0].WreckHeatKnown || prior.Features[0].WreckBornTick != 20 {
				t.Fatal("changed earlier committed metadata")
			}
		})
	}
}

func TestWreckHeatPublicationIsTransientAndReadOnly(t *testing.T) {
	s, inst := wreckPublicationFixture(t)
	s.publishSnapshot(20)
	if s.Snapshot.Current().Features[0].WreckHeatKnown {
		t.Fatal("first sight of map feature invented a birth")
	}
	before, err := s.Features.RetailFeatureImage()
	if err != nil {
		t.Fatal(err)
	}
	simDraws, crtDraws := s.SimRNG().Draws(), s.CrtRNG().Draws()
	s.noteWreckBirth(inst)
	s.publishSnapshot(21)
	after, err := s.Features.RetailFeatureImage()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("presentation changed saved features: %v", err)
	}
	if s.SimRNG().Draws() != simDraws || s.CrtRNG().Draws() != crtDraws {
		t.Fatal("presentation consumed authoritative RNG")
	}
	s.publishSnapshot(20 + wreckPresentationLifetimeTicks - 1)
	if !s.Snapshot.Current().Features[0].WreckHeatKnown {
		t.Fatal("retired fresh birth history early")
	}
	s.publishSnapshot(20 + wreckPresentationLifetimeTicks)
	if s.Snapshot.Current().Features[0].WreckHeatKnown || len(s.publication.wrecks.births) != 0 {
		t.Fatal("retained expired birth history")
	}
	s.noteWreckBirth(inst)
	s.SeedSessionRNG(7, 11)
	if s.publication.wrecks.births != nil || s.publication.wrecks.instances != nil {
		t.Fatal("battle re-entry retained birth history")
	}
	s.noteWreckBirth(inst)
	s.Features.RemoveFeatureAt(4, 4, features.CauseReclaim)
	s.Snapshot = nil
	s.publishSnapshot(1)
	if len(s.publication.wrecks.births) != 0 {
		t.Fatal("disabled snapshots retained removed wreck")
	}
}
