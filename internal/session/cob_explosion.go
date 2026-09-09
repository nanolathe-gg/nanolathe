package session

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// cobExplosionSink resolves explode's bitmap requests while the COB callback
// still owns its source unit. It admits them directly to the shared effect
// pool; a deferred queue could turn a full-pool refusal into a later admission
// and would change the callback's observable order [04 R-COB-04 §1, §4][I5].
type cobExplosionSink struct{ presentation *cobPresentationSink }

// AdmitWholePiece leaves physical debris unbound. The VM has already made its
// six physical draws and hidden the source before it reaches this refusal.
// TODO(U13): bind the researched whole-piece arena and phase-4 lifecycle
// without creating a second effect lifetime [04 R-COB-04 §1, §2].
func (s *cobExplosionSink) AdmitWholePiece(cob.WholePieceExplosion) bool { return false }

// AdmitShatter leaves physical shatter unbound. The VM has already made its
// six physical draws and hidden the source before it reaches this refusal.
// TODO(U13): bind the fragment geometry arena and its pool-before-draw walk
// without creating a second effect lifetime [04 R-COB-04 §1, §3].
func (s *cobExplosionSink) AdmitShatter(cob.ShatterExplosion) bool { return false }

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
