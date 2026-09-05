package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestProjectileSelectorArtResolvesAgainstStockFX locks the presentation half
// of the play-test report "no projectiles render when firing".
//
// Render type 4 draws the shared ground `shadow` frame and then frame
// `(now − creationTick) mod frameCount` of the `fx` entry the weapon's `color`
// byte selects — 0 `cannonshell`, 1 `plasmasm`, 2 `plasmamd`, 3 `ultrashell`,
// 4 `plasmasm` [06 R-WFX-01 §4]. It is the busiest family in stock content (82
// of the 198 weapons, every ballistic shell and the Peewee's `emg` among the
// guns), so a dispatch that cannot resolve it draws nothing for most of a
// battle. Two things had to be true and neither was: the committed view had to
// carry the selector, and the frame count of the selected sequence had to be
// resolvable at the draw boundary.
func TestProjectileSelectorArtResolvesAgainstStockFX(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	c := &Client{}
	c.SetModelFS(fs)
	opts := c.projectileDispatchOptions()

	// The Peewee's `emg`: rendertype 4, color 2, i.e. `plasmamd`.
	view := frame.ProjectileView{
		Handle:          1,
		RenderType:      render.RenderTypeSelectorGAF,
		Selector:        2,
		PrimaryColor:    2,
		HasPrimaryColor: true,
		CreationTick:    100,
	}
	frameCount, ok := opts.FrameCount(view)
	if !ok || frameCount <= 0 {
		t.Fatalf("selector 2 resolved no frame count (%d, ok=%v); the modulus of [06 R-WFX-01 §4] cannot be taken", frameCount, ok)
	}

	d := render.DispatchProjectileView(view, view.CreationTick+1, opts)
	if d.Suppressed {
		t.Fatal("a stock render-type-4 record was suppressed; every gun and shell in the game would be invisible")
	}
	if d.BaseFrame == nil {
		t.Fatal("no ground shadow frame: render types 1, 3, 4 and 6 all draw it [06 R-WFX-01 §4]")
	}
	if d.FrameAsset == nil {
		t.Fatal("no selector frame resolved for `plasmamd`")
	}
	if d.Frame != 1 {
		t.Fatalf("frame index %d, want (now − creationTick) mod frameCount = 1", d.Frame)
	}
	// The modulus wraps rather than running off the end of the sequence.
	wrapped := render.DispatchProjectileView(view, view.CreationTick+uint32(frameCount), opts)
	if wrapped.Suppressed || wrapped.Frame != 0 {
		t.Fatalf("frame index at one full sequence = %d (suppressed=%v), want the wrap to 0", wrapped.Frame, wrapped.Suppressed)
	}

	// The authored 255 is the −1 sentinel that suppresses the whole case; the
	// stock `earthquake` meteor is the weapon that authors it [06 R-WFX-01 §1].
	suppressed := view
	suppressed.Selector = -1
	if d := render.DispatchProjectileView(suppressed, 101, opts); !d.Suppressed {
		t.Fatal("selector −1 must suppress render type 4 entirely")
	}
	// 5..254 name no bound slot and draw nothing.
	outOfRange := view
	outOfRange.Selector = 7
	if d := render.DispatchProjectileView(outOfRange, 101, opts); !d.Suppressed {
		t.Fatal("a selector outside 0..4 resolves no bound entry and must draw nothing")
	}
}
