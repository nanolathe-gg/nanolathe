package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"

	"github.com/nanolathe/nanolathe/internal/world"
)

// featureBlastFixture builds a terrain holding one stamped feature and a combat
// service wired to its runtime, the way composition wires them.
func featureBlastFixture(t *testing.T, def *content.FeatureDef, cx, cz int, sim *rng.Simulation) (*Service, *features.Service, *world.Terrain) {
	t.Helper()
	const side = 16
	attrs := make([]formats.TNTAttribute, side*side)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	terrain := &world.Terrain{
		CellW:       side,
		CellH:       side,
		Plot:        world.ExpandPlot(attrs, side, side),
		FeatureDefs: []*content.FeatureDef{def},
		SeaLevel:    10,
	}
	feats := features.NewService(terrain, sim, nil, nil)
	if feats.PlaceAt(cx, cz, def) == nil {
		t.Fatalf("feature placement at (%d,%d) failed", cx, cz)
	}
	return &Service{Features: feats}, feats, terrain
}

func blastWeapon(area, damage, firestarter int32) *content.WeaponDef {
	w := &content.WeaponDef{AreaOfEffect: area, DamageDefault: damage, Firestarter: firestarter}
	w.CanonicalKey = "blast"
	return w
}

func featureCellCentre(cx, cz int) Vec3 {
	return Vec3{
		X: numeric.FixedFromInt(int64(cx)*16 + 8),
		Y: numeric.FixedFromInt(10),
		Z: numeric.FixedFromInt(int64(cz)*16 + 8),
	}
}

// TestBlastAccumulatesTheAuthoredDefaultDamageOnAFeature locks [06 §13.1] /
// [05 R-FEAT-01 §8] step 6: the amount a blast lands on a feature is the
// weapon's authored DEFAULT damage word exactly — no area falloff, no armour
// table, no veterancy — accumulated on the anchor, and the feature is destroyed
// when the sum reaches the definition's damage capacity (`<` survives, so `>=`
// destroys).
func TestBlastAccumulatesTheAuthoredDefaultDamageOnAFeature(t *testing.T) {
	def := &content.FeatureDef{Damage: 10, FootprintX: 1, FootprintZ: 1}
	def.CanonicalKey = "rock"
	sim := rng.SimulationFromState(1)
	svc, feats, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	w := newCombatFixtureWorld(4, nil)
	weapon := blastWeapon(64, 4, 0) // radius 32, four damage a hit

	before := sim.Draws()
	svc.ExplodeWeaponAt(w, terrain, weapon, featureCellCentre(8, 8), 0, 1)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 4 {
		t.Fatalf("accumulator %d after one hit, want the authored default 4 [05 R-FEAT-01 §8 step 6]", got)
	}
	svc.ExplodeWeaponAt(w, terrain, weapon, featureCellCentre(8, 8), 0, 2)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 8 {
		t.Fatalf("accumulator %d after two hits, want 8", got)
	}
	if feats.InstanceAt(8, 8) == nil {
		t.Fatal("the feature died at 8 of 10 capacity; `<` must survive")
	}
	svc.ExplodeWeaponAt(w, terrain, weapon, featureCellCentre(8, 8), 0, 3)
	if feats.InstanceAt(8, 8) != nil || !terrain.PlotAt(8, 8).IsEmpty() {
		t.Fatalf("the feature survived 12 of 10 capacity; `>=` must destroy")
	}
	if got := sim.Draws() - before; got != 0 {
		t.Fatalf("feature damage consumed %d simulation draws, want none — only ignition draws [01 §7.5]", got)
	}
}

// TestBlastDamageIgnoresFalloff is the same contract from the other side: a
// feature at the far edge of the radius takes the same full default word as one
// at the impact point, because the falloff computed for units in that same loop
// is not applied here [05 R-FEAT-01 §8].
func TestBlastDamageIgnoresFalloff(t *testing.T) {
	def := &content.FeatureDef{Damage: 100, FootprintX: 1, FootprintZ: 1}
	def.CanonicalKey = "rock"
	sim := rng.SimulationFromState(1)
	svc, _, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	w := newCombatFixtureWorld(4, nil)
	weapon := blastWeapon(128, 7, 0) // radius 64
	// Impact three cells away: well inside the radius, far from zero distance.
	svc.ExplodeWeaponAt(w, terrain, weapon, featureCellCentre(11, 8), 0, 1)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 7 {
		t.Fatalf("accumulator %d at distance, want the undiminished 7 [05 R-FEAT-01 §8]", got)
	}
}

// TestBlastOutsideRadiusLeavesTheFeatureAlone locks the strict `< R` acceptance
// [06 §9.3] on the feature branch.
func TestBlastOutsideRadiusLeavesTheFeatureAlone(t *testing.T) {
	def := &content.FeatureDef{Damage: 100, FootprintX: 1, FootprintZ: 1}
	def.CanonicalKey = "rock"
	sim := rng.SimulationFromState(1)
	svc, _, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	w := newCombatFixtureWorld(4, nil)
	// Radius 8; the feature centre is 48 world units away.
	svc.ExplodeWeaponAt(w, terrain, blastWeapon(16, 7, 0), featureCellCentre(11, 8), 0, 1)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 0 {
		t.Fatalf("accumulator %d for a feature outside the radius, want 0", got)
	}
}

// TestFirestarterBlastIgnitesWithOneSimulationDraw locks the ignition half of
// [05 R-FEAT-01 §8] step 5 and the countdown of §9 step 4: a flammable feature
// hit by a weapon whose firestarter byte is nonzero IGNITES and takes no damage
// on that hit, spending exactly one simulation draw, and the countdown lands in
// `half .. 2·half − 1` for `half = sparkTicks >> 1`.
func TestFirestarterBlastIgnitesWithOneSimulationDraw(t *testing.T) {
	// A sprite definition whose burn sequence RESOLVES: ignition needs the
	// sequence's frame words from the content metadata seam, and a 3D
	// definition never burns [05 R-FEAT-01 §9 step 1].
	def := &content.FeatureDef{Damage: 10, FootprintX: 1, FootprintZ: 1, Flamable: true, SparkTime: 150, Filename: "trees"}
	def.CanonicalKey = "tree"
	def.SeqNameBurn = "treeburn"
	sim := rng.SimulationFromState(12345)
	svc, feats, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	feats.SequenceFrames = func(_ *content.FeatureDef, selector uint8) []int32 {
		if selector != 0 {
			return nil
		}
		return []int32{100}
	}
	w := newCombatFixtureWorld(4, nil)

	before := sim.Draws()
	svc.ExplodeWeaponAt(w, terrain, blastWeapon(64, 4, 1), featureCellCentre(8, 8), 0, 1)
	if got := sim.Draws() - before; got != 1 {
		t.Fatalf("ignition consumed %d simulation draws, want exactly one [05 R-FEAT-01 §9 step 4]", got)
	}
	inst := feats.InstanceAt(8, 8)
	if inst == nil || !inst.IsBurning {
		t.Fatalf("the firestarter impact did not ignite: %#v", inst)
	}
	if got := terrain.PlotAt(8, 8).AnchorWord(); got == 4 {
		t.Fatal("an ignition also accumulated damage; ignition takes precedence [05 R-FEAT-01 §8 step 5]")
	}
	if inst.BurnCountdown < 75 || inst.BurnCountdown > 149 {
		t.Fatalf("countdown %d, want 75..149 for the shipped 150 spark ticks [05 R-FEAT-01 §9]", inst.BurnCountdown)
	}
}

// TestUnitsOnlySkipsTheFeatureWalk locks the one weapon flag that removes the
// feature branch entirely [06 §9.3][05 R-FEAT-01 §8].
func TestUnitsOnlySkipsTheFeatureWalk(t *testing.T) {
	def := &content.FeatureDef{Damage: 100, FootprintX: 1, FootprintZ: 1}
	def.CanonicalKey = "rock"
	sim := rng.SimulationFromState(1)
	svc, _, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	w := newCombatFixtureWorld(4, nil)
	weapon := blastWeapon(64, 7, 0)
	weapon.UnitsOnly = true
	svc.ExplodeWeaponAt(w, terrain, weapon, featureCellCentre(8, 8), 0, 1)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 0 {
		t.Fatalf("accumulator %d under unitsonly, want the feature walk skipped", got)
	}
}

// TestOneBlastDamagesOneFeatureOnce locks the 64-entry anchor memory
// [06 §9.3][05 R-FEAT-01 §8]: a footprint covering several cells of the blast
// is still damaged once, because the entry is called with the ANCHOR and the
// anchor is deduplicated.
func TestOneBlastDamagesOneFeatureOnce(t *testing.T) {
	def := &content.FeatureDef{Damage: 100, FootprintX: 3, FootprintZ: 3}
	def.CanonicalKey = "bigrock"
	sim := rng.SimulationFromState(1)
	svc, _, terrain := featureBlastFixture(t, def, 8, 8, &sim)
	w := newCombatFixtureWorld(4, nil)
	svc.ExplodeWeaponAt(w, terrain, blastWeapon(128, 5, 0), featureCellCentre(9, 9), 0, 1)
	if got := terrain.PlotAt(8, 8).AnchorWord(); got != 5 {
		t.Fatalf("accumulator %d, want one application of 5 for a nine-cell footprint", got)
	}
}
