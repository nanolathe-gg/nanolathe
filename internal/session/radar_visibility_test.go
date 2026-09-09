package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestRadarFeatureVisibleSamplesOwnPositionNotFootprintCorners locks the
// second-pass admission test to a one-point sample at the candidate's own
// projected position, not the world composer's two-corner footprint test of
// [03 §5.1.5]. The minimap contacts pass draws projectiles and features from
// one shared, kind-agnostic list and admits each through the mode-selected
// local player visibility source at its own projected cell [03 §3.9]; a plot
// cell/footprint corner marked visible must not admit a feature whose actual
// X/Y/Z position (what the marker is drawn at) is not.
func TestRadarFeatureVisibleSamplesOwnPositionNotFootprintCorners(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	vis := visibility.New(terrain, visibility.ModeCurrentEnabled)
	const local = uint8(0)

	// Mark visible only the tile the plot cell's origin (CX, CZ) projects
	// into — the corner the old two-corner extents test sampled.
	cellTileIdx := func(cx, cz int32) int {
		w, _ := vis.GridDimensions()
		x := world.CellToWorld(cx)
		z := world.CellToWorld(cz)
		u := int32(int16(int64(x)>>16)) >> 5
		v := int32(int16(int64(z)>>16)) >> 5
		return int(v*w + u)
	}
	grid := vis.ByteGrid(visibility.PlayerID(local))
	grid[cellTileIdx(4, 4)] = 1

	// The feature's own position is far from that cell, in a tile that is
	// NOT marked visible.
	far := numeric.Fixed(500 << 16)
	f := frame.FeatureView{
		Owner: combat.NeutralSide, OwnerKnown: false,
		CX: 4, CZ: 4, FootX: 1, FootZ: 1,
		X: far, Y: 0, Z: far,
	}
	s := &Session{Vis: vis, LocalOwner: local, ViewingOwner: local}
	if radarFeatureVisible(s, f) {
		t.Fatal("feature admitted through a footprint-corner cell, not its own projected position [03 §3.9]")
	}

	// Now mark the feature's own position visible instead: it must admit.
	w, _ := vis.GridDimensions()
	u := int32(int16(int64(far)>>16)) >> 5
	vIdx := int32(int16(int64(far)>>16)) >> 5
	grid[int(vIdx*w+u)] = 1
	if !radarFeatureVisible(s, f) {
		t.Fatal("feature was not admitted once its own projected position became visible [03 §3.9]")
	}
}

// TestRadarFeatureVisibleOwnerLocalBypass locks the second pass's only
// bypass — owner-local identity — independent of LOS state [03 §3.9].
func TestRadarFeatureVisibleOwnerLocalBypass(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	vis := visibility.New(terrain, visibility.ModeCurrentEnabled)
	const local = uint8(3)
	s := &Session{Vis: vis, LocalOwner: local, ViewingOwner: local}

	notLocal := frame.FeatureView{Owner: 1, OwnerKnown: true}
	if radarFeatureVisible(s, notLocal) {
		t.Fatal("a foreign-owned, un-lit feature was admitted without an LOS or owner bypass")
	}
	owned := frame.FeatureView{Owner: local, OwnerKnown: true}
	if !radarFeatureVisible(s, owned) {
		t.Fatal("an owner-local feature did not bypass the LOS gate")
	}
}

// TestRadarPublishedFeatureIgnoresFriendlyContactStatusBits confirms the
// publisher's Visible bit for a feature/projectile record never derives from
// the 0x300 friendly-contact status pair: that pair is a term of the UNIT
// pass's blip gate only, and a feature's flag byte / a projectile's flags
// word do not carry it with that meaning [03 §3.9]. radarFeatureVisible takes
// no status argument at all, so this is a structural guarantee; the test
// pins it against a future signature change that might thread Status back
// in.
func TestRadarPublishedFeatureIgnoresFriendlyContactStatusBits(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	vis := visibility.New(terrain, visibility.ModeCurrentEnabled)
	const local = uint8(0)
	s := &Session{Vis: vis, LocalOwner: local, ViewingOwner: local}

	// Not visible, not owner-local: must not be admitted regardless of any
	// status-shaped field a caller might otherwise be tempted to pass through.
	notDrawn := frame.FeatureView{Owner: 2, OwnerKnown: true, Status: 0x300}
	if radarFeatureVisible(s, notDrawn) {
		t.Fatal("an un-lit, non-local feature was admitted [03 §3.9]")
	}
	// Owner-local: admitted regardless of status.
	drawn := frame.FeatureView{Owner: local, OwnerKnown: true, Status: 0x300}
	if !radarFeatureVisible(s, drawn) {
		t.Fatal("an owner-local feature was not admitted [03 §3.9]")
	}
}
