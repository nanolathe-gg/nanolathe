package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRadarMapPixelUsesSignedHighWord(t *testing.T) {
	tests := []struct {
		name  string
		fixed numeric.Fixed
		want  int32
	}{
		{name: "negative", fixed: numeric.Fixed(-3 << 16), want: -3},
		{name: "signed high-word wrap", fixed: numeric.Fixed(0x8001 << 16), want: -32767},
		{name: "fraction truncates", fixed: numeric.Fixed(-3<<16 + 0xffff), want: -3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := radarMapPixel(tc.fixed); got != tc.want {
				t.Fatalf("radarMapPixel(%d) = %d, want %d", tc.fixed, got, tc.want)
			}
		})
	}
}

func TestRadarGAFBlitCopiesIndexedPixelsAtAnchor(t *testing.T) {
	dst := &render.RadarSurface{W: 8, H: 8, Pitch: 8, Bits: make([]byte, 64)}
	f := &formats.GAFFrame{
		Width: 2, Height: 2, XOffset: 1, YOffset: 1,
		Pixels: []byte{3, 9, 4, 5}, Transparent: []bool{false, true, false, false},
	}
	blitRadarGAF(dst, 3, 4, f)
	if got, _ := dst.At(2, 3); got != 3 {
		t.Fatalf("radar GAF anchor pixel = %d, want 3", got)
	}
	if got, _ := dst.At(3, 3); got != 0 {
		t.Fatalf("transparent radar GAF pixel = %d, want untouched 0", got)
	}
	if got, _ := dst.At(2, 4); got != 4 {
		t.Fatalf("radar GAF lower-left pixel = %d, want 4", got)
	}
	if got, _ := dst.At(3, 4); got != 5 {
		t.Fatalf("radar GAF lower-right pixel = %d, want 5", got)
	}
}

func TestRadarRingOnlyContactAdmitsBlinkAndDoesNotNeedBlipArt(t *testing.T) {
	ringOnly := render.MinimapContact{
		Visible: true, LocalPlayer: 1, Owner: 1, MinimapMode: 1,
		RingEnabled: true, RingRange: 1024,
	}
	if !radarContactAdmitted(ringOnly, render.BlinkState{}) {
		t.Fatal("visible ring-only contact was not admitted")
	}
	ringOnly.BlinkSuppress = 1
	if radarContactAdmitted(ringOnly, render.BlinkState{Phase: 0}) {
		t.Fatal("blink-suppressed ring-only contact admitted on clear phase")
	}
	if !radarContactAdmitted(ringOnly, render.BlinkState{Phase: 1}) {
		t.Fatal("blink-suppressed ring-only contact rejected on blink phase")
	}
}

func TestRadarLetterboxRoutesOutsideFittedRect(t *testing.T) {
	m := camera.LayoutMinimap(640, 480)
	if m.PadY == 0 || m.H == camera.MinimapLongSide {
		t.Fatalf("wide layout did not produce vertical letterbox: %+v", m)
	}
	if m.HitTest(0, m.PadY-1) {
		t.Fatal("letterbox bar was accepted as fitted radar")
	}
	if !m.HitTest(m.PadX, m.PadY) || !m.HitTest(m.Right(), m.Bottom()) {
		t.Fatal("fitted radar rectangle lost an inclusive edge")
	}
}

func TestRadarPublishedContactVisibilityUsesOwnerBypass(t *testing.T) {
	c := frame.RadarContactView{Owner: 2, OwnerKnown: true}
	if radarPublishedContactVisible(c, 1) {
		t.Fatal("neutral contact became visible without published gate")
	}
	if !radarPublishedContactVisible(c, 2) {
		t.Fatal("owner-local contact did not bypass visibility gate")
	}
	c.Status = 0x100
	if !radarPublishedContactVisible(c, 1) {
		t.Fatal("friendly contact did not bypass visibility gate")
	}
}

func TestRadarPublishedOwnerZeroNeedsPublishedVisibility(t *testing.T) {
	contact := frame.RadarContactView{Owner: 0}
	if radarPublishedContactVisible(contact, 0) {
		t.Fatal("zero-valued unpublished contact bypassed visibility for local player zero")
	}
	contact.Visible = true
	if !radarPublishedContactVisible(contact, 0) {
		t.Fatal("published visible owner-zero contact was rejected")
	}
}

func TestRadarContactRangeGateRequiresSelectionAndActivation(t *testing.T) {
	c := frame.RadarContactView{Active: true}
	if radarContactRangeEnabled(c) {
		t.Fatal("unselected contact emitted selected-range circles")
	}
	c.Status = 0x10
	c.RangeStatus = true
	if !radarContactRangeEnabled(c) {
		t.Fatal("selected active contact did not emit selected-range circles")
	}
	c.Active = false
	c.OnOffable = true
	c.RangeStatus = false
	if radarContactRangeEnabled(c) {
		t.Fatal("inactive on/off-capable contact emitted selected-range circles")
	}
	c.OnOffable = false
	c.RangeStatus = true
	if !radarContactRangeEnabled(c) {
		t.Fatal("inactive non-toggle contact lost selected-range circles")
	}
}

func TestRadarProjectileAndFeatureStatusArtGate(t *testing.T) {
	c := frame.RadarContactView{Kind: frame.RadarContactProjectile}
	if !radarProjectileDot(c) {
		t.Fatal("clear projectile status did not select one-pixel dot")
	}
	for _, mask := range []uint32{1 << 29, 1 << 30, 0x40} {
		c.Status = mask
		if radarProjectileDot(c) {
			t.Fatalf("projectile status %#x incorrectly selected dot", mask)
		}
	}
	c.Kind = frame.RadarContactFeature
	c.Status = 0
	if radarProjectileDot(c) {
		t.Fatal("feature contact selected projectile dot")
	}
}

func TestRadarPublishedPaletteSelectsAuthoredFrame(t *testing.T) {
	h := &retailBattleHUD{}
	c := frame.RadarContactView{Owner: 1, Palette: 2}
	if got := h.radarOwnerFrameIndex(nil, c, 4); got != 2 {
		t.Fatalf("published palette selected frame %d, want 2", got)
	}
}

func TestRebuildRadarPublishedContactPixelsAndSelectedRange(t *testing.T) {
	const local = uint8(1)
	picture := &render.RadarSurface{W: 126, H: 126, Pitch: 128, Bits: make([]byte, 126*126)}
	for i := range picture.Bits {
		picture.Bits[i] = 1
	}
	h := &retailBattleHUD{
		radar: render.NewMinimapService(render.MinimapServiceConfig{
			Picture: picture, MapW: 1, MapH: 1, LocalSlot: local,
		}),
		radarBlipGAF: &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
			Width: 1, Height: 1, Pixels: []byte{23}, Transparent: []bool{false},
		}}}},
		radarFeatureGAF: &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
			Width: 1, Height: 1, Pixels: []byte{31}, Transparent: []bool{false},
		}}, {Frame: &formats.GAFFrame{
			Width: 1, Height: 1, Pixels: []byte{37}, Transparent: []bool{false},
		}}}},
	}
	b := &battleSession{sess: &session.Session{World: &world.Terrain{PlayRight: 126, PlayBottom: 126}}}
	cur := &frame.Frame{
		Selection:  frame.SelectionView{LocalPlayer: local},
		Visibility: frame.VisibilityView{W: 1, H: 1, Valid: true, WordVisible: []uint16{1 << local}, Visible: []uint8{1}},
		Radar: frame.RadarView{Contacts: []frame.RadarContactView{
			// Selected, active, non-toggle unit: its published authored range
			// produces a radar-colored circle and its authored blip pixel.
			{Kind: frame.RadarContactUnit, Owner: local, X: numeric.Fixed(32 << 16), Z: numeric.Fixed(63 << 16), Status: 0x10, RangeStatus: true, Active: true, RadarDistance: 8, Palette: 0, Visible: true},
			// Unselected unit retains only its blip; the selected-range circle
			// must not appear at x=88.
			{Kind: frame.RadarContactUnit, Owner: local, X: numeric.Fixed(80 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 0},
			{Kind: frame.RadarContactFeature, Owner: local, X: numeric.Fixed(96 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 1},
			{Kind: frame.RadarContactProjectile, Owner: local, X: numeric.Fixed(112 << 16), Z: numeric.Fixed(63 << 16), Visible: true},
		}},
	}
	final := h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if final == nil {
		t.Fatal("published radar contacts did not rebuild final surface")
	}
	if got, _ := final.At(32, 63); got != 23 {
		t.Fatalf("unit blip pixel = %d, want authored 23", got)
	}
	if got, _ := final.At(40, 63); got != 10 {
		t.Fatalf("selected range circle pixel = %d, want radar palette 10", got)
	}
	if got, _ := final.At(88, 63); got != 1 {
		t.Fatalf("unselected unit emitted range circle pixel %d, want mapped background 1", got)
	}
	if got, _ := final.At(96, 63); got != 37 {
		t.Fatalf("feature marker pixel = %d, want authored owner frame 37", got)
	}
	if got, _ := final.At(112, 63); got != 14 {
		t.Fatalf("projectile dot pixel = %d, want projectile palette 14", got)
	}
}
