package ai

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestCompiledMenuSkipsUnresolvedCandidates(t *testing.T) {
	// Base names survive compilation; unresolved download products do not
	// extend the list [02 R-CAT-01 §5,§8]. Selection must reject an invalid
	// type without suppressing later options or consuming a reservoir draw
	// [08 R-AI-02 §2][08 R-AI-01 §8].
	dir := t.TempDir()
	for _, name := range []string{"gamedata", "download"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	sideData := `[CANBUILD] { [builder] {
		canbuild1=missing_before; canbuild2=Second;
		canbuild3=missing_between; canbuild4=Limited;
		canbuild5=First; canbuild6=missing_after;
	} }`
	if err := os.WriteFile(filepath.Join(dir, "gamedata", "sidedata.tdf"), []byte(sideData), 0644); err != nil {
		t.Fatal(err)
	}
	download := `[item] { UNITMENU=builder; MENU=1; BUTTON=0; UNITNAME=missing_download; }`
	if err := os.WriteFile(filepath.Join(dir, "download", "menu.tdf"), []byte(download), 0644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	menus, err := content.CompileBuildMenus(fs)
	if err != nil {
		t.Fatal(err)
	}
	builder := testBuilder("builder")
	builder.Def.Builder = true
	builder.Def.Side = "ARM"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": builder.Def}, BuildMenus: menus}
	for _, name := range []string{"first", "second", "limited"} {
		cat.Units[name] = &content.UnitDef{
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: name},
			UnitName:         name, Side: "ARM",
		}
	}
	placements, err := content.CompileDownloadMenus(fs, cat.Units)
	if err != nil {
		t.Fatal(err)
	}
	content.ApplyDownloadMenus(cat.Units, menus, placements)
	wantButtons := []string{"missing_before", "Second", "missing_between", "Limited", "First", "missing_after"}
	if got := menus["builder"].Buttons; !reflect.DeepEqual(got, wantButtons) {
		t.Fatalf("compiled menu=%v, want authored base order without unresolved download %v", got, wantButtons)
	}
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)
	for _, mode := range []int32{0, 1} {
		for _, staleVector := range []bool{false, true} {
			strat := &Strategic{
				Catalog: cat,
				Counts:  map[string]int32{"limited": 2},
				ClassVectors: map[string]ClassVector{
					"first": {C0: 100}, "second": {C0: 100}, "limited": {C0: 100},
				},
			}
			if staleVector {
				// Catalog membership remains authoritative even if a stale
				// strategic entry would give an unresolved name a positive score.
				strat.ClassVectors["missing_before"] = ClassVector{C0: 100}
			}
			profile := &Profile{
				Weight: map[string]int32{"first": 40, "second": 100},
				Limit:  map[string]int32{"limited": 2},
			}
			const seed = 37
			probe := rng.NewSimulation(seed)
			_ = probe.Uint32n(100)
			want := Candidate{DefKey: "Second", Score: 100}
			if probe.Uint32n(140) < 40 {
				want = Candidate{DefKey: "First", Score: 40}
			}
			sim := rng.NewSimulation(seed)
			sel := &testSelector{player: 1, catalog: cat, strategic: strat, profile: profile, rng: &sim, missionMode: mode}
			got, ok := Select(sel, builder, econ)
			if !ok || got != want || sim.State != probe.State || sim.Draws() != probe.Draws() {
				t.Fatalf("mode=%d stale=%v: selection=%+v/%v RNG=%d/%d, want %+v/true RNG=%d/%d", mode, staleVector, got, ok, sim.State, sim.Draws(), want, probe.State, probe.Draws())
			}
		}
	}
}

func TestResolvedCandidateWithoutClassVectorRefusesSelection(t *testing.T) {
	// Invalid menu names are skippable; a resolved definition without its
	// initialized vector is still missing strategic state [PLAN 11 C2].
	builder := testBuilder("builder")
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		"ready": testBuilder("ready").Def, "unready": testBuilder("unready").Def,
	}}
	strat := &Strategic{Catalog: cat, ClassVectors: map[string]ClassVector{"ready": {C0: 100}}}
	sim := rng.NewSimulation(37)
	sel := &testSelector{player: 1, catalog: cat, strategic: strat, profile: &Profile{}, rng: &sim}
	econ := testEcon(1, 1000, 1000, 500, 500, 300, 10, 0, 0)
	if got, ok := SelectWithCandidates(sel, builder, econ, []string{"ready", "unready"}); ok || got != (Candidate{}) {
		t.Fatalf("missing vector retained an earlier candidate: %+v/%v", got, ok)
	}
	if sim.Draws() != 1 {
		t.Fatalf("missing vector draw count=%d, want only the preceding positive candidate's draw", sim.Draws())
	}
}
