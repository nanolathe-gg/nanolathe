package client

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestModelTextureRegistryBindsLoadedModelIdentitiesAndTopology(t *testing.T) {
	fs, cat, terrain, wreck, _ := modelTextureBindingFixture(t)
	r, err := NewModelTextureRegistry(fs, cat, terrain, len(terrain.FeatureDefs))
	if err != nil {
		t.Fatal(err)
	}

	first := r.unitModel("alpha", 1, "shared")
	second := r.unitModel("beta", 2, "shared")
	if first == nil || second == nil || first == second {
		t.Fatalf("same-file unit definitions did not receive distinct loads: %p %p", first, second)
	}
	if got := r.unitModel("alpha", 1, "shared"); got != first {
		t.Fatalf("one definition did not retain its loaded model: %p want %p", got, first)
	}
	cl := &Client{modelTextures: r}
	if got := cl.modelForDebris(frame.DebrisView{DefName: "alpha", DefID: 1, Model: "shared"}); got != first {
		t.Fatalf("debris alpha model = %p, want definition cursor %p", got, first)
	}
	if got := cl.modelForDebris(frame.DebrisView{DefName: "beta", DefID: 2, Model: "shared"}); got != second {
		t.Fatalf("debris beta model = %p, want definition cursor %p", got, second)
	}
	beforePicking := len(r.players)
	if got := cl.HullModel("SHARED"); got != first.compiled {
		t.Fatalf("picker lost the authored model-name lookup: %p want %p", got, first.compiled)
	}
	if len(r.players) != beforePicking {
		t.Fatal("geometry lookup registered texture players")
	}
	firstWeapon := r.projectileModel(7, "shared")
	secondWeapon := r.projectileModel(8, "shared")
	if firstWeapon == nil || firstWeapon != secondWeapon {
		t.Fatalf("same-file weapons did not reuse the first loaded model: %p %p", firstWeapon, secondWeapon)
	}
	if got, want := r.featureOrder, []string{"root", "stamped", "corpse", "wreck", "wreck2", "ash"}; !sameStrings(got, want) {
		t.Fatalf("feature admissions = %v, want %v", got, want)
	}
	if r.modelFor(modelLoadFeature, wreck.CanonicalKey, wreck.Object) == nil {
		t.Fatal("referenced but unplaced wreck did not bind before tick one")
	}
	corpse := r.loads[modelTextureLoadKey{kind: modelLoadFeature, id: "corpse"}]
	wreck2 := r.loads[modelTextureLoadKey{kind: modelLoadFeature, id: "wreck2"}]
	if corpse == nil || wreck2 == nil || corpse == wreckModelFor(r, "wreck") || corpse == wreck2 || wreckModelFor(r, "wreck") == wreck2 {
		t.Fatal("same-file feature definitions did not retain distinct model loads")
	}

	load := r.unitByID[1]
	wantOrder := []modelTexturePrimitiveKey{
		{load: load, piece: 3, primitive: 0}, // root sibling subtree
		{load: load, piece: 2, primitive: 0}, // child sibling subtree
		{load: load, piece: 1, primitive: 0}, // child subtree
		{load: load, piece: 0, primitive: 0}, // root primitive
	}
	for i, key := range wantOrder {
		position := playerPosition(r.players, r.bindings[key])
		if position < 0 {
			t.Fatalf("missing primitive %+v", key)
		}
		if i != 0 && position != playerPosition(r.players, r.bindings[wantOrder[i-1]])+1 {
			t.Fatalf("topology binding %d followed a different subtree order", i)
		}
	}

	// A primary ten-frame entry is an ordinary sequence. LOGOS supplies a team
	// frame only after the primary bank misses.
	primary, ok := r.resolve("tenprimary")
	if !ok || primary.kind != texAnimated {
		t.Fatalf("primary ten-frame texture resolved as %+v, want ordinary animation", primary)
	}
	fallback, ok := r.resolve("fallbackteam")
	if !ok || fallback.kind != texTeam {
		t.Fatalf("LOGOS fallback resolved as %+v, want team texture", fallback)
	}

	nonLoop := r.bindings[modelTexturePrimitiveKey{load: load, piece: 1, primitive: 0}]
	loop := r.bindings[modelTexturePrimitiveKey{load: load, piece: 3, primitive: 0}]
	r.StepPhase7()
	r.StepPhase7()
	if _, ok := nonLoop.player.FrameIndex(); ok {
		t.Fatal("non-loop GAF entry remained attached after its final frame")
	}
	if index, ok := loop.player.FrameIndex(); !ok || index != 0 {
		t.Fatalf("looping GAF entry index = %d, active=%v, want frame zero", index, ok)
	}
	wreckLoad := modelTextureLoadKey{kind: modelLoadFeature, id: "wreck"}
	wreckModel := r.loads[wreckLoad]
	if wreckModel == nil {
		t.Fatal("referenced wreck has no feature load")
	}
	animated, ok := r.resolve("tenprimary")
	if got := r.animatedFrame(wreckModel.compiled, 0, 0, animated); !ok || got != animated.entry.Frames[2].Frame {
		t.Fatal("referenced wreck did not advance with the first phase-7 ticks")
	}
}

func TestModelTextureRegistrySeparatesRestoreSuffixAfterNormalLinks(t *testing.T) {
	fs, cat, terrain, _, _ := modelTextureBindingFixture(t)
	root := cat.Features["root"]
	wreck := cat.Features["wreck"]
	stamped := cat.Features["stamped"]
	terrain.FeatureDefs = []*content.FeatureDef{root, wreck, stamped}

	r, err := NewModelTextureRegistry(fs, cat, terrain, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.featureOrder, []string{"root", "corpse", "wreck", "wreck2", "ash", "stamped"}; !sameStrings(got, want) {
		t.Fatalf("feature admissions = %v, want %v", got, want)
	}
	ordinaryTerrain := *terrain
	ordinaryTerrain.FeatureDefs = terrain.FeatureDefs[:1]
	ordinary, err := NewModelTextureRegistry(fs, cat, &ordinaryTerrain, 1)
	if err != nil {
		t.Fatal(err)
	}
	added := 0
	for key := range r.bindings {
		if key.load == (modelTextureLoadKey{kind: modelLoadFeature, id: "stamped"}) {
			added++
		}
	}
	if len(r.players) != len(ordinary.players)+added {
		t.Fatal("restore added cursors beyond the newly admitted stamped definition")
	}
	beforeModel := wreckModelFor(r, "wreck")
	beforePlayers := append([]phase7Stepper(nil), r.players...)
	r.AdmitFeatureDefinition(wreck)
	if wreckModelFor(r, "wreck") != beforeModel || len(r.players) != len(beforePlayers) {
		t.Fatal("duplicate restore admission replaced its model or added a player")
	}
	for i, player := range beforePlayers {
		if r.players[i] != player {
			t.Fatal("duplicate restore admission replaced an earlier cursor")
		}
	}
}

func TestModelTextureRegistryRejectsMutatedModelProvider(t *testing.T) {
	fs, cat, terrain, _, modelPath := modelTextureBindingFixture(t)
	if err := os.WriteFile(modelPath, []byte("not a 3do"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewModelTextureRegistry(fs, cat, terrain, len(terrain.FeatureDefs)); err == nil {
		t.Fatal("constructor accepted a model provider mutated after catalog validation")
	}
}

func TestModelTextureRegistryOnlyPreparesRequestedFeatureModels(t *testing.T) {
	fs, cat, terrain, _, _ := modelTextureBindingFixture(t)
	unused := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "unused"}, Object: "missing"}
	cat.Features["unused"] = unused
	if _, err := NewModelTextureRegistry(fs, cat, terrain, len(terrain.FeatureDefs)); err != nil {
		t.Fatalf("unused feature model blocked battle: %v", err)
	}
	// A restore suffix can request a definition absent from the terrain and
	// corpse roots. It must keep the same required-model failure policy.
	terrain.FeatureDefs = append(terrain.FeatureDefs, unused)
	if _, err := NewModelTextureRegistry(fs, cat, terrain, len(terrain.FeatureDefs)-1); err == nil {
		t.Fatal("requested restore feature accepted its missing model")
	}
}

func playerPosition(players []phase7Stepper, want phase7Stepper) int {
	for i, player := range players {
		if player == want {
			return i
		}
	}
	return -1
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func wreckModelFor(r *ModelTextureRegistry, id string) *unitModel {
	return r.loads[modelTextureLoadKey{kind: modelLoadFeature, id: id}]
}

func modelTextureBindingFixture(t *testing.T) (*vfs.FS, *content.Catalog, *world.Terrain, *content.FeatureDef, string) {
	t.Helper()
	quad := func(texture string) formats.ThreeDOObject {
		return formats.ThreeDOObject{
			Version: 1, Selection: -1, Name: texture,
			Vertices:   []formats.ThreeDOVertex{{}, {X: 1}, {X: 1, Y: 1}, {Y: 1}},
			Primitives: []formats.ThreeDOPrimitive{{TextureName: texture, VertexIndices: []uint16{0, 1, 2, 3}}},
		}
	}
	model := &formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{
		quad("tenprimary"), quad("loop"), quad("nonloop"), quad("ordinary"),
	}}
	model.Objects[0].NextSibling = 1
	model.Objects[0].FirstChild = 2
	model.Objects[2].NextSibling = 3
	threeDO, err := formats.EncodeThreeDO(model)
	if err != nil {
		t.Fatalf("encode 3do fixture: %v", err)
	}
	frame := func(color byte) formats.GAFWriteFrame {
		return formats.GAFWriteFrame{Width: 1, Height: 1, Duration: 1, Pixels: []byte{color}}
	}
	ten := make([]formats.GAFWriteFrame, 10)
	for i := range ten {
		ten[i] = frame(byte(i + 1))
	}
	primary, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "tenprimary", Loop: true, Frames: ten},
		{Name: "loop", Loop: true, Frames: []formats.GAFWriteFrame{frame(1), frame(2)}},
		{Name: "nonloop", Frames: []formats.GAFWriteFrame{frame(3), frame(4)}},
		{Name: "ordinary", Loop: true, Frames: []formats.GAFWriteFrame{frame(5), frame(6)}},
	})
	if err != nil {
		t.Fatalf("encode primary GAF fixture: %v", err)
	}
	logos, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "fallbackteam", Frames: ten}})
	if err != nil {
		t.Fatalf("encode LOGOS fixture: %v", err)
	}
	dir := t.TempDir()
	for path, data := range map[string][]byte{
		"objects3d/shared.3do": threeDO,
		"textures/primary.gaf": primary,
		"textures/logos.gaf":   logos,
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	root := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "root"}, FeatureDead: "wreck", FeatureReclamate: "wreck2", FeatureBurnt: "ash"}
	stamped := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "stamped"}, Object: "shared"}
	corpse := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "corpse"}, Object: "shared"}
	wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"}, Object: "shared", FeatureBurnt: "root"}
	wreck2 := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck2"}, Object: "shared", FeatureBurnt: "root"}
	ash := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "ash"}, Object: "shared", FeatureBurnt: "root"}
	terrain := &world.Terrain{FeatureNames: []string{"root"}, FeatureDefs: []*content.FeatureDef{root, stamped}}
	return fs, &content.Catalog{
		Units: map[string]*content.UnitDef{
			"alpha": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "alpha"}, UnitDefID: 1, ObjectName: "shared", Corpse: "corpse"},
			"beta":  {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "beta"}, UnitDefID: 2, ObjectName: "shared"},
		},
		Weapons: map[string]*content.WeaponDef{
			"weaponone": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "weaponone"}, ID: 7, Model: "shared"},
			"weapontwo": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "weapontwo"}, ID: 8, Model: "shared"},
		},
		Features: map[string]*content.FeatureDef{"root": root, "stamped": stamped, "corpse": corpse, "wreck": wreck, "wreck2": wreck2, "ash": ash},
	}, terrain, wreck, filepath.Join(dir, "objects3d/shared.3do")
}

func TestModelTextureRegistryKeepsDuplicateAndEmptyUnitIdentity(t *testing.T) {
	fs, cat, terrain, _, source := modelTextureBindingFixture(t)
	cat.Units["beta"].CanonicalKey = "alpha"
	cat.Units["beta"].UnitName = "alpha"
	cat.Units["alpha"].MovementClass = "first"
	cat.Units["beta"].MovementClass = "later"
	r, err := NewModelTextureRegistry(fs, cat, terrain, len(terrain.FeatureDefs))
	if err != nil {
		t.Fatal(err)
	}
	first := r.unitModel("alpha", 1, "shared")
	later := r.unitModel("alpha", 2, "shared")
	if first == nil || later == nil || first == later {
		t.Fatal("duplicate name replaced the later record model")
	}
	if r.unitModel("alpha", 0, "shared") != first {
		t.Fatal("name fallback lost first record")
	}
	if got := r.trailInfo("alpha", 2).move; got != "later" {
		t.Fatalf("duplicate trail classification = %q, want later record", got)
	}
	preview := newModelTextureRegistry(fs, true)
	if preview.unitModel("", 0, "objects3d/shared.3do") == nil {
		t.Fatal("standalone preview lost explicit model path")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(source), ".3do"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Directory providers snapshot names at mount time; include the new resource.
	withEmpty := vfs.New()
	if err := withEmpty.MountDirectory(filepath.Dir(filepath.Dir(source)), 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { withEmpty.Close() })
	cat.Units["alpha"].ObjectName = ""
	r, err = NewModelTextureRegistry(withEmpty, cat, terrain, len(terrain.FeatureDefs))
	if err != nil {
		t.Fatal(err)
	}
	cl := &Client{modelTextures: r}
	v := frame.UnitView{DefName: "alpha", DefID: 1}
	if cl.modelForUnit(v) == nil || cl.HullModel("") == nil {
		t.Fatal("empty basename lost model or picking geometry")
	}
}
