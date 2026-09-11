package units

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestP28COB01RARMCKStrictBindingTrace records the stock ARMCK asset identity
// and the state produced by the production strict binder's D+wake Create
// barrier. It deliberately asserts relationships owned by the binding route,
// not an untraced retail first-frame pose [R-P28-COB-01R].
func TestP28COB01RARMCKStrictBindingTrace(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	def, ok := cat.Unit("armck")
	if !ok || def == nil {
		t.Fatal("retail ARMCK definition missing")
	}
	modelPath := "objects3d/" + strings.ToLower(strings.TrimSpace(def.ObjectName)) + ".3do"
	mdl, err := model.Load(fs, modelPath)
	if err != nil {
		t.Fatalf("load ARMCK model %q: %v", def.ObjectName, err)
	}
	u := &Unit{Def: def}
	sim := rng.NewSimulation(1)
	var lifecycle []string
	binding, err := BindCOBWithPortsAndVisibilityAndContextForUnit(fs, u, mdl, &sim, nil, nil, func(binding *cob.Binding) error {
		// Observe completion before Create starts. A late sink can lose its
		// return receipt when stock child work reuses the slot in one drain
		// [R-P28-COB-01R]; that is not abnormal script termination.
		binding.Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) { lifecycle = append(lifecycle, e.Name+":"+e.Phase) })
		return nil
	})
	if err != nil {
		t.Fatalf("strict bind ARMCK: %v", err)
	}
	if u.COBBinding() != binding || u.GetScript() != binding.VM {
		t.Fatal("strict ARMCK bind did not retain the pre-Create unit context")
	}
	if binding == nil || binding.Program == nil || binding.VM == nil || binding.Callbacks == nil {
		t.Fatalf("incomplete ARMCK binding: %#v", binding)
	}
	if !binding.CreateInvoked || !binding.Callbacks.CreateInvoked() || binding.VM.DrainCalls != 1 {
		t.Fatalf("Create route: invoked=%t bridge=%t drains=%d, want once through one D+wake barrier", binding.CreateInvoked, binding.Callbacks.CreateInvoked(), binding.VM.DrainCalls)
	}
	if len(binding.Program.Pieces) != len(binding.PieceMap) || len(binding.VM.Pieces) != len(binding.Program.Pieces) {
		t.Fatalf("piece tables disagree: COB=%d map=%d VM=%d", len(binding.Program.Pieces), len(binding.PieceMap), len(binding.VM.Pieces))
	}
	for i, modelIndex := range binding.PieceMap {
		if modelIndex < 0 || modelIndex >= len(mdl.Pieces) {
			t.Fatalf("COB piece %d %q unresolved: model index %d", i, binding.Program.Pieces[i], modelIndex)
		}
		if binding.Program.Pieces[i] != mdl.Pieces[modelIndex].Name {
			t.Fatalf("piece %d strict map %q -> %q is not exact", i, binding.Program.Pieces[i], mdl.Pieces[modelIndex].Name)
		}
	}
	wantPieces := []string{"nanospray", "turret", "rfoot", "lfoot", "pelvis", "lflap", "rflap", "guncover", "nozzle", "arms", "nanobody2", "ground"}
	wantModel := []int{10, 4, 2, 3, 1, 5, 6, 11, 9, 7, 8, 0}
	if len(binding.Program.Pieces) != len(wantPieces) {
		t.Fatalf("ARMCK COB pieces=%d want=%d", len(binding.Program.Pieces), len(wantPieces))
	}
	for i := range wantPieces {
		if binding.Program.Pieces[i] != wantPieces[i] || binding.PieceMap[i] != wantModel[i] {
			t.Fatalf("ARMCK piece %d = %q -> %d, want %q -> %d", i, binding.Program.Pieces[i], binding.PieceMap[i], wantPieces[i], wantModel[i])
		}
		piece := binding.VM.Pieces[i]
		if piece.RotX != 0 || piece.RotY != 0 || piece.RotZ != 0 || piece.Trans[0].Raw() != 0 || piece.Trans[1].Raw() != 0 || piece.Trans[2].Raw() != 0 || piece.Hidden || piece.DontShade || piece.DontShadow {
			t.Fatalf("ARMCK post-delta-zero diagnostic piece %q = %#v, want zero/default state", wantPieces[i], piece)
		}
		if u.RenderPieceFlags[wantModel[i]]&0x01 == 0 {
			t.Fatalf("ARMCK post-delta-zero piece %q unexpectedly hidden", wantPieces[i])
		}
	}
	if u.InBuildStance || u.Busy {
		t.Fatalf("ARMCK post-delta-zero ports: stance=%t busy=%t", u.InBuildStance, u.Busy)
	}
	// TODO(question): the exact retail first committed pose remains Unknown;
	// the paired scenario/factory publication probe in [R-P28-COB-01R] would
	// settle it. This diagnostic proves only that the current delta-zero
	// route leaves Create work whose
	// finish is observed by the next ordinary drain; an implementation must not
	// force a piece pose to conceal that boundary [R-P28-COB-01R].
	createIdentity := binding.VM.ThreadIdentity(0)
	binding.Callbacks.Drain(1)
	if binding.VM.ThreadIdentity(0) == createIdentity {
		t.Fatal("stock child work did not reuse the completed Create slot")
	}
	binding.Callbacks.StartBuildingHeading(1234)
	binding.Callbacks.Drain(1)
	binding.Callbacks.StopBuilding()
	binding.Callbacks.Drain(1)
	// The early observer also sees the optional reload callback, absent from
	// this stock script, that creation attempts after binding [04 R-CB-01 §4].
	wantLifecycle := []string{"Create:start", "SetMaxReloadTime:start-failed", "Create:finish", "StartBuilding:start", "StartBuilding:finish", "StopBuilding:start", "StopBuilding:finish"}
	if got := strings.Join(lifecycle, " "); got != strings.Join(wantLifecycle, " ") {
		t.Fatalf("ARMCK lifecycle=%v want=%v", lifecycle, wantLifecycle)
	}
}
