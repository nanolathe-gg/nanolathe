package camera

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// ViewScale is the presentation record scale [F-P1-008], encoded as twice
// the number of screen pixels per world pixel. ViewScaleNative is the retail
// picture and ViewScaleDetail is the 2x detail view (DESIGN_GPU_RENDERER §14.1).
// Zero reads as native, so a zero Camera is the retail one. Fractional live
// presentation factors belong to Zoom (DESIGN_GPU_RENDERER §16.2).
type ViewScale int32

const (
	ViewScaleNative ViewScale = 2 // 1x, the retail view
	ViewScaleDetail ViewScale = 4 // 2x, the detail view
)

// Norm selects the two supported views, reading anything below detail as native.
func (s ViewScale) Norm() ViewScale {
	if s >= ViewScaleDetail {
		return ViewScaleDetail
	}
	return ViewScaleNative
}

// Native reports whether the scale is the retail 1x view.
func (s ViewScale) Native() bool { return s.Norm() == ViewScaleNative }

// Whole returns the whole factor (1 or 2). Both supported views are integral.
func (s ViewScale) Whole() (int32, bool) { return int32(s.Norm() / 2), true }

// Project scales a world-relative offset to the first screen pixel it covers.
// Inverse(Project(v)) == v at both supported views (DESIGN_GPU_RENDERER §14.1).
func (s ViewScale) Project(v int32) int32 { return v * int32(s.Norm()/2) }

// Inverse maps a screen-relative offset back to the world pixel drawn there,
// flooring negative offsets too [I3][03 §2.1].
func (s ViewScale) Inverse(v int32) int32 {
	return int32(numeric.FloorDiv(int64(v), int64(s.Norm()/2)))
}

// Px scales an extent or authored offset at the record scale.
func (s ViewScale) Px(v int32) int32 { return s.Project(v) }

// Float is the scale as a factor, for a shader lane or a metadata record.
func (s ViewScale) Float() float64 { return float64(s.Norm()) / 2 }

// Next is the F9 cycle: 1x, 2x and back to 1x (DESIGN_GPU_RENDERER §14.6).
func (s ViewScale) Next() ViewScale {
	if s.Native() {
		return ViewScaleDetail
	}
	return ViewScaleNative
}

// String is the factor the user reads: "1x" or "2x".
func (s ViewScale) String() string {
	if s.Native() {
		return "1x"
	}
	return "2x"
}

// ParseViewScale reads "1" or "2", with an optional trailing "x".
func ParseViewScale(text string) (ViewScale, error) {
	switch strings.TrimSuffix(strings.TrimSpace(text), "x") {
	case "1":
		return ViewScaleNative, nil
	case "2":
		return ViewScaleDetail, nil
	}
	return 0, fmt.Errorf("view scale must be 1 (native) or 2 (the detail view), got %q", text)
}
