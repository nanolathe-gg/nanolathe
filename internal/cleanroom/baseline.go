package cleanroom

// Baseline is the census of raw-forensics occurrences that predate the
// clean-room lint, taken with tools/cleanroom-baseline. It is a debt
// register, not a permission list: every entry is a comment or research
// line that still has to be rewritten as clean-room prose, with its
// address-level trail moved to /tmp/ta-decompile/notes/.
//
// Total at baseline: 198 occurrences across 48 files.
//
// A file absent from this map must have zero occurrences. Counts may only
// go down, and going down requires updating this file in the same change.
var Baseline = map[string]int{
	"cmd/nanolathe/allies_test.go":                  1,
	"cmd/nanolathe/selmap_test.go":                  3,
	"formats/gaf_colorkey_test.go":                  2,
	"internal/ai/o6_score_test.go":                  5,
	"internal/audio/positional.go":                  1,
	"internal/cob/ports_test.go":                    3,
	"internal/combat/aim.go":                        1,
	"internal/combat/damage.go":                     7,
	"internal/combat/death.go":                      2,
	"internal/combat/impact.go":                     1,
	"internal/combat/meteor.go":                     1,
	"internal/combat/motion.go":                     6,
	"internal/combat/motion_test.go":                1,
	"internal/combat/pool.go":                       20,
	"internal/combat/stockpile.go":                  9,
	"internal/combat/target.go":                     6,
	"internal/construction/capture.go":              20,
	"internal/construction/factory_test.go":         1,
	"internal/construction/gap_p014_p015_test.go":   2,
	"internal/construction/resurrection.go":         17,
	"internal/construction/reverse.go":              1,
	"internal/construction/rs10_test.go":            1,
	"internal/content/sound_sc7_test.go":            1,
	"internal/features/reproduce.go":                1,
	"internal/features/sink.go":                     2,
	"internal/hud/minimap_test.go":                  4,
	"internal/hud/selection_test.go":                2,
	"internal/mission/catalog.go":                   1,
	"internal/mission/placement.go":                 18,
	"internal/mission/sparse_test.go":               1,
	"internal/movement/collision.go":                11,
	"internal/movement/integrate.go":                4,
	"internal/movement/locomotion_fidelity_test.go": 2,
	"internal/movement/profile.go":                  9,
	"internal/orders/pump_test.go":                  2,
	"internal/orders/selectable.go":                 1,
	"internal/orders/zbuildweapon.go":               6,
	"internal/render/minimap_test.go":               2,
	"internal/render/model_test.go":                 1,
	"internal/save/bank_test.go":                    1,
	"internal/save/retail_corpus_test.go":           1,
	"internal/units/sweep.go":                       4,
	"internal/visibility/fog.go":                    1,
	"internal/world/picking.go":                     1,
	"internal/world/placement.go":                   2,
	"internal/world/placement_test.go":              2,
	"internal/world/terrain.go":                     5,
	"internal/world/terrain_test.go":                2,
}
