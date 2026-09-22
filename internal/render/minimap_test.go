package render

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestMinimapRadarSurfaceAtSet(t *testing.T) {
	r := &RadarSurface{W: 2, H: 2, Pitch: (2 + 3) &^ 3, Bits: make([]byte, 4)}
	if !r.Set(0, 0, 0xAA) {
		t.Fatalf("Set failed")
	}
	if v, ok := r.At(0, 0); !ok || v != 0xAA {
		t.Fatalf("At got %v %v want 0xAA true", v, ok)
	}
	if r.Set(2, 0, 1) {
		t.Fatalf("Set OOB should fail")
	}
	if _, ok := r.At(-1, 0); ok {
		t.Fatalf("At OOB should fail")
	}
	if r.Pitch != 4 {
		t.Fatalf("Pitch want 4 got %d", r.Pitch)
	}
}

func TestMinimapLayoutLetterboxWideTallSquare(t *testing.T) {
	// Square: mapW==mapH -> RadarW=126, RadarH=126, Origin 0,0 [07 §10][03 §3.6]
	m := camera.LayoutMinimap(100, 100)
	if m.W != 126 || m.H != 126 || m.PadX != 0 || m.PadY != 0 {
		t.Fatalf("square got %+v want W=126 H=126 pad 0,0", m)
	}
	// Wide: mapW>mapH -> RadarW=126, RadarH= floor(mapH*126/mapW) [07 §10]
	m = camera.LayoutMinimap(200, 100)
	if m.W != 126 {
		t.Fatalf("wide W want 126 got %d", m.W)
	}
	wantH := int32(100 * 126 / 200)
	if m.H != wantH {
		t.Fatalf("wide H want %d got %d", wantH, m.H)
	}
	if m.PadX != 0 {
		t.Fatalf("wide PadX want 0 got %d", m.PadX)
	}
	wantPadY := (126 - wantH) / 2
	if m.PadY != wantPadY {
		t.Fatalf("wide PadY want %d got %d", wantPadY, m.PadY)
	}
	// Tall: mapW<mapH
	m = camera.LayoutMinimap(100, 200)
	wantW := int32(100 * 126 / 200)
	if m.W != wantW || m.H != 126 || m.PadY != 0 {
		t.Fatalf("tall got %+v want W=%d H=126 padY 0", m, wantW)
	}
	wantPadX := (126 - wantW) / 2
	if m.PadX != wantPadX {
		t.Fatalf("tall PadX want %d got %d", wantPadX, m.PadX)
	}
	// Letterbox pitch check
	r := &RadarSurface{W: int(m.W), H: int(m.H), Pitch: (int(m.W) + 3) &^ 3}
	if r.Pitch != (int(m.W)+3)&^3 {
		t.Fatalf("pitch")
	}
}

func TestMinimapRadarProjectionHalfShear(t *testing.T) {
	// rx=worldX*RadarW/PlayRight, ry=(worldZ - (worldY>>1))*RadarH/PlayBottom SAR 1 [03 §3.9]
	m := camera.Minimap{W: 126, H: 63, PadX: 3, PadY: 31} // arbitrary
	playW := int32(1000)
	playH := int32(1000)
	// without Y
	rx, ry := RadarProjection(500, 400, 0, playW, playH, m)
	wantRx := int32(500 * 126 / 1000)
	wantRy := int32(400 * 63 / 1000)
	if rx != wantRx || ry != wantRy {
		t.Fatalf("proj without Y got %d,%d want %d,%d", rx, ry, wantRx, wantRy)
	}
	// with Y shear: worldY=100 -> 100>>1 =50, so Z effective 350
	rx2, ry2 := RadarProjection(500, 400, 100, playW, playH, m)
	wantRy2 := int32((400 - (100 >> 1)) * 63 / 1000)
	if rx2 != wantRx || ry2 != wantRy2 {
		t.Fatalf("proj with Y shear got %d,%d want %d,%d", rx2, ry2, wantRx, wantRy2)
	}
	// negative Y shear arithmetic
	rx3, ry3 := RadarProjection(0, 0, -2, playW, playH, m)
	// -2>>1 = -1, so 0 - (-1)=1 -> 1*63/1000 =0 trunc
	if ry3 != int32((0-(-2>>1))*63/1000) {
		t.Fatalf("negative shear ry got %d want %d", ry3, (0-(-2>>1))*63/1000)
	}
	_ = rx3
}

func TestMinimapRadarRadius(t *testing.T) {
	// Radar*dist/Play trunc [03 §3.10][07 §10]
	if got := RadarRadius(100, 126, 1000); got != 12 {
		t.Fatalf("radius 100*126/1000 want 12 got %d", got)
	}
	if got := RadarRadius(0, 126, 1000); got != 0 {
		t.Fatalf("zero dist want 0 got %d", got)
	}
	if got := RadarRadius(100, 0, 1000); got != 0 {
		t.Fatalf("zero radarSize want 0")
	}
	if got := RadarRadius(100, 126, 0); got != 0 {
		t.Fatalf("zero playSize want 0")
	}
}

func TestMinimapBuildRadarPictureLetterboxAndGuard(t *testing.T) {
	// Terrain 4x4 cells -> TileW=2 TileH=2, Wpix=64 Hpix=64, PlayRight=32 PlayBottom=-64? Use positive via 16*Cell but ensure positive.
	// Use CellW=4 CellH=8 to get positive PlayBottom.
	ter := &world.Terrain{
		CellW:      8,
		CellH:      8,
		PlayRight:  8*16 - 32,  // 96
		PlayBottom: 8*16 - 128, // 0? Actually 128-128=0, need larger: CellH=16 => 256-128=128
	}
	ter.CellH = 16
	ter.PlayBottom = 16*16 - 128 // 128
	ter.CellW = 8
	ter.PlayRight = 8*16 - 32   // 96
	tileW := int(ter.CellW / 2) // 4
	tileH := int(ter.CellH / 2) // 8
	ter.TileIndices = make([]uint16, tileW*tileH)
	for i := range ter.TileIndices {
		ter.TileIndices[i] = 0
	}
	// TileSet with 2 tiles: tile0 all 0x11, tile1 all 0x22
	ter.TileSet = make([][1024]byte, 2)
	for i := 0; i < 1024; i++ {
		ter.TileSet[0][i] = 0x11
		ter.TileSet[1][i] = 0x22
	}
	// Guard index >=TileCount ->0: set one index to 5 (>=2) should sample tile0
	ter.TileIndices[0] = 5
	m := camera.LayoutMinimap(int32(ter.CellW*16), int32(ter.CellH*16))
	// m.W/H derived from raw Wpix/Hpix? Layout uses mapW/mapH as Wpix/Hpix? But our Build uses playW/playH for sampling; m is just geometry.
	// Ensure m has positive.
	if m.W <= 0 || m.H <= 0 {
		t.Fatalf("layout got %+v", m)
	}
	tables := identityALP()
	pic := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, nil, 0, 0, &tables)
	if pic == nil {
		t.Fatalf("pic nil")
	}
	if pic.W != int(m.W) || pic.H != int(m.H) {
		t.Fatalf("pic size %d,%d want %d,%d", pic.W, pic.H, m.W, m.H)
	}
	if pic.Pitch != (pic.W+3)&^3 {
		t.Fatalf("pitch want %d got %d", (pic.W+3)&^3, pic.Pitch)
	}
	if len(pic.Bits) != pic.W*pic.H {
		t.Fatalf("bits len %d want %d", len(pic.Bits), pic.W*pic.H)
	}
	// Due to supersampling with the established out-of-range tile-index guard,
	// first pixel should be 0x11 [03 §3.7].
	if pic.Bits[0] != 0x11 {
		t.Fatalf("guard idx>=TileCount→0 failed, got %02x want 0x11 [03 §3.7]", pic.Bits[0])
	}
	// Authored TNT dimensions are independent of the lens dimensions; the ALP
	// path accepts arbitrary source sizes, including the observed tall variant
	// [fmt tnt][03 §3.7].
	if pic2 := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, make([]byte, 4*4), 4, 4, &tables); pic2 == nil || len(pic2.Bits) != pic2.W*pic2.H {
		t.Fatalf("arbitrary baked scaling must produce a complete picture")
	}
}

func TestMinimapBuildRadarPictureFromTerrain(t *testing.T) {
	ter := &world.Terrain{CellW: 8, CellH: 16, PlayRight: 96, PlayBottom: 128}
	tileW := int(ter.CellW / 2)
	tileH := int(ter.CellH / 2)
	ter.TileIndices = make([]uint16, tileW*tileH)
	ter.TileSet = make([][1024]byte, 1)
	for i := range ter.TileSet[0] {
		ter.TileSet[0][i] = 0x33
	}
	m := camera.LayoutMinimap(ter.CellW*16, ter.CellH*16)
	tables := identityALP()
	if pic := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, nil, 0, 0, &tables); pic == nil || pic.W != int(m.W) {
		t.Fatalf("terrain-derived picture failed %+v", pic)
	}
	// An absent terrain is suppressed rather than sampled.
	if pic2 := BuildRadarPicture(nil, 0, 0, m, nil, 0, 0, &tables); pic2 != nil {
		t.Fatalf("nil terrain must be suppressed")
	}
}

func TestMinimapBuildRadarPictureALPBlend(t *testing.T) {
	// Asymmetric entries lock the established row-first, two-level order
	// [03 §3.7].
	var tables palette.Tables
	tables.Alpha[1*256+2] = 10
	tables.Alpha[3*256+4] = 20
	tables.Alpha[10*256+20] = 30
	baked := []byte{1, 2, 3, 4}
	pic := BuildRadarPicture(nil, 1, 1, camera.Minimap{W: 1, H: 1}, baked, 2, 2, &tables)
	if pic == nil || len(pic.Bits) != 1 || pic.Bits[0] != 30 {
		t.Fatalf("row-first ALP result got %#v want 30", pic)
	}
}

func TestMinimapBakedALPArbitraryRetailDimensions(t *testing.T) {
	// The authored 252×252 and 252×256 sources both feed the 126×126 lens.
	// ALP is deliberately non-identity so the expected values lock source
	// coordinate truncation and the row-first three-look-up arithmetic [03 §3.7].
	var tables palette.Tables
	for i := 0; i < 256; i++ {
		for j := 0; j < 256; j++ {
			tables.Alpha[i*256+j] = byte((i + j) & 255)
		}
	}
	makeSource := func(w, h int) []byte {
		src := make([]byte, w*h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				src[y*w+x] = byte((x + 3*y) & 255)
			}
		}
		return src
	}
	for _, tc := range []struct {
		name       string
		w, h       int
		wantCenter byte
		wantBottom byte
	}{
		{name: "square", w: 252, h: 252, wantCenter: 40, wantBottom: 248},
		{name: "tall", w: 252, h: 256, wantCenter: 64, wantBottom: 28},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pic := BuildRadarPicture(nil, 0, 0, camera.Minimap{W: 126, H: 126}, makeSource(tc.w, tc.h), tc.w, tc.h, &tables)
			if pic == nil {
				t.Fatal("baked picture rejected")
			}
			center := 63*pic.W + 7
			bottom := 125*pic.W + 7
			if pic.Bits[center] != tc.wantCenter || pic.Bits[bottom] != tc.wantBottom {
				t.Fatalf("pixels at (7,63)/(7,125) got %d/%d want %d/%d", pic.Bits[center], pic.Bits[bottom], tc.wantCenter, tc.wantBottom)
			}
		})
	}
}

func identityALP() palette.Tables {
	var tables palette.Tables
	for i := 0; i < 256; i++ {
		for j := 0; j < 256; j++ {
			tables.Alpha[i*256+j] = byte(i)
		}
	}
	return tables
}

func TestMinimapBuildMappedVisIdxAndGateOrder(t *testing.T) {
	// picture 2x2, mapW=2 mapH=2
	pic := &RadarSurface{W: 2, H: 2, Pitch: 4, Bits: []byte{10, 20, 30, 40}}
	// wordMask TileW*TileH =4, bit for localSlot 0
	wordMask := []uint16{1, 0, 1, 1} // index0 visible, 1 unexplored, 2 visible,3 visible
	byteGrid := []uint8{1, 1, 0, 1}  // index2 fogged explored (byte 0)
	dcb := byte(0xFF)
	guiRemap := make([]byte, 256)
	for i := 0; i < 256; i++ {
		guiRemap[i] = byte(255 - i) // invert
	}
	mapped := buildMappedInto(nil, pic, wordMask, byteGrid, 2, 2, 0, dcb, guiRemap)
	if mapped == nil {
		t.Fatalf("mapped nil")
	}
	// visIdx = (y*mapH/h)*mapW + (x*mapW/w) TRUNC [03 §3.8]
	// y=0 x=0 -> visIdx (0*2/2)*2 +0*2/2=0 -> word bit1 true, byte1 -> raw 10
	if got := mapped.Bits[0]; got != 10 {
		t.Fatalf("visIdx 0,0 want raw 10 got %d", got)
	}
	// y=0 x=1 -> visIdx (0)*2+1=1 -> word 0 -> DCB
	if got := mapped.Bits[1]; got != dcb {
		t.Fatalf("visIdx 0,1 unexplored want DCB %d got %d", dcb, got)
	}
	// y=1 x=0 -> visIdx (1*2/2)*2+0=2 -> word1 byte0 -> remap 30->225
	if got := mapped.Bits[2]; got != guiRemap[30] {
		t.Fatalf("visIdx 1,0 fogged explored want remap %d got %d", guiRemap[30], got)
	}
	// y=1 x=1 -> visIdx 3 -> raw 40
	if got := mapped.Bits[3]; got != 40 {
		t.Fatalf("visIdx 1,1 want 40 got %d", got)
	}
	// Test nil remap -> no remap, use raw even when byte==0 but word set
	mapped2 := buildMappedInto(nil, pic, wordMask, byteGrid, 2, 2, 0, dcb, nil)
	if mapped2.Bits[2] != 30 {
		t.Fatalf("nil guiRemap should use raw, got %d want 30", mapped2.Bits[2])
	}
	// Test word bit 0 → DCB regardless of byte
	wordMaskAllZero := []uint16{0, 0, 0, 0}
	mapped3 := buildMappedInto(nil, pic, wordMaskAllZero, byteGrid, 2, 2, 0, dcb, guiRemap)
	for i, v := range mapped3.Bits {
		if v != dcb {
			t.Fatalf("all zero wordMask idx %d want DCB got %d", i, v)
		}
	}
	// Test localSlot bit check
	mapped4 := buildMappedInto(nil, pic, []uint16{2, 2, 2, 2}, byteGrid, 2, 2, 0, dcb, nil) // bit0 not set, bit1 set
	for _, v := range mapped4.Bits {
		if v != dcb {
			t.Fatalf("localSlot 0 bit not set should be DCB got %d", v)
		}
	}
	mapped5 := buildMappedInto(nil, pic, []uint16{2, 2, 2, 2}, byteGrid, 2, 2, 1, dcb, nil) // slot1 -> bit 2 -> should see raw/fog
	if mapped5.Bits[0] == dcb {
		t.Fatalf("slot1 should see")
	}
}

func TestMinimapRebuildFinalLayerOrderAndBlink(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10, PadX: 0, PadY: 0}
	mapped := &RadarSurface{W: 10, H: 10, Pitch: 12, Bits: make([]byte, 100)}
	for i := range mapped.Bits {
		mapped.Bits[i] = 1
	}
	playW := int32(100)
	playH := int32(100)
	// Two contacts at same radar pixel, later should overwrite earlier (pool order ascending slice order is caller-stable) [03 §3.9]
	contacts := []MinimapContact{
		{WorldX: 50, WorldZ: 50, WorldY: 0, Owner: 0, Palette: 10, Hovered: false},
		{WorldX: 50, WorldZ: 50, WorldY: 0, Owner: 0, Palette: 20, Hovered: false},
	}
	blink := BlinkState{Phase: 1}
	blit := func(dst *RadarSurface, x, y int, color byte, hovered bool) {
		dst.Set(x, y, color)
		if hovered {
			dst.Set(x+1, y, color)
		}
	}
	final := rebuildFinalExactInto(nil, mapped, m, playW, playH, contacts, blink, blit, 0xA0, 0xB0, 0xC0)
	if final == nil {
		t.Fatalf("final nil")
	}
	rx, ry := RadarProjection(50, 50, 0, playW, playH, m)
	idx := int(ry)*final.W + int(rx)
	if idx < 0 || idx >= len(final.Bits) {
		t.Fatalf("rx,ry OOB %d,%d", rx, ry)
	}
	if got := final.Bits[idx]; got != 20 {
		t.Fatalf("layer order later overwrites earlier: got %d want 20 [03 §3.9]", got)
	}
	// The hover ring draws on top of the blip at the same location plus a second pixel
	contacts2 := []MinimapContact{
		{WorldX: 20, WorldZ: 20, WorldY: 0, Palette: 10, Hovered: false},
		{WorldX: 20, WorldZ: 20, WorldY: 0, Palette: 30, Hovered: true},
	}
	final2 := rebuildFinalExactInto(nil, mapped, m, playW, playH, contacts2, blink, blit, 0xA0, 0xB0, 0xC0)
	rx2, ry2 := RadarProjection(20, 20, 0, playW, playH, m)
	if v, _ := final2.At(int(rx2), int(ry2)); v != 30 {
		t.Fatalf("hover ring should overwrite blip at same pixel, got %d want 30", v)
	}
	// Check second pixel offset exists
	if v, ok := final2.At(int(rx2+1), int(ry2)); !ok || v != 30 {
		t.Fatalf("hover ring second pixel not drawn")
	}
	// Circles overwrite blip: create contact with circle radius
	// Circles reach layer 4 only through the selected-unit circle gate
	// [03 §3.9] "Selected-unit circle gate correction".
	contacts3 := []MinimapContact{
		{WorldX: 50, WorldZ: 50, WorldY: 0, Palette: 10, RawDistRadar: 20, RangeStatus: true}, // outer radius ~ 10*20/100=2
	}
	final3 := rebuildFinalExactInto(nil, mapped, m, playW, playH, contacts3, blink, blit, 0xA0, 0xB0, 0xC0)
	// circle radius 2 should overwrite blip at offset: blip at rx,ry, circle outline at rx+2,ry should be circle color (0xA0 placeholder)
	rx3, ry3 := RadarProjection(50, 50, 0, playW, playH, m)
	r := RadarRadius(20, m.W, playW)
	if r == 0 {
		t.Fatalf("radius zero")
	}
	cx := int(rx3) + int(r)
	cy := int(ry3)
	if v, _ := final3.At(cx, cy); v != 0xA0 {
		t.Fatalf("circle should overwrite, at %d,%d got %d want 0xA0 [03 §3.9] radius %d", cx, cy, v, r)
	}
	// Ensure wipe: mapped bits 1 should be present where no blip/circle
	if v, _ := final3.At(0, 0); v != 1 {
		t.Fatalf("wipe from MAPPED via copy [03 §3.9] at 0,0 got %d want 1", v)
	}
}

// TestMinimapSelectedUnitCircleGate replaces TestMinimapNoRadarCircleGate,
// retired 2026-08-30.
//
// What the retired test locked: that MinimapContact.NoRadar suppressed a
// contact's sensor circles, and that setting Stealth overrode it so a
// "stealthed no-radar" unit drew its circles again. Both halves are wrong.
// [03 §3.9] "Selected-unit circle gate correction" (Established) says in terms
// that the previous "no-radar" label was wrong, that the unit parser loads no
// unit `noradar` key at all, and that "the cloak/hidden instance bit and the
// definition `stealth` flag are not this callback gate". The real gate is the
// selected/range-status bit, and then active OR not on/off-capable.
//
// The three observable outcomes of that gate, which is what this test locks:
// a unit the viewer can see but has not selected draws no circle; a selected
// on/off-capable unit that is inactive draws none; a selected unit that passes
// draws one. The producer folds the active/onoffable term into RangeStatus, so
// the first two arrive here as RangeStatus == false.
func TestMinimapSelectedUnitCircleGate(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10}
	playW, playH := int32(100), int32(100)
	blit := func(dst *RadarSurface, x, y int, color byte, hovered bool) {
		dst.Set(x, y, color)
	}
	base := MinimapContact{
		WorldX: 50, WorldZ: 50, Visible: true, Palette: 9,
		RawDistRadar: 20,
	}
	centerX, centerY := RadarProjection(base.WorldX, base.WorldZ, 0, playW, playH, m)
	outerX := centerX + RadarRadius(base.RawDistRadar, m.W, playW)

	// Gate closed: the blip still draws, the circle does not. This is the
	// visible enemy the playtest reported, and the viewer's own unselected
	// tower, which reach this layer identically.
	mapped := &RadarSurface{W: 10, H: 10, Bits: make([]byte, 100)}
	closed := base
	final := rebuildFinalExactInto(nil, mapped, m, playW, playH, []MinimapContact{closed}, BlinkState{Phase: 1}, blit, 7, 8, 9)
	if got, _ := final.At(int(centerX), int(centerY)); got != 9 {
		t.Fatalf("the circle gate must not suppress the blip: got %d want 9 [03 §3.9]", got)
	}
	if got, _ := final.At(int(outerX), int(centerY)); got != 0 {
		t.Fatalf("an unselected unit drew a circle: got %d want none [03 §3.9]", got)
	}

	// Stealth is not a term of this gate: it cannot reopen it.
	stealthed := base
	stealthed.Stealth = true
	final = rebuildFinalExactInto(nil, mapped, m, playW, playH, []MinimapContact{stealthed}, BlinkState{Phase: 1}, blit, 7, 8, 9)
	if got, _ := final.At(int(outerX), int(centerY)); got != 0 {
		t.Fatalf("stealth reopened the selected-unit circle gate: got %d want none [03 §3.9]", got)
	}

	// Gate open: the selected unit draws exactly one outer circle in the radar
	// index, over its own blip.
	open := base
	open.RangeStatus = true
	final = rebuildFinalExactInto(nil, mapped, m, playW, playH, []MinimapContact{open}, BlinkState{Phase: 1}, blit, 7, 8, 9)
	if got, _ := final.At(int(outerX), int(centerY)); got != 7 {
		t.Fatalf("a selected unit drew no circle: got %d want the radar index 7 [03 §3.9][03 §3.10]", got)
	}

	// The per-unit blink countdown suppresses the blip but not the circle:
	// [03 §3.9] "Contact layering and ring-only cases" establishes that a unit
	// can appear ring-only while its range branches still run.
	ringOnly := open
	ringOnly.BlinkSuppress = 1
	final = rebuildFinalExactInto(nil, mapped, m, playW, playH, []MinimapContact{ringOnly}, BlinkState{Phase: 0}, blit, 7, 8, 9)
	if got, _ := final.At(int(centerX), int(centerY)); got == 9 {
		t.Fatalf("a blink-suppressed contact drew its blip on a non-blink phase [03 §3.9]")
	}
	if got, _ := final.At(int(outerX), int(centerY)); got != 7 {
		t.Fatalf("a blink-suppressed contact lost its circle: got %d want 7 [03 §3.9 ring-only]", got)
	}
}

// TestMinimapWeaponRingIsGatedOnSelection locks the layer-5 gate: the ring
// loop sits under the unit's selected bit (status bit 4), like the sensor
// circles, so a detected enemy is never ringed [03 R-MM-01 §3 "rings are gated
// on selection"]. Before this the loop ran for any admitted contact carrying
// the ring flag.
func TestMinimapWeaponRingIsGatedOnSelection(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10}
	playW, playH := int32(100), int32(100)
	blit := func(dst *RadarSurface, x, y int, color byte, hovered bool) { dst.Set(x, y, color) }
	base := MinimapContact{
		WorldX: 50, WorldZ: 50, Visible: true, Palette: 9,
		RingEnabled: true, RingRange: 20,
	}
	centerX, centerY := RadarProjection(base.WorldX, base.WorldZ, 0, playW, playH, m)
	ringX := centerX + RadarRadius(base.RingRange, m.W, playW)

	mapped := &RadarSurface{W: 10, H: 10, Bits: make([]byte, 100)}
	final := rebuildFinalExactInto(nil, mapped, m, playW, playH, []MinimapContact{base}, BlinkState{Phase: 1}, blit, 7, 8, 9)
	if got, _ := final.At(int(ringX), int(centerY)); got != 0 {
		t.Fatalf("an unselected contact drew a weapon ring: got %d want none [03 R-MM-01 §3]", got)
	}

	selected := base
	selected.Status = 0x10
	final = rebuildFinalExactInto(nil, mapped, m, playW, playH, []MinimapContact{selected}, BlinkState{Phase: 1}, blit, 7, 8, 9)
	if got, _ := final.At(int(ringX), int(centerY)); got != 9 {
		t.Fatalf("a selected contact drew no weapon ring: got %d want the ring index 9 [03 R-MM-01 §3]", got)
	}

	// The ring loop is independent of the circles' activation term: a selected
	// unit with RangeStatus false still rings.
	if selected.RangeStatus {
		t.Fatal("fixture error: the ring case must not carry the circle gate")
	}
}

// The blip layer's blink term is the unit's per-instance blink-suppress
// countdown against the shared blink phase, and nothing else [03 §3.9] layer 2.
//
// There is no stealth term in the blip gate (an earlier form of this test
// asserted that a contact carrying the definition `stealth` flag blinked) — the
// gate is the four-disjunct visibility test and then `blinkSuppress == 0 ||
// blinkPhase`. Stealth belongs to the sensor phase's contact callback
// [03 R-VIS-01 §5], where it suppresses the radar and sonar detection that
// would have written the seen bit; a stealthy unit that reaches this layer at
// all was admitted by line of sight and draws a steady blip.
func TestMinimapBlinkGate(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10}
	mapped := &RadarSurface{W: 10, H: 10, Bits: make([]byte, 100)}
	for i := range mapped.Bits {
		mapped.Bits[i] = 5
	}
	blit := func(dst *RadarSurface, x, y int, color byte, hovered bool) {
		dst.Set(x, y, color)
	}
	rx, ry := RadarProjection(10, 10, 0, 100, 100, m)

	// A running blink-suppress countdown hides the blip off-phase and shows it
	// on-phase.
	suppressed := []MinimapContact{{WorldX: 10, WorldZ: 10, WorldY: 0, Palette: 9, Visible: true, BlinkSuppress: 3}}
	finalOff := rebuildFinalExactInto(nil, mapped, m, 100, 100, suppressed, BlinkState{Phase: 0}, blit, 0xA0, 0xB0, 0xC0)
	if v, _ := finalOff.At(int(rx), int(ry)); v != 5 {
		t.Fatalf("blink-suppressed blip drawn on the clear phase, got %d want mapped 5 [03 §3.9]", v)
	}
	finalOn := rebuildFinalExactInto(nil, mapped, m, 100, 100, suppressed, BlinkState{Phase: 1}, blit, 0xA0, 0xB0, 0xC0)
	if v, _ := finalOn.At(int(rx), int(ry)); v != 9 {
		t.Fatalf("blink-suppressed blip missing on the blink phase, got %d want 9 [03 §3.9]", v)
	}

	// Definition stealth alone never blinks a blip.
	stealthy := []MinimapContact{{WorldX: 10, WorldZ: 10, WorldY: 0, Palette: 9, Visible: true, Stealth: true}}
	steady := rebuildFinalExactInto(nil, mapped, m, 100, 100, stealthy, BlinkState{Phase: 0}, blit, 0xA0, 0xB0, 0xC0)
	if v, _ := steady.At(int(rx), int(ry)); v != 9 {
		t.Fatalf("a stealthy contact blinked, got %d want the steady blip 9 [03 §3.9][03 R-VIS-01 §5]", v)
	}
}

func TestMinimapServiceConsumesCommittedBlinkPhase(t *testing.T) {
	s := NewMinimapService(MinimapServiceConfig{})
	s.dirty = 0
	s.SetBlinkPhase(3)
	if got := s.Blink().Phase; got != 1 {
		t.Fatalf("blink phase should normalize to bit 0, got %d", got)
	}
	if s.dirty != MinimapDirtyFinal {
		t.Fatalf("phase change dirty bits = %d, want FINAL only", s.dirty)
	}
	s.dirty = 0
	s.SetBlinkPhase(1)
	if s.dirty != 0 {
		t.Fatalf("equivalent phase change dirtied surfaces: %d", s.dirty)
	}
	s.SetBlinkPhase(2)
	if got := s.Blink().Phase; got != 0 {
		t.Fatalf("blink phase should mask higher bits, got %d", got)
	}
}

func TestMinimapServiceReusesMappedRevisionAndRefreshesFinalRevision(t *testing.T) {
	picture := &RadarSurface{W: 1, H: 1, Pitch: 1, Bits: []byte{7}}
	s := NewMinimapService(MinimapServiceConfig{Picture: picture, MapW: 1, MapH: 1, LocalSlot: 0})
	word := []uint16{1}
	current := []uint8{1}
	if !s.RebuildMappedVersion(word, current, 1, 3) {
		t.Fatal("first mapped build failed")
	}
	// MAPPED is recomposed over its own retained storage, so surface identity is
	// no longer the signal that a rebuild ran; compare the composite. Clearing
	// the explored word replaces the picture byte with the fog fill.
	first := append([]byte(nil), s.mapped.Bits...)
	word[0] = 0
	if !s.RebuildMappedVersion(word, current, 1, 3) || !bytes.Equal(s.mapped.Bits, first) {
		t.Fatal("unchanged mapping revision rebuilt MAPPED")
	}
	if !s.RebuildMappedVersion(word, current, 1, 4) || bytes.Equal(s.mapped.Bits, first) {
		t.Fatal("new mapping revision did not rebuild MAPPED")
	}
	m := camera.Minimap{W: 1, H: 1}
	if !s.RebuildFinalVersion(m, 1, 1, nil, nil, 0, 0, 0, 9) {
		t.Fatal("first final build failed")
	}
	revision := s.FinalRevision()
	if !s.RebuildFinalVersion(m, 1, 1, nil, nil, 0, 0, 0, 9) || s.FinalRevision() != revision {
		t.Fatal("same final input rebuilt FINAL")
	}

	// A replacement publisher may start with the same counter. Both surfaces
	// must refresh even when the host is still presenting the same tick.
	word[0] = 1
	before := append([]byte(nil), s.final.Bits...)
	if !s.RebuildMappedVersion(word, current, 2, 4) || !s.RebuildFinalVersion(m, 1, 1, nil, nil, 0, 0, 0, 9) {
		t.Fatal("replacement source failed to rebuild")
	}
	if s.FinalRevision() <= revision || bytes.Equal(before, s.final.Bits) {
		t.Fatal("replacement source retained stale FINAL pixels at the same tick")
	}
	revision = s.FinalRevision()
	s.SetBlinkPhase(1)
	if !s.RebuildFinalVersion(m, 1, 1, nil, nil, 0, 0, 0, 10) || s.FinalRevision() <= revision {
		t.Fatal("blink input did not refresh FINAL")
	}
}

func TestMinimapPhaseGatesRegularAndDashedPresentation(t *testing.T) {
	m := camera.Minimap{W: 32, H: 32}
	mapped := &RadarSurface{W: 32, H: 32, Bits: make([]byte, 32*32)}
	contacts := []MinimapContact{{
		WorldX: 50, WorldZ: 50, Visible: true, LocalPlayer: 1, Owner: 1,
		BlinkSuppress: 1, Palette: 9,
	}}
	blit := func(dst *RadarSurface, x, y int, color byte, hovered bool) {
		dst.Set(x, y, color)
	}
	rx, ry := RadarProjection(50, 50, 0, 100, 100, m)
	off := rebuildFinalExactInto(nil, mapped, m, 100, 100, contacts, BlinkState{Phase: 0}, blit, 7, 8, 9)
	on := rebuildFinalExactInto(nil, mapped, m, 100, 100, contacts, BlinkState{Phase: 1}, blit, 7, 8, 9)
	if got, _ := off.At(int(rx), int(ry)); got != 0 {
		t.Fatalf("regular suppressed contact phase 0 pixel = %d, want mapped 0", got)
	}
	if got, _ := on.At(int(rx), int(ry)); got != 9 {
		t.Fatalf("regular suppressed contact phase 1 pixel = %d, want blip 9", got)
	}

	phase0 := &RadarSurface{W: 32, H: 32, Bits: make([]byte, 32*32)}
	phase1 := &RadarSurface{W: 32, H: 32, Bits: make([]byte, 32*32)}
	drawDashedCircle(phase0, 16, 16, 8, 5, false)
	drawDashedCircle(phase1, 16, 16, 8, 5, true)
	if string(phase0.Bits) == string(phase1.Bits) {
		t.Fatal("dashed ring parity did not change with committed phase")
	}
}

// TestMinimapBakedUsedRectCropsPaddingOnNonSquareMap is a play-test
// regression fixture (WU-19-133): a tall, thin map's baked TNT minimap uses
// only the left part of the stored bitmap's width, with the rest padded
// [fmt tnt "Minimap"]. This fixture is authored, not retail bytes: a 20x10
// stored bitmap standing in for the shipped 252x252/252x256 canvases, with
// real terrain in columns 0..7 and a solid fill colour (100, matching the
// TNT format's own verified 0x64 pad byte) in columns 8..19.
//
// Before the fix, BuildRadarPicture resampled the whole 20-wide stored
// bitmap into the destination regardless of how much of it was real, so a
// destination column past 8/20 of the way across read the fill colour
// instead of terrain — the reported "blue stripe" on a tall map's minimap.
// The fix (bakedMinimapUsedRect) crops to the used sub-rectangle first, so
// every destination column must land on real terrain.
func TestMinimapBakedUsedRectCropsPaddingOnNonSquareMap(t *testing.T) {
	const (
		bakedW, bakedH = 20, 10
		usedW          = 8 // playW*bakedW/playH = 40*20/100 = 8, truncating
		fill           = byte(100)
	)
	baked := make([]byte, bakedW*bakedH)
	for y := 0; y < bakedH; y++ {
		for x := 0; x < bakedW; x++ {
			if x < usedW {
				baked[y*bakedW+x] = byte(10 + x) // real terrain content, 10..17
			} else {
				baked[y*bakedW+x] = fill // padding outside the used sub-rectangle
			}
		}
	}

	playW, playH := int32(40), int32(100) // a tall, thin map: playW < playH
	layout := camera.LayoutMinimap(playW, playH)
	if layout.W <= 0 || layout.H <= 0 {
		t.Fatalf("layout got %+v", layout)
	}

	tables := identityALP() // out = p00, i.e. nearest sample at (sx,sy) truncated
	pic := BuildRadarPicture(nil, playW, playH, layout, baked, bakedW, bakedH, &tables)
	if pic == nil {
		t.Fatalf("baked picture rejected")
	}

	// The rightmost destination column must still land on real terrain, not
	// the fill colour: every dst x in [0,W) must map into the used
	// sub-rectangle's columns [0,usedW), never into the padding beyond it.
	lastCol := pic.W - 1
	if got := pic.Bits[lastCol]; got == fill {
		t.Fatalf("rightmost minimap column read the TNT pad colour (%d) instead of terrain — used-rect crop not applied", fill)
	}
	if got, want := pic.Bits[lastCol], byte(10+usedW-1); got != want {
		t.Fatalf("rightmost minimap column = %d, want %d (last real terrain column, nearest-sampled)", got, want)
	}

	// No pixel anywhere in the picture may be the pad colour: the crop must
	// remove every fill byte from the resize's source before it runs.
	for i, v := range pic.Bits {
		if v == fill {
			t.Fatalf("picture pixel %d is the TNT pad colour %d; padding leaked into the resample", i, fill)
		}
	}
}

// TestMinimapBakedUsedRectWideMapPadsBottomRows mirrors the crop for a wide
// map, whose real image occupies the top rows with the bottom padded
// [fmt tnt "Minimap"], the mirror image of the tall-map case above.
func TestMinimapBakedUsedRectWideMapPadsBottomRows(t *testing.T) {
	const (
		bakedW, bakedH = 10, 20
		usedH          = 8 // playH*bakedH/playW = 40*20/100 = 8, truncating
		fill           = byte(100)
	)
	baked := make([]byte, bakedW*bakedH)
	for y := 0; y < bakedH; y++ {
		for x := 0; x < bakedW; x++ {
			if y < usedH {
				baked[y*bakedW+x] = byte(10 + y)
			} else {
				baked[y*bakedW+x] = fill
			}
		}
	}

	playW, playH := int32(100), int32(40) // a wide map: playH < playW
	layout := camera.LayoutMinimap(playW, playH)
	if layout.W <= 0 || layout.H <= 0 {
		t.Fatalf("layout got %+v", layout)
	}

	tables := identityALP()
	pic := BuildRadarPicture(nil, playW, playH, layout, baked, bakedW, bakedH, &tables)
	if pic == nil {
		t.Fatalf("baked picture rejected")
	}

	lastRow := pic.H - 1
	idx := lastRow * pic.W
	if got := pic.Bits[idx]; got == fill {
		t.Fatalf("bottom minimap row read the TNT pad colour (%d) instead of terrain — used-rect crop not applied", fill)
	}
	for i, v := range pic.Bits {
		if v == fill {
			t.Fatalf("picture pixel %d is the TNT pad colour %d; padding leaked into the resample", i, fill)
		}
	}
}
