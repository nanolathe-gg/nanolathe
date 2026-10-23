package content

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// frozenFixtureModel authors a one-piece 3DO: the piece origin's X places the
// geometry and the one vertex's Y sets the model-top height.
func frozenFixtureModel(t *testing.T, translateX, height int32) []byte {
	t.Helper()
	data, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "base", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Translation: [3]int32{translateX << 16, 0, 0},
		Vertices:    []formats.ThreeDOVertex{{Y: height << 16}},
	}}})
	if err != nil {
		t.Fatalf("EncodeThreeDO: %v", err)
	}
	return data
}

// frozenFixtureCOB authors a compiled script in the [fmt cob] container: the
// 44-byte header, the code words, the entry-point and name tables and the
// string pool.
func frozenFixtureCOB(code []uint32, scripts []string, indexes []uint32, pieces []string) []byte {
	const header = 44
	offScriptIndex := uint32(header + len(code)*4)
	offScriptNames := offScriptIndex + uint32(len(scripts)*4)
	offPieceNames := offScriptNames + uint32(len(scripts)*4)
	strStart := offPieceNames + uint32(len(pieces)*4)
	size := int(strStart)
	for _, s := range append(append([]string(nil), scripts...), pieces...) {
		size += len(s) + 1
	}
	data := make([]byte, size)
	put := func(off int, v uint32) { binary.LittleEndian.PutUint32(data[off:], v) }
	put(0, 4)
	put(4, uint32(len(scripts)))
	put(8, uint32(len(pieces)))
	put(12, uint32(len(code)))
	put(24, offScriptIndex)
	put(28, offScriptNames)
	put(32, offPieceNames)
	put(36, header)
	put(40, strStart)
	for i, word := range code {
		put(header+i*4, word)
	}
	cur := strStart
	for i, s := range scripts {
		put(int(offScriptIndex)+i*4, indexes[i])
		put(int(offScriptNames)+i*4, cur)
		copy(data[cur:], s+"\x00")
		cur += uint32(len(s) + 1)
	}
	for i, p := range pieces {
		put(int(offPieceNames)+i*4, cur)
		copy(data[cur:], p+"\x00")
		cur += uint32(len(p) + 1)
	}
	return data
}

func frozenFixtureGAF(t *testing.T, entry string, holds ...uint32) []byte {
	t.Helper()
	var frames []formats.GAFWriteFrame
	for _, hold := range holds {
		frames = append(frames, formats.GAFWriteFrame{Width: 2, Height: 2, Duration: hold, Pixels: []byte{1, 1, 1, 1}})
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: entry, Frames: frames}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// frozenFixture is a small authored content set: a unit that a battle
// creates, a unit no one has built yet, a feature model, an effect bank and a
// feature bank, a map and the default AI profile. No retail bytes.
type frozenFixture struct {
	root string
	fs   *vfs.FS
}

const frozenFixtureCreate = 0x10065000

func newFrozenFixture(t *testing.T) *frozenFixture {
	t.Helper()
	root := t.TempDir()
	writeFrozenFixtureFile(t, root, "objects3d/fixture.3do", frozenFixtureModel(t, 1, 8))
	writeFrozenFixtureFile(t, root, "objects3d/other.3do", frozenFixtureModel(t, 2, 12))
	writeFrozenFixtureFile(t, root, "objects3d/rock.3do", frozenFixtureModel(t, 0, 4))
	writeFrozenFixtureFile(t, root, "scripts/testunit.cob", frozenFixtureCOB([]uint32{frozenFixtureCreate}, []string{"Create"}, []uint32{0}, []string{"base"}))
	writeFrozenFixtureFile(t, root, "scripts/otherunit.cob", frozenFixtureCOB([]uint32{frozenFixtureCreate, frozenFixtureCreate}, []string{"Create"}, []uint32{0}, []string{"base"}))
	writeFrozenFixtureFile(t, root, "anims/fx.gaf", frozenFixtureGAF(t, "smoke 1", 2, 2, 2))
	writeFrozenFixtureFile(t, root, "anims/trees.gaf", frozenFixtureGAF(t, "treeburn", 3, 0, 2))
	writeFrozenFixtureFile(t, root, "maps/test.ota", []byte("[GlobalHeader]\n{\n}\n"))
	writeFrozenFixtureFile(t, root, "ai/default.txt", []byte("plan any\n"))
	return &frozenFixture{root: root, fs: mountFrozenFixture(t, root)}
}

func (f *frozenFixture) write(t *testing.T, logical string, data []byte) {
	t.Helper()
	writeFrozenFixtureFile(t, f.root, logical, data)
}

// catalog builds the catalog a compile of the fixture's current files would
// carry: each unit's model height and program come from those files.
func (f *frozenFixture) catalog(t *testing.T) *Catalog {
	t.Helper()
	unit := func(id uint32, name, object string) *UnitDef {
		model, err := os.ReadFile(filepath.Join(f.root, "objects3d", object+".3do"))
		if err != nil {
			t.Fatal(err)
		}
		three, err := formats.LoadThreeDO(model)
		if err != nil {
			t.Fatal(err)
		}
		script, err := os.ReadFile(filepath.Join(f.root, "scripts", name+".cob"))
		if err != nil {
			t.Fatal(err)
		}
		prog, err := cob.Load(script)
		if err != nil {
			t.Fatal(err)
		}
		top := three.ModelTop()
		return &UnitDef{
			DefinitionHeader: DefinitionHeader{CanonicalKey: name}, UnitDefID: id,
			UnitName: name, ObjectName: object, ModelTopFixed: top, ModelTop: (top >> 16) & 0xFF,
			Script: prog, Limit: -1, LimitEnabled: true,
		}
	}
	// Records are in the compiler's name order, which the name index searches.
	records := []*UnitDef{unit(1, "otherunit", "other"), unit(2, "testunit", "fixture")}
	return &Catalog{
		unitRecords: records,
		Units:       firstUnitNames(records),
		Features: map[string]*FeatureDef{
			"tree1": {DefinitionHeader: DefinitionHeader{CanonicalKey: "tree1"}, Filename: "trees", SeqNameBurn: "treeburn"},
			"rock":  {DefinitionHeader: DefinitionHeader{CanonicalKey: "rock"}, Object: "rock"},
		},
	}
}

func (f *frozenFixture) freeze(t *testing.T, r SimulationInputRequest) *SimulationInputs {
	t.Helper()
	sources, err := CaptureSimulationSources(f.fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Catalog == nil {
		r.Catalog = f.catalog(t)
	}
	if r.MapOTA == "" {
		r.MapOTA = "maps/test.ota"
	}
	inputs, err := FreezeSimulationInputs(sources, r)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	return inputs
}

func manifestEntry(inputs *SimulationInputs, family uint8, key string) (SimulationInput, bool) {
	for _, e := range inputs.Manifest() {
		if e.Family == family && e.Key == key {
			return e, true
		}
	}
	return SimulationInput{}, false
}

// M2-C8: a resource edited during a battle does not reach it — the frozen
// model of a unit not yet built, the sealed view's bytes and the identity all
// stay as admitted — and a second battle admitted after the edit sees a new
// identity that differs in exactly that model.
func TestFrozenInputsKeepTheirContentAfterAnEdit(t *testing.T) {
	f := newFrozenFixture(t)
	first := f.freeze(t, SimulationInputRequest{})
	admitted := first.Digest()
	before, ok := first.Model("OTHER")
	if !ok || len(before.Pieces) != 1 {
		t.Fatalf("never-built unit's model was not frozen: %v %v", before, ok)
	}
	beforeBytes, err := first.Filesystem().ReadFileLimit("objects3d/other.3do", 1<<20)
	if err != nil {
		t.Fatal(err)
	}

	// Same height, different geometry: the catalog stays a valid compile of
	// the edited files, so only the model changes.
	f.write(t, "objects3d/other.3do", frozenFixtureModel(t, 7, 12))

	if again, ok := first.Model("other"); !ok || again != before || again.Pieces[0].Translate != before.Pieces[0].Translate {
		t.Fatalf("frozen model changed under an edit: %v", again)
	}
	if got, err := first.Filesystem().ReadFileLimit("objects3d/other.3do", 1<<20); err != nil || string(got) != string(beforeBytes) {
		t.Fatalf("sealed view served the edit: %v", err)
	}
	if first.Digest() != admitted {
		t.Fatal("frozen identity changed under an edit")
	}

	second := f.freeze(t, SimulationInputRequest{Catalog: f.catalog(t)})
	if second.Digest() == admitted {
		t.Fatal("a battle admitted after the edit kept the old identity")
	}
	edited, ok := second.Model("other")
	if !ok || edited.Pieces[0].Translate == before.Pieces[0].Translate {
		t.Fatal("second battle did not freeze the edited model")
	}
	firstManifest, secondManifest := first.Manifest(), second.Manifest()
	if len(firstManifest) != len(secondManifest) {
		t.Fatalf("manifest sizes %d and %d", len(firstManifest), len(secondManifest))
	}
	for i := range firstManifest {
		changed := firstManifest[i] != secondManifest[i]
		isModel := firstManifest[i].Family == SimulationFamilyModel && firstManifest[i].Key == "objects3d/other.3do"
		if changed != isModel {
			t.Fatalf("entry %+v changed=%v; only the edited model may differ", firstManifest[i], changed)
		}
	}
}

// M2-C8: units not yet created are admitted with their programs and models,
// and the creation path resolves them from the frozen value.
func TestFrozenInputsAdmitANeverCreatedUnit(t *testing.T) {
	f := newFrozenFixture(t)
	inputs := f.freeze(t, SimulationInputRequest{})
	cob, ok := manifestEntry(inputs, SimulationFamilyCOB, "unit/otherunit")
	if !ok || cob.Presence != SimulationInputPresent || cob.Ordinal != 1 {
		t.Fatalf("never-built unit's program entry = %+v, %v", cob, ok)
	}
	if cob.SemanticDigest != programSemanticDigest(inputs.Catalog().Units["otherunit"].Script) {
		t.Fatal("program digest is not the program the binder uses")
	}
	model, ok := manifestEntry(inputs, SimulationFamilyModel, "objects3d/other.3do")
	if !ok || model.Presence != SimulationInputPresent {
		t.Fatalf("never-built unit's model entry = %+v, %v", model, ok)
	}
	if feature, ok := manifestEntry(inputs, SimulationFamilyModel, "objects3d/rock.3do"); !ok || feature.Presence != SimulationInputPresent {
		t.Fatalf("feature model entry = %+v, %v", feature, ok)
	}
	if m, ok := inputs.Model(" Other "); !ok || m.Name != "objects3d/other.3do" {
		t.Fatalf("Model resolved %v, %v; want the creation path's name", m, ok)
	}
	if _, ok := inputs.Model(""); ok {
		t.Fatal("an empty object name resolved a model")
	}
}

// A battle that compiles its animation table and one handed an equal
// precompiled table agree in identity; the precompiled value is the one used.
func TestFrozenSimArtNilAndPrecompiledAgree(t *testing.T) {
	f := newFrozenFixture(t)
	precompiled := CompileSimArt(f.fs, f.catalog(t))
	compiled := f.freeze(t, SimulationInputRequest{})
	supplied := f.freeze(t, SimulationInputRequest{SimArt: precompiled})
	if compiled.Digest() != supplied.Digest() {
		t.Fatal("nil and precompiled animation tables with equal content disagree")
	}
	if supplied.SimArt() != precompiled || compiled.SimArt() == precompiled {
		t.Fatal("frozen inputs did not keep the table they validated")
	}
	if n, ok := compiled.SimArt().EffectEntryFrameCount("", "smoke 1"); !ok || n != 3 {
		t.Fatalf("compiled table smoke frames = %d, %v", n, ok)
	}
	if bank, ok := manifestEntry(compiled, SimulationFamilySimArt, "bank/fx"); !ok || bank.Presence != SimulationInputPresent {
		t.Fatalf("effect bank entry = %+v, %v", bank, ok)
	}
	if seq, ok := manifestEntry(compiled, SimulationFamilySimArt, "sequence/trees|treeburn"); !ok || seq.Presence != SimulationInputPresent {
		t.Fatalf("feature sequence entry = %+v, %v", seq, ok)
	}
}

// A table compiled from another capture is refused, and the refusal leaves the
// capture free to freeze with a table of its own.
func TestFrozenInputsRejectSimArtFromAnotherCapture(t *testing.T) {
	f := newFrozenFixture(t)
	cat := f.catalog(t)
	earlier, err := CaptureSimulationSources(f.fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	foreign := CompileSimArt(earlier.Filesystem(), cat)
	f.write(t, "anims/fx.gaf", frozenFixtureGAF(t, "smoke 1", 5, 5, 5))

	sources, err := CaptureSimulationSources(f.fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	request := SimulationInputRequest{Catalog: cat, MapOTA: "maps/test.ota", SimArt: foreign}
	if _, err := FreezeSimulationInputs(sources, request); !errors.Is(err, ErrSimulationInputNotCaptured) {
		t.Fatalf("table from another capture: %v", err)
	}
	for name, table := range map[string]*SimArt{
		"uncompiled":        {},
		"another catalog":   CompileSimArt(f.fs, &Catalog{}),
		"never compiled fs": CompileSimArt(nil, cat),
	} {
		request.SimArt = table
		if _, err := FreezeSimulationInputs(sources, request); !errors.Is(err, ErrSimulationInputNotCaptured) {
			t.Fatalf("%s table: %v", name, err)
		}
	}
	request.SimArt = nil
	inputs, err := FreezeSimulationInputs(sources, request)
	if err != nil {
		t.Fatalf("refusals consumed the capture: %v", err)
	}
	if holds, ok := inputs.SimArt().EffectEntryHolds("", "smoke 1"); !ok || holds[0] != 5 {
		t.Fatalf("battle compiled holds %v, %v; want the capture's", holds, ok)
	}
}

// A catalog whose programs or model heights the capture does not reproduce
// was compiled from other sources and is refused.
func TestFrozenInputsRejectACatalogFromAnotherCapture(t *testing.T) {
	for _, edit := range []struct {
		name, path string
		data       func(*testing.T) []byte
	}{
		{"program", "scripts/otherunit.cob", func(*testing.T) []byte {
			return frozenFixtureCOB([]uint32{frozenFixtureCreate}, []string{"Create"}, []uint32{0}, []string{"base"})
		}},
		{"model height", "objects3d/other.3do", func(t *testing.T) []byte { return frozenFixtureModel(t, 2, 20) }},
	} {
		t.Run(edit.name, func(t *testing.T) {
			f := newFrozenFixture(t)
			stale := f.catalog(t)
			f.write(t, edit.path, edit.data(t))
			sources, err := CaptureSimulationSources(f.fs, "test")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := FreezeSimulationInputs(sources, SimulationInputRequest{Catalog: stale}); !errors.Is(err, ErrSimulationInputNotCaptured) {
				t.Fatalf("stale catalog: %v", err)
			}
			if _, err := FreezeSimulationInputs(sources, SimulationInputRequest{Catalog: f.catalog(t)}); err != nil {
				t.Fatalf("catalog of the capture's files: %v", err)
			}
		})
	}
}

// A capture freezes once; afterwards its view answers only what it captured.
func TestFrozenInputsFreezeOnceAndSeal(t *testing.T) {
	f := newFrozenFixture(t)
	sources, err := CaptureSimulationSources(f.fs, "test")
	if err != nil {
		t.Fatal(err)
	}
	r := SimulationInputRequest{Catalog: f.catalog(t), MapOTA: "maps/test.ota"}
	inputs, err := FreezeSimulationInputs(sources, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FreezeSimulationInputs(sources, r); err == nil {
		t.Fatal("a capture froze twice")
	}
	if len(inputs.UncapturedLookups()) != 0 {
		t.Fatalf("freeze left uncaptured lookups: %v", inputs.UncapturedLookups())
	}
	f.write(t, "units/late.fbi", []byte("late"))
	if _, err := inputs.Filesystem().ReadFileLimit("units/late.fbi", 64); !errors.Is(err, vfs.ErrNotFound) {
		t.Fatalf("sealed view read a path it never captured: %v", err)
	}
	if got := inputs.UncapturedLookups(); len(got) != 1 || got[0] != "read units/late.fbi" {
		t.Fatalf("uncaptured lookups = %v", got)
	}
	if view, ok := inputs.Filesystem().(interface{ SimulationInputs() *SimulationInputs }); !ok || view.SimulationInputs() != inputs {
		t.Fatal("sealed view does not lead back to its inputs")
	}
	if inputs.Filesystem() == sources.Filesystem() {
		t.Fatal("sealed view is the capture view")
	}
}

// The AI profile's default-file fallback, the map and the extension inputs
// are part of the identity; equivalent mutator spellings agree and an applied
// mutator or another Community table does not.
func TestFrozenInputsRecordFallbacksAndExtensions(t *testing.T) {
	f := newFrozenFixture(t)
	base := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT"})
	ai, ok := manifestEntry(base, SimulationFamilyAI, "ai/missing.txt")
	if !ok || ai.Presence != SimulationInputFallback || ai.FallbackKey != "ai/default.txt" {
		t.Fatalf("AI fallback entry = %+v, %v", ai, ok)
	}
	if ota, ok := manifestEntry(base, SimulationFamilyMap, "maps/test.ota"); !ok || ota.Presence != SimulationInputPresent {
		t.Fatalf("map entry = %+v, %v", ota, ok)
	}
	identity := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", Mutators: Mutators{Health: Factor{Num: 1, Den: 1}, BuilderSpeed: Factor{Num: 1, Den: 1}}})
	if identity.Digest() != base.Digest() {
		t.Fatal("identity mutator spellings disagree")
	}
	// Builder speed's own index joins the extension digest only when set.
	builder := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", Mutators: Mutators{BuilderSpeed: Factor{Num: 5, Den: 1}}})
	if builder.Digest() == base.Digest() {
		t.Fatal("Builder speed did not change the identity")
	}
	// The Smart makers switch follows it in the tail, so each combination is
	// its own identity and off is the base (docs/DESIGN_MODS_MUTATORS.md
	// "Smart makers").
	smart := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", Mutators: Mutators{SmartMakers: SwitchOn()}})
	both := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", Mutators: Mutators{BuilderSpeed: Factor{Num: 5, Den: 1}, SmartMakers: SwitchOn()}})
	off := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", Mutators: Mutators{SmartMakers: Factor{Num: 1, Den: 1}}})
	if smart.Digest() == base.Digest() || smart.Digest() == builder.Digest() || both.Digest() == builder.Digest() || both.Digest() == smart.Digest() || off.Digest() != base.Digest() {
		t.Fatal("the Smart makers tail must give each combination its own identity and off the base")
	}
	prepared := f.catalog(t).Clone()
	if err := prepared.ApplyMutators(Mutators{BuildSpeed: Factor{Num: 2, Den: 1}}); err != nil {
		t.Fatal(err)
	}
	prepared.Units["testunit"].BuildTime = 100 // an effective value the clone carries
	mutated := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", Catalog: prepared, Mutators: Mutators{BuildSpeed: Factor{Num: 2, Den: 1}}})
	if mutated.Digest() == base.Digest() {
		t.Fatal("applied mutators did not change the identity")
	}
	community := f.freeze(t, SimulationInputRequest{AIProfile: "Missing.TXT", CommunityDigest: [32]byte{1}})
	if community.Digest() == base.Digest() {
		t.Fatal("the Community table did not change the identity")
	}
	sources, _ := CaptureSimulationSources(f.fs, "test")
	if _, err := FreezeSimulationInputs(sources, SimulationInputRequest{Catalog: f.catalog(t), Mutators: Mutators{Health: Factor{Num: 5, Den: 1}}}); err == nil {
		t.Fatal("a factor off the step list was frozen")
	}
	sources, _ = CaptureSimulationSources(f.fs, "test")
	if _, err := FreezeSimulationInputs(sources, SimulationInputRequest{Catalog: f.catalog(t), Mutators: Mutators{BuilderSpeed: Factor{Num: 4, Den: 1}}}); err == nil {
		t.Fatal("a Builder speed factor off its own list was frozen")
	}
	sources, _ = CaptureSimulationSources(f.fs, "test")
	if _, err := FreezeSimulationInputs(sources, SimulationInputRequest{Catalog: f.catalog(t), Mutators: Mutators{SmartMakers: Factor{Num: 3, Den: 1}}}); err == nil {
		t.Fatal("a Smart makers step off its switch was frozen")
	}
}

// The products a builder's pages install are read only by the Modern AI, so
// they join the identity as an AI-family entry when a Modern computer plays
// and leave every other battle's identity as it was.
func TestFrozenInputsCarryInstalledProductsForModernAI(t *testing.T) {
	f := newFrozenFixture(t)
	withMenu := func(installed ...string) *Catalog {
		c := f.catalog(t).Clone()
		c.BuildMenus = map[string]*BuildMenuPage{"testunit": {
			DefinitionHeader:  DefinitionHeader{CanonicalKey: "testunit"},
			Builder:           "testunit",
			InstalledProducts: installed,
		}}
		return c
	}
	plain := f.freeze(t, SimulationInputRequest{Catalog: withMenu()})
	if _, ok := manifestEntry(plain, SimulationFamilyAI, "modern/installed-products"); ok {
		t.Fatal("a battle without a Modern computer carries the installed products")
	}
	if other := f.freeze(t, SimulationInputRequest{Catalog: withMenu("otherunit")}); other.Digest() != plain.Digest() {
		t.Fatal("installed products changed the identity of a battle without a Modern computer")
	}
	modern := f.freeze(t, SimulationInputRequest{Catalog: withMenu(), ModernAI: true})
	if _, ok := manifestEntry(modern, SimulationFamilyAI, "modern/installed-products"); !ok {
		t.Fatal("a Modern battle lacks the installed-products entry")
	}
	if installed := f.freeze(t, SimulationInputRequest{Catalog: withMenu("otherunit"), ModernAI: true}); installed.Digest() == modern.Digest() {
		t.Fatal("installed products did not change a Modern battle's identity")
	}
}
