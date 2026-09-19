package content

import "testing"

// TestLOSTablesFillSlotsByGeneratedName locks the loader's storage rule
// [03 R-COMP-02 §1]: the list is sized to the declared numtables and each
// zero-based slot d is filled from the section the loader NAMES by building
// "TABLE" followed by d+1. File order carries no meaning, an absent section
// leaves that slot's empty line list, and a name the loader never builds is
// never read.
//
// Compacting the discovered sections instead would pull TABLE4 into slot 2 and
// shift every later table down, skewing the terrain-ray footprint of every
// observer whose group lands past the gap.
//
// The fixture is authored here, not copied from an install.
func TestLOSTablesFillSlotsByGeneratedName(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "gamedata/los.tdf", data: `
[TABLEINFO]
{
    numtables=4;
}
[TABLE2]
{
    numlines=1;
    line1=2,0,1,0,2;
}
[TABLE1]
{
    numlines=1;
    line1=1,0,1;
}
[TABLE4]
{
    numlines=1;
    line1=1,0,4;
}
[TABLE6]
{
    numlines=1;
    line1=1,0,6;
}
`})
	lt, err := CompileLOSTables(fs, RetailLimits())
	if err != nil {
		t.Fatalf("CompileLOSTables: %v", err)
	}
	if lt.NumTables != 4 {
		t.Fatalf("numtables = %d, want the declared 4", lt.NumTables)
	}
	// Four slots, then the undeclared residue.
	if len(lt.Tables) != 5 {
		t.Fatalf("compiled %d tables, want four slots plus the undeclared TABLE6", len(lt.Tables))
	}
	for slot, want := range []struct {
		num   int
		lines int
	}{{1, 1}, {2, 1}, {3, 0}, {4, 1}} {
		got := lt.Tables[slot]
		if got.TableNum != want.num || len(got.Lines) != want.lines {
			t.Fatalf("slot %d = TABLE%d with %d lines, want TABLE%d with %d",
				slot, got.TableNum, len(got.Lines), want.num, want.lines)
		}
	}
	// Slot 1 is TABLE2 even though TABLE2 is authored first.
	if got := lt.Tables[1].Lines[0]; len(got) != 5 || got[4] != 2 {
		t.Fatalf("slot 1 line = %v, want TABLE2's two-step line", got)
	}
	// Slot 2 is the gap: no [TABLE3] section, so an empty line list — TABLE4
	// must NOT have moved down into it.
	if lt.Tables[2].NumLines != 0 || len(lt.Tables[2].Lines) != 0 {
		t.Fatalf("slot 2 = %#v, want the empty record of an unauthored TABLE3", lt.Tables[2])
	}
	// The undeclared section is kept after the slots so nothing is lost (SC9),
	// and no slot addresses it.
	if got := lt.Tables[4]; got.TableNum != 6 || len(got.Lines) != 1 {
		t.Fatalf("residue entry = %#v, want the undeclared TABLE6", got)
	}
}

// TestLOSTablesKeepUndeclaredSections records the reference install's shape in
// miniature (SC9): more sections than the declared count, with the extras kept
// after the slots and unreachable.
func TestLOSTablesKeepUndeclaredSections(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "gamedata/los.tdf", data: `
[TABLEINFO]
{
    numtables=2;
}
[TABLE1]
{
    numlines=1;
    line1=1,0,1;
}
[TABLE2]
{
    numlines=1;
    line1=2,0,1,0,2;
}
[TABLE3]
{
    numlines=1;
    line1=3,0,1,0,2,0,3;
}
`})
	lt, err := CompileLOSTables(fs, RetailLimits())
	if err != nil {
		t.Fatalf("CompileLOSTables: %v", err)
	}
	if len(lt.Tables) != 3 {
		t.Fatalf("compiled %d tables, want two slots plus the undeclared TABLE3", len(lt.Tables))
	}
	for i, wantNum := range []int{1, 2, 3} {
		if lt.Tables[i].TableNum != wantNum {
			t.Fatalf("entry %d = TABLE%d, want TABLE%d", i, lt.Tables[i].TableNum, wantNum)
		}
	}
	// The clamp is the declared count, so the group index never reaches the
	// third entry; that is locked in internal/visibility's raster tests.
	if lt.NumTables != 2 {
		t.Fatalf("numtables = %d, want the declared 2", lt.NumTables)
	}
}
