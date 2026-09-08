package render

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// EffectDraw is the immutable presentation instruction for one admitted
// effect.  It intentionally retains the authored Kind/Graphic and frame
// selectors; asset lookup belongs to the client presentation boundary.
type EffectDraw struct {
	ID       uint32
	EventSeq uint64
	Kind     string
	Graphic  string
	FrameA   int32
	FrameB   int32
	// ActiveA and ActiveB are the published liveness of the two embedded
	// animation players [03 §1]: FrameA is drawn only while ActiveA holds and
	// FrameB only while ActiveB holds. The record outlives whichever player
	// finishes first, and a terminated player's index-zero residue is not a
	// frame to draw [03 §4.4].
	ActiveA      bool
	ActiveB      bool
	X, Y, Z      numeric.Fixed
	Light        bool
	PaletteRow   int16
	Shake        int32
	AssetID      string
	SequenceID   string
	Strip        int8
	FlashRadius  int32
	FlashLevel   int32
	HasFlashDisc bool
	// HasCalculatedFlash and CalculatedTable carry the secondary cursor's
	// generated table to the draw pass [06 R-WFX-01 §2]; FrameB is its cursor.
	HasCalculatedFlash bool
	CalculatedTable    uint8
	// StripFill is the two-by-two fill colour of a mirrored strip sub-record
	// [03 R-STRIP-01 §2]; zero means the view is not a fill.
	StripFill                 uint8
	TargetX, TargetY, TargetZ numeric.Fixed
	NanolatheIndex            int32
	NanolatheCount            int32
	NanolatheGeometryKnown    bool
	NanolatheTargetBoxKnown   bool
	NanolatheTargetMin        [3]numeric.Fixed
	NanolatheTargetMax        [3]numeric.Fixed
}

// BuildEffectDraws copies effect metadata in admission order.  The source is
// already a bounded immutable snapshot; no effect is synthesized when the
// producer emitted none [F-P0-034][I6].
func BuildEffectDraws(effects []frame.EffectView) []EffectDraw {
	if len(effects) == 0 {
		return nil
	}
	return BuildEffectDrawsInto(make([]EffectDraw, 0, len(effects)), effects)
}

// BuildEffectDrawsInto is BuildEffectDraws over a caller-owned buffer. The
// records are values with no pointer fields beyond their interned strings, so
// the recorder can keep one buffer for the life of the client and refill it
// every frame instead of allocating one draw list per effect pass; the returned
// slice is the same sequence BuildEffectDraws produces. The caller must not
// retain the result past its next call [I6].
func BuildEffectDrawsInto(dst []EffectDraw, effects []frame.EffectView) []EffectDraw {
	out := dst[:0]
	for _, e := range effects {
		out = append(out, EffectDraw{
			ID:                 e.ID,
			EventSeq:           e.EventSeq,
			Kind:               e.Kind,
			Graphic:            e.Graphic,
			FrameA:             e.SeqA,
			FrameB:             e.SeqB,
			ActiveA:            e.ActiveA,
			ActiveB:            e.ActiveB,
			X:                  e.X,
			Y:                  e.Y,
			Z:                  e.Z,
			Light:              e.Light,
			PaletteRow:         e.PaletteRow,
			Shake:              e.Shake,
			AssetID:            e.AssetID,
			SequenceID:         e.SequenceID,
			Strip:              e.Strip,
			FlashRadius:        e.FlashRadius,
			FlashLevel:         e.FlashLevel,
			HasFlashDisc:       e.HasFlashDisc,
			HasCalculatedFlash: e.HasCalculatedFlash, CalculatedTable: e.CalculatedTable,
			StripFill: e.StripFill,
			TargetX:   e.TargetX, TargetY: e.TargetY, TargetZ: e.TargetZ,
			NanolatheIndex:         e.NanolatheIndex,
			NanolatheCount:         e.NanolatheCount,
			NanolatheGeometryKnown: e.NanolatheGeometryKnown,
		})
	}
	return out
}
