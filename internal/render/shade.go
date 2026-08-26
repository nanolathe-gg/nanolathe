// Package render — SHD row selection helper [03 §4.3] [03 §5.2][rr-09 addendum].
//
// [03 §4.3] PALETTE.SHD is 8192 bytes: 32 rows × 256 entries for shading/darkening.
// Row 15 identity (mean 232/256 self-maps), row 0 near-black (97 drop), row 31 +54 bright.
// Row selection is row = __ftol(dot*5)&31 with dont-shade pin 15 [03 §2.4.1][rr-09 addendum] via
// per-vertex averaged normals; implemented in internal/client/model.go. This file retains
// SHDMidRow as a presentation fallback for callers without a normal and centralizes the
// placeholder contract grep-able via A23 so nobody re-fixes it .

package render

// SHDRowCount is the number of SHD rows [03 §4.3] (fmt pal).
const SHDRowCount = 32 // [03 §4.3] 32×256

// SHDMidRow is the presentation fallback mid row [03 §4.3][rr-09 addendum].
// Real SHD row = __ftol(dot*5)&31 implemented in client/model.go ;
// this alias remains for callers without a per-vertex dot (presentation-only, sim never reads SHD).
const SHDMidRow = 16 // [03 §4.3][rr-09 addendum] fallback; real row in client/model.go

// SHDIdentityRow is the measured near-identity row (row 15) where 232/256 entries self-map [03 §4.3].
// Kept for reference; the placeholder in use is SHDMidRow=16.
const SHDIdentityRow = 15 // [03 §4.3]

// SelectShadeRow returns the SHD row for model lighting.
// Real per-vertex selection is row=__ftol(dot*5)&31 [rr-09 addendum] via internal/client/model.go;
// this helper remains a presentation fallback returning mid row for callers without a normal .
// light is retained so callers do not invent a per-primitive row without citation.
func SelectShadeRow(light int) int {
	_ = light
	return SHDMidRow // fallback; real row in client/model.go [rr-09 addendum]
}

// ClampShadeRow clamps a row to 0..31 [03 §4.3].
func ClampShadeRow(row int) int {
	if row < 0 {
		return 0
	}
	if row >= SHDRowCount {
		return SHDRowCount - 1
	}
	return row
}
