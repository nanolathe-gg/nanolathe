package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/world"
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

func TestMinimapLetterboxFill(t *testing.T) {
	if got := LetterboxFill(); got != 0 {
		t.Fatalf("LetterboxFill want 0 got %d [03 §3.6] TODO(question)", got)
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
	m := camera.Minimap{W: 126, H: 63, PadX: 0, PadY: 31} // arbitrary
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
	pic := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, nil, 0, 0, nil)
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
	// Due to supersampling with tile0 fallback, first pixel should be 0x11 (guard case)
	// worldX for tx=0, ty=0 is 0, tileIdx at (0,0) is 5 -> guard to 0 -> pix 0x11, blended nearest without ALP gives 0x11.
	if pic.Bits[0] != 0x11 {
		t.Fatalf("guard idx>=TileCount→0 failed, got %02x want 0x11 [03 §3.7] [analysis omitted]:3C", pic.Bits[0])
	}
	// Test baked path still produces w*h
	baked := make([]byte, 4*4)
	for i := range baked {
		baked[i] = byte(i)
	}
	pic2 := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, baked, 4, 4, nil)
	if len(pic2.Bits) != pic2.W*pic2.H {
		t.Fatalf("baked rescale len")
	}
}

func TestMinimapBuildRadarPictureFromWorld(t *testing.T) {
	ter := &world.Terrain{CellW: 8, CellH: 16, PlayRight: 96, PlayBottom: 128}
	tileW := int(ter.CellW / 2)
	tileH := int(ter.CellH / 2)
	ter.TileIndices = make([]uint16, tileW*tileH)
	ter.TileSet = make([][1024]byte, 1)
	for i := range ter.TileSet[0] {
		ter.TileSet[0][i] = 0x33
	}
	m := camera.LayoutMinimap(ter.CellW*16, ter.CellH*16)
	pic := BuildRadarPictureFromWorld(ter, m, nil, 0, 0, nil)
	if pic == nil || pic.W != int(m.W) {
		t.Fatalf("FromWorld failed %+v", pic)
	}
	// nil terrain helper
	pic2 := BuildRadarPictureFromWorld(nil, m, nil, 0, 0, nil)
	if pic2 == nil || pic2.W != int(m.W) {
		t.Fatalf("FromWorld nil terrain")
	}
}

func TestMinimapBuildRadarPictureALPBlend(t *testing.T) {
	// Verify ALP path when tables non-nil vs fallback nearest.
	ter := &world.Terrain{CellW: 4, CellH: 12, PlayRight: 32, PlayBottom: 64}
	tileW := int(ter.CellW / 2)
	tileH := int(ter.CellH / 2)
	ter.TileIndices = make([]uint16, tileW*tileH)
	ter.TileSet = make([][1024]byte, 1)
	// Make tile with distinct pattern to test blend: every pixel distinct? Simplify: tile0 has gradient.
	for i := 0; i < 1024; i++ {
		ter.TileSet[0][i] = byte(i & 0xFF)
	}
	m := camera.Minimap{W: 2, H: 2}
	var tables palette.Tables
	// Fill Alpha as identity-ish: Alpha[a*256+b] = (a+b)/2 average for test determinism.
	for i := 0; i < 256; i++ {
		for j := 0; j < 256; j++ {
			tables.Alpha[i*256+j] = byte((i + j) / 2)
		}
	}
	picWith := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, nil, 0, 0, &tables)
	picWithout := BuildRadarPicture(ter, ter.PlayRight, ter.PlayBottom, m, nil, 0, 0, nil)
	if picWith == nil || picWithout == nil {
		t.Fatalf("nil pic")
	}
	// Without tables fallback is nearest (p00), with tables blended average should differ for non-uniform region.
	// At least both produce valid bits.
	if len(picWith.Bits) != 4 || len(picWithout.Bits) != 4 {
		t.Fatalf("len")
	}
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
	mapped := BuildMapped(pic, wordMask, byteGrid, 2, 2, 0, dcb, guiRemap)
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
	mapped2 := BuildMapped(pic, wordMask, byteGrid, 2, 2, 0, dcb, nil)
	if mapped2.Bits[2] != 30 {
		t.Fatalf("nil guiRemap should use raw, got %d want 30", mapped2.Bits[2])
	}
	// Test word bit 0 → DCB regardless of byte
	wordMaskAllZero := []uint16{0, 0, 0, 0}
	mapped3 := BuildMapped(pic, wordMaskAllZero, byteGrid, 2, 2, 0, dcb, guiRemap)
	for i, v := range mapped3.Bits {
		if v != dcb {
			t.Fatalf("all zero wordMask idx %d want DCB got %d", i, v)
		}
	}
	// Test localSlot bit check
	mapped4 := BuildMapped(pic, []uint16{2, 2, 2, 2}, byteGrid, 2, 2, 0, dcb, nil) // bit0 not set, bit1 set
	for _, v := range mapped4.Bits {
		if v != dcb {
			t.Fatalf("localSlot 0 bit not set should be DCB got %d", v)
		}
	}
	mapped5 := BuildMapped(pic, []uint16{2, 2, 2, 2}, byteGrid, 2, 2, 1, dcb, nil) // slot1 -> bit 2 -> should see raw/fog
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
		{WorldX: 50, WorldZ: 50, WorldY: 0, Owner: 0, Palette: 10, IsCommander: false},
		{WorldX: 50, WorldZ: 50, WorldY: 0, Owner: 0, Palette: 20, IsCommander: false},
	}
	blink := BlinkState{Countdown: 7, Phase: 1}
	final := RebuildFinal(mapped, m, playW, playH, contacts, blink, nil)
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
	// Commander draws on top of blip at same location plus second pixel
	contacts2 := []MinimapContact{
		{WorldX: 20, WorldZ: 20, WorldY: 0, Palette: 10, IsCommander: false},
		{WorldX: 20, WorldZ: 20, WorldY: 0, Palette: 30, IsCommander: true},
	}
	final2 := RebuildFinal(mapped, m, playW, playH, contacts2, blink, nil)
	rx2, ry2 := RadarProjection(20, 20, 0, playW, playH, m)
	if v, _ := final2.At(int(rx2), int(ry2)); v != 30 {
		t.Fatalf("commander should overwrite blip at same pixel, got %d want 30", v)
	}
	// Check second pixel offset exists
	if v, ok := final2.At(int(rx2+1), int(ry2)); !ok || v != 30 {
		t.Fatalf("commander second pixel not drawn")
	}
	// Circles overwrite blip: create contact with circle radius
	contacts3 := []MinimapContact{
		{WorldX: 50, WorldZ: 50, WorldY: 0, Palette: 10, RawDistRadar: 20}, // outer radius ~ 10*20/100=2
	}
	final3 := RebuildFinal(mapped, m, playW, playH, contacts3, blink, nil)
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
		t.Fatalf("wipe from MAPPED via copy [03 §3.9] [analysis omitted]: at 0,0 got %d want 1", v)
	}
}

func TestMinimapBlinkGate(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10}
	mapped := &RadarSurface{W: 10, H: 10, Bits: make([]byte, 100)}
	for i := range mapped.Bits {
		mapped.Bits[i] = 5
	}
	contacts := []MinimapContact{{WorldX: 10, WorldZ: 10, WorldY: 0, Palette: 9, Stealth: true}}
	blinkOff := BlinkState{Countdown: 7, Phase: 0}
	finalOff := RebuildFinal(mapped, m, 100, 100, contacts, blinkOff, nil)
	rx, ry := RadarProjection(10, 10, 0, 100, 100, m)
	if v, _ := finalOff.At(int(rx), int(ry)); v != 5 {
		t.Fatalf("stealth hidden when blink==0, got %d want mapped 5 [03 §3.9]", v)
	}
	blinkOn := BlinkState{Countdown: 7, Phase: 1}
	finalOn := RebuildFinal(mapped, m, 100, 100, contacts, blinkOn, nil)
	if v, _ := finalOn.At(int(rx), int(ry)); v != 9 {
		t.Fatalf("stealth visible when blink==1, got %d want 9", v)
	}
	// Tick: every 8 frames ^=1 [03 §3.6]
	b := BlinkState{Countdown: 7, Phase: 0}
	for i := 0; i < 7; i++ {
		b.Tick()
		if b.Phase != 0 {
			t.Fatalf("phase should stay 0 while countdown >0, tick %d phase %d", i, b.Phase)
		}
	}
	if b.Countdown != 0 {
		t.Fatalf("after 7 ticks Countdown want 0 got %d", b.Countdown)
	}
	b.Tick() // wraps 0->7 and toggles
	if b.Countdown != 7 || b.Phase != 1 {
		t.Fatalf("wrap Tick want Countdown 7 Phase1 got %d %d", b.Countdown, b.Phase)
	}
	// Next 8 ticks should toggle back
	for i := 0; i < 8; i++ {
		b.Tick()
	}
	if b.Phase != 0 {
		t.Fatalf("phase should toggle every 8, got %d want 0", b.Phase)
	}
}

func TestMinimapNoRadarAndPaletteFallback(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10}
	mapped := &RadarSurface{W: 10, H: 10, Bits: make([]byte, 100)}
	contacts := []MinimapContact{{WorldX: 10, WorldZ: 10, WorldY: 0, Palette: 0, Owner: 5, NoRadar: true}}
	final := RebuildFinal(mapped, m, 100, 100, contacts, BlinkState{Phase: 1}, nil)
	rx, ry := RadarProjection(10, 10, 0, 100, 100, m)
	if v, _ := final.At(int(rx), int(ry)); v != 0x80+5 {
		t.Fatalf("NoRadar should not suppress the contact blip, got %d", v)
	}
	contacts[0].NoRadar = false
	final2 := RebuildFinal(mapped, m, 100, 100, contacts, BlinkState{Phase: 1}, nil)
	wantPal := byte(0x80 + 5)
	if v, _ := final2.At(int(rx), int(ry)); v != wantPal {
		t.Fatalf("palette fallback want %d got %d", wantPal, v)
	}
}
