package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Admission must see the live script pose even if nothing has ever drawn the
// unit. Selection/coloured/non-quad faces consume no fragment RNG, and the
// committed copy survives later pose and pool changes [04 R-COB-04 §3][I6].
func TestShatterSamplesSimulationPoseAndPublishesDetachedGeometry(t *testing.T) {
	s, u := bitmapExplosionFixture(t, bitmapOnlyFlag, numeric.FixedFromInt(30))
	binding := u.COBBinding()
	u.Move.Heading, u.Move.Pitch, u.Move.Bank = 0, 0, 0
	piece := &binding.Model.Pieces[1]
	piece.Translate = [3]numeric.Fixed{}
	binding.Model.Pieces[binding.Model.Root].Translate = [3]numeric.Fixed{}
	piece.Vertices = [][3]numeric.Fixed{{0, 0, 0}, {4 << 16, 0, 0}, {4 << 16, 4 << 16, 0}, {0, 4 << 16, 0}}
	piece.Selection = true
	piece.Primitives = []model.Primitive{
		{VertexIndices: []uint16{0, 1, 2, 3}},
		{IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3}},
		{VertexIndices: []uint16{0, 1, 2}},
		{VertexIndices: []uint16{0, 1, 2, 3}},
	}
	binding.VM.Pieces[1].Trans = [3]numeric.Fixed{7 << 16, 3 << 16, 2 << 16}
	s.SetFragmentMaterialResolver(func(def uint16, piece, primitive int, colour uint8) render.FrozenFragmentMaterial {
		return render.FrozenFragmentMaterial{UnitDefID: def, PieceIndex: piece, PrimitiveIndex: primitive, FrameIndex: 2, Valid: true}
	})
	sink := &cobExplosionSink{presentation: &cobPresentationSink{session: s, publication: s.publication, source: u.Handle}}
	request := cob.ShatterExplosion{PhysicalExplosion: cob.PhysicalExplosion{Source: cob.ExplosionSource{Identity: cob.ExplosionPieceIdentity{COBPiece: 1}}}}
	before := s.SimRNG().Draws()
	if !sink.AdmitShatter(request) {
		t.Fatal("shatter refused an eligible quad")
	}
	if got := s.SimRNG().Draws() - before; got != 8 {
		t.Fatalf("fragment draws=%d, want 8", got)
	}
	views := s.publishFragments(nil)
	if len(views) != 1 || views[0].Position != [3]numeric.Fixed{u.X + 7<<16, u.Y + 3<<16, u.Z - 2<<16} || views[0].PrimitiveIndex != 3 || views[0].FrameIndex != 2 {
		t.Fatalf("published fragment=%+v", views)
	}
	saved := views[0]
	binding.VM.Pieces[1].RotY = 16384
	if !sink.AdmitShatter(request) {
		t.Fatal("rotated shatter refused")
	}
	next := s.publishFragments(nil)
	if len(next) != 2 || next[1].Vertices == saved.Vertices {
		t.Fatal("new fragment ignored current rotation")
	}
	s.stepEffectPhase(18)
	if views[0] != saved {
		t.Fatal("published geometry aliases mutable pose or pool")
	}
	s.Snapshot = frame.NewBuffer()
	s.publishSnapshot(18)
	if len(s.Snapshot.Current().Fragments) != 2 {
		t.Fatal("production publication omitted fragments")
	}
}
