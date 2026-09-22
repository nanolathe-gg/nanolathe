package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

func TestOffMapFilingKeepsCanonicalRelinkOrder(t *testing.T) {
	f := newFilingFixture(12)
	g, _ := sectorGrid(f.sectorFixture)
	g.AttachOverlap(f, func(owner uint8) uint8 { return owner })
	for _, id := range []int{9, 2, 6} {
		f.addAt(id, activeState, Cell{X: -2, Z: 10}, 1, 1)
		g.StampPlane(PlaneAir, f.rect[id].anchor, 1, 1, id)
	}
	check := func(want []pool.Handle) {
		t.Helper()
		var got []pool.Handle
		previous := ^uint64(0)
		g.VisitOffMapFiled(func(h pool.Handle, seq uint64) bool {
			if seq >= previous {
				t.Fatalf("filing sequence %d follows %d", seq, previous)
			}
			previous = seq
			got = append(got, h)
			return true
		})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("off-map order %v, want %v", got, want)
		}
	}
	check([]pool.Handle{6, 2, 9})
	f.moveTo(g, 9, Cell{X: -3, Z: 10}) // same record keeps its place
	check([]pool.Handle{6, 2, 9})
	f.moveTo(g, 2, Cell{X: 10, Z: 10})
	check([]pool.Handle{6, 9})
	f.moveTo(g, 2, Cell{X: -2, Z: 10})
	check([]pool.Handle{2, 6, 9})
	g.ForgetFiling(6)
	check([]pool.Handle{2, 9})
	visits := 0
	g.VisitOffMapFiled(func(pool.Handle, uint64) bool { visits++; return false })
	if visits != 1 {
		t.Fatalf("early stop made %d visits", visits)
	}
}
