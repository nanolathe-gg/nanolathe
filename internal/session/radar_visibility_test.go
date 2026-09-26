package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The projectile contact pass samples the candidate's own projected position
// through the mode-selected visibility source [03 §3.9].
func TestRadarPointVisibleSamplesOwnPosition(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	vis := visibility.New(terrain, visibility.ModeCurrentEnabled)
	const local = uint8(0)
	s := &Session{Vis: vis, LocalOwner: local, ViewingOwner: local}
	grid := vis.ByteGrid(visibility.PlayerID(local))
	grid[0] = 1
	far := numeric.Fixed(500 << 16)
	if radarPointVisible(s, combat.NeutralSide, false, far, 0, far) {
		t.Fatal("projectile admitted through a visible cell away from its own position [03 §3.9]")
	}
	w, _ := vis.GridDimensions()
	cx := int32(int16(int64(far)>>16)) >> 5
	cz := int32(int16(int64(far)>>16)) >> 5
	grid[int(cz*w+cx)] = 1
	if !radarPointVisible(s, combat.NeutralSide, false, far, 0, far) {
		t.Fatal("projectile was not admitted at its visible position [03 §3.9]")
	}
}

// Owner-local identity is the projectile pass's only visibility bypass
// [03 §3.9]; an unresolved owner zero must not borrow that identity.
func TestRadarPointVisibleOwnerLocalBypass(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, Plot: make([]world.PlotCell, 64*64)}
	vis := visibility.New(terrain, visibility.ModeCurrentEnabled)
	for _, local := range []uint8{0, 3} {
		s := &Session{Vis: vis, LocalOwner: local, ViewingOwner: local}
		if radarPointVisible(s, 1, true, 0, 0, 0) {
			t.Fatal("foreign projectile admitted without LOS")
		}
		if !radarPointVisible(s, local, true, 0, 0, 0) {
			t.Fatal("owner-local projectile did not bypass LOS")
		}
		if radarPointVisible(s, local, false, 0, 0, 0) {
			t.Fatal("unresolved projectile owner bypassed LOS")
		}
	}
}
