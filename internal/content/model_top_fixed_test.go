package content

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestModelTopFixedIsTheSameWalkAsModelTop locks the relationship between the
// definition's two model-top forms: ModelTopFixed is the full 16.16 dword the
// walk returns and ModelTop is that dword's byte-masked whole-unit high word
// [06 R-DMG-01 §7][03 §3.2]. Both come from one walk of one model, so the
// second must be derivable from the first — a drift between them would mean
// the contact test and the LOS eye height disagree about the same unit.
func TestModelTopFixedIsTheSameWalkAsModelTop(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })

	// A handful of stock models rather than the whole catalog: compiling is
	// the expensive part and the relationship is per-model.
	defs := map[string]*UnitDef{
		"armcom":   {ObjectName: "armcom"},
		"armflash": {ObjectName: "armflash"},
		"corcom":   {ObjectName: "corcom"},
		"armpw":    {ObjectName: "armpw"},
	}
	if err := validateRequiredModels(fs, defs, nil, nil); err != nil {
		t.Fatalf("validateRequiredModels: %v", err)
	}

	for name, def := range defs {
		if def.ModelTopFixed < 0 {
			t.Fatalf("%s: the walk is floored at zero, got %d", name, def.ModelTopFixed)
		}
		if want := (def.ModelTopFixed >> 16) & 0xFF; def.ModelTop != want {
			t.Fatalf("%s: ModelTop = %d, want the high word %d of ModelTopFixed %d",
				name, def.ModelTop, want, def.ModelTopFixed)
		}
	}
}

// TestModelTopFixedIsOutsideTheCanonicalIdentity locks that the new field does
// not enter the definition hash, and therefore does not change the catalog
// hash: the canonical string is the FBI record's identity, and both model-top
// forms come from a different asset with its own provenance. Two definitions
// that differ only in their model tops render the same canonical bytes.
func TestModelTopFixedIsOutsideTheCanonicalIdentity(t *testing.T) {
	a := &UnitDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "x"}, UnitName: "x"}
	b := *a
	b.ModelTop = 39
	b.ModelTopFixed = 39 << 16
	if got, want := string(writeUnitCanonical(&b)), string(writeUnitCanonical(a)); got != want {
		t.Fatal("the model-top fields must not appear in the canonical identity string")
	}
	if HashDefinition(writeUnitCanonical(&b)) != HashDefinition(writeUnitCanonical(a)) {
		t.Fatal("the model-top fields must not change the definition hash")
	}
}
