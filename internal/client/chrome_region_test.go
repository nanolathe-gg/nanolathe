package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Magnified chrome maps every framebuffer pixel of a virtual pixel back to it,
// and an identity region records no marker, so unmagnified chrome keeps its
// exact recording (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func TestChromeRegion(t *testing.T) {
	r := ChromeRegion{Scale: 3}
	for _, p := range [][4]int32{{0, 0, 0, 0}, {2, 2, 0, 0}, {3, 5, 1, 1}, {383, 1439, 127, 479}} {
		if x, y := r.ToVirtual(p[0], p[1]); x != p[2] || y != p[3] {
			t.Errorf("ToVirtual(%d,%d) = %d,%d, want %d,%d", p[0], p[1], x, y, p[2], p[3])
		}
	}
	if w, h := r.VirtualSize(3840, 2160); w != 1280 || h != 720 {
		t.Errorf("VirtualSize = %dx%d, want 1280x720", w, h)
	}
	s := ChromeRegion{Scale: 1, OffsetX: 128}
	if x, _ := s.ToVirtual(129, 0); x != 1 {
		t.Errorf("offset ToVirtual x = %d, want 1", x)
	}
	if w, _ := s.VirtualSize(2560, 1440); w != 2432 {
		t.Errorf("offset VirtualSize w = %d, want 2432", w)
	}

	c, err := New(Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.BeginChromeRegion(ChromeRegion{Scale: 1})
	c.EndChromeRegion()
	if n := len(c.list.WorldSpaces()); n != 0 {
		t.Fatalf("identity region recorded %d markers, want 0", n)
	}
	c.BeginChromeRegion(ChromeRegion{Scale: 2})
	if w, h := c.ChromeSize(); w != 320 || h != 240 {
		t.Fatalf("ChromeSize inside a 2x region = %dx%d, want 320x240", w, h)
	}
	c.EndChromeRegion()
	if w, h := c.ChromeSize(); w != 640 || h != 480 {
		t.Fatalf("ChromeSize after the region = %dx%d, want 640x480", w, h)
	}
	if n := len(c.list.WorldSpaces()); n != 2 {
		t.Fatalf("2x region recorded %d markers, want 2", n)
	}
}
