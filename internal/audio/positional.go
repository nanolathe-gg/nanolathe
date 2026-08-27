package audio

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Positional audio is presentation-only and must never mutate authoritative
// state or draw from the simulation RNG stream [03 §8.3] C19, [01 §7.2] I4.
// The helper has audience gating, two-level attenuation, and viewport-relative
// pan [03 §8.3].

// Viewport describes the presentation viewport in 16.16 world units projected
// to pixel space via the retail half-height shear. For the mono fallback the
// volume gate uses integer pixel bounds; for stereo the pan vector is
// viewport-relative. All fields are world-pixel units (short truncated).
type Viewport struct {
	Left, Top     int32 // camera origin in map pixels (short truncated)
	Width, Height int32 // viewport dimensions in pixels (tiles*16)
	MapW, MapH    int32 // map dimensions in pixels (tiles*16) for mixer center
	StereoCapable bool  // backend-reported stereo capability [03 §8.3]
}

// Pan holds the viewport-relative offsets passed to the mixer when stereo is
// capable. The mixer computes dx = pos.x - ((viewW/2)<<4) - left and a
// half-height sheared dy. The third component is zero in retail.
type Pan struct {
	X, Y, Z int32
}

// MixerCenter returns the reference-center floats used before playback when
// stereo capable: (mapW+mapH)/2 <<4 and
// ?? (world center). We expose the scalar used there for tests.
func MixerCenter(v Viewport) (cx, cy float32) {
	cx = float32((v.MapW + v.MapH) / 2 << 4)
	cy = float32((v.MapW + v.MapH) / 2 * 0x10) // same as cx in retail, kept distinct for doc
	return cx, cy
}

// ComputePan returns the retail pan vector for a world position in 16.16
// fixed point. It mirrors the stereo branch:
//
//	dx = posXpx - ((viewW/2)<<4) - left
//	dy = top + ((viewH/2)<<4) + (posYhi>>1) - posZpx
//
// pos is [x, y, z] in 16.16; y is height. The shear is (y>>1) applied to z.
func ComputePan(pos [3]numeric.Fixed, v Viewport) Pan {
	px := int32(int16(int64(pos[0]) >> 16))
	py := int32(int16(int64(pos[1]) >> 16))
	pz := int32(int16(int64(pos[2]) >> 16))
	dx := px - ((v.Width / 2) << 4) - v.Left
	dy := v.Top + ((v.Height / 2) << 4) + (py >> 1) - pz
	return Pan{X: dx, Y: 0, Z: dy}
}

// Attenuation codes are DirectSound volume centibels for the mono fallback.
// In-view -585 vs
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
// because the viewport compares tile extents with truncated positions. Pos is
// 16.16.
func Attenuate(pos [3]numeric.Fixed, v Viewport) int32 {
	px := int32(int16(int64(pos[0]) >> 16))
	pz := int32(int16(int64(pos[2]) >> 16))
	left := v.Left
	top := v.Top
	right := v.Width*0x10 + left
	bottom := v.Height*0x10 + top
	if left <= px && top <= pz && px <= right && pz <= bottom {
		return VolInView
	}
	return VolOffScreen
}

// VisibilityMode selects the audience gate source. When ModeExplored is set
// the per-player explored byte grid is tested; otherwise the LOS word mask is
// tested at the local player bit only (no ally OR) [03 §3.1].
type VisibilityMode uint32

const (
	ModeExplored VisibilityMode = 1 << 1 // 0x02 byte-vs-word [P0-18] [03 §3.1]
)

// IsAudible decides the local audience gate for a world position.
// It quantizes the position to a visibility cell, rejects off-map positions,
// then checks the mode-selected grid [03 §3.1].
//
// cellX, cellY are already quantized to 32-pixel visibility tiles (pos>>20 with
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// wordMask is []uint16 length w*h, ten usable bits per cell [03 §3.1].
// byteGrids is [10][]uint8 per-player explored counts, each length w*h.
// localSlot is the local player slot (0..9). mode chooses source.
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
	if localSlot < 0 || localSlot >= 10 {
		return false
	}
	bit := uint16(1 << localSlot)
	return wordMask[idx]&bit != 0
}

// CellFromWorld converts a 16.16 world position to visibility tile coordinates
// via sign-corrected floor division.
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
