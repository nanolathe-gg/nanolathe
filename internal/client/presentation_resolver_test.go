package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestProjectileVisibilityUsesPublishedLocalCoverageAndShear(t *testing.T) {
	mask := make([]uint8, 4)
	mask[3] = 1 // u=1,row=1
	vis := frame.VisibilityView{W: 2, H: 2, Visible: mask, Valid: true}
	if !ProjectileVisible(vis, frame.ProjectileView{X: numeric.Fixed(32 << 16), Z: numeric.Fixed(48 << 16)}, 0, 0) {
		t.Fatal("projectile in published coverage cell was rejected")
	}
	if ProjectileVisible(vis, frame.ProjectileView{X: numeric.Fixed(31 << 16), Z: numeric.Fixed(48 << 16)}, 0, 0) {
		t.Fatal("projectile in uncovered cell was admitted")
	}
	if ProjectileVisible(vis, frame.ProjectileView{X: numeric.Fixed(-1)}, 0, 0) {
		t.Fatal("negative projected cell was admitted")
	}
	if ProjectileVisible(frame.VisibilityView{W: 2, H: 2, Visible: mask}, frame.ProjectileView{}, 0, 0) {
		t.Fatal("invalid visibility publication was admitted")
	}
}

func TestProjectileVisibleRejectsMalformedGridAndReservedPlayer(t *testing.T) {
	p := frame.ProjectileView{X: numeric.Fixed(32 << 16), Z: numeric.Fixed(48 << 16)}
	if ProjectileVisible(frame.VisibilityView{
		W: 2, H: 2, Visible: []uint8{0, 0, 0, 1, 1}, Valid: true,
	}, p, ProjectileVisibilityModeBytes, 0) {
		t.Fatal("oversized byte grid was admitted")
	}
	if ProjectileVisible(frame.VisibilityView{
		W: 2, H: 2, WordVisible: []uint16{0, 0, 0, 1}, Valid: true,
	}, p, 0, 10) {
		t.Fatal("reserved player slot was admitted")
	}
}

func TestTerrainScreenCoverageIsMapBounded(t *testing.T) {
	c := &Client{
		terrain: &world.Terrain{CellW: 2, CellH: 2},
		cam:     &camera.Camera{X: 8, Z: 4},
	}
	if !c.terrainScreenCoverage(0, 0) {
		t.Fatal("map pixel at camera origin should be terrain-covered")
	}
	if c.terrainScreenCoverage(-9, 0) {
		t.Fatal("pixel west of terrain was admitted")
	}
	if c.terrainScreenCoverage(32, 0) {
		t.Fatal("pixel beyond terrain width was admitted")
	}
}

func TestDrawProjectileViewsDropsEarlierInstructionsOnGlobalAbort(t *testing.T) {
	c := &Client{
		width:   8,
		height:  8,
		indexed: make([]uint8, 64),
		cam:     &camera.Camera{},
	}
	views := []frame.ProjectileView{
		{Handle: 1, RenderType: render.RenderTypeBeam},
		{Handle: 2, RenderType: render.RenderTypeGlobalGAF},
	}
	stats := c.DrawProjectileViews(
		views,
		1,
		nil,
		func(frame.ProjectileView) bool { return false },
		render.ProjectileDispatchOptions{
			Color: func(frame.ProjectileView) (int32, int32, bool) { return 7, 0, true },
		},
	)
	if !stats.Aborted || stats.Dispatched != 0 {
		t.Fatalf("global GAF abort leaked prior instructions: %+v", stats)
	}
	for i, px := range c.indexed {
		if px != 0 {
			t.Fatalf("aborted projectile renderer wrote pixel %d=%d", i, px)
		}
	}
}

func TestDrawEffectViewsAdmitsAuthoredLHTRowZero(t *testing.T) {
	c := &Client{
		width:   8,
		height:  8,
		indexed: make([]uint8, 64),
		cam:     &camera.Camera{},
		pal:     &palette.Tables{},
	}
	stats := c.DrawEffectViews([]frame.EffectView{{ID: 1, Light: true}}, EffectDrawOptions{
		LHTGeometry: func(frame.EffectView) (int, int, bool) { return 1, 0, true },
		TerrainCoverage: func(x, y int) bool {
			return x == 0 && y == 0
		},
	})
	if stats.Halos != 1 || stats.Skipped != 0 {
		t.Fatalf("authored LHT row 0 was rejected: %+v", stats)
	}
}
