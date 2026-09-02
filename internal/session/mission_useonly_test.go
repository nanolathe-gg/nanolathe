package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

// TestUseOnlyRestrictionAtBattleEntry locks battle entry's unit-restriction
// loader for kind 1 [08 R-ENTRY-01 §2 step 4][05 R-SHARE-01 §8]: a present file
// leaves the catalog holding only the definitions it names, renumbered, and a
// missing file leaves the catalog whole and untouched.
func TestUseOnlyRestrictionAtBattleEntry(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "camps", "useonly"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The retail files carry one empty section per allowed unit; the section
	// name is the unit name.
	body := "[ARMCOM]\n\t{\n\t}\n[armpw]\n\t{\n\t}\n"
	if err := os.WriteFile(filepath.Join(root, "camps", "useonly", "two.tdf"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatalf("mount: %v", err)
	}

	base := minimalCatalogForStrict()
	armpwDef := *base.Units["armcom"]
	armpwDef.UnitName = "armpw"
	armpwDef.CanonicalKey = "armpw"
	armpwDef.Commander = false
	base.Units["armpw"] = &armpwDef
	whole := len(base.Units)
	if whole < 3 {
		t.Fatalf("fixture needs a definition the restriction can remove, have %d", whole)
	}

	restricted, err := applyUseOnlyRestriction(fs, base, "camps/useonly/two.tdf")
	if err != nil {
		t.Fatalf("applyUseOnlyRestriction: %v", err)
	}
	if restricted == base {
		t.Fatal("the restriction must not be applied to the shared catalog: it lasts one battle [05 R-SHARE-01 §8]")
	}
	if len(base.Units) != whole {
		t.Fatalf("the input catalog was mutated: %d definitions, want %d", len(base.Units), whole)
	}
	if len(restricted.Units) != 2 {
		t.Fatalf("restricted catalog holds %v, want exactly the two named units", restricted.SortedUnitKeys())
	}
	armcom, ok := restricted.Unit("armcom")
	if !ok || armcom.UnitDefID != 1 {
		t.Fatalf("armcom must survive renumbered to 1: %+v ok=%v", armcom, ok)
	}
	if armpw, ok := restricted.Unit("armpw"); !ok || armpw.UnitDefID != 2 {
		t.Fatalf("armpw must survive renumbered to 2: %+v ok=%v", armpw, ok)
	}
	if _, ok := restricted.Unit("corcom"); ok {
		t.Fatal("an unlisted definition must be absent from the catalog")
	}

	// A missing file leaves every definition creatable, and hands back the very
	// catalog it was given.
	same, err := applyUseOnlyRestriction(fs, base, "camps/useonly/absent.tdf")
	if err != nil {
		t.Fatalf("applyUseOnlyRestriction(missing): %v", err)
	}
	if same != base || len(same.Units) != whole {
		t.Fatalf("a missing restriction file must leave the catalog whole and unwrapped, got %d definitions", len(same.Units))
	}
	// No authored UseOnlyUnits key routes to an empty path and never touches
	// the VFS.
	if none, err := applyUseOnlyRestriction(fs, base, ""); err != nil || none != base {
		t.Fatalf("an unauthored restriction path must be a no-op: err=%v", err)
	}
}
