package gui

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestBuildSliderArrowCrossExtent locks the arrow gadgets' cross-axis size —
// a horizontal bar's arrow height, a vertical bar's arrow width — to
// art.ArrowCrossExtent rather than to the bar's own short axis (BaseExtent).
// A static trace of the arrow gadget's rectangle store found the builder
// writes each arrow's full rectangle straight from that arrow's own SLIDERS
// frame (base+6 for the decrement arrow, base+8 for the increment one),
// independently of the bar's BaseExtent [07 R-WGT-01 §5]. Distinct
// BaseExtent and ArrowCrossExtent values below would surface a regression to
// the old "reuse the bar's short axis" reading, which this test would catch
// as a mismatch.
func TestBuildSliderArrowCrossExtent(t *testing.T) {
	art := &SliderArt{BaseExtent: 20, KnobExtent: 9, ArrowExtent: 12, ArrowCrossExtent: 14}

	hbar := Gadget{Kind: KindScrollBar, Active: 1, Rect: Rect{X: 100, Y: 40, W: 200, H: 16}}
	_, harrows := BuildSlider(hbar, art)
	for i, a := range harrows {
		if a.Rect.H != 14 {
			t.Fatalf("horizontal arrow %d height = %d want ArrowCrossExtent 14, not BaseExtent 20 [07 R-WGT-01 §5]", i, a.Rect.H)
		}
		if a.Rect.W != 12 {
			t.Fatalf("horizontal arrow %d width = %d want ArrowExtent 12 [07 R-WGT-01 §5]", i, a.Rect.W)
		}
	}

	vbar := Gadget{Kind: KindScrollBar, Active: 1, Rect: Rect{X: 20, Y: 30, W: 16, H: 100}}
	_, varrows := BuildSlider(vbar, art)
	for i, a := range varrows {
		if a.Rect.W != 14 {
			t.Fatalf("vertical arrow %d width = %d want ArrowCrossExtent 14, not BaseExtent 20 [07 R-WGT-01 §5]", i, a.Rect.W)
		}
		if a.Rect.H != 12 {
			t.Fatalf("vertical arrow %d height = %d want ArrowExtent 12 [07 R-WGT-01 §5]", i, a.Rect.H)
		}
	}
}

// TestRetailSliderArrowFramesMatchBaseExtent corroborates the trace above
// against the shipped asset: in anims/commongui.gaf's SLIDERS entry, frames
// base+6 and base+8 (the two arrows) carry the same size on their cross axis
// as frame base itself (the bar's BaseExtent), in both orientations. This is
// an asset property, not a code contract — the builder still reads the
// arrows' cross extent from their own frames, independently of BaseExtent
// [07 R-WGT-01 §5] — but it is why the two happen to coincide in retail play.
// Retail assets are opt-in (see internal/testsupport.RetailRoot); this test
// skips unless $NANOLATHE_RETAIL_ASSETS (or the legacy $NANOLATHE_TA_ROOT) is
// set.
func TestRetailSliderArrowFramesMatchBaseExtent(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	defer fs.Close()

	data, err := fs.ReadFile("anims/commongui.gaf")
	if err != nil {
		t.Fatalf("read commongui.gaf: %v", err)
	}
	gaf, err := formats.LoadGAF(data)
	if err != nil {
		t.Fatalf("parse commongui.gaf: %v", err)
	}
	entry, ok := gaf.Find("SLIDERS")
	if !ok {
		t.Fatal("commongui.gaf has no SLIDERS entry")
	}
	frame := func(i int32) *formats.GAFFrame {
		if int(i) >= len(entry.Frames) || entry.Frames[i].Frame == nil {
			t.Fatalf("SLIDERS frame %d missing", i)
		}
		return entry.Frames[i].Frame
	}

	for _, base := range []int32{0, 10} {
		horizontal := base == 10
		baseFrame, dec, inc := frame(base), frame(base+6), frame(base+8)
		var baseExtent, decCross, incCross uint16
		if horizontal {
			baseExtent, decCross, incCross = baseFrame.Height, dec.Height, inc.Height
		} else {
			baseExtent, decCross, incCross = baseFrame.Width, dec.Width, inc.Width
		}
		if decCross != baseExtent || incCross != baseExtent {
			t.Fatalf("base %d: arrow cross extents dec=%d inc=%d want BaseExtent %d", base, decCross, incCross, baseExtent)
		}
	}
}
