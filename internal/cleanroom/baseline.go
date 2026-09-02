package cleanroom

// Baseline is the census of raw-forensics occurrences that predate the
// clean-room lint, taken with tools/cleanroom-baseline. It is a debt
// register, not a permission list: every entry is a comment or research
// line that still has to be rewritten as clean-room prose, with its
// address-level trail moved to /tmp/ta-decompile/notes/.
//
// Total at baseline: 84 occurrences across 24 files.
//
// A file absent from this map must have zero occurrences. Counts may only
// go down, and going down requires updating this file in the same change.
var Baseline = map[string]int{
	"internal/combat/aim.go":              1,
	"internal/combat/damage.go":           7,
	"internal/combat/death.go":            2,
	"internal/combat/impact.go":           1,
	"internal/combat/meteor.go":           1,
	"internal/combat/motion.go":           6,
	"internal/combat/motion_test.go":      1,
	"internal/combat/pool.go":             20,
	"internal/combat/stockpile.go":        9,
	"internal/combat/target.go":           6,
	"internal/hud/selection_test.go":      2,
	"internal/movement/integrate.go":      1,
	"internal/orders/pump_test.go":        2,
	"internal/orders/selectable.go":       1,
	"internal/orders/zbuildweapon.go":     6,
	"internal/render/minimap_test.go":     2,
	"internal/render/model_test.go":       1,
	"internal/save/bank_test.go":          1,
	"internal/save/retail_corpus_test.go": 1,
	"internal/units/sweep.go":             4,
	"internal/world/picking.go":           1,
	"internal/world/placement_test.go":    2,
	"internal/world/terrain.go":           4,
	"internal/world/terrain_test.go":      2,
}
