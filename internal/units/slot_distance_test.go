package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// distanceWordBinding builds a three-piece model whose two leaves sit at
// authored world-Z offsets from the root, plus a VM with matching piece state,
// so ComposePiece answers the two queries the distance word is built from.
// Heading, pitch and bank are all zero on a bare unit, so the composed origin
// of a leaf is its authored translation.
func distanceWordBinding(t *testing.T) *cob.Binding {
	t.Helper()
	prog := &cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"Create": 0},
		Pieces:      []string{"base", "muzzle", "aimfrom"},
		ScriptsByID: []int{0},
	}
	vm := cob.NewVM(prog)
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Children: []int{1, 2}},
			{Name: "muzzle", Parent: 0, Translate: [3]numeric.Fixed{0, 0, numeric.FixedFromInt(3)}},
			{Name: "aimfrom", Parent: 0, Translate: [3]numeric.Fixed{0, 0, numeric.FixedFromInt(1)}},
		},
	}
	return &cob.Binding{Program: prog, VM: vm, Model: mdl, PieceMap: []int{0, 1, 2}}
}

// TestSlotDistanceWordIsTheScaledQueryMinusAimFromZ locks the slot
// initializer's distance word [06 R-WPN-05 §3] (RWU-19-39): for each slot the
// word is `trunc(1.25 × (queryPoint.z − aimFromPoint.z))` over the two composed
// piece points in 16.16 world units — a Z difference alone, with the sign kept
// — and it is zero when the two points coincide, which is what a script that
// answers no `AimFrom*` produces (the fallback re-runs `Query*`).
func TestSlotDistanceWordIsTheScaledQueryMinusAimFromZ(t *testing.T) {
	binding := distanceWordBinding(t)
	u := &Unit{}
	// Slot 0: muzzle at Z=3, aim-from at Z=1 — a two-world-unit forward delta.
	u.Slots[0].MuzzlePiece, u.Slots[0].AimOriginPiece = 1, 2
	// Slot 1: the AimFrom* fallback resolved to the Query* piece, so the two
	// points coincide [R-CB-01 §4].
	u.Slots[1].MuzzlePiece, u.Slots[1].AimOriginPiece = 1, 1
	// Slot 2: the muzzle behind the aim-from piece along world Z. The section
	// establishes the arithmetic and leaves the case's reachability Unknown.
	u.Slots[2].MuzzlePiece, u.Slots[2].AimOriginPiece = 2, 1

	WriteSlotDistanceWords(u, binding)

	// 1.25 × 2.0 world units = 2.5, i.e. 163840 in 16.16.
	if got := u.Slots[0].DistanceWord; got != 163840 {
		t.Fatalf("slot 0 distance word = %d, want 163840 = trunc(1.25 × (3.0 − 1.0)) in 16.16 [06 R-WPN-05 §3]", got)
	}
	if got := u.Slots[1].DistanceWord; got != 0 {
		t.Fatalf("slot 1 distance word = %d, want 0: with no AimFrom piece the two points coincide [06 R-WPN-05 §3]", got)
	}
	// 1.25 × -2.0 world units = -2.5, i.e. -163840 in 16.16: the sign is kept.
	if got := u.Slots[2].DistanceWord; got != -163840 {
		t.Fatalf("slot 2 distance word = %d, want -163840: the difference is signed, not a length [06 R-WPN-05 §3]", got)
	}
}

// TestSlotDistanceWordIsZeroWithoutAResolvablePiece locks the "script answered
// neither query" case: a negative `Query*` identity is the ordinary
// unit-origin muzzle path [04 §5.3], the two points coincide, and the stored
// value is zero [06 R-WPN-05 §3].
func TestSlotDistanceWordIsZeroWithoutAResolvablePiece(t *testing.T) {
	binding := distanceWordBinding(t)
	u := &Unit{}
	for i := 0; i < NumSlots; i++ {
		u.Slots[i].MuzzlePiece = -1
		u.Slots[i].AimOriginPiece = -1
		u.Slots[i].DistanceWord = 999 // must be overwritten, not left behind
	}
	WriteSlotDistanceWords(u, binding)
	for i := 0; i < NumSlots; i++ {
		if got := u.Slots[i].DistanceWord; got != 0 {
			t.Fatalf("slot %d distance word = %d, want 0 [06 R-WPN-05 §3]", i, got)
		}
	}
}
