package render

// Nanoframe reveal [03 §5.2]. An unfinished unit is composed exactly like a
// finished one and then recoloured band by band against the model's own
// per-pixel height key, with the model's polygon outlines overdrawn in a
// pulsing green. The remaining fraction (1 at request, 0 at completion) drives
// a sweep line that runs the height key from one end of the model to the other
// five separate times; which colour lands above, on, and below that line is
// what turns the reveal from an empty outline into a solid green body and then
// into the finished texture.

// Nanoframe pixel verdicts. Anything else is a palette index to write.
const (
	NanoframeErase = -2 // the pixel is not drawn at all
	NanoframeKeep  = -1 // the pixel keeps its composed (textured or flat) colour
)

// NanoframeHeightBias is the constant the model rasterizer adds to a vertex's
// whole model-relative height to form the per-pixel height key, which doubles
// as the model's own depth key [03 §5.2][03 R-REN-03A §2]. The base exists so
// that geometry below the model origin still keys non-negative.
//
// This constant previously carried an open-question marker — "retail selects
// 125 instead of 50 on one unit-definition flag bit whose authored name is not
// identified; every stock draw path observed takes the 50 branch". Both halves are
// now answered and the marker is retired. The bit is the FBI `Digger` key; its
// contribution is a further +75, which is where 125 came from (125 = 50 + 75),
// and after the waterline pass the finished image is erased wherever the key is
// at or below 125 — exactly the geometry at or below the model origin, the
// buried half of a pop-up defence. Three stock units author it: ARMAMB,
// CORTOAST and CORVIPE [03 R-REN-03A §8]. It is not a branch between two bases:
// the raised base and the erase are one behavior and only make sense together.
//
// The key is also the *whole* height, not half of it; the halved reading came
// from the anti-aliased vertex path, which doubles the vertex before dividing
// [03 R-REN-03A §2 "Correction to the key formula"]. The one live key builder
// is `modelHeightKey` in the client's model composer, which applies this base,
// the Digger term, and the floor narrowing together; a second, divergent copy
// of the formula used to live here and has been removed rather than repaired,
// so there is one site (I11).
const NanoframeHeightBias = 50

// NanoframePulse returns the two nanoframe pulse colours for one unit at one
// tick. Both ping-pong across the sixteen-entry green ramp at 0xa0, at 33/30
// and 57/30 steps per tick, offset per unit by its own identifier so that two
// adjacent nanoframes do not pulse in lockstep [03 §5.2].
func NanoframePulse(unitID uint16, tick uint32) (bandColor, outlineColor uint8) {
	return nanoRamp(uint32(unitID^5) + tick*33/30), nanoRamp(uint32(unitID^9) + tick*57/30)
}

// nanoRamp folds a counter into the 0xa0..0xaf ramp, reflecting every sixteen
// steps so the colour walks up and back down rather than jumping.
func nanoRamp(v uint32) uint8 {
	if v&0x10 != 0 {
		return 0xaf - uint8(v&0xf)
	}
	return 0xa0 + uint8(v&0xf)
}

// NanoframeReveal is one tick's reveal state for one unfinished unit.
type NanoframeReveal struct {
	Line  uint8 // sweep line position in height-key units
	Floor uint8 // start of the four-unit sweep band
	Below int16 // verdict for pixels under the band
	Band  int16 // verdict for pixels inside the band
	Above int16 // verdict for pixels at or over the line
}

// Verdict resolves one composed pixel's height key.
func (r NanoframeReveal) Verdict(heightKey uint8) int16 {
	switch {
	case heightKey < r.Floor:
		return r.Below
	case heightKey >= r.Line:
		return r.Above
	default:
		return r.Band
	}
}

// BuildNanoframeReveal maps a remaining fraction and the tick's two pulse
// colours onto the reveal's five stages [03 §5.2]. `remaining` is the
// authoritative construction fraction: 1 at request, 0 at completion, so the
// stages run from the last case here to the first as the unit is built.
func BuildNanoframeReveal(remaining float32, bandColor, outlineColor uint8) NanoframeReveal {
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 1 {
		remaining = 1
	}
	p := int32(remaining * 255) // truncated toward zero, as the retail conversion is
	var r NanoframeReveal
	var line int32
	switch {
	case p > 235:
		// Empty outline, swept once by a bright line running down the model.
		line = (p - 235) * 255 / 20
		r.Below, r.Band, r.Above = NanoframeErase, int16(bandColor), NanoframeErase
	case p > 200:
		// Empty outline again, swept a second time.
		line = (p - 200) * 255 / 35
		r.Below, r.Band, r.Above = NanoframeErase, int16(bandColor), NanoframeErase
	case p > 115:
		// Solid green rises from the model's base; nothing above the line yet.
		line = (115-p)*255/85 - 1
		r.Below, r.Band, r.Above = int16(bandColor), int16(outlineColor), NanoframeErase
	case p > 30:
		// Texture rises from the base; the rest of the body stays solid green.
		line = (30-p)*255/85 - 1
		r.Below, r.Band, r.Above = NanoframeKeep, int16(outlineColor), int16(bandColor)
	default:
		// Fully textured, with a last bright line running back down the model.
		line = p * 255 / 30
		r.Below, r.Band, r.Above = NanoframeKeep, int16(bandColor), NanoframeKeep
	}
	r.Line = uint8(line)
	if r.Line >= 4 {
		r.Floor = r.Line - 4
	}
	return r
}
