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

// The audience gate that decides whether a positional cue is heard is NOT
// here. It is the visibility service's point query [03 §8.3] "audience
// gating", which projects the source with the half-height shear and tests the
// mode-selected grid at the local player's slot [03 §3.2] step 4. This package
// previously carried an IsAudible/CellFromWorld pair that quantized X and Z
// alone and took copies of the grids; both were wrong for any elevated source
// and are gone, so nothing can reach for the approximation again.
