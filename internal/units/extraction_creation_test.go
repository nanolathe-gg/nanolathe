package units

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// extractorScriptProgram is an authored stand-in for the shape every stock
// extractor script has: a `SetSpeed` entry that takes the creation-time
// footprint accumulator and drives an animation with it. Nothing here is
// copied retail data — the opcodes are the documented COB encoding
// [04 §4.3][04 §4.6] and the body is a push, a push of argument word zero and
// a spin. `spin` pops the speed and then the acceleration under it and stores
// speed divided by the latched tick denominator, truncated toward zero, so the
// piece advances by exactly footprintSum/30 per drained tick [04 §4.6].
func extractorScriptProgram() *cob.Program {
	return &cob.Program{
		Code: []uint32{
			// SetSpeed at word 0.
			0x10021001, 0, // push acceleration 0 — immediate, no ramp
			0x10021002, 0, // push argument word zero: the footprint accumulator
			0x10003000, 1, model.AxisY, // spin piece 1 about Y
			0x10021001, 0,
			0x10065000, // return
			// Create at word 9.
			0x10021001, 0,
			0x10065000,
		},
		Scripts:     map[string]int{"SetSpeed": 0, "Create": 9},
		ScriptsByID: []int{0, 9},
		Pieces:      []string{"base", "arms"},
	}
}

func extractorFixtureDef(extractsMetal float64, footX, footZ int32) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("extractorfixture")},
		UnitName:         "extractorfixture",
		MaxDamage:        100,
		Limit:            -1,
		ExtractsMetal:    extractsMetal,
		FootprintX:       footX,
		FootprintZ:       footZ,
		Script:           extractorScriptProgram(),
	}
}

// seededTerrain is an eight-by-eight canonical plot whose every cell carries
// the same authored surface-metal byte, which is what a canonical map's plot
// holds [05 R-PROD-01 §6]: the schema value is written to every cell and the
// attribute pass never touches the metal byte again.
func seededTerrain(t *testing.T, surfaceMetal int32) *world.Terrain {
	t.Helper()
	const cells = 8
	attrs := make([]formats.TNTAttribute, cells*cells)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	ter := &world.Terrain{
		CellW:   cells,
		CellH:   cells,
		Version: world.VersionCanonical,
		Plot:    world.ExpandPlot(attrs, cells, cells),
	}
	if err := ter.ApplySchema(&content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: surfaceMetal}}}, 0); err != nil {
		t.Fatalf("seed surface metal: %v", err)
	}
	return ter
}

// cellCentre is the world coordinate of cell c's origin. A unit created there
// stamps at cell c, so its footprint rectangle starts at c minus half the
// footprint, the corner [05 R-PROD-01 §6]'s walk begins at.
func cellCentre(c int32) numeric.Fixed {
	return world.CellToWorld(c)
}

// TestCreatorSamplesExtractionForEveryCreationPath locks the move of the
// extraction sample into the creator [05 R-PROD-01 §6]. The relationship under
// test is that a unit created directly — the path resurrection, the initial-
// mission interpreter and save reconstruction take — reads exactly the rate a
// unit placed through a session's own battle entry reads, because both now run
// the same sampler inside World.Create. Before this, only the four placement
// call sites sampled and a direct Create yielded no rate at all, which is a
// silent zero in the settlement: it reads the stored rate, never
// `extractsmetal` [05 R-PROD-01 §1].
func TestCreatorSamplesExtractionForEveryCreationPath(t *testing.T) {
	const surfaceMetal = 3
	const footprint = 3
	const extractsMetal = 2.0

	ter := seededTerrain(t, surfaceMetal)
	def := extractorFixtureDef(extractsMetal, footprint, footprint)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)
	w.SetExtractionSampler(ter)

	// The placement call sites this replaces computed the rectangle's minimum
	// corner as the unit's own cell less half the footprint, then asked the
	// plot for the sum. That expression is the relationship, not a number
	// copied from anywhere.
	const cell = int32(4)
	wantRate, wantSum, err := ter.SampleMetalWithFootprintSum(cell-footprint/2, cell-footprint/2, footprint, footprint, extractsMetal)
	if err != nil {
		t.Fatalf("reference sample: %v", err)
	}

	h, err := w.Create(def, 0, cellCentre(cell), 0, cellCentre(cell))
	if err != nil {
		t.Fatalf("direct create: %v", err)
	}
	u := w.Unit(h)
	if u.SpotMetal != wantRate {
		t.Fatalf("a directly created extractor read rate %v, want the placed rate %v [05 R-PROD-01 §6]", u.SpotMetal, wantRate)
	}
	// Every covered cell contributes its metal byte plus one, so a nine-cell
	// footprint over metal byte 3 sums to 36 and the rate is that times the
	// multiplier. Asserting the arithmetic as well as the relationship catches
	// a sampler that agrees with itself while being wrong.
	if wantSum != (surfaceMetal+1)*footprint*footprint {
		t.Fatalf("footprint accumulator %d, want %d: Σ(byte+1) over the covered cells", wantSum, (surfaceMetal+1)*footprint*footprint)
	}

	// The accumulator also reaches the script as the deferred SetSpeed of
	// [04 R-COB-04 §9]: the fixture spins its second piece at sum/30 per tick.
	vm := u.GetScript()
	if vm == nil {
		t.Fatal("fixture extractor has no script VM")
	}
	before := vm.Pieces[1].RotY
	for i := 0; i < 10; i++ {
		vm.Drain(1)
	}
	if got, want := vm.Pieces[1].RotY-before, uint16(int32(wantSum)/30*10); got != want {
		t.Fatalf("the creator's SetSpeed carried %d over ten ticks, want %d for accumulator %d [04 R-COB-04 §9]", got, want, wantSum)
	}
}

// TestCreatorSamplesAnExtractorAgainstTheMapEdge is the creator's half of
// [05 R-PROD-01 §6]'s per-cell bounds test: an extractor whose stamped
// rectangle leaves the map keeps the sum of the cells that are on it. The
// partial sum matters because the settlement reads the stored rate and never
// `extractsmetal` [05 R-PROD-01 §1] — a rejected sample is silently a
// non-extractor.
func TestCreatorSamplesAnExtractorAgainstTheMapEdge(t *testing.T) {
	const surfaceMetal = 3
	ter := seededTerrain(t, surfaceMetal) // eight by eight
	def := extractorFixtureDef(1, 3, 3)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)
	w.SetExtractionSampler(ter)

	// Cell 0 with a three-wide footprint starts the rectangle at cell -1, so
	// one column and one row lie off the map and four cells remain.
	h, err := w.Create(def, 0, cellCentre(0), 0, cellCentre(0))
	if err != nil {
		t.Fatalf("create at the map corner: %v", err)
	}
	if got, want := w.Unit(h).SpotMetal, float32(4*(surfaceMetal+1)); got != want {
		t.Fatalf("an extractor on the map corner read rate %v, want %v — the four covered cells, off-map coordinates contributing nothing [05 R-PROD-01 §6]", got, want)
	}
}

// TestCreatorLeavesNonExtractorsAlone locks the gate: the sample runs only when
// the definition's `extractsmetal` is strictly greater than zero, and the
// script notification is part of the same act, so a non-extractor's script is
// never started with a footprint it has no use for [05 R-PROD-01 §6].
func TestCreatorLeavesNonExtractorsAlone(t *testing.T) {
	ter := seededTerrain(t, 3)
	def := extractorFixtureDef(0, 3, 3)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)
	w.SetExtractionSampler(ter)

	h, err := w.Create(def, 0, cellCentre(4), 0, cellCentre(4))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	if u.SpotMetal != 0 {
		t.Fatalf("a definition that does not extract got rate %v, want 0", u.SpotMetal)
	}
	vm := u.GetScript()
	before := vm.Pieces[1].RotY
	for i := 0; i < 10; i++ {
		vm.Drain(1)
	}
	if got := vm.Pieces[1].RotY - before; got != 0 {
		t.Fatalf("a non-extractor's SetSpeed ran: piece turned %d over ten ticks", got)
	}
}

// TestForcedSlotCreationSamplesExtraction covers the save-reconstruction
// allocator, the second creation entry point: it is a creation, so it samples
// like the first [05 R-PROD-01 §6]. The restore adapter that follows it
// overwrites the rate from the save image where one is present; a unit the
// restore creates without one still gets a real rate rather than a zero.
func TestForcedSlotCreationSamplesExtraction(t *testing.T) {
	ter := seededTerrain(t, 5)
	def := extractorFixtureDef(1, 2, 2)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)
	w.SetExtractionSampler(ter)

	const cell = int32(4)
	wantRate, _, err := ter.SampleMetalWithFootprintSum(cell-1, cell-1, 2, 2, 1)
	if err != nil {
		t.Fatalf("reference sample: %v", err)
	}
	h, err := w.CreateWithForcedSlot(def, 0, cellCentre(cell), 0, cellCentre(cell), 3)
	if err != nil {
		t.Fatalf("forced-slot create: %v", err)
	}
	if got := w.Unit(h).SpotMetal; got != wantRate {
		t.Fatalf("forced-slot creation read rate %v, want %v [05 R-PROD-01 §6]", got, wantRate)
	}
}

// TestCreatorWithoutASamplerLeavesTheRateAlone covers the fixture and
// no-map cases: with no plot bound the creator has nothing to read, and it
// leaves the record's zero rather than inventing a rate.
func TestCreatorWithoutASamplerLeavesTheRateAlone(t *testing.T) {
	def := extractorFixtureDef(2, 3, 3)
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newFixtureWorld(4, cat)

	h, err := w.Create(def, 0, cellCentre(4), 0, cellCentre(4))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := w.Unit(h).SpotMetal; got != 0 {
		t.Fatalf("an unbound creator produced rate %v, want 0", got)
	}
}
