//go:build retail

package content

import "testing"

// TestRetailCategoryRegistry checks the category membership shape against the
// unmodified reference install; assets remain external to the repository.
func TestRetailCategoryRegistry(t *testing.T) {
	c := compiledRetailCatalog(t)
	arm, ok := c.Category("ARM")
	if !ok || arm.IsZero() {
		t.Fatal("retail ARM category is missing or empty")
	}
	core, ok := c.Category("core")
	if !ok || core.IsZero() {
		t.Fatal("retail CORE category is missing or empty")
	}
	armcom, ok := c.Unit("armcom")
	if !ok || !arm.Contains(armcom.UnitDefID) {
		t.Fatalf("ARM mask does not contain ARMCOM ID %d", armcom.UnitDefID)
	}
	corcom, ok := c.Unit("corcom")
	if !ok || !core.Contains(corcom.UnitDefID) {
		t.Fatalf("CORE mask does not contain CORCOM ID %d", corcom.UnitDefID)
	}
	if arm.Contains(corcom.UnitDefID) || core.Contains(armcom.UnitDefID) {
		t.Fatal("ARM/CORE masks unexpectedly overlap commander definitions")
	}
	for _, u := range c.Units {
		if u == nil || u.UnitDefID == 0 || u.UnitDefID >= CategoryMaskWords*32 {
			t.Fatalf("invalid retail unit definition ID %d", u.UnitDefID)
		}
	}
}
