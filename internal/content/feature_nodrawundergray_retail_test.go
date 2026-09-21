//go:build retail

package content

import "testing"

// TestStockWallFeaturesCarryNoDrawUnderGray locks the reach on the reference
// install: the four forced sections exist, none of them authors the key, and
// all four compile with the flag set, so the renderer's placer-or-LOS gate
// applies to every stock dragon's-teeth and fortification placement
// [02 §5 "Four section names force nodrawundergray"][05 R-FEAT-01 §1].
func TestStockWallFeaturesCarryNoDrawUnderGray(t *testing.T) {
	c := compiledRetailCatalog(t)
	for _, name := range []string{"dragonsteeth", "dragonsteeth_core", "fortification", "fortification_core"} {
		def, ok := c.Features[name]
		if !ok || def == nil {
			t.Fatalf("stock feature %s missing from the catalog", name)
		}
		if !def.NoDrawUnderGray {
			t.Fatalf("stock feature %s compiled NoDrawUnderGray=false; the section name must force it [05 R-FEAT-01 §1]", name)
		}
	}
}
