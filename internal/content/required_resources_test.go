package content

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestCompileSightShapesUsesAuthoredPluralMaskEntry(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "anims/vismasks.gaf", data: string(testGAFEntries(
			testGAFEntry{name: "decoy", frames: 1, duration: 1},
			testGAFEntry{name: "vismask", frames: 10, duration: 1},
		))},
	)
	shapes, err := CompileSightShapes(fs)
	if err != nil {
		t.Fatalf("CompileSightShapes: %v", err)
	}
	if shapes.Count() != 10 {
		t.Fatalf("shape count = %d, want authored vismask entry's 10 frames", shapes.Count())
	}
	if shapes.Provenance.LogicalPath != "anims/vismasks.gaf" {
		t.Fatalf("shape provenance = %#v, want plural authored path", shapes.Provenance)
	}
}

func TestCompileSightShapesRequiresPluralAuthoredResource(t *testing.T) {
	fs := newFixtureFS(t,
		fixtureFile{path: "anims/vismask.gaf", data: string(testGAF("vismask", 10, 1))},
	)
	_, err := CompileSightShapes(fs)
	if err == nil {
		t.Fatal("CompileSightShapes accepted the singular cursor GAF as a visibility table")
	}
	for _, want := range []string{
		"nanolathe: required authored resource:",
		"logical path anims/vismasks.gaf",
		"providers searched []",
		"expected the retail visibility-mask GAF entry vismask",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestCompileLOSTablesRequiresAuthoredResource(t *testing.T) {
	_, err := CompileLOSTables(newFixtureFS(t), RetailLimits())
	if err == nil {
		t.Fatal("CompileLOSTables accepted a missing required resource")
	}
	for _, want := range []string{
		"nanolathe: required authored resource:",
		"logical path gamedata/los.tdf",
		"providers searched []",
		"expected retail LOS.TDF terrain-ray tables",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestCompileLOSTablesReadsAuthoredFixture(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "gamedata/los.tdf", data: `
[TABLEINFO]
{
    numtables=1;
}
[TABLE1]
{
    numlines=1;
    line1=1,0,1;
}
`})
	lt, err := CompileLOSTables(fs, RetailLimits())
	if err != nil {
		t.Fatalf("CompileLOSTables: %v", err)
	}
	if lt.NumTables != 1 || len(lt.Tables) != 1 || len(lt.Tables[0].Lines) != 1 {
		t.Fatalf("compiled LOS fixture = %#v, want one authored table and line", lt)
	}
}

func TestCompileMeteorMissingResourceKeepsRetailEmptyDefaults(t *testing.T) {
	md, err := CompileMeteor(vfs.New(), RetailLimits())
	if err != nil {
		t.Fatalf("CompileMeteor missing resource: %v", err)
	}
	if md.MeteorWeapon != "" || md.MeteorRadius != 0 || md.MeteorDensity != 0 || md.MeteorDuration != 0 || md.MeteorInterval != 0 {
		t.Fatalf("missing meteor resource changed defaults: %#v", md)
	}
}

func TestCompileMeteorReadsAuthoredFixture(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "gamedata/meteor.tdf", data: `
[Default]
{
    MeteorWeapon=METEOR;
    MeteorRadius=300;
    MeteorDensity=2;
    MeteorDuration=5;
    MeteorInterval=60;
}
`})
	md, err := CompileMeteor(fs, RetailLimits())
	if err != nil {
		t.Fatalf("CompileMeteor: %v", err)
	}
	if md.MeteorWeapon != "METEOR" || md.MeteorRadius != 300 || md.MeteorDensity != 2 || md.MeteorDuration != 5 || md.MeteorInterval != 60 {
		t.Fatalf("compiled meteor fixture = %#v", md)
	}
}
