package render

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// EffectDraw is the immutable presentation instruction for one admitted
// effect.  It intentionally retains the authored Kind/Graphic and frame
// selectors; asset lookup belongs to the client presentation boundary.
type EffectDraw struct {
	ID                        uint32
	EventSeq                  uint64
	Kind                      string
	Graphic                   string
	FrameA                    int32
	FrameB                    int32
	X, Y, Z                   numeric.Fixed
	Light                     bool
	PaletteRow                int16
	Shake                     int32
	AssetID                   string
	SequenceID                string
	Strip                     int8
	FlashRadius               int32
	FlashLevel                int32
	HasFlashDisc              bool
	TargetX, TargetY, TargetZ numeric.Fixed
	NanolatheIndex            int32
	NanolatheCount            int32
	NanolatheGeometryKnown    bool
}

// BuildEffectDraws copies effect metadata in admission order.  The source is
// already a bounded immutable snapshot; no effect is synthesized when the
// producer emitted none [F-P0-034][I6].
func BuildEffectDraws(effects []frame.EffectView) []EffectDraw {
	if len(effects) == 0 {
		return nil
	}
	out := make([]EffectDraw, 0, len(effects))
	for _, e := range effects {
		out = append(out, EffectDraw{
			ID:           e.ID,
			EventSeq:     e.EventSeq,
			Kind:         e.Kind,
			Graphic:      e.Graphic,
			FrameA:       e.SeqA,
			FrameB:       e.SeqB,
			X:            e.X,
			Y:            e.Y,
			Z:            e.Z,
			Light:        e.Light,
			PaletteRow:   e.PaletteRow,
			Shake:        e.Shake,
			AssetID:      e.AssetID,
			SequenceID:   e.SequenceID,
			Strip:        e.Strip,
			FlashRadius:  e.FlashRadius,
			FlashLevel:   e.FlashLevel,
			HasFlashDisc: e.HasFlashDisc,
			TargetX:      e.TargetX, TargetY: e.TargetY, TargetZ: e.TargetZ,
			NanolatheIndex:         e.NanolatheIndex,
			NanolatheCount:         e.NanolatheCount,
			NanolatheGeometryKnown: e.NanolatheGeometryKnown,
		})
	}
	return out
}
