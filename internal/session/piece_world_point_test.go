package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestPieceWorldPosIsUnitPlusLocatorOffset locks the emit-sfx POINT clause of
// [04 R-COB-03 §6] against [03 R-RAST-01 §8]: the piece's world point is the
// unit's position PLUS the locator's triple, with no further sign change,
// because the locator has already applied the model/world Z mirror once on its
// own output.
//
// The fixture mounts the piece at authored `(2, 1, 30)`, which [03 §2.4] C20's
// load-time half-turn stores as `(−2, 1, −30)`. On a unit at heading 0 —
// facing world −Z [04 R-MOV-01 §4] — the spawn point is therefore
// `unit + (−2, 1, +30)`: the authored X mirrored, the authored Z kept. The
// nano spray source of [03 §5.5] and the two piece-position COB ports reach
// the same locator and share this answer.
//
// The VECTOR clause of the same section keeps effectWorldPoint, which
// subtracts: it transforms the piece's own vertices, which are genuinely model
// space and have not been through the locator's output negation. One mirror,
// applied once on each path.
func TestPieceWorldPosIsUnitPlusLocatorOffset(t *testing.T) {
	const (
		authoredX = 2
		authoredY = 1
		authoredZ = 30
	)
	s, _ := newStripTestSession(5, 5)
	s.Units = units.NewSliced(4, nil)
	def := &content.UnitDef{MaxDamage: 100, Limit: -1, Script: fixtureCOBProgram()}
	h, err := s.Units.Create(def, 0, numeric.FixedFromInt(100), numeric.FixedFromInt(200), numeric.FixedFromInt(300))
	if err != nil {
		t.Fatalf("create fixture unit: %v", err)
	}
	u := s.Units.Unit(h)
	if u == nil {
		t.Fatal("fixture unit missing from the pool")
	}
	// The allocator seeds a heading; zero it so the composition is the
	// authored mount and the sense under test is the only thing moving the
	// point.
	u.Move.Heading, u.Move.Pitch, u.Move.Bank = 0, 0, 0
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Children: []int{1}},
			{Name: "emitter", Parent: 0, Translate: [3]numeric.Fixed{
				numeric.FixedFromInt(-authoredX),
				numeric.FixedFromInt(authoredY),
				numeric.FixedFromInt(-authoredZ),
			}},
		},
	}
	vm := &cob.VM{Pieces: make([]model.PieceState, 2)}
	// COB index 0 is the emitter, which is model index 1: the two orders
	// differ so the test cannot pass by reading the wrong piece.
	u.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{Model: mdl, VM: vm, PieceMap: []int{1, 0}}}

	sink := &cobPresentationSink{session: s, source: h}
	got, ok := sink.pieceWorldPos(0)
	if !ok {
		t.Fatal("piece world point declined for a mapped piece")
	}
	want := [3]numeric.Fixed{
		u.X.Add(numeric.FixedFromInt(-authoredX)),
		u.Y.Add(numeric.FixedFromInt(authoredY)),
		u.Z.Add(numeric.FixedFromInt(authoredZ)),
	}
	if got != want {
		t.Fatalf("piece world point = %v, want %v: unit + (−ax, ay, +az) for an authored (%d,%d,%d) at heading 0 [03 R-RAST-01 §8]",
			got, want, authoredX, authoredY, authoredZ)
	}
	// A second negation here would cancel the locator's and put the emitter
	// the same distance behind the unit. Because the screen ordinate is
	// `Z − Y/2`, that is twice the piece's depth offset in whole pixels
	// [03 §2.5].
	if mirrored := u.Z.Sub(numeric.FixedFromInt(authoredZ)); got[2] == mirrored {
		t.Fatal("piece world Z was mirrored a second time: the locator already returns (x, y, −z) [03 R-RAST-01 §8]")
	}
}
