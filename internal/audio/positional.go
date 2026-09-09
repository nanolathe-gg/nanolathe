package audio

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Positional audio is presentation-only and must never mutate authoritative
// state or draw from the simulation RNG stream [03 §8.3] C19, [01 §7.2] I4.
// The helper has audience gating, Sound-Mode-selected placement, and
// viewport-relative pan [03 §8.3].

// SoundMode is the selected spatial placement policy. It deliberately names
// the setting rather than a device capability: a stereo host still follows the
// Mono branch until the player selects 3D [03 R-AUD-01 §1][03 R-AUD-01 §2].
type SoundMode uint8

const (
	SoundModeOff SoundMode = iota
	SoundModeMono
	SoundMode3D
)

// SpatialModeFromPreference translates the packed settings field. Only the
// exact value 2 enables 3-D; every other stored value clears the device 3-D
// flag, including Off and hand-edited in-range values [03 R-AUD-01 §2].
func SpatialModeFromPreference(mode int) SoundMode {
	if mode == int(SoundMode3D) {
		return SoundMode3D
	}
	return SoundModeMono
}

// Viewport describes the presentation viewport in 16.16 world units projected
// to pixel space via the retail half-height shear. For the mono fallback the
// volume gate uses integer pixel bounds; for 3-D the pan vector is
// viewport-relative. Left/Top are map pixels, while Width/Height and MapW/MapH
// are 16-pixel cells, exactly as the retail placement arithmetic requires.
type Viewport struct {
	Left, Top     int32 // beam origin in map pixels (short truncated)
	Width, Height int32 // beam dimensions in 16-pixel cells
	MapW, MapH    int32 // map dimensions in 16-pixel cells for 3-D max distance
	SoundMode     SoundMode
	// StereoCapable is retained for the existing publication tuple and mirrors
	// selected 3-D placement; it is no longer a device-capability decision.
	StereoCapable bool
}

// Pan holds the viewport-relative offsets passed to the 3-D mixer. The mixer
// computes dx = pos.x - ((viewW/2)<<4) - left and a half-height sheared dy.
// The middle component is zero in retail.
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

// DistanceBounds returns the 3-D buffer distances in pixels. Width, Height,
// MapW and MapH are their retail 16-pixel-cell quantities [03 R-AUD-01 §1].
func DistanceBounds(v Viewport) (minDist, maxDist int32) {
	minDist = ((v.Height + v.Width) / 2) * 16
	maxDist = (v.MapW + v.MapH) * 16
	return minDist, maxDist
}

// DistanceGain is DirectSound's documented default inverse-distance rolloff:
// full gain through minDist, minDist/distance beyond it, and the max-distance
// gain held after maxDist [03 R-AUD-01 §1]. The distance uses both planar
// components of the established placement vector. It is presentation-only.
func DistanceGain(p Pan, v Viewport) float64 {
	minDist, maxDist := DistanceBounds(v)
	if minDist <= 0 || maxDist <= 0 {
		// TODO(question): retail's behavior for zero/invalid device distances is
		// unestablished. Host policy retains the base level rather than inventing
		// a clamp; a trace with malformed view/map dimensions would settle it.
		return 1
	}
	distance := math.Hypot(float64(p.X), float64(p.Z))
	if distance <= float64(minDist) {
		return 1
	}
	limit := float64(maxDist)
	if limit < float64(minDist) {
		// TODO(question): retail's behavior when maxDist < minDist is not
		// established. Keep the closest valid endpoint as host policy.
		limit = float64(minDist)
	}
	if distance > limit {
		distance = limit
	}
	return float64(minDist) / distance
}

// The audience gate that decides whether a positional cue is heard is NOT
// here. It is the visibility service's point query [03 §8.3] "audience
// gating", which projects the source with the half-height shear and tests the
// mode-selected grid at the local player's slot [03 §3.2] step 4. This package
// previously carried an IsAudible/CellFromWorld pair that quantized X and Z
// alone and took copies of the grids; both were wrong for any elevated source
// and are gone, so nothing can reach for the approximation again.
