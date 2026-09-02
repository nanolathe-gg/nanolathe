package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestReachTestDoesNotConsumeNanoQuery locks [05 R-P0-06 §4] / [05 R-P0-06 §6]:
// QueryNanoPiece runs once per accepted work step and never speculatively.
// Stock two-emitter scripts alternate their spray piece per call, so a reach
// test that quietly spends a call pins the emitter to one piece — which is
// what the session's per-tick NeedsWalk poll did to the construction kbot.
// The work rows' reach tests measure from the acting unit and resolve no piece
// [04 R-ORD-01 §5].
func TestReachTestDoesNotConsumeNanoQuery(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fs.Close()

	const unitName = "armack"
	mdl, err := model.Load(fs, "objects3d/"+unitName+".3do")
	if err != nil {
		t.Skipf("%s model unavailable: %v", unitName, err)
	}
	names := make([]string, len(mdl.Pieces))
	for i, p := range mdl.Pieces {
		names[i] = p.Name
	}
	binding, err := cob.BindStrict(fs, cob.BindingRequest{UnitName: unitName, Model: mdl, ModelPieces: names})
	if err != nil {
		t.Skipf("%s script unavailable: %v", unitName, err)
	}

	u := &units.Unit{
		X:   numeric.Fixed(320 * 65536),
		Z:   numeric.Fixed(240 * 65536),
		Def: &content.UnitDef{BuildDistance: 128},
	}
	u.ScriptState = &units.ScriptState{VM: binding.VM, Binding: binding}
	// A bound walk driver is what previously enabled the speculative query.
	svc := &Service{Movement: &movement.System{}}

	first, _, ok := svc.QueryNanoPiece(u)
	if !ok {
		t.Fatalf("first nano query refused")
	}
	// A reach test between two emissions must leave the script's rotation
	// exactly where the first emission left it.
	svc.IsWithinNanoRangePublic(u, u.X, u.Z, 1, 1)
	second, _, ok := svc.QueryNanoPiece(u)
	if !ok {
		t.Fatalf("second nano query refused")
	}
	if first == second {
		t.Fatalf("nano piece did not rotate across a reach test: both queries returned %d; a speculative QueryNanoPiece is being spent [05 R-P0-06 §4]", first)
	}
	third, _, _ := svc.QueryNanoPiece(u)
	if third != first {
		t.Fatalf("nano rotation is not the script's two-piece cycle: %d %d %d", first, second, third)
	}
}
