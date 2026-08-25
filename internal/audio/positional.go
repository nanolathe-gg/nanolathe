package audio

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Positional audio is presentation-only and must never mutate authoritative
// state or draw from the simulation RNG stream [03 §8.3] C19, [01 §7.2] I4.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// attenuation plus viewport-relative pan. This file paraphrases that contract
// without copying raw decompile.

// Viewport describes the presentation viewport in 16.16 world units projected
// to pixel space via the retail half-height shear. For the mono fallback the
// volume gate uses integer pixel bounds; for stereo the pan vector is
// viewport-relative. All fields are world-pixel units (short truncated).
type Viewport struct {
	Left, Top     int32 // camera origin in map pixels (short truncated)
	Width, Height int32 // viewport dimensions in pixels (tiles*16)
	MapW, MapH    int32 // map dimensions in pixels (tiles*16) for mixer center
	StereoCapable bool  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

// Pan holds the viewport-relative offsets passed to the mixer when stereo is
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// half-height sheared dy. The third component is zero in retail.
type Pan struct {
	X, Y, Z int32
}

// MixerCenter returns the reference-center floats that retail sets via
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// ?? (world center). We expose the scalar used there for tests.
func MixerCenter(v Viewport) (cx, cy float32) {
	cx = float32((v.MapW + v.MapH) / 2 << 4)
	cy = float32((v.MapW + v.MapH) / 2 * 0x10) // same as cx in retail, kept distinct for doc
	return cx, cy
}

// ComputePan returns the retail pan vector for a world position in 16.16
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
//	dx = posXpx - ((viewW/2)<<4) - left
//	dy = top + ((viewH/2)<<4) + (posYhi>>1) - posZpx
//
// pos is [x, y, z] in 16.16; y is height. The shear is (y>>1) applied to z.
func ComputePan(pos [3]numeric.Fixed, v Viewport) Pan {
	px := int32(pos[0] >> 16) // short truncate pixel
	py := int32(pos[1] >> 16)
	pz := int32(pos[2] >> 16)
	// retail uses short casts; we keep 32-bit but semantics identical for stock map sizes.
	dx := px - ((v.Width / 2) << 4) - v.Left
	dy := v.Top + ((v.Height / 2) << 4) + (py >> 1) - pz
	return Pan{X: dx, Y: 0, Z: dy}
}

// Attenuation codes are DirectSound volume centibels observed in
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// off-screen -1585 is a ~10 dB step (|586 vs 1586? Actual 32-bit as signed
// 16: 0xFDB7=-585, 0xF9CF=-1585). Retail never discards off-screen, just
// attenuates. We keep the raw signed values.
const (
	VolInView    = -585  // 0xFFFFFDB7 [03 §8.3]
	VolOffScreen = -1585 // 0xFFFFF9CF [03 §8.3] ~1000 lower
)

// Attenuate returns the mono-fallback volume for a world pos vs viewport.
// Inside inclusive bounds left<=x<=right and top<=z<=bottom yields VolInView
// else VolOffScreen. Right = left + viewW*0x10, bottom = top + viewH*0x10
// because retail compares tiles<<4 vs short-truncated positions (see
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func Attenuate(pos [3]numeric.Fixed, v Viewport) int32 {
	px := int32(pos[0] >> 16)
	pz := int32(pos[2] >> 16)
	left := v.Left
	top := v.Top
	right := v.Width*0x10 + left
	bottom := v.Height*0x10 + top
	if left <= px && top <= pz && px <= right && pz <= bottom {
		return VolInView
	}
	return VolOffScreen
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// gate source. When ModeExplored is set the per-player explored byte grid at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// at the local player bit only (no ally OR).
type VisibilityMode uint32

const (
	ModeExplored VisibilityMode = 1 << 1 // 0x02 byte-vs-word [P0-18] [03 §3.1]
)

// IsAudible decides the local audience gate for a world position.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// cellX, cellY are already quantized to 32-pixel visibility tiles (pos>>20 with
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// wordMask is []uint16 length w*h, ten usable bits per cell [03 §3.1].
// byteGrids is [10][]uint8 per-player explored counts, each length w*h.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Off-grid returns false (off-map silent).
// Ally vision is never OR'd — only the local slot's bit/count is tested.
func IsAudible(cellX, cellY int, localSlot int, mode VisibilityMode, wordMask []uint16, byteGrids [10][]uint8, w, h int) bool {
	if cellX < 0 || cellY < 0 || cellX >= w || cellY >= h {
		return false
	}
	idx := cellY*w + cellX
	if mode&ModeExplored != 0 {
		if localSlot < 0 || localSlot >= len(byteGrids) || byteGrids[localSlot] == nil {
			return false
		}
		if idx >= len(byteGrids[localSlot]) {
			return false
		}
		return byteGrids[localSlot][idx] != 0
	}
	if idx >= len(wordMask) {
		return false
	}
	bit := uint16(1 << (localSlot & 0x1F))
	return wordMask[idx]&bit != 0
}

// CellFromWorld converts a 16.16 world position to visibility tile coordinates
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// For retail map sizes this equals worldToCell >>1? We keep the literal shift
// so edge at negative coordinates matches the sign-corrected floor (Invariants I3).
func CellFromWorld(p numeric.Fixed) int {
	// floorDiv on 16.16 units where tile is 32 pixels = 32*65536 = 0x200000
	raw := int64(p)
	const tile = 32 * 65536
	q := raw / tile
	if raw < 0 && raw%tile != 0 {
		q--
	}
	return int(q)
}

// CellFromWorld2D returns tile X,Z for a 3-component pos (ignores Y).
func CellFromWorld2D(pos [3]numeric.Fixed) (int, int) {
	return CellFromWorld(pos[0]), CellFromWorld(pos[2])
}
