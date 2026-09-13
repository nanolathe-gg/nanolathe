package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Rebuild admission shares the top northwest probe with session and committed
// presentation; a below-water base must not reject a surfaced hull [06 §3.1].
func TestVisibilityHullRebuildNorthwestCorner(t *testing.T) {
	ter := &world.Terrain{CellW: 16, CellH: 16, SeaLevel: 20}
	vis := visibility.New(ter, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	vis.ByteGrid(0)[1*int(vis.W)+1] = 1
	u := &units.Unit{Owner: 1, X: 64 * numeric.FixedOne, Z: 96 * numeric.FixedOne, Def: &content.UnitDef{FootprintX: 3, FootprintZ: 5, ModelTopFixed: 21 << 16}}
	if !directlyVisibleAtRebuild(0, u, vis) {
		t.Fatal("top northwest corner failed direct admission")
	}
	u.Hidden = true
	u.Flags |= visibility.DecloakBit
	if directlyVisibleAtRebuild(0, u, vis) {
		t.Fatal("decloak timer bypassed hidden-instance rejection")
	}
}
