package content

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

type retailOrderFixture struct {
	*fixtureFS
	entries []vfs.EntryInfo
}

func (f *retailOrderFixture) RetailReadDir(string) ([]vfs.EntryInfo, error) {
	return append([]vfs.EntryInfo(nil), f.entries...), nil
}

type suppliedOrderFixture struct {
	*fixtureFS
	entries []vfs.EntryInfo
}

func (f *suppliedOrderFixture) ReadDir(string) ([]vfs.EntryInfo, error) {
	return append([]vfs.EntryInfo(nil), f.entries...), nil
}

func downloadEntry(path string, mountOrder int) vfs.EntryInfo {
	return vfs.EntryInfo{
		Path: path,
		Name: path,
		Source: vfs.Provenance{
			LogicalPath:  path,
			ProviderType: "hpi",
			SourcePath:   "fixture.hpi",
			MountOrder:   mountOrder,
		},
	}
}

func testUnit(name string, builder bool, pageCount int32) *UnitDef {
	return &UnitDef{
		DefinitionHeader: DefinitionHeader{CanonicalKey: CanonicalKey(name)},
		UnitName:         name,
		Builder:          builder,
		BuildPageCount:   pageCount,
	}
}

func TestCompileDownloadMenusPreservesSparseSlotsAndExtendsBuilders(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "download/a.tdf", data: `
[FIRST] { UNITMENU=ARMLAB; MENU=3; BUTTON=5; UNITNAME=ARMWAR; }
[BADBUILDER] { UNITMENU=MISSING; MENU=7; BUTTON=4; UNITNAME=ARMWAR; }
[BADPRODUCT] { UNITMENU=ARMLAB; MENU=6; BUTTON=3; UNITNAME=MISSING; }
`},
		fixtureFile{path: "download/b.tdf", data: `
[SECOND] { UNITMENU=ARMLAB; MENU=3; BUTTON=1; UNITNAME=ARMFLEA; }
[EMPTYBASE] { UNITMENU=ARMNANO; MENU=4; BUTTON=2; UNITNAME=ARMFLEA; }
`},
		fixtureFile{path: "download/c.tdf", data: `
[FIRSTUNRESOLVEDBUILDER] { UNITMENU=MISSING; MENU=7; BUTTON=4; UNITNAME=ARMFLASH; }
`},
	)
	units := map[string]*UnitDef{
		"armlab":   testUnit("ARMLAB", true, 2),
		"armnano":  testUnit("ARMNANO", true, 0),
		"armwar":   testUnit("ARMWAR", false, 0),
		"armflea":  testUnit("ARMFLEA", false, 0),
		"armflash": testUnit("ARMFLASH", false, 0),
	}
	menus := map[string]*BuildMenuPage{
		"armlab": {DefinitionHeader: DefinitionHeader{CanonicalKey: "armlab"}, Builder: "ARMLAB", Buttons: []string{"ARMCK"}, BaseButtonCount: 1},
	}

	placements, err := CompileDownloadMenus(fs, units)
	if err != nil {
		t.Fatalf("CompileDownloadMenus: %v", err)
	}
	if len(placements) != 6 {
		t.Fatalf("placements = %d, want all 6 safely represented records: %#v", len(placements), placements)
	}
	resolvedPage := (&Catalog{DownloadPlacements: placements}).DownloadPlacementsForPage("ARMLAB", 2)
	if len(resolvedPage) != 2 || resolvedPage[0].Button != 5 || resolvedPage[1].Button != 1 {
		t.Fatalf("ARMLAB authored sparse slots = %#v, want buttons [5 1] in file order", resolvedPage)
	}
	if placements[0].FileOrder != 0 || placements[0].ItemOrder != 0 || placements[3].FileOrder != 1 || placements[3].ItemOrder != 0 {
		t.Fatalf("placement order not retained: %#v", placements)
	}
	if placements[0].Provenance.LogicalPath != "download/a.tdf" {
		t.Fatalf("provenance = %#v, want download/a.tdf", placements[0].Provenance)
	}

	warnings := ApplyDownloadMenus(units, menus, placements)
	if units["armlab"].BuildPageCount != 6 {
		t.Fatalf("ARMLAB BuildPageCount = %d, want 6 from resolved builder even with unresolved product", units["armlab"].BuildPageCount)
	}
	if got := menus["armlab"].Buttons; len(got) != 3 || got[0] != "ARMCK" || got[1] != "ARMWAR" || got[2] != "ARMFLEA" {
		t.Fatalf("ARMLAB authoritative products = %v", got)
	}
	if units["armnano"].BuildPageCount != 4 {
		t.Fatalf("ARMNANO BuildPageCount = %d, want raised to 4", units["armnano"].BuildPageCount)
	}
	if got := menus["armnano"]; got == nil || len(got.Buttons) != 1 || got.Buttons[0] != "ARMFLEA" {
		t.Fatalf("builder without CANBUILD child did not receive its retail list: %#v", got)
	}
	if _, exists := menus["missing"]; exists {
		t.Fatal("unresolved UNITMENU created a builder list")
	}
	if !units["armflash"].Downloadable || len(warnings) == 0 {
		t.Fatalf("first item with unresolved builder did not enforce its resolved product: downloadable=%v warnings=%v", units["armflash"].Downloadable, warnings)
	}
	if got := menus["armlab"].BaseButtons(); len(got) != 1 || got[0] != "ARMCK" {
		t.Fatalf("base CANBUILD boundary lost after downloads: %v", got)
	}
}

func TestDownloadPlacementsPageLookupCloneAndHash(t *testing.T) {
	placement := DownloadMenuPlacement{
		Builder: "ARMLAB", Menu: 3, Button: 5, Product: "ARMWAR", BuilderResolved: true, ProductResolved: true,
		Provenance: Provenance{LogicalPath: "download/armwar.tdf", ProviderID: "addon.ufo", MountOrder: 4},
	}
	cat := &Catalog{
		DownloadPlacements: []DownloadMenuPlacement{placement},
		BuildMenus: map[string]*BuildMenuPage{
			"armlab": {Builder: "ARMLAB", Buttons: []string{"ARMCK", "ARMWAR"}, BaseButtonCount: 1},
		},
	}
	hashBuildMenu(cat.BuildMenus["armlab"])
	got := cat.DownloadPlacementsForPage("arMLab", 2)
	if len(got) != 1 || got[0].VisiblePage() != 2 || got[0].Button != 5 {
		t.Fatalf("page lookup = %#v, want ARMWAR at visible page 2 button 5", got)
	}
	if wrong := cat.DownloadPlacementsForPage("ARMLAB", 3); len(wrong) != 0 {
		t.Fatalf("wrong visible page returned %#v", wrong)
	}

	cat.Hash = catalogHash(cat)
	clone := cat.Clone()
	if clone.Hash != cat.Hash || catalogHash(clone) != cat.Hash {
		t.Fatal("download placement did not survive clone/hash")
	}
	if got := clone.BuildMenus["armlab"].BaseButtons(); len(got) != 1 || got[0] != "ARMCK" {
		t.Fatalf("base-button boundary did not survive clone: %v", got)
	}
	clone.DownloadPlacements[0].Product = "ARMFLEA"
	clone.BuildMenus["armlab"].Buttons[0] = "MUTATED"
	if cat.DownloadPlacements[0].Product != "ARMWAR" {
		t.Fatal("clone shares download placement storage")
	}
	if cat.BuildMenus["armlab"].Buttons[0] != "ARMCK" {
		t.Fatal("clone shares final build-menu storage")
	}
	if catalogHash(clone) == cat.Hash {
		t.Fatal("download placement mutation did not affect canonical catalog hash")
	}
}

func TestDownloadMenusPreserveRetailUnionOrder(t *testing.T) {
	base := newFixtureFS(t,
		fixtureFile{path: "download/z-first.tdf", data: `[ENTRY] { UNITMENU=ARMLAB; MENU=3; BUTTON=0; UNITNAME=ARMWAR; }`},
		fixtureFile{path: "download/a-second.tdf", data: `[ENTRY] { UNITMENU=ARMLAB; MENU=3; BUTTON=1; UNITNAME=ARMFLEA; }`},
	)
	entries := []vfs.EntryInfo{
		downloadEntry("download/z-first.tdf", 2),
		downloadEntry("download/a-second.tdf", 7),
	}
	units := map[string]*UnitDef{
		"armlab":  testUnit("ARMLAB", true, 2),
		"armwar":  testUnit("ARMWAR", false, 0),
		"armflea": testUnit("ARMFLEA", false, 0),
	}
	menus := map[string]*BuildMenuPage{
		"armlab": {Builder: "ARMLAB", Buttons: []string{"ARMCK"}, BaseButtonCount: 1},
	}

	placements, err := CompileDownloadMenus(&retailOrderFixture{fixtureFS: base, entries: entries}, units)
	if err != nil {
		t.Fatalf("CompileDownloadMenus retail order: %v", err)
	}
	if len(placements) != 2 || placements[0].Product != "ARMWAR" || placements[1].Product != "ARMFLEA" {
		t.Fatalf("retail union order was alphabetized: %#v", placements)
	}
	ApplyDownloadMenus(units, menus, placements)
	if got := menus["armlab"].Buttons; len(got) != 3 || got[1] != "ARMWAR" || got[2] != "ARMFLEA" {
		t.Fatalf("append order = %v, want base then retail-order Warrior, Flea", got)
	}
}

func TestDownloadMenusPreserveFallbackReadDirOrder(t *testing.T) {
	base := newFixtureFS(t,
		fixtureFile{path: "download/z-first.tdf", data: `[ENTRY] { UNITMENU=ARMLAB; MENU=3; BUTTON=0; UNITNAME=ARMWAR; }`},
		fixtureFile{path: "download/a-second.tdf", data: `[ENTRY] { UNITMENU=ARMLAB; MENU=3; BUTTON=1; UNITNAME=ARMFLEA; }`},
	)
	units := map[string]*UnitDef{
		"armlab":  testUnit("ARMLAB", true, 2),
		"armwar":  testUnit("ARMWAR", false, 0),
		"armflea": testUnit("ARMFLEA", false, 0),
	}
	fs := &suppliedOrderFixture{
		fixtureFS: base,
		entries: []vfs.EntryInfo{
			downloadEntry("download/z-first.tdf", 2),
			downloadEntry("download/a-second.tdf", 7),
		},
	}
	placements, err := CompileDownloadMenus(fs, units)
	if err != nil {
		t.Fatalf("CompileDownloadMenus fallback order: %v", err)
	}
	if len(placements) != 2 || placements[0].Product != "ARMWAR" || placements[1].Product != "ARMFLEA" {
		t.Fatalf("fallback ReadDir order was changed: %#v", placements)
	}
}
