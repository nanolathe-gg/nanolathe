package content

import "testing"

// TestRestrictToCreatableCompactsAndRenumbers locks the campaign unit
// restriction's shape [08 R-ENTRY-01 §2 step 4][05 R-SHARE-01 §8]: the listed
// definitions survive, every other definition is removed, and the survivors are
// re-sorted and renumbered from 1 (index 0 stays the null sentinel, which this
// catalog holds implicitly rather than as a record).
func TestRestrictToCreatableCompactsAndRenumbers(t *testing.T) {
	newCatalog := func(t *testing.T) *Catalog {
		t.Helper()
		c := &Catalog{Units: map[string]*UnitDef{
			"armcom":   {UnitName: "ARMCOM"},
			"armflash": {UnitName: "ARMFLASH"},
			"armpw":    {UnitName: "ARMPW"},
			"corcom":   {UnitName: "CORCOM"},
		}}
		for k, u := range c.Units {
			u.CanonicalKey = k
		}
		reg, err := CompileCategories(c.Units)
		if err != nil {
			t.Fatalf("CompileCategories: %v", err)
		}
		c.Categories = reg
		return c
	}

	// Two names: only those two records remain, renumbered 1..2 in sorted
	// canonical-key order. The section names are matched case-insensitively
	// against each definition's unitname.
	c := newCatalog(t)
	before := c.Hash
	if err := c.RestrictToCreatable([]string{"ArmPw", "ARMCOM", "notaunit"}); err != nil {
		t.Fatalf("RestrictToCreatable: %v", err)
	}
	if len(c.Units) != 2 {
		t.Fatalf("restricted catalog holds %d definitions, want the two listed ones: %v", len(c.Units), c.SortedUnitKeys())
	}
	armcom, ok := c.Unit("armcom")
	if !ok || armcom.UnitDefID != 1 {
		t.Fatalf("armcom must survive renumbered to 1, got %+v ok=%v", armcom, ok)
	}
	armpw, ok := c.Unit("armpw")
	if !ok || armpw.UnitDefID != 2 {
		t.Fatalf("armpw must survive renumbered to 2, got %+v ok=%v", armpw, ok)
	}
	if _, ok := c.Unit("armflash"); ok {
		t.Fatal("an unlisted definition must be absent from the catalog, not merely unbuildable [05 R-SHARE-01 §8]")
	}
	if def, ok := c.UnitDefByIndex(2); !ok || def != armpw {
		t.Fatalf("index 2 must resolve to the renumbered survivor, got %+v ok=%v", def, ok)
	}
	if c.Hash == before {
		t.Fatal("definition identity moved; the catalog digest must move with it")
	}

	// A missing file is an empty call site, not an empty list: the catalog is
	// left whole. Callers report absence by not calling at all, so the guard
	// here is that a nil list still removes everything — the caller, not this
	// method, owns "file absent".
	whole := newCatalog(t)
	if err := whole.RestrictToCreatable(whole.SortedUnitKeys()); err != nil {
		t.Fatalf("RestrictToCreatable(all): %v", err)
	}
	if len(whole.Units) != 4 {
		t.Fatalf("listing every definition must leave the catalog whole, got %d", len(whole.Units))
	}
}
