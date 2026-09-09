// Typed construction payloads [P0-I05].

package construction

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// FactoryPayload is the typed payload for factory production orders [05 "Factory production lifecycle"][P0-I05].
// It lives on orders.Node in the PRIMARY segment: definition catalog index in Param1,
// remaining count in Param2, factory state/progress in Phase, and BuildDefKey for stable identity.
// ID is BuildingBuild [04 §3.1][GAP T3].
type FactoryPayload struct {
	DefKey string // canonical catalog key, stable via Catalog.UnitDefIndex [P0-I05][02 §5]
	Index  uint32 // 1-based catalog index, 0 sentinel [P0-I05]
	Count  uint32 // remaining builds [05]
	Phase  uint8  // factory state 0..4 [05]
}

// MobilePayload is the typed payload for mobile construction orders [05 "Construction arithmetic"][P0-I05].
// Site anchor is authoritative GoalX/Z (world 16.16) plus orientation; builder relation is Owner.
// ID is MobileBuild or VTOL_MobileBuild [04 §3.1][P0-I05].
type MobilePayload struct {
	DefKey      string        // canonical key [P0-I05]
	Index       uint32        // catalog index [P0-I05]
	SiteX       numeric.Fixed // world X anchor [P0-I05]
	SiteZ       numeric.Fixed // world Z anchor [P0-I05]
	Orientation uint16        // build angle [02 "Unit record"] BuildAngle
	Count       uint32
}

// AssistPayload is the typed payload for assist/repair/reclaim/capture/resurrection orders [05][P0-I05].
// Target identity is orders.Node.Target (pool.Handle), progress is operation-specific.
type AssistPayload struct {
	Target   uint32 // pool.Handle of target unit/feature [P0-I05]
	Progress uint32 // operation-specific progress/counter [05]
	Op       string // operation name for diagnostics, e.g., "HelpBuild", "RepairUnit", "Reclaim", "Capture", "Resurrect"
}
