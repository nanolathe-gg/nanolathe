// Package render implements presentation-only rendering state [03 §1] [03 §5.6].
// Screen shake is presentation but driven from the authoritative impact dispatcher
// so every peer requests the same shakes from the same weapon data [03 §5.6].
package render

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// Shake implements screen shake [03 §5.6] (C9).
//
// State is presentation-only and never touches the simulation RNG stream (I4).
// It uses the CRT presentation stream and costs exactly two CRT draws per
// active tick [03 §5.6] (I4). The jitter is added in place to the global
// camera origin, producing a permanent random walk clamped to the map [03 §5.6].
//
// Request handling [03 §5.6]:
//   - If the preference bit 0x10 is set the request returns with state
//     untouched; which authored setting drives that bit is not established
//     (TODO(question): setting name unestablished [03 §5.6]).
//   - Otherwise if inactive the two amplitude accumulators are cleared to zero
//     while the duration accumulator is left unchanged.
//   - New duration is trunc((duration+authoredDuration)/2) with signed
//     truncation toward zero [01 §8], remaining is set to duration, the signed
//     magnitudes are added into the two amplitude accumulators, and if
//     duration>0 active is set. There is no queue, maximum, or distance falloff.
//
// Consumption [03 §5.6], once per simulation tick after the projectile phase:
//
//	if not active: return
//	if remaining <=0: active=false; return
//	sx = amplitudeX * remaining / duration  (signed, truncating)
//	sy = amplitudeY * remaining / duration
//	cameraX += rand()*sx/0x8000 - (abs(sx)>>1)  // two CRT draws [03 §5.6] (I4)
//	cameraY += rand()*sy/0x8000 - (abs(sy)>>1)
//	remaining -=1
//
// The envelope is linear decay with uniform white noise [03 §5.6].
// The camera clamp runs immediately afterwards [03 §5.6].
type Shake struct {
	active    bool
	duration  int32
	remaining int32
	ampX      int32
	ampY      int32
	disabled  bool // preference bit 0x10 [03 §5.6]
}

// SetDisabled controls the preference bit 0x10 gate [03 §5.6].
// When disabled is true Request returns with state untouched.
func (s *Shake) SetDisabled(disabled bool) {
	if s == nil {
		return
	}
	s.disabled = disabled
}

// Disabled reports whether the preference gate is set [03 §5.6].
func (s *Shake) Disabled() bool {
	if s == nil {
		return false
	}
	return s.disabled
}

// IsActive reports whether shake is active [03 §5.6].
func (s *Shake) IsActive() bool {
	if s == nil {
		return false
	}
	return s.active
}

// Duration returns the current blended duration accumulator [03 §5.6].
func (s *Shake) Duration() int32 {
	if s == nil {
		return 0
	}
	return s.duration
}

// Remaining returns ticks remaining [03 §5.6].
func (s *Shake) Remaining() int32 {
	if s == nil {
		return 0
	}
	return s.remaining
}

// AmpX returns the X amplitude accumulator [03 §5.6].
func (s *Shake) AmpX() int32 {
	if s == nil {
		return 0
	}
	return s.ampX
}

// AmpY returns the Y amplitude accumulator [03 §5.6].
func (s *Shake) AmpY() int32 {
	if s == nil {
		return 0
	}
	return s.ampY
}

// Request requests shake with the same signed magnitude on both axes [03 §5.6].
// The sole observed direct caller (authoritative impact dispatcher) passes the
// same authored magnitude on both axes [03 §5.6].
func (s *Shake) Request(magnitude, duration int32) {
	s.RequestXY(magnitude, magnitude, duration)
}

// RequestWithPrefs requests shake gated by the preference byte [03 §5.6].
// If bit 0x10 is set the request returns with state untouched.
func (s *Shake) RequestWithPrefs(magnitude, duration int32, prefs byte) {
	if prefs&0x10 != 0 {
		return
	}
	s.Request(magnitude, duration)
}

// RequestXY requests shake with distinct X/Y magnitudes [03 §5.6].
// This is the generic form; Request is the observed symmetric case.
func (s *Shake) RequestXY(magX, magY, authoredDuration int32) {
	if s == nil {
		return
	}
	if s.disabled {
		return // [03 §5.6] preference bit 0x10 disables it, state untouched
	}
	// If inactive the two amplitude accumulators are cleared to zero while the
	// duration accumulator is left unchanged [03 §5.6].
	if !s.active {
		s.ampX = 0
		s.ampY = 0
	}
	// New duration is trunc((duration + authoredDuration)/2) with signed
	// truncation toward zero [03 §5.6] [01 §8]. Go's int32 division truncates
	// toward zero.
	s.duration = (s.duration + authoredDuration) / 2
	s.remaining = s.duration
	s.ampX += magX
	s.ampY += magY
	if s.duration > 0 {
		s.active = true
	} else {
		// TODO(question): spec says "if duration>0 active is set" — unclear
		// whether a zero duration should clear active when already active.
		// Treat zero duration as inactive; remaining is zero so next Tick will
		// deactivate anyway.
		s.active = false
	}
}

// Tick consumes one simulation tick of shake [03 §5.6] (C9) (I4).
// It must be called once per simulation tick after the projectile phase [03 §5.6].
// It uses exactly two CRT draws per active tick and never touches lockstep state [03 §5.6] (I4).
// The jitter is added in place to the global camera origin and clamped to the map [03 §5.6] [07 §10].
// It is presentation-only: it reads the CRT stream and mutates the camera but
// never mutates simulation state (I6).
func (s *Shake) Tick(cam *camera.Camera, crt *rng.CRT) {
	if s == nil || cam == nil || crt == nil {
		return
	}
	if !s.active {
		return
	}
	if s.remaining <= 0 {
		s.active = false
		return
	}
	if s.duration == 0 {
		s.active = false
		return
	}
	// sx = amplitudeX * remaining / duration (signed, truncating) [03 §5.6]
	sx := s.ampX * s.remaining / s.duration
	sy := s.ampY * s.remaining / s.duration
	// Two CRT draws per active tick [03 §5.6] (I4) — presentation stream, not simulation (I4).
	rx := crt.Rand() // 0..0x7FFF [01 §7.2]
	ry := crt.Rand()
	dx := int32(int64(rx)*int64(sx)/0x8000 - int64(abs32(sx)>>1))
	dy := int32(int64(ry)*int64(sy)/0x8000 - int64(abs32(sy)>>1))
	cam.X += dx
	cam.Z += dy
	// Camera clamp holds both axes inside the map [03 §5.6] [07 §10].
	cam.X = clampAxis(cam.X, cam.MapW, cam.ViewW)
	cam.Z = clampAxis(cam.Z, cam.MapH, cam.ViewH)
	s.remaining--
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// clampAxis implements the per-axis retail clamp order [07 §10]:
// maximum = mapSize - viewSize
// if camera <0 ->0 else if camera>maximum ->maximum
// Ordered form controls the negative-maximum domain.
func clampAxis(cameraVal, mapSize, viewSize int32) int32 {
	maximum := mapSize - viewSize
	if cameraVal < 0 {
		return 0
	}
	if cameraVal > maximum {
		return maximum
	}
	return cameraVal
}
