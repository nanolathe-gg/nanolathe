package gpurender

import "testing"

// These are user-authored modern presentation policies, not retail contracts
// (GPU design §25.2). In particular damage must not create waves for tiny art.
func TestDynamicBlastAdmissionAndIndependentControls(t *testing.T) {
	for _, tc := range []struct {
		size         float32
		area, damage int32
	}{{19, 950, 30000}, {47, 100, 200}, {51, 31, 100}, {51, 48, 79}} {
		if _, _, s := dynamicBlastShape(4, tc.size, 1, tc.area, tc.damage, true); s != 0 {
			t.Fatalf("tiny hit admitted: %+v", tc)
		}
	}
	if _, _, s := dynamicBlastShape(4, 48, 1, 32, 80, true); s <= 0 {
		t.Fatal("threshold impact omitted")
	}
	r0, w0, s0 := dynamicBlastShape(4, 66, 1, 48, 80, true)
	r1, w1, s1 := dynamicBlastShape(4, 66, 1, 256, 80, true)
	r2, w2, s2 := dynamicBlastShape(4, 66, 1, 48, 1200, true)
	if r1 <= r0 || s1 != s0 || w1 != w0 {
		t.Fatal("area must boost radius independently")
	}
	if s2 <= s0 || r2 != r0 || w2 != w0 {
		t.Fatal("damage must boost strength independently")
	}
	for _, size := range []float32{64, 66, 76, 126, 128, 252, 425} {
		for _, age := range []float32{-1, 0, 0.5, 1, 4, 14.5, 15} {
			oldR, oldW, oldS := blastShape(age, size, 1)
			r, w, s := dynamicBlastShape(age, size, 1, 400, 1800, true)
			if r < oldR || w < oldW || s < oldS {
				t.Fatalf("existing wave shrank: size=%v age=%v", size, age)
			}
			if size >= 128 && (r != oldR || w != oldW || s != oldS) {
				t.Fatal("special explosion changed")
			}
			r, w, s = dynamicBlastShape(age, size, 1, 400, 1800, false)
			if r != oldR || w != oldW || s != oldS {
				t.Fatal("unknown profile lost artwork fallback")
			}
		}
	}
	r, w, s := dynamicBlastShape(4, 66, 2, 110, 350, true)
	a, b, c := dynamicBlastShape(4, 66, 1, 110, 350, true)
	if r != a*2 || w != b*2 || s != c*2 {
		t.Fatal("record scale changed profile")
	}
	r, w, s = dynamicBlastShape(4, 66, 1, 1<<30, 1<<30, true)
	a, b, c = dynamicBlastShape(4, 66, 1, 256, 1200, true)
	if r != a || w != b || s != c {
		t.Fatal("boosts did not saturate")
	}
}
