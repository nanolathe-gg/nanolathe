package session

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// The authored map makes this a complete detached load without retail assets.
// Its canonical TNT has 32x32 empty cells, one tile and no minimap [fmt tnt].
func restoreRNGFixture(t *testing.T) (*save.Bank, RetailLoadDeps) {
	t.Helper()
	fs := featureLifecycleFS(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "maps"), 0o755); err != nil {
		t.Fatal(err)
	}
	ota := "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials] { [special0] { specialwhat=StartPos1; XPos=160; ZPos=160; } }\n}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "maps", "test.ota"), []byte(ota), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ai"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ai", "default.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	const cells = 32 * 32
	const attrs = 64 + cells/2
	const graphics = attrs + cells*4
	tnt := make([]byte, graphics+1024)
	for i, value := range []uint32{0x2000, 32, 32, 64, attrs, graphics, 1} {
		binary.LittleEndian.PutUint32(tnt[i*4:], value)
	}
	for i := range cells {
		tnt[attrs+i*4] = 10
		binary.LittleEndian.PutUint16(tnt[attrs+i*4+1:], 0xffff)
	}
	if err := os.WriteFile(filepath.Join(root, "maps", "test.tnt"), tnt, 0o644); err != nil {
		t.Fatal(err)
	}
	writeCompositionModel(t, root, "fixture", 0)
	writeCompositionCOBProgram(t, root, "armcom", []uint32{
		0x10021001, 0, 0x10021001, 65535, 0x10041000, 0x10065000,
	}, []string{"Create"}, []uint32{0}, []string{"modelroot", "modelchild"})
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	cat := featureLifecycleCatalog()
	cat.Maps["test"] = &content.MapHeader{Schemas: []content.MapSchema{{Name: "Schema 0"}}}
	def := cat.Units["armcom"]
	def.ObjectName, def.CanHover, def.BuildAngle, def.Limit = "fixture", true, 4096, -1
	def.Script = nil // bind the authored Create program through the production loader
	src := featureLifecycleSession(t, fs, cat)
	for i := range 2 {
		h, err := src.Units.Create(def, 0, numeric.FixedFromInt(int64(160+i*64)), numeric.FixedFromInt(10), numeric.FixedFromInt(160))
		if err != nil {
			t.Fatal(err)
		}
		// A phase deliberately different from either reconstructed value;
		// no save field is allowed to restore this marker [08 R-SAVE-UNIT-01].
		src.Units.Unit(h).BobPhase = -1
		src.Movement.EnsureUnit(src.Units.Unit(h))
		if !src.Movement.HasMover(h) {
			t.Fatal("fixture unit has no mover to save")
		}
	}
	if src.Features.PlaceAt(5, 5, cat.Features["tree1"]) == nil || !src.Features.Ignite(5, 5, 1, 0) {
		t.Fatal("fixture burn was not created")
	}
	src.Features.InstanceAt(5, 5).BurnCountdown = 80
	inputs, err := src.RetailBattleSaveInputs(save.Summary{Gametype: GametypeMultiplayer, MapName: "test", Players: 1, IsBattle: true}, save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := ProjectRetailSession(src, inputs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := projection.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	bank, err := save.OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	return bank, RetailLoadDeps{FS: fs, Catalog: cat, UnitLimit: src.Units.UnitLimit(), SimSeed: 41, CRTSeed: 43}
}

// The full loader must retain each constructor phase through mover, script and
// base-body restoration. A draw-count assertion alone misses permutations:
// burn ignition, heading, phase, Create RNG, heading, phase, Create RNG
// [08 R-SAVE-02 §11][08 R-SAVE-UNIT-01][04 R-MOV-01 §5c].
func TestDetachedLoadIgnitesFeaturesBeforeUnitRandomInitialization(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			bank, deps := restoreRNGFixture(t)
			deps.Gameplay = mode
			want := rng.NewSimulation(deps.SimSeed)
			// Human slots also construct strategic state before the restore
			// dispatcher [08 R-SAVE-02 §11-A][08 R-AI-03 §4].
			landW, landH := want.Uint32n(10)+11, want.Uint32n(3)+11
			want.Uint32n(landW)
			want.Uint32n(landH)
			waterW, waterH := want.Uint32n(20)+14, want.Uint32n(3)+14
			want.Uint32n(waterW)
			want.Uint32n(waterH)
			want.Uint32n(75) // ignition: half the authored SparkTime of 150
			var phases [2]int16
			for i := range phases {
				want.Uint32n(4096)
				phases[i] = int16(uint16(want.Uint32n(65536)))
				want.Uint32n(65536) // authored Create random(0, 65535)
			}
			result, err := LoadRetailSaveWithDeps(bank, deps)
			if err != nil {
				t.Fatal(err)
			}
			s := result.Battle.Session
			for i, rec := range result.Battle.Image.Units.Records {
				u := s.Units.Unit(result.Battle.StableUnit[rec.StableID])
				if u == nil {
					t.Fatalf("restored unit %d missing", rec.StableID)
				}
				if u.BobPhase != phases[i] {
					t.Fatalf("restored unit %d phase = %d, want %d", rec.StableID, u.BobPhase, phases[i])
				}
				if !s.Movement.HasMover(u.Handle) {
					t.Fatalf("restored unit %d lost its mover", rec.StableID)
				}
			}
			if s.SimRNG().State != want.State || s.SimRNG().Draws() != want.Draws() || s.CrtRNG().Draws() != 0 {
				t.Fatalf("restored RNG = %d/%d sim, %d CRT draws; want %d/%d sim and zero CRT", s.SimRNG().State, s.SimRNG().Draws(), s.CrtRNG().Draws(), want.State, want.Draws())
			}
			if burn := s.Features.InstanceAt(5, 5); burn == nil || !burn.IsBurning || burn.BurnCountdown != 80 {
				t.Fatalf("saved burn state was not restored: %+v", burn)
			}
		})
	}
}

// A failed core pass keeps its deferred cues for a retry, without repeating
// ignition or publishing anything from the failed candidate [08 R-SAVE-FEATURE-01].
func TestStagedFeatureIgnitionIsNotRepeatedByCoreRetry(t *testing.T) {
	bank, deps := restoreRNGFixture(t)
	stage, err := StageRetailBattle(bank, deps)
	if err != nil {
		t.Fatal(err)
	}
	s := stage.Session
	before := s.SimRNG().Draws()
	sounds := 0
	s.Features.BurnSound = func([3]numeric.Fixed) { sounds++ }
	good := stage.Image.Units.Scripts[0].Data
	stage.Image.Units.Scripts[0].Data = nil
	if err := RestoreRetailBattleCore(stage); err == nil {
		t.Fatal("invalid script snapshot was accepted")
	}
	if sounds != 0 || s.SimRNG().Draws() != before+3 {
		t.Fatal("failed core pass must construct only its first unit, without publishing ignition")
	}
	stage.Image.Units.Scripts[0].Data = good
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	if sounds != 1 || s.SimRNG().Draws() != before+6 {
		t.Fatalf("retry sounds/draws = %d/%d, want 1/%d", sounds, s.SimRNG().Draws(), before+6)
	}
	if err := RestoreRetailBattleCore(stage); err != nil {
		t.Fatal(err)
	}
	if sounds != 1 || s.SimRNG().Draws() != before+6 {
		t.Fatal("completed core pass repeated ignition or its deferred sound")
	}
}
