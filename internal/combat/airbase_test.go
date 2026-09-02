package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// padDef is a definition carrying both flags the third list requires
// [06 §3.1 "the third list"].
func padDef(builder, airbase bool) *content.UnitDef {
	return &content.UnitDef{Builder: builder, IsAirBase: airbase}
}

func pad(h pool.Handle, owner uint8, x, z int) *units.Unit {
	return &units.Unit{
		Handle:    h,
		Owner:     owner,
		Alive:     true,
		Activated: true,
		Def:       padDef(true, true),
		X:         fixed(x),
		Z:         fixed(z),
	}
}

// TestAirBaseListMembershipAtRebuild locks the third list's membership rule:
// fully built, alive, death latch clear, `builder` AND `isairbase`, activation
// bit set [06 §3.1 "the third list"][04 R-AIR-01 §11]. Each case flips exactly
// one input off a member.
func TestAirBaseListMembershipAtRebuild(t *testing.T) {
	member := pad(1, 0, 0, 0)
	if !IsAirBaseListMember(member) {
		t.Fatalf("a fully built, activated, alive builder+isairbase unit is a member [06 §3.1]")
	}

	cases := []struct {
		name string
		mut  func(u *units.Unit)
	}{
		{"not a builder", func(u *units.Unit) { u.Def = padDef(false, true) }},
		{"not an airbase", func(u *units.Unit) { u.Def = padDef(true, false) }},
		{"deactivated", func(u *units.Unit) { u.Activated = false }},
		{"still under construction", func(u *units.Unit) { u.Remaining = 0.5 }},
		{"alive bit clear", func(u *units.Unit) { u.Alive = false }},
		{"death latch set", func(u *units.Unit) { u.Dying = true }},
		{"no definition", func(u *units.Unit) { u.Def = nil }},
	}
	for _, c := range cases {
		u := pad(1, 0, 0, 0)
		c.mut(u)
		if IsAirBaseListMember(u) {
			t.Errorf("%s must not join the third list [06 §3.1][04 R-AIR-01 §11]", c.name)
		}
	}
}

// TestRebuildAirBaseListAllianceRowAndOrder locks two things: the rebuild's
// friendly test is the candidate owner's one-directional alliance row toward
// the registry's ally group [05 R-SHARE-01 §1][06 §3.1], not the symmetric
// predicate; and members are appended in unit-array order (I1).
func TestRebuildAirBaseListAllianceRowAndOrder(t *testing.T) {
	own := pad(4, 0, 0, 0)
	ally := pad(2, 1, 0, 0)   // declares toward group 0
	oneWay := pad(7, 2, 0, 0) // group 0 declares toward it, it does not declare back
	enemy := pad(9, 3, 0, 0)  // no declaration either way
	arr := []*units.Unit{ally, own, oneWay, enemy}

	// Row A of `from` indexed by `toward` [05 R-SHARE-01 §1].
	declares := func(from, toward uint8) bool {
		return from == 1 && toward == 0
	}

	got := RebuildAirBaseList(arr, 0, declares)
	want := []pool.Handle{2, 4}
	if len(got) != len(want) {
		t.Fatalf("third list %v, want %v [06 §3.1]", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("third list %v, want %v — members append in unit-array order [06 §3.1] (I1)", got, want)
		}
	}
	if len(RebuildAirBaseList(arr, 0, nil)) != 1 {
		t.Fatalf("with no alliance rows composed only the ally group's own units are friendly [06 §3.1]")
	}
}

// TestScanAirBaseListRadiusIsInclusive locks the scan's admission distance:
// `(dx² >> 32) + (dz² >> 32) <= 0xF00²` on the 16.16 positions, whole world
// units squared, **inclusive** [04 R-AIR-01 §11][06 §3.1].
func TestScanAirBaseListRadiusIsInclusive(t *testing.T) {
	r := int(AirBaseSeekRadius)
	on := pad(3, 0, r, 0)     // exactly at the radius: admitted
	past := pad(5, 0, r+1, 0) // one world unit further: rejected
	world := map[pool.Handle]*units.Unit{3: on, 5: past}
	lookup := func(h pool.Handle) *units.Unit { return world[h] }

	got := ScanAirBaseList(0, 0, []pool.Handle{3, 5}, lookup)
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("scan admitted %v, want [3] — the radius compare is inclusive [04 R-AIR-01 §11]", got)
	}
}

// TestScanAirBaseListOrderFlagsAndLiveness locks three scan contracts at once
// [04 R-AIR-01 §11]: admitted entries are pushed in **list** order with nothing
// scored or sorted; the three admission flags are re-tested; and liveness is
// **not** — a pad destroyed since the rebuild is still offered, because the
// landing order's own pad query rejects it later [04 R-AIR-01 §6].
func TestScanAirBaseListOrderFlagsAndLiveness(t *testing.T) {
	high := pad(9, 0, 10, 0)
	low := pad(2, 0, 20, 0)
	dead := pad(4, 0, 30, 0)
	dead.Alive = false
	dead.Dying = true
	off := pad(6, 0, 40, 0)
	off.Activated = false
	notPad := pad(8, 0, 50, 0)
	notPad.Def = padDef(true, false)

	world := map[pool.Handle]*units.Unit{9: high, 2: low, 4: dead, 6: off, 8: notPad}
	lookup := func(h pool.Handle) *units.Unit { return world[h] }

	// The list order the registry produced, deliberately not handle-ascending.
	list := []pool.Handle{9, 2, 4, 6, 8}
	got := ScanAirBaseList(0, 0, list, lookup)
	want := []pool.Handle{9, 2, 4}
	if len(got) != len(want) {
		t.Fatalf("scan admitted %v, want %v [04 R-AIR-01 §11]", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scan admitted %v, want %v — list order, flags re-tested, liveness not [04 R-AIR-01 §11]", got, want)
		}
	}
}
