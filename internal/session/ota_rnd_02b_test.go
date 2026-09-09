package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestOTARND02BPublicationPreservesPieceShadePolarity exercises the real
// fixture unit creation and publication path. COB DONT_SHADE clears bit 2 in
// the unit-owned record; publication must expose that as effective
// DontShade=true, and restoring the bit must publish false [04 §4.3]
// [03 §2.4.1] [R-RND-02A].
func TestOTARND02BPublicationPreservesPieceShadePolarity(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "shade-publication"},
		MaxDamage:        1,
		Script: &cob.Program{
			Code:        []uint32{0x1000e000, 0, 0x10065000}, // DONT_SHADE, base, return
			Scripts:     map[string]int{"Create": 0},
			Pieces:      []string{"base"},
			ScriptsByID: []int{0},
		},
	}
	w := units.NewSliced(2, nil)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create fixture unit: %v", err)
	}
	u := w.Unit(h)
	if u == nil || u.GetScript() == nil {
		t.Fatal("fixture unit did not retain its COB VM")
	}
	if len(u.RenderPieceFlags) != 1 || u.RenderPieceFlags[0]&0x04 != 0 {
		t.Fatalf("Create DONT_SHADE flags = %#v, want bit 2 clear", u.RenderPieceFlags)
	}

	s := &Session{Snapshot: frame.NewBuffer(), Units: w, LocalOwner: 0}
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 || len(cur.Units[0].Pieces) != 1 {
		t.Fatalf("published unit/pieces = %#v, want one unit with one piece", cur)
	}
	if !cur.Units[0].Pieces[0].DontShade {
		t.Fatalf("published DontShade = false, want true for cleared bit 2")
	}

	if !u.SetRenderPieceFlag(0, 0x04, true) {
		t.Fatal("restoring shade bit failed")
	}
	s.publishSnapshot(2)
	next := s.Snapshot.Current()
	if next == nil || len(next.Units) != 1 || len(next.Units[0].Pieces) != 1 {
		t.Fatalf("second published unit/pieces = %#v", next)
	}
	if next.Units[0].Pieces[0].DontShade {
		t.Fatal("published DontShade = true after shade bit was restored")
	}
}
