package aikit

import "testing"

// The reference implementation's demo seeds pcg32_srandom_r(42, 54); its
// first outputs are the published known answers for PCG-XSH-RR 64/32.
func TestRandPCG32KnownAnswer(t *testing.T) {
	r := NewRand(42, 54)
	want := []uint32{0xa15c02b7, 0x7b47f409, 0xba1d3330, 0x83d2f293, 0xbfa4784b, 0xcbed606e}
	for i, w := range want {
		if got := r.Uint32(); got != w {
			t.Fatalf("draw %d = %#x, want %#x", i, got, w)
		}
	}
}

// Players in one battle draw unrelated sequences; the same seed and slot
// replay the same sequence.
func TestPlayerRandStreams(t *testing.T) {
	a, b, c := PlayerRand(7, 0), PlayerRand(7, 1), PlayerRand(7, 0)
	same := 0
	for i := 0; i < 64; i++ {
		x, y, z := a.Uint32(), b.Uint32(), c.Uint32()
		if x != z {
			t.Fatalf("draw %d: same seed and slot diverged", i)
		}
		if x == y {
			same++
		}
	}
	if same > 1 {
		t.Errorf("slots 0 and 1 agreed on %d of 64 draws", same)
	}
}

func TestRandHelpers(t *testing.T) {
	r := NewRand(1, 1)
	var hist [5]int
	for i := 0; i < 5000; i++ {
		v := r.Intn(5)
		if v < 0 || v >= 5 {
			t.Fatalf("Intn(5) = %d", v)
		}
		hist[v]++
		if x := r.Range(-3, 3); x < -3 || x > 3 {
			t.Fatalf("Range(-3,3) = %d", x)
		}
	}
	for i, n := range hist {
		if n < 850 || n > 1150 {
			t.Errorf("Intn(5) bucket %d drew %d of 5000", i, n)
		}
	}
	before := r
	if r.Intn(0) != 0 || r.Range(4, 4) != 4 || r.Chance(0) || !r.Chance(1000) || r.Pick([]int32{0, -1}) != -1 {
		t.Error("degenerate helper results")
	}
	if r != before {
		t.Error("degenerate helpers advanced the generator")
	}
	var picks [3]int
	for i := 0; i < 3000; i++ {
		picks[r.Pick([]int32{1, 0, 2})]++
	}
	if picks[1] != 0 || picks[0] < 850 || picks[0] > 1150 {
		t.Errorf("Pick(1,0,2) = %v", picks)
	}
}

// Consecutive battle seeds must not clump a player's first draw: the style
// a brain draws at its first think would otherwise repeat across nearby
// seeds. Windows of 24 seeds × 2 slots over Intn(6) average a chi-square
// near its 5 degrees of freedom (unhashed PCG seeding averaged about 9.5).
func TestPlayerRandConsecutiveSeeds(t *testing.T) {
	var chi float64
	windows := 0
	for base := uint32(1); base < 12000; base += 24 {
		var h [6]float64
		for s := base; s < base+24; s++ {
			for slot := uint8(0); slot < 2; slot++ {
				r := PlayerRand(s, slot)
				h[r.Intn(6)]++
			}
		}
		for _, c := range h {
			chi += (c - 8) * (c - 8) / 8
		}
		windows++
	}
	if mean := chi / float64(windows); mean > 6.5 {
		t.Errorf("first draws over consecutive seeds clump: mean window chi-square %.2f", mean)
	}
}
