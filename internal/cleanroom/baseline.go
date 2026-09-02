package cleanroom

// Baseline is the census of raw-forensics occurrences that predate the
// clean-room lint, taken with tools/cleanroom-baseline. It is a debt
// register, not a permission list: every entry is a comment or research
// line that still has to be rewritten as clean-room prose, with its
// address-level trail moved to /tmp/ta-decompile/notes/.
//
// Total at baseline: 19 occurrences across 5 files.
//
// A file absent from this map must have zero occurrences. Counts may only
// go down, and going down requires updating this file in the same change.
//
// Three of the five remaining entries are not clean-room debt at all — they
// are literal hex constants in real arithmetic that happen to match the
// structure-offset pattern's shape, not prose describing executable layout:
// hud/selection_test.go's `PagePagedBit` test literal, movement/integrate.go's
// sine-table rounding bias, and world/picking.go's cell-alignment constant in
// CursorToWorld. None can be reworded without touching code, which is outside
// comment-only scope; a future pass may narrow the pattern instead. The
// remaining two (combat/stockpile.go, orders/zbuildweapon.go) were owned by a
// concurrent WU-19-51 pass at the time of writing.
var Baseline = map[string]int{
	"internal/combat/stockpile.go":    9,
	"internal/hud/selection_test.go":  2,
	"internal/movement/integrate.go":  1,
	"internal/orders/zbuildweapon.go": 6,
	"internal/world/picking.go":       1,
}
