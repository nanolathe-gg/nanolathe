// Package palette loads the retail palette tables and performs logical →
// physical lookups at present time.
//
// [03 §4.3] defines the five file-backed tables: PALETTE.PAL 1024 B
// (256×4 RGB+pad), GUIPAL.PAL, ALP 64 K (256×256), LHT 8 K (32×256),
// SHD 8 K (32×256), plus a 256-byte logical→physical lookup that the
// palette-install helper maintains. [fmt pal] gives the raw layouts
// (no header, size-identified) and the per-entry PAL stride.
//
// The 256-byte logical→physical table is NOT an indexed-pixel route. Retail
// builds it at GUI bootstrap by matching every GUIPAL.PAL entry into the
// installed PALETTE.PAL display palette, and consults it only when a
// *semantic* colour entry is resolved — GUI colour fields, FNT colours, the
// HUD's dcb[] health/resource entries, the selection overlays. Image bytes
// (GAF frames, PCX backgrounds, TNT tiles) are already PALETTE.PAL indices and
// are copied through unchanged; "no GUI lookup is performed again during
// indexed-to-RGB presentation" [03 §4.3][07 "Retail palette contract"].
// Model lighting selects an SHD row and the explosion flash halo selects an
// LHT row, both on indices that are already physical [03 §4.3.1][03 §4.3.2].
package palette
