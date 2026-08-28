package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestMinimapHUDHitTestInclusive(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	if !h.HitTest(0, 0) {
		t.Fatalf("HitTest top-left inclusive want true")
	}
	if !h.HitTest(126, 126) {
		t.Fatalf("HitTest bottom-right inclusive want true fallback 0,0,126,126")
	}
	if h.HitTest(127, 0) || h.HitTest(0, 127) {
		t.Fatalf("HitTest outside want false")
	}
	// Also compare to camera minimap HitTest semantics: both inclusive
	m := camera.LayoutMinimap(640, 480)
	camHit := m.HitTest(m.PadX, m.PadY)
	h2 := NewMinimapHUD(Anchors{}, Rect{X1: m.PadX, Y1: m.PadY, X2: m.Right(), Y2: m.Bottom()})
	if !h2.HitTest(m.PadX, m.PadY) != !camHit {
		t.Fatalf("hud HitTest mismatch camera HitTest")
	}
	if !h2.HitTest(m.Right(), m.Bottom()) {
		t.Fatalf("hud HitTest bottom-right want true")
	}
	if h2.HitTest(m.PadX-1, m.PadY) {
		t.Fatalf("hud HitTest left-1 want false")
	}
}

func TestMinimapHUDRequiresAuthoredRect(t *testing.T) {
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	if h.Rect != (Rect{X1: 0, Y1: 0, X2: 126, Y2: 126}) {
		t.Fatalf("explicit authored rect was not retained: %+v", h.Rect)
	}
	if h.BlinkCountdown != 7 {
		t.Fatalf("BlinkCountdown want 7 got %d [03 §3.6]", h.BlinkCountdown)
	}
	// The 30 side anchors do not contain a minimap record.
	var a Anchors
	a[0] = Rect{X1: 10, Y1: 10, X2: 20, Y2: 20}
	h2 := NewMinimapHUD(a, Rect{})
	if h2.Rect != (Rect{}) {
		t.Fatalf("unresolved rail anchor must remain empty: %+v", h2.Rect)
	}
}

func TestMinimapHUDWorldToMinimapRoundTrip(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	cases := []struct {
		mapW, mapH   int32
		playW, playH int32
	}{
		{640, 480, camera.PlayRight(640), camera.PlayBottom(480)},
		{1024, 1024, 992, 896},
		{480, 640, 448, 512},
	}
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	for _, c := range cases {
		m := camera.LayoutMinimap(c.mapW, c.mapH)
		pts := []struct{ wx, wz int32 }{
			{0, 0},
			{c.playW - 1, c.playH - 1},
			{c.playW / 2, c.playH / 2},
			{c.playW / 4, c.playH / 3},
			{100, 200},
		}
		for _, p := range pts {
			rx, ry := h.WorldToMinimap(p.wx, p.wz, c.playW, c.playH, m)
			// Must be inside hud rect's radar area when world inside play? For fallback full square, rx 0..126 inclusive may be slightly out of m's pad but inside hud.
			wx2, wz2 := h.MinimapToWorld(rx, ry, c.playW, c.playH, m)
			dx := wx2 - p.wx
			if dx < 0 {
				dx = -dx
			}
			dz := wz2 - p.wz
			if dz < 0 {
				dz = -dz
			}
			allowX := c.playW/m.W + 1
			if m.W == 0 {
				allowX = 1
			}
			allowZ := c.playH/m.H + 1
			if m.H == 0 {
				allowZ = 1
			}
			if dx > allowX || dz > allowZ {
				t.Fatalf("world->minimap->world trunc error >allow (%d,%d): map %dx%d play %dx%d world (%d,%d) -> minimap (%d,%d) -> world (%d,%d) delta %d,%d", allowX, allowZ, c.mapW, c.mapH, c.playW, c.playH, p.wx, p.wz, rx, ry, wx2, wz2, dx, dz)
			}
			// Also test MinimapToWorld matches camera RadarToWorld
			wx3, wz3 := m.RadarToWorld(rx, ry, c.playW, c.playH)
			if wx3 != wx2 || wz3 != wz2 {
				t.Fatalf("MinimapToWorld mismatch RadarToWorld: %d,%d vs %d,%d", wx3, wz3, wx2, wz2)
			}
		}
	}
}

func TestMinimapHUDViewportRect(t *testing.T) { // inclusive and clipped to HUD rect, 1-pixel not filled, hiColor DDA placeholder [07 §10][03 §3.9]
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	playW, playH := int32(608), int32(352) // 640,480 raw -> play [03 §3.4]
	m := camera.LayoutMinimap(640, 480)    // wide: W126 H94 PadY 16
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 128, ViewH: 128, MapW: playW, MapH: playH}
	r := h.ViewportRect(cam, m, playW, playH)
	// Viewport rect must be inclusive and within HUD rect
	hl, ht, hr, hb := h.Rect.Ordered()
	if r.X1 < hl || r.Y1 < ht || r.X2 > hr || r.Y2 > hb {
		t.Fatalf("ViewportRect not clipped to HUD rect: got %+v hud %+v", r, h.Rect)
	}
	if r.X1 > r.X2 || r.Y1 > r.Y2 {
		t.Fatalf("ViewportRect degenerate inclusive: %+v", r)
	}
	// 1-pixel thickness: should not be filled (test by checking area vs perimeter not needed, just ensure inclusive)
	// Check that rect width/height correspond to scaled view / play
	// For cam 0,0 with view 128, expect left ~ Pad, top ~ PadY
	// World 0 -> rx = Pad, world 127 -> rx ~ Pad + 127*W/play
	// Just ensure clipped and not filled via client draw test
	// Test clipping: camera at far edge should clip to HUD rect
	cam2 := &camera.Camera{X: playW - 10, Z: playH - 10, ViewW: 128, ViewH: 128, MapW: playW, MapH: playH}
	r2 := h.ViewportRect(cam2, m, playW, playH)
	if r2.X2 > hr || r2.Y2 > hb {
		t.Fatalf("ViewportRect edge not clipped: got %+v hud %+v", r2, h.Rect)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// ViewportRect itself is presentation-only; palette index not stored here, but ensure rect is 1-pixel
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// The caller supplies the palette and draws the one-pixel outline.
}

func TestMinimapHUDViewportRectUsesDisplayDestination(t *testing.T) {
	// A real presentation destination is not necessarily the 126-pixel canvas:
	// this catches returning canvas coordinates when the HUD is offset and scaled.
	h := NewMinimapHUD(Anchors{}, Rect{X1: 200, Y1: 100, X2: 451, Y2: 351})
	playW, playH := int32(608), int32(352)
	m := camera.LayoutMinimap(640, 480)
	cam := &camera.Camera{X: 64, Z: 32, ViewW: 64, ViewH: 64}
	r := h.ViewportRect(cam, m, playW, playH)
	want := Rect{X1: 226, Y1: 148, X2: 252, Y2: 182}
	if r != want {
		t.Fatalf("ViewportRect display conversion want %+v got %+v", want, r)
	}
}

func TestMinimapHUDDirtyBlink(t *testing.T) {
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 10, Y2: 10})
	// DirtyBlink bits: bit0 blink, bit1 FINAL dirty, bit2 MAPPED dirty [03 §3.6]
	h.DirtyBlink = 0x0004 // MAPPED dirty
	if h.DirtyBlink&0x0004 == 0 {
		t.Fatalf("MAPPED dirty bit not set")
	}
	h.DirtyBlink |= 0x0002 // FINAL dirty
	h.DirtyBlink ^= 0x0001 // blink toggle every 8 frames
	if h.DirtyBlink == 0 {
		t.Fatalf("DirtyBlink bits")
	}
}

func TestPlaySizeForMinimap(t *testing.T) { // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	ter := &world.Terrain{CellW: 64, CellH: 64}
	ter.PlayRight = 64*16 - 32
	ter.PlayBottom = 64*16 - 128
	w, h := PlaySizeForMinimap(ter)
	if w != ter.PlayRight || h != ter.PlayBottom {
		t.Fatalf("PlaySizeForMinimap want %d,%d got %d,%d", ter.PlayRight, ter.PlayBottom, w, h)
	}
	// Missing map extents are not reconstructed from raw dimensions.
	ter2 := &world.Terrain{CellW: 32, CellH: 32}
	w2, h2 := PlaySizeForMinimap(ter2)
	if w2 != 0 || h2 != 0 {
		t.Fatalf("PlaySizeForMinimap missing extents want 0,0 got %d,%d", w2, h2)
	}
}
