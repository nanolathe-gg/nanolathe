// Package render — SHD row selection helper [03 §4.3] [03 §5.2].
//
// [03 §4.3] PALETTE.SHD is 8192 bytes: 32 rows × 256 entries for shading/darkening.
// Row 15 identity (mean 232/256 self-maps), row 0 near-black (97 drop), row 31 +54 bright.
// The exact row selection formula is not established in bounded decompile.
// Plan uses identity/mid row 16 placeholder (A23) [PLAN_13 Explicit unknowns].
// This file centralizes the placeholder so the contract is grep-able via A23.

package render

// SHDRowCount is the number of SHD rows [03 §4.3] (fmt pal).
const SHDRowCount = 32 // [03 §4.3] 32×256

// SHDMidRow is the placeholder mid row used until the selection formula is traced (A23).
// TODO(question): SHD row selection formula not established [03 §4.3]; use identity mid row 16.
// This is presentation-only; sim never reads SHD.
const SHDMidRow = 16 // [03 §4.3] A23 placeholder

// SHDIdentityRow is the measured near-identity row (row 15) where 232/256 entries self-map [03 §4.3].
// Kept for reference; the placeholder in use is SHDMidRow=16.
const SHDIdentityRow = 15 // [03 §4.3]

// SelectShadeRow returns the SHD row for model lighting.
// Until the retail light→row formula is traced, it returns the placeholder mid row.
// A23 stays TODO(question) presentation-only [03 §4.3][PLAN_GAPS A23].
// light is unused until established; it is retained as an argument so callers
// do not invent a per-primitive row without citation.
func SelectShadeRow(light int) int {
	_ = light
	return SHDMidRow // TODO(question) A23
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
