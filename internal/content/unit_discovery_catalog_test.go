package content

import (
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func TestUnitCompatibilityGatesAndCollectedWarning(t *testing.T) {
	if !compatibleUnitVersion(3.1) || compatibleUnitVersion(3.2) {
		t.Fatal("version boundary did not preserve 3.1 acceptance and 3.2 rejection")
	}
	// The catalog floors each binary64 operand before the signed-64 low-word
	// conversion. These exact binary fractions make the negative floor and
	// low-word wrap observable in the final acceptance result [02 R-MALF-01 §5].
	const wrapped = -4294967296.0
	if !compatibleUnitVersion(wrapped + 3.125) {
		t.Fatal("floored wrapped Version 3.125 fraction was rejected")
	}
	if compatibleUnitVersion(wrapped + 3.25) {
		t.Fatal("floored wrapped Version 3.25 fraction was accepted")
	}
	if !compatibleUnitCopyright("Copyright 1997 Humongous Entertainment. All rights reserved.") {
		t.Fatal("copyright year normalization rejected an accepted template")
	}
	if compatibleUnitCopyright("Copyright 1997 Other. All rights reserved.") {
		t.Fatal("nonmatching copyright was accepted")
	}
	fs := newFixtureFS(t, fixtureFile{path: "units/new.fbi", data: "[UNITINFO]{ unitname=new; Version=3.2; Copyright=Copyright 1997 Humongous Entertainment. All rights reserved.; }"})
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil || len(result.units) != 0 || !result.incompatibilityWarning {
		t.Fatalf("version gate = (%#v, %v), want one collected warning", result, err)
	}
	loose := &topologyFixtureFS{fixtureFS: fs, providers: map[string]vfs.Provenance{"units/new.fbi": {ProviderType: "directory"}}}
	result, err = compileUnitsWithLanguage(loose, "")
	if err != nil || result.incompatibilityWarning {
		t.Fatalf("loose incompatible unit warning suppression = (%#v, %v)", result, err)
	}
}

func TestCatalogUnitWarningsRetainCompatibilityAndBuildMenuDiagnostics(t *testing.T) {
	units := map[string]*UnitDef{
		"new": {UnitName: "NEW"},
	}
	menus := map[string]*BuildMenuPage{
		"builder": {Builder: "BUILDER", Buttons: []string{"new"}, BaseButtonCount: 1},
	}
	warnings := catalogUnitWarnings(true, units, menus)
	want := []string{
		incompatibleUnitsWarning,
		"Hey! Somebody forgot to set downloadable=1 for NEW",
	}
	if len(warnings) != len(want) {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}
	for i := range want {
		if warnings[i] != want[i] {
			t.Fatalf("warning %d = %q, want %q", i, warnings[i], want[i])
		}
	}
	if !units["new"].Downloadable {
		t.Fatal("build-menu warning did not enforce downloadable on its unit")
	}
}
