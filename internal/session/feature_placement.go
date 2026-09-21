// The mission file's `[features]` pass, shared by the campaign and skirmish
// battle-entry paths [02 R-MAP-01 §8][05 R-FEAT-01 §3].

package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// missionFeatureAnchor resolves one mission-file `[features]` entry's authored
// coordinates to the anchor cell the stamp receives [02 R-MAP-01 §8].
//
// The authored pair is a CELL index. It is not a map pixel position: the pixel
// convention of `[units]` and `[specials]` does not extend to this section and
// nothing divides the pair by the cell size [fmt ota].
//
// The resolved definition then selects the anchor. A definition that names a
// sprite `filename` anchors at the authored cell verbatim. Every other
// definition — including one that names neither a sprite nor a model, which is
// why the predicate is "no sprite" rather than "has model" — is
// centre-referenced: the anchor is the authored cell minus half the
// definition's stored footprint, with the halving a signed division that
// truncates toward zero. Go's integer division is that division, and the
// stored footprint is signed, so a negative authored footprint halves toward
// zero here exactly as it does there.
//
// The caller applies the in-bounds test to what this returns, never to the
// authored pair: the subtraction is what decides whether an entry near an edge
// is placed at all.
func missionFeatureAnchor(def *content.FeatureDef, cellX, cellZ int32) (int32, int32) {
	if def == nil || def.Filename != "" {
		return cellX, cellZ
	}
	return cellX - def.FootprintX/2, cellZ - def.FootprintZ/2
}

// stampMissionFeatures is the mission file's `[features]` pass.
//
// Terrain-provided and mission-provided feature records converge on the same
// feature stamping service, and deterministic load order matters [08
// "Placement and battle entry"]: the terrain file's own features are already
// stamped by world.Load's ExpandPlot + stampFeatureAnchors, and this pass adds
// the mission file's in decode order — never map iteration order — on top of
// them [I1][04 §6.2]. No RNG draws occur here [I4].
//
// Each placement is tried in order and a failure never aborts the ones after
// it: a blank or unresolved name, an out-of-bounds anchor and the stamp's own
// refusals all skip the single entry [P1-10][P1-15]. The stamp itself is
// PlaceAt, which applies the footprint fringe, the dense-pack teardown and the
// pool limits with placer nibble 10 [05 R-FEAT-01 §3][06 §13.1].
//
// The whole loop runs inside RunMissionFeaturePass because the loader's order
// puts this pass BEFORE the edge/lava void sweep [02 R-MAP-01 §6]: a footprint
// reaching an already-swept cell would be refused by the dense-pack teardown
// and the entire feature dropped, and a fringe cell of a feature placed in the
// edge strips has to end void rather than fringe. The metal-deposit seeding
// that each battle entry runs after this pass still runs after every stamp
// [05 R-FEAT-01 §7].
func stampMissionFeatures(s *Session, m *mission.Mission) error {
	if s == nil || s.World == nil || s.Features == nil {
		return nil
	}
	if m == nil || len(m.Features) == 0 {
		return nil
	}
	s.World.RunMissionFeaturePass(func() {
		for _, fp := range m.Features {
			if !fp.IsPlaced() {
				continue
			}
			name := fp.Name
			if name == "" {
				continue
			}
			var def *content.FeatureDef
			if s.Catalog != nil && s.Catalog.Features != nil {
				def = s.Catalog.Features[content.CanonicalKey(name)]
			}
			if def == nil {
				continue
			}
			cx, cz := missionFeatureAnchor(def, fp.X, fp.Z)
			// The bounds test runs on the SUBTRACTED anchor, not on the
			// authored cell [02 R-MAP-01 §8]. An out-of-range anchor silently
			// skips the entry, as retail's does [P1-15].
			if cx < 0 || cz < 0 || cx >= s.World.CellW || cz >= s.World.CellH {
				continue
			}
			s.Features.PlaceAt(int(cx), int(cz), def)
		}
	})
	return nil
}
