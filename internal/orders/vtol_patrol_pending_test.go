package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestVTOLPatrolPhaseOneClearsOnlyThreePendingBits locks the width of
// `VTOL_Patrol` phase 1's pending clear [04 R-ORD-02 §2]. The row clears the
// three movement-outcome bits 0x20, 0x40 and 0x80 and writes the word back a
// byte at a time, so the payload-release and rebind bits 0x100 and 0x200 — and
// everything above them — survive. The payload installer's own clear is the
// wider five-bit one [04 R-AIR-01 §4]; the two masks part company on the path
// where phase 2's marker allocation fails and nothing else clears them.
func TestVTOLPatrolPhaseOneClearsOnlyThreePendingBits(t *testing.T) {
	u := &units.Unit{
		Handle: 1,
		Def:    &content.UnitDef{UnitDefID: 9, MaxDamage: 100, CanFly: true, CanMove: true, BMCode: 1},
		Alive:  true,
	}
	u.Health = u.Def.MaxDamage
	u.Move.Mode, u.Move.ModeMirror = 2, 2

	n := &Node{Owner: u.Handle, Phase: 1, Satisfied: ^uint32(0)}

	if got := vtolPatrolHandler(u, n, 0, 10); got != 1 {
		t.Fatalf("phase 1 returned %d, want 1 (advance) [04 R-ORD-02 §2]", got)
	}
	if want := ^uint32(0) &^ uint32(0xE0); n.Satisfied != want {
		t.Fatalf("pending word = %#x, want %#x: only 0x20, 0x40 and 0x80 are cleared, "+
			"0x100 and 0x200 survive the byte-wide write [04 R-ORD-02 §2]", n.Satisfied, want)
	}
}
