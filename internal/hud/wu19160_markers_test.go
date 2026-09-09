package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

// The radar rectangle is compiled-in, not authored: the fitted radar inside the
// fixed 126-pixel canvas at the surface's top-left corner, inclusive on both
// edges [03 §3.7][07 R-HUD-05]. The inclusive edges are the easy silent
// regression — an exclusive right/bottom loses the last row and column of the
// picture and shifts every hit test by one.
func TestMinimapCanvasRectIsTheInclusiveFittedRadar(t *testing.T) {
	// A wide map letterboxes on Y: RadarW 126, pad only on Y.
	m := camera.LayoutMinimap(1000, 400)
	r := MinimapCanvasRect(m)
	if r.X1 != m.PadX || r.Y1 != m.PadY {
		t.Fatalf("origin = (%d,%d), want the letterbox pad (%d,%d)", r.X1, r.Y1, m.PadX, m.PadY)
	}
	if r.X2 != m.PadX+m.W-1 || r.Y2 != m.PadY+m.H-1 {
		t.Fatalf("far corner = (%d,%d), want inclusive (%d,%d)", r.X2, r.Y2, m.PadX+m.W-1, m.PadY+m.H-1)
	}
	if m.PadX != 0 || m.PadY <= 0 {
		t.Fatalf("wide map letterbox = padX %d padY %d, want the pad on Y alone", m.PadX, m.PadY)
	}
	// The canvas never leaves the 126-pixel square at the surface corner.
	if r.X1 < 0 || r.Y1 < 0 || r.X2 > camera.MinimapLongSide-1 || r.Y2 > camera.MinimapLongSide-1 {
		t.Fatalf("rect %+v leaves the %d-pixel canvas at the surface corner", r, camera.MinimapLongSide)
	}
	h := NewMinimapHUD(Anchors{}, r)
	if !h.HitTest(r.X2, r.Y2) || h.HitTest(r.X2+1, r.Y2) {
		t.Fatal("hit test is not inclusive on the far corner")
	}
}
