package render

// SHD row constants and the DONT_SHADE identity row
// [03 §4.3][03 R-RAST-01 §5].
//
// [03 §4.3] PALETTE.SHD is 8192 bytes: 32 rows × 256 entries for
// shading/darkening. Row 15 is the near-identity row (mean 232/256 entries
// self-map), row 0 is near-black, row 31 is +54 bright. The shaded piece
// renderer's real row selection is row = trunc(dot*5.0) & 0x1F with
// DONT_SHADE pinning row 15, computed per vertex-averaged normal by
// ShadeRowForNormal in model.go [03 R-RAST-01 §5]. There is no other row
// source: a presentation-fallback "mid row" constant used to stand here
// (`SHDMidRow`/`ModelShadeMidRow`/`SelectShadeRow`, deleted 2026-09-01) but
// no production draw path could reach it — internal/client/model.go reads
// PrimitiveDraw.ShadeRows directly, always the real formula's output, and
// the one call site that read the singular ShadeRow field used it only as a
// != NoShadeRow boolean gate, never as a numeric row. Row 16 is not a
// retail value anywhere; it must not survive as a reachable default.

// Shading is the presentation-level model-shading display option: with it set,
// a unit whose class bit says structure (`BMcode=0`) draws through the shaded
// piece renderer; every other unit, and every unit at all with the option
// clear, draws unshaded [R-RND-02A].
//
// Where the option comes from is settled, not open. This previously carried an
// open-question marker, "where retail loads the Shading display option from at
// startup; the Options restore-defaults path is the only traced writer". The
// restore-defaults path is neither the only writer nor the first one:
// `Shading` is a named 32-bit value under the game's registry key, read by the
// startup settings loader, which installs the default — the bit **set** — and
// writes it straight back when the value is absent [02 §3][02 "registry
// preference (Total Annihilation key)"]. The `VISUALS` page's `SHADING` button
// is bit 5 of the display option word and is the runtime writer
// [07 R-FE-01 §6]. This build keeps the same value and default in
// internal/settings (`DefaultShading`); `client.SetShadowOptions` carries the
// player's choice here, so this global and the client's own bit are one
// setting with one writer.
var Shading = true

// SHDRowCount is the number of SHD rows [03 §4.3] (fmt pal).
const SHDRowCount = 32 // [03 §4.3] 32×256

// SHDIdentityRow is the measured near-identity row (row 15) where 232/256
// entries self-map [03 §4.3]. It is also the DONT_SHADE pin value
// ShadeRowForNormal returns and the default PrimitiveDraw.ShadeRow carries
// before a real per-corner row is resolved [03 R-RAST-01 §5].
const SHDIdentityRow = 15 // [03 §4.3][03 R-RAST-01 §5]

// NoShadeRow marks a textured primitive emitted by the unshaded piece
// renderer. It bypasses PALETTE.SHD rather than approximating that path with
// row 15, which is not an identity mapping for every palette index [R-RND-02A]
// [fmt pal "SHD"].
const NoShadeRow = -1

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
