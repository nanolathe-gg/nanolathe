package main

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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
	// The second pass's only bypass is owner-local identity. The
	// friendly-contact status pair 0x300 is a term of the UNIT pass's blip
	// gate only and must not admit a projectile/feature candidate here
	// [03 §3.9].
	c.Status = 0x300
	if radarPublishedContactVisible(c, 1) {
		t.Fatal("friendly-contact status bits incorrectly bypassed the second-pass visibility gate")
	}
	c.Visible = true
	if !radarPublishedContactVisible(c, 1) {
		t.Fatal("published-visible contact did not pass the second-pass gate")
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

// TestRadarBlipGateKeepsUndetectedEnemiesOff locks the consumer half of the
// minimap admission chain [03 §3.9]: an enemy contact whose committed status
// carries no bit of the 0x300 mask draws no blip, and the same contact with the
// seen marker set — the bit the sensor phase writes for a radar or
// line-of-sight admission [R-VIS-01 §4] — does. The producer half is locked in
// internal/session.
func TestRadarBlipGateKeepsUndetectedEnemiesOff(t *testing.T) {
	// MinimapMode 1 is the ordinary battle mode; the "all mapped" mode word and
	// the global show-all option bit are both off [03 §3.9].
	undetected := render.MinimapContact{Owner: 2, LocalPlayer: 1, MinimapMode: 1}
	if radarContactAdmitted(undetected, render.BlinkState{}) {
		t.Fatal("an enemy contact with no sensor or LOS admission drew a minimap blip [03 §3.9]")
	}
	fullRadar := undetected
	fullRadar.Options = radarAllContactsOption
	if !radarContactAdmitted(fullRadar, render.BlinkState{}) {
		t.Fatal("the full-radar option did not admit an otherwise hidden enemy contact [03 §3.9]")
	}

	detected := undetected
	detected.Status = uint32(visibility.SeenBit)
	detected.Visible = true
	if !radarContactAdmitted(detected, render.BlinkState{}) {
		t.Fatal("an enemy contact carrying the seen marker was not admitted [03 §3.9][R-VIS-01 §4]")
	}

	// The viewer's own units are admitted by owner identity regardless of any
	// sensor state [03 §3.9].
	ownUnit := render.MinimapContact{Owner: 1, LocalPlayer: 1, MinimapMode: 1}
	if !radarContactAdmitted(ownUnit, render.BlinkState{}) {
		t.Fatal("the viewing player's own unit was not admitted [03 §3.9]")
	}
}

func TestRadarSurvivesCompleteBattleHUDComposition(t *testing.T) {
	const radarPixel, panelPixel byte = 83, 17
	const playW, playH int32 = 512, 512

	buf := frame.NewBuffer()
	cur := buf.BeginWrite()
	cur.Selection.LocalPlayer = 0
	cur.Visibility = frame.VisibilityView{
		W: 1, H: 1, Valid: true, WordVisible: []uint16{1}, Visible: []uint8{1},
	}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}

	terrain := &world.Terrain{PlayRight: playW, PlayBottom: playH}
	sess := &session.Session{Snapshot: buf, World: terrain, LocalOwner: 0}
	h := &retailBattleHUD{
		side: &content.SideDef{},
		panelSide: &formats.GAFFrame{
			Width: 129, Height: 480, Pixels: make([]byte, 129*480), Transparent: make([]bool, 129*480),
		},
		radar: render.NewMinimapService(render.MinimapServiceConfig{
			Picture: &render.RadarSurface{W: 126, H: 126, Pitch: 128, Bits: bytes.Repeat([]byte{radarPixel}, 126*126)},
			MapW:    1, MapH: 1, LocalSlot: 0,
		}),
		minimapAnchor:   hud.Rect{X1: 0, Y1: 0, X2: 125, Y2: 125},
		minimapAnchorOK: true,
	}
	for i := range h.panelSide.Pixels {
		h.panelSide.Pixels[i] = panelPixel
	}
	b := &battleSession{
		sess: sess,
		cam:  &camera.Camera{X: 0, Z: 0, ViewW: playW, ViewH: playH, MapW: playW, MapH: playH},
		hud:  h,
	}
	c, err := client.New(client.Options{Buffer: buf, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	c.SetUIStage(battleHUDUIStage{hud: h, battle: b})

	for _, offset := range []int8{ui.PanelVisible, ui.PanelParked} {
		b.battleState().PanelOffset = offset
		img := c.ComposeFrame()
		for _, p := range [][2]int{{1, 1}, {63, 63}, {125, 125}} {
			got := img.RGBAAt(p[0], p[1]).R
			if got != radarPixel {
				t.Fatalf("panel offset %d erased radar at (%d,%d): got %d, want %d", offset, p[0], p[1], got, radarPixel)
			}
		}
		for _, p := range [][2]int{{126, 1}, {128, 63}, {126, 125}} {
			got := img.RGBAAt(p[0], p[1]).R
			if got != panelPixel {
				t.Fatalf("panel offset %d changed PANELSIDE bezel at (%d,%d): got %d, want %d", offset, p[0], p[1], got, panelPixel)
			}
		}
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

// TestRadarPublishedProjectileArtSelector locks layer 6's three branches. The
// selector is three WEAPON-DEFINITION flags resolved by the publisher — a
// `targetable` or `interceptor` weapon draws the `nuclogo` marker, a `noradar`
// weapon draws nothing at all, and every other weapon draws the 1×1 dot
// [03 §3.9] layer 6 [06 §11.3]. It is not a per-record status word, and the
// pass walks the projectile pool alone: no feature reaches it.
func TestRadarPublishedProjectileArtSelector(t *testing.T) {
	for _, tc := range []struct {
		name string
		art  frame.RadarProjectileArt
	}{
		{"ordinary weapon takes the dot", frame.RadarProjectileDot},
		{"targetable or interceptor takes the marker", frame.RadarProjectileMarker},
		{"noradar draws nothing", frame.RadarProjectileHidden},
	} {
		c := frame.RadarContactView{Kind: frame.RadarContactProjectile, RadarArt: tc.art}
		if got := radarPublishedProjectileArt(c); got != tc.art {
			t.Fatalf("%s: selector returned %d, want %d", tc.name, got, tc.art)
		}
	}
	// The old status-word reading would have taken the dot for any record with
	// bits 29/30 clear; the record's status is not a term any more.
	noisy := frame.RadarContactView{Kind: frame.RadarContactProjectile, Status: 1<<29 | 1<<30 | 0x40}
	if got := radarPublishedProjectileArt(noisy); got != frame.RadarProjectileDot {
		t.Fatalf("record status still steers the selector: got %d, want the dot", got)
	}
	for _, kind := range []frame.RadarContactKind{frame.RadarContactUnit, frame.RadarContactFeature} {
		c := frame.RadarContactView{Kind: kind, RadarArt: frame.RadarProjectileMarker}
		if got := radarPublishedProjectileArt(c); got != frame.RadarProjectileHidden {
			t.Fatalf("kind %d reached the projectile pass: got %d", kind, got)
		}
	}
}

func TestRadarPublishedPaletteSelectsAuthoredFrame(t *testing.T) {
	h := &retailBattleHUD{}
	c := frame.RadarContactView{Owner: 1, Palette: 2, PaletteKnown: true}
	if got := h.radarOwnerFrameIndex(c, 4); got != 2 {
		t.Fatalf("published palette selected frame %d, want 2", got)
	}
	ownerZero := frame.RadarContactView{Owner: 0, Palette: 0, PaletteKnown: true}
	if got := h.radarOwnerFrameIndex(ownerZero, 4); got != 0 {
		t.Fatalf("known owner-zero palette selected frame %d, want 0", got)
	}
	unknown := frame.RadarContactView{Owner: 1, Palette: 2}
	if got := h.radarOwnerFrameIndex(unknown, 4); got != -1 {
		t.Fatalf("unknown palette selected frame %d, want suppressed", got)
	}
	neutral := frame.RadarContactView{Owner: 10, PaletteKnown: false}
	if got := h.radarOwnerFrameIndex(neutral, 4); got != -1 {
		t.Fatalf("neutral palette selected frame %d, want suppressed", got)
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
		radarMarkerGAF: &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
			Width: 1, Height: 1, Pixels: []byte{31}, Transparent: []bool{false},
		}}, {Frame: &formats.GAFFrame{
			Width: 1, Height: 1, Pixels: []byte{37}, Transparent: []bool{false},
		}}}},
	}
	b := &battleSession{sess: &session.Session{World: &world.Terrain{PlayRight: 126, PlayBottom: 126}}, radarOptions: radarAllContactsOption}
	cur := &frame.Frame{
		Tick:          17,
		ViewingPlayer: local,
		Selection:     frame.SelectionView{LocalPlayer: local},
		Visibility:    frame.VisibilityView{W: 1, H: 1, Valid: true, MappingSource: 1, MappingVersion: 1, WordVisible: []uint16{1 << local}, Visible: []uint8{1}},
		Radar: frame.RadarView{MappingLOS: 3, BlinkPhase: 1, Contacts: []frame.RadarContactView{
			// Selected, active, non-toggle unit: its published authored range
			// produces a radar-colored circle and its authored blip pixel.
			{Kind: frame.RadarContactUnit, Owner: local, X: numeric.Fixed(32 << 16), Z: numeric.Fixed(63 << 16), Status: 0x10, RangeStatus: true, Active: true, RadarDistance: 8, Palette: 0, PaletteKnown: true, Visible: true, Rings: []frame.RadarRingView{{}, {}}},
			// Unselected unit retains only its blip; the selected-range circle
			// must not appear at x=88.
			{Kind: frame.RadarContactUnit, Owner: local, X: numeric.Fixed(80 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 0, PaletteKnown: true},
			// A feature never reaches the projectile pass, whatever its
			// owner art would have selected [03 §3.9] layer 6.
			{Kind: frame.RadarContactFeature, Owner: local, X: numeric.Fixed(96 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 1, PaletteKnown: true},
			// The three projectile branches: dot, `nuclogo` marker in the
			// owner's frame, and `noradar` drawing nothing.
			{Kind: frame.RadarContactProjectile, Owner: local, X: numeric.Fixed(112 << 16), Z: numeric.Fixed(63 << 16), Visible: true},
			{Kind: frame.RadarContactProjectile, Owner: local, X: numeric.Fixed(120 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 1, PaletteKnown: true, RadarArt: frame.RadarProjectileMarker},
			{Kind: frame.RadarContactProjectile, Owner: local, X: numeric.Fixed(8 << 16), Z: numeric.Fixed(63 << 16), Visible: true, RadarArt: frame.RadarProjectileHidden},
		}},
	}
	final := h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if final == nil {
		t.Fatal("published radar contacts did not rebuild final surface")
	}
	if len(h.radarContacts) < 2 || h.radarContacts[0].Options != radarAllContactsOption || h.radarContacts[1].Options != radarAllContactsOption {
		t.Fatalf("full-radar option was not copied to primary and extra-ring contacts: %+v", h.radarContacts)
	}
	if h.radarContacts[0].MinimapMode != 3 || h.radarContacts[1].MinimapMode != 3 {
		t.Fatal("committed visibility mask did not reach primary and extra-ring contacts")
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
	if got, _ := final.At(96, 63); got != 1 {
		t.Fatalf("feature drew %d on the projectile pass, want mapped background 1", got)
	}
	if got, _ := final.At(112, 63); got != 14 {
		t.Fatalf("projectile dot pixel = %d, want projectile palette 14", got)
	}
	if got, _ := final.At(120, 63); got != 37 {
		t.Fatalf("targetable/interceptor marker pixel = %d, want authored owner frame 37", got)
	}
	if got, _ := final.At(8, 63); got != 1 {
		t.Fatalf("noradar projectile drew %d, want mapped background 1", got)
	}
	first := append([]byte(nil), final.Bits...)
	revision := h.radar.FinalRevision()
	second := h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if second == nil || !bytes.Equal(second.Bits, first) {
		t.Fatal("rebuilding one committed frame changed radar output")
	}
	if h.radar.FinalRevision() != revision {
		t.Fatal("rebuilding one committed frame refreshed FINAL")
	}
	cur.Tick++
	cur.Radar.Contacts[0].X = numeric.Fixed(64 << 16)
	third := h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if third == nil || h.radar.FinalRevision() <= revision || bytes.Equal(third.Bits, first) {
		t.Fatal("a new committed contact frame did not refresh FINAL")
	}
	if got := h.radar.Blink().Phase; got != cur.Radar.BlinkPhase {
		t.Fatalf("rebuild phase = %d, want committed %d", got, cur.Radar.BlinkPhase)
	}
}

// TestRebuildRadarHoverRingFollowsPointer locks layer 3 of the contacts pass:
// the `radlogohigh` ring is drawn on the contact whose identity equals the
// host's hovered-unit word — the pointer record's unit word that the footer's
// second source also reads [03 §3.9][07 R-HUD-03 §1]. It is not a commander
// marker: a seen ENEMY commander must draw its ordinary blip and nothing more,
// or the minimap leaks an identity the player has not earned.
func TestRebuildRadarHoverRingFollowsPointer(t *testing.T) {
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
		radarHoverGAF: &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: &formats.GAFFrame{
			Width: 1, Height: 1, Pixels: []byte{47}, Transparent: []bool{false},
		}}}},
	}
	b := &battleSession{
		sess:         &session.Session{World: &world.Terrain{PlayRight: 126, PlayBottom: 126}},
		radarOptions: radarAllContactsOption,
	}
	cur := &frame.Frame{
		Tick:          11,
		ViewingPlayer: local,
		Selection:     frame.SelectionView{LocalPlayer: local},
		Visibility:    frame.VisibilityView{W: 1, H: 1, Valid: true, MappingSource: 1, MappingVersion: 1, WordVisible: []uint16{1 << local}, Visible: []uint8{1}},
		Radar: frame.RadarView{MappingLOS: 3, BlinkPhase: 1, Contacts: []frame.RadarContactView{
			// The hovered unit.
			{Kind: frame.RadarContactUnit, Handle: 5, Owner: local, X: numeric.Fixed(32 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 0, PaletteKnown: true},
			// A seen enemy unit — a commander in every respect the frame still
			// carries. It gets a blip and no ring.
			{Kind: frame.RadarContactUnit, Handle: 9, Owner: 2, X: numeric.Fixed(80 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 0, PaletteKnown: true},
			// The null handle is the pointer record's "no unit hovered" value.
			{Kind: frame.RadarContactUnit, Handle: 0, Owner: local, X: numeric.Fixed(112 << 16), Z: numeric.Fixed(63 << 16), Visible: true, Palette: 0, PaletteKnown: true},
		}},
	}
	b.footerHoverUnit = 5
	final := h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if final == nil {
		t.Fatal("published radar contacts did not rebuild final surface")
	}
	if got, _ := final.At(32, 63); got != 47 {
		t.Fatalf("hovered contact pixel = %d, want authored hover ring 47", got)
	}
	if got, _ := final.At(80, 63); got != 23 {
		t.Fatalf("enemy contact pixel = %d, want ordinary blip 23 with no ring", got)
	}
	if got, _ := final.At(112, 63); got != 23 {
		t.Fatalf("null-handle contact pixel = %d, want ordinary blip 23", got)
	}
	// With nothing hovered the ring disappears entirely; the null handle must
	// not match the null hover word.
	b.footerHoverUnit = 0
	cur.Tick++
	final = h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if final == nil {
		t.Fatal("second rebuild produced no surface")
	}
	for _, x := range []int{32, 80, 112} {
		if got, _ := final.At(x, 63); got != 23 {
			t.Fatalf("with no unit hovered, contact at x=%d drew %d, want ordinary blip 23", x, got)
		}
	}
	// A hovered unit that the contact gate does not admit draws no ring: the
	// ring still obeys the blip's own admission [03 §3.9].
	b.footerHoverUnit = 9
	b.radarOptions = 0
	cur.Tick++
	cur.Radar.Contacts[1].Visible = false
	final = h.rebuildRadar(b, cur, camera.Minimap{W: 126, H: 126})
	if final == nil {
		t.Fatal("third rebuild produced no surface")
	}
	if got, _ := final.At(80, 63); got != 1 {
		t.Fatalf("unadmitted hovered contact drew %d, want mapped background 1", got)
	}
}
