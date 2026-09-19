package content

import (
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestUnitSecondaryDiscoveryAndOverwriteFields(t *testing.T) {
	// The secondary file fails discovery admission — it is a loose-directory
	// winner, the one retail drop gate Nanolathe keeps [02 R-CAT-01 §4] — but
	// there is no such gate when another admitted definition reopens it by
	// stored name [02 R-CAT-01 §5].
	fs := newLooseUnitFS(newFixtureFS(t,
		fixtureFile{path: "units/a.fbi", data: `[UNITINFO]{UnitName=target; Name=first; Side=ARM; AI_Weight=weight 25; AI_Limit=limit 2; Wacky=1; NoRestrict=1; ObjectName=old; BuildCostEnergy=11; BuildCostMetal=12; MaxDamage=999; BankScale=9; TEDClass=old;}`},
		fixtureFile{path: "units/target.fbi", data: `[UNITINFO]{UnitName=renamed; Name=second; Side=CORE; AI_Weight=weight 90; AI_Limit=limit 9; Wacky=0; BuildCostEnergy=0; MaxDamage=42; MaxVelocity=1.5; TEDClass=new;}`},
	), "units/target.fbi")
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.records) != 1 {
		t.Fatalf("records = %d", len(result.records))
	}
	u := result.records[0]
	if u.UnitName != "renamed" || u.Name != "second" || u.ObjectName != "renamed" || u.CanonicalKey != "renamed" || result.units["renamed"] != u || u.UnitDefID != 1 {
		t.Fatalf("secondary identity = %s / %s / %s, ID %d", u.UnitName, u.Name, u.ObjectName, u.UnitDefID)
	}
	if u.Side != "ARM" || u.AIWeight != "weight 25" || !u.Wacky || u.Unknown["AI_Limit"] != "limit 2" || u.Provenance.LogicalPath != "units/target.fbi" || u.DiscoveryProvenance.LogicalPath != "units/a.fbi" || u.Unknown["TEDClass"] != "new" {
		t.Fatalf("discovery-only fields changed: side=%s weight=%s wacky=%t unknown=%v provenance=%+v", u.Side, u.AIWeight, u.Wacky, u.Unknown, u.Provenance)
	}
	if u.NoRestrict || u.BuildCostEnergy != 0 || u.BuildCostMetal != 0 || u.MaxDamage != 42 || u.BankScale != 65536 || u.MoveRate1 != 3*65536 || u.MoveRate2 != 3*65536 {
		t.Fatal("second authored zero, absent default, or chained default retained discovery value")
	}
}

// secondaryReadFS changes only the post-discovery read result so failure
// policies can be exercised without aborting first-stage discovery.
type secondaryReadFS struct {
	*fixtureFS
	mode  string
	reads int
}

func (f *secondaryReadFS) Stat(name string) (vfs.EntryInfo, error) {
	if f.mode == "missing" {
		return vfs.EntryInfo{}, errors.New("authored missing resource")
	}
	if f.mode == "zero" {
		return vfs.EntryInfo{Path: name}, nil
	}
	return f.fixtureFS.Stat(name)
}

func (f *secondaryReadFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	f.reads++
	if f.reads > 1 {
		switch f.mode {
		case "rename":
			if name == "units/a.fbi" {
				return []byte(`[UNITINFO]{UnitName=z;}`), nil
			}
		case "read failure":
			return nil, errors.New("authored read failure")
		case "no section":
			return []byte("[OTHER]{}"), nil
		case "syntax":
			return []byte("[UNITINFO]{Name=unterminated}"), nil
		}
	}
	return f.fixtureFS.ReadFileLimit(name, max)
}

func TestUnitSecondaryFailurePreservesDiscoveryOnly(t *testing.T) {
	for _, mode := range []string{"missing", "zero", "read failure", "no section", "syntax"} {
		t.Run(mode, func(t *testing.T) {
			fs := &secondaryReadFS{fixtureFS: newFixtureFS(t, fixtureFile{path: "units/a.fbi", data: `[UNITINFO]{UnitName=a; Name=first; ObjectName=model; Side=ARM; BuildCostMetal=7; NoRestrict=1; MaxDamage=99;}`}), mode: mode}
			result, err := compileUnitsWithLanguage(fs, "")
			if mode == "syntax" {
				if err == nil || !strings.Contains(err.Error(), "Parse error in .TDF File!") {
					t.Fatalf("syntax failure = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.warnings) != 1 || !strings.Contains(result.warnings[0], "logical path units/a.fbi") || !strings.Contains(result.warnings[0], "discovered at units/a.fbi from provider fixture.hpi") {
				t.Fatalf("secondary failure lost its source diagnostic: %v", result.warnings)
			}
			if mode == "read failure" && !strings.Contains(result.warnings[0], "authored read failure") {
				t.Fatalf("secondary read cause was discarded: %v", result.warnings)
			}
			u := result.units["a"]
			if u == nil || u.Name != "first" || u.ObjectName != "model" || u.BuildCostMetal != 7 || !u.NoRestrict {
				t.Fatal("secondary failure changed discovery fields")
			}
			if u.MaxDamage != 0 || u.BankScale != 0 || u.DamageModifier != 0 || u.StandingFireOrder != 0 || u.FootprintX != 0 || u.MaxSlope != 0 {
				t.Fatal("unparsed gameplay fields received discovery values or parser defaults")
			}
		})
	}
}

func TestUnitSecondaryEmptyNameAndAbsentIdentity(t *testing.T) {
	for _, body := range []string{`[UNITINFO]{}`, `[UNITINFO]{UnitName=; Name=; ObjectName=;}`} {
		fs := newFixtureFS(t, fixtureFile{path: "units/a.fbi", data: `[UNITINFO]{UnitName=a; Name=old; ObjectName=old;}`})
		u := compileUnitDiscovery(mustParseTDF(t, fs.files["units/a.fbi"]).Root.Section("UNITINFO"), "", Provenance{})
		fs.files["units/a.fbi"] = body
		if _, err := compileUnitSecondary(fs, u, ""); err != nil {
			t.Fatal(err)
		}
		if u.UnitName != "" || u.Name != "" || u.ObjectName != "" {
			t.Fatal("absent or empty secondary identity retained discovery text")
		}
	}
	fs := newFixtureFS(t, fixtureFile{path: "units/discovered.fbi", data: `[UNITINFO]{ObjectName=old; MaxDamage=999;}`})
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.units[""].MaxDamage != 0 {
		t.Fatal("empty name fell back to discovery filename")
	}
	// Test the otherwise ordinary named-resource parser directly: discovery
	// may already have finished before a secondary resource becomes available.
	fs.files["units/.fbi"] = `[UNITINFO]{UnitName=; ObjectName=new; MaxDamage=12;}`
	u := result.units[""]
	if _, err := compileUnitSecondary(fs, u, ""); err != nil {
		t.Fatal(err)
	}
	if u.UnitName != "" || u.ObjectName != "new" || u.MaxDamage != 12 {
		t.Fatal("empty name did not read units/.fbi")
	}
}

func TestUnitSecondaryRenameDoesNotResortLookup(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "units/a.fbi", data: `[UNITINFO]{UnitName=a;}`},
		fixtureFile{path: "units/b.fbi", data: `[UNITINFO]{UnitName=b;}`},
		fixtureFile{path: "units/c.fbi", data: `[UNITINFO]{UnitName=c;}`},
	)
	result, err := compileUnitsWithLanguage(&secondaryReadFS{fixtureFS: fs, mode: "rename"}, "")
	if err != nil {
		t.Fatal(err)
	}
	records, units := result.records, result.units
	if records[0].UnitName != "z" || records[0].UnitDefID != 1 || records[1].UnitName != "b" || records[2].UnitName != "c" {
		t.Fatal("secondary rename reordered records")
	}
	// In final order z,b,c, lower-bound reaches z for b and the end for z.
	if units["b"] != nil || units["z"] != nil || units["c"] != records[2] {
		t.Fatal("lookup repaired the unsorted final names")
	}
}

func TestUnitSecondaryMissingPreservesUninitializedLinks(t *testing.T) {
	result, err := compileUnitsWithLanguage(newFixtureFS(t,
		fixtureFile{path: "units/a.fbi", data: `[UNITINFO]{UnitName=missing; Category=ARM; Weapon1=laser;}`},
		fixtureFile{path: "units/b.fbi", data: `[UNITINFO]{UnitName=b;}`},
	), "")
	if err != nil {
		t.Fatal(err)
	}
	categories, err := compileCategoryRecords(result.records, result.units, RetailLimits())
	if err != nil {
		t.Fatal(err)
	}
	sentinel := &WeaponDef{ID: 0}
	linkUnitWeaponRecords(result.records, map[string]*WeaponDef{"noweapon": sentinel})
	unparsed, parsed := result.units["missing"], result.units["b"]
	if !unparsed.DiscoveryOnly || parsed.DiscoveryOnly || unparsed.UnitDefID != 2 || parsed.UnitDefID != 1 {
		t.Fatal("discovery state or stable index lost")
	}
	all, _ := categories.Lookup("ALL")
	if !unparsed.UnitMask.IsZero() || all.Contains(2) || !all.Contains(1) {
		t.Fatal("unparsed record gained category membership")
	}
	if unparsed.Weapon1Def != nil || unparsed.Weapon2Def != nil || unparsed.ExplodeAsDef != nil || parsed.Weapon1Def != sentinel {
		t.Fatal("unparsed record gained weapon links")
	}
}

func TestUnitSecondaryResourceExtensionReplacesLastPeriod(t *testing.T) {
	for _, name := range []string{"target.old", "folder.old/unit"} {
		resource := "units/target.fbi"
		if name == "folder.old/unit" {
			resource = "units/folder.fbi"
		}
		fs := newFixtureFS(t, fixtureFile{path: resource, data: `[UNITINFO]{UnitName=parsed;}`})
		u := &UnitDef{UnitName: name}
		if _, err := compileUnitSecondary(fs, u, ""); err != nil {
			t.Fatal(err)
		}
		if u.UnitName != "parsed" {
			t.Fatalf("stored name %q did not select %q", name, resource)
		}
	}
}

func TestUnitSecondaryPreservesDuplicateRecordIDs(t *testing.T) {
	// units/shared.fbi is a loose-directory winner, so discovery drops it
	// [02 R-CAT-01 §4] and it reaches the records only as the secondary
	// resource both duplicates reopen by stored name [02 R-CAT-01 §5].
	result, err := compileUnitsWithLanguage(newLooseUnitFS(newFixtureFS(t,
		fixtureFile{path: "units/a.fbi", data: `[UNITINFO]{UnitName=shared; Side=ARM;}`},
		fixtureFile{path: "units/b.fbi", data: `[UNITINFO]{UnitName=shared; Side=CORE;}`},
		fixtureFile{path: "units/shared.fbi", data: `[UNITINFO]{UnitName=renamed; ObjectName=model;}`},
	), "units/shared.fbi"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.records) != 2 || result.units["renamed"] != result.records[0] {
		t.Fatal("secondary parsing collapsed duplicate records or changed first-equal lookup")
	}
	for i, u := range result.records {
		if u.UnitDefID != uint32(i+1) || u.ObjectName != "model" || u.UnitName != "renamed" {
			t.Fatal("secondary parse changed record ID or skipped a duplicate")
		}
	}
	if result.records[0].Side != "ARM" || result.records[1].Side != "CORE" {
		t.Fatal("secondary parse lost distinct discovery fields")
	}
}

// Renaming can leave every final name unreachable without invalidating any
// retained definition ID [02 R-CAT-01 §5].
func TestUnitSecondaryUnreachableNamesKeepFinalizedIdentity(t *testing.T) {
	records := []*UnitDef{{UnitName: "z", UnitDefID: 1}, {UnitName: "b", UnitDefID: 2}, {UnitName: "a", UnitDefID: 3}}
	cat := &Catalog{unitRecords: records, Units: firstUnitNames(records), Hash: "authored-finalized"}
	if len(cat.Units) != 0 || !cat.Finalized() {
		t.Fatal("unreachable names invalidated the retained catalog")
	}
	if got, ok := cat.UnitDefByIndex(3); !ok || got != records[2] {
		t.Fatal("unreachable name lost index-based identity")
	}
}
