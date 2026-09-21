package content

import "testing"

// TestUnitAdmissionIgnoresCopyrightAndVersion locks the Nanolathe content
// admission policy of DESIGN_CONTENT_VFS §5 "Unit admission": a definition is
// admitted whatever its `Copyright` string and `Version` number say, and no
// incompatibility diagnostic is produced. Retail drops both of these records
// and reports the version case [02 R-MALF-01 §5]; dropping that gate is a
// deliberate, user-authorized departure, not a parity defect.
func TestUnitAdmissionIgnoresCopyrightAndVersion(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "units/modern.fbi", data: "[UNITINFO]{ unitname=modern; Version=9.9; Copyright=Copyright 2026 Somebody Else. All rights reserved.; }"},
		fixtureFile{path: "units/nometa.fbi", data: "[UNITINFO]{ unitname=nometa; }"},
		fixtureFile{path: "units/retail.fbi", data: "[UNITINFO]{ unitname=retail; Version=3.1; Copyright=Copyright 1997 Humongous Entertainment. All rights reserved.; }"},
	)
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		t.Fatalf("compile units: %v", err)
	}
	if len(result.units) != 3 {
		t.Fatalf("admitted units = %d (%v), want all three definitions", len(result.units), result.units)
	}
	for _, name := range []string{"modern", "nometa", "retail"} {
		if result.units[name] == nil {
			t.Fatalf("definition %q was dropped; admission must ignore Copyright and Version", name)
		}
	}
	for _, w := range result.warnings {
		t.Fatalf("admission produced the diagnostic %q; the incompatibility report is retired with the gate", w)
	}
}

// TestUnitAdmissionKeepsLooseFileDrop locks the boundary of the policy above:
// the loose-file drop is a separate retail rule [02 R-CAT-01 §4] and is
// unchanged. A loose FBI winner is parsed and then dropped, so a unit whose
// only provider is a directory does not reach the catalog even though its
// authored compatibility metadata is now irrelevant.
func TestUnitAdmissionKeepsLooseFileDrop(t *testing.T) {
	loose := newLooseUnitFS(newFixtureFS(t,
		fixtureFile{path: "units/loose.fbi", data: "[UNITINFO]{ unitname=loose; Version=3.1; Copyright=Copyright 1997 Humongous Entertainment. All rights reserved.; }"},
	), "units/loose.fbi")
	result, err := compileUnitsWithLanguage(loose, "")
	if err != nil {
		t.Fatalf("compile units: %v", err)
	}
	if len(result.units) != 0 {
		t.Fatalf("loose definition admitted = %v, want the retail archive gate to drop it", result.units)
	}
}

// TestUnitAdmissionRetailFixtureOrderUnchanged locks record ordering and
// definition-ID assignment across the admission change: a fixture set that
// every retail gate admitted before — archive-provided, Version 3.1, the
// retail copyright line — still compacts and sorts to the same order, with
// IDs assigned from 1 in sorted canonical-key order. Enumeration order is
// deliberately the reverse of the final order, so a perturbed compaction or
// sort would show here [02 R-CAT-01 §5].
func TestUnitAdmissionRetailFixtureOrderUnchanged(t *testing.T) {
	const retail = " Version=3.1;\nCopyright=Copyright 1997 Humongous Entertainment. All rights reserved.;\n"
	fs := newFixtureFS(t,
		fixtureFile{path: "units/a.fbi", data: "[UNITINFO]{ unitname=zulu;" + retail + "}"},
		fixtureFile{path: "units/b.fbi", data: "[UNITINFO]{ unitname=mike;" + retail + "}"},
		fixtureFile{path: "units/c.fbi", data: "[UNITINFO]{ unitname=alpha;" + retail + "}"},
	)
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		t.Fatalf("compile units: %v", err)
	}
	want := []string{"alpha", "mike", "zulu"}
	if len(result.records) != len(want) {
		t.Fatalf("records = %d, want %d", len(result.records), len(want))
	}
	for i, name := range want {
		got := result.records[i]
		if got.CanonicalKey != name || got.UnitDefID != uint32(i+1) {
			t.Fatalf("record %d = %q ID %d, want %q ID %d", i, got.CanonicalKey, got.UnitDefID, name, i+1)
		}
	}
}

func TestDownloadableEnforcementIgnoresBuildMenuButtons(t *testing.T) {
	// [02 §5] "downloadable enforcement" / [02 R-CAT-01 §8] step 3: ONLY the
	// first item's product name of each download record participates. A
	// CANBUILD button name never does, so a menu-only unit that authors
	// downloadable=0 must stay clear and warn nothing.
	menuOnly := &UnitDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "menuonly"}, UnitName: "MENUONLY"}
	downloaded := &UnitDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "downloaded"}, UnitName: "DOWNLOADED"}
	builder := &UnitDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "builder"}, UnitName: "BUILDER", Builder: true}
	records := []*UnitDef{builder, downloaded, menuOnly}
	units := map[string]*UnitDef{"builder": builder, "downloaded": downloaded, "menuonly": menuOnly}
	menus := map[string]*BuildMenuPage{
		"builder": {DefinitionHeader: DefinitionHeader{CanonicalKey: "builder"}, Builder: "BUILDER", Buttons: []string{"MENUONLY"}, BaseButtonCount: 1},
	}
	placements := []DownloadMenuPlacement{
		{Builder: "BUILDER", Menu: 1, Button: 1, Product: "DOWNLOADED", BuilderResolved: true, ProductResolved: true},
	}

	warnings := applyDownloadRecordMenus(records, units, menus, placements)

	want := []string{"Hey!  Somebody forgot to set downloadable=1 for DOWNLOADED"}
	if len(warnings) != len(want) || warnings[0] != want[0] {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}
	if !downloaded.Downloadable {
		t.Fatal("download record first product did not have downloadable forced on")
	}
	if menuOnly.Downloadable {
		t.Fatal("a CANBUILD button name forced downloadable; only download-record first products may [02 R-CAT-01 §8]")
	}
}
