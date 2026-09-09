package camera

import (
	"fmt"
	"strings"
)

// ViewScale is the presentation view scale [F-P1-008] in half steps: the
// number of screen pixels one world pixel covers, times two. ViewScaleNative
// is the retail picture, ViewScaleMid the 1.5x view and ViewScaleDetail the
// 2x detail view (DESIGN_GPU_RENDERER §14.1). Zero reads as native, so a
// zero Camera is the retail one.
//
// It is a named type so that arithmetic against a plain pixel count does not
// compile: every world-space extent goes through Px, every projected position
// through Project and every picked position through Inverse, which is what
// keeps the 1.5x rounding in one place. At the whole scales the three are the
// exact integer multiply and floor divide the 1x and 2x views were built on,
// so nothing composed at those scales changes by a pixel.
type ViewScale int32

const (
	ViewScaleNative ViewScale = 2 // 1x, the retail view
	ViewScaleMid    ViewScale = 3 // 1.5x
	ViewScaleDetail ViewScale = 4 // 2x, the detail view
)

// Norm clamps the scale to the three views, reading zero (and anything below
// native) as native.
func (s ViewScale) Norm() ViewScale {
	if s <= ViewScaleNative {
		return ViewScaleNative
	}
	if s >= ViewScaleDetail {
		return ViewScaleDetail
	}
	return s
}

// Native reports whether the scale is the retail 1x view.
func (s ViewScale) Native() bool { return s.Norm() == ViewScaleNative }

// Whole returns the scale as a whole factor when it is one (1 or 2), and
// false at 1.5x.
func (s ViewScale) Whole() (int32, bool) {
	n := s.Norm()
	if n%2 != 0 {
		return 0, false
	}
	return int32(n / 2), true
}

// Project scales a world-relative offset to the first screen pixel it covers:
// ceil(v·s/2). Its inverse is Inverse, and the pair is consistent at every
// scale — Inverse(Project(v)) == v — so a picked screen pixel names the world
// pixel drawn there. At a whole scale it is the exact multiply.
func (s ViewScale) Project(v int32) int32 {
	n := int64(s.Norm())
	p := int64(v) * n
	if p >= 0 {
		return int32((p + 1) / 2)
	}
	return int32(p / 2) // truncation toward zero is ceil for a negative value
}

// Inverse maps a screen-relative offset back to the world pixel drawn there:
// floor(v·2/s) [I3][03 §2.1]. At a whole scale it is the floor divide the
// picking path always used.
func (s ViewScale) Inverse(v int32) int32 {
	n := int64(s.Norm())
	return int32(floorDiv(int64(v)*2, n))
}

// Px scales a screen extent or authored offset — a radius, a half-width, a
// sprite anchor — rounding half away from zero at 1.5x, so a symmetric extent
// stays symmetric. At a whole scale it is the exact multiply.
func (s ViewScale) Px(v int32) int32 {
	n := int64(s.Norm())
	p := int64(v) * n
	if p >= 0 {
		return int32((p + 1) / 2)
	}
	return int32((p - 1) / 2)
}

// Float is the scale as a factor, for a shader lane or a metadata record.
func (s ViewScale) Float() float64 { return float64(s.Norm()) / 2 }

// Next is the F9 cycle: 1x, 1.5x, 2x and back to 1x (DESIGN_GPU_RENDERER
// §14.6).
func (s ViewScale) Next() ViewScale {
	if n := s.Norm(); n < ViewScaleDetail {
		return n + 1
	}
	return ViewScaleNative
}

// String is the factor the user reads: "1x", "1.5x" or "2x".
func (s ViewScale) String() string {
	switch s.Norm() {
	case ViewScaleMid:
		return "1.5x"
	case ViewScaleDetail:
		return "2x"
	}
	return "1x"
}

// ParseViewScale reads a scale the way `--zoom` spells it: "1", "1.5" or "2",
// with an optional trailing "x".
func ParseViewScale(text string) (ViewScale, error) {
	switch strings.TrimSuffix(strings.TrimSpace(text), "x") {
	case "1":
		return ViewScaleNative, nil
	case "1.5":
		return ViewScaleMid, nil
	case "2":
		return ViewScaleDetail, nil
	}
	return 0, fmt.Errorf("view scale must be 1 (native), 1.5 or 2 (the detail view), got %q", text)
}
