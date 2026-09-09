package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"testing"
)

func blastSprite(key string, width, damage int32) *content.FeatureDef {
	return &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: key}, Filename: "authored", FootprintX: width, FootprintZ: 1, Damage: damage}
}

// A successor stamps over the next candidate before that cell is visited.
// Its fringe resolves to the already remembered anchor, so the successor
// receives no damage [06 §9.3][05 R-FEAT-01 §3, §5, §8].
func TestBlastDiscoversOverlappingSuccessorLive(t *testing.T) {
	source := blastSprite("source", 1, 10)
	successor := blastSprite("wide-successor", 2, 100)
	source.FeatureDeadDef = successor
	sim := rng.SimulationFromState(42)
	svc, feats, terrain := featureBlastFixture(t, source, 4, 4, &sim)
	if feats.PlaceAt(5, 4, blastSprite("neighbor", 1, 100)) == nil {
		t.Fatal("neighbor refused")
	}
	before := sim
	svc.ExplodeWeaponAt(newCombatFixtureWorld(4, nil), terrain, blastWeapon(128, 10, 0), featureCellCentre(4, 4), 0, 1)
	if got := feats.InstanceAt(4, 4); got == nil || got.Def != successor {
		t.Fatalf("successor = %+v", got)
	}
	if feats.InstanceAt(5, 4) != nil || !terrain.PlotAt(5, 4).IsFringe() {
		t.Fatal("neighbor was not replaced by successor fringe")
	}
	if got := terrain.PlotAt(4, 4).AnchorWord(); got != 0 {
		t.Fatalf("successor damage = %d, want 0", got)
	}
	if cand, ok := feats.AreaCandidateAt(5, 4); !ok || cand.CX != 4 || cand.CZ != 4 {
		t.Fatalf("fringe ownership = %+v, %t", cand, ok)
	}
	if sim.State != before.State || sim.Draws() != before.Draws() {
		t.Fatal("nonflammable blast consumed random values")
	}
}

// After all 64 anchor entries are occupied, further candidates remain
// unremembered. A shrinking footprint must nevertheless disappear from later
// discovery; precollecting its former fringe would hit its successor again
// [06 §9.3][05 R-FEAT-01 §8].
func TestBlastOverflowDiscoversShrinkingFootprintLive(t *testing.T) {
	remembered := blastSprite("remembered", 1, 100)
	sim := rng.SimulationFromState(42)
	svc, feats, terrain := featureBlastFixture(t, remembered, 0, 0, &sim)
	for i := 1; i < 64; i++ {
		if feats.PlaceAt(i%16, i/16, remembered) == nil {
			t.Fatalf("placement %d refused", i)
		}
	}
	successor := blastSprite("small-successor", 1, 100)
	source := blastSprite("unremembered-wide", 2, 10)
	source.FeatureDeadDef = successor
	if feats.PlaceAt(0, 4, source) == nil {
		t.Fatal("wide placement refused")
	}
	before := sim
	svc.ExplodeWeaponAt(newCombatFixtureWorld(4, nil), terrain, blastWeapon(1024, 10, 0), featureCellCentre(8, 8), 0, 1)
	for i := 0; i < 64; i++ {
		if got := terrain.PlotAt(int32(i%16), int32(i/16)).AnchorWord(); got != 10 {
			t.Fatalf("remembered anchor %d damage=%d", i, got)
		}
	}
	if got := feats.InstanceAt(0, 4); got == nil || got.Def != successor {
		t.Fatalf("successor = %+v", got)
	}
	if !terrain.PlotAt(1, 4).IsEmpty() {
		t.Fatal("former fringe survived shrink")
	}
	if got := terrain.PlotAt(0, 4).AnchorWord(); got != 0 {
		t.Fatalf("successor damage = %d, want 0", got)
	}
	if sim.State != before.State || sim.Draws() != before.Draws() {
		t.Fatal("nonflammable blast consumed random values")
	}
}
