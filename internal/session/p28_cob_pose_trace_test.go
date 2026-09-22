package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestP28COB01RScenarioARMCKPublishesStrictCreateState follows mission
// reconstruction through the production unit binder into the immutable frame.
// It locks the publication route and copy boundary without claiming that the
// resulting values are the still-untraced retail first visual pose
// [R-P28-COB-01R][01 §4.4][03 §2.4].
func TestP28COB01RScenarioARMCKPublishesStrictCreateState(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatalf("new production unit world: %v", err)
	}
	s := &Session{
		Catalog: cat,
		Units:   w,
		Econ:    &economy.Service{},
		Snapshot: frame.NewBuffer(frame.Capacities{
			Units: 2,
		}),
		publication: newPublicationState(frame.NewEventBuffer(frame.Limits{})),
	}
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: 1}
	s.rngSim = rng.NewSimulation(1)
	s.rngCrt = rng.NewCRT(1)
	s.rngInitialized = true
	w.SetSimulationRNG(&s.rngSim)
	w.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(fs, u) })
	m := &mission.Mission{Units: []mission.UnitPlacement{{UnitName: "ARMCK", Player: 1, X: 16 << 16, Z: 16 << 16}}}
	if err := reconstructUnits(s, m); err != nil {
		t.Fatalf("scenario reconstruction: %v", err)
	}
	var u *units.Unit
	for _, candidate := range w.IterSliced() {
		if candidate != nil && candidate.Def != nil && candidate.Def.CanonicalKey == content.CanonicalKey("armck") {
			u = candidate
			break
		}
	}
	if u == nil || u.COBBinding() == nil || u.GetScript() == nil {
		t.Fatal("scenario ARMCK lacks strict production binding")
	}
	binding := u.COBBinding()
	if !binding.CreateInvoked || !binding.Callbacks.CreateInvoked() || binding.VM.DrainCalls != 1 {
		t.Fatalf("scenario Create invoked=%t bridge=%t drains=%d", binding.CreateInvoked, binding.Callbacks.CreateInvoked(), binding.VM.DrainCalls)
	}
	if u.InBuildStance || u.Busy || u.BuildingState {
		t.Fatalf("scenario ARMCK has construction callback state before publication: stance=%t busy=%t start=%t", u.InBuildStance, u.Busy, u.BuildingState)
	}
	s.publishSnapshot(1)
	committed := s.Snapshot.Current()
	if committed == nil || len(committed.Units) != 1 || committed.Units[0].Slot != u.Handle {
		t.Fatalf("committed scenario units: %#v", committed)
	}
	view := committed.Units[0]
	if len(view.Pieces) != len(binding.VM.Pieces) {
		t.Fatalf("committed pieces=%d VM pieces=%d", len(view.Pieces), len(binding.VM.Pieces))
	}
	for i, piece := range view.Pieces {
		state := binding.VM.Pieces[i]
		if piece.Name != binding.Program.Pieces[i] || piece.RotX != state.RotX || piece.RotY != state.RotY || piece.RotZ != state.RotZ || piece.Tx != state.Trans[0] || piece.Ty != state.Trans[1] || piece.Tz != state.Trans[2] {
			t.Fatalf("committed piece %d does not copy strict VM state: view=%#v VM=%#v", i, piece, state)
		}
	}
	committedPieces := append([]frame.PieceView(nil), view.Pieces...)
	binding.VM.Pieces[0].RotY++
	u.RenderPieceFlags[binding.PieceMap[0]] ^= 0x01
	if !reflect.DeepEqual(committed.Units[0].Pieces, committedPieces) {
		t.Fatal("committed ARMCK pose changed after live simulation mutation")
	}
}
