//go:build retail

package content

import (
	"sort"
	"testing"
)

func TestZZHoverCensus(t *testing.T) {
	cat := compiledRetailCatalog(t)
	type row struct {
		key                    string
		waterline, maxVelocity int32
		upright, floater       bool
	}
	var rows []row
	for k, u := range cat.Units {
		if u == nil || !u.CanHover {
			continue
		}
		rows = append(rows, row{k, u.Waterline, u.MaxVelocity, u.Upright, u.Floater})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	counts := map[int32]int{}
	for _, r := range rows {
		counts[r.waterline]++
		t.Logf("%-14s waterline=%-4d maxvel=%-9d upright=%-5v floater=%v", r.key, r.waterline, r.maxVelocity, r.upright, r.floater)
	}
	t.Logf("canhover unit count: %d", len(rows))
	var wl []int32
	for k := range counts {
		wl = append(wl, k)
	}
	sort.Slice(wl, func(i, j int) bool { return wl[i] < wl[j] })
	for _, w := range wl {
		t.Logf("WATERLINE %d -> %d units", w, counts[w])
	}
}
