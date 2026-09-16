package render

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// This file holds the strip-object arithmetic both the authoritative strip
// table and the presentation-side debris trail containers evaluate. There is
// one implementation of each expression: the simulation's phase-11 strip sweep
// calls these with values drawn from the simulation's CRT stream, and the
// client's debris trail store calls them with values drawn from its own
// presentation CRT. Each function takes the already-drawn value rather than a
// stream, so the caller keeps ownership of draw order and draw count [I4].

// SmokeLastFrame folds one CRT value into a smoke puff's own final animation
// frame: `crtRand·(frameCount − 3)/0x8000 + 2`, expressed against the
// container's stored `frameCount − 1` [03 R-FX-01 §3][06 R-WFX-01 §5]. A
// container with no bound frame count yields 0, which means "not finished yet"
// and is finished later from the same retained draw.
func SmokeLastFrame(frameCountBase int32, draw int32) int32 {
	if frameCountBase <= 2 {
		return 0
	}
	return int32(int64(draw)*int64(frameCountBase-2)/0x8000) + 2
}

// SmokeFrameHold folds one CRT value into the next animation hold: half to full
// of the authored delay [03 R-STRIP-01 §3].
func SmokeFrameHold(authored int32, draw int32) int32 {
	half := authored / 2 // signed truncating [I3]
	return half + int32(int64(draw)*int64(half)/0x8000)
}

// FlameTrailStep is the flame-stream trail's per-axis step,
// `((B − A) · trunc(65536 / lifetime)) >> 16` [03 R-FX-01 §3]. The truncated
// reciprocal is what makes it fall slightly short of the exact quotient.
func FlameTrailStep(delta numeric.Fixed, lifetime int32) numeric.Fixed {
	if lifetime <= 0 {
		return 0
	}
	recip := int64(65536) / int64(lifetime) // truncating [I3]
	return numeric.Fixed((delta.Raw() * recip) >> 16)
}
