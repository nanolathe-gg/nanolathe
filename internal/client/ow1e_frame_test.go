package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestOW1F_ProjectileVisibleUsesPublishedGrid(t *testing.T) {
	vis := snapshot.VisibilityView{W: 2, H: 2, Valid: true, CoverageBytes: true, Visible: []uint8{0, 0, 0, 1}}
	fn := projectileVisible(vis, 0)
	if !fn(snapshot.ProjectileView{X: numeric.Fixed(32 << 16), Z: numeric.Fixed(48 << 16)}) {
		t.Fatal("visible projectile rejected")
	}
	if fn(snapshot.ProjectileView{X: numeric.Fixed(0), Z: numeric.Fixed(0)}) {
		t.Fatal("invisible projectile admitted")
	}
	// An invalid publication is fail-closed.
	fn2 := projectileVisible(snapshot.VisibilityView{}, 0)
	if fn2(snapshot.ProjectileView{}) {
		t.Fatal("invalid visibility must cull projectiles")
	}
}

func TestOW1F_ProjectileVisibleSelectsHistoryWordMask(t *testing.T) {
	vis := snapshot.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{1 << 2}}
	if !projectileVisible(vis, 2)(snapshot.ProjectileView{}) {
		t.Fatal("projectile in local history word cell was rejected")
	}
	if projectileVisible(vis, 1)(snapshot.ProjectileView{}) {
		t.Fatal("projectile admitted through another player's history bit")
	}
	if projectileVisible(snapshot.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{}}, 0)(snapshot.ProjectileView{}) {
		t.Fatal("missing history word mask must cull projectiles")
	}
	allVisible := snapshot.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{1 << 10}}
	if projectileVisible(allVisible, 10)(snapshot.ProjectileView{}) {
		t.Fatal("reserved player slot 10 must cull projectiles")
	}
}

func TestOW1F_ProjectileDispatchOptionsBeamNotSuppressed(t *testing.T) {
	opts := (&Client{}).projectileDispatchOptions()
	v := snapshot.ProjectileView{RenderType: render.RenderTypeBeam, PrimaryColor: 13, HasPrimaryColor: true}
	alwaysVisible := func(snapshot.ProjectileView) bool { return true }
	d, aborted := render.BuildProjectileDraws([]snapshot.ProjectileView{v}, 0, alwaysVisible, nil, opts)
	if aborted || len(d) != 1 || d[0].Kind != "beam" {
		t.Fatalf("beam dispatch with published color failed: draws=%+v aborted=%v", d, aborted)
	}
	if d[0].Color != 13 {
		t.Fatalf("beam color from palette row want 13 got %d", d[0].Color)
	}
}

func TestOW1E_EffectDrawOptionsNeverInventsArtwork(t *testing.T) {
	// effectDrawOptions returns nil ResolveFrame, so any graphic stays unresolved and is skipped, not invented.
	c := &Client{}
	opts := c.effectDrawOptions()
	if opts.ResolveFrame != nil {
		if _, ok := opts.ResolveFrame(snapshot.EffectView{Graphic: "explosion"}, 0); ok {
			t.Fatal("effect artwork should not be invented")
		}
	}
	if opts.LHTGeometry != nil {
		if _, _, ok := opts.LHTGeometry(snapshot.EffectView{Light: true}); ok {
			t.Fatal("LHT geometry should not be invented")
		}
	}
	if opts.TerrainCoverage == nil {
		t.Fatal("terrain coverage must be present for LHT halo [03 §4.3.1]")
	}
}

func TestOW1E_ProjectileRendersRegardlessOfUnitCount(t *testing.T) {
	// Compose with a frame that has projectiles but zero units must still
	// invoke DrawProjectileViews and produce non-zero indexed pixels.
	c, err := New(Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	c.cam = &camera.Camera{X: 0, Z: 0, ViewW: 64, ViewH: 64, MapW: 512, MapH: 512}
	// Minimal terrain for coverage.
	c.indexed = make([]uint8, 64*64)
	vis := snapshot.VisibilityView{W: 2, H: 2, Valid: true, CoverageBytes: true, Visible: []uint8{1, 1, 1, 1}}
	prev := &snapshot.Frame{Tick: 1, Visibility: vis, Projectiles: []snapshot.ProjectileView{{Handle: 1, X: numeric.Fixed(32 << 16), Z: numeric.Fixed(32 << 16), RenderType: render.RenderTypeBeam, PaletteRow: 210}}}
	cur := &snapshot.Frame{Tick: 2, Visibility: vis, Projectiles: []snapshot.ProjectileView{{Handle: 1, X: numeric.Fixed(33 << 16), Z: numeric.Fixed(33 << 16), RenderType: render.RenderTypeBeam, PaletteRow: 210}}}
	buf := &snapshot.Buffer{}
	buf.Publish(prev)
	buf.Publish(cur)
	c.buffer = buf
	// Compose at alpha 0.5 should draw beam strokes even though Units is empty.
	c.composeIndexed(0.5, prev, cur, true)
	// At least one pixel should be non-zero (beam stroke).
	nonZero := 0
	for _, p := range c.indexed {
		if p != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("projectile frame with zero units produced no pixels")
	}
}
