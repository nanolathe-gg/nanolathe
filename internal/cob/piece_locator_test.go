package cob

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The authored (file-space) mounting point the three locator tests share, one
// per consuming package. [03 §2.4] C20's load-time half-turn negates the first
// and third coordinate of every parent translation, so an authored
// `(ax, ay, az)` is stored as `(−ax, ay, −az)`; the locator then negates the
// composed Z once on output, and the world offset is `(−ax, ay, +az)`
// [03 R-RAST-01 §8].
const (
	locatorAuthoredX = 2
	locatorAuthoredY = 1
	locatorAuthoredZ = 30
)

// locatorFixture is a two-piece model whose leaf carries the authored mount
// above, plus a binding whose COB index 0 maps to that leaf. The COB and model
// piece orders deliberately differ so the test cannot pass by reading the
// wrong index.
func locatorFixture() *Binding {
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Children: []int{1}},
			{Name: "flare", Parent: 0, Translate: [3]numeric.Fixed{
				numeric.FixedFromInt(-locatorAuthoredX),
				numeric.FixedFromInt(locatorAuthoredY),
				numeric.FixedFromInt(-locatorAuthoredZ),
			}},
		},
	}
	vm := &VM{Pieces: make([]model.PieceState, 2)}
	return &Binding{Model: mdl, VM: vm, PieceMap: []int{1, 0}}
}

// TestComposePieceReturnsTheLocatorWorldOffset locks [03 R-RAST-01 §8]:
// ComposePiece is retail's piece locator, and what it returns is the WORLD
// offset — the model-space composition with Z negated once, on output. A piece
// authored at `(ax, ay, az)` on a unit at heading 0 — which faces world −Z
// [04 R-MOV-01 §4] — is `unit + (−ax, ay, +az)`: the authored X mirrored, the
// authored Z kept.
//
// The negation belongs here and nowhere else. Every consumer that forms a
// world point adds this triple with no further sign change, so a second
// negation at a call site would cancel this one and put a forward-mounted
// muzzle, spray source or build plate the same distance BEHIND the unit.
func TestComposePieceReturnsTheLocatorWorldOffset(t *testing.T) {
	b := locatorFixture()

	got, ok := b.ComposePiece(0, 0, 0, 0)
	if !ok {
		t.Fatal("locator declined a mapped piece")
	}
	want := [3]numeric.Fixed{
		numeric.FixedFromInt(-locatorAuthoredX),
		numeric.FixedFromInt(locatorAuthoredY),
		numeric.FixedFromInt(locatorAuthoredZ),
	}
	if got != want {
		t.Fatalf("locator offset at heading 0 = %v, want %v: authored (%d,%d,%d) is world (−ax, ay, +az) [03 R-RAST-01 §8]",
			got, want, locatorAuthoredX, locatorAuthoredY, locatorAuthoredZ)
	}

	// The mirror is applied AFTER the whole chain, the unit's own heading
	// included — never inside the composition. A half turn reverses the
	// composition's X and Z, so both the world X and the world Z reverse with
	// it [03 R-RAST-01 §8] step 2.
	half, ok := b.ComposePiece(0, 0x8000, 0, 0)
	if !ok {
		t.Fatal("locator declined a mapped piece at heading 0x8000")
	}
	if half[0] != numeric.FixedFromInt(locatorAuthoredX) || half[1] != numeric.FixedFromInt(locatorAuthoredY) || half[2] != numeric.FixedFromInt(-locatorAuthoredZ) {
		t.Fatalf("locator offset at heading 0x8000 = %v, want [%d %d %d]: the fold rotates, the mirror follows it",
			half, numeric.FixedFromInt(locatorAuthoredX), numeric.FixedFromInt(locatorAuthoredY), numeric.FixedFromInt(-locatorAuthoredZ))
	}
}
