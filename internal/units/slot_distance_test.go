package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// distanceWordBinding builds a three-piece model whose two leaves sit at
// authored MODEL-space Z offsets from the root, plus a VM with matching piece
// state, so ComposePiece answers the two queries the distance word is built
// from. Heading, pitch and bank are all zero on a bare unit, so the composed
// origin of a leaf is its authored translation.
//
// Model space is mirrored in Z against world space [03 R-RAST-01 §2], so the
// leaf authored at model Z = −3 sits three world units FORWARD of the root and
// the one at model Z = −1 sits one world unit forward. That flip is the whole
// of the WU-19-138 correction: the word is a difference of two WORLD points,
// and reading the composed Z as if it were already world Z inverted the sign
// of every stock unit's word.
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
			{Name: "muzzle", Parent: 0, Translate: [3]numeric.Fixed{0, 0, numeric.FixedFromInt(-3)}},
			{Name: "aimfrom", Parent: 0, Translate: [3]numeric.Fixed{0, 0, numeric.FixedFromInt(-1)}},
		},
	}
	return &cob.Binding{Program: prog, VM: vm, Model: mdl, PieceMap: []int{0, 1, 2}}
}

// TestSlotDistanceWordIsTheScaledQueryMinusAimFromZ locks the slot
// initializer's distance word [06 R-WPN-05 §3] (RWU-19-39, corrected
// WU-19-138): for each slot the word is
// `trunc(1.25 × (queryPoint.z − aimFromPoint.z))` over the two composed piece
// points converted into WORLD space, in 16.16 world units — a Z difference
// alone, with the sign kept — and it is zero when the two points coincide,
// which is what a script that answers no `AimFrom*` produces (the fallback
// re-runs `Query*`).
func TestSlotDistanceWordIsTheScaledQueryMinusAimFromZ(t *testing.T) {
	binding := distanceWordBinding(t)
	u := &Unit{}
	// Slot 0: muzzle three world units forward, aim-from one world unit
	// forward — a two-world-unit forward delta, which is the stock tank
	// geometry (a flare at the end of a barrel, ahead of the turret).
	u.Slots[0].MuzzlePiece, u.Slots[0].AimOriginPiece = 1, 2
	// Slot 1: the AimFrom* fallback resolved to the Query* piece, so the two
	// points coincide [R-CB-01 §4].
	u.Slots[1].MuzzlePiece, u.Slots[1].AimOriginPiece = 1, 1
	// Slot 2: the muzzle behind the aim-from piece along world Z. The section
	// establishes the arithmetic; the stock corpus does not reach it at a spawn
	// heading, but a script that answers the two queries the other way round
	// would.
	u.Slots[2].MuzzlePiece, u.Slots[2].AimOriginPiece = 2, 1

	WriteSlotDistanceWords(u, binding)

	// 1.25 × 2.0 world units = 2.5, i.e. 163840 in 16.16. A forward-mounted
	// muzzle gives a POSITIVE word, which is what keeps the unsigned `T0`
	// divide of [06 §6.4] down at a handful of ticks.
	if got := u.Slots[0].DistanceWord; got != 163840 {
		t.Fatalf("slot 0 distance word = %d, want 163840 = trunc(1.25 × (3.0 − 1.0)) in world 16.16 [06 R-WPN-05 §3]", got)
	}
	if got := u.Slots[1].DistanceWord; got != 0 {
		t.Fatalf("slot 1 distance word = %d, want 0: with no AimFrom piece the two points coincide [06 R-WPN-05 §3]", got)
	}
	// 1.25 × -2.0 world units = -2.5, i.e. -163840 in 16.16: the sign is kept.
	if got := u.Slots[2].DistanceWord; got != -163840 {
		t.Fatalf("slot 2 distance word = %d, want -163840: the difference is signed, not a length [06 R-WPN-05 §3]", got)
	}
}

// TestSlotDistanceWordIsBuiltFromWorldNotModelZ locks the WU-19-138 correction
// on its own, because the arithmetic above would pass equally on a build that
// read the composed Z as world Z with both pieces authored the other way
// round. Composed piece coordinates are model space, mirrored in Z against
// world space [03 R-RAST-01 §2]; a muzzle authored FORWARD of the unit
// composes to a negative model Z and must yield a POSITIVE word.
//
// This is the sign the ballistic launch depends on: [06 §6.4]'s `T0` divide is
// unsigned, so an inverted word turns five ticks into some eleven thousand and
// the `T0 × gravity` pre-decrement buries the shell on its birth tick.
func TestSlotDistanceWordIsBuiltFromWorldNotModelZ(t *testing.T) {
	binding := distanceWordBinding(t)
	u := &Unit{Z: numeric.FixedFromInt(1000)} // the unit term must cancel
	u.Slots[0].MuzzlePiece, u.Slots[0].AimOriginPiece = 1, 2
	WriteSlotDistanceWords(u, binding)
	if got := u.Slots[0].DistanceWord; got <= 0 {
		t.Fatalf("a muzzle mounted forward of the aim-from piece stored %d: the word is built from model Z, not world Z [06 R-WPN-05 §3 correction]", got)
	}
	// The unit's own Z is common to both points and must not survive the
	// difference.
	far := &Unit{Z: numeric.FixedFromInt(-4000)}
	far.Slots[0].MuzzlePiece, far.Slots[0].AimOriginPiece = 1, 2
	WriteSlotDistanceWords(far, binding)
	if far.Slots[0].DistanceWord != u.Slots[0].DistanceWord {
		t.Fatalf("the word moved with the unit position (%d vs %d); it is a difference of two points on the same unit [06 R-WPN-05 §3]",
			far.Slots[0].DistanceWord, u.Slots[0].DistanceWord)
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
