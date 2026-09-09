package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// cobExplosionSink resolves explode's bitmap requests while the COB callback
// still owns its source unit. It admits them directly to the shared effect
// pool; a deferred queue could turn a full-pool refusal into a later admission
// and would change the callback's observable order [04 R-COB-04 §1, §4][I5].
type cobExplosionSink struct{ presentation *cobPresentationSink }

// AdmitWholePiece copies the selected raw model piece into the bounded debris
// arena after the VM has made its six physical draws and hidden its source.
// The detached record retains the source slot only; publication later observes
// that slot's current owner through RawUnitRecord [04 R-COB-04 §1, §2].
func (s *cobExplosionSink) AdmitWholePiece(request cob.WholePieceExplosion) bool {
	if s == nil || s.presentation == nil || s.presentation.session == nil || s.presentation.session.Units == nil {
		return false
	}
	presentation := s.presentation
	u := presentation.session.Units.Unit(presentation.source)
	if u == nil || u.COBBinding() == nil || u.COBBinding().Model == nil {
		return false
	}
	binding := u.COBBinding()
	cobPiece := request.Source.Identity.COBPiece
	if cobPiece < 0 || cobPiece >= len(binding.PieceMap) {
		return false
	}
	piece := binding.PieceMap[cobPiece]
	if piece < 0 || piece >= len(binding.Model.Pieces) {
		return false
	}
	// TODO(question): retain the source render-piece translation at admission.
	// This adapter currently recomposes the COB locator; retail copies the last
	// materialized translation without forcing a rebuild [04 R-COB-04 §2, §3].
	position, ok := presentation.pieceWorldPos(cobPiece)
	if !ok {
		return false
	}
	defName := ""
	if u.Def != nil {
		defName = u.Def.CanonicalKey
	}
	presentation.session.ensureDebris()
	return presentation.session.debris.Admit(render.DebrisRequest{
		Source:       presentation.source,
		DefID:        presentation.session.Units.DefIDForHandle(presentation.source),
		DefName:      defName,
		Model:        binding.Model,
		PieceIndex:   piece,
		GeometryName: request.Source.Identity.GeometryName,
		Points:       binding.Model.Pieces[piece].Vertices,
		Position:     position,
		Angles: [3]uint16{
			request.Source.State.Transform.RotX,
			request.Source.State.Transform.RotY,
			request.Source.State.Transform.RotZ,
		},
		RenderFlags: request.Source.State.RenderFlags,
		Velocity: [3]numeric.Fixed{
			request.Kinematics.Velocity[0].Add(u.Move.VelX),
			request.Kinematics.Velocity[1].Add(u.Move.VelY),
			request.Kinematics.Velocity[2].Add(u.Move.VelZ),
		},
		AngularRates: request.Kinematics.AngularRates,
		Lifetime:     request.Kinematics.Lifetime,
		Fall:         request.Flags&cob.ExplosionFall != 0,
		ExplodeOnHit: request.Flags&cob.ExplosionWholePieceOnHit != 0,
		Smoke:        request.Flags&cob.ExplosionSmoke != 0,
		Fire:         request.Flags&cob.ExplosionFire != 0,
	})
}

// AdmitBitmap resolves a COB piece through the same helper as PIECE_XZ and
// PIECE_Y, then immediately admits the named art and calculated table 2. The
// named player's exact source is the existing effect timing resolver; the
// calculated player's established two-tick table timing keeps this record
// drawable even when that presentation resolver is absent [04 R-COB-04 §4].
func (s *cobExplosionSink) AdmitBitmap(request cob.BitmapExplosion) bool {
	if s == nil || s.presentation == nil || s.presentation.session == nil ||
		s.presentation.publication == nil || s.presentation.publication.effects == nil {
		return false
	}
	graphic, ok := bitmapExplosionGraphic(request.Kind)
	if !ok {
		return false
	}
	pos, ok := s.presentation.pieceWorldPos(request.Source.Identity.COBPiece)
	if !ok {
		return false
	}
	tick := uint32(0)
	if s.presentation.clock != nil {
		tick = s.presentation.clock.GlobalTick
	}
	piece := int32(request.Source.Identity.COBPiece)
	if cobPiece := request.Source.Identity.COBPiece; cobPiece >= 0 && cobPiece < len(s.presentation.pieceMap) {
		piece = int32(s.presentation.pieceMap[cobPiece])
	}
	event := frame.Event{
		Kind:               frame.KindExplosion,
		Tick:               tick,
		Source:             s.presentation.source,
		Piece:              piece,
		Graphic:            graphic,
		AssetID:            "fx",
		X:                  pos[0],
		Y:                  pos[1],
		Z:                  pos[2],
		HasCalculatedFlash: true,
		CalculatedTable:    2,
		DurationsB:         render.FlashFrameDurations(2),
	}
	if !s.presentation.publication.effects.Admit(tick, event) {
		return false
	}
	if s.aboveSea(pos[1]) {
		// Class 7, parameter 15 is the already-researched strip-9 land-dust
		// puffer: three smoke-1 puffs over its 15-tick window [03 R-FX-01 §3].
		s.presentation.session.appendStripSmokePuffer(9, pos, SmokePuffLandDust)
	}
	return true
}

func (s *cobExplosionSink) aboveSea(y numeric.Fixed) bool {
	if s == nil || s.presentation == nil || s.presentation.session == nil || s.presentation.session.World == nil {
		return false
	}
	// The allocator reads the signed high word of the position and compares it
	// strictly with the zero-extended sea-level byte. A fractional part above
	// the plane therefore does not reach the land-dust arm [04 R-COB-04 §4].
	wholeY := int32(y.Raw()) >> numeric.FractionBits
	return wholeY > int32(s.presentation.session.World.SeaLevel)
}

func bitmapExplosionGraphic(kind cob.BitmapExplosionKind) (string, bool) {
	switch kind {
	case cob.BitmapExplosionPrimary:
		return "explosion", true
	case cob.BitmapExplode2:
		return "explode2", true
	case cob.BitmapExplode3:
		return "explode3", true
	case cob.BitmapExplode4:
		return "explode4", true
	case cob.BitmapExplode5:
		return "explode5", true
	case cob.BitmapNuke1:
		return "nuke1", true
	default:
		return "", false
	}
}
