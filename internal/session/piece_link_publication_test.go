package session

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// pieceLinkFixture is testdata/piece_link, authored for this test. The model
// is base → child turret → sibling barrel, depth-first [base, turret, barrel].
// The script declares [base, barrel, gun, extra], and its Create moves
// gun +10 on X, extra +7 on Y and barrel +3 on Z with move-now.
//
// The link pass [04 R-COB-01 §4] gives [0, 2, 1, -1]: barrel swaps into slot
// one, gun has no match from slot two onward and takes turret — the unclaimed
// model piece its slot now holds — and extra is beyond the model's three
// pieces.
const pieceLinkFixture = "testdata/piece_link"

// TestPieceLinkFixtureBytes pins the committed fixture to the authored layout
// above, so the binary files cannot drift from their description.
func TestPieceLinkFixtureBytes(t *testing.T) {
	const f = 65536
	code := []uint32{
		0x10021001, 10 * f, 0x1000b000, 2, 0, // gun X = 10
		0x10021001, 7 * f, 0x1000b000, 3, 1, // extra Y = 7
		0x10021001, 3 * f, 0x1000b000, 1, 2, // barrel Z = 3
		0x10065000,
	}
	want := fixtureCOBBytes(code, []string{"Create"}, []uint32{0}, []string{"base", "barrel", "gun", "extra"})
	got, err := os.ReadFile(pieceLinkFixture + "/scripts/pieceslot.cob")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("pieceslot.cob differs from its authored layout (err %v)", err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(pieceLinkFixture, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	mdl, _, err := loadAuthoredModel(fs, "pieceslot")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range mdl.Pieces {
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"base", "turret", "barrel"}) {
		t.Fatalf("pieceslot.3do depth-first pieces = %q, want [base turret barrel]", names)
	}
}

// TestPublishedPieceLanesCarryTheScriptLink locks the publication half of
// presentation's piece pairing: each committed lane carries the model piece
// the binding linked its script piece to, so an alias lane poses the slot's
// model piece and a lane beyond the model poses none [04 R-COB-01 §4] [03 §2.4].
// A lane never carries a name, which is what used to drop the alias on screen.
func TestPublishedPieceLanesCarryTheScriptLink(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountDirectory(pieceLinkFixture, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "pieceslot"}, UnitName: "pieceslot", ObjectName: "pieceslot", MaxDamage: 10, Limit: -1}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	s := &Session{rngSim: rng.NewSimulation(77), rngCrt: rng.NewCRT(9), rngInitialized: true, publication: newPublicationState(frame.NewEventBuffer(frame.Limits{}), 0)}
	w := units.NewSliced(2, cat)
	w.SetCOBSource(fs, globalCobLoader)
	w.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(fs, u) })
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := w.Unit(h).COBBinding().PieceMap; !slices.Equal(got, []int{0, 2, 1, -1}) {
		t.Fatalf("fixture link = %v, want [0 2 1 -1]", got)
	}
	s.Snapshot, s.Units = frame.NewBuffer(), w
	s.publishSnapshot(1)
	cur := s.Snapshot.Current()
	if cur == nil || len(cur.Units) != 1 {
		t.Fatalf("published frame = %#v, want one unit", cur)
	}
	lanes := cur.Units[0].Pieces
	type lane struct {
		index      int
		name       string
		tx, ty, tz numeric.Fixed
	}
	var got []lane
	for _, p := range lanes {
		got = append(got, lane{p.Index, p.Name, p.Tx, p.Ty, p.Tz})
	}
	ten, seven, three := numeric.FixedFromInt(10), numeric.FixedFromInt(7), numeric.FixedFromInt(3)
	want := []lane{
		{index: 0},             // base
		{index: 2, tz: three},  // barrel
		{index: 1, tx: ten},    // gun, the alias: turret's slot
		{index: -1, ty: seven}, // extra, beyond the model
	}
	if !slices.Equal(got, want) {
		t.Fatalf("published lanes = %+v, want %+v", got, want)
	}
}
