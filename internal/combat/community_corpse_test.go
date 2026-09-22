package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestCommunityCorpseFallUsesPlacedFeatureAndOrdinaryGravity(t *testing.T) {
	attrs := make([]formats.TNTAttribute, 16)
	for i := range attrs {
		attrs[i].Height = 32
		attrs[i].Feature = world.PlotFeatureNone
	}
	terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: world.ExpandPlot(attrs, 4, 4), SeaLevel: 10, Gravity: 2}
	s := &Service{Rules: CommunityRules{}}
	s.Community.AirCorpseFall = true
	position := Vec3{X: 16 << 16, Y: 40 << 16, Z: 16 << 16}
	for _, tc := range []struct {
		name               string
		position, velocity Vec3
		sea                uint8
		want               numeric.Fixed
	}{
		{"above land", position, Vec3{}, 10, -1},
		{"on ground", Vec3{X: position.X, Y: 32 << 16, Z: position.Z}, Vec3{}, 10, 0},
		{"water equality", position, Vec3{}, 32, 0},
		{"off map", Vec3{X: -1, Y: position.Y}, Vec3{}, 10, 0},
		{"horizontal motion", position, Vec3{X: 1}, 10, 0},
		{"existing sink", position, Vec3{Y: -11468}, 10, -11468},
	} {
		terrain.SeaLevel = tc.sea
		got := s.rules().CorpseVelocity(s, tc.position, tc.velocity, terrain)
		if got.Y != tc.want || got.X != tc.velocity.X || got.Z != tc.velocity.Z {
			t.Fatalf("%s: %+v", tc.name, got)
		}
	}
	terrain.SeaLevel = 10
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		s.Rules = rules
		fs := features.NewService(terrain, nil, nil, nil)
		def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "falling-wreck"}, FootprintX: 1, FootprintZ: 1, Object: "wreck", Damage: 100}
		corpse := fs.PlaceCorpse(world.Cell{X: 1, Z: 1}, [3]numeric.Fixed{position.X, position.Y, position.Z}, features.Orientation{}, def, false, 0)
		if corpse == nil {
			t.Fatal("corpse refused")
		}
		s.SeedCorpseVelocity(corpse, terrain)
		_, strict := rules.(StrictRules)
		want := numeric.Fixed(-1)
		if strict {
			want = 0
		}
		if corpse.Vy != want {
			t.Fatalf("%T velocity = %d, want %d", rules, corpse.Vy, want)
		}
		fs.TickLifecycle(1)
		if strict {
			if corpse.Y != position.Y || !corpse.Settled {
				t.Fatal("Strict corpse moved")
			}
		} else if corpse.Y != position.Y-1 || corpse.Vy != -3 || corpse.Settled {
			t.Fatalf("gravity integration: y=%d velocity=%d settled=%v", corpse.Y, corpse.Vy, corpse.Settled)
		}
	}
	s.Community.AirCorpseFall = false
	if got := s.rules().CorpseVelocity(s, position, Vec3{}, terrain); got != (Vec3{}) {
		t.Fatal("disabled feature seeded fall")
	}
}
