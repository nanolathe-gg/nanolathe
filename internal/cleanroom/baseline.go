package cleanroom

// Baseline is the census of raw-forensics occurrences that predate the
// clean-room lint, taken with tools/cleanroom-baseline. It is a debt
// register, not a permission list: every entry is a comment or research
// line that still has to be rewritten as clean-room prose, with its
// address-level trail moved to $HOME/ta-decompile/notes/.
//
// Total at baseline: 3 occurrences across 2 files.
//
// A file absent from this map must have zero occurrences. Counts may only
// go down, and going down requires updating this file in the same change.
//
// Both remaining entries are not clean-room debt at all — they are
// literal hex constants in real arithmetic that happen to match the
// structure-offset or executable-address patterns' shape, not prose
// describing executable layout: hud/selection_test.go's `PagePagedBit` test
// literal and
// world/picking.go's cell-alignment constant in CursorToWorld. None can be
// reworded without touching code, which is outside comment-only scope; a
// future pass may narrow the patterns instead. WU-19-56 rewrote the previous
// two genuine entries (combat/stockpile.go, orders/zbuildweapon.go) as
// clean-room prose citing [06 R-WPN-05 §2] and [06 §11.1] and removed them
// from this map.
var Baseline = map[string]int{
	"internal/hud/selection_test.go": 2,
	"internal/world/picking.go":      1,
}
