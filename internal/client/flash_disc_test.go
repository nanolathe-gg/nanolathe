package client

import (
	"testing"
)

// TestFlashTableGeometryMatchesTheDrawCensus checks the three calculated
// tables against the one number [06 R-WFX-01 §2] publishes that is sensitive
// to every frame's size: the CRT draw count, one draw per pixel.
//
// 23,456 / 107,335 / 260,815 draws is the sum of n² over each table's frames,
// so a wrong frame count, a wrong starting side or a wrong step shows up here
// immediately. It is the strongest available check on generated art that has
// no reference bitmap to compare against.
func TestFlashTableGeometryMatchesTheDrawCensus(t *testing.T) {
	want := []int{23456, 107335, 260815}
	for table := 0; table < flashTableCount; table++ {
		crt := flashRand{state: 1}
		before := crt.drawn
		frames := buildFlashTable(table, &crt)
		if got := crt.drawn - before; got != want[table] {
			t.Fatalf("table %d spent %d CRT draws, want %d [06 R-WFX-01 §2]", table, got, want[table])
		}
		wantFrames := []int{12, 15, 15}[table]
		if len(frames) != wantFrames {
			t.Fatalf("table %d has %d frames, want %d", table, len(frames), wantFrames)
		}
		wantFirst := []int{64, 128, 200}[table]
		if frames[0].Side != wantFirst {
			t.Fatalf("table %d first frame side %d, want %d", table, frames[0].Side, wantFirst)
		}
		wantLast := []int{20, 30, 46}[table]
		if frames[len(frames)-1].Side != wantLast {
			t.Fatalf("table %d last frame side %d, want %d", table, frames[len(frames)-1].Side, wantLast)
		}
		// The frame's own offsets are both H, which is what centres the disc.
		if frames[0].Offset != frames[0].Side/2 {
			t.Fatalf("table %d frame 0 offset %d, want H = %d", table, frames[0].Offset, frames[0].Side/2)
		}
	}
}

// TestFlashDiscIsAVerticallyCompressedEllipse locks the shape that [03 §4.3.1]
// had backwards. The 1.33 multiplies the ROW term, so the disc reaches further
// horizontally than vertically; the section said the factor was on the x term,
// which would compress the wrong axis, and it also denied the division by H
// that the arithmetic performs.
func TestFlashDiscIsAVerticallyCompressedEllipse(t *testing.T) {
	crt := flashRand{state: 7}
	d := buildFlashDisc(64, &crt)
	h := d.Offset

	opaque := func(x, y int) bool {
		if x < 0 || y < 0 || x >= d.Side || y >= d.Side {
			return false
		}
		return d.Pixels[y*d.Side+x] != flashTransparent
	}
	reach := func(dx, dy int) int {
		n := 0
		for k := 1; k < d.Side; k++ {
			if !opaque(h+dx*k, h+dy*k) {
				break
			}
			n++
		}
		return n
	}
	horizontal, vertical := reach(1, 0), reach(0, 1)
	if horizontal <= vertical {
		t.Fatalf("disc reaches %d horizontally and %d vertically; the 1.33 belongs on the ROW term, so it must be wider than tall", horizontal, vertical)
	}
	// sqrt(1.33) ≈ 1.153, and the per-pixel edge jitter is worth up to nine
	// thirty-seconds of the radius, so bound the ratio rather than pin it.
	if horizontal > vertical*2 {
		t.Fatalf("disc reach %d/%d is far past the sqrt(1.33) ellipse the arithmetic describes", horizontal, vertical)
	}

	// The centre sits near the top of the ramp. It is not pinned to 0x6E: at
	// the centre the distance term is zero, so the whole of v comes from the
	// per-pixel draw r (0..9) and the centre index varies over the top of the
	// ramp with it — which is exactly the fuzz the draw exists to add.
	centre := d.Pixels[h*d.Side+h]
	if centre == flashTransparent || centre < flashRingIndex-10 {
		t.Fatalf("disc centre index %#x, want the top of the 0x4F..0x6E ramp", centre)
	}
	if got := d.Pixels[0]; got != flashTransparent {
		t.Fatalf("disc corner index %#x, want transparent", got)
	}
	// Every opaque byte lies on the authored ramp.
	for i, p := range d.Pixels {
		if p == flashTransparent {
			continue
		}
		if p < 0x4F || p > flashRingIndex {
			t.Fatalf("pixel %d index %#x is off the 0x4F..0x6E ramp", i, p)
		}
	}
}
