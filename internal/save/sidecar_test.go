package save

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
)

// A sidecar round-trips every field, including the three spellings of `mod`
// (null, an object, "custom"), and a save without one reads as absent rather
// than as an error (docs/DESIGN_MODS_MUTATORS.md §7.2, §7.3 step 1).
func TestSidecarRoundTripsEveryModSpelling(t *testing.T) {
	dir := t.TempDir()
	enabled := true
	full := Sidecar{
		Profile:         "nanolathe-1.0",
		Mod:             SidecarModRef{Mod: &SidecarMod{ID: "prota", Version: "4.8", SHA256: strings.Repeat("ab", 32)}},
		ContentProfile:  "prota",
		ContentManifest: "manifest",
		Catalog:         "catalog",
		Rules:           "community-3.9",
		Gameplay:        "community-3.9",
		Community: SidecarCommunity{
			Sources: SidecarCommunitySources{Player: community.Overrides{Table: "community-3.9", Veterancy: &enabled}},
			Entry:   community.Features{UnitLimit: 1500, ConstructionKickout: true},
		},
		UnitLimit: 1500,
		Mutators:  map[string]string{"buildSpeed": "2", "health": "1.5"},
	}
	for name, mod := range map[string]SidecarModRef{
		"object": full.Mod,
		"none":   {},
		"custom": {Custom: true},
	} {
		bank := filepath.Join(dir, name+".SAV")
		want := full
		want.Mod = mod
		if err := WriteSidecar(bank, want); err != nil {
			t.Fatal(err)
		}
		got, ok, err := ReadSidecar(bank)
		if err != nil || !ok {
			t.Fatalf("%s: ReadSidecar = (%v, %v)", name, ok, err)
		}
		want.Schema = SidecarSchema
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: read back %+v, want %+v", name, got, want)
		}
	}
	raw, err := os.ReadFile(SidecarPath(filepath.Join(dir, "none.SAV")))
	if err != nil || !strings.Contains(string(raw), `"mod": null`) {
		t.Fatalf("no-mod sidecar = %s, %v; want mod null", raw, err)
	}
	raw, _ = os.ReadFile(SidecarPath(filepath.Join(dir, "custom.SAV")))
	if !strings.Contains(string(raw), `"mod": "custom"`) {
		t.Fatalf("custom sidecar = %s; want mod \"custom\"", raw)
	}
	// Only the sidecars remain: no temporary file outlives a write.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".SAV"+SidecarSuffix) {
			t.Fatalf("stray file %s after writing", e.Name())
		}
	}

	if _, ok, err := ReadSidecar(filepath.Join(dir, "absent.SAV")); ok || err != nil {
		t.Fatalf("absent sidecar = (%v, %v), want (false, nil)", ok, err)
	}
	if err := RemoveSidecar(filepath.Join(dir, "none.SAV")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSidecar(filepath.Join(dir, "none.SAV")); err != nil {
		t.Fatalf("removing an absent sidecar = %v, want nil", err)
	}
}

// A sidecar that exists but cannot be honoured is an error, never read as
// absent: that would load the game with none of the selection it records.
func TestSidecarRefusesAnotherSchemaAndMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"schema":  `{"schema": 2, "rules": "modern", "mod": null}`,
		"json":    `{"schema": 1,`,
		"mod":     `{"schema": 1, "mod": "prota"}`,
		"modless": `{"schema": 1, "mod": {"id": "prota"}}`,
	} {
		bank := filepath.Join(dir, name+".SAV")
		if err := os.WriteFile(SidecarPath(bank), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := ReadSidecar(bank); err == nil || ok {
			t.Fatalf("%s: ReadSidecar = (%v, %v), want an error", name, ok, err)
		}
	}
}
