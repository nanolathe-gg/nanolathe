package airdiag

import (
	"sort"
	"testing"
)

func TestScanAirBaseDefs(t *testing.T) {
	h := newHarness(t)
	var names []string
	for _, key := range h.Catalog.SortedUnitKeys() {
		def, ok := h.Catalog.Unit(key)
		if !ok || def == nil {
			continue
		}
		if def.IsAirBase {
			names = append(names, def.UnitName)
		}
	}
	sort.Strings(names)
	t.Logf("isairbase definitions (%d): %v", len(names), names)
	if d, ok := h.Catalog.Unit("ARMAP"); ok && d != nil {
		t.Logf("ARMAP: isairbase=%v builder=%v canfly=%v footprint=%dx%d", d.IsAirBase, d.Builder, d.CanFly, d.FootprintX, d.FootprintZ)
	}
}
