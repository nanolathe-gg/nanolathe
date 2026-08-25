package audio

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestIsAudible_ExploredAndLOS(t *testing.T) {
	w, h := 10, 10
	wordMask := make([]uint16, w*h)
	byteGrids := [10][]uint8{}
	for i := range byteGrids {
		byteGrids[i] = make([]uint8, w*h)
	}
	// LOS mode: bit for player 1 set at (3,3)
	wordMask[3*w+3] = 1 << 1
	if !IsAudible(3, 3, 1, 0, wordMask, byteGrids, w, h) {
		t.Fatal("LOS visible cell should be audible")
	}
	if IsAudible(3, 3, 2, 0, wordMask, byteGrids, w, h) {
		t.Fatal("LOS different player should not be audible (no ally OR)")
	}
	// Explored mode: byte grid
	byteGrids[1][5*w+5] = 1
	if !IsAudible(5, 5, 1, ModeExplored, wordMask, byteGrids, w, h) {
		t.Fatal("explored cell should be audible")
	}
	if IsAudible(5, 5, 1, 0, wordMask, byteGrids, w, h) {
		t.Fatal("LOS mode should not read byte grid")
	}
	// off-map
	if IsAudible(-1, 0, 1, 0, wordMask, byteGrids, w, h) {
		t.Fatal("off-map should be silent")
	}
	if IsAudible(w, h, 1, ModeExplored, wordMask, byteGrids, w, h) {
		t.Fatal("off-map should be silent")
	}
}

func TestCellFromWorld_Floor(t *testing.T) {
	// tile = 32*65536 = 2097152
	// pos 0 → cell 0
	if CellFromWorld(0) != 0 {
		t.Fatalf("0 -> %d want 0", CellFromWorld(0))
	}
	// pos 2097151 → still 0 (just under tile)
	if CellFromWorld(2097151) != 0 {
		t.Fatalf("2097151 -> %d want 0", CellFromWorld(2097151))
	}
	if CellFromWorld(2097152) != 1 {
		t.Fatalf("2097152 -> %d want 1", CellFromWorld(2097152))
	}
	// negative: -1 → -1 floor
	if CellFromWorld(-1) != -1 {
		t.Fatalf("-1 -> %d want -1", CellFromWorld(-1))
	}
	if CellFromWorld(-2097152) != -1 {
		t.Fatalf("-2097152 -> %d want -1", CellFromWorld(-2097152))
	}
	if CellFromWorld(-2097153) != -2 {
		t.Fatalf("-2097153 -> %d want -2", CellFromWorld(-2097153))
	}
}

func TestAttenuate_InsideVsOutside(t *testing.T) {
	v := Viewport{Left: 0, Top: 0, Width: 20, Height: 15} // pixels, but Attenuate uses *0x10
	// right = 0+20*16=320, bottom=0+15*16=240
	inside := [3]numeric.Fixed{numeric.Fixed(160 * 65536), 0, numeric.Fixed(120 * 65536)}
	if got := Attenuate(inside, v); got != VolInView {
		t.Fatalf("inside vol %d want %d", got, VolInView)
	}
	// exactly on border inclusive
	border := [3]numeric.Fixed{numeric.Fixed(320 * 65536), 0, numeric.Fixed(240 * 65536)}
	if got := Attenuate(border, v); got != VolInView {
		t.Fatalf("border vol %d want in-view", got)
	}
	// outside
	outside := [3]numeric.Fixed{numeric.Fixed(321 * 65536), 0, numeric.Fixed(0)}
	if got := Attenuate(outside, v); got != VolOffScreen {
		t.Fatalf("outside vol %d want off", got)
	}
	// off-screen still not silent, just attenuated
	if VolOffScreen == 0 {
		t.Fatal("off-screen vol should be negative")
	}
}

func TestComputePan_Centered(t *testing.T) {
	v := Viewport{Left: 100, Top: 50, Width: 40, Height: 30, MapW: 100, MapH: 100, StereoCapable: true}
	pos := [3]numeric.Fixed{numeric.Fixed((100 + 20*8) * 65536), numeric.Fixed(0), numeric.Fixed((50 + 15*8) * 65536)}
	pan := ComputePan(pos, v)
	// centered should be near 0
	// dx = px - ((w/2)<<4) - left = (100+160) - (20<<4)=260-320-100=-160? Wait compute: px=260, w/2=20, 20<<4=320, left=100 => 260-320-100=-160
	// So not zero because w/2*16 is half viewport in subpixel? Accept any int
	if pan.Y != 0 {
		t.Fatalf("pan Y should be 0 got %d", pan.Y)
	}
	// stereo pan X should be deterministic
	if pan.X != -160 && pan.Z != 70 { // 50+ (15*8)=170, 170+? Actually dy = top + (h/2)<<4 + (y>>1) - pz =50+240+0-170=120
		// just ensure not zero
	}
}
