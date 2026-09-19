package content

import (
	"fmt"
	"testing"
)

func TestUnitRecordSortEqualNamesAtPartitionBoundary(t *testing.T) {
	// Insertion preserves equal records through size sixteen. At seventeen,
	// the partition swaps every opposite pair, even with equal keys [02 R-CAT-01 §5].
	for _, count := range []int{16, 17} {
		files := make([]fixtureFile, count)
		for i := range files {
			files[i] = fixtureFile{path: fmt.Sprintf("units/file%02d.fbi", i), data: fmt.Sprintf("[UNITINFO]{UnitName=SAME; Name=record%d;}", i)}
		}
		result, err := compileUnitsWithLanguage(newFixtureFS(t, files...), "")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.records) != count {
			t.Fatalf("retained %d of %d", len(result.records), count)
		}
		for i, u := range result.records {
			want := i
			if count == 17 {
				want = count - 1 - i
			}
			if u.Name != fmt.Sprintf("record%d", want) {
				t.Fatalf("size %d index %d = %s, want record%d", count, i, u.Name, want)
			}
		}
		if result.units["same"] != result.records[0] {
			t.Fatal("name index did not select first equal record")
		}
	}
}

func TestUnitDiscoveryCompactionPreservesRetailDuplicateOrder(t *testing.T) {
	for _, abort := range []bool{false, true} {
		// units/a.fbi is dropped by the retail archive gate — the one unit
		// drop gate Nanolathe keeps [02 R-CAT-01 §4] — so its slot is the
		// hole the discovery compaction fills from the end.
		files := []fixtureFile{
			{path: "units/a.fbi", data: "[UNITINFO]{UnitName=SAME;}"},
			{path: "units/b.fbi", data: "[UNITINFO]{UnitName=SAME; Name=middle;}"},
			{path: "units/c.fbi", data: "[UNITINFO]{UnitName=SAME; Name=last;}"},
		}
		if abort {
			files = append(files, fixtureFile{path: "units/d.fbi", data: "[OTHER]{}"})
		}
		result, err := compileUnitsWithLanguage(newLooseUnitFS(newFixtureFS(t, files...), "units/a.fbi"), "")
		if err != nil {
			t.Fatal(err)
		}
		want := "last" // normal discovery swaps the last kept record into the rejected first slot
		if abort {
			want = "middle"
		} // early abort leaves only the compiler's stable compaction
		if len(result.records) != 2 || result.units["same"].Name != want {
			t.Fatalf("abort=%t: records=%v first=%s, want %s", abort, result.records, result.units["same"].Name, want)
		}
	}
}

func TestUnitRecordsLinkCloneHashAndRestriction(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "units/a.fbi", data: "[UNITINFO]{UnitName=DUP; ObjectName=first; Category=FIRST; Weapon1=laser; MovementClass=walk; Builder=1;}"},
		fixtureFile{path: "units/b.fbi", data: "[UNITINFO]{UnitName=dup; ObjectName=second; Category=SECOND; Weapon1=laser; MovementClass=walk; Builder=1;}"},
		fixtureFile{path: "units/c.fbi", data: "[UNITINFO]{ObjectName=empty; Category=EMPTY;}"},
		fixtureFile{path: "units/d.fbi", data: "[UNITINFO]{UnitName=; ObjectName=empty2; Category=EMPTYSECOND;}"},
		fixtureFile{path: "guis/0.gui", data: "present"},
		fixtureFile{path: "scripts/dup.cob", data: string(contentTestCOB([]uint32{0}))},
		fixtureFile{path: "guis/dup1.gui", data: "present"},
		fixtureFile{path: "objects3d/first.3do", data: authoredRequiredModel3DO(t, 1<<16)},
		fixtureFile{path: "objects3d/second.3do", data: authoredRequiredModel3DO(t, 2<<16)},
		fixtureFile{path: "objects3d/empty.3do", data: authoredRequiredModel3DO(t, 3<<16)},
		fixtureFile{path: "objects3d/empty2.3do", data: authoredRequiredModel3DO(t, 4<<16)},
	)
	// Exercise record linking and cloning with authored compiled definitions.
	// The discovery/secondary resource contract is covered separately: a
	// missing name-based FBI does not compile these gameplay fields.
	var records []*UnitDef
	for _, logicalPath := range []string{"units/a.fbi", "units/b.fbi", "units/c.fbi", "units/d.fbi"} {
		u := compileUnitSection(mustParseTDF(t, fs.files[logicalPath]).Root.Section("UNITINFO"), logicalPath, "", Provenance{LogicalPath: logicalPath})
		records = append(records, u)
	}
	sortUnitRecords(records)
	result := unitCompileResult{records: records, units: firstUnitNames(records)}
	var err error
	c := &Catalog{Units: result.units, unitRecords: result.records, Weapons: map[string]*WeaponDef{"laser": {DefinitionHeader: DefinitionHeader{CanonicalKey: "laser"}, ID: 1, Name: "laser"}}}
	c.Categories, err = compileCategoryRecords(result.records, result.units, RetailLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.UnitRecords()) != 4 || len(c.Units) != 2 {
		t.Fatal("duplicate or empty record lost")
	}
	applyMovementFootprintRecords(result.records, map[string]*MovementClass{"walk": {FootprintX: 4, FootprintZ: 5}})
	linkUnitWeaponRecords(result.records, c.Weapons)
	if err := validateRequiredRecordModels(fs, result.records, nil, nil); err != nil {
		t.Fatal(err)
	}
	fillUnitRecordBuildPages(fs, result.records)
	if err := fillUnitRecordScripts(fs, result.records[2:]); err != nil {
		t.Fatal(err)
	}
	for i, u := range c.UnitRecords() {
		id := uint32(i + 1)
		if got, ok := c.UnitDefByIndex(id); !ok || got != u {
			t.Fatalf("index %d lost record", id)
		}
		if got, ok := c.UnitIndexOf(u); !ok || got != id {
			t.Fatalf("record %d lost identity", id)
		}
		if !u.UnitMask.Contains(id) {
			t.Fatalf("record %d lost membership", id)
		}
		if u.ModelTopFixed == 0 {
			t.Fatalf("record %d lost model", id)
		}
		if u.UnitName == "" {
			if !u.HasPageZeroGUI || u.BuildPageCount != 1 {
				t.Fatal("empty name lost numeric GUI probe")
			}
		} else if u.Weapon1Def != c.Weapons["laser"] || u.FootprintX != 4 || u.BuildPageCount != 2 || u.Script == nil {
			t.Fatalf("duplicate lost linked resource: weapon=%p want=%p footprint=%d pages=%d", u.Weapon1Def, c.Weapons["laser"], u.FootprintX, u.BuildPageCount)
		}
	}
	if id, ok := c.UnitDefIndex(""); !ok || id != 1 {
		t.Fatalf("empty name index=%d,%t", id, ok)
	}
	mask, _ := c.Category("SECOND")
	if !mask.Contains(4) || mask.Contains(3) {
		t.Fatal("duplicate category collapsed onto first record")
	}
	c.Hash = catalogHash(c)
	clone := c.Clone()
	if clone.Hash != c.Hash || catalogHash(clone) != c.Hash {
		t.Fatal("clone hash changed")
	}
	view := clone.UnitRecords()
	view[0] = nil
	if clone.UnitRecords()[0] == nil {
		t.Fatal("record view exposed slice storage")
	}
	later, _ := clone.UnitDefByIndex(4)
	if later == c.unitRecords[3] || later.Weapon1Def != clone.Weapons["laser"] {
		t.Fatal("later duplicate clone aliases original")
	}
	later.MaxDamage++
	later.Hash = HashDefinition(writeUnitCanonical(later))
	if catalogHash(clone) == c.Hash {
		t.Fatal("later duplicate omitted from catalog digest")
	}
	if err := clone.RestrictToCreatable([]string{"dup", ""}); err != nil {
		t.Fatal(err)
	}
	if len(clone.UnitRecords()) != 2 || clone.Units["dup"].ObjectName != "first" || clone.Units[""].ObjectName != "empty" {
		t.Fatal("restriction kept later equal-name record")
	}
	if len(c.UnitRecords()) != 4 {
		t.Fatal("restriction mutated source catalog")
	}
}
